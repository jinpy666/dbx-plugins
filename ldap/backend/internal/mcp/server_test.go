package mcp

// server_test.go：Server 层纯逻辑（settings 通道 + 响应上限；验收用例
// S-SRV-*，清单见 shared/frontend/README.zh-CN.md）。

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"io.dbx.ldap.plugin/internal/ldapconn"
	"io.dbx.ldap.plugin/internal/lifecycle"
	"io.dbx.ldap.plugin/internal/store"
)

func TestServerSettingsGetSetRoundtrip(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ldapconn.NewService(), st)

	got := server.SettingsGet()["settings"].(Settings)
	if got != DefaultSettings() {
		t.Fatalf("initial settings mismatch: %+v", got)
	}
	updated, err := server.SettingsSet(map[string]any{"reportWaitMs": float64(200), "cellWidth": float64(60)})
	if err != nil {
		t.Fatal(err)
	}
	if updated["responseLimitBytes"] != 16*1024 {
		t.Fatalf("responseLimitBytes should stay default: %+v", updated)
	}
	// 新 Server 同目录加载持久化值。
	reloaded := NewServer(ldapconn.NewService(), st).SettingsGet()["settings"].(Settings)
	if reloaded.ReportWaitMs != 200 || reloaded.CellWidth != 60 {
		t.Fatalf("persistence mismatch: %+v", reloaded)
	}
	if _, err := server.SettingsSet(nil); err == nil {
		t.Fatal("nil updates must be rejected")
	}
}

func TestServerEnforceResponseLimit(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	server.settings.ResponseLimitBytes = 512

	big := map[string]any{
		"matched": 1,
		"rows":    []map[string]any{{"dn": strings.Repeat("x", 2000)}},
		"sample":  []map[string]any{{"dn": strings.Repeat("y", 2000)}},
		"stats":   map[string]any{"objectClass": map[string]int{"a": 1}},
	}
	trimmed := server.enforceResponseLimit(big)
	if trimmed["truncated"] != true {
		t.Fatal("trimmed result must set truncated")
	}
	if _, has := trimmed["rows"]; has {
		t.Fatal("rows should be dropped first")
	}
	if _, has := trimmed["matched"]; !has {
		t.Fatal("light fields survive trimming")
	}

	// rows 可丢弃：丢掉后回到限额内（保留 stats），置 truncated。
	droppable := map[string]any{"rows": []map[string]any{{"dn": strings.Repeat("z", 4096)}}, "stats": map[string]any{"objectClass": map[string]int{"a": 1}}}
	trimmedRows := server.enforceResponseLimit(droppable)
	if trimmedRows["truncated"] != true {
		t.Fatal("dropping rows must set truncated")
	}
	if _, has := trimmedRows["stats"]; !has {
		t.Fatal("stats survive when rows alone overflow")
	}

	// 不可丢弃字段本身就超限：最终占位响应。
	huge := map[string]any{"baseDn": strings.Repeat("z", 4096), "rows": []map[string]any{{"dn": strings.Repeat("z", 4096)}}}
	final := server.enforceResponseLimit(huge)
	if final["truncated"] != true || final["note"] == nil {
		t.Fatalf("final fallback shape mismatch: %+v", final)
	}

	small := map[string]any{"matched": 1}
	if server.enforceResponseLimit(small)["matched"] != 1 {
		t.Fatal("small results pass through untouched")
	}
}

func TestServerCallUnknownTool(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	if _, err := server.Call("ldap_nonexistent", map[string]any{}); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("unknown tool must error: %v", err)
	}
}

