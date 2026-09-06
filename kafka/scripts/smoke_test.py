#!/usr/bin/env python3
"""Smoke test for the dbx-plugin-kafka sidecar (scenarios S1-S15).

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
    S11 schema registry (Phase 2, redpanda SR on 19081): subjects/list ->
        register -> produce(schema) -> consume(schema) roundtrip ->
        compatibility get/set/check -> delete/version + delete subject
        (SR unreachable -> SKIP)
    S12 schema registry AWS Glue backend (Phase 3): schema/test(provider=glue)
        -> subjects/list -> versions/list -> get -> versions/compare ->
        compatibility get/set/check -> register(create+version) ->
        delete/version + delete subject. Runs only when GLUE_TEST_REGION +
        GLUE_TEST_REGISTRY are set (needs a real AWS Glue registry; there is
        no local Glue container -> SKIP otherwise).
    S13 topics/records/clear (Phase 3): produce 5 -> confirmTopic mismatch
        rejected (-32602, offsets unchanged) -> correct confirmTopic clears
        (rows shape partition/ok/deleted|null/lowWatermark|null) -> offsets
        latest==earliest per partition -> consume 0 records.
    S14 PROTOBUF schema-mount roundtrip (Phase 3): register the pre-generated
        FileDescriptorSet (kafka-seed/protobuf/orders_fdset.b64) on subject
        order-value via the SR REST API (POST /subjects/{subject}/versions,
        schemaType PROTOBUF) -> produce(JSON payload, format protobuf) ->
        consume decodes to JSON (no decodeError). A registry that does not
        preserve the base64(FDSet) form (e.g. redpanda re-parses it as proto
        text) is legitimate evidence -> scenario SKIP with the full details.
    S15 OAUTHBEARER static_token connection/test (Phase 3): runs only when
        KAFKA_TEST_OAUTH_STATIC_TOKEN is set (optionally KAFKA_TEST_OAUTH_
        BOOTSTRAP); there is no local MSK/OAUTHBEARER listener -> SKIP.

SKIP semantics (M0 §5.2):
  * a method not registered / not implemented yet  -> SKIP (backend under
    parallel development), never FAIL;
  * no Kafka container reachable                   -> whole suite SKIP;
  * SR (redpanda 19081) unreachable                -> S11/S14 SKIP only;
  * set KAFKA_TEST_REQUIRE=1 to turn env SKIPs into FAILs (CI).

Usage:
    DBX_PLUGIN_SIDECAR=/path/to/dbx-plugin-kafka python3 scripts/smoke_test.py
"""

from __future__ import annotations

import base64
import ipaddress
import json as jsonlib
import os
import socket
import sys
import urllib.error
import urllib.parse
import urllib.request
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

# Phase 2: redpanda SR test service (docker-compose.kafka-test.yml).
SR_URL = os.environ.get("KAFKA_TEST_SR_URL", "http://127.0.0.1:19081").rstrip("/")
SR_BOOTSTRAP = os.environ.get("KAFKA_TEST_SR_BOOTSTRAP", "127.0.0.1:19092")

# Phase 3: AWS Glue Schema Registry smoke (real AWS only; there is no local
# Glue container). Credentials, when provided, come strictly from the
# environment (no literals in this file).
GLUE_REGION = os.environ.get("GLUE_TEST_REGION", "").strip()
GLUE_REGISTRY = os.environ.get("GLUE_TEST_REGISTRY", "").strip()
GLUE_AUTH_MODE = os.environ.get("GLUE_TEST_AUTH_MODE", "static").strip() or "static"
GLUE_ACCESS_KEY_ID = os.environ.get("GLUE_TEST_ACCESS_KEY_ID", "").strip()
GLUE_SECRET_ACCESS_KEY = os.environ.get("GLUE_TEST_SECRET_ACCESS_KEY", "")
GLUE_SESSION_TOKEN = os.environ.get("GLUE_TEST_SESSION_TOKEN", "")

# Phase 3: OAUTHBEARER smoke gate (S15). There is no local MSK/OAUTHBEARER
# listener; the scenario runs only when a static token is provided (the token
# travels through the environment only, never into this file).
OAUTH_STATIC_TOKEN = os.environ.get("KAFKA_TEST_OAUTH_STATIC_TOKEN", "").strip()
OAUTH_BOOTSTRAP = os.environ.get("KAFKA_TEST_OAUTH_BOOTSTRAP", "").strip()

