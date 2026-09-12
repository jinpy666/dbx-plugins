#!/usr/bin/env python3
"""MCP smoke test for the dbx-ldap-plugin sidecar (M1, scenarios M1-M10).

Covers the shared/IMPL_PLAN_PLUGIN_MCP.zh-CN.md (v2) §1/§2/§4 tool face over
the stdio-jsonl protocol (mcp/tools + mcp/call + mcp/settings, the ssh
mcp.rs skeleton ported to Go):

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

SKIP semantics (M0 §5.2, same as smoke_test.py):
  * method/tool not registered  -> SKIP (never FAIL);
  * no sidecar binary           -> whole suite SKIP (FAIL with LDAP_TEST_REQUIRE=1);
  * M10 additionally SKIPs without a reachable OpenLDAP test container.

Usage:
    DBX_PLUGIN_SIDECAR=/path/to/dbx-plugin-ldap python3 scripts/smoke_mcp.py
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
        ("connectionid is required",),
    )
    return "unknown connection / cursor / missing connectionId all refused"


@scenario("M9", "unknown tool and unknown method")
def m9_unknown(client: SidecarClient) -> str:
    expect_error(
        lambda: client.request("mcp/call", {"tool": "ldap_nonexistent", "arguments": {}}),
        ("unknown tool",),
    )
    try:
        client.request("mcp/nonexistent", {})
        raise AssertionError("expected -32601 for an unregistered method")
    except SidecarError as cause:
        if not is_method_not_registered(cause):
            raise AssertionError(f"expected method-not-registered, got: {cause}") from cause
    return "unknown tool -32000; unknown method -32601"


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
            m6_intent_applied, m7_ui_state, m8_digest_gates, m9_unknown, m10_container]


def main() -> int:
    binary = default_binary()
    if not os.path.exists(binary):
        message = f"sidecar binary not found: {binary} (set DBX_PLUGIN_SIDECAR)"
        for fn in _all_scenarios():
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

    report()
    return 1 if any(result.status == "FAIL" for result in RESULTS) else 0


if __name__ == "__main__":
    raise SystemExit(main())