func TestServerUIStateSnapshotWithoutIntent(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	if err := server.ReportUIState(map[string]any{"summary": map[string]any{"panel": "search", "count": float64(7)}}); err != nil {
		t.Fatal(err)
	}
	result, err := server.Call("ldap_ui_state", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := result["snapshot"].(map[string]any)
	if snapshot["panel"] != "search" || snapshot["count"] != float64(7) {
		t.Fatalf("snapshot mismatch: %+v", snapshot)
	}
}

func TestServerUIStateReportUnknownIntentGuidance(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	// 未知/过期 intentId 的 report 必须报错，且错误带可行动自纠指引
	// （60s 进程内过期；重发 ldap_ui_* 或省略 intentId 读最新快照）。
	err := server.ReportUIState(map[string]any{
		"intentId": "i-nope",
		"status":   "applied",
		"summary":  map[string]any{"panel": "search"},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown or expired") {
		t.Fatalf("unknown intentId must error: %v", err)
	}
	for _, hint := range []string{"60s", "ldap_ui_*", "omit intentId"} {
		if !strings.Contains(err.Error(), hint) {
			t.Fatalf("unknown intent error must self-heal with %q: %v", hint, err)
		}
	}
}

func TestServerCursorNextErrors(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	if _, err := server.Call("ldap_cursor_next", map[string]any{}); err == nil {
		t.Fatal("missing cursorId must error")
	}
	if _, err := server.Call("ldap_cursor_next", map[string]any{"cursorId": "cur-nope"}); err == nil || !strings.Contains(err.Error(), "unknown cursorId") {
		t.Fatalf("unknown cursor must error: %v", err)
	}
	// 容错性：错误给可行动指引（重发 digest），不是干巴巴的 id 回显。
	_, err := server.Call("ldap_cursor_next", map[string]any{"cursorId": "cur-nope"})
	if !strings.Contains(err.Error(), "ldap_search_digest") {
		t.Fatalf("unknown cursor error must suggest ldap_search_digest: %v", err)
	}
}

func TestServerUnknownToolListsAvailable(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	_, err := server.Call("ldap_serach_digest", nil) // 拼写错误形态
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("unknown tool must error: %v", err)
	}
	for _, name := range toolNames() {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("unknown tool error must list %q for self-correction: %v", name, err)
		}
	}
}

func TestServerUIFocusPanelValidation(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	server.settings.ReportWaitMs = 60 // 无前端：快速 pending
	// 非法 panel：本地直接报错并列出合法值，不发注定 rejected 的 intent。
	_, err := server.Call("ldap_ui_focus", map[string]any{"panel": "main"})
	if err == nil || !strings.Contains(err.Error(), "panel must be search, tree, or schema") {
		t.Fatalf("invalid panel must be refused with legal values: %v", err)
	}
	// 大小写变体归一化（前端拿到规范值）。
	result, err := server.Call("ldap_ui_focus", map[string]any{"panel": "TREE"})
	if err != nil {
		t.Fatal(err)
	}
	_ = result // pending/applied 均可；关键是不报错
}

func TestServerUISearchNormalizesIntentParams(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	server.settings.ReportWaitMs = 60
	var captured map[string]any
	server.SetEmitter(func(method string, params any) {
		if method == "ldap/ui/intent" {
			envelope := params.(map[string]any)
			captured = envelope["params"].(map[string]any)
		}
	})
	if _, err := server.Call("ldap_ui_search", map[string]any{
		"filter":     "(uid=a)",
		"scope":      "SUB",
		"attributes": "cn, mail",
		"sizeLimit":  "50",
	}); err != nil {
		t.Fatal(err)
	}
	if captured == nil {
		t.Fatal("intent event not emitted")
	}
	if captured["scope"] != "sub" {
		t.Fatalf("scope must be normalized: %#v", captured["scope"])
	}
	if attrs, ok := captured["attributes"].([]string); !ok || !reflect.DeepEqual(attrs, []string{"cn", "mail"}) {
		t.Fatalf("attributes must parse the comma string: %#v", captured["attributes"])
	}
	if captured["sizeLimit"] != 50 {
		t.Fatalf("sizeLimit must be coerced to int: %#v (%T)", captured["sizeLimit"], captured["sizeLimit"])
	}
	// 非法 scope：报错而不是静默按 sub 扫描。
	if _, err := server.Call("ldap_ui_search", map[string]any{"filter": "(uid=a)", "scope": "children"}); err == nil || !strings.Contains(err.Error(), "scope must be") {
		t.Fatalf("invalid scope must be refused: %v", err)
	}
}

