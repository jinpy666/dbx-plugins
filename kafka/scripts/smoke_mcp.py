#!/usr/bin/env python3
"""MCP smoke test for the dbx-kafka-plugin sidecar (M3, scenarios K1-K11).

Covers the shared/IMPL_PLAN_PLUGIN_MCP.zh-CN.md (v2) §1/§2/§3/§4/§6.3 tool
face over the stdio-jsonl protocol (mcp/tools + mcp/call + mcp/settings,
ldap Go skeleton parity):

    K1  mcp/settings/get + set           -> defaults, partial update, invalid refused
    K2  mcp/tools lists the 11 kafka tools -> names + JSON Schema required fields
    K3  read-only connection tools list  -> all 4 write tools omitted with reasons
    K4  write tool on read-only conn     -> refused (-32000)
    K5  UI intent without a frontend     -> state=pending + digest fallback hint
    K6  UI intent with a report          -> state=applied + summary roundtrip
    K7  kafka_ui_state                   -> by intentId + latest snapshot (+ streams)
    K8  digest/cursor gates              -> unknown connection/cursor/topic refused
    K9  unknown tool / method            -> clear error (-32601 SKIP semantics)
    K10 allow_delete=false               -> only the delete-class tools are omitted
    K11 digest + cursor + produce + two-phase writes + audit source=mcp
        (needs the Kafka test container)

SKIP semantics (M0 §5.2, same as smoke_test.py):
  * method/tool not registered  -> SKIP (never FAIL);
  * no sidecar binary           -> whole suite SKIP (FAIL with KAFKA_TEST_REQUIRE=1);
  * K11 additionally SKIPs without a reachable Kafka test container.

Usage:
    DBX_PLUGIN_SIDECAR=/path/to/dbx-plugin-kafka python3 scripts/smoke_mcp.py
"""

from __future__ import annotations

import json
import os
import socket
import sys
import tempfile
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
BOOTSTRAP = f"{HOST}:{PORT}"
REQUIRE = os.environ.get("KAFKA_TEST_REQUIRE", "") == "1"

EXPECTED_TOOLS = [
    "kafka_ui_search",
    "kafka_ui_focus",
    "kafka_ui_select",
    "kafka_ui_state",
    "kafka_ui_topics",
    "kafka_messages_digest",
    "kafka_cursor_next",
    "kafka_messages_produce",
    "kafka_topics_delete",
    "kafka_groups_offsets_reset",
    "kafka_topics_records_clear",
]

WRITE_TOOLS = ["kafka_messages_produce", "kafka_topics_delete", "kafka_groups_offsets_reset", "kafka_topics_records_clear"]
DELETE_CLASS_TOOLS = ["kafka_topics_delete", "kafka_topics_records_clear"]


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
                note = fn(*args, **kwargs)
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
                RESULTS.append(ScenarioResult(no, name, "PASS", note or ""))
        run.no = no  # type: ignore[attr-defined]
        run.name = name  # type: ignore[attr-defined]
        return run
    return decorate


# -- helpers -------------------------------------------------------------------


def expect_error(fn, markers: tuple[str, ...] = ()) -> SidecarError:
    try:
        fn()
    except SidecarError as cause:
        lowered = str(cause).lower()
        if markers and not any(marker in lowered for marker in markers):
            raise AssertionError(f"error message mismatch: {cause}") from cause
        return cause
    raise AssertionError("expected a sidecar error but the call succeeded")


def unwrap(result: dict) -> dict:
    """mcp/call returns the MCP content envelope; unwrap the JSON text payload."""
    content = result.get("content") or []
    assert content and content[0].get("type") == "text", result
    return json.loads(content[0]["text"])


def call_tool(client: SidecarClient, tool: str, **arguments) -> dict:
    return unwrap(client.request("mcp/call", {"tool": tool, "arguments": arguments}))


def make_connection(connection_id: str, **extra_config) -> dict:
    external = {
        "display_name": f"smoke-mcp-{connection_id}",
        "bootstrap_servers": BOOTSTRAP,
        "security_protocol": "PLAINTEXT",
        "client_id": "dbx-kafka-smoke-mcp",
        "read_only": False,
        "allow_delete": True,
    }
    external.update(extra_config)
    return {
        "id": connection_id,
        "name": f"smoke-mcp-{connection_id}",
        "external_config": external,
        "connection_secrets": {},
    }