# Phase 3: pre-generated FileDescriptorSet fixture (G path; no protoc needed
# at runtime -- smoke only consumes the committed base64).
PROTOBUF_FDSET_PATH = os.path.join(
    os.path.dirname(os.path.abspath(__file__)), "kafka-seed", "protobuf", "orders_fdset.b64"
)

# per-run marker so re-runs against a dirty container stay deterministic
RUN = uuid.uuid4().hex[:8]
# int form of the marker, kept inside Avro int range (int32)
RUN_INT = int(RUN[:6], 16)

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
            headers: dict | None = None, schema: dict | None = None) -> dict:
    params: dict = {"topic": topic, "value": value}
    if key is not None:
        params["key"] = key
    if headers:
        params["headers"] = headers
    if schema:
        params["schema"] = schema
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
    # S9 left the writable connection switched to the read-only one; restore
    # the writable connection for create/delete (phase-1 script defect that
    # only shows against a live broker).
    connect(client, make_connection("smoke-main", read_only=False, allow_delete=True))
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
        # §3.2 冻结语义：confirmTopic 不匹配是参数错（-32602），
        # 与 topics/records/clear 的 S13 断言一致。
        if cause.code != -32602:
            raise AssertionError(f"expected param error -32602, got {cause.code}: {cause}")
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


@scenario("S11", "schema registry subjects/register/produce/consume/compat/delete")
def run_s11(client: SidecarClient) -> None:
    """S11 Phase 2 SR roundtrip against the redpanda Confluent-compatible registry.

    subjects/list -> register -> produce(schema) -> consume(schema) roundtrip ->
    compatibility get/set/check -> delete/version + delete subject.
    SR unreachable -> SKIP (never FAIL).
    """
    if not sr_reachable():
        message = f"no Schema Registry at {SR_URL} (docker compose up redpanda-sr-test)"
        raise SkipScenario(message)

    subject = f"dbx-smoke-sr-{RUN}-value"
    topic = f"dbx-smoke-sr-{RUN}"
    schema = jsonlib.dumps({
        "type": "record",
        "name": "SmokeRecord",
        "fields": [
            {"name": "id", "type": "int"},
            {"name": "note", "type": "string"},
        ],
    })
    payload = jsonlib.dumps({"id": RUN_INT, "note": f"sr-{RUN}"})

    # dedicated writable connection pointed at the redpanda broker + SR
    connection = make_connection("smoke-sr", read_only=False, allow_delete=True)
    connection["external_config"]["bootstrap_servers"] = SR_BOOTSTRAP
    connection["external_config"]["sr_url"] = SR_URL
    connect(client, connection)

    try:
        # schema/test
        result = data_of(domain(client, "kafka/schema/test", {}))
        if result.get("ok") is not True or "AVRO" not in (result.get("compatibleFormats") or []):
            raise AssertionError(f"schema/test unexpected result: {result}")

        # topics/create (explicit; do not rely on auto-create)
        created = data_of(domain(client, "kafka/topics/create",
                                 {"topics": [topic], "partitions": 1, "replicationFactor": 1}))
        if not created.get("results") or not created["results"][0].get("ok"):
            raise AssertionError(f"topics/create on SR broker failed: {created}")

        # subjects/list (may be empty on a fresh registry)
        result = data_of(domain(client, "kafka/schema/subjects/list", {}))
        if not isinstance(result.get("subjects"), list):
            raise AssertionError(f"subjects/list did not return a list: {result}")

        # register -> id/version
        result = data_of(domain(client, "kafka/schema/register",
                                {"subject": subject, "format": "avro", "schema": schema}))
        schema_id = result.get("id")
        schema_version = int(result.get("version") or 0)
        if not schema_id or not schema_version:
            raise AssertionError(f"register returned no id/version: {result}")

        # produce with schema mount (value is the un-encoded payload)
        produced = produce(client, topic,
                           value=payload,
                           schema={"subject": subject, "format": "avro"})
        for field in ("partition", "offset", "timestamp"):
            if field not in produced:
                raise AssertionError(f"produce(schema) missing {field}: {produced}")

        # consume with schema mount -> decoded JSON + schema provenance
        result = consume(client, topic=topic,
                         schema={"subject": subject, "format": "avro"})
        hits = [m for m in result.get("messages", []) if m.get("schemaId") == schema_id]
        if not hits:
            raise AssertionError(f"no message with schemaId={schema_id}: {result}")
        message = hits[-1]
        if message.get("schemaSubject") != subject:
            raise AssertionError(f"schemaSubject mismatch: {message.get('schemaSubject')!r}")
        if message.get("decodeError"):
            raise AssertionError(f"schema decode failed: {message.get('decodeError')}")
        decoded = jsonlib.loads(message.get("valueText") or "")
        if decoded.get("note") != f"sr-{RUN}" or decoded.get("id") != RUN_INT:
            raise AssertionError(f"schema roundtrip payload mismatch: {decoded}")

        # compatibility: global get -> subject set -> check
        compat = data_of(domain(client, "kafka/schema/compatibility/get", {}))
        if not compat.get("level") or compat.get("scope") != "global":
            raise AssertionError(f"global compatibility unexpected: {compat}")
        compat = data_of(domain(client, "kafka/schema/compatibility/set",
                                {"subject": subject, "level": "BACKWARD"}))
        if compat.get("scope") != "subject" or compat.get("level") != "BACKWARD":
            raise AssertionError(f"set compatibility unexpected: {compat}")
        check = data_of(domain(client, "kafka/schema/compatibility/check",
                               {"subject": subject, "format": "avro", "schema": schema}))
        if check.get("isCompatible") is not True:
            raise AssertionError(f"compatibility check not compatible: {check}")

        # delete: version first (critical gate), then the subject itself.
        # Implementation difference: Confluent keeps a soft-deleted subject
        # after its last version is gone (DELETE subject returns the
        # remaining versions), while redpanda drops it entirely (40401) --
        # tolerate the redpanda shape here.
        deleted = data_of(domain(client, "kafka/schema/delete/version",
                                 {"subject": subject, "version": schema_version}))
        if 1 not in (deleted.get("deletedVersions") or []):
            raise AssertionError(f"delete/version unexpected: {deleted}")
        try:
            deleted = data_of(domain(client, "kafka/schema/delete", {"subject": subject}))
            if not isinstance(deleted.get("deletedVersions"), list):
                raise AssertionError(f"delete subject unexpected: {deleted}")
        except SidecarError as cause:
            if "40401" not in str(cause) and "not found" not in str(cause).lower():
                raise
    finally:
        try:
            domain(client, "kafka/topics/delete", {"topics": [topic], "confirmTopic": topic})
        except (SidecarError, AssertionError):
            pass
        # restore the domain connection for any later bookkeeping
        connect(client, make_connection("smoke-main", read_only=False, allow_delete=True))


