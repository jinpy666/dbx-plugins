package mcp

// server_test.go：Server 层纯逻辑（settings 通道 + 响应上限 + intent 分派 +
// 只读/allow_delete 工具清单剔除；验收用例 S-SRV-*，清单见
// shared/frontend/README.zh-CN.md）。

import (
	"strings"
	"testing"
	"time"

	"io.dbx.kafka.plugin/internal/kafkaconn"
	"io.dbx.kafka.plugin/internal/lifecycle"
	"io.dbx.kafka.plugin/internal/store"
)

func TestServerSettingsGetSetRoundtrip(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(kafkaconn.NewService(), st)

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
	reloaded := NewServer(kafkaconn.NewService(), st).SettingsGet()["settings"].(Settings)
	if reloaded.ReportWaitMs != 200 || reloaded.CellWidth != 60 {
		t.Fatalf("persistence mismatch: %+v", reloaded)
	}
	if _, err := server.SettingsSet(nil); err == nil {
		t.Fatal("nil updates must be rejected")
	}
}

func TestServerEnforceResponseLimit(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	server.settings.ResponseLimitBytes = 512

	big := map[string]any{
		"matched": 1,
		"rows":    []map[string]any{{"topic": strings.Repeat("x", 2000)}},
		"sample":  []map[string]any{{"topic": strings.Repeat("y", 2000)}},
		"stats":   map[string]any{"keys": map[string]int{"a": 1}},
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
	droppable := map[string]any{"rows": []map[string]any{{"topic": strings.Repeat("z", 4096)}}, "stats": map[string]any{"keys": map[string]int{"a": 1}}}
	trimmedRows := server.enforceResponseLimit(droppable)
	if trimmedRows["truncated"] != true {
		t.Fatal("dropping rows must set truncated")
	}
	if _, has := trimmedRows["stats"]; !has {
		t.Fatal("stats survive when rows alone overflow")
	}

	// 不可丢弃字段本身就超限：最终占位响应。
	huge := map[string]any{"topic": strings.Repeat("z", 4096), "rows": []map[string]any{{"topic": strings.Repeat("z", 4096)}}}
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
	server := NewServer(kafkaconn.NewService(), nil)
	if _, err := server.Call("kafka_nonexistent", map[string]any{}); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("unknown tool must error: %v", err)
	}
}

