#!/usr/bin/env python3
"""ldapi:// (unix socket) real-container smoke for dbx-plugin-ldap.

Orchestration (self-contained, follows smoke_auth_test.py conventions):
  1. cross-compile the sidecar as a static linux/arm64 binary (go required);
  2. start bitnami/openldap (slapd listens on `ldapi:/// ldap://:1389/`);
  3. `docker cp` the binary into the container and drive it over
     `docker exec -i … /tmp/sidecar` using the NDJSON SidecarClient — the
     sidecar runs on the same host namespace as slapd, so the ldapi socket
     is a real AF_UNIX endpoint (Docker Desktop socket bind-mounts are not
     host-connectable, hence the in-container topology);
  4. scenarios (all auth_type=external, zero credentials on the wire):
       L1  connection/test over ldapi:///                 -> success
       L2  connect + ldap/search (sub)                    -> count >= 1
       L3  ldap/rootDse over ldapi                        -> attributes non-empty

Credentials: the container admin password is randomly generated, only passed
via environment to the container, never printed or written to files. SASL
EXTERNAL carries no credentials at all.

Usage:
    python3 scripts/smoke_ldapi_test.py [--keep]
"""

from __future__ import annotations

import argparse
import os
import pathlib
import secrets
import shutil
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
from sidecar_client_jsonl import SidecarClient, SidecarError, lifecycle_params  # noqa: E402

REPO = pathlib.Path(__file__).resolve().parent.parent
CONTAINER = "dbx-ldap-ldapi-smoke"
LDAPI_SOCKET = "/opt/bitnami/openldap/var/run/ldapi"
ROOT_DN = "dc=example,dc=org"


@dataclass
class ScenarioResult:
    no: str
    name: str
    status: str  # PASS / FAIL / SKIP
    detail: str = ""


RESULTS: list[ScenarioResult] = []


def scenario(no: str, name: str):
    def decorate(fn):
        def run(*args, **kwargs):
            try:
                detail = fn(*args, **kwargs) or ""
            except SidecarError as cause:
                RESULTS.append(ScenarioResult(no, name, "FAIL", str(cause)))
                return
            except Exception as cause:  # unexpected crash counts as FAIL
                RESULTS.append(ScenarioResult(no, name, "FAIL", f"unexpected: {cause}"))
                return
            RESULTS.append(ScenarioResult(no, name, "PASS", detail))
        return run
    return decorate


# -- provisioning --------------------------------------------------------------


def build_linux_sidecar(out_path: pathlib.Path) -> None:
    subprocess.run(
        ["go", "build", "-ldflags=-s -w", "-o", str(out_path), "."],
        cwd=REPO / "backend",
        env={**os.environ, "CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": "arm64"},
        check=True,
    )


def start_container(admin_password: str) -> None:
    subprocess.run(
        [
            "docker", "run", "--rm", "-d", "--name", CONTAINER,
            "-e", f"LDAP_ADMIN_USERNAME=admin",
            "-e", f"LDAP_ADMIN_PASSWORD={admin_password}",
            "-e", f"LDAP_ROOT={ROOT_DN}",
            "bitnami/openldap:latest",
        ],
        check=True,
        stdout=subprocess.DEVNULL,
    )


def wait_for_socket(timeout: float = 90.0) -> None:
    """Wait until the ldapi socket exists AND stays put.

    The bitnami entrypoint starts slapd twice (a transient instance during
    tree bootstrap, then the final one). The socket disappears between the
    two, so require it to be present on three probes spaced ~2s apart.
    """
    deadline = time.monotonic() + timeout
    hits = 0
    while time.monotonic() < deadline:
        probe = subprocess.run(
            ["docker", "exec", CONTAINER, "bash", "-c", f"[ -S {LDAPI_SOCKET} ] && [ -f $(dirname {LDAPI_SOCKET})/slapd.pid ] && echo up"],
            capture_output=True,
        )
        hits = hits + 1 if b"up" in probe.stdout else 0
        if hits >= 3:
            return
        time.sleep(2.0)
    raise RuntimeError(f"ldapi socket {LDAPI_SOCKET} not stable within {timeout:.0f}s")


