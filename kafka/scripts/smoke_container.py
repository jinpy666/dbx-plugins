#!/usr/bin/env python3
"""Container orchestration for the Kafka smoke test (scenarios S1-S10).

Wraps docker-compose.kafka-test.yml (apache/kafka KRaft single node on
127.0.0.1:9092):

    python3 scripts/smoke_container.py [--keep]

* PLAINTEXT test container: no credentials exist, so nothing sensitive is
  passed through the environment (if a SASL/TLS variant is added, every
  secret must be exported before invoking docker compose);
* readiness is probed with a real metadata request (kafka-topics.sh --list
  inside the container), never a bare port probe;
* topics are seeded via scripts/kafka-seed/seed-topics.sh (idempotent,
  no credentials);
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
COMPOSE_FILE = REPO / "docker-compose.kafka-test.yml"
CONTAINER = "dbx-kafka-test"
HOST = "127.0.0.1"
PORT = 9092
INNER_BOOTSTRAP = "localhost:9092"
READY_TIMEOUT_SECS = 120


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


def metadata_probe() -> subprocess.CompletedProcess:
    """Real metadata/list request inside the container (not a port probe)."""
    return subprocess.run(
        [
            "docker", "exec", CONTAINER,
            "/opt/kafka/bin/kafka-topics.sh",
            "--bootstrap-server", INNER_BOOTSTRAP,
            "--list",
        ],
        capture_output=True,
    )


def wait_ready() -> None:
    deadline = time.monotonic() + READY_TIMEOUT_SECS
    while time.monotonic() < deadline:
        if port_open():
            result = metadata_probe()
            if result.returncode == 0:
                time.sleep(1)
                return
        time.sleep(2)
    raise SystemExit(f"Kafka container not answering metadata requests on {HOST}:{PORT} after {READY_TIMEOUT_SECS}s")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--keep", action="store_true", help="leave the container running")
    args = parser.parse_args()

    compose("up", "-d")
    try:
        wait_ready()
        sh(["bash", str(REPO / "scripts" / "kafka-seed" / "seed-topics.sh")])
        env = os.environ.copy()
        env.update(
            KAFKA_TEST_HOST=HOST,
            KAFKA_TEST_PORT=str(PORT),
        )
        result = sh([sys.executable, str(REPO / "scripts" / "smoke_test.py")], env=env)
        return result.returncode
    finally:
        if not args.keep:
            compose("down", "-v", check=False)


if __name__ == "__main__":
    raise SystemExit(main())
