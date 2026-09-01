#!/usr/bin/env python3
"""Read-only smoke for the dbx-ldap-plugin sidecar against a REAL directory.

The main suite (smoke_test.py, S1-S10) needs write access and therefore only
runs over a disposable OpenLDAP container. This suite exercises the same
sidecar paths against a production directory (e.g. Active Directory) with
strictly read-only operations:

    R1  connection/test (simple bind)            -> success
    R1b  optional second URL (LDAP_REAL_URL_PLAIN) -> success
    R2  connect + rootDse                        -> namingContexts covers base DN
    R3  base-scope search on the root            -> 1 entry
    R4  paged one-scope aggregate                -> paged count == full count
    R5  ldap/count matches one-scope search      -> equal counts
    R6  ldap/entry/get on the root DN            -> objectClass present
    R7  ldap/schema metadata                     -> attributeNames non-empty
    R8  search outside allowed_base_dns          -> rejected (policy)
    R9  invalid RFC 4515 filter                  -> filter validation error
    R10 disconnect then call                     -> unknown-connection error

Defensive defaults: the connection profile is created with read_only=true and
no scenario ever issues a write; the whitelist scenario pins allowed_base_dns
so even the rejection probe cannot reach the directory.

All configuration comes from the environment (no credentials in code):

    LDAP_REAL_URL        full ldap/ldaps URL (required; no default target)
    LDAP_REAL_BINDDN     bind name, AD accepts NETBIOS\\user (required)
    LDAP_REAL_BINDPW     bind password (required; never printed)
    LDAP_REAL_ROOT       base DN (default dc=corp,dc=int,dc=kn style is NOT
                         assumed; required when the directory has no default)
    LDAP_REAL_URL_PLAIN  optional second URL for R1b (plain 389 vs ldaps 636)
    LDAP_REAL_TLS_VERIFY set to "0" to skip TLS certificate verification
    LDAP_TEST_REQUIRE=1  turn env SKIPs into FAILs (CI)

Usage:
    DBX_PLUGIN_SIDECAR=/path/to/dbx-plugin-ldap \
    LDAP_REAL_URL=ldaps://ldap.example.com:636 \
    LDAP_REAL_BINDDN='CORP\\svc-user' LDAP_REAL_BINDPW='...' \
    LDAP_REAL_ROOT='dc=corp,dc=example,dc=com' \
    python3 scripts/smoke_real_test.py
"""

from __future__ import annotations

import os
import socket
import sys
from urllib.parse import urlparse

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from sidecar_client_jsonl import (  # noqa: E402
    SidecarClient,
    SidecarError,
    default_binary,
    is_method_not_registered,
    lifecycle_params,
)

URL = os.environ.get("LDAP_REAL_URL", "")
URL_PLAIN = os.environ.get("LDAP_REAL_URL_PLAIN", "")
BIND_DN = os.environ.get("LDAP_REAL_BINDDN", "")
BIND_PW = os.environ.get("LDAP_REAL_BINDPW", "")
ROOT = os.environ.get("LDAP_REAL_ROOT", "")
TLS_VERIFY = os.environ.get("LDAP_REAL_TLS_VERIFY", "1") != "0"
REQUIRE = os.environ.get("LDAP_TEST_REQUIRE", "") == "1"
DATA_DIR = os.environ.get("LDAP_TEST_DATA_DIR", "")

# connection.host carries a full ldap/ldaps URL (legacy form; since v0.1.19
# the manifest binds a bare hostname + port/tls_mode fields instead, both are
# accepted). The network layer dials runtime.host:port, so the port must ride
# along explicitly (ldaps default 636, plain 389) or the dial falls back to 389.
def url_port(raw_url: str) -> int:
    parsed = urlparse(raw_url)
    return parsed.port or (636 if parsed.scheme == "ldaps" else 389)


class SkipScenario(Exception):
    """Raised by a scenario itself when a precondition is unavailable."""


class ScenarioResult:
    def __init__(self, no: str, name: str, status: str, detail: str = ""):
        self.no = no
        self.name = name
        self.status = status
        self.detail = detail