func TestServerUIStateSnapshotWithoutIntent(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	if err := server.ReportUIState(map[string]any{"summary": map[string]any{"panel": "messages", "count": float64(7)}}); err != nil {
		t.Fatal(err)
	}
	result, err := server.Call("kafka_ui_state", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := result["snapshot"].(map[string]any)
	if snapshot["panel"] != "messages" || snapshot["count"] != float64(7) {
		t.Fatalf("snapshot mismatch: %+v", snapshot)
	}
}

func TestServerUIStateAppendsStreams(t *testing.T) {
	svc := kafkaconn.NewService()
	server := NewServer(svc, nil)
	// 未注册连接：connectionId 给出时 stream 段为空数组（不报错，降级可用）。
	result, err := server.Call("kafka_ui_state", map[string]any{"connectionId": "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if streams, ok := result["streams"].([]kafkaconn.StreamStatus); !ok || len(streams) != 0 {
		t.Fatalf("streams segment mismatch: %+v", result["streams"])
	}
}

func TestServerCursorNextErrors(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	if _, err := server.Call("kafka_cursor_next", map[string]any{}); err == nil {
		t.Fatal("missing cursorId must error")
	}
	if _, err := server.Call("kafka_cursor_next", map[string]any{"cursorId": "cur-nope"}); err == nil || !strings.Contains(err.Error(), "unknown cursorId") {
		t.Fatalf("unknown cursor must error: %v", err)
	}
}

func TestServerLocatorValidation(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	if _, err := server.Call("kafka_ui_select", map[string]any{}); err == nil || !strings.Contains(err.Error(), "Missing required parameters: partition, offset") {
		t.Fatalf("double missing must enumerate both in schema order: %v", err)
	}
	if _, err := server.Call("kafka_ui_select", map[string]any{"partition": float64(0)}); err == nil || !strings.Contains(err.Error(), "Missing required parameters: offset") {
		t.Fatalf("missing offset must enumerate offset only: %v", err)
	}
	// present-but-非法值：精确点名，不混入枚举。
	if _, err := server.Call("kafka_ui_select", map[string]any{"partition": "abc", "offset": float64(1)}); err == nil || !strings.Contains(err.Error(), "partition must be a non-negative integer") {
		t.Fatalf("invalid partition must be named precisely: %v", err)
	}
}

func TestServerToolsExcludeWriteForRestrictedConnections(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	// 未注册连接：全量清单（11 工具）。
	all := server.Tools("")
	if len(all["tools"].([]map[string]any)) != 11 {
		t.Fatalf("expected 11 tools, got %d", len(all["tools"].([]map[string]any)))
	}
	if _, has := all["omittedWriteTools"]; has {
		t.Fatal("no omission note without a restricted connection")
	}

	// 只读连接：全部写工具剔除并附原因。
	if err := connect2(server.svc, "ro-conn2", true, true); err != nil {
		t.Fatal(err)
	}
	ro := server.Tools("ro-conn2")
	tools := ro["tools"].([]map[string]any)
	if len(tools) != 7 {
		t.Fatalf("expected 7 tools for read-only, got %d", len(tools))
	}
	for _, tool := range tools {
		for _, writeName := range writeToolNames {
			if tool["name"] == writeName {
				t.Fatalf("write tool %s must be excluded for a read-only connection", writeName)
			}
		}
	}
	omitted := ro["omittedWriteTools"].([]map[string]any)
	if len(omitted) != 4 {
		t.Fatalf("expected 4 omitted write tools, got %+v", omitted)
	}

	// 可写但 allow_delete=false：仅删除类两工具剔除。
	if err := connect2(server.svc, "nodelete-conn", false, false); err != nil {
		t.Fatal(err)
	}
	nodelete := server.Tools("nodelete-conn")
	names := map[string]bool{}
	for _, tool := range nodelete["tools"].([]map[string]any) {
		names[tool["name"].(string)] = true
	}
	if names["kafka_messages_produce"] != true || names["kafka_groups_offsets_reset"] != true {
		t.Fatal("produce/offsets_reset must stay listed when only allow_delete=false")
	}
	if names["kafka_topics_delete"] || names["kafka_topics_records_clear"] {
		t.Fatal("delete-class tools must be omitted when allow_delete=false")
	}

	// 完全可写连接：写工具在清单内。
	if err := connect2(server.svc, "rw-conn", false, true); err != nil {
		t.Fatal(err)
	}
	rw := server.Tools("rw-conn")
	if len(rw["tools"].([]map[string]any)) != 11 {
		t.Fatal("writable connection must list all tools")
	}
}

// connect2 经 lifecycle payload 注册一个连接配置（只登记配置、惰性建连，
// 不需要真实集群）。默认 bootstrap 指向 127.0.0.1:9092——注意：本机若真有
// Kafka，执行类断言会拨号成功；需要"确定不可达"的执行失败场景用 connect2At。
func connect2(svc *kafkaconn.Service, id string, readOnly, allowDelete bool) error {
	return connect2At(svc, id, readOnly, allowDelete, "127.0.0.1:9092")
}

// connect2At connect2 的显式 bootstrap 版（封闭单测用不可达端口，如
// 127.0.0.1:1，避免依赖"本机 9092 恰好无 broker"的环境假设）。
func connect2At(svc *kafkaconn.Service, id string, readOnly, allowDelete bool, bootstrap string) error {
	boolText := func(value bool) string {
		if value {
			return "true"
		}
		return "false"
	}
	params, err := lifecycle.Parse([]byte(`{
		"connection": {"id": "` + id + `", "name": "` + id + `", "host": "127.0.0.1", "port": 9092,
			"read_only": ` + boolText(readOnly) + `,
			"external_config": {"read_only": false, "allow_delete": ` + boolText(allowDelete) + `, "bootstrap_servers": "` + bootstrap + `"}}
	}`))
	if err != nil {
		return err
	}
	return svc.Connect(params)
}

func TestServerWriteGatesOnPolicy(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	// 未连接连接按只读兜底：mcp/call 侧直接拒绝（纵深防御）。
	if _, err := server.Call("kafka_messages_produce", map[string]any{"connectionId": "nope", "topic": "t", "value": "v"}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("write on unconnected profile must be refused: %v", err)
	}
	if _, err := server.Call("kafka_topics_delete", map[string]any{"connectionId": "nope", "topics": []any{"t"}}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("delete on unconnected profile must be refused: %v", err)
	}
}

func TestServerTwoPhasePreviewAndConsume(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	server.now = func() time.Time { return intentBase }
	// 封闭环境：注册指向确定不可达端口的连接，两阶段校验通过后的执行层
	// 失败（拨号失败）不应依赖"本机 9092 恰好无 broker"。
	if err := connect2At(server.svc, "c1", false, true, "127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"connectionId": "c1", "topics": []any{"orders"}}
	// 预览：不执行写，签发一次性令牌（60s TTL）。
	preview, err := server.topicsDelete(args)
	if err != nil {
		t.Fatal(err)
	}
	if preview["confirmToken"] == "" || preview["expiresAt"] == "" {
		t.Fatalf("preview shape mismatch: %+v", preview)
	}
	if _, ok := preview["preview"].(deleteArgs); !ok {
		t.Fatalf("preview payload must be the canonical request: %+v", preview["preview"])
	}
	// 参数 hash 绑定：改参即作废。
	_, err = server.topicsDelete(map[string]any{"connectionId": "c1", "topics": []any{"other"}, "confirmToken": preview["confirmToken"]})
	if err == nil || !strings.Contains(err.Error(), "arguments changed") {
		t.Fatalf("changed params must invalidate: %v", err)
	}
	// 未知令牌拒绝。
	if _, err := server.topicsDelete(map[string]any{"connectionId": "c1", "topics": []any{"orders"}, "confirmToken": "c-nope"}); err == nil || !strings.Contains(err.Error(), "unknown or already used") {
		t.Fatalf("unknown token must be refused: %v", err)
	}
	// 同参数第二阶段能走到执行层（此处连接不存在，执行报连接错误而非 hash 错）。
	preview2, err := server.topicsDelete(args)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.topicsDelete(map[string]any{"connectionId": "c1", "topics": []any{"orders"}, "confirmToken": preview2["confirmToken"]}); err == nil || strings.Contains(err.Error(), "confirmToken") {
		t.Fatalf("valid token should pass the gate (execution error expected): %v", err)
	}
	// 同参数 token 已被执行失败前的 Consume 烧掉？否——执行失败即终态：
	// token 在校验通过时已消费，重放必须 unknown。
	if got := server.confirms.Consume(preview2["confirmToken"].(string), HashParams([]byte(`{"connectionId":"c1","topics":["orders"]}`)), intentBase); got != ConfirmUnknown {
		t.Fatalf("consumed token must not be reusable: %v", got)
	}
}
