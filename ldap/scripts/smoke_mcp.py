#!/usr/bin/env python3
"""MCP smoke test for the dbx-ldap-plugin sidecar (M1, scenarios M1-M17).

Covers the shared/IMPL_PLAN_PLUGIN_MCP.zh-CN.md (v2) §1/§2/§4 tool face over
the stdio-jsonl protocol (mcp/tools + mcp/call + mcp/settings, the ssh
mcp.rs skeleton ported to Go), plus the standalone `--mcp` stdio mode
(design §0.2/§5 stdio row, scenarios M11/M12/M14):

    M1  mcp/settings/get + set           -> defaults, partial update, invalid refused
    M2  mcp/tools lists the 8 ldap tools -> names + JSON Schema required fields
    M3  read-only connection tools list  -> ldap_entry_write omitted with reason
    M4  write tool on read-only conn     -> refused (-32000)
    M5  UI intent without a frontend     -> state=pending + digest fallback hint
    M6  UI intent with a report          -> state=applied + summary roundtrip
    M7  ldap_ui_state                    -> by intentId + latest snapshot
    M8  digest/cursor gates              -> unknown connection/cursor refused
    M9  unknown tool / method            -> clear error (-32601 SKIP semantics)
    M10 digest + cursor + two-phase write (needs the OpenLDAP test container)
    M11 stdio --mcp: initialize/tools-list/ui UNAVAILABLE/gates (offline)
    M12 stdio --mcp inline credentials: digest/cursor/two-phase (container)
    M13 LLM input variants: tolerant parsing or clear refusal (offline)
    M14 stdio bridge fallback: fail-closed w/o the DBX app + mock-bridge
        forward contract (design §5 stdio row, ssh smoke scenario 8 analogue)
    M15 digest aggregation variants on real data: groupBy/distinct/topN/
        subtree counts/clamps + zero-hit and empty-key edges (container)
    M16 ldap_ui_schema behavior: cold/warm cache, truncation at 300 names,
        disconnected-connection error (container)
    M17 cursor session edges: deep paging, explicit-offset reread, offset
        clamp beyond the end, settings-driven TTL expiry (container)
    M18 stdio robustness: malformed JSON/UTF-8, notification silence, request
        shape tiering, 8MiB line, pipelining, CRLF — process stays alive
        (offline, 可靠性纵深轮)
    M19 digest repeat idempotency + cursor reread stability + per-preview
        tokens (container, 可靠性纵深轮)
    M20 enum invalid values list valid options (scope/action) + case
        normalization online (container, 第七轮口径拉齐)

SKIP semantics (M0 §5.2, same as smoke_test.py):
  * method/tool not registered  -> SKIP (never FAIL);
  * no sidecar binary           -> whole suite SKIP (FAIL with LDAP_TEST_REQUIRE=1);
  * M10/M12/M15/M16/M17/M19/M20 additionally SKIP without a reachable OpenLDAP
    test container.

Usage:
    DBX_PLUGIN_SIDECAR=/path/to/dbx-plugin-ldap python3 scripts/smoke_mcp.py
"""

from __future__ import annotations

import http.server
import json
import os
import select
import socket
import subprocess
import sys
import tempfile
import threading
import time
import uuid
from datetime import datetime, timezone

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

URL = f"ldap://{HOST}:{PORT}"

EXPECTED_TOOLS = [
    "ldap_ui_focus",
    "ldap_ui_search",
    "ldap_ui_select",
    "ldap_ui_state",
    "ldap_ui_schema",
    "ldap_search_digest",
    "ldap_cursor_next",
    "ldap_entry_write",
]


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
        "auth_type": "simple",
        "bind_dn": BIND_DN,
        "base_dn": ROOT,
        "timeout_secs": 10,
    }
    external.update(extra_config)
    return {
        "id": connection_id,
        "name": f"smoke-mcp-{connection_id}",
        "host": URL,
        "port": PORT,
        "external_config": external,
        "connection_secrets": {"bind_password": BIND_PW},
    }


def report_intent_applied(message: dict) -> dict | None:
    """on_event hook: answer an ldap/ui/intent notification with a report."""
    if message.get("method") == "ldap/ui/intent":
        intent_id = (message.get("params") or {}).get("intentId", "")
        return {
            "method": "ldap/ui/state/report",
            "params": {
                "intentId": intent_id,
                "status": "applied",
                "summary": {
                    "count": 1,
                    "anchor": f"uid=user0000,ou=people,{ROOT}",
                    "rows": [{"dn": f"uid=user0000,ou=people,{ROOT}"}],
                },
            },
        }
    return None


def ldap_reachable() -> tuple[bool, str]:
    try:
        with socket.create_connection((HOST, PORT), timeout=2):
            return True, ""
    except OSError as cause:
        return False, f"no LDAP test container at {HOST}:{PORT}: {cause}"


# -- scenarios (offline: no LDAP server needed) --------------------------------


@scenario("M1", "mcp/settings get/set defaults + validation")
def m1_settings(client: SidecarClient) -> str:
    settings = client.request("mcp/settings/get")["settings"]
    assert settings["reportWaitMs"] == 5000, settings
    assert settings["responseLimitBytes"] == 16 * 1024, settings
    assert settings["digestGroupLimit"] == 20 and settings["digestTopN"] == 10, settings
    updated = client.request("mcp/settings/set", {"reportWaitMs": 300, "cellWidth": 80})["settings"]
    assert updated["reportWaitMs"] == 300 and updated["cellWidth"] == 80, updated
    expect_error(lambda: client.request("mcp/settings/set", {"reportWaitMs": 0}), ("between 1 and",))
    expect_error(lambda: client.request("mcp/settings/set", {"reportWaitMs": "fast"}), ("positive integer",))
    # 白名单外字段容忍（部分更新语义），当前值不被污染。
    tolerated = client.request("mcp/settings/set", {"unknownField": 1})["settings"]
    assert tolerated["reportWaitMs"] == 300, tolerated
    return "defaults 5s/16KiB; partial update; invalid refused"


@scenario("M2", "mcp/tools lists the 8 ldap tools with schemas")
def m2_tools(client: SidecarClient) -> str:
    tools = client.request("mcp/tools")["tools"]
    names = [tool["name"] for tool in tools]
    for expected in EXPECTED_TOOLS:
        assert expected in names, f"{expected} missing from {names}"
    digest = next(tool for tool in tools if tool["name"] == "ldap_search_digest")
    schema = digest["inputSchema"]
    assert schema["type"] == "object" and "connectionId" in schema["properties"], schema
    assert set(schema["required"]) >= {"connectionId", "filter"}, schema
    write = next(tool for tool in tools if tool["name"] == "ldap_entry_write")
    assert "confirmToken" in write["inputSchema"]["properties"], write
    return f"{len(tools)} tools with JSON Schema"


