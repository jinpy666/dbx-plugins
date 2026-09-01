#!/usr/bin/env python3
"""M3/TLS smoke test for the dbx-ldap-plugin sidecar (scenarios T1-T6, A1-A5).

Covers the X-C路 M3 tasks over a live OpenLDAP container with LDAPS/StartTLS
(docker-compose.ldap-tls-test.yml, bitnami/openldap, self-signed server cert):

    T1  connection/test over ldaps:// with tls_verify=false   -> success
    T2  connect over ldaps + search + audit warning           -> entries + audit.jsonl tls-insecure warning
    T3  connection/test with StartTLS (ldap:// 389)           -> success
    T4  ldaps:// with tls_verify=true (self-signed)           -> dial rejected (certificate error)
    T5  ldaps + use_starttls=true                             -> rejected (mutually exclusive)
    T6  structured form: bare host + tls_mode=ldaps + port    -> success (v0.1.19 fields)
    A1  external (SASL EXTERNAL) over TCP                     -> real bind attempted (no "planned for M3")
    A2  digest_md5                                            -> PASS or SKIP when server lacks the SASL mech
    A3  ntlm (no AD in container)                             -> real NTLMSSP bind attempted (mech-missing error ok)
    A4  ntlm_hash (missing hash / with hash)                  -> param surface + real bind attempted
    A5  kerberos (missing config / dead KDC)                  -> real KDC attempt (network error), never "planned for M3"

Hard-to-provision integrations are asserted at the "real implementation path"
level and marked accordingly (tiny-rdm 源 bind 分发 :1210-1346；真机 KDC/AD
集成登记为后续 ldap-gssapi-test.yml 工作，见 docs/PROGRESS-XC.zh-CN.md 遗留节).

SKIP semantics (M0 §5.2):
  * no TLS container reachable        -> whole suite SKIP;
  * server lacks a SASL mechanism     -> per-scenario SKIP with reason;
  * set LDAP_TEST_REQUIRE=1           -> env SKIPs become FAILs (CI).

Usage (self-contained orchestration, mirrors scripts/smoke_container.py):
    export LDAP_ADMIN_PASSWORD=...        # never stored on disk
    python3 scripts/smoke_auth_test.py [--keep]

Or against an already-running TLS container / manual compose:
    export LDAP_TLS_CERTS_DIR=/tmp/...    # ephemeral dir with openldap.crt/.key/openldap-ca.crt
    docker compose -f docker-compose.ldap-tls-test.yml up -d
    DBX_PLUGIN_SIDECAR=... python3 scripts/smoke_auth_test.py --attach
"""

from __future__ import annotations

import argparse
import json
import os
import socket
import ssl
import subprocess
import sys
import tempfile
import time
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from sidecar_client_jsonl import (  # noqa: E402
    SidecarClient,
    SidecarError,
    default_binary,
    is_method_not_registered,
    lifecycle_params,
)

REPO = Path(__file__).resolve().parent.parent
COMPOSE_FILE = REPO / "docker-compose.ldap-tls-test.yml"
CONTAINER = "dbx-ldap-tls-test"
HOST = os.environ.get("LDAP_TEST_HOST", "127.0.0.1")
LDAP_PORT = int(os.environ.get("LDAP_TEST_PORT", "1389"))
LDAPS_PORT = int(os.environ.get("LDAP_TEST_LDAPS_PORT", "636"))
ROOT = os.environ.get("LDAP_TEST_ROOT", "dc=example,dc=org")
BIND_DN = os.environ.get("LDAP_TEST_BINDDN", f"cn=admin,{ROOT}")
REQUIRE = os.environ.get("LDAP_TEST_REQUIRE", "") == "1"
DATA_DIR = os.environ.get("LDAP_TEST_DATA_DIR", "")
READY_TIMEOUT_SECS = 90

M3_NOT_IMPLEMENTED = "planned for m3"


class SkipScenario(Exception):
    """Raised by a scenario when a precondition is unavailable."""


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