def report_intent_applied(message: dict) -> dict | None:
    """on_event hook: answer a kafka/ui/intent notification with a report."""
    if message.get("method") == "kafka/ui/intent":
        intent_id = (message.get("params") or {}).get("intentId", "")
        return {
            "method": "kafka/ui/state/report",
            "params": {
                "intentId": intent_id,
                "status": "applied",
                "summary": {
                    "count": 2,
                    "anchor": "order-events-p0-o42",
                    "rows": [{"partition": 0, "offset": 42}],
                },
            },
        }
    return None


def kafka_reachable() -> tuple[bool, str]:
    try:
        with socket.create_connection((HOST, PORT), timeout=2.0):
            return True, ""
    except OSError as cause:
        return False, f"no Kafka test container at {BOOTSTRAP}: {cause}"


# -- scenarios (offline: no Kafka server needed) --------------------------------


@scenario("K1", "mcp/settings get/set defaults + validation")
def k1_settings(client: SidecarClient) -> str:
    settings = client.request("mcp/settings/get")["settings"]
    assert settings["reportWaitMs"] == 5000, settings
    assert settings["responseLimitBytes"] == 16 * 1024, settings
    assert settings["digestGroupLimit"] == 20 and settings["digestTopN"] == 10, settings
    assert settings["digestScanLimit"] == 1000, settings
    updated = client.request("mcp/settings/set", {"reportWaitMs": 300, "cellWidth": 80})["settings"]
    assert updated["reportWaitMs"] == 300 and updated["cellWidth"] == 80, updated
    expect_error(lambda: client.request("mcp/settings/set", {"reportWaitMs": 0}), ("between 1 and",))
    expect_error(lambda: client.request("mcp/settings/set", {"reportWaitMs": "fast"}), ("positive integer",))
    expect_error(lambda: client.request("mcp/settings/set", {"digestScanLimit": 100001}), ("between 1 and",))
    # 白名单外字段容忍（部分更新语义），当前值不被污染。
    tolerated = client.request("mcp/settings/set", {"unknownField": 1})["settings"]
    assert tolerated["reportWaitMs"] == 300, tolerated
    return "defaults 5s/16KiB; partial update; invalid refused"


@scenario("K2", "mcp/tools lists the 11 kafka tools with schemas")
def k2_tools(client: SidecarClient) -> str:
    tools = client.request("mcp/tools")["tools"]
    names = [tool["name"] for tool in tools]
    for expected in EXPECTED_TOOLS:
        assert expected in names, f"{expected} missing from {names}"
    digest = next(tool for tool in tools if tool["name"] == "kafka_messages_digest")
    schema = digest["inputSchema"]
    assert schema["type"] == "object" and "topic" in schema["properties"], schema
    assert set(schema["required"]) >= {"connectionId", "topic"}, schema
    delete = next(tool for tool in tools if tool["name"] == "kafka_topics_delete")
    assert "confirmToken" in delete["inputSchema"]["properties"], delete
    return f"{len(tools)} tools with JSON Schema"


@scenario("K3", "read-only connection omits all write tools")
def k3_readonly_tools(client: SidecarClient) -> str:
    ro_id = "smoke-mcp-ro"
    client.request("connection/connect", lifecycle_params(make_connection(ro_id, read_only=True)))
    result = client.request("mcp/tools", {"connectionId": ro_id})
    names = [tool["name"] for tool in result["tools"]]
    for write_tool in WRITE_TOOLS:
        assert write_tool not in names, f"{write_tool} must be omitted"
    omitted = {row["name"]: row["reason"] for row in result.get("omittedWriteTools") or []}
    assert set(omitted) == set(WRITE_TOOLS), omitted
    # 全量清单（不带 connectionId）仍包含写工具。
    all_names = [tool["name"] for tool in client.request("mcp/tools")["tools"]]
    for write_tool in WRITE_TOOLS:
        assert write_tool in all_names
    return "4 write tools omitted with a reason for read-only"


@scenario("K4", "write tool refused on a read-only connection")
def k4_readonly_write(client: SidecarClient) -> str:
    ro_id = "smoke-mcp-ro"
    error = expect_error(
        lambda: client.request("mcp/call", {
            "tool": "kafka_messages_produce",
            "arguments": {"connectionId": ro_id, "topic": "t", "value": "v"},
        }),
        ("read-only",),
    )
    assert error.code == -32000, error.code
    return "mcp/call refused before any write"


