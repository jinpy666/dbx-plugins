package mcp

// stdio_test.go：standalone `--mcp` stdio 模式纯逻辑单测（S-STDIO-*）：
// JSON-RPC 协议循环、UNAVAILABLE 分支、内联凭据池化键、连接解析门。
// 全部离线（不拨号：连接类断言停在解析/注册层，注册是惰性建连）。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func newTestStdioServer() *StdioServer {
	return NewStdioServer("0.0.0-test", nil, nil)
}

// decodeResponse 把响应帧解为通用 map。
func decodeResponse(t *testing.T, response map[string]any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func errorOf(t *testing.T, decoded map[string]any) map[string]any {
	t.Helper()
	errorFrame, _ := decoded["error"].(map[string]any)
	return errorFrame
}

// S-STDIO-1 initialize：协议版本 + serverInfo（name/版本注入）。
func TestStdioInitialize(t *testing.T) {
	server := newTestStdioServer()
	decoded := decodeResponse(t, server.handleLine([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)))
	if errorOf(t, decoded) != nil {
		t.Fatalf("initialize must not fail: %v", decoded)
	}
	result := decoded["result"].(map[string]any)
	if result["protocolVersion"] != MCPProtocolVersion {
		t.Fatalf("protocolVersion: %v", result["protocolVersion"])
	}
	info := result["serverInfo"].(map[string]any)
	if info["name"] != "io.dbx.ldap" || info["version"] != "0.0.0-test" {
		t.Fatalf("serverInfo: %v", info)
	}
	if decoded["id"] != float64(1) {
		t.Fatalf("id roundtrip: %v", decoded["id"])
	}
}

// S-STDIO-2 通知不回包；ping 空结果；未知方法 -32601；坏 JSON -32700。
func TestStdioProtocolSemantics(t *testing.T) {
	server := newTestStdioServer()
	if response := server.handleLine([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); response != nil {
		t.Fatalf("notification must stay silent, got %v", response)
	}
	decoded := decodeResponse(t, server.handleLine([]byte(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)))
	if _, ok := decoded["result"].(map[string]any); !ok {
		t.Fatalf("ping result: %v", decoded)
	}
	decoded = decodeResponse(t, server.handleLine([]byte(`{"jsonrpc":"2.0","id":3,"method":"mcp/nonexistent","params":{}}`)))
	if got := errorOf(t, decoded); got == nil || got["code"] != float64(-32601) {
		t.Fatalf("unknown method must be -32601: %v", decoded)
	}
	decoded = decodeResponse(t, server.handleLine([]byte(`not json`)))
	if got := errorOf(t, decoded); got == nil || got["code"] != float64(-32700) {
		t.Fatalf("parse error must be -32700: %v", decoded)
	}
}

// S-STDIO-3 tools/list：复用注册表全量 8 工具；连接类工具补内联参数声明
// 并把 required 的 connectionId 放宽为 anyOf；UI 工具 schema 不动。
func TestStdioToolsList(t *testing.T) {
	server := newTestStdioServer()
	decoded := decodeResponse(t, server.handleLine([]byte(`{"jsonrpc":"2.0","id":4,"method":"tools/list"}`)))
	tools := decoded["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 8 {
		t.Fatalf("tool count: %d", len(tools))
	}
	byName := map[string]map[string]any{}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		byName[tool["name"].(string)] = tool
	}
	digest := byName["ldap_search_digest"]["inputSchema"].(map[string]any)
	properties := digest["properties"].(map[string]any)
	for _, key := range []string{"host", "port", "tlsMode", "startTls", "bindDn", "password", "baseDn", "readOnly"} {
		if _, ok := properties[key]; !ok {
			t.Fatalf("inline property %q missing from digest schema", key)
		}
	}
	for _, entry := range digest["required"].([]any) {
		if entry == "connectionId" {
			t.Fatalf("connectionId must be relaxed out of required: %v", digest["required"])
		}
	}
	if digest["anyOf"] == nil {
		t.Fatal("anyOf selector requirement missing")
	}
	ui := byName["ldap_ui_focus"]["inputSchema"].(map[string]any)
	if _, ok := ui["properties"].(map[string]any)["host"]; ok {
		t.Fatal("UI tool schema must stay untouched")
	}
	if _, ok := ui["anyOf"]; ok {
		t.Fatal("UI tool must not gain anyOf")
	}
}

// S-STDIO-4 UNAVAILABLE 分支：UI 类工具 isError content 明确不假死。
func TestStdioUIToolsUnavailable(t *testing.T) {
	server := newTestStdioServer()
	for _, name := range []string{"ldap_ui_focus", "ldap_ui_search", "ldap_ui_select", "ldap_ui_state", "ldap_ui_schema"} {
		decoded := decodeResponse(t, server.handleLine([]byte(
			`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"`+name+`","arguments":{}}}`)))
		result := decoded["result"].(map[string]any)
		if result["isError"] != true {
			t.Fatalf("%s must return isError=true: %v", name, result)
		}
		content := result["content"].([]any)[0].(map[string]any)
		text := content["text"].(string)
		if !strings.Contains(text, "UNAVAILABLE") || !strings.Contains(text, "此工具需要 DBX 工作台") {
			t.Fatalf("%s text: %s", name, text)
		}
	}
}

// S-STDIO-5 连接解析门：缺参引导 / 未知 connectionId 走桥兜底（无桥时
// fail-closed 引导错误）/ 已池化 id 直通（不触网）。
func TestStdioConnectionGate(t *testing.T) {
	server := newTestStdioServer()
	server.bridgeEnsureWait = 50 * time.Millisecond
	missing := server.callTool("ldap_search_digest", map[string]any{"filter": "(objectClass=*)"})
	if missing["isError"] != true || !strings.Contains(missing["content"].([]map[string]any)[0]["text"].(string), "host (required)") {
		t.Fatalf("missing-params guidance: %v", missing)
	}
	// 未知 connectionId：桥未发布（空 app-data）→ fail-closed 合并错误
	// （桥失败原因 + 内联凭据出路），不假死。
	t.Setenv("DBX_APP_DATA_DIR", t.TempDir())
	t.Setenv("DBX_APP_LAUNCH_CMD", ":")
	unknown := server.callTool("ldap_search_digest", map[string]any{"connectionId": "mcp-nope", "filter": "(objectClass=*)"})
	text := unknown["content"].([]map[string]any)[0]["text"].(string)
	if unknown["isError"] != true || !strings.Contains(text, "DBX app bridge") || !strings.Contains(text, "inline connection parameters") {
		t.Fatalf("unknown connectionId guidance: %v", unknown)
	}
	// 已池化 id：解析直通（不注入不覆盖，更不走桥）。
	id, err := server.pooledConnectionID(inlineConn{Host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"connectionId": id}
	forwarded, handled, err := server.resolveConnectionOrForward("ldap_search_digest", args)
	if err != nil || handled || forwarded != nil {
		t.Fatalf("pooled id must stay local: %v %v %v", forwarded, handled, err)
	}
	if args["connectionId"] != id {
		t.Fatalf("pooled id must stay untouched: %v", args)
	}
}

// S-STDIO-6 内联凭据池化键：同参数同键（键序无关）、异凭据异键、别名归一、
// presence 判定。
func TestStdioInlinePoolKey(t *testing.T) {
	base := inlineConn{Host: "h", Password: "p"}
	if base.poolKey() != (inlineConn{Host: "h", Password: "p"}).poolKey() {
		t.Fatal("same params must share a pool key")
	}
	if base.poolKey() == (inlineConn{Host: "h", Password: "other"}).poolKey() {
		t.Fatal("different credentials must not collide")
	}
	a, okA := parseInlineConn(map[string]any{"host": "h", "port": float64(389), "password": "p", "tlsMode": "starttls"})
	b, okB := parseInlineConn(map[string]any{"tlsMode": "starttls", "password": "p", "port": float64(389), "host": "h"})
	if !okA || !okB || a.poolKey() != b.poolKey() {
		t.Fatal("argument order must not change the pool key")
	}
	alias, _ := parseInlineConn(map[string]any{"host": "h", "startTls": true})
	if alias.TLSMode != "starttls" {
		t.Fatalf("startTls alias: %+v", alias)
	}
	bindPassword, _ := parseInlineConn(map[string]any{"host": "h", "bindPassword": "x"})
	if bindPassword.Password != "x" {
		t.Fatalf("bindPassword alias: %+v", bindPassword)
	}
	if _, present := parseInlineConn(map[string]any{"password": "x"}); present {
		t.Fatal("absent host must report not-present")
	}
}

// S-STDIO-7 toLifecycle：内联参数折算标准 lifecycle 形状（host/port 进
// connection + runtime，凭据进 secrets，策略进 external_config）。
func TestStdioInlineToLifecycle(t *testing.T) {
	inline, present := parseInlineConn(map[string]any{
		"host": "ldap.example.com", "port": float64(636), "tlsMode": "ldaps",
		"bindDn": "cn=svc", "password": "pw", "baseDn": "dc=example,dc=org", "readOnly": true,
	})
	if !present {
		t.Fatal("inline params must be present")
	}
	params := inline.toLifecycle("mcp-x")
	if params.ConnectionID() != "mcp-x" || params.Connection.Host != "ldap.example.com" || params.Connection.Port != 636 {
		t.Fatalf("connection shape: %+v", params.Connection)
	}
	if params.Runtime.Host != "ldap.example.com" || params.Runtime.Port != 636 {
		t.Fatalf("runtime endpoint: %+v", params.Runtime)
	}
	if params.ConfigString("tls_mode") != "ldaps" || params.ConfigString("bind_dn") != "cn=svc" ||
		params.ConfigString("base_dn") != "dc=example,dc=org" || !params.ConfigBool("read_only") {
		t.Fatalf("external_config: %+v", params.Connection.ExternalConfig)
	}
	if params.SecretString("bind_password") != "pw" {
		t.Fatalf("secrets: %+v", params.Connection.Secrets)
	}
}

// S-STDIO-8 池淘汰：上限 8，最旧淘汰且底层连接断开（svc.Disconnect 幂等）。
func TestStdioPoolCapEviction(t *testing.T) {
	server := newTestStdioServer()
	ids := make([]string, 0, inlinePoolCap+1)
	for index := 0; index <= inlinePoolCap; index++ {
		id, err := server.pooledConnectionID(inlineConn{Host: fmt.Sprintf("h-%d", index)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if len(server.hash) != inlinePoolCap || len(server.ids) != inlinePoolCap {
		t.Fatalf("pool size: %d/%d", len(server.hash), len(server.ids))
	}
	if server.poolHas(ids[0]) {
		t.Fatal("oldest entry must be evicted")
	}
	if !server.poolHas(ids[inlinePoolCap]) {
		t.Fatal("newest entry must stay")
	}
	// 命中复用：同参数再次池化返回同一 id 且不扩张。
	dup, err := server.pooledConnectionID(inlineConn{Host: fmt.Sprintf("h-%d", inlinePoolCap)})
	if err != nil || dup != ids[inlinePoolCap] || len(server.hash) != inlinePoolCap {
		t.Fatalf("pool hit: %v %v %d", dup, err, len(server.hash))
	}
}

// S-STDIO-9 会话类工具直通 Server.Call（cursor 语义复用，不触连接门）。
func TestStdioCursorNextReusesCall(t *testing.T) {
	server := newTestStdioServer()
	result := server.callTool("ldap_cursor_next", map[string]any{"cursorId": "cur-nope"})
	if result["isError"] != true {
		t.Fatalf("cursor gate: %v", result)
	}
	if !strings.Contains(result["content"].([]map[string]any)[0]["text"].(string), "unknown cursorId") {
		t.Fatalf("cursor text: %v", result)
	}
}

// S-STDIO-10 Serve 循环：喂行读响应；通知不回包；响应合法 NDJSON 且按 id
// 关联（乱序容忍）。
func TestStdioServeLoop(t *testing.T) {
	server := newTestStdioServer()
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`not json`,
		`{"jsonrpc":"2.0","id":3,"method":"mcp/nonexistent","params":{}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"ldap_ui_focus","arguments":{"panel":"tree"}}}`,
		"",
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := server.Serve(strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("expect 5 responses (notification silent), got %d:\n%s", len(lines), out.String())
	}
	byID := map[string]map[string]any{}
	for _, line := range lines {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("response line not JSON: %q", line)
		}
		id := "\"null\""
		if raw, err := json.Marshal(decoded["id"]); err == nil {
			id = string(raw)
		}
		byID[id] = decoded
	}
	if got := byID[`1`]["result"].(map[string]any)["protocolVersion"]; got != MCPProtocolVersion {
		t.Fatalf("initialize via loop: %v", got)
	}
	if _, ok := byID[`2`]["result"]; !ok {
		t.Fatalf("ping via loop: %v", byID[`2`])
	}
	if got := errorOf(t, byID[`null`]); got == nil || got["code"] != float64(-32700) {
		t.Fatalf("parse error via loop: %v", byID[`null`])
	}
	if got := errorOf(t, byID[`3`]); got == nil || got["code"] != float64(-32601) {
		t.Fatalf("unknown method via loop: %v", byID[`3`])
	}
	if got := byID[`4`]["result"].(map[string]any)["isError"]; got != true {
		t.Fatalf("ui unavailable via loop: %v", byID[`4`])
	}
}