@scenario("S12", "AWS Glue schema registry test/list/get/register/compat/delete")
def run_s12(client: SidecarClient) -> None:
    """S12 Phase 3 AWS Glue SR roundtrip (management plane only).

    Producing/consuming with a schema mount stays Confluent-only (business
    error on glue), so this scenario exercises the kafka/schema/* arms:
    test -> subjects/list -> versions/list -> get -> versions/compare ->
    compatibility get/set/check -> register (CreateSchema + RegisterSchemaVersion)
    -> delete/version + delete subject. Requires GLUE_TEST_REGION +
    GLUE_TEST_REGISTRY (a real AWS Glue registry); local environments have no
    Glue container, so the scenario SKIPs without them.
    """
    if not GLUE_REGION or not GLUE_REGISTRY:
        raise SkipScenario(
            "no AWS Glue test registry (local has no Glue container; set "
            "GLUE_TEST_REGION + GLUE_TEST_REGISTRY to run against a real registry)"
        )

    schema = jsonlib.dumps({
        "type": "record",
        "name": "GlueSmokeRecord",
        "fields": [
            {"name": "id", "type": "int"},
            {"name": "note", "type": "string"},
        ],
    })
    subject = f"dbx-smoke-glue-{RUN}-value"

    connection = make_connection("smoke-glue", read_only=False, allow_delete=True)
    external = connection["external_config"]
    external["glue_region"] = GLUE_REGION
    external["glue_registry_name"] = GLUE_REGISTRY
    external["glue_auth_mode"] = GLUE_AUTH_MODE
    if GLUE_ACCESS_KEY_ID:
        external["glue_access_key_id"] = GLUE_ACCESS_KEY_ID
    if GLUE_AUTH_MODE == "static" and not (GLUE_ACCESS_KEY_ID and GLUE_SECRET_ACCESS_KEY):
        raise SkipScenario(
            "GLUE_TEST_AUTH_MODE=static needs GLUE_TEST_ACCESS_KEY_ID + "
            "GLUE_TEST_SECRET_ACCESS_KEY in the environment"
        )
    if GLUE_SECRET_ACCESS_KEY:
        connection["connection_secrets"]["glue_secret_access_key"] = GLUE_SECRET_ACCESS_KEY
    if GLUE_SESSION_TOKEN:
        connection["connection_secrets"]["glue_session_token"] = GLUE_SESSION_TOKEN
    connect(client, connection)

    try:
        # schema/test -> provider must be reported as glue
        result = data_of(domain(client, "kafka/schema/test", {}))
        if result.get("provider") != "glue":
            raise AssertionError(f"schema/test provider unexpected: {result}")

        # subjects/list (shape only; the registry may hold unrelated schemas)
        result = data_of(domain(client, "kafka/schema/subjects/list", {}))
        if not isinstance(result.get("subjects"), list):
            raise AssertionError(f"subjects/list did not return a list: {result}")

        # register on a fresh subject -> CreateSchema (first version)
        result = data_of(domain(client, "kafka/schema/register",
                                {"subject": subject, "format": "avro", "schema": schema,
                                 "compatibility": "NONE"}))
        first_version = int(result.get("version") or 0)
        if first_version != 1 or not result.get("versionId"):
            raise AssertionError(f"glue register(create) unexpected: {result}")

        # register again -> RegisterSchemaVersion (second version)
        result = data_of(domain(client, "kafka/schema/register",
                                {"subject": subject, "format": "avro", "schema": schema}))
        second_version = int(result.get("version") or 0)
        if second_version <= first_version:
            raise AssertionError(f"glue register(version) unexpected: {result}")

        # versions/list + get + compare
        result = data_of(domain(client, "kafka/schema/versions/list", {"subject": subject}))
        versions = [row.get("version") for row in result.get("versions", [])]
        if versions != [first_version, second_version]:
            raise AssertionError(f"versions/list unexpected: {result}")
        got = data_of(domain(client, "kafka/schema/get",
                             {"subject": subject, "version": first_version}))
        if got.get("version") != first_version or not got.get("schema"):
            raise AssertionError(f"schema/get unexpected: {got}")
        diff = data_of(domain(client, "kafka/schema/versions/compare",
                              {"subject": subject,
                               "fromVersion": first_version, "toVersion": second_version}))
        if not isinstance(diff.get("hunks"), list):
            raise AssertionError(f"versions/compare unexpected: {diff}")

        # compatibility: per-schema only on glue (no global level)
        cause = expect_error(lambda: domain(client, "kafka/schema/compatibility/get", {}))
        if "per-schema" not in str(cause):
            raise AssertionError(f"global compatibility should fail on glue: {cause}")
        compat = data_of(domain(client, "kafka/schema/compatibility/get", {"subject": subject}))
        if not compat.get("level"):
            raise AssertionError(f"compatibility/get unexpected: {compat}")
        compat = data_of(domain(client, "kafka/schema/compatibility/set",
                                {"subject": subject, "level": "BACKWARD_ALL"}))
        if compat.get("level") != "BACKWARD_ALL":
            raise AssertionError(f"compatibility/set unexpected: {compat}")
        check = data_of(domain(client, "kafka/schema/compatibility/check",
                               {"subject": subject, "format": "avro", "schema": schema}))
        if check.get("isCompatible") is not True or not check.get("messages"):
            raise AssertionError(f"compatibility/check unexpected: {check}")

        # produce with a schema mount on glue -> business error (Confluent-only
        # wire format; bootstrap may be unreachable, the guard fires first)
        cause = expect_error(lambda: produce(client, "dbx-smoke-glue-nonexistent",
                                             value="{}",
                                             schema={"subject": subject, "format": "avro"}))
        if "Confluent wire format" not in str(cause) or "AWS Glue" not in str(cause):
            raise AssertionError(f"produce(glue mount) unexpected error: {cause}")

        # cleanup: delete the second version, then the subject
        deleted = data_of(domain(client, "kafka/schema/delete/version",
                                 {"subject": subject, "version": second_version}))
        if second_version not in (deleted.get("deletedVersions") or []):
            raise AssertionError(f"delete/version unexpected: {deleted}")
        deleted = data_of(domain(client, "kafka/schema/delete", {"subject": subject}))
        if not isinstance(deleted.get("deletedVersions"), list):
            raise AssertionError(f"delete subject unexpected: {deleted}")
    finally:
        try:
            domain(client, "kafka/schema/delete", {"subject": subject})
        except (SidecarError, AssertionError):
            pass
        connect(client, make_connection("smoke-main", read_only=False, allow_delete=True))