@scenario("M3", "read-only connection omits the write tool")
def m3_readonly_tools(client: SidecarClient) -> str:
    ro_id = "smoke-mcp-ro"
    client.request("connection/connect", lifecycle_params(make_connection(ro_id, read_only=True)))
    result = client.request("mcp/tools", {"connectionId": ro_id})
    names = [tool["name"] for tool in result["tools"]]
    assert "ldap_entry_write" not in names, names
    omitted = result.get("omittedWriteTools") or []
    assert omitted and omitted[0]["name"] == "ldap_entry_write", omitted
    assert "read-only" in omitted[0]["reason"], omitted
    # 全量清单（不带 connectionId）仍包含写工具。
    all_names = [tool["name"] for tool in client.request("mcp/tools")["tools"]]
    assert "ldap_entry_write" in all_names
    return "write tool omitted with a reason for read-only"


@scenario("M4", "write tool refused on a read-only connection")
def m4_readonly_write(client: SidecarClient) -> str:
    ro_id = "smoke-mcp-ro"
    error = expect_error(
        lambda: client.request("mcp/call", {
            "tool": "ldap_entry_write",
            "arguments": {"connectionId": ro_id, "action": "delete", "dn": f"ou=x,{ROOT}"},
        }),
        ("read-only",),
    )
    assert error.code == -32000, error.code
    return "mcp/call refused before any write"


@scenario("M5", "UI intent pends without a frontend")
def m5_intent_pending(client: SidecarClient) -> str:
    # M1 把 reportWaitMs 调到 300ms：无前端时快速收敛为 pending。
    result = call_tool(client, "ldap_ui_focus", panel="schema")
    assert result["state"] == "pending", result
    assert result["intentId"], result
    assert "ldap_search_digest" in result.get("hint", ""), result
    return "pending + digest fallback hint"


@scenario("M6", "UI intent applied via the report callback")
def m6_intent_applied(client: SidecarClient) -> str:
    result = unwrap(client.request(
        "mcp/call",
        {"tool": "ldap_ui_search", "arguments": {"filter": "(uid=user0000)", "scope": "sub"}},
        on_event=report_intent_applied,
    ))
    assert result["state"] == "applied", result
    summary = result.get("summary") or {}
    assert summary.get("count") == 1 and summary.get("anchor"), result
    return "applied + summary roundtrip over ldap/ui/state/report"


@scenario("M7", "ldap_ui_state reads intent result and snapshot")
def m7_ui_state(client: SidecarClient) -> str:
    applied = unwrap(client.request(
        "mcp/call",
        {"tool": "ldap_ui_select", "arguments": {"dn": f"uid=user0000,ou=people,{ROOT}"}},
        on_event=report_intent_applied,
    ))
    assert applied["state"] == "applied", applied
    state = call_tool(client, "ldap_ui_state", intentId=applied["intentId"])
    assert state["state"] == "applied" and state["summary"]["count"] == 1, state
    # 快照型 report 后，无 intentId 的 ldap_ui_state 返回最新快照。
    client.request("ldap/ui/state/report", {"status": "snapshot", "summary": {"panel": "search", "count": 42}})
    snapshot = call_tool(client, "ldap_ui_state")
    assert snapshot["snapshot"]["panel"] == "search" and snapshot["snapshot"]["count"] == 42, snapshot
    expect_error(
        lambda: call_tool(client, "ldap_ui_state", intentId="i-nonexistent"),
        ("unknown intentid",),
    )
    return "by intentId + latest snapshot both work"


@scenario("M8", "digest tools refuse unknown connections")
def m8_digest_gates(client: SidecarClient) -> str:
    expect_error(
        lambda: call_tool(client, "ldap_search_digest", connectionId="smoke-mcp-nope", filter="(objectClass=*)"),
        ("not connected", "connection"),
    )
    expect_error(
        lambda: call_tool(client, "ldap_cursor_next", cursorId="cur-nope"),
        ("unknown cursorid",),
    )
    expect_error(
        lambda: call_tool(client, "ldap_ui_schema"),
        ("missing required parameters: connectionid",),
    )
    # 缺参枚举（ssh 同款 §3.9）：entry_write 三缺一次全点名；单缺只点名
    # 其一；显式 null 视同缺失。
    error = expect_error(
        lambda: call_tool(client, "ldap_entry_write"),
        ("missing required parameters: connectionid, action, dn",),
    )
    assert "connectionid, action, dn" in str(error).lower(), error
    expect_error(
        lambda: call_tool(client, "ldap_entry_write", action="delete", dn=f"uid=x,{ROOT}"),
        ("missing required parameters: connectionid",),
    )
    expect_error(
        lambda: call_tool(client, "ldap_entry_write", connectionId="smoke-mcp-nope", action=None, dn=f"uid=x,{ROOT}"),
        ("missing required parameters: action",),
    )
    return "unknown connection / cursor refused; missing params enumerated in schema order"


@scenario("M9", "unknown tool and unknown method")
def m9_unknown(client: SidecarClient) -> str:
    error = expect_error(
        lambda: client.request("mcp/call", {"tool": "ldap_serach_digest", "arguments": {}}),
        ("unknown tool",),
    )
    # 易用性：错误列出可用工具名，让 LLM 一轮自纠拼写错误。
    assert "ldap_search_digest" in str(error), error
    try:
        client.request("mcp/nonexistent", {})
        raise AssertionError("expected -32601 for an unregistered method")
    except SidecarError as cause:
        if not is_method_not_registered(cause):
            raise AssertionError(f"expected method-not-registered, got: {cause}") from cause
    return "unknown tool -32000 (with tool list); unknown method -32601"


@scenario("M13", "LLM input variants: tolerant parsing or clear refusal (offline)")
def m13_input_variants(client: SidecarClient) -> str:
    # panel 枚举：非法值本地拒绝并列出合法值（不发注定 rejected 的 intent）。
    expect_error(
        lambda: call_tool(client, "ldap_ui_focus", panel="main"),
        ("panel must be search, tree, or schema",),
    )
    # scope：非法值拒绝（并列出 schema enum 值与 RFC 别名）；大写合法值
    # 归一化进 intent params。
    scope_error = expect_error(
        lambda: call_tool(client, "ldap_ui_search", filter="(uid=x)", scope="children"),
        ("scope must be base, one, or sub",),
    )
    assert "baseobject" in str(scope_error).lower() and "singlelevel" in str(scope_error).lower(), scope_error
    captured: dict = {}

    def capture_params(message: dict) -> dict | None:
        if message.get("method") == "ldap/ui/intent":
            captured.update((message.get("params") or {}).get("params") or {})
            captured["_intentId"] = (message.get("params") or {}).get("intentId", "")
            return {
                "method": "ldap/ui/state/report",
                "params": {"intentId": captured["_intentId"], "status": "applied",
                           "summary": {"count": 0}},
            }
        return None

    unwrap(client.request(
        "mcp/call",
        {"tool": "ldap_ui_search", "arguments": {
            "filter": "(uid=x)", "scope": "SUB", "attributes": "cn, mail", "sizeLimit": "50"}},
        on_event=capture_params,
    ))
    assert captured.get("scope") == "sub", captured
    assert captured.get("attributes") == ["cn", "mail"], captured
    assert captured.get("sizeLimit") == 50, captured
    # settings：字符串数字接受，非数字仍拒绝。
    updated = client.request("mcp/settings/set", {"cellWidth": "80"})["settings"]
    assert updated["cellWidth"] == 80, updated
    client.request("mcp/settings/set", {"cellWidth": 120})
    expect_error(lambda: client.request("mcp/settings/set", {"cellWidth": "wide"}),
                 ("positive integer",))
    # cursor 未知 id：错误带重发 digest 的可行动指引。
    expect_error(
        lambda: call_tool(client, "ldap_cursor_next", cursorId="cur-nope"),
        ("unknown cursorid", "ldap_search_digest"),
    )
    return "panel/scope refused clearly; SUB/cn,mail/\"50\" tolerated; settings string numbers ok"


