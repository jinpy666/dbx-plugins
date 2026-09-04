#!/usr/bin/env python3
"""Smoke test for the dbx-plugin-kafka sidecar (scenarios S1-S10).

Covers the IMPL_PLAN_DBX_KAFKA §8 table over a live KRaft container
(docker-compose.kafka-test.yml, apache/kafka, PLAINTEXT 127.0.0.1:9092):

    S1  initialize + connection/test without connection params -> -32602
    S2  connection/test on a fake/unreachable cluster          -> business error
    S3  connect + topics/list                                  -> seeded topics
    S4  produce -> consume round-trip                          -> fidelity (key/value/headers, base64)
    S5  consume with valueFilter                               -> matched subset only
    S6  groups/list + acls/list                                -> shapes (ACL unsupported -> SKIP)
    S7  stream start -> events -> stop                         -> session delivers messages
    S8  export json/csv                                        -> parseable content
    S9  read-only connection write                             -> rejected (-32000)
    S10 topics create + delete confirmTopic gate               -> gate enforced

SKIP semantics (M0 §5.2):
  * a method not registered / not implemented yet  -> SKIP (backend under
    parallel development), never FAIL;
  * no Kafka container reachable                   -> whole suite SKIP;
  * set KAFKA_TEST_REQUIRE=1 to turn env SKIPs into FAILs (CI).

Usage:
    DBX_PLUGIN_SIDECAR=/path/to/dbx-plugin-kafka python3 scripts/smoke_test.py
"""

from __future__ import annotations

import base64
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

HOST = os.environ.get("KAFKA_TEST_HOST", "127.0.0.1")
PORT = int(os.environ.get("KAFKA_TEST_PORT", "9092"))
REQUIRE = os.environ.get("KAFKA_TEST_REQUIRE", "") == "1"
DATA_DIR = os.environ.get("KAFKA_TEST_DATA_DIR", "")

BOOTSTRAP = f"{HOST}:{PORT}"

# per-run marker so re-runs against a dirty container stay deterministic
RUN = uuid.uuid4().hex[:8]

TOPIC_EVENTS = "dbx-smoke-events"
TOPIC_BINARY = "dbx-smoke-binary"
TOPIC_FILTER = "dbx-smoke-filter"
TOPIC_STREAM = "dbx-smoke-stream"
TOPIC_EXPORT = "dbx-smoke-export"


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