@scenario("K5", "UI intent pends without a frontend")
def k5_intent_pending(client: SidecarClient) -> str:
    # K1 把 reportWaitMs 调到 300ms：无前端时快速收敛为 pending。
    result = call_tool(client, "kafka_ui_focus", panel="topics")
    assert result["state"] == "pending", result
    assert result["intentId"], result
    assert "kafka_messages_digest" in result.get("hint", ""), result
    return "pending + digest fallback hint"


@scenario("K6", "UI intent applied via the report callback")
def k6_intent_applied(client: SidecarClient) -> str:
    result = unwrap(client.request(
        "mcp/call",
        {"tool": "kafka_ui_search", "arguments": {"topic": "order-events", "limit": 100, "valueFilter": "x"}},
        on_event=report_intent_applied,
    ))
    assert result["state"] == "applied", result
    summary = result.get("summary") or {}
    assert summary.get("count") == 2 and summary.get("anchor"), result
    # 定位参数缺失的工具校验。
    expect_error(
        lambda: client.request("mcp/call", {"tool": "kafka_ui_select", "arguments": {}}),
        ("partition is required",),
    )
    return "applied + summary roundtrip over kafka/ui/state/report"


@scenario("K7", "kafka_ui_state reads intent result and snapshot")
def k7_ui_state(client: SidecarClient) -> str:
    applied = unwrap(client.request(
        "mcp/call",
        {"tool": "kafka_ui_select", "arguments": {"partition": 0, "offset": 42}},
        on_event=report_intent_applied,
    ))
    assert applied["state"] == "applied", applied
    state = call_tool(client, "kafka_ui_state", intentId=applied["intentId"])
    assert state["state"] == "applied" and state["summary"]["count"] == 2, state
    # 快照型 report 后，无 intentId 的 kafka_ui_state 返回最新快照。
    client.request("kafka/ui/state/report", {"status": "snapshot", "summary": {"panel": "messages", "topic": "order-events", "count": 42}})
    snapshot = call_tool(client, "kafka_ui_state")
    assert snapshot["snapshot"]["panel"] == "messages" and snapshot["snapshot"]["count"] == 42, snapshot
    # connectionId 给出时附带 stream 段（无会话 = 空数组）。
    with_streams = call_tool(client, "kafka_ui_state", connectionId="smoke-mcp-ro")
    assert "streams" in with_streams and isinstance(with_streams["streams"], list), with_streams
    expect_error(
        lambda: call_tool(client, "kafka_ui_state", intentId="i-nonexistent"),
        ("unknown intentid",),
    )
    return "by intentId + latest snapshot + streams segment all work"


@scenario("K8", "digest tools refuse unknown connections/cursors/topics")
def k8_digest_gates(client: SidecarClient) -> str:
    expect_error(
        lambda: call_tool(client, "kafka_messages_digest", connectionId="smoke-mcp-nope", topic="t"),
        ("not connected", "connection"),
    )
    expect_error(
        lambda: call_tool(client, "kafka_messages_digest", connectionId="smoke-mcp-ro"),
        ("topic is required",),
    )
    expect_error(
        lambda: call_tool(client, "kafka_messages_digest", connectionId="smoke-mcp-ro", topic="t", format="bogus"),
        ("format must be",),
    )
    expect_error(
        lambda: call_tool(client, "kafka_cursor_next", cursorId="cur-nope"),
        ("unknown cursorid",),
    )
    expect_error(
        lambda: call_tool(client, "kafka_ui_topics"),
        ("connectionid is required",),
    )
    return "unknown connection / cursor / missing topic / bad format all refused"


@scenario("K9", "unknown tool and unknown method")
def k9_unknown(client: SidecarClient) -> str:
    expect_error(
        lambda: client.request("mcp/call", {"tool": "kafka_nonexistent", "arguments": {}}),
        ("unknown tool",),
    )
    try:
        client.request("mcp/nonexistent", {})
        raise AssertionError("expected -32601 for an unregistered method")
    except SidecarError as cause:
        if not is_method_not_registered(cause):
            raise AssertionError(f"expected method-not-registered, got: {cause}") from cause
    return "unknown tool -32000; unknown method -32601"


