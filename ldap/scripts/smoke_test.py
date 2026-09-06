#!/usr/bin/env python3
"""Smoke test for the dbx-ldap-plugin sidecar (scenarios S1-S14).

Covers the IMPL_PLAN_DBX_LDAP §8 table over a live OpenLDAP container:

    S1  connection/test (simple bind)            -> success
    S2  connect + search base scope on root      -> entries non-empty
    S3  search sub with pageSize                 -> paged aggregate == full
    S4  entry add/modify/get/delete round-trip   -> read-back matches
    S5  modifyDn rename                          -> new DN found, old gone
    S6  read-only connection write               -> rejected (-32000)
    S7  blocked_attributes (userPassword)        -> stripped from results
    S8  search outside allowed_base_dns          -> rejected
    S9  disconnect then call                     -> unknown-connection error
    S10 invalid RFC 4515 filter                  -> filter validation error
    S11 rootDse namingContexts                   -> seeded ROOT advertised
    S12 {SSHA} password write + simple bind      -> bind ok; audit has no values
    S13 childrenCount + recursive subtree delete -> children gone; 1 aggregate audit
    S14 jpegPhoto binary round-trip              -> base64 identical

SKIP semantics (M0 §5.2):
  * a method not registered / not implemented yet  -> SKIP (backend under
    parallel development), never FAIL;
  * no OpenLDAP container reachable                -> whole suite SKIP;
  * set LDAP_TEST_REQUIRE=1 to turn env SKIPs into FAILs (CI).

Usage:
    DBX_PLUGIN_SIDECAR=/path/to/dbx-plugin-ldap python3 scripts/smoke_test.py
"""

from __future__ import annotations

import base64
import hashlib
import json
import os
import socket
import sys
import uuid

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from sidecar_client_jsonl import (  # noqa: E402
    SidecarClient,
    SidecarError,
    default_binary,
    is_method_not_registered,
    lifecycle_params,
)

HOST = os.environ.get("LDAP_TEST_HOST", "127.0.0.1")
PORT = int(os.environ.get("LDAP_TEST_PORT", "389"))
ROOT = os.environ.get("LDAP_TEST_ROOT", "dc=example,dc=org")
BIND_DN = os.environ.get("LDAP_TEST_BINDDN", "cn=admin,dc=example,dc=org")
BIND_PW = os.environ.get("LDAP_TEST_BINDPW", "adminpassword")
REQUIRE = os.environ.get("LDAP_TEST_REQUIRE", "") == "1"
DATA_DIR = os.environ.get("LDAP_TEST_DATA_DIR", "")

PEOPLE_OU = f"ou=people,{ROOT}"

# connection.host: since v0.1.19 the manifest binds a bare hostname here
# (fields host/port/tls_mode), while full ldap/ldaps URLs from older saved
# connections are still accepted (legacy passthrough in buildLDAPURL).
# These smokes keep exercising the legacy URL form on purpose.
URL = f"ldap://{HOST}:{PORT}"


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