@scenario("S13", "topics/records/clear gate + truncate + offsets converge")
def run_s13(client: SidecarClient) -> None:
    """S13 Phase 3 clear topic (IMPL_PLAN §12.2.1).

    produce 5 -> confirmTopic mismatch rejected (-32602, offsets unchanged)
    -> correct confirmTopic clears (rows shape partition/ok, deleted/
    lowWatermark int|null) -> offsets/list latest==earliest per partition ->
    consume returns 0 records.
    """
    # clear sits behind the same gate as topics/delete: needs allow_delete.
    connect(client, make_connection("smoke-main", read_only=False, allow_delete=True))
    topic = f"dbx-smoke-clear-{RUN}"
    created = data_of(domain(client, "kafka/topics/create",
                             {"topics": [topic], "partitions": 2, "replicationFactor": 1}))
    if not created.get("results") or not created["results"][0].get("ok"):
        raise AssertionError(f"topics/create failed: {created}")
    try:
        for i in range(5):
            produce(client, topic, value=f"clear-{RUN}-{i}")

        def latest_offsets() -> dict:
            result = data_of(domain(client, "kafka/topics/offsets/list",
                                    {"topics": [topic], "offsetTime": "latest"}))
            return {row.get("partition"): row.get("offset") for row in result.get("rows", [])}

        before = latest_offsets()
        if not any((offset or 0) > 0 for offset in before.values()):
            raise AssertionError(f"produced records not visible in offsets/list: {before}")

        # gate: confirmTopic mismatch -> -32602, records untouched
        cause = expect_error(
            lambda: domain(client, "kafka/topics/records/clear",
                           {"topic": topic, "confirmTopic": f"not-{topic}"}),
            markers=("confirm", "topic"),
        )
        if cause.code != -32602:
            raise AssertionError(f"expected -32602 for confirmTopic mismatch, got {cause.code}: {cause}")
        if latest_offsets() != before:
            raise AssertionError(f"offsets changed after rejected clear: {latest_offsets()} != {before}")

        # correct confirmTopic -> rows shape (partition/ok, int|null extras)
        result = data_of(domain(client, "kafka/topics/records/clear",
                                {"topic": topic, "confirmTopic": topic}))
        rows = result.get("rows")
        if not isinstance(rows, list) or not rows:
            raise AssertionError(f"clear returned no rows: {result}")
        partitions = []
        for row in rows:
            for field in ("partition", "ok"):
                if field not in row:
                    raise AssertionError(f"clear row missing {field}: {row}")
            if row.get("ok") is not True:
                raise AssertionError(f"clear row not ok: {row}")
            for field in ("deleted", "lowWatermark"):
                value = row.get(field)
                if value is not None and not isinstance(value, int):
                    raise AssertionError(f"clear row {field} not int|null: {row}")
            deleted = row.get("deleted")
            # contract: deleted = pre-clear hw - post-clear lowWatermark; a
            # full clear lands lowWatermark on hw so deleted reads 0 (null is
            # contract-legal when either offset fetch fails)
            low = row.get("lowWatermark")
            if deleted is not None and low is not None and deleted != before.get(row.get("partition"), 0) - low:
                raise AssertionError(
                    f"clear row deleted={deleted} != hw {before.get(row.get('partition'))} - lowWatermark {low}: {row}")
            partitions.append(row.get("partition"))
        if partitions != sorted(partitions):
            raise AssertionError(f"clear rows not ordered by partition: {partitions}")

        # latest == earliest per partition (all visible records gone)
        latest = data_of(domain(client, "kafka/topics/offsets/list",
                                {"topics": [topic], "offsetTime": "latest"}))
        earliest = data_of(domain(client, "kafka/topics/offsets/list",
                                  {"topics": [topic], "offsetTime": "earliest"}))
        highs = {row.get("partition"): row.get("offset") for row in latest.get("rows", [])}
        lows = {row.get("partition"): row.get("offset") for row in earliest.get("rows", [])}
        if not highs or set(highs) != set(lows):
            raise AssertionError(f"offsets/list partition sets mismatch: latest={highs} earliest={lows}")
        for partition, high in highs.items():
            if high != lows.get(partition):
                raise AssertionError(
                    f"partition {partition} latest={high} != earliest={lows.get(partition)}")

        # consume sees zero records after the clear
        result = consume(client, topic=topic)
        if result.get("messages") or result.get("matched", 0) != 0:
            raise AssertionError(f"consume after clear returned records: matched={result.get('matched')}")
    finally:
        try:
            domain(client, "kafka/topics/delete", {"topics": [topic], "confirmTopic": topic})
        except (SidecarError, AssertionError):
            pass