# -- container scenario (M10) ---------------------------------------------------


@scenario("M10", "digest + cursor + two-phase write (container)")
def m10_container(client: SidecarClient) -> str:
    ok, reason = ldap_reachable()
    if not ok:
        raise SkipScenario(reason)
    conn_id = f"smoke-mcp-main-{uuid.uuid4().hex[:6]}"
    client.request("connection/connect", lifecycle_params(make_connection(conn_id)))
    try:
        # digest：filter 服务端执行 + 本地聚合 + cursor 物化。
        digest = call_tool(client, "ldap_search_digest",
                           connectionId=conn_id, filter="(objectClass=*)", distinctAttr="objectClass")
        assert digest["matched"] > 0, digest
        assert digest["stats"]["objectClass"], digest
        assert digest["stats"].get("distinct", {}).get("attribute") == "objectClass", digest
        assert len(digest["sample"]) <= 5, digest
        cursor_id = digest["cursorId"]

        batch = call_tool(client, "ldap_cursor_next", cursorId=cursor_id, n=2)
        assert batch["rows"] and len(batch["rows"]) <= 2, batch

        # 两阶段 delete：preview + confirmToken（不执行）→ 带 token 执行。
        dn = f"ou=mcp-smoke-{uuid.uuid4().hex[:6]},{ROOT}"
        call_tool(client, "ldap_entry_write", connectionId=conn_id, action="add", dn=dn,
                  attributes={"objectClass": ["organizationalUnit"], "ou": [dn.split(",")[0][3:]]})
        preview = call_tool(client, "ldap_entry_write", connectionId=conn_id, action="delete", dn=dn)
        assert preview["preview"]["dn"] == dn and preview.get("confirmToken"), preview

        # 参数 hash 绑定：token 有效但参数被改 → 作废（须重开预览）。
        expect_error(lambda: call_tool(client, "ldap_entry_write", connectionId=conn_id, action="delete",
                                       dn=dn, recursive=True, confirmToken=preview["confirmToken"]),
                     ("arguments changed",))

        # hash 绑定 connectionId：新开预览的 token 换另一个连接复用 → 作废
        #（防跨连接误删；Consume 一次性，之前作废的 token 不能拿来复测）。
        alt_id = f"smoke-mcp-alt-{uuid.uuid4().hex[:6]}"
        client.request("connection/connect", lifecycle_params(make_connection(alt_id)))
        try:
            preview_alt = call_tool(client, "ldap_entry_write", connectionId=conn_id,
                                    action="delete", dn=dn)
            expect_error(lambda: call_tool(client, "ldap_entry_write", connectionId=alt_id,
                                           action="delete", dn=dn,
                                           confirmToken=preview_alt["confirmToken"]),
                         ("arguments changed",))
        finally:
            try:
                client.request("connection/disconnect", {"connection": {"id": alt_id}})
            except Exception:
                pass

        preview2 = call_tool(client, "ldap_entry_write", connectionId=conn_id, action="delete", dn=dn)
        confirm = call_tool(client, "ldap_entry_write", connectionId=conn_id, action="delete",
                            dn=dn, confirmToken=preview2["confirmToken"])
        assert confirm["success"] is True, confirm

        # 一次性：同 token 复用 → unknown。
        expect_error(lambda: call_tool(client, "ldap_entry_write", connectionId=conn_id, action="delete",
                                       dn=dn, confirmToken=preview2["confirmToken"]),
                     ("unknown or already used",))

        # schema 元发现。
        schema = call_tool(client, "ldap_ui_schema", connectionId=conn_id)
        assert schema["attributeNames"], schema
        return f"digest matched={digest['matched']}; two-phase delete executed; token single-use"
    finally:
        try:
            client.request("connection/disconnect", {"connection": {"id": conn_id}})
        except Exception:
            pass


# -- container scenarios M15/M16/M17 (round 2: aggregation/schema/cursor) -----


def add_entry(client: SidecarClient, conn_id: str, dn: str, attributes: dict) -> dict:
    return call_tool(client, "ldap_entry_write", connectionId=conn_id, action="add",
                     dn=dn, attributes=attributes)


def recursive_delete(client: SidecarClient, conn_id: str, dn: str) -> None:
    preview = call_tool(client, "ldap_entry_write", connectionId=conn_id,
                        action="delete", dn=dn, recursive=True)
    confirm = call_tool(client, "ldap_entry_write", connectionId=conn_id,
                        action="delete", dn=dn, recursive=True,
                        confirmToken=preview["confirmToken"])
    assert confirm["success"] is True, confirm