def connect_sidecar(data_dir: str) -> SidecarClient:
    process = subprocess.Popen(
        ["docker", "exec", "-i", "-e", f"DBX_PLUGIN_DATA_DIR={data_dir}", CONTAINER, "/tmp/sidecar"],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
    )
    return SidecarClient(process, timeout=30.0)


def lifecycle(ldapi_url: str) -> dict:
    connection = {
        "id": "ldapi-e2e",
        "name": "ldapi-e2e",
        "host": ldapi_url,
        "external_config": {"auth_type": "external", "base_dn": ROOT_DN, "timeout_secs": 8},
    }
    # runtime.host 为空：ldapi 拨号不走网络层，socket 路径取 URL Path（go-ldap 语义）。
    return lifecycle_params(connection) | {"runtime": {"host": "", "port": 0}}


# -- scenarios -----------------------------------------------------------------


@scenario("L1", "connection/test over ldapi:/// (SASL EXTERNAL)")
def run_l1(client: SidecarClient, ldapi_url: str):
    result = client.request("connection/test", lifecycle(ldapi_url))
    if not result.get("success"):
        raise AssertionError(f"ldapi connection/test did not succeed: {result}")
    if "auth=external" not in str(result.get("message", "")):
        raise AssertionError(f"unexpected test message: {result}")
    return result.get("message", "")


@scenario("L2", "connect + ldap/search (sub) over ldapi")
def run_l2(client: SidecarClient, ldapi_url: str):
    client.request("connection/connect", lifecycle(ldapi_url))
    time.sleep(0.2)
    result = client.request(
        "ldap/search",
        {"connectionId": "ldapi-e2e", "filter": "(objectClass=*)", "scope": "sub"},
    )
    if result.get("count", 0) < 1:
        raise AssertionError(f"search over ldapi returned no entries: {result}")
    return f"count={result['count']}"


@scenario("L3", "ldap/rootDse over ldapi")
def run_l3(client: SidecarClient, ldapi_url: str):
    client.request("connection/connect", lifecycle(ldapi_url) | {"connection": lifecycle(ldapi_url)["connection"] | {"id": "ldapi-e2e-rdse"}})
    time.sleep(0.2)
    result = client.request("ldap/rootDse", {"connectionId": "ldapi-e2e-rdse"})
    if not result.get("attributes"):
        raise AssertionError(f"rootDse returned no attributes: {result}")
    return f"attributes={len(result['attributes'])}"


# -- main ----------------------------------------------------------------------


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--keep", action="store_true", help="keep the container for debugging")
    args = parser.parse_args()

    if shutil.which("go") is None or shutil.which("docker") is None:
        print("SKIP: go / docker unavailable on this host")
        return 0

    temp_dir = pathlib.Path(tempfile.mkdtemp(prefix="ldapi-smoke-"))
    container_started = False
    admin_password = secrets.token_urlsafe(24)  # 只经环境变量喂给容器，不落盘不打印
    try:
        binary = temp_dir / "dbx-plugin-ldap-linux-arm64"
        build_linux_sidecar(binary)
        start_container(admin_password)
        container_started = True
        wait_for_socket()
        subprocess.run(["docker", "cp", str(binary), f"{CONTAINER}:/tmp/sidecar"], check=True, stdout=subprocess.DEVNULL)

        client = connect_sidecar(str(temp_dir / "data"))
        try:
            client.initialize()
            ldapi_url = f"ldapi://{LDAPI_SOCKET}"
            for runner in (run_l1, run_l2, run_l3):
                runner(client, ldapi_url)
        finally:
            client.close()
    except Exception as cause:
        RESULTS.append(ScenarioResult(" provisioning", "provisioning", "FAIL", str(cause)))
    finally:
        if container_started and not args.keep:
            subprocess.run(["docker", "rm", "-f", CONTAINER], stdout=subprocess.DEVNULL)
            shutil.rmtree(temp_dir, ignore_errors=True)

    failures = 0
    for result in RESULTS:
        print(f"[{result.status}] {result.no} {result.name}" + (f" — {result.detail}" if result.detail and result.status != "PASS" else f" — {result.detail}" if result.detail else ""))
        if result.status == "FAIL":
            failures += 1
    print(f"\nldapi smoke: {sum(1 for r in RESULTS if r.status == 'PASS')} PASS, "
          f"{sum(1 for r in RESULTS if r.status == 'FAIL')} FAIL")
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