func TestServerToolsExcludeWriteForReadOnly(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	// 未注册连接：全量清单。
	all := server.Tools("")
	if len(all["tools"].([]map[string]any)) != 8 {
		t.Fatalf("expected 8 tools, got %d", len(all["tools"].([]map[string]any)))
	}
	if _, has := all["omittedWriteTools"]; has {
		t.Fatal("no omission note without a read-only connection")
	}
	// 只读连接（connect 只登记配置、惰性建连，不需要真实服务器）。
	roParams, err := lifecycle.Parse([]byte(`{
		"connection": {"id": "ro-conn", "name": "ro", "host": "127.0.0.1", "port": 389,
			"external_config": {"read_only": true}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := server.svc.Connect(roParams); err != nil {
		t.Fatal(err)
	}
	ro := server.Tools("ro-conn")
	tools := ro["tools"].([]map[string]any)
	for _, tool := range tools {
		if tool["name"] == writeToolName {
			t.Fatal("write tool must be excluded for a read-only connection")
		}
	}
	if len(tools) != 7 {
		t.Fatalf("expected 7 tools for read-only, got %d", len(tools))
	}
	omitted := ro["omittedWriteTools"].([]map[string]any)
	if len(omitted) != 1 || omitted[0]["name"] != writeToolName {
		t.Fatalf("omission note mismatch: %+v", omitted)
	}
	// 非只读连接：写工具在清单内。
	rwParams, err := lifecycle.Parse([]byte(`{
		"connection": {"id": "rw-conn", "name": "rw", "host": "127.0.0.1", "port": 389}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := server.svc.Connect(rwParams); err != nil {
		t.Fatal(err)
	}
	rw := server.Tools("rw-conn")
	for _, tool := range rw["tools"].([]map[string]any) {
		if tool["name"] == writeToolName {
			return
		}
	}
	t.Fatal("write tool must be listed for a writable connection")
}

// connectLazy 注册一个惰性连接配置（不拨号；svc.WithConn 才建连）。
func connectLazy(t *testing.T, server *Server, id string) {
	t.Helper()
	params, err := lifecycle.Parse([]byte(`{"connection": {"id": "` + id + `", "name": "` + id + `", "host": "127.0.0.1", "port": 1}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := server.svc.Connect(params); err != nil {
		t.Fatal(err)
	}
}

func TestServerSearchDigestScopeValidation(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	connectLazy(t, server, "digest-conn")
	// 非法 scope：参数校验先行，报错列出合法值。
	_, err := server.Call("ldap_search_digest", map[string]any{
		"connectionId": "digest-conn", "filter": "(objectClass=*)", "scope": "children",
	})
	if err == nil || !strings.Contains(err.Error(), "scope must be base, one, or sub") {
		t.Fatalf("invalid scope must be refused: %v", err)
	}
	// 大写合法变体通过校验（到达拨号层报网络错误，而不是 scope 错误）。
	_, err = server.Call("ldap_search_digest", map[string]any{
		"connectionId": "digest-conn", "filter": "(objectClass=*)", "scope": "SUB",
	})
	if err == nil || strings.Contains(err.Error(), "scope must be") {
		t.Fatalf("uppercase sub must pass validation (dial error expected): %v", err)
	}
}

func TestServerTwoPhaseTokenBindsConnection(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	server.settings.ReportWaitMs = 10
	connectLazy(t, server, "del-a")
	connectLazy(t, server, "del-b")
	dn := "ou=x,dc=example,dc=org"

	// 连接 A 上开预览签发 token。
	preview, err := server.Call("ldap_entry_write", map[string]any{
		"connectionId": "del-a", "action": "delete", "dn": dn,
	})
	if err != nil {
		t.Fatal(err)
	}
	token, _ := preview["confirmToken"].(string)
	if token == "" {
		t.Fatalf("preview must issue confirmToken: %#v", preview)
	}
	if preview["note"] == nil {
		t.Fatalf("preview must carry the execute hint: %#v", preview)
	}

	// 换连接 B 携同 token：hash 绑定 connectionId → 作废（防跨连接误删）。
	_, err = server.Call("ldap_entry_write", map[string]any{
		"connectionId": "del-b", "action": "delete", "dn": dn, "confirmToken": token,
	})
	if err == nil || !strings.Contains(err.Error(), "arguments changed") {
		t.Fatalf("token must not survive a connection switch: %v", err)
	}

	// 同连接 A 重开预览拿新 token → hash 通过，到达执行层（拨号失败，
	// 但错误必须是网络/执行错误而非 token 错误）。
	preview2, err := server.Call("ldap_entry_write", map[string]any{
		"connectionId": "del-a", "action": "delete", "dn": dn,
	})
	if err != nil {
		t.Fatal(err)
	}
	token2 := preview2["confirmToken"].(string)
	_, err = server.Call("ldap_entry_write", map[string]any{
		"connectionId": "del-a", "action": "delete", "dn": dn, "confirmToken": token2,
	})
	if err == nil || strings.Contains(err.Error(), "confirmToken") {
		t.Fatalf("valid token must reach the execute layer, got: %v", err)
	}
	// token 一次性：复用已消费 token → 明确错误。
	_, err = server.Call("ldap_entry_write", map[string]any{
		"connectionId": "del-a", "action": "delete", "dn": dn, "confirmToken": token2,
	})
	if err == nil || !strings.Contains(err.Error(), "unknown or already used") {
		t.Fatalf("consumed token must be refused: %v", err)
	}
}

func TestServerSettingsSetStringNumber(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	// 字符串数字被宽容接受（LLM 常见变体）；真非数字仍拒绝。
	updated, err := server.SettingsSet(map[string]any{"reportWaitMs": "250"})
	if err != nil {
		t.Fatal(err)
	}
	if updated["settings"].(Settings).ReportWaitMs != 250 {
		t.Fatalf("string number not applied: %+v", updated)
	}
	if _, err := server.SettingsSet(map[string]any{"reportWaitMs": "fast"}); err == nil {
		t.Fatal("non-numeric string must still be refused")
	}
}

// S-SRV-WIRE settings → cursor/confirm 会话参数构造接线：NewServer 按加载
// 的 settings 初始化，SettingsSet 后下一次会话按新参数执行。
func TestServerSessionParamsWireSettings(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ldapconn.NewService(), st)
	if server.cursors.ttl != 10*time.Minute || server.cursors.capacity != 8 || server.cursors.maxRows != 10000 {
		t.Fatalf("cursor store defaults mismatch: %+v", server.cursors)
	}
	if server.confirms.TTL() != ConfirmTTL {
		t.Fatalf("confirm store default TTL mismatch: %v", server.confirms.TTL())
	}
	if _, err := server.SettingsSet(map[string]any{
		"cursorTtlSecs": "120", "maxCursorSessions": float64(4), "maxCursorRows": float64(5000), "confirmTtlSecs": float64(30),
	}); err != nil {
		t.Fatal(err)
	}
	if server.cursors.ttl != 120*time.Second || server.cursors.capacity != 4 || server.cursors.maxRows != 5000 {
		t.Fatalf("cursor store tuning not applied: %+v", server.cursors)
	}
	if server.confirms.TTL() != 30*time.Second {
		t.Fatalf("confirm TTL tuning not applied: %v", server.confirms.TTL())
	}
	// cursorNext 错误消息携带实际生效 TTL/容量（settings 可调后的动态值）。
	_, err = server.Call("ldap_cursor_next", map[string]any{"cursorId": "cur-nope"})
	if err == nil || !strings.Contains(err.Error(), "TTL 120s") || !strings.Contains(err.Error(), "at most 4") {
		t.Fatalf("unknown cursor error must carry effective values: %v", err)
	}
}