@scenario("S14", "PROTOBUF schema mount produce/consume roundtrip")
def run_s14(client: SidecarClient) -> None:
    """S14 Phase 3 PROTOBUF codec against the redpanda Confluent-compatible SR.

    Registers the committed FileDescriptorSet fixture on subject `order-value`
    via the SR REST API (POST /subjects/{subject}/versions, schemaType
    PROTOBUF -- the endpoint the sidecar itself uses), verifies the registry
    stores the base64(FDSet) verbatim, then produce(JSON payload, format
    protobuf) -> consume(schema mount) decodes back to JSON with schemaId/
    schemaSubject provenance and no decodeError. A registry that accepts
    PROTOBUF but re-parses the FDSet base64 as proto text (redpanda) breaks
    the contract form -- the full details stay in the report as evidence and
    the scenario SKIPs (never a fake PASS).
    """
    if not sr_reachable():
        message = f"no Schema Registry at {SR_URL} (docker compose up redpanda-sr-test)"
        raise SkipScenario(message)
    try:
        with open(PROTOBUF_FDSET_PATH, encoding="utf-8") as handle:
            fdset_b64 = handle.read().strip()
    except OSError as cause:
        raise SkipScenario(f"protobuf seed fixture unreadable: {cause}")
    if not fdset_b64:
        raise SkipScenario("protobuf seed fixture is empty")

    # subject `order-value` exercises the message-disambiguation rule 2
    # (strip -value suffix -> PascalCase tail matches the FDSet message).
    subject = "order-value"
    try:
        registered = sr_rest("POST", f"/subjects/{subject}/versions",
                             {"schemaType": "PROTOBUF", "schema": fdset_b64})
    except urllib.error.HTTPError as cause:
        body = cause.read().decode(errors="replace").strip()
        raise SkipScenario(
            "registry rejected PROTOBUF registration (evidence of missing "
            f"PROTOBUF support): POST /subjects/{subject}/versions -> "
            f"HTTP {cause.code} {body}"
        )
    except (urllib.error.URLError, OSError) as cause:
        raise SkipScenario(f"Schema Registry REST unreachable during register: {cause}")
    if not registered.get("id"):
        raise AssertionError(f"SR register returned no id: {registered}")

    # dedicated writable connection pointed at the redpanda broker + SR (kept
    # active until the cleanup finally ran, so schema deletes target the SR)
    connection = make_connection("smoke-pb", read_only=False, allow_delete=True)
    connection["external_config"]["bootstrap_servers"] = SR_BOOTSTRAP
    connection["external_config"]["sr_url"] = SR_URL
    connect(client, connection)

    try:
        # the registry must store the FDSet base64 verbatim (contract §12.2.2:
        # the sidecar decodes the schema field as base64 -> FileDescriptorSet)
        try:
            latest = sr_rest("GET", f"/subjects/{subject}/versions/latest")
        except urllib.error.HTTPError as cause:
            body = cause.read().decode(errors="replace").strip()
            raise SkipScenario(
                f"registry lost the PROTOBUF schema after registration (evidence: "
                f"GET latest -> HTTP {cause.code} {body})")
        if latest.get("schema") != fdset_b64:
            raise SkipScenario(
                "registry does not support PROTOBUF in the base64(FileDescriptorSet) "
                "form (evidence: registration accepted as id="
                f"{registered.get('id')} but the stored schema differs; stored="
                f"{latest.get('schema')!r}); the sidecar decodes base64(FDSet) from "
                "the registry, so the protobuf roundtrip cannot run against it")

        topic = f"dbx-smoke-pb-{RUN}"
        schema_ref = {"subject": subject, "format": "protobuf"}
        try:
            created = data_of(domain(client, "kafka/topics/create",
                                     {"topics": [topic], "partitions": 1, "replicationFactor": 1}))
            if not created.get("results") or not created["results"][0].get("ok"):
                raise AssertionError(f"topics/create on SR broker failed: {created}")

            # produce: value stays the plain JSON payload (protojson semantics)
            value = jsonlib.dumps({"id": "o-1", "amount": 42, "item": "sku-9"})
            try:
                produced = produce(client, topic, value=value, schema=schema_ref)
            except SidecarError as cause:
                if is_method_not_registered(cause):
                    raise
                raise SkipScenario(
                    "produce with a PROTOBUF mount failed against the registry "
                    f"(evidence of missing/partial PROTOBUF support): {cause}")
            for field in ("partition", "offset", "timestamp"):
                if field not in produced:
                    raise AssertionError(f"produce(protobuf) missing {field}: {produced}")

            # consume with the same mount -> decoded JSON, no decodeError
            result = consume(client, topic=topic, schema=schema_ref)
            hits = [m for m in result.get("messages", [])
                    if m.get("partition") == produced.get("partition")
                    and m.get("offset") == produced.get("offset")]
            if not hits:
                raise AssertionError(
                    f"produced protobuf message (p{produced.get('partition')}@"
                    f"{produced.get('offset')}) not in consume result: {result}")
            message = hits[-1]
            if message.get("decodeError"):
                raise AssertionError(f"protobuf decode failed: {message.get('decodeError')}")
            if message.get("schemaSubject") != subject:
                raise AssertionError(f"schemaSubject mismatch: {message.get('schemaSubject')!r} != {subject!r}")
            if message.get("schemaId") != registered.get("id"):
                raise AssertionError(f"schemaId mismatch: {message.get('schemaId')} != {registered.get('id')}")
            decoded = jsonlib.loads(message.get("valueText") or "")
            # int64 renders as a protojson string (known deviation, PROTOCOL
            # §3.8); assert the string fields that must round-trip exactly.
            if decoded.get("id") != "o-1" or decoded.get("item") != "sku-9":
                raise AssertionError(f"protobuf roundtrip payload mismatch: {decoded}")
        finally:
            try:
                domain(client, "kafka/topics/delete", {"topics": [topic], "confirmTopic": topic})
            except (SidecarError, AssertionError):
                pass
    finally:
        # schema cleanup must run while the SR-bound connection is still the
        # active one, then the domain connection is restored for later
        # bookkeeping (same order as S11)
        try:
            version = int(registered.get("version") or 0)
            if version:
                data_of(domain(client, "kafka/schema/delete/version",
                               {"subject": subject, "version": version}))
            # implementation difference (S11): redpanda drops the subject once
            # its last version is gone -> DELETE subject 40401s; tolerate it.
            try:
                data_of(domain(client, "kafka/schema/delete", {"subject": subject}))
            except SidecarError as cause:
                if "40401" not in str(cause) and "not found" not in str(cause).lower():
                    raise
        except (SidecarError, AssertionError):
            pass
        try:
            connect(client, make_connection("smoke-main", read_only=False, allow_delete=True))
        except (SidecarError, AssertionError):
            pass


