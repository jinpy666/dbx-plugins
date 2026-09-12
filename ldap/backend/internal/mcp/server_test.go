package mcp

// server_test.go：Server 层纯逻辑（settings 通道 + 响应上限；验收用例
// S-SRV-*，清单见 shared/frontend/README.zh-CN.md）。

import (
	"strings"
	"testing"

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

func TestServerCursorNextErrors(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	if _, err := server.Call("ldap_cursor_next", map[string]any{}); err == nil {
		t.Fatal("missing cursorId must error")
	}
	if _, err := server.Call("ldap_cursor_next", map[string]any{"cursorId": "cur-nope"}); err == nil || !strings.Contains(err.Error(), "unknown cursorId") {
		t.Fatalf("unknown cursor must error: %v", err)
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