@scenario("M15", "digest aggregation variants on real data (container)")
def m15_digest_variants(client: SidecarClient) -> str:
    ok, reason = ldap_reachable()
    if not ok:
        raise SkipScenario(reason)
    conn_id = f"smoke-mcp-digest-{uuid.uuid4().hex[:6]}"
    client.request("connection/connect", lifecycle_params(make_connection(conn_id)))
    ou = f"ou=digest-{uuid.uuid4().hex[:6]},{ROOT}"
    run = uuid.uuid4().hex[:6]
    try:
        # 造数据：1 OU + 12 个 inetOrgPerson（mail 唯一、description 200 字符）。
        add_entry(client, conn_id, ou, {"objectClass": ["organizationalUnit"],
                                        "ou": [ou.split(",")[0][3:]]})
        # uid 带 run 唯一前缀：容器里可能共存其他用例的 uid=agent* 数据
        #（shared 容器，撞车会把 matched 顶爆），filter 随前缀保证只命中本
        # 轮造的 12 条。
        prefix = f"dg{run}"
        users_dn = [f"uid={prefix}agent{i},{ou}" for i in range(12)]
        for index, dn in enumerate(users_dn):
            add_entry(client, conn_id, dn, {
                "objectClass": ["inetOrgPerson"], "uid": [f"{prefix}agent{index}"],
                "sn": [f"Agent{index}"], "cn": [f"{prefix}agent{index}"],
                "mail": [f"{prefix}agent{index}@example.org"],
                "description": ["d" * 200],
            })

        # 变体 1：groupBy(objectClass) + distinct(uid)（attributes 只带
        # description——distinctAttr 不在投影中也必须自动进远端投影，否则
        # distinct 恒空）。base 用连接缺省根：12 个 uid 收拢进 OU 直接子键
        #（subtrees 子树计数语义）。
        digest = call_tool(client, "ldap_search_digest", connectionId=conn_id,
                           filter=f"(uid={prefix}agent*)", distinctAttr="uid",
                           attributes=["description"])
        assert digest["matched"] == 12, digest
        assert digest["baseDn"] == ROOT, digest["baseDn"]
        assert digest["stats"]["objectClass"].get("inetOrgPerson") == 12, digest
        assert len(digest["stats"]["objectClass"]) <= 20, digest
        assert digest["stats"]["subtrees"].get(ou) == 12, digest["stats"]["subtrees"]
        # filter 不命中 base 自身 → subtrees 只有 OU 一个键。
        assert len(digest["stats"]["subtrees"]) == 1, digest["stats"]["subtrees"]
        distinct = digest["stats"]["distinct"]
        assert distinct["attribute"] == "uid" and distinct["valueCount"] == 12, distinct
        assert len(distinct["values"]) <= 10 and distinct.get("truncated") is True, distinct
        # clamp：样本 ≤5、单元格 120 字符（DN 定位字段不截断）。
        assert len(digest["sample"]) <= 5, digest["sample"]
        first = digest["sample"][0]
        assert first["dn"] == users_dn[0], first
        assert len(first["attributes"]["description"][0]) == 121, first["attributes"]["description"]
        assert first["attributes"]["description"][0].endswith("…"), first["attributes"]["description"]

        # 变体 1b：base=OU 层——直接子条目各自成键（subtreeKey 语义：base 的
        # 直接子返回 DN 本身），固化为形状断言而非假设收拢。
        narrowed = call_tool(client, "ldap_search_digest", connectionId=conn_id,
                             baseDn=ou, filter="(objectClass=inetOrgPerson)")
        assert narrowed["matched"] == 12, narrowed
        assert len(narrowed["stats"]["subtrees"]) == 12, narrowed["stats"]["subtrees"]
        assert all(count == 1 for count in narrowed["stats"]["subtrees"].values()), narrowed["stats"]

        # 变体 2：filter 命中 0 → 聚合键为空的边界 + 空会话 cursor 可翻。
        empty = call_tool(client, "ldap_search_digest", connectionId=conn_id,
                          baseDn=ou, filter="(uid=nope)", distinctAttr="uid")
        assert empty["matched"] == 0 and empty["scanned"] == 0, empty
        assert empty["stats"]["objectClass"] == {} and empty["stats"]["subtrees"] == {}, empty
        assert empty["sample"] == [] and empty["cursorId"], empty
        empty_page = call_tool(client, "ldap_cursor_next", cursorId=empty["cursorId"])
        assert empty_page["rows"] == [] and empty_page["done"] is True, empty_page

        # 变体 3：聚合键为空——distinct 指向不存在的属性（valueCount=0 不报错）。
        nokey = call_tool(client, "ldap_search_digest", connectionId=conn_id,
                          baseDn=ou, filter="(objectClass=inetOrgPerson)",
                          distinctAttr="employeeNumber")
        assert nokey["stats"].get("distinct", {}).get("valueCount") == 0, nokey["stats"]

        # 变体 4：物化 cursor 全页（n 用字符串数字变体，深度翻页到 done）。
        full = call_tool(client, "ldap_cursor_next", cursorId=digest["cursorId"], n="20")
        assert len(full["rows"]) == 12 and full["done"] is True and full["offset"] == 0, full
        assert full["nextOffset"] == 12, full

        return (f"digest matched=12; distinct uid valueCount=12 topN-clamped; "
                f"zero-hit + empty-key edges ok; cell clamp 121 runes")
    finally:
        try:
            recursive_delete(client, conn_id, ou)
        except Exception:
            pass
        try:
            client.request("connection/disconnect", {"connection": {"id": conn_id}})
        except Exception:
            pass


@scenario("M16", "ldap_ui_schema cache + truncation behavior (container)")
def m16_schema_behavior(client: SidecarClient) -> str:
    ok, reason = ldap_reachable()
    if not ok:
        raise SkipScenario(reason)
    conn_id = f"smoke-mcp-schema-{uuid.uuid4().hex[:6]}"
    client.request("connection/connect", lifecycle_params(make_connection(conn_id)))
    try:
        # 冷：真实 subschema 拉取。OpenLDAP 的 attributeTypes 远超 300 → 截断
        # 恰好 300 + truncated 标志；objectClass 数远小于 300 → 不截断。
        cold = call_tool(client, "ldap_ui_schema", connectionId=conn_id)
        assert cold["attributeNames"], cold
        assert len(cold["attributeNames"]) == 300 and cold["attributeNamesTruncated"] is True, \
            f"expected the 300-name clamp: {len(cold['attributeNames'])} {cold['attributeNamesTruncated']}"
        assert len(cold["objectClassNames"]) < 300 and cold["objectClassTruncated"] is False, cold

        # 热：第二次调用走 schema 缓存，形状与冷调用完全一致。
        warm = call_tool(client, "ldap_ui_schema", connectionId=conn_id)
        assert warm == cold, "warm cache must return the identical shape"
    finally:
        try:
            client.request("connection/disconnect", {"connection": {"id": conn_id}})
        except Exception:
            pass
    # 缓存失效（断连）后：可行动错误而非假死（引导 connection/connect）。
    expect_error(
        lambda: call_tool(client, "ldap_ui_schema", connectionId=conn_id),
        ("not connected",),
    )
    return "cold fetch clamps at 300 + truncated flag; warm cache identical; disconnected refused"


@scenario("M17", "cursor session edges: paging depth/clamp/settings TTL (container)")
def m17_cursor_edges(client: SidecarClient) -> str:
    ok, reason = ldap_reachable()
    if not ok:
        raise SkipScenario(reason)
    conn_id = f"smoke-mcp-cursor-{uuid.uuid4().hex[:6]}"
    client.request("connection/connect", lifecycle_params(make_connection(conn_id)))
    try:
        digest = call_tool(client, "ldap_search_digest", connectionId=conn_id,
                           filter="(objectClass=*)")
        matched = digest["matched"]
        assert matched >= 6, digest  # seed 6 条起
        cursor_id = digest["cursorId"]

        # 深度翻页：显式 offset 逐窗推进直至 done。
        window1 = call_tool(client, "ldap_cursor_next", cursorId=cursor_id, offset=0, n=3)
        assert len(window1["rows"]) == 3 and window1["offset"] == 0 and not window1["done"], window1
        window2 = call_tool(client, "ldap_cursor_next", cursorId=cursor_id, offset=3, n=3)
        assert window2["offset"] == 3 and len(window2["rows"]) == 3, window2
        # 显式 offset=0 重读同窗：内容幂等（定位字段不漂移）。
        reread = call_tool(client, "ldap_cursor_next", cursorId=cursor_id, offset=0, n=3)
        assert [row["dn"] for row in reread["rows"]] == [row["dn"] for row in window1["rows"]], reread
        # offset 越界：clamp 到行数末尾（空批 + done + clamp 后 offset），不报错。
        beyond = call_tool(client, "ldap_cursor_next", cursorId=cursor_id, offset=matched + 50)
        assert beyond["rows"] == [] and beyond["done"] is True, beyond
        assert beyond["offset"] == matched and beyond["nextOffset"] == matched, beyond

        # settings 接线：cursorTtlSecs=1 → 新 digest 的 cursor 在 TTL 后过期，
        # 且错误携带实际生效 TTL（files 同款语义）。
        client.request("mcp/settings/set", {"cursorTtlSecs": 1})
        try:
            short = call_tool(client, "ldap_search_digest", connectionId=conn_id,
                              filter="(objectClass=*)")
            time.sleep(1.3)
            expect_error(
                lambda: call_tool(client, "ldap_cursor_next", cursorId=short["cursorId"]),
                ("cursor expired (ttl 1s)",),
            )
        finally:
            client.request("mcp/settings/set", {"cursorTtlSecs": 600})

        # confirmTtlSecs 接线：preview 的 expiresAt 反映实际生效 TTL（30s）。
        # preview 不执行任何写。
        client.request("mcp/settings/set", {"confirmTtlSecs": 30})
        try:
            preview = call_tool(client, "ldap_entry_write", connectionId=conn_id,
                                action="delete", dn=f"ou=ttl-probe,{ROOT}")
            expires = datetime.fromisoformat(preview["expiresAt"].replace("Z", "+00:00"))
            delta = (expires - datetime.now(timezone.utc)).total_seconds()
            assert 24 <= delta <= 31, f"expiresAt should sit ~30s out, got {delta:.0f}s: {preview}"
        finally:
            client.request("mcp/settings/set", {"confirmTtlSecs": 60})
        return f"matched={matched}: deep paging + explicit reread + offset clamp + TTL expiry wiring"
    finally:
        try:
            client.request("connection/disconnect", {"connection": {"id": conn_id}})
        except Exception:
            pass