def assert_not_m3_stub(cause: SidecarError) -> None:
    """The M3 auth path must never answer with the M1-era 'planned for M3' stub."""
    if M3_NOT_IMPLEMENTED in str(cause).lower():
        raise AssertionError(f"backend still reports M3 as not implemented: {cause}")


def make_connection(connection_id: str, url: str, extra_secrets: dict | None = None, **extra_config) -> dict:
    external = {
        "auth_type": "simple",
        "bind_dn": BIND_DN,
        "base_dn": ROOT,
        "timeout_secs": 10,
    }
    external.update(extra_config)
    port = LDAPS_PORT if url.startswith("ldaps://") else LDAP_PORT
    secrets = {"bind_password": os.environ["LDAP_ADMIN_PASSWORD"]}
    secrets.update(extra_secrets or {})
    return {
        "id": connection_id,
        "name": f"smoke-{connection_id}",
        "host": url,
        "port": port,
        "external_config": external,
        "connection_secrets": secrets,
    }


CURRENT_CONNECTION = {"id": ""}
# Resolved in main(): LDAP_TEST_DATA_DIR or an isolated temp dir (used for the
# audit.jsonl assertion in T2).
CURRENT_DATA_DIR = {"path": DATA_DIR}


def connect(client: SidecarClient, connection: dict) -> None:
    CURRENT_CONNECTION["id"] = connection["id"]
    client.request("connection/connect", lifecycle_params(connection))


def domain(client: SidecarClient, method: str, params: dict) -> dict:
    payload = dict(params)
    payload.setdefault("connectionId", CURRENT_CONNECTION["id"])
    return client.request(method, payload)


def search(client: SidecarClient, **params) -> dict:
    payload = {"filter": "(objectClass=*)", "scope": "sub"}
    payload.update(params)
    return domain(client, "ldap/search", payload)


def read_audit_lines(data_dir: str) -> list[dict]:
    path = Path(data_dir) / "audit.jsonl"
    if not path.exists():
        return []
    lines = []
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if line:
            try:
                lines.append(json.loads(line))
            except json.JSONDecodeError:
                continue
    return lines


# -- TLS scenarios (T) ---------------------------------------------------------

@scenario("T1", "connection/test over ldaps:// with tls_verify=false")
def run_t1(client: SidecarClient) -> None:
    """T1 InsecureSkipVerify path: self-signed cert accepted, test succeeds."""
    result = client.request("connection/test", lifecycle_params(
        make_connection("smoke-tls-test", f"ldaps://{HOST}:{LDAPS_PORT}", tls_verify=False),
    ))
    if result.get("success") is not True:
        raise AssertionError(f"ldaps connection/test did not succeed: {result}")


@scenario("T2", "connect over ldaps + search + audit warning")
def run_t2(client: SidecarClient) -> None:
    """T2 connect+search over ldaps (tls_verify=false); audit.jsonl gains a
    tls-insecure warning row (IMPL_PLAN §10 semantics)."""
    connect(client, make_connection("smoke-tls-main", f"ldaps://{HOST}:{LDAPS_PORT}", tls_verify=False))
    result = search(client, baseDn=ROOT, scope="base")
    if len(result.get("entries", [])) == 0:
        raise AssertionError("base-scope search over ldaps returned no entries")

    data_dir = DATA_DIR or CURRENT_DATA_DIR["path"]
    if not data_dir:
        raise SkipScenario("no data dir known; audit warning check skipped")
    warnings = [
        rec for rec in read_audit_lines(data_dir)
        if rec.get("action", "").endswith("tls-insecure") or rec.get("action") == "tls-insecure"
    ]
    if not warnings:
        raise AssertionError("audit.jsonl has no tls-insecure warning after ldaps connect")
    for rec in warnings:
        blob = json.dumps(rec)
        if os.environ["LDAP_ADMIN_PASSWORD"] and os.environ["LDAP_ADMIN_PASSWORD"] in blob:
            raise AssertionError("audit warning leaked a credential")


