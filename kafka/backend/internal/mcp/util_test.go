package mcp

// util_test.go：缺参枚举（ssh 同款，MCP_ACCEPTANCE §3.9，第七轮拉齐）——
// 一次报出全部缺失 required 参数（按 schema required 顺序）；单缺只点名
// 其一；显式 null 视同缺失；present-but-类型错误（空串/非法值）不混入
// 枚举，由逐参数精确校验点名。

import (
	"strings"
	"testing"

	"io.dbx.kafka.plugin/internal/kafkaconn"
)

func TestMissingRequiredEnumeration(t *testing.T) {
	err := missingRequired(map[string]any{}, "connectionId", "group", "resetTo")
	if err == nil || err.Error() != "Missing required parameters: connectionId, group, resetTo" {
		t.Fatalf("triple missing must enumerate in schema order: %v", err)
	}
	err = missingRequired(map[string]any{"group": "g"}, "connectionId", "group", "resetTo")
	if err == nil || err.Error() != "Missing required parameters: connectionId, resetTo" {
		t.Fatalf("partial missing must enumerate the gaps only: %v", err)
	}
	err = missingRequired(map[string]any{"connectionId": "c", "group": nil, "resetTo": "latest"}, "connectionId", "group", "resetTo")
	if err == nil || err.Error() != "Missing required parameters: group" {
		t.Fatalf("explicit null must count as missing: %v", err)
	}
	if err := missingRequired(map[string]any{"connectionId": "c", "group": "g", "resetTo": "latest"}, "connectionId", "group", "resetTo"); err != nil {
		t.Fatalf("all present must pass: %v", err)
	}
}

// S-REQ-ENUM 工具入口接线：digest 双缺全点名、单缺只其一、空串走精确点名。
func TestMessagesDigestMissingParamsEnumerated(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	_, err := server.Call("kafka_messages_digest", map[string]any{})
	if err == nil || err.Error() != "Missing required parameters: connectionId, topic" {
		t.Fatalf("digest {} must enumerate both in schema order: %v", err)
	}
	_, err = server.Call("kafka_messages_digest", map[string]any{"connectionId": "c"})
	if err == nil || err.Error() != "Missing required parameters: topic" {
		t.Fatalf("digest without topic must name it only: %v", err)
	}
	_, err = server.Call("kafka_messages_digest", map[string]any{"connectionId": nil, "topic": "t"})
	if err == nil || !strings.Contains(err.Error(), "Missing required parameters: connectionId") {
		t.Fatalf("null connectionId must count as missing: %v", err)
	}
	// present-but-空串：精确点名（不进枚举）。
	_, err = server.Call("kafka_messages_digest", map[string]any{"connectionId": "c", "topic": "  "})
	if err == nil || !strings.Contains(err.Error(), "topic is required") {
		t.Fatalf("blank topic must hit the precise per-param check: %v", err)
	}
}

// S-REQ-ENUM offsets_reset 三缺全点名；topics_delete/topics 存在但空数组
// 走精确点名（数组参数的 present-but-invalid 形态）。
func TestWriteToolsMissingParamsEnumerated(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	_, err := server.Call("kafka_groups_offsets_reset", map[string]any{})
	if err == nil || err.Error() != "Missing required parameters: connectionId, group, resetTo" {
		t.Fatalf("reset {} must enumerate all three: %v", err)
	}
	_, err = server.Call("kafka_groups_offsets_reset", map[string]any{"connectionId": "c", "resetTo": "latest"})
	if err == nil || err.Error() != "Missing required parameters: group" {
		t.Fatalf("reset without group must name it only: %v", err)
	}
	_, err = server.Call("kafka_topics_delete", map[string]any{})
	if err == nil || err.Error() != "Missing required parameters: connectionId, topics" {
		t.Fatalf("delete {} must enumerate both: %v", err)
	}
	// topics 存在但为空数组：精确点名（不混入枚举）。
	_, err = server.Call("kafka_topics_delete", map[string]any{"connectionId": "c", "topics": []any{}})
	if err == nil || !strings.Contains(err.Error(), "topics is required") {
		t.Fatalf("empty topics array must hit the precise per-param check: %v", err)
	}
}