# -- stdio mode (M11/M12): standalone `--mcp` newline-delimited JSON-RPC ------
class McpStdioClient:
    """Drive the sidecar in standalone `--mcp` stdio MCP mode.

    MCP 2024-11-05: one JSON-RPC 2.0 message per line on stdin/stdout.
    Tool execution errors arrive as results with isError=true (never raised);
    protocol-level errors (unknown method, parse error) raise SidecarError.
    """

    def __init__(self, process: subprocess.Popen, timeout: float = 20.0):
        self.process = process
        self.timeout = timeout
        self.next_id = 100
        # 响应可能乱序写出（逐请求 goroutine）：不匹配当前等待 id 的帧先进
        # 缓冲，绝不丢弃——丢弃会让后续 read_frame/request 永远等不到
        # （M18 挂死根因，与 kafka K17/files T5 同源）。
        self.pending: list[dict] = []
        # os.read 的原始字节缓冲（_read_line 自行分行）：BufferedReader 的
        # 用户态缓冲会让 select 漏报就绪——表现成"响应丢失"假超时。
        self._rawbuf = b""

    @classmethod
    def start(cls, binary: str, data_dir: str, timeout: float = 20.0,
              extra_env: dict | None = None) -> "McpStdioClient":
        env = dict(os.environ)
        env["DBX_PLUGIN_DATA_DIR"] = data_dir
        env.update(extra_env or {})
        process = subprocess.Popen(
            [binary, "--mcp"],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=env,
        )
        return cls(process, timeout)

    def _send(self, message: dict) -> None:
        assert self.process.stdin
        self.process.stdin.write((json.dumps(message, ensure_ascii=False) + "\n").encode())
        self.process.stdin.flush()

    def _read_line(self, deadline: float) -> bytes:
        """One stdout line with a real mid-read deadline.

        Two traps avoided here: (1) plain readline() would block forever when
        the sidecar stops answering (the suite timeout only fires between
        reads); (2) select() on the BufferedReader reports "not ready" when a
        previous readline already pulled later frames into the userspace
        buffer — the data is there but the pipe is empty, which looks exactly
        like a lost response (M18 2/6 flake root cause). So: os.read + our
        own line buffer, never the buffered reader.
        """
        while True:
            index = self._rawbuf.find(b"\n")
            if index >= 0:
                line = self._rawbuf[:index + 1]
                self._rawbuf = self._rawbuf[index + 1:]
                return line
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise SidecarError("timeout waiting for MCP stdio response")
            ready, _, _ = select.select([self.process.stdout.fileno()], [], [], remaining)
            if not ready:
                raise SidecarError("timeout waiting for MCP stdio response")
            chunk = os.read(self.process.stdout.fileno(), 65536)
            if not chunk:
                raise SidecarError("MCP stdio sidecar closed stdout")
            self._rawbuf += chunk

    def _next_frame(self, request_id, deadline: float) -> dict:
        """Next response frame whose id matches (None = id null). True JSON-RPC
        notifications (no id key) never answer anything and are dropped; every
        other non-matching frame is buffered for the caller that awaits it."""
        while True:
            for index, message in enumerate(self.pending):
                if message.get("id") == request_id:
                    return self.pending.pop(index)
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise SidecarError("timeout waiting for MCP stdio response")
            line = self._read_line(deadline)
            if not line:
                raise SidecarError("MCP stdio sidecar closed stdout")
            text = line.decode(errors="replace").strip()
            if not text:
                continue
            message = json.loads(text)
            if "id" not in message:
                continue  # notification: silent by protocol
            self.pending.append(message)

    def request(self, method: str, params: dict | None = None, timeout: float | None = None) -> dict:
        if timeout:
            previous, self.timeout = self.timeout, timeout
        self.next_id += 1
        request_id = self.next_id
        self._send({"jsonrpc": "2.0", "id": request_id, "method": method, "params": params or {}})
        try:
            deadline = time.monotonic() + self.timeout
            while True:
                message = self._next_frame(request_id, deadline)
                if message.get("error") is not None:
                    error = message["error"]
                    raise SidecarError(error.get("message", ""), error.get("code"))
                return message.get("result") or {}
        finally:
            if timeout:
                self.timeout = previous

    def notify(self, method: str, params: dict | None = None) -> None:
        self._send({"jsonrpc": "2.0", "method": method, "params": params or {}})

    def call_tool(self, name: str, **arguments) -> dict:
        return self.request("tools/call", {"name": name, "arguments": arguments})

    # -- 可靠性纵深段（M18）用的低层原语 -------------------------------

    def send_raw(self, raw: bytes) -> None:
        """Send a pre-encoded stdin line verbatim (malformed JSON, CRLF, ...)."""
        assert self.process.stdin
        self.process.stdin.write(raw)
        self.process.stdin.flush()

    def read_frame(self, request_id, timeout: float | None = None) -> dict:
        """Read the next full JSON-RPC frame whose id matches (None = id null).
        Out-of-order frames are buffered (never discarded) — see pending."""
        if timeout:
            previous, self.timeout = self.timeout, timeout
        try:
            return self._next_frame(request_id, time.monotonic() + self.timeout)
        finally:
            if timeout:
                self.timeout = previous

    def alive(self) -> bool:
        """The sidecar process must keep serving (never crash on bad input)."""
        return self.process.poll() is None

    def close(self) -> None:
        try:
            if self.process.stdin:
                self.process.stdin.close()
            self.process.wait(timeout=5)
        except Exception:
            self.process.kill()


def unwrap_stdio(result: dict) -> dict:
    """Unwrap a successful tools/call content envelope into the JSON payload."""
    content = result.get("content") or []
    assert content and content[0].get("type") == "text", result
    assert result.get("isError") is not True, result
    return json.loads(content[0]["text"])


def expect_tool_error(result: dict, markers: tuple[str, ...]) -> str:
    assert result.get("isError") is True, result
    text = result["content"][0]["text"]
    for marker in markers:
        assert marker in text, f"{marker!r} missing from: {text}"
    return text


