#!/usr/bin/env python3
"""Container orchestration for the LDAP smoke test (scenarios S1-S10).

Wraps docker-compose.ldap-test.yml (bitnami/openldap on 127.0.0.1:1389):

    export LDAP_ADMIN_PASSWORD=...          # required, never stored on disk
    python3 scripts/smoke_container.py [--keep]

* the admin password is only ever passed through the environment
  (compose variable substitution -> container env -> LDAP_TEST_BINDPW);
* seed data lives in scripts/ldap-seed/ (no credentials there);
* --keep leaves the container running for debugging, default tears it down.
"""

from __future__ import annotations

import argparse
import os
import socket
import subprocess
import sys
import time
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
COMPOSE_FILE = REPO / "docker-compose.ldap-test.yml"
CONTAINER = "dbx-ldap-test"
HOST = "127.0.0.1"
PORT = 1389
READY_TIMEOUT_SECS = 90


def sh(cmd: list[str], **kwargs) -> subprocess.CompletedProcess:
    print("+", " ".join(cmd), flush=True)
    return subprocess.run(cmd, **kwargs)


def compose(*args: str, check: bool = True) -> subprocess.CompletedProcess:
    return sh(["docker", "compose", "-f", str(COMPOSE_FILE), *args], check=check)


def port_open() -> bool:
    try:
        with socket.create_connection((HOST, PORT), timeout=1.0):
            return True
    except OSError:
        return False


def wait_ready() -> None:
    """Wait until slapd actually answers an admin bind (port-open alone is not
    enough: bitnami's bootstrap stops/restarts slapd while the port already
    accepts and then resets connections)."""
    admin_dn = f"cn=admin,{os.environ.get('LDAP_TEST_ROOT', 'dc=example,dc=org')}"
    probe = [
        "docker", "exec", CONTAINER, "ldapsearch", "-x",
        "-H", f"ldap://127.0.0.1:{PORT}",
        "-D", admin_dn,
        "-w", os.environ["LDAP_ADMIN_PASSWORD"],
        "-b", os.environ.get("LDAP_TEST_ROOT", "dc=example,dc=org"),
        "-s", "base", "(objectClass=*)", "1.1",
    ]
    deadline = time.monotonic() + READY_TIMEOUT_SECS
    while time.monotonic() < deadline:
        if port_open():
            result = subprocess.run(probe, capture_output=True)
            if result.returncode == 0:
                time.sleep(1)
                return
        time.sleep(1)
    raise SystemExit(f"LDAP container not serving admin bind on {HOST}:{PORT} after {READY_TIMEOUT_SECS}s")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--keep", action="store_true", help="leave the container running")
    args = parser.parse_args()

    if not os.environ.get("LDAP_ADMIN_PASSWORD"):
        print("error: export LDAP_ADMIN_PASSWORD before running this script", file=sys.stderr)
        return 2

    env = os.environ.copy()
    env.update(
        LDAP_TEST_HOST=HOST,
        LDAP_TEST_PORT=str(PORT),
        LDAP_TEST_ROOT="dc=example,dc=org",
        LDAP_TEST_BINDDN="cn=admin,dc=example,dc=org",
        LDAP_TEST_BINDPW=os.environ["LDAP_ADMIN_PASSWORD"],
    )

    compose("up", "-d")
    try:
        wait_ready()
        result = sh([sys.executable, str(REPO / "scripts" / "smoke_test.py")], env=env)
        return result.returncode
    finally:
        if not args.keep:
            compose("down", "-v", check=False)


if __name__ == "__main__":
    raise SystemExit(main())