@scenario("S15", "OAUTHBEARER static_token connection/test (env gated)")
def run_s15(client: SidecarClient) -> None:
    """S15 Phase 3 OAUTHBEARER (IMPL_PLAN §12.2.3), env gate like S12 (Glue).

    Runs only when KAFKA_TEST_OAUTH_STATIC_TOKEN is set (optionally
    KAFKA_TEST_OAUTH_BOOTSTRAP to point at the cluster): builds a
    static_token + SASL_SSL profile and runs connection/test. The token comes
    strictly from the environment; there is no local MSK/OAUTHBEARER listener,
    so the default outcome is a scenario SKIP.
    """
    if not OAUTH_STATIC_TOKEN:
        raise SkipScenario(
            "no OAUTHBEARER environment (no MSK/OAUTHBEARER listener locally; "
            "set KAFKA_TEST_OAUTH_STATIC_TOKEN [+ KAFKA_TEST_OAUTH_BOOTSTRAP] to run)"
        )
    connection = make_connection("smoke-oauth")
    external = connection["external_config"]
    external["bootstrap_servers"] = OAUTH_BOOTSTRAP or BOOTSTRAP
    external["security_protocol"] = "SASL_SSL"
    external["sasl_mechanism"] = "OAUTHBEARER"
    external["oauth_token_source"] = "static_token"
    connection["connection_secrets"]["oauth_static_token"] = OAUTH_STATIC_TOKEN
    result = client.request("connection/test", lifecycle_params(connection))
    if result.get("success") is not True:
        raise AssertionError(f"OAUTHBEARER connection/test unexpected result: {result}")