@scenario("M11", "stdio --mcp: initialize/tools/UNAVAILABLE/gates (offline)")
def m11_stdio_offline() -> str:
    data_dir = tempfile.mkdtemp(prefix="dbx-ldap-mcp-stdio-")
    client = McpStdioClient.start(default_binary(), data_dir)
    try:
        init = client.request("initialize", {"protocolVersion": "2024-11-05"})
        assert init["protocolVersion"] == "2024-11-05", init
        assert init["serverInfo"]["name"] == "io.dbx.ldap", init
        assert init["serverInfo"]["version"], init
        # notifications/initialized 不回包：下一条响应必须属于紧随其后的 ping。
        client.notify("notifications/initialized")
        assert client.request("ping") == {}, "ping after notification"
        # tools/list：8 工具照常列出；连接类工具补内联凭据声明并放宽 required。
        tools = client.request("tools/list")["tools"]
        names = [tool["name"] for tool in tools]
        assert len(names) == 8 and set(names) == set(EXPECTED_TOOLS), names
        digest = next(tool for tool in tools if tool["name"] == "ldap_search_digest")
        schema = digest["inputSchema"]
        for key in ("host", "port", "tlsMode", "startTls", "bindDn", "password", "baseDn"):
            assert key in schema["properties"], key
        assert "connectionId" not in schema["required"], schema
        # UI 类工具照常列出但 tools/call 明确 UNAVAILABLE，不假死。
        unavailable = client.call_tool("ldap_ui_focus", panel="tree")
        expect_tool_error(unavailable, ("UNAVAILABLE", "此工具需要 DBX 工作台"))
        # 连接解析门：缺内联凭据给引导错误（未知 connectionId 的桥兜底
        # fail-closed / mock 桥转发在 M14 专场景）。
        expect_tool_error(
            client.call_tool("ldap_search_digest", filter="(objectClass=*)"),
            ("host (required)",),
        )
        # 未知方法返回 JSON-RPC 错误不崩。
        try:
            client.request("mcp/nonexistent", {})
            raise AssertionError("expected -32601 for an unknown method")
        except SidecarError as cause:
            assert cause.code == -32601, cause
        return "initialize + tools-list(8 + inline schema) + ui UNAVAILABLE + gates"
    finally:
        client.close()


@scenario("M12", "stdio --mcp inline credentials: digest/cursor/two-phase (container)")
def m12_stdio_container() -> str:
    ok, reason = ldap_reachable()
    if not ok:
        raise SkipScenario(reason)
    data_dir = tempfile.mkdtemp(prefix="dbx-ldap-mcp-stdio-")
    client = McpStdioClient.start(default_binary(), data_dir)
    inline = dict(
        host=HOST, port=PORT, tlsMode="none", authType="simple",
        bindDn=BIND_DN, password=BIND_PW, baseDn=ROOT, timeoutSecs=10,
    )
    try:
        # digest：内联凭据真实查询（凭据只进进程内池化连接，不落盘）。
        digest = unwrap_stdio(client.call_tool(
            "ldap_search_digest", **inline,
            filter="(objectClass=*)", distinctAttr="objectClass"))
        assert digest["matched"] > 0 and digest["cursorId"], digest
        page = unwrap_stdio(client.call_tool("ldap_cursor_next", cursorId=digest["cursorId"], n=2))
        assert page["rows"] and len(page["rows"]) <= 2, page

        # 两阶段写全流程：add → delete preview → confirm → token 一次性。
        dn = f"ou=mcp-stdio-{uuid.uuid4().hex[:6]},{ROOT}"
        added = unwrap_stdio(client.call_tool(
            "ldap_entry_write", **inline, action="add", dn=dn,
            attributes={"objectClass": ["organizationalUnit"], "ou": [dn.split(",")[0][3:]]}))
        assert added["success"] is True, added
        preview = unwrap_stdio(client.call_tool("ldap_entry_write", **inline, action="delete", dn=dn))
        assert preview["preview"]["dn"] == dn and preview.get("confirmToken"), preview
        confirm = unwrap_stdio(client.call_tool(
            "ldap_entry_write", **inline, action="delete", dn=dn,
            confirmToken=preview["confirmToken"]))
        assert confirm["success"] is True, confirm
        expect_tool_error(
            client.call_tool("ldap_entry_write", **inline, action="delete", dn=dn,
                             confirmToken=preview["confirmToken"]),
            ("unknown or already used",),
        )
        # 审计：stdio 模式写路径照常落 audit.jsonl（source=mcp）。
        audit_path = os.path.join(data_dir, "audit.jsonl")
        sources = set()
        with open(audit_path, encoding="utf-8") as handle:
            for line in handle:
                record = json.loads(line)
                if record.get("source") == "mcp":
                    sources.add(record["action"])
        assert any("delete" in action for action in sources), sources
        return f"stdio digest matched={digest['matched']}; two-phase delete executed; token single-use"
    finally:
        client.close()


# -- stdio robustness (M18, 可靠性纵深轮): real-process adversarial protocol
#    input against the standalone `--mcp` server -----------------------------