@scenario("K10", "allow_delete=false omits only the delete-class tools")
def k10_allow_delete(client: SidecarClient) -> str:
    nd_id = "smoke-mcp-nodelete"
    client.request("connection/connect", lifecycle_params(make_connection(nd_id, allow_delete=False)))
    result = client.request("mcp/tools", {"connectionId": nd_id})
    names = [tool["name"] for tool in result["tools"]]
    assert "kafka_messages_produce" in names and "kafka_groups_offsets_reset" in names, names
    for write_tool in DELETE_CLASS_TOOLS:
        assert write_tool not in names, f"{write_tool} must be omitted"
    omitted = {row["name"] for row in result.get("omittedWriteTools") or []}
    assert omitted == set(DELETE_CLASS_TOOLS), omitted
    return "produce/offsets_reset stay listed; delete-class tools omitted"


# -- container scenario (K11) ----------------------------------------------------


@scenario("K11", "digest + cursor + produce + two-phase writes + audit (container)")
def k11_container(client: SidecarClient, data_dir: str) -> str:
    ok, reason = kafka_reachable()
    if not ok:
        raise SkipScenario(reason)
    run = uuid.uuid4().hex[:6]
    topic = f"dbx-smoke-mcp-{run}"
    conn_id = f"smoke-mcp-main-{run}"
    client.request("connection/connect", lifecycle_params(make_connection(conn_id)))
    try:
        client.request("kafka/topics/create", {"connectionId": conn_id, "topics": [topic], "partitions": 2, "replicationFactor": 1})

        # 元发现：topic 名清单（≤50 截断语义）。
        topics = call_tool(client, "kafka_ui_topics", connectionId=conn_id)
        assert topic in topics["topics"], topics

        # 单阶段写：produce（source=mcp 审计）。
        for index in range(3):
            produced = call_tool(client, "kafka_messages_produce", connectionId=conn_id, topic=topic,
                                 key=f"k-{index}", value=json.dumps({"run": run, "v": index}))
            assert produced["success"] is True and produced["partition"] in (0, 1), produced

        # digest：本地聚合 + cursor 物化。
        digest = call_tool(client, "kafka_messages_digest", connectionId=conn_id, topic=topic,
                           fields=["$.v"], maxScanRecords=2000)
        assert digest["matched"] == 3 and digest["scanned"] >= 3, digest
        stats = digest["stats"]
        assert sum(stats["perPartition"].values()) == 3, stats
        assert stats["keys"].get("k-0") == 1, stats
        assert stats["timeHistogram"], stats
        assert stats["fields"][0]["field"] == "$.v" and stats["fields"][0]["valueCount"] == 3, stats
        assert len(digest["sample"]) <= 5 and "cursorId" in digest, digest
        cursor_id = digest["cursorId"]

        # cursor 翻页：定位字段行（partition/offset），不重发条件。
        page = call_tool(client, "kafka_cursor_next", cursorId=cursor_id, n=2)
        assert len(page["rows"]) == 2 and page["rows"][0]["topic"] == topic, page
        assert "partition" in page["rows"][0] and "offset" in page["rows"][0], page

        # 两阶段 delete：preview + confirmToken → 参数改动作废 → 重开预览 → 执行。
        scratch = f"dbx-smoke-mcp-del-{run}"
        client.request("kafka/topics/create", {"connectionId": conn_id, "topics": [scratch], "partitions": 1, "replicationFactor": 1})
        preview = call_tool(client, "kafka_topics_delete", connectionId=conn_id, topics=[scratch])
        assert preview["preview"]["topics"] == [scratch] and preview.get("confirmToken"), preview
        expect_error(
            lambda: call_tool(client, "kafka_topics_delete", connectionId=conn_id, topics=[topic], confirmToken=preview["confirmToken"]),
            ("arguments changed",),
        )
        preview2 = call_tool(client, "kafka_topics_delete", connectionId=conn_id, topics=[scratch])
        confirm = call_tool(client, "kafka_topics_delete", connectionId=conn_id, topics=[scratch], confirmToken=preview2["confirmToken"])
        assert confirm["success"] is True, confirm
        # 一次性：同 token 复用 → unknown。
        expect_error(
            lambda: call_tool(client, "kafka_topics_delete", connectionId=conn_id, topics=[scratch], confirmToken=preview2["confirmToken"]),
            ("unknown or already used",),
        )
        deleted_names = [row["topic"] for row in confirm["results"]]
        assert scratch in deleted_names, confirm

        # 两阶段 offsets reset：preview → confirm。
        group = f"smoke-mcp-g-{run}"
        reset_preview = call_tool(client, "kafka_groups_offsets_reset", connectionId=conn_id, group=group,
                                  topics=[topic], resetTo="latest")
        assert reset_preview["preview"]["resetTo"] == "latest", reset_preview
        reset_confirm = call_tool(client, "kafka_groups_offsets_reset", connectionId=conn_id, group=group,
                                  topics=[topic], resetTo="latest", confirmToken=reset_preview["confirmToken"])
        assert reset_confirm["success"] is True and reset_confirm["rows"], reset_confirm

        # 两阶段 records clear：preview → confirm → 清空后 digest matched=0。
        clear_preview = call_tool(client, "kafka_topics_records_clear", connectionId=conn_id, topic=topic)
        assert clear_preview["preview"]["topic"] == topic, clear_preview
        clear_confirm = call_tool(client, "kafka_topics_records_clear", connectionId=conn_id, topic=topic,
                                  confirmToken=clear_preview["confirmToken"])
        assert clear_confirm["success"] is True, clear_confirm
        after = call_tool(client, "kafka_messages_digest", connectionId=conn_id, topic=topic, maxScanRecords=2000)
        assert after["matched"] == 0, after

        # 审计：MCP 写路径 source=mcp（audit.jsonl 与 kafka/audit 事件同条）。
        audit_path = os.path.join(data_dir, "audit.jsonl")
        sources = {}
        with open(audit_path, encoding="utf-8") as handle:
            for line in handle:
                record = json.loads(line)
                if record.get("connectionId") == conn_id:
                    sources.setdefault(record["action"], set()).add(record.get("source"))
        # audit.jsonl 的 action 带 kafka/ 前缀（main 层 auditAction）。
        assert "mcp" in sources.get("kafka/produce", set()), sources
        assert "mcp" in sources.get("kafka/topics-delete", set()), sources
        assert "mcp" in sources.get("kafka/group-offsets-reset", set()), sources
        assert "mcp" in sources.get("kafka/topics.records.clear", set()), sources
        # 工作台路径（topics/create 走领域方法）不带 source 字段。
        assert None in sources.get("kafka/topics-create", set()), sources
        return f"digest matched=3 -> cleared; two-phase delete/reset/clear executed; audit source=mcp"
    finally:
        try:
            client.request("connection/disconnect", {"connection": {"id": conn_id}})
        except Exception:
            pass