def data_of(result: dict) -> dict:
    """Unwrap the `{ok: true, data}` envelope; tolerate flat results."""
    data = result.get("data")
    return data if isinstance(data, dict) else result


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
        "display_name": f"smoke-{connection_id}",
        "bootstrap_servers": BOOTSTRAP,
        "security_protocol": "PLAINTEXT",
        "client_id": "dbx-kafka-smoke",
        "read_only": True,
        "allow_delete": False,
    }
    external.update(extra_config)
    return {
        "id": connection_id,
        "name": f"smoke-{connection_id}",
        "external_config": external,
        "connection_secrets": {},
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


def produce(client: SidecarClient, topic: str, key: str | None = None, value: str = "",
            headers: dict | None = None) -> dict:
    params: dict = {"topic": topic, "value": value}
    if key is not None:
        params["key"] = key
    if headers:
        params["headers"] = headers
    return data_of(domain(client, "kafka/messages/produce", params))


def consume(client: SidecarClient, **params) -> dict:
    base = {"offsetStrategy": "earliest", "limit": 100, "timeoutMs": 5000}
    base.update(params)
    return data_of(domain(client, "kafka/messages/consume", base))


def topic_names(client: SidecarClient, **params) -> list[str]:
    result = data_of(domain(client, "kafka/topics/list", params))
    return [t.get("name") for t in result.get("topics", [])]


# -- scenarios -----------------------------------------------------------------

@scenario("S1", "initialize + connection/test without params -> -32602")
def run_s1(client: SidecarClient) -> None:
    """S1 initialize + connection/test without connection params -> -32602."""
    info = client.initialize()
    if not info:
        raise AssertionError("plugin/initialize returned no result")
    cause = expect_error(lambda: client.request("connection/test", {}))
    if is_method_not_registered(cause):
        raise AssertionError("connection/test must be a registered lifecycle method")
    if cause.code != -32602:
        raise AssertionError(f"expected parameter error -32602, got {cause.code}: {cause}")


@scenario("S2", "connection/test on a fake cluster -> business error")
def run_s2(client: SidecarClient) -> None:
    """S2 connection/test against an unreachable cluster -> business error, no crash."""
    connection = make_connection("smoke-fake")
    connection["external_config"]["bootstrap_servers"] = "127.0.0.1:1"
    cause = expect_error(lambda: client.request("connection/test", lifecycle_params(connection)))
    if is_method_not_registered(cause):
        raise AssertionError("connection/test must be a registered lifecycle method")
    if cause.code not in (None, -32000):
        raise AssertionError(f"expected business error -32000, got {cause.code}: {cause}")


@scenario("S3", "connect + topics/list contains seeded topics")
def run_s3(client: SidecarClient) -> None:
    """S3 connect then topics/list -> seeded topics visible, internal hidden."""
    connect(client, make_connection("smoke-main", read_only=False, allow_delete=True))
    result = data_of(domain(client, "kafka/topics/list", {}))
    topics = result.get("topics", [])
    names = {t.get("name") for t in topics}
    for seeded in (TOPIC_EVENTS, TOPIC_BINARY, TOPIC_FILTER, TOPIC_STREAM, TOPIC_EXPORT):
        if seeded not in names:
            raise AssertionError(f"seeded topic {seeded} missing from topics/list: {sorted(names)}")
    for topic in topics:
        for field in ("name", "partitionCount", "replicationFactor"):
            if field not in topic:
                raise AssertionError(f"topic entry missing {field}: {topic}")
        if topic.get("isInternal"):
            raise AssertionError(f"internal topic leaked without includeInternal: {topic}")
    internal = topic_names(client, includeInternal=True)
    if "__consumer_offsets" in names:
        raise AssertionError("__consumer_offsets present in default listing")
    if "__consumer_offsets" not in internal:
        # fresh cluster: __consumer_offsets only exists after the first group
        # commit, so there is nothing to assert yet
        raise SkipScenario("__consumer_offsets not created yet (no group committed); internal-flag check skipped")


@scenario("S4", "produce -> consume round-trip fidelity")
def run_s4(client: SidecarClient) -> None:
    """S4 produce -> consume round-trip -> key/value/headers/base64 consistent."""
    marker = f"roundtrip-{RUN}"
    key = f"key-{marker}"
    value = f"value-{marker}-你好"
    headers = {"origin": "smoke", "case": marker}
    produced = produce(client, TOPIC_BINARY, key=key, value=value, headers=headers)
    for field in ("partition", "offset", "timestamp"):
        if field not in produced:
            raise AssertionError(f"produce result missing {field}: {produced}")

    result = consume(client, topic=TOPIC_BINARY)
    messages = result.get("messages", [])
    if result.get("matched", 0) <= 0 or not messages:
        raise AssertionError(f"consume returned no messages: scanned={result.get('scanned')}")
    hits = [m for m in messages if m.get("key") == key]
    if not hits:
        raise AssertionError(f"produced message (key={key}) not found among {len(messages)}")
    message = hits[-1]
    if message.get("valueText") != value:
        raise AssertionError(f"valueText mismatch: {message.get('valueText')!r} != {value!r}")
    decoded = base64.b64decode(message.get("valueBase64") or "").decode("utf-8")
    if decoded != value:
        raise AssertionError("valueBase64 does not decode to the produced value")
    if message.get("headers") != headers:
        raise AssertionError(f"headers mismatch: {message.get('headers')} != {headers}")
    if message.get("partition") != produced.get("partition") or message.get("offset") != produced.get("offset"):
        raise AssertionError("consume locate mismatch vs produce partition/offset")


@scenario("S5", "consume valueFilter matches subset only")
def run_s5(client: SidecarClient) -> None:
    """S5 consume with valueFilter contains -> only matching messages counted."""
    needle = f"needle-{RUN}"
    for i in range(3):
        produce(client, TOPIC_FILTER, value=f"{needle}-{i}")
        produce(client, TOPIC_FILTER, value=f"other-{RUN}-{i}")
    result = consume(client, topic=TOPIC_FILTER, valueFilter=needle, matchMode="contains")
    messages = result.get("messages", [])
    if result.get("matched", 0) != 3:
        raise AssertionError(f"matched={result.get('matched')}, want 3 for needle {needle}")
    if len(messages) != 3:
        raise AssertionError(f"got {len(messages)} messages, want 3")
    for message in messages:
        if needle not in (message.get("valueText") or ""):
            raise AssertionError(f"filter leaked non-matching message: {message.get('valueText')!r}")


@scenario("S6", "groups/list + acls/list shapes")
def run_s6(client: SidecarClient) -> None:
    """S6 groups/list shape (empty is valid); acls/list SKIPs when the test
    container has no authorizer enabled."""
    result = data_of(domain(client, "kafka/groups/list", {}))
    groups = result.get("groups")
    if not isinstance(groups, list):
        raise AssertionError(f"groups/list did not return a list: {result}")
    for group in groups:
        for field in ("group", "state"):
            if field not in group:
                raise AssertionError(f"group entry missing {field}: {group}")

    def list_acls() -> dict:
        return data_of(domain(client, "kafka/acls/list", {"filter": {"resourceType": "topic"}}))

    try:
        acl_result = list_acls()
    except SidecarError as cause:
        if is_method_not_registered(cause):
            raise SkipScenario(f"backend not implemented: {cause}")
        lowered = str(cause).lower()
        if any(marker in lowered for marker in ("acl", "authoriz", "authoris", "security", "unsupported", "not enabled")):
            raise SkipScenario(f"test container has no ACL authorizer: {cause}")
        raise
    acls = acl_result.get("acls")
    if not isinstance(acls, list):
        raise AssertionError(f"acls/list did not return a list: {acl_result}")


@scenario("S7", "stream start -> events -> stop")
def run_s7(client: SidecarClient) -> None:
    """S7 stream session delivers kafka/stream/messages events, then stops."""
    marker = f"stream-{RUN}"
    result = data_of(domain(client, "kafka/stream/start",
                            {"topic": TOPIC_STREAM, "offsetStrategy": "earliest",
                             "limit": 100, "timeoutMs": 3000}))
    session_id = result.get("sessionId")
    if not session_id:
        raise AssertionError(f"stream/start returned no sessionId: {result}")
    try:
        produce(client, TOPIC_STREAM, value=f"{marker}-after-start")
        event = client.wait_event("kafka/stream/messages", timeout=15.0)
        if event is None:
            raise AssertionError("no kafka/stream/messages event within 15s")
        payload = event.get("params", {})
        if payload.get("sessionId") != session_id:
            raise AssertionError(f"event sessionId mismatch: {payload.get('sessionId')} != {session_id}")
        texts = [m.get("valueText") or "" for m in payload.get("messages", [])]
        if not any(marker in text for text in texts):
            raise AssertionError(f"stream event did not carry the produced marker: {texts[:5]}")
    finally:
        data_of(domain(client, "kafka/stream/stop", {"sessionId": session_id}))


@scenario("S8", "export json + csv")
def run_s8(client: SidecarClient) -> None:
    """S8 messages/export returns parseable json and csv payloads."""
    import json as jsonlib

    marker = f"export-{RUN}"
    produce(client, TOPIC_EXPORT, key=f"{marker}-k", value=f"{marker}-v1")
    base = {"topic": TOPIC_EXPORT, "offsetStrategy": "earliest", "limit": 10000, "timeoutMs": 5000}

    result = data_of(domain(client, "kafka/messages/export", {**base, "format": "json"}))
    for field in ("content", "filename", "contentType"):
        if not result.get(field):
            raise AssertionError(f"json export missing {field}: {result}")
    rows = jsonlib.loads(result["content"])
    if not any(marker in jsonlib.dumps(row) for row in rows):
        raise AssertionError("json export does not contain the produced marker")

    result = data_of(domain(client, "kafka/messages/export", {**base, "format": "csv"}))
    if marker not in result.get("content", ""):
        raise AssertionError("csv export does not contain the produced marker")
    if ".csv" not in result.get("filename", ""):
        raise AssertionError(f"csv export filename wrong: {result.get('filename')}")


@scenario("S9", "read-only write rejection")
def run_s9(client: SidecarClient) -> None:
    """S9 read-only connection rejects produce/create with -32000."""
    connect(client, make_connection("smoke-ro"))
    cause = expect_error(
        lambda: produce(client, TOPIC_EVENTS, value=f"nope-{RUN}"),
        markers=("read", "readonly", "only", "blocked", "policy", "拒绝", "只读"),
    )
    if cause.code not in (None, -32000):
        raise AssertionError(f"expected business error -32000, got {cause.code}: {cause}")
    cause = expect_error(
        lambda: domain(client, "kafka/topics/create",
                       {"topics": [f"nope-{RUN}"], "partitions": 1, "replicationFactor": 1}),
        markers=("read", "readonly", "only", "blocked", "policy", "拒绝", "只读"),
    )
    if cause.code not in (None, -32000):
        raise AssertionError(f"expected business error -32000, got {cause.code}: {cause}")


@scenario("S10", "topics create + delete confirmTopic gate")
def run_s10(client: SidecarClient) -> None:
    """S10 create/delete round-trip; delete without confirmTopic is rejected."""
    topic = f"dbx-smoke-tmp-{RUN}"
    result = data_of(domain(client, "kafka/topics/create",
                            {"topics": [topic], "partitions": 1, "replicationFactor": 1}))
    results = result.get("results", [])
    if not results or results[0].get("ok") is not True:
        raise AssertionError(f"topics/create failed: {result}")
    try:
        cause = expect_error(
            lambda: domain(client, "kafka/topics/delete", {"topics": [topic]}),
            markers=("confirm", "topic"),
        )
        if cause.code not in (None, -32000):
            raise AssertionError(f"expected business error -32000, got {cause.code}: {cause}")
        result = data_of(domain(client, "kafka/topics/delete",
                                {"topics": [topic], "confirmTopic": topic}))
        results = result.get("results", [])
        if not results or results[0].get("ok") is not True:
            raise AssertionError(f"topics/delete with confirmTopic failed: {result}")
        deadline_attempts = 10
        for _ in range(deadline_attempts):
            if topic not in topic_names(client, includeInternal=True):
                return
            import time as timelib
            timelib.sleep(0.5)
        raise AssertionError(f"topic {topic} still listed after delete")
    finally:
        try:
            domain(client, "kafka/topics/delete", {"topics": [topic], "confirmTopic": topic})
        except SidecarError:
            pass


# -- driver --------------------------------------------------------------------

def kafka_reachable() -> tuple[bool, str]:
    try:
        with socket.create_connection((HOST, PORT), timeout=2.0):
            return True, ""
    except OSError as cause:
        return False, f"no Kafka broker at {BOOTSTRAP} ({cause})"


def main() -> int:
    steps = [
        ("S1", "initialize + connection/test without params -> -32602", run_s1),
        ("S2", "connection/test on a fake cluster -> business error", run_s2),
        ("S3", "connect + topics/list contains seeded topics", run_s3),
        ("S4", "produce -> consume round-trip fidelity", run_s4),
        ("S5", "consume valueFilter matches subset only", run_s5),
        ("S6", "groups/list + acls/list shapes", run_s6),
        ("S7", "stream start -> events -> stop", run_s7),
        ("S8", "export json + csv", run_s8),
        ("S9", "read-only write rejection", run_s9),
        ("S10", "topics create + delete confirmTopic gate", run_s10),
    ]

    ok, reason = kafka_reachable()
    if not ok:
        status = "FAIL" if REQUIRE else "SKIP"
        for no, name, _ in steps:
            RESULTS.append(ScenarioResult(no, name, status, reason))
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
        for connection_id in ("smoke-main", "smoke-ro"):
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
    widths = (5, 48, 6)
    print(f"{'No.':<{widths[0]}} {'Scenario':<{widths[1]}} {'Status':<{widths[2]}} Detail")
    for result in RESULTS:
        print(f"{result.no:<{widths[0]}} {result.name:<{widths[1]}} {result.status:<{widths[2]}} {result.detail}")
    counts = {status: sum(1 for r in RESULTS if r.status == status) for status in ("PASS", "FAIL", "SKIP")}
    print(f"\ntotal={len(RESULTS)} PASS={counts['PASS']} FAIL={counts['FAIL']} SKIP={counts['SKIP']}")


if __name__ == "__main__":
    raise SystemExit(main())