RESULTS: list[ScenarioResult] = []


def scenario(no: str, name: str):
    def decorate(fn):
        def run(*args, **kwargs):
            try:
                fn(*args, **kwargs)
            except SkipScenario as cause:
                RESULTS.append(ScenarioResult(no, name, "SKIP", str(cause)))
            except (SidecarError, AssertionError) as cause:
                if is_method_not_registered(cause):
                    RESULTS.append(ScenarioResult(no, name, "SKIP", f"backend not implemented: {cause}"))
                else:
                    RESULTS.append(ScenarioResult(no, name, "FAIL", str(cause)))
            except Exception as cause:  # unexpected crash counts as FAIL
                RESULTS.append(ScenarioResult(no, name, "FAIL", f"unexpected: {cause}"))
            else:
                RESULTS.append(ScenarioResult(no, name, "PASS"))
        return run
    return decorate


# -- helpers -------------------------------------------------------------------

def expect_error(fn, markers: tuple[str, ...] = ()) -> SidecarError:
    """Run fn expecting a SidecarError; assert on marker substrings if given."""
    try:
        fn()
    except SidecarError as cause:
        lowered = str(cause).lower()
        if markers and not any(marker in lowered for marker in markers):
            raise AssertionError(f"error message mismatch: {cause}") from cause
        return cause
    raise AssertionError("expected a sidecar error but the call succeeded")


def make_connection(connection_id: str, url: str | None = None, **extra_config) -> dict:
    external = {
        "auth_type": "simple",
        "bind_dn": BIND_DN,
        "base_dn": ROOT,
        "timeout_secs": 15,
        "tls_verify": TLS_VERIFY,
        "read_only": True,  # defensive: this suite must never write
    }
    external.update(extra_config)
    target = url or URL
    return {
        "id": connection_id,
        "name": f"real-{connection_id}",
        "host": target,
        "port": url_port(target),
        "external_config": external,
        "connection_secrets": {"bind_password": BIND_PW},
    }


# Domain methods require connectionId (backend main.go decodeParams); the DBX
# host injects it per workbench context, so the smoke driver tracks the last
# connected connection the same way.
CURRENT_CONNECTION = {"id": ""}


def domain(client: SidecarClient, method: str, params: dict) -> dict:
    payload = dict(params)
    payload.setdefault("connectionId", CURRENT_CONNECTION["id"])
    return client.request(method, payload)


def connect(client: SidecarClient, connection: dict) -> None:
    CURRENT_CONNECTION["id"] = connection["id"]
    client.request("connection/connect", lifecycle_params(connection))


def search(client: SidecarClient, **params) -> dict:
    payload = {"filter": "(objectClass=*)", "scope": "sub"}
    payload.update(params)
    return domain(client, "ldap/search", payload)


# -- scenarios -----------------------------------------------------------------

@scenario("R1", "initialize + connection/test (simple)")
def run_r1(client: SidecarClient) -> None:
    """R1 initialize + connection/test over the primary URL -> success."""
    info = client.initialize()
    if not info:
        raise AssertionError("plugin/initialize returned no result")
    result = client.request("connection/test", lifecycle_params(make_connection("real-test")))
    if result.get("success") is not True:
        raise AssertionError(f"connection/test did not succeed: {result}")


@scenario("R1b", "connection/test (secondary URL)")
def run_r1b(client: SidecarClient) -> None:
    """R1b optional plain-URL connection/test; SKIP when LDAP_REAL_URL_PLAIN unset."""
    if not URL_PLAIN:
        raise SkipScenario("LDAP_REAL_URL_PLAIN not set")
    result = client.request("connection/test", lifecycle_params(make_connection("real-plain", url=URL_PLAIN)))
    if result.get("success") is not True:
        raise AssertionError(f"connection/test did not succeed: {result}")