# -- runner ----------------------------------------------------------------------


def report() -> None:
    widths = (4, 52, 6)
    print(f"{'No.':<{widths[0]}} {'Scenario':<{widths[1]}} {'Status':<{widths[2]}} Detail")
    for result in RESULTS:
        print(f"{result.no:<{widths[0]}} {result.name:<{widths[1]}} {result.status:<{widths[2]}} {result.detail}")
    counts = {status: sum(1 for r in RESULTS if r.status == status) for status in ("PASS", "FAIL", "SKIP")}
    print(f"\ntotal={len(RESULTS)} PASS={counts['PASS']} FAIL={counts['FAIL']} SKIP={counts['SKIP']}")


def _all_scenarios():
    return [k1_settings, k2_tools, k3_readonly_tools, k4_readonly_write, k5_intent_pending,
            k6_intent_applied, k7_ui_state, k8_digest_gates, k9_unknown, k10_allow_delete]


def main() -> int:
    binary = default_binary()
    if not os.path.exists(binary):
        message = f"sidecar binary not found: {binary} (set DBX_PLUGIN_SIDECAR)"
        for fn in _all_scenarios():
            RESULTS.append(ScenarioResult(fn.no, fn.name, "FAIL" if REQUIRE else "SKIP", message))
        RESULTS.append(ScenarioResult(k11_container.no, k11_container.name, "FAIL" if REQUIRE else "SKIP", message))
        report()
        return 0

    # 隔离数据目录：mcp-settings.json 与 audit.jsonl 不污染真实插件数据。
    data_dir = tempfile.mkdtemp(prefix="dbx-kafka-mcp-smoke-")
    client = SidecarClient.start(data_dir=data_dir)
    try:
        client.initialize()
        for fn in _all_scenarios():
            fn(client)
        k11_container(client, data_dir)
    finally:
        for conn_id in ("smoke-mcp-ro", "smoke-mcp-nodelete"):
            try:
                client.request("connection/disconnect", {"connection": {"id": conn_id}})
            except Exception:
                pass
        try:
            client.close()
        except Exception:
            pass

    report()
    return 1 if any(result.status == "FAIL" for result in RESULTS) else 0


if __name__ == "__main__":
    raise SystemExit(main())