@scenario("T3", "connection/test with StartTLS on ldap:// 389")
def run_t3(client: SidecarClient) -> None:
    """T3 StartTLS upgrade path on the plain port with the self-signed cert."""
    result = client.request("connection/test", lifecycle_params(
        make_connection("smoke-starttls", f"ldap://{HOST}:{LDAP_PORT}", tls_verify=False, use_starttls=True),
    ))
    if result.get("success") is not True:
        raise AssertionError(f"StartTLS connection/test did not succeed: {result}")


@scenario("T4", "ldaps with tls_verify=true rejects self-signed cert")
def run_t4(client: SidecarClient) -> None:
    """T4 verification stays on by default: the self-signed chain must fail."""
    connect(client, make_connection("smoke-tls-strict", f"ldaps://{HOST}:{LDAPS_PORT}", tls_verify=True))
    cause = expect_error(lambda: search(client, baseDn=ROOT, scope="base"))
    lowered = str(cause).lower()
    if not any(marker in lowered for marker in ("certificate", "x509", "tls", "verify", "authority", "handshake")):
        raise AssertionError(f"expected a TLS verification failure, got: {cause}")


@scenario("T5", "ldaps + use_starttls are mutually exclusive")
def run_t5(client: SidecarClient) -> None:
    """T5 backend rejects StartTLS on an ldaps URL (dial-level guard)."""
    cause = expect_error(lambda: client.request("connection/test", lifecycle_params(
        make_connection("smoke-tls-mutex", f"ldaps://{HOST}:{LDAPS_PORT}", tls_verify=False, use_starttls=True),
    )))
    if not any(marker in str(cause).lower() for marker in ("starttls", "start tls", "ldaps")):
        raise AssertionError(f"expected StartTLS/ldaps mutual-exclusion error, got: {cause}")


@scenario("T6", "structured form: bare host + tls_mode=ldaps (v0.1.19 fields)")
def run_t6(client: SidecarClient) -> None:
    """T6 新表单三字段（host 裸主机名 / port / tls_mode）与 legacy URL 同链路。"""
    connection = make_connection(
        "smoke-tls-structured", f"ldaps://{HOST}:{LDAPS_PORT}", tls_verify=False,
    )
    connection["host"] = HOST
    connection["external_config"]["tls_mode"] = "ldaps"
    result = client.request("connection/test", lifecycle_params(connection))
    if result.get("success") is not True:
        raise AssertionError(f"structured-field ldaps connection/test did not succeed: {result}")


# -- M3 auth scenarios (A) -----------------------------------------------------

@scenario("A1", "external (SASL EXTERNAL) over TCP attempts real bind")
def run_a1(client: SidecarClient) -> None:
    """A1 ExternalBind is wired: over plain TCP OpenLDAP answers with a bind
    error (no TLS client cert) — that error proves the M3 path, not the stub."""
    cause = expect_error(lambda: client.request("connection/test", lifecycle_params(
        make_connection("smoke-external", f"ldap://{HOST}:{LDAP_PORT}", auth_type="external"),
    )))
    assert_not_m3_stub(cause)
    # ldapi://unix-socket success path needs a sidecar-local slapd socket and is
    # covered by unit tests + future integration compose (SKIP reason documented).


@scenario("A2", "digest_md5 binds or server lacks the SASL mech")
def run_a2(client: SidecarClient) -> None:
    """A2 DIGEST-MD5: success -> PASS; OpenLDAP without the cyrus-sasl
    digest-md5 module (or without a plaintext secret to digest against)
    answers an auth-method/unavailable error -> SKIP with reason."""
    try:
        result = client.request("connection/test", lifecycle_params(
            make_connection("smoke-digest", f"ldap://{HOST}:{LDAP_PORT}", auth_type="digest_md5", username=BIND_DN),
        ))
    except SidecarError as cause:
        assert_not_m3_stub(cause)
        lowered = str(cause).lower()
        if any(marker in lowered for marker in (
            "authmethod", "not supported", "unwilling", "inappropriate",
            "sasl", "no secret in database",
        )):
            raise SkipScenario(f"OpenLDAP container cannot serve DIGEST-MD5: {cause}")
        raise AssertionError(f"DIGEST-MD5 bind failed with an unexpected error: {cause}")
    if result.get("success") is not True:
        raise AssertionError(f"digest_md5 connection/test returned no success: {result}")