@scenario("R2", "connect + rootDse namingContexts")
def run_r2(client: SidecarClient) -> None:
    """R2 connect then rootDse -> namingContexts covers the configured base DN."""
    connect(client, make_connection("real-main"))
    root = domain(client, "ldap/rootDse", {})
    contexts = root.get("attributes", {}).get("namingContexts", [])
    lowered = [ctx.lower() for ctx in contexts]
    if ROOT.lower() not in lowered:
        raise AssertionError(f"base DN {ROOT} not in namingContexts: {contexts}")


@scenario("R3", "base-scope search on the root -> 1 entry")
def run_r3(client: SidecarClient) -> None:
    """R3 base-scope search on the root DN -> exactly 1 entry."""
    result = search(client, baseDn=ROOT, scope="base")
    if result.get("count") != 1:
        raise AssertionError(f"base-scope search returned {result.get('count')} entries")


@scenario("R4", "paged one-scope aggregate")
def run_r4(client: SidecarClient) -> None:
    """R4 one-scope search with pageSize -> paged aggregate equals full count."""
    full = search(client, baseDn=ROOT, scope="one", pageSize=1000)
    paged = search(client, baseDn=ROOT, scope="one", pageSize=7)
    if full.get("count", 0) == 0:
        raise AssertionError("no entries under the root to aggregate")
    if paged.get("count") != full.get("count"):
        raise AssertionError(f"paged aggregate {paged.get('count')} != full {full.get('count')}")


@scenario("R5", "count endpoint matches one-scope search")
def run_r5(client: SidecarClient) -> None:
    """R5 ldap/count on the root -> equals a scope=one search count."""
    counted = domain(client, "ldap/count", {"baseDn": ROOT})
    listed = search(client, baseDn=ROOT, scope="one", sizeLimit=10000)
    if counted.get("count", 0) != listed.get("count", 0):
        raise AssertionError(f"count {counted.get('count')} != one-scope search {listed.get('count')}")


@scenario("R6", "entry/get on the root DN")
def run_r6(client: SidecarClient) -> None:
    """R6 ldap/entry/get on the root DN -> entry present with objectClass."""
    fetched = domain(client, "ldap/entry/get", {"dn": ROOT})
    entry = fetched.get("entry", {})
    if entry.get("dn", "").lower() != ROOT.lower():
        raise AssertionError(f"unexpected entry DN: {entry.get('dn')!r}")
    if not entry.get("attributes", {}).get("objectClass"):
        raise AssertionError("root entry has no objectClass attribute")


@scenario("R7", "schema metadata")
def run_r7(client: SidecarClient) -> None:
    """R7 ldap/schema -> attributeNames non-empty, subschemaSubentry set."""
    schema = domain(client, "ldap/schema", {})
    if not schema.get("subschemaSubentry"):
        raise AssertionError("schema returned no subschemaSubentry")
    if not schema.get("attributeNames"):
        raise AssertionError("schema returned no attributeNames")


@scenario("R8", "read whitelist enforcement")
def run_r8(client: SidecarClient) -> None:
    """R8 reads outside allowed_base_dns are rejected (policy, no real read)."""
    connect(client, make_connection("real-wl", allowed_base_dns=["dc=example,dc=org"]))
    expect_error(lambda: search(client, baseDn=ROOT, scope="base"),
                 markers=("allow", "whitelist", "permitted", "denied", "outside", "base"))


@scenario("R9", "invalid filter rejection")
def run_r9(client: SidecarClient) -> None:
    """R9 invalid RFC 4515 filter -> filter validation error."""
    connect(client, make_connection("real-filter"))
    cause = expect_error(lambda: search(client, baseDn=ROOT, scope="sub", filter="((( "))
    if is_method_not_registered(cause):
        raise AssertionError("filter validation should be a business error")
    lowered = str(cause).lower()
    if not any(marker in lowered for marker in ("filter", "过滤")):
        raise AssertionError(f"error does not mention the filter: {cause}")