@scenario("M18", "stdio robustness: malformed protocol input stays alive (offline)")
def m18_stdio_robustness() -> str:
    data_dir = tempfile.mkdtemp(prefix="dbx-ldap-mcp-stdio-")
    client = McpStdioClient.start(default_binary(), data_dir, timeout=45)
    try:
        client.request("initialize", {"protocolVersion": "2024-11-05"})
        # 1) 非法 JSON / 非法 UTF-8 字节流 → -32700（id null），进程存活。
        for name, raw in (
            ("garbage", b"not json\n"),
            ("truncated", b'{"jsonrpc":"2.0","id":1,"method":\n'),
            ("invalid-utf8", b"\xff\xfe\x7b\x7d\n"),
        ):
            client.send_raw(raw)
            frame = client.read_frame(None)
            assert frame["error"]["code"] == -32700 and frame["id"] is None, (name, frame)
        assert client.alive() and client.request("ping") == {}, "alive after malformed lines"

        # 2) notification（无 id，含未知通知名）→ 不响应，流不错位。
        client.send_raw(b'{"jsonrpc":"2.0","method":"notifications/unknown/x"}\n')
        assert client.request("ping") == {}, "notification must not corrupt the stream"

        # 3) 请求形状分档（MCP_ACCEPTANCE §2）：缺 id / id 为 object /
        #    method 缺失或非字符串 / jsonrpc 版本非法 → -32600 结构化报错。
        #    注意 read_frame 按各自回包 id 取帧：缺 id/id 非法回 null id，
        #    其余回显请求 id。
        for name, line, frame_id in (
            ("missing-id", '{"jsonrpc":"2.0","method":"ping"}', None),
            ("id-object", '{"jsonrpc":"2.0","id":{"a":1},"method":"ping"}', None),
            ("missing-method", '{"jsonrpc":"2.0","id":7}', 7),
            ("method-number", '{"jsonrpc":"2.0","id":8,"method":42}', 8),
            ("bad-version", '{"jsonrpc":"1.0","id":9,"method":"ping"}', 9),
        ):
            client.send_raw(line.encode() + b"\n")
            frame = client.read_frame(frame_id)
            assert frame["error"]["code"] == -32600, (name, frame)
            if frame_id is not None:
                assert frame["id"] == frame_id, (name, frame)
        assert client.request("ping") == {}, "alive after invalid request shapes"

        # 4) 8 MiB 单行 arguments → 优雅工具层报错（连接门引导），不 panic 不挂死。
        huge = "x" * (8 * 1024 * 1024)
        client.next_id += 1
        big_id = client.next_id
        client.send_raw(json.dumps({
            "jsonrpc": "2.0", "id": big_id, "method": "tools/call",
            "params": {"name": "ldap_search_digest",
                       "arguments": {"filter": f"(uid={huge})"}}}).encode() + b"\n")
        frame = client.read_frame(big_id)
        assert "error" not in frame and frame["result"]["isError"] is True, frame
        assert client.request("ping") == {}, "alive after the 8MiB line"

        # 5) pipelining：不等待响应连发 3 个不同 id 请求 → 一一对应。
        client.next_id += 1
        ids = (client.next_id, client.next_id + 1, client.next_id + 2)
        client.send_raw(json.dumps({"jsonrpc": "2.0", "id": ids[0], "method": "ping"}).encode() + b"\n")
        client.send_raw(json.dumps({"jsonrpc": "2.0", "id": ids[1], "method": "mcp/nope"}).encode() + b"\n")
        client.send_raw(json.dumps({"jsonrpc": "2.0", "id": ids[2], "method": "ping"}).encode() + b"\n")
        seen = set()
        for request_id in ids:
            frame = client.read_frame(request_id)
            if "error" in frame:
                assert frame["error"]["code"] == -32601, frame
            seen.add(frame["id"])
        assert seen == set(ids), (seen, ids)
        assert client.request("ping") == {}, "alive after pipelining"

        # 6) 空行 / CRLF 容忍。
        client.send_raw(b"\r\n\n   \r\n")
        assert client.request("ping") == {}, "blank/CRLF lines tolerated"
        assert client.alive(), "process must survive the whole flood"
        return ("malformed bytes -32700; notifications silent; shapes -32600; 8MiB line; "
                "pipelining 3 ids; CRLF — alive throughout")
    finally:
        client.close()


# -- digest repeat idempotency (M19, 可靠性纵深轮): repeated identical reads
#    stay stable on real directory data --------------------------------------


@scenario("M19", "digest repeat idempotency + cursor reread stability (container)")
def m19_digest_idempotent(client: SidecarClient) -> str:
    ok, reason = ldap_reachable()
    if not ok:
        raise SkipScenario(reason)
    conn_id = f"smoke-mcp-idem-{uuid.uuid4().hex[:6]}"
    client.request("connection/connect", lifecycle_params(make_connection(conn_id)))
    try:
        # 重复搜索结果稳定：同参数两次 digest，matched/stats 一致、各自物化
        # 独立 cursor。
        first = call_tool(client, "ldap_search_digest", connectionId=conn_id,
                          filter="(objectClass=*)", distinctAttr="objectClass")
        second = call_tool(client, "ldap_search_digest", connectionId=conn_id,
                           filter="(objectClass=*)", distinctAttr="objectClass")
        assert first["matched"] == second["matched"] > 0, (first["matched"], second["matched"])
        assert first["stats"]["objectClass"] == second["stats"]["objectClass"], "stats must be stable"
        assert first["cursorId"] != second["cursorId"], "each digest materializes its own cursor"

        # 同 cursor 同 offset 重读：行内容稳定（定位字段不漂移）。
        page_a = call_tool(client, "ldap_cursor_next", cursorId=first["cursorId"], offset=0, n=5)
        page_b = call_tool(client, "ldap_cursor_next", cursorId=first["cursorId"], offset=0, n=5)
        assert [row["dn"] for row in page_a["rows"]] == [row["dn"] for row in page_b["rows"]], "reread must be stable"

        # 两阶段 preview 幂等：同参数两次 preview 各自签发独立 token，均不执行写。
        dn = f"ou=mcp-idem-{uuid.uuid4().hex[:6]},{ROOT}"
        add_entry(client, conn_id, dn, {"objectClass": ["organizationalUnit"],
                                        "ou": dn.split(",")[0][3:]})
        try:
            preview_a = call_tool(client, "ldap_entry_write", connectionId=conn_id,
                                  action="delete", dn=dn)
            preview_b = call_tool(client, "ldap_entry_write", connectionId=conn_id,
                                  action="delete", dn=dn)
            assert preview_a["confirmToken"] != preview_b["confirmToken"], "each preview gets its own token"
        finally:
            recursive_delete(client, conn_id, dn)
        return f"matched stable at {first['matched']}; cursor reread stable; per-preview tokens"
    finally:
        try:
            client.request("connection/disconnect", {"connection": {"id": conn_id}})
        except Exception:
            pass


# -- stdio bridge fallback (M14, design §5 stdio row + ssh smoke scenario 8) --


class MockBridge:
    """Local mock of the DBX app's `/call-plugin-tool` TCP bridge.

    Minimal host-contract implementation: publishes its port in
    `<app_data_dir>/mcp-bridge-port`, records every request (path + snake_case
    body), and replies with a configurable status/body. Lets the forward path
    be exercised without a real DBX.app.
    """

    def __init__(self, app_data: str, status: int = 200, body: dict | None = None):
        self.requests: list[tuple[str, dict]] = []
        self.status = status
        self.body = body or {
            "content": [{"type": "text", "text": json.dumps({"matched": 7})}],
            "isError": False,
        }
        outer = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                length = int(self.headers.get("Content-Length", "0"))
                outer.requests.append(
                    (self.path, json.loads(self.rfile.read(length) or b"{}"))
                )
                payload = json.dumps(outer.body).encode()
                self.send_response(outer.status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)

            def log_message(self, *args):
                pass

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        port = self.server.server_address[1]
        with open(os.path.join(app_data, "mcp-bridge-port"), "w", encoding="utf-8") as handle:
            handle.write(str(port))
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def stop(self) -> None:
        self.server.shutdown()
        self.server.server_close()


