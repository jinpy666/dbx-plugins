#!/usr/bin/env python3
"""Drive the dbx-plugin-kafka sidecar over its stdio-jsonl (NDJSON) protocol.

The Go SDK reads/writes one JSON-RPC 2.0 message per line (`\\n` terminated)
on stdout/stdin; stderr is a diagnostics channel. Interface mirrors the
framed `sidecar_client.py` from the ssh-sftp plugin:
`start / start_default / initialize / request / wait_event / close`.

Usage as a library:
    from sidecar_client_jsonl import SidecarClient, lifecycle_params
    client = SidecarClient.start_default()
    client.initialize()
    client.request("connection/test", lifecycle_params(connection))
"""

from __future__ import annotations

import json
import os
import subprocess
import time

PROTOCOL_VERSION = 1

# sentinel returned via on_event handling to signal "handled, keep waiting"
_EVENT_HANDLED = object()


def default_binary() -> str:
    env = os.environ.get("DBX_PLUGIN_SIDECAR")
    if env:
        return env
    # installed copy first, then a local build output
    home = os.path.expanduser("~")
    installed = (
        f"{home}/Library/Application Support/com.dbx.app/plugins/io.dbx.kafka/"
        f"versions/current/bin/darwin-arm64/dbx-plugin-kafka"
    )
    if os.path.exists(installed):
        return installed
    return "backend/bin/dbx-plugin-kafka"


class SidecarError(RuntimeError):
    def __init__(self, message: str, code: int | None = None):
        super().__init__(message)
        self.code = code


def is_method_not_registered(error: BaseException) -> bool:
    """True when the sidecar does not implement the method yet (SKIP, not FAIL)."""
    if isinstance(error, SidecarError) and error.code == -32601:
        return True
    text = str(error).lower()
    markers = (
        "not implemented",
        "unimplemented",
        "method not found",
        "unknown method",
        "no such method",
        "not registered",
        "-32601",
    )
    return any(marker in text for marker in markers)


class SidecarClient:
    def __init__(self, process: subprocess.Popen, timeout: float = 20.0):
        self.process = process
        self.timeout = timeout
        self.next_id = 1
        self.events: list[dict] = []
        self._pending: dict[int, dict] = {}

    @classmethod
    def start(cls, binary: str | None = None, data_dir: str | None = None, timeout: float = 20.0) -> "SidecarClient":
        binary = binary or default_binary()
        if not os.path.exists(binary):
            raise SidecarError(f"sidecar binary not found: {binary} (set DBX_PLUGIN_SIDECAR)")
        env = dict(os.environ)
        if data_dir:
            env["DBX_PLUGIN_DATA_DIR"] = data_dir
        process = subprocess.Popen(
            [binary],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=env,
        )
        return cls(process, timeout)

    # -- low-level NDJSON ----------------------------------------------------

    def _send_line(self, message: dict) -> None:
        assert self.process.stdin
        self.process.stdin.write((json.dumps(message, ensure_ascii=False) + "\n").encode())
        self.process.stdin.flush()

    def _read_line(self, deadline: float) -> dict | None:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise SidecarError("timeout waiting for sidecar line")
        line = self.process.stdout.readline()  # type: ignore[union-attr]
        if not line:
            return None
        text = line.decode(errors="replace").strip()
        if not text:
            return None
        try:
            return json.loads(text)
        except json.JSONDecodeError:
            # Non-protocol output on stdout; ignore defensively.
            return None

    def _pump(self, want_id: int | None = None, on_event=None) -> object:
        """Read lines until the response for want_id arrives; stash events."""
        deadline = time.monotonic() + self.timeout
        while True:
            message = self._read_line(deadline)
            if message is None:
                raise SidecarError("sidecar closed stdout (no line)")
            if want_id is not None and message.get("id") == want_id:
                if message.get("error") is not None:
                    error = message["error"]
                    data = error.get("data", "")
                    suffix = f": {data}" if data else ""
                    raise SidecarError(f"{error.get('message')}{suffix}", error.get("code"))
                return message.get("result")
            # notification / unrelated response
            self.events.append(message)
            if want_id is None:
                return None
            if on_event is not None:
                reply = on_event(message)
                if isinstance(reply, dict):
                    self.next_id += 1
                    sub = {
                        "jsonrpc": "2.0",
                        "id": self.next_id,
                        "method": reply["method"],
                        "params": reply.get("params", {}),
                    }
                    self._send_line(sub)
            # events arriving while a request is in flight must not consume
            # its timeout budget
            deadline = time.monotonic() + self.timeout

    # -- protocol ------------------------------------------------------------

    def initialize(self) -> dict:
        return self.request(
            "plugin/initialize",
            {"host": {"protocolVersions": [PROTOCOL_VERSION]}},
        )

    def request(self, method: str, params: dict | None = None, timeout: float | None = None,
                on_event=None) -> dict:
        """Send a request and wait for its response.

        on_event(message) is invoked synchronously for every notification that
        arrives while waiting; return a dict from it to send a follow-up
        request, keeping challenge/response flows single-threaded.
        """
        if timeout:
            previous, self.timeout = self.timeout, timeout
        self.next_id += 1
        request_id = self.next_id
        message = {"jsonrpc": "2.0", "id": request_id, "method": method, "params": params or {}}
        self._send_line(message)
        try:
            result = self._pump(request_id, on_event=on_event)
            return result if isinstance(result, dict) else {}
        finally:
            if timeout:
                self.timeout = previous

    def notify(self, method: str, params: dict | None = None) -> None:
        self._send_line({"jsonrpc": "2.0", "method": method, "params": params or {}})

    def wait_event(self, method: str, timeout: float = 10.0) -> dict | None:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            for event in self.events:
                if event.get("method") == method or str(event.get("params", {}).get("method", "")).endswith(method):
                    return event
            self.timeout = max(0.5, deadline - time.monotonic())
            try:
                self._pump(None)
            except SidecarError:
                break
        for event in self.events:
            if event.get("method") == method:
                return event
        return None

    def drain_stderr(self) -> str:
        try:
            return self.process.stderr.read().decode(errors="replace") if self.process.stderr else ""
        except Exception:
            return ""

    def close(self) -> None:
        try:
            if self.process.stdin:
                self.process.stdin.close()
            self.process.wait(timeout=5)
        except Exception:
            self.process.kill()


def lifecycle_params(connection: dict) -> dict:
    """Build connection lifecycle params the way the DBX host does.

    binding 落点：config → connection.external_config.<field>，secret →
    connection.connection_secrets.<field>（M0 §3.1）。Kafka 的 manifest
    没有 host/port 绑定字段（只有 bootstrap_servers → config），runtime
    host:port 取 bootstrap_servers 首个 host:port（sidecar 网络层经宿主
    传输层出口拨号；无隧道时同值直连）。
    """
    bootstrap = str((connection.get("external_config") or {}).get("bootstrap_servers", ""))
    first = bootstrap.replace(",", " ").split()[0] if bootstrap.strip() else ""
    host, _, port = first.partition(":")
    return {
        "provider": {"id": "io.dbx.kafka.connection", "databaseType": "kafka"},
        "connection": connection,
        "runtime": {"host": host, "port": int(port) if port.isdigit() else 9092},
    }


if __name__ == "__main__":
    client = SidecarClient.start()
    try:
        info = client.initialize()
        print(json.dumps(info, indent=2, ensure_ascii=False))
    finally:
        client.close()