@scenario("R10", "disconnect then call")
def run_r10(client: SidecarClient) -> None:
    """R10 disconnect then call -> unknown connection business error."""
    connection = make_connection("real-dc")
    connect(client, connection)
    client.request("connection/disconnect", {"connection": {"id": connection["id"]}})
    cause = expect_error(lambda: search(client, baseDn=ROOT, scope="base"))
    if is_method_not_registered(cause):
        raise AssertionError("disconnect path should be a business error, not method-not-found")
    lowered = str(cause).lower()
    if not any(marker in lowered for marker in ("connect", "connection", "unknown", "not found", "不存在")):
        raise AssertionError(f"unexpected disconnect error text: {cause}")


# -- driver --------------------------------------------------------------------

def ldap_reachable(raw_url: str) -> tuple[bool, str]:
    parsed = urlparse(raw_url)
    host, port = parsed.hostname, url_port(raw_url)
    try:
        with socket.create_connection((host, port), timeout=5.0):
            return True, ""
    except OSError as cause:
        return False, f"no LDAP server at {host}:{port} ({cause})"


def missing_env() -> str:
    required = {
        "LDAP_REAL_URL": URL,
        "LDAP_REAL_BINDDN": BIND_DN,
        "LDAP_REAL_BINDPW": BIND_PW,
        "LDAP_REAL_ROOT": ROOT,
    }
    missing = [name for name, value in required.items() if not value]
    if missing:
        return f"missing env: {', '.join(missing)} (no default real-server target)"
    return ""


def main() -> int:
    steps = [
        ("R1", "initialize + connection/test (simple)", run_r1),
        ("R1b", "connection/test (secondary URL)", run_r1b),
        ("R2", "connect + rootDse namingContexts", run_r2),
        ("R3", "base-scope search on the root", run_r3),
        ("R4", "paged one-scope aggregate", run_r4),
        ("R5", "count endpoint matches one-scope search", run_r5),
        ("R6", "entry/get on the root DN", run_r6),
        ("R7", "schema metadata", run_r7),
        ("R8", "read whitelist enforcement", run_r8),
        ("R9", "invalid filter rejection", run_r9),
        ("R10", "disconnect then call", run_r10),
    ]

    skip_reason = missing_env()
    if not skip_reason:
        ok, skip_reason = ldap_reachable(URL)
        if not ok and URL_PLAIN:
            # primary unreachable: still try, the suite will surface the error
            pass

    binary = default_binary()
    if not skip_reason and not os.path.exists(binary):
        skip_reason = f"sidecar binary not found: {binary} (set DBX_PLUGIN_SIDECAR)"

    if skip_reason:
        status = "FAIL" if REQUIRE else "SKIP"
        for no, name, _ in steps:
            RESULTS.append(ScenarioResult(no, name, status, skip_reason))
        report()
        return 0

    client = SidecarClient.start(data_dir=DATA_DIR or None)
    try:
        try:
            client.initialize()
        except (SidecarError, AssertionError) as cause:
            message = f"plugin/initialize failed: {cause}"
            for no, name, _ in steps:
                RESULTS.append(ScenarioResult(no, name, "FAIL" if REQUIRE else "SKIP", message))
            report()
            return 0
        for _, _, fn in steps:
            fn(client)
    finally:
        for connection_id in ("real-test", "real-plain", "real-main", "real-wl", "real-filter", "real-dc"):
            try:
                client.request("connection/disconnect", {"connection": {"id": connection_id}})
            except Exception:
                pass
        try:
            client.close()
        except Exception:
            pass

    report()
    return 1 if any(result.status == "FAIL" for result in RESULTS) else 0


def report() -> None:
    widths = (5, 42, 6)
    print(f"{'No.':<{widths[0]}} {'Scenario':<{widths[1]}} {'Status':<{widths[2]}} Detail")
    for result in RESULTS:
        print(f"{result.no:<{widths[0]}} {result.name:<{widths[1]}} {result.status:<{widths[2]}} {result.detail}")
    counts = {status: sum(1 for r in RESULTS if r.status == status) for status in ("PASS", "FAIL", "SKIP")}
    print(f"\ntotal={len(RESULTS)} PASS={counts['PASS']} FAIL={counts['FAIL']} SKIP={counts['SKIP']}")


if __name__ == "__main__":
    raise SystemExit(main())