@scenario("A3", "ntlm attempts real NTLMSSP bind (no AD expected)")
def run_a3(client: SidecarClient) -> None:
    """A3 NTLMBind is wired: OpenLDAP (no AD) must answer with an auth-method /
    bind error rather than a parameter or stub error."""
    cause = expect_error(lambda: client.request("connection/test", lifecycle_params(
        make_connection("smoke-ntlm", f"ldap://{HOST}:{LDAP_PORT}", auth_type="ntlm", username="admin", domain="example.org"),
    )))
    assert_not_m3_stub(cause)
    lowered = str(cause).lower()
    if any(marker in lowered for marker in ("username or binddn is required",)):
        raise AssertionError(f"NTLM param surface regressed: {cause}")
    # Any server-side bind rejection is acceptable here (NTLMSSP mech absent on
    # plain OpenLDAP); reaching it proves the go-ldap NTLMBind call path.


@scenario("A4", "ntlm_hash param surface + real bind attempt")
def run_a4(client: SidecarClient) -> None:
    """A4 (a) missing hash -> 'ntlmHash is required'; (b) with hash -> server
    bind error (never the M1 stub)."""
    cause = expect_error(lambda: client.request("connection/test", lifecycle_params(
        make_connection("smoke-ntlmhash-empty", f"ldap://{HOST}:{LDAP_PORT}", auth_type="ntlm_hash", username="admin"),
    )))
    if "ntlmhash is required" not in str(cause).lower():
        raise AssertionError(f"missing-hash case should fail parameter validation, got: {cause}")

    cause = expect_error(lambda: client.request("connection/test", lifecycle_params(
        make_connection("smoke-ntlmhash", f"ldap://{HOST}:{LDAP_PORT}", auth_type="ntlm_hash", username="admin", domain="example.org"),
    )))
    assert_not_m3_stub(cause)
    if "username or binddn is required" in str(cause).lower():
        raise AssertionError(f"NTLM hash param surface regressed: {cause}")


@scenario("A5", "kerberos wiring (config errors + dead-KDC network error)")
def run_a5(client: SidecarClient) -> None:
    """A5 (a) no realm/kdc/krb5.conf -> kerberos config error; (b) inline
    realm+KDC pointing at a dead port -> gokrb5 AS_REQ network error."""
    cause = expect_error(lambda: client.request("connection/test", lifecycle_params(
        make_connection("smoke-krb-nocfg", f"ldap://{HOST}:{LDAP_PORT}", auth_type="kerberos", username="admin"),
    )))
    assert_not_m3_stub(cause)
    if "kerberos" not in str(cause).lower():
        raise AssertionError(f"expected a kerberos config error, got: {cause}")

    # Dead KDC: runtime dials the LDAP endpoint (this host), the KDC client
    # dials 127.0.0.1:18688 where nothing listens -> AS_REQ failure.
    cause = expect_error(lambda: client.request("connection/test", lifecycle_params(
        make_connection(
            "smoke-krb-deadkdc",
            f"ldap://{HOST}:{LDAP_PORT}",
            extra_secrets={"krb_password": os.urandom(16).hex()},
            auth_type="kerberos",
            username="admin",
            krb_credential_type="password",
            krb_realm="EXAMPLE.ORG",
            krb_kdc_host="127.0.0.1",
            krb_kdc_port=18688,
        ),
    )))
    assert_not_m3_stub(cause)
    lowered = str(cause).lower()
    if not any(marker in lowered for marker in ("kdc", "as_req", "connection", "refused", "tcp", "timeout")):
        raise AssertionError(f"expected a KDC network error, got: {cause}")