@scenario("M14", "stdio bridge fallback: fail-closed + mock-bridge forward")
def m14_bridge_fallback() -> str:
    # 1) 桥未发布：空 app-data + no-op launch（`:`，无 UI 弹出）→ ensure 跑满
    #    app-start 预算（30s，与 ssh smoke 场景 8 同款：这正是被测行为）→
    #    fail-closed 可行动错误（桥失败原因 + 内联凭据出路），不假死。
    app_data = tempfile.mkdtemp(prefix="dbx-ldap-mcp-appdata-")
    client = McpStdioClient.start(
        default_binary(), tempfile.mkdtemp(prefix="dbx-ldap-mcp-stdio-"),
        timeout=45,
        extra_env={"DBX_APP_DATA_DIR": app_data, "DBX_APP_LAUNCH_CMD": ":"},
    )
    try:
        client.request("initialize", {"protocolVersion": "2024-11-05"})
        result = client.call_tool("ldap_search_digest", connectionId="mcp-nope",
                                  filter="(objectClass=*)")
        text = expect_tool_error(result, ("DBX app bridge", "inline connection parameters"))
        assert "mcp-nope" in text, text
    finally:
        client.close()

    # 2) 桥存在（本地 mock 桥）：未池化 connectionId 转发宿主契约五字段，
    #    应用侧 MCP envelope 逐字透传为 stdio 响应；本地池不被污染。
    app_data = tempfile.mkdtemp(prefix="dbx-ldap-mcp-appdata-")
    mock = MockBridge(app_data)
    client = McpStdioClient.start(
        default_binary(), tempfile.mkdtemp(prefix="dbx-ldap-mcp-stdio-"),
        extra_env={"DBX_APP_DATA_DIR": app_data, "DBX_APP_LAUNCH_CMD": ":"},
    )
    try:
        client.request("initialize", {"protocolVersion": "2024-11-05"})
        forwarded = unwrap_stdio(client.call_tool(
            "ldap_search_digest", connectionId="saved-jane",
            filter="(uid=jane)", timeoutSecs="30"))
        assert forwarded == {"matched": 7}, forwarded
        assert len(mock.requests) == 1, mock.requests
        path, body = mock.requests[0]
        assert path == "/call-plugin-tool", path
        assert body["plugin_id"] == "io.dbx.ldap", body
        assert body["connection_id"] == "saved-jane" and body["tool"] == "ldap_search_digest", body
        assert body["arguments"]["filter"] == "(uid=jane)", body
        assert body["timeout_ms"] == 30000, body
    finally:
        client.close()
        mock.stop()

    # 3) 桥 404（连接不存在/旧版应用）：错误带 "DBX app bridge returned HTTP"。
    app_data = tempfile.mkdtemp(prefix="dbx-ldap-mcp-appdata-")
    mock = MockBridge(app_data, status=404,
                      body={"error": "Connection with id 'ghost' not found"})
    client = McpStdioClient.start(
        default_binary(), tempfile.mkdtemp(prefix="dbx-ldap-mcp-stdio-"),
        extra_env={"DBX_APP_DATA_DIR": app_data, "DBX_APP_LAUNCH_CMD": ":"},
    )
    try:
        client.request("initialize", {"protocolVersion": "2024-11-05"})
        result = client.call_tool("ldap_search_digest", connectionId="ghost", filter="x")
        expect_tool_error(result, ("DBX app bridge returned HTTP 404",))
    finally:
        client.close()
        mock.stop()
    return "fail-closed without the app; mock-bridge forward contract + envelope verbatim; 404 surfaced"


# -- container scenario M20 (round 7: enum errors + case normalization) ---------


@scenario("M20", "enum invalid values list valid options + case normalization (container)")
def m20_enum_online(client: SidecarClient) -> str:
    """第七轮（契约 §3.3 在线钉桩）：带真实连接的调用里 enum 参数传非法值，
    报错必须列出 schema enum 全部合法值；大小写归一在线生效（SUBTREE 正常
    消费）。离线探针（M13）够不到的在线段在这里补齐。"""
    ok, reason = ldap_reachable()
    if not ok:
        raise SkipScenario(reason)
    conn_id = f"smoke-mcp-enum-{uuid.uuid4().hex[:6]}"
    client.request("connection/connect", lifecycle_params(make_connection(conn_id)))
    try:
        # scope="bogus"：报错列出 schema enum（base/one/sub）+ RFC 别名。
        scope_error = expect_error(
            lambda: call_tool(client, "ldap_search_digest", connectionId=conn_id,
                              filter="(objectClass=*)", scope="bogus"),
            ("scope must be base, one, or sub",),
        )
        assert "base" in str(scope_error) and "one" in str(scope_error) and "sub" in str(scope_error), scope_error
        assert "baseobject" in str(scope_error).lower() and "subtree" in str(scope_error).lower(), scope_error
        assert "bogus" in str(scope_error), scope_error

        # action="bogus"：报错列出 schema enum（add/modify/delete/modifyDn）。
        action_error = expect_error(
            lambda: call_tool(client, "ldap_entry_write", connectionId=conn_id,
                              action="bogus", dn=f"uid=x,{ROOT}"),
            ("action must be add, modify, delete, or modifydn",),
        )
        assert "add" in str(action_error) and "modifydn" in str(action_error).lower(), action_error

        # 大小写归一在线钉一组：scope="SUBTREE" 正常工作（= sub 全扫）。
        digest = call_tool(client, "ldap_search_digest", connectionId=conn_id,
                           filter="(objectClass=*)", scope="SUBTREE")
        assert digest["matched"] > 0, digest
        assert digest["cursorId"], digest
        return f"scope/action enum errors list options; SUBTREE normalized (matched={digest['matched']})"
    finally:
        try:
            client.request("connection/disconnect", {"connection": {"id": conn_id}})
        except Exception:
            pass


# -- runner ----------------------------------------------------------------------


def report() -> None:
    widths = (4, 44, 6)
    print(f"{'No.':<{widths[0]}} {'Scenario':<{widths[1]}} {'Status':<{widths[2]}} Detail")
    for result in RESULTS:
        print(f"{result.no:<{widths[0]}} {result.name:<{widths[1]}} {result.status:<{widths[2]}} {result.detail}")
    counts = {status: sum(1 for r in RESULTS if r.status == status) for status in ("PASS", "FAIL", "SKIP")}
    print(f"\ntotal={len(RESULTS)} PASS={counts['PASS']} FAIL={counts['FAIL']} SKIP={counts['SKIP']}")


def _all_scenarios():
    return [m1_settings, m2_tools, m3_readonly_tools, m4_readonly_write, m5_intent_pending,
            m6_intent_applied, m7_ui_state, m8_digest_gates, m9_unknown, m13_input_variants,
            m10_container, m15_digest_variants, m16_schema_behavior, m17_cursor_edges,
            m19_digest_idempotent, m20_enum_online]


def _stdio_scenarios():
    # stdio 模式自持进程（--mcp），不走 jsonl client 生命周期。
    return [m11_stdio_offline, m12_stdio_container, m18_stdio_robustness, m14_bridge_fallback]


def main() -> int:
    binary = default_binary()
    if not os.path.exists(binary):
        message = f"sidecar binary not found: {binary} (set DBX_PLUGIN_SIDECAR)"
        for fn in _all_scenarios() + _stdio_scenarios():
            RESULTS.append(ScenarioResult(fn.no, fn.name, "FAIL" if REQUIRE else "SKIP", message))
        report()
        return 0

    # 隔离数据目录：mcp-settings.json 与 audit.jsonl 不污染真实插件数据。
    data_dir = tempfile.mkdtemp(prefix="dbx-ldap-mcp-smoke-")
    client = SidecarClient.start(data_dir=data_dir)
    try:
        client.initialize()
        for fn in _all_scenarios():
            fn(client)
    finally:
        try:
            client.request("connection/disconnect", {"connection": {"id": "smoke-mcp-ro"}})
        except Exception:
            pass
        try:
            client.close()
        except Exception:
            pass

    for fn in _stdio_scenarios():
        fn()

    report()
    return 1 if any(result.status == "FAIL" for result in RESULTS) else 0


if __name__ == "__main__":
    raise SystemExit(main())