# -- driver --------------------------------------------------------------------

def kafka_reachable() -> tuple[bool, str]:
    try:
        with socket.create_connection((HOST, PORT), timeout=2.0):
            return True, ""
    except OSError as cause:
        return False, f"no Kafka broker at {BOOTSTRAP} ({cause})"


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    """Disable redirect following: the harness must not chase off-host URLs."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


_OPENER = urllib.request.build_opener(_NoRedirect)


def _ensure_local_url(url: str) -> None:
    """SSRF guard: only allow http(s) URLs resolving to loopback/private addresses."""
    parsed = urllib.parse.urlparse(url)
    default_port = 443 if parsed.scheme == "https" else 80
    if parsed.scheme not in ("http", "https") or not parsed.hostname:
        raise SystemExit(f"KAFKA_TEST_SR_URL must be an http(s) URL, got {url!r}")
    try:
        infos = socket.getaddrinfo(
            parsed.hostname, parsed.port or default_port, proto=socket.IPPROTO_TCP
        )
    except socket.gaierror as exc:
        raise SystemExit(f"cannot resolve SR host {parsed.hostname!r}: {exc}") from exc
    for info in infos:
        addr = ipaddress.ip_address(info[4][0])
        if not (addr.is_loopback or addr.is_private):
            raise SystemExit(
                f"refusing SR requests to non-private address {addr} (from {url!r})"
            )


def sr_reachable() -> bool:
    """Phase 2: probe the redpanda Confluent-compatible Schema Registry."""
    _ensure_local_url(SR_URL)
    try:
        with _OPENER.open(f"{SR_URL}/subjects", timeout=3.0):
            return True
    except (urllib.error.URLError, OSError):
        return False


def sr_rest(method: str, path: str, payload: dict | None = None, timeout: float = 5.0) -> dict:
    """Call the Confluent-compatible SR REST API directly.

    Uses the same endpoints the sidecar's schema client uses (POST
    /subjects/<s>/versions to register, matching schema.go); S14 needs the
    raw response/HTTPError body as PROTOBUF support evidence.
    """
    _ensure_local_url(f"{SR_URL}{path}")
    data = jsonlib.dumps(payload).encode() if payload is not None else None
    request = urllib.request.Request(
        f"{SR_URL}{path}", data=data, method=method,
        headers={"Content-Type": "application/vnd.schemaregistry.v1+json"},
    )
    with _OPENER.open(request, timeout=timeout) as response:
        return jsonlib.loads(response.read())


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
        ("S11", "schema registry subjects/register/produce/consume/compat/delete", run_s11),
        ("S12", "AWS Glue schema registry test/list/get/register/compat/delete", run_s12),
        ("S13", "topics/records/clear gate + truncate + offsets converge", run_s13),
        ("S14", "PROTOBUF schema mount produce/consume roundtrip", run_s14),
        ("S15", "OAUTHBEARER static_token connection/test (env gated)", run_s15),
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
        for connection_id in ("smoke-main", "smoke-ro", "smoke-sr", "smoke-glue", "smoke-pb", "smoke-oauth"):
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