# -- container orchestration (self-contained; mirrors smoke_container.py) ------

def sh(cmd: list[str], **kwargs) -> subprocess.CompletedProcess:
    print("+", " ".join(cmd), flush=True)
    return subprocess.run(cmd, **kwargs)


def compose(*args: str, check: bool = True, env: dict | None = None) -> subprocess.CompletedProcess:
    return sh(["docker", "compose", "-f", str(COMPOSE_FILE), *args], check=check, env=env)


def generate_self_signed_cert(cert_dir: Path) -> None:
    """Generate a throwaway self-signed server cert for the container."""
    cert_dir.mkdir(parents=True, exist_ok=True)
    key = cert_dir / "openldap.key"
    crt = cert_dir / "openldap.crt"
    ca = cert_dir / "openldap-ca.crt"
    sh([
        "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
        "-keyout", str(key), "-out", str(crt), "-days", "2",
        "-subj", "/CN=dbx-ldap-tls-test", "-addext",
        "subjectAltName=DNS:localhost,IP:127.0.0.1,DNS:dbx-ldap-tls-test",
    ], check=True, capture_output=True)
    # bitnami expects the CA cert file to exist as well; a self-signed cert
    # doubles as its own CA for this throwaway setup.
    ca.write_bytes(crt.read_bytes())
    os.chmod(key, 0o600)


def port_open(port: int) -> bool:
    try:
        with socket.create_connection((HOST, port), timeout=1.0):
            return True
    except OSError:
        return False


def tls_handshake_ok() -> bool:
    try:
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
        with socket.create_connection((HOST, LDAPS_PORT), timeout=2.0) as sock:
            with ctx.wrap_socket(sock, server_hostname="localhost"):
                return True
    except OSError:
        return False


def wait_ready() -> None:
    """Wait until slapd answers an admin bind on 1389 and completes a TLS
    handshake on 636 (bitnami bootstrap restarts slapd mid-way)."""
    admin_dn = BIND_DN
    probe = [
        "docker", "exec", CONTAINER, "ldapsearch", "-x",
        "-H", f"ldap://127.0.0.1:{LDAP_PORT}",
        "-D", admin_dn,
        "-w", os.environ["LDAP_ADMIN_PASSWORD"],
        "-b", ROOT, "-s", "base", "(objectClass=*)", "1.1",
    ]
    deadline = time.monotonic() + READY_TIMEOUT_SECS
    while time.monotonic() < deadline:
        if port_open(LDAP_PORT) and port_open(LDAPS_PORT):
            bound = subprocess.run(probe, capture_output=True).returncode == 0
            if bound and tls_handshake_ok():
                time.sleep(1)
                return
        time.sleep(1)
    raise SystemExit(f"TLS LDAP container not ready on {HOST}:{LDAP_PORT}/{LDAPS_PORT} after {READY_TIMEOUT_SECS}s")


# -- driver --------------------------------------------------------------------

STEPS = [
    ("T1", "ldaps:// connection/test (tls_verify=false)", run_t1),
    ("T2", "ldaps connect + search + audit warning", run_t2),
    ("T3", "StartTLS connection/test on 389", run_t3),
    ("T4", "ldaps tls_verify=true rejects self-signed", run_t4),
    ("T5", "ldaps + use_starttls mutually exclusive", run_t5),
    ("T6", "structured form (host/tls_mode) over ldaps", run_t6),
    ("A1", "external (SASL EXTERNAL) real bind path", run_a1),
    ("A2", "digest_md5 (SASL mech dependent)", run_a2),
    ("A3", "ntlm real bind attempt (no AD)", run_a3),
    ("A4", "ntlm_hash param surface + bind attempt", run_a4),
    ("A5", "kerberos wiring + dead-KDC network error", run_a5),
]