def make_connection(connection_id: str, **extra_config) -> dict:
    external = {
        "auth_type": "simple",
        "bind_dn": BIND_DN,
        "base_dn": ROOT,
        "timeout_secs": 10,
    }
    external.update(extra_config)
    return {
        "id": connection_id,
        "name": f"smoke-{connection_id}",
        "host": URL,
        "port": PORT,
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


def entry_exists(client: SidecarClient, dn: str) -> bool:
    try:
        search(client, baseDn=dn, scope="base", sizeLimit=1)
        return True
    except SidecarError:
        return False


def ensure_people_ou(client: SidecarClient) -> None:
    if entry_exists(client, PEOPLE_OU):
        return
    domain(client,
        "ldap/entry/add",
        {
            "dn": PEOPLE_OU,
            "attributes": {"objectClass": ["organizationalUnit"], "ou": ["people"]},
        },
    )


# -- scenarios -----------------------------------------------------------------

@scenario("S1", "initialize + connection/test (simple)")
def run_s1(client: SidecarClient) -> None:
    """S1 initialize + connection/test (simple) -> success."""
    info = client.initialize()
    if not info:
        raise AssertionError("plugin/initialize returned no result")
    result = client.request("connection/test", lifecycle_params(make_connection("smoke-test")))
    if result.get("success") is not True:
        raise AssertionError(f"connection/test did not succeed: {result}")


@scenario("S2", "connect then search base scope on the root -> entries non-empty")
def run_s2(client: SidecarClient) -> None:
    """S2 connect then search base scope on the root -> entries non-empty."""
    connect(client, make_connection("smoke-main"))
    result = search(client, baseDn=ROOT, scope="base")
    entries = result.get("entries", [])
    if len(entries) == 0:
        raise AssertionError("base-scope search on the root returned no entries")


@scenario("S3", "paged subtree aggregate")
def run_s3(client: SidecarClient) -> None:
    """S3 subtree search with pageSize -> paged aggregate equals full count."""
    full = search(client, baseDn=ROOT, scope="sub", pageSize=1000)
    paged = search(client, baseDn=ROOT, scope="sub", pageSize=2)
    if full.get("count", 0) == 0:
        raise AssertionError("no entries under root to aggregate")
    if paged.get("count") != full.get("count"):
        raise AssertionError(f"paged aggregate {paged.get('count')} != full {full.get('count')}")


@scenario("S3b", "count endpoint matches one-scope search count")
def run_s3b(client: SidecarClient) -> None:
    """S3b ldap/count on the root -> equals a scope=one search count."""
    counted = domain(client, "ldap/count", {"baseDn": ROOT})
    listed = search(client, baseDn=ROOT, scope="one", sizeLimit=10000)
    if counted.get("count", 0) != listed.get("count", 0):
        raise AssertionError(f"count {counted.get('count')} != one-scope search {listed.get('count')}")


@scenario("S4", "entry add/modify/get/delete round-trip")
def run_s4(client: SidecarClient) -> None:
    """S4 add/modify/get/delete round-trip -> read-back consistent."""
    ensure_people_ou(client)
    dn = f"cn=Smoke Test {uuid.uuid4().hex[:8]},{PEOPLE_OU}"
    try:
        domain(client,
            "ldap/entry/add",
            {
                "dn": dn,
                "attributes": {
                    "objectClass": ["top", "person"],
                    "cn": [dn.split(",")[0].split("=")[1]],
                    "sn": ["Smoke"],
                    "description": ["before"],
                },
            },
        )
        domain(client,
            "ldap/entry/modify",
            {"dn": dn, "changes": [{"operation": "replace", "attribute": "description", "values": ["after"]}]},
        )
        fetched = domain(client, "ldap/entry/get", {"dn": dn})
        description = fetched.get("entry", {}).get("attributes", {}).get("description", [])
        if description != ["after"]:
            raise AssertionError(f"description read-back mismatch: {description}")
    finally:
        try:
            domain(client, "ldap/entry/delete", {"dn": dn})
        except SidecarError:
            pass
    expect_error(lambda: domain(client, "ldap/entry/get", {"dn": dn}))


@scenario("S5", "modifyDn rename")
def run_s5(client: SidecarClient) -> None:
    """S5 modifyDn rename -> new DN searchable, old DN gone."""
    ensure_people_ou(client)
    old_dn = f"cn=Smoke RDN {uuid.uuid4().hex[:8]},{PEOPLE_OU}"
    rdn = old_dn.split(",")[0]
    new_rdn = f"cn={rdn.split('=', 1)[1]} renamed"
    new_dn = f"{new_rdn},{PEOPLE_OU}"
    try:
        domain(client,
            "ldap/entry/add",
            {
                "dn": old_dn,
                "attributes": {
                    "objectClass": ["top", "person"],
                    "cn": [rdn.split("=", 1)[1]],
                    "sn": ["Rdn"],
                },
            },
        )
        domain(client,
            "ldap/entry/modifyDn",
            {"dn": old_dn, "newRdn": new_rdn, "deleteOldRdn": True},
        )
        if not entry_exists(client, new_dn):
            raise AssertionError(f"renamed entry not found at {new_dn}")
        if entry_exists(client, old_dn):
            raise AssertionError(f"old entry still present at {old_dn}")
    finally:
        for target in (new_dn, old_dn):
            try:
                domain(client, "ldap/entry/delete", {"dn": target})
            except SidecarError:
                pass


@scenario("S6", "read-only write rejection")
def run_s6(client: SidecarClient) -> None:
    """S6 read-only connection rejects writes with -32000."""
    connect(client, make_connection("smoke-ro", read_only=True))
    cause = expect_error(
        lambda: domain(client,
            "ldap/entry/add",
            {"dn": f"cn=Nope,{PEOPLE_OU}", "attributes": {"objectClass": ["top", "person"], "cn": ["Nope"], "sn": ["No"]}},
        )
    )
    if cause.code not in (None, -32000):
        raise AssertionError(f"expected business error -32000, got {cause.code}: {cause}")


@scenario("S7", "blocked_attributes stripping")
def run_s7(client: SidecarClient) -> None:
    """S7 blocked_attributes: writes carrying a blocked attribute are rejected
    (tiny-rdm ensureLDAPWriteAllowed semantics, kept as-is by the port) and
    reads never leak userPassword (stripped from get/search results)."""
    connect(client, make_connection("smoke-pw"))
    # (a) an add carrying userPassword is rejected by profile policy
    dn = f"cn=Smoke Pw {uuid.uuid4().hex[:8]},{PEOPLE_OU}"
    cause = expect_error(
        lambda: domain(
            client,
            "ldap/entry/add",
            {"dn": dn, "attributes": {"objectClass": ["top", "person"], "cn": [dn.split(",")[0].split("=")[1]], "sn": ["Pw"], "userPassword": ["s3cret"]}},
        ),
        markers=("blocked",),
    )
    if cause.code not in (None, -32000):
        raise AssertionError(f"expected business error -32000, got {cause.code}: {cause}")
    # (b) a seeded entry holding userPassword never leaks it via get/search
    seeded_dn = f"uid=seedpw,{PEOPLE_OU}"
    for method, params in (
        ("ldap/entry/get", {"dn": seeded_dn}),
        ("ldap/search", {"baseDn": PEOPLE_OU, "scope": "one", "filter": "(objectClass=person)"}),
    ):
        result = domain(client, method, params)
        entries = result.get("entries", []) if method == "ldap/search" else [result.get("entry", {})]
        if method == "ldap/entry/get" and not entries[0]:
            raise AssertionError(f"seeded entry {seeded_dn} not found")
        for entry in entries:
            for name in entry.get("attributes", {}):
                if name.lower() == "userpassword":
                    raise AssertionError(f"userPassword leaked via {method}")


@scenario("S8", "read whitelist enforcement")
def run_s8(client: SidecarClient) -> None:
    """S8 reads outside allowed_base_dns are rejected."""
    connect(client, make_connection("smoke-wl", allowed_base_dns=PEOPLE_OU))
    expect_error(lambda: search(client, baseDn=ROOT, scope="base"), markers=("allow", "whitelist", "permitted", "denied", "outside", "base"))


@scenario("S9", "disconnect then call")
def run_s9(client: SidecarClient) -> None:
    """S9 disconnect then call -> unknown connection business error."""
    connection = make_connection("smoke-dc")
    connect(client, connection)
    client.request("connection/disconnect", {"connection": {"id": connection["id"]}})
    cause = expect_error(lambda: search(client, baseDn=ROOT, scope="base"))
    if is_method_not_registered(cause):
        raise AssertionError("disconnect path should be a business error, not method-not-found")
    lowered = str(cause).lower()
    if not any(marker in lowered for marker in ("connect", "connection", "unknown", "not found", "不存在")):
        raise AssertionError(f"unexpected disconnect error text: {cause}")


@scenario("S10", "invalid filter rejection")
def run_s10(client: SidecarClient) -> None:
    """S10 invalid RFC 4515 filter -> filter validation error."""
    connect(client, make_connection("smoke-filter"))
    cause = expect_error(lambda: search(client, baseDn=ROOT, scope="sub", filter="((( "))
    if is_method_not_registered(cause):
        raise AssertionError("filter validation should be a business error")
    lowered = str(cause).lower()
    if not any(marker in lowered for marker in ("filter", "过滤")):
        raise AssertionError(f"error does not mention the filter: {cause}")


@scenario("S11", "rootDse namingContexts (auto baseDn primitive)")
def run_s11(client: SidecarClient) -> None:
    """S11 explicit namingContexts request works; workbench auto-baseDn (v0.1.20)
    依赖该原语在 namingContexts 缺省不下发时也能取到值。"""
    connect(client, make_connection("smoke-rootdse"))
    result = domain(client, "ldap/rootDse", {"attributes": ["namingContexts", "defaultNamingContext"]})
    attributes = (result.get("entry") or result).get("attributes") or {}
    contexts = [v for v in (attributes.get("namingContexts") or []) if str(v).strip()]
    if not contexts:
        raise AssertionError(f"rootDse namingContexts empty: {result}")
    if not any(str(v).strip().lower().replace(" ", "") == ROOT.lower().replace(" ", "") for v in contexts):
        raise AssertionError(f"namingContexts {contexts} does not contain the seeded ROOT {ROOT}")


# Tiny valid JPEG (8x8, 627 bytes, generated with Go image/jpeg) shared with the
# seed fixture uid=seedphoto (scripts/ldap-seed/01-testdata.ldif, scenario S14).
SMOKE_JPEG_B64 = (
    "/9j/2wCEAAoHBwgHBgoICAgLCgoLDhgQDg0NDh0VFhEYIx8lJCIfIiEmKzcvJik0KSEiMEExNDk7"
    "Pj4+JS5ESUM8SDc9PjsBCgsLDg0OHBAQHDsoIig7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7"
    "Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7O//AABEIAAgACAMBIgACEQEDEQH/xAGiAAABBQEBAQEBAQAA"
    "AAAAAAAAAQIDBAUGBwgJCgsQAAIBAwMCBAMFBQQEAAABfQECAwAEEQUSITFBBhNRYQcicRQygZGh"
    "CCNCscEVUtHwJDNicoIJChYXGBkaJSYnKCkqNDU2Nzg5OkNERUZHSElKU1RVVldYWVpjZGVmZ2hp"
    "anN0dXZ3eHl6g4SFhoeIiYqSk5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV"
    "1tfY2drh4uPk5ebn6Onq8fLz9PX29/j5+gEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoLEQAC"
    "AQIEBAMEBwUEBAABAncAAQIDEQQFITEGEkFRB2FxEyIygQgUQpGhscEJIzNS8BVictEKFiQ04SXx"
    "FxgZGiYnKCkqNTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqCg4SFhoeIiYqS"
    "k5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV1tfY2dri4+Tl5ufo6ery8/T1"
    "9vf4+fr/2gAMAwEAAhEDEQA/AKmjeDvu/u/0ra/4Q7/pn+lbWjfw1t15+IzCvz7l5PmmI+qrU//Z"
)


@scenario("S12", "password hash write + bind verification")
def run_s12(client: SidecarClient) -> None:
    """S12 {SSHA} password round-trip: userPassword written via entry/modify on
    a connection that overrides blocked_attributes (the default table blocks
    it), then simple bind with the plaintext verified via connection/test.
    Neither the plaintext nor the hash may appear in any ldap/audit event."""
    connect(client, make_connection("smoke-pwhash", blocked_attributes=["token"]))
    ensure_people_ou(client)
    dn = f"uid=smokepw-{uuid.uuid4().hex[:8]},{PEOPLE_OU}"
    uid = dn.split(",")[0].split("=")[1]
    password = f"Ssmoke-{uuid.uuid4().hex}"  # never logged/asserted verbatim
    salt = os.urandom(8)
    hashed = "{SSHA}" + base64.b64encode(hashlib.sha1(password.encode() + salt).digest() + salt).decode()
    try:
        domain(client,
            "ldap/entry/add",
            {
                "dn": dn,
                "attributes": {
                    "objectClass": ["top", "person", "inetOrgPerson"],
                    "cn": [uid],
                    "sn": ["Pw"],
                    "uid": [uid],
                },
            },
        )
        domain(client,
            "ldap/entry/modify",
            {"dn": dn, "changes": [{"operation": "replace", "attribute": "userPassword", "values": [hashed]}]},
        )
        bind = {
            "id": "smoke-pwbind",
            "name": "smoke-pwbind",
            "host": URL,
            "port": PORT,
            "external_config": {"auth_type": "simple", "bind_dn": dn, "base_dn": ROOT},
            "connection_secrets": {"bind_password": password},
        }
        result = client.request("connection/test", lifecycle_params(bind))
        if result.get("success") is not True:
            raise AssertionError(f"bind with the hashed password failed: {result.get('message', result)}")
        # 凭据红线：任何 ldap/audit 事件不得携带明文或哈希值。
        for event in client.events:
            if event.get("method") != "ldap/audit":
                continue
            blob = json.dumps(event.get("params", {}), ensure_ascii=True)
            if password in blob or hashed in blob:
                raise AssertionError("ldap/audit event carries a password value")
    finally:
        try:
            domain(client, "ldap/entry/delete", {"dn": dn})
        except SidecarError:
            pass


@scenario("S13", "childrenCount + recursive subtree delete")
def run_s13(client: SidecarClient) -> None:
    """S13 N1 subtree delete: parent + two levels of children -> childrenCount
    per level -> recursive delete -> every child DN gone, exactly one aggregate
    subtree_delete audit carrying the deleted entry count."""
    ensure_people_ou(client)
    suffix = uuid.uuid4().hex[:8]
    parent = f"ou=SmokeTree-{suffix},{PEOPLE_OU}"
    child = f"ou=branch,{parent}"
    leaf = f"cn=leaf,{child}"
    extra = f"ou=empty-{suffix},{parent}"
    entries = [
        (parent, {"objectClass": ["organizationalUnit"], "ou": [f"SmokeTree-{suffix}"]}),
        (extra, {"objectClass": ["organizationalUnit"], "ou": [f"empty-{suffix}"]}),
        (child, {"objectClass": ["organizationalUnit"], "ou": ["branch"]}),
        (leaf, {"objectClass": ["top", "person"], "cn": ["leaf"], "sn": ["Leaf"]}),
    ]
    try:
        for dn, attributes in entries:
            domain(client, "ldap/entry/add", {"dn": dn, "attributes": attributes})
        counted = domain(client, "ldap/entry/childrenCount", {"dn": parent})
        if counted.get("count") != 2 or counted.get("truncated"):
            raise AssertionError(f"childrenCount(parent) = {counted}, want count=2")
        counted = domain(client, "ldap/entry/childrenCount", {"dn": child})
        if counted.get("count") != 1:
            raise AssertionError(f"childrenCount(child) = {counted}, want count=1")
        result = domain(client, "ldap/entry/delete", {"dn": parent, "recursive": True})
        if result.get("success") is not True:
            raise AssertionError(f"recursive delete failed: {result}")
        for dn, _ in entries:
            if entry_exists(client, dn):
                raise AssertionError(f"entry survived subtree delete: {dn}")
        # 审计聚合：该目标恰好一条 subtree_delete/ok，带 deletedCount（camelCase）。
        subtree = [
            event["params"] for event in client.events
            if event.get("method") == "ldap/audit"
            and str(event.get("params", {}).get("target", "")).lower() == parent.lower()
            and event["params"].get("action") == "subtree_delete"
        ]
        if len(subtree) != 1:
            raise AssertionError(f"expected exactly one subtree_delete audit, got {len(subtree)}")
        record = subtree[0]
        if record.get("result") != "ok" or record.get("deletedCount") != len(entries):
            raise AssertionError(f"aggregate audit = {record}, want ok + deletedCount={len(entries)}")
    finally:
        for dn, _ in entries:  # best-effort cleanup on failure paths only
            try:
                domain(client, "ldap/entry/delete", {"dn": dn})
            except SidecarError:
                pass


@scenario("S14", "jpegPhoto binary round-trip")
def run_s14(client: SidecarClient) -> None:
    """S14 binary attribute: the seeded jpegPhoto sample is written to a fresh
    entry via entry/modify and read back via entry/get; the base64 must match
    byte-for-byte (JSON carries bytes as latin-1-mapped code points)."""
    connect(client, make_connection("smoke-photo"))
    ensure_people_ou(client)
    photo = base64.b64decode(SMOKE_JPEG_B64)
    value = photo.decode("latin-1")  # U+0000-U+00FF bijection over the wire
    dn = f"cn=Smoke Photo {uuid.uuid4().hex[:8]},{PEOPLE_OU}"
    rdn = dn.split(",")[0].split("=")[1]
    try:
        domain(client,
            "ldap/entry/add",
            {"dn": dn, "attributes": {"objectClass": ["top", "person", "inetOrgPerson"], "cn": [rdn], "sn": ["Photo"]}},
        )
        domain(client,
            "ldap/entry/modify",
            {"dn": dn, "changes": [{"operation": "add", "attribute": "jpegPhoto", "values": [value]}]},
        )
        fetched = domain(client, "ldap/entry/get", {"dn": dn, "attributes": ["jpegPhoto"]})
        values = fetched.get("entry", {}).get("attributes", {}).get("jpegPhoto", [])
        if len(values) != 1:
            raise AssertionError(f"jpegPhoto read-back missing: {fetched.get('entry', {}).get('attributes')}")
        if values[0].encode("latin-1") != photo:
            raise AssertionError("jpegPhoto round-trip mismatch")
    finally:
        try:
            domain(client, "ldap/entry/delete", {"dn": dn})
        except SidecarError:
            pass


# -- driver --------------------------------------------------------------------

def ldap_reachable() -> tuple[bool, str]:
    try:
        with socket.create_connection((HOST, PORT), timeout=2.0):
            return True, ""
    except OSError as cause:
        return False, f"no LDAP server at {HOST}:{PORT} ({cause})"


def main() -> int:
    steps = [
        ("S1", "initialize + connection/test (simple)", run_s1),
        ("S2", "connect + base-scope root search", run_s2),
        ("S3", "paged subtree aggregate", run_s3),
        ("S3b", "count endpoint matches one-scope search count", run_s3b),
        ("S4", "entry add/modify/get/delete round-trip", run_s4),
        ("S5", "modifyDn rename", run_s5),
        ("S6", "read-only write rejection", run_s6),
        ("S7", "blocked_attributes stripping", run_s7),
        ("S8", "read whitelist enforcement", run_s8),
        ("S10", "invalid filter rejection", run_s10),
        ("S11", "rootDse namingContexts (auto baseDn)", run_s11),
        ("S12", "password hash write + bind verification", run_s12),
        ("S13", "childrenCount + recursive subtree delete", run_s13),
        ("S14", "jpegPhoto binary round-trip", run_s14),
        ("S9", "disconnect then call", run_s9),
    ]

    ok, reason = ldap_reachable()
    if not ok:
        if REQUIRE:
            for no, name, _ in steps:
                RESULTS.append(ScenarioResult(no, name, "FAIL", reason))
        else:
            for no, name, _ in steps:
                RESULTS.append(ScenarioResult(no, name, "SKIP", reason))
        report()
        return 0

    binary = default_binary()
    if not os.path.exists(binary):
        message = f"sidecar binary not found: {binary} (set DBX_PLUGIN_SIDECAR)"
        for no, name, _ in steps:
            RESULTS.append(ScenarioResult(no, name, "FAIL" if REQUIRE else "SKIP", message))
        report()
        return 0

    client = SidecarClient.start(data_dir=DATA_DIR or None)
    try:
        # initialize once up-front; scenario S1 re-asserts it explicitly.
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
        for connection_id in ("smoke-main", "smoke-ro", "smoke-pw", "smoke-wl", "smoke-filter", "smoke-dc", "smoke-pwhash", "smoke-photo"):
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
    widths = (4, 40, 6)
    print(f"{'No.':<{widths[0]}} {'Scenario':<{widths[1]}} {'Status':<{widths[2]}} Detail")
    for result in RESULTS:
        print(f"{result.no:<{widths[0]}} {result.name:<{widths[1]}} {result.status:<{widths[2]}} {result.detail}")
    counts = {status: sum(1 for r in RESULTS if r.status == status) for status in ("PASS", "FAIL", "SKIP")}
    print(f"\ntotal={len(RESULTS)} PASS={counts['PASS']} FAIL={counts['FAIL']} SKIP={counts['SKIP']}")


if __name__ == "__main__":
    raise SystemExit(main())