def report() -> None:
    widths = (4, 46, 6)
    print(f"{'No.':<{widths[0]}} {'Scenario':<{widths[1]}} {'Status':<{widths[2]}} Detail")
    for result in RESULTS:
        print(f"{result.no:<{widths[0]}} {result.name:<{widths[1]}} {result.status:<{widths[2]}} {result.detail}")
    counts = {status: sum(1 for r in RESULTS if r.status == status) for status in ("PASS", "FAIL", "SKIP")}
    print(f"\ntotal={len(RESULTS)} PASS={counts['PASS']} FAIL={counts['FAIL']} SKIP={counts['SKIP']}")


def mark_all(status: str, message: str) -> None:
    for no, name, _ in STEPS:
        RESULTS.append(ScenarioResult(no, name, status, message))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--keep", action="store_true", help="leave the container running")
    parser.add_argument("--attach", action="store_true", help="use an already-running TLS container")
    args = parser.parse_args()

    if not os.environ.get("LDAP_ADMIN_PASSWORD"):
        print("error: export LDAP_ADMIN_PASSWORD before running this script", file=sys.stderr)
        return 2

    attach = args.attach
    cert_dir: Path | None = None
    compose_env = os.environ.copy()  # teardown needs LDAP_TLS_CERTS_DIR too
    if not attach:
        if not shutil_which("docker"):
            mark_all("FAIL" if REQUIRE else "SKIP", "docker CLI not available")
            report()
            return 0
        cert_dir = Path(tempfile.mkdtemp(prefix="dbx-ldap-tls-certs-"))
        generate_self_signed_cert(cert_dir)
        compose_env["LDAP_TLS_CERTS_DIR"] = str(cert_dir)
        compose("up", "-d", check=True, env=compose_env)
    else:
        if not port_open(LDAP_PORT) or not port_open(LDAPS_PORT):
            mark_all("FAIL" if REQUIRE else "SKIP", f"no TLS LDAP server at {HOST}:{LDAP_PORT}/{LDAPS_PORT} (attach mode)")
            report()
            return 0

    try:
        if not attach:
            try:
                wait_ready()
            except SystemExit as cause:
                mark_all("FAIL" if REQUIRE else "SKIP", str(cause))
                report()
                return 0

        binary = default_binary()
        if not os.path.exists(binary):
            mark_all("FAIL" if REQUIRE else "SKIP", f"sidecar binary not found: {binary} (set DBX_PLUGIN_SIDECAR)")
            report()
            return 0

        data_dir = DATA_DIR or tempfile.mkdtemp(prefix="dbx-ldap-auth-smoke-data-")
        CURRENT_DATA_DIR["path"] = data_dir
        client = SidecarClient.start(data_dir=data_dir or None)
        try:
            try:
                client.initialize()
            except (SidecarError, AssertionError) as cause:
                mark_all("FAIL" if REQUIRE else "SKIP", f"plugin/initialize failed: {cause}")
                report()
                return 0
            for _, _, fn in STEPS:
                fn(client)
        finally:
            for connection_id in ("smoke-tls-test", "smoke-tls-main", "smoke-starttls", "smoke-tls-strict",
                                  "smoke-tls-mutex", "smoke-external", "smoke-digest", "smoke-ntlm",
                                  "smoke-ntlmhash-empty", "smoke-ntlmhash", "smoke-krb-nocfg", "smoke-krb-deadkdc"):
                try:
                    client.request("connection/disconnect", {"connection": {"id": connection_id}})
                except Exception:
                    pass
            try:
                client.close()
            except Exception:
                pass
    finally:
        if not attach and not args.keep:
            compose("down", "-v", check=False, env=compose_env)
            if cert_dir is not None:
                subprocess.run(["rm", "-rf", str(cert_dir)], check=False)

    report()
    return 1 if any(result.status == "FAIL" for result in RESULTS) else 0


def shutil_which(binary: str) -> str | None:
    from shutil import which
    return which(binary)


if __name__ == "__main__":
    raise SystemExit(main())
