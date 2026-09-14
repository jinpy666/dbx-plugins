package mcp

// writegate_test.go：kafka 写门对抗输入矩阵（可靠性纵深轮，S-WGATE-*）：
// 只读/allow_delete 门矩阵、两阶段令牌（过期/参数被改/一次性）、reset
// timestampMs 越界（0/负数/非整数/远未来）预检不白烧令牌、produce 头部
// 预算（空键/超长键/大量 header）。全部离线：连接惰性注册、执行类断言
// 停在拨号层（127.0.0.1:1 确定不可达）。

import (
	"fmt"
	"strings"
	"testing"

	"io.dbx.kafka.plugin/internal/kafkaconn"
)

// S-WGATE-1 写门策略矩阵：只读连接 produce/clear/reset 全拒（read-only）；
// allow_delete=false 时 delete/clear 拒（allow_delete）而 reset（写非删）
// 放行到执行层；未注册连接按只读兜底。
func TestServerWriteGateAdversarialMatrix(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	if err := connect2At(server.svc, "wg-ro", true, true, "127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	if err := connect2At(server.svc, "wg-nodelete", false, false, "127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	// 只读：全部写族拒绝（含此前未单测的 reset）。
	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"kafka_messages_produce", map[string]any{"topic": "t", "value": "v"}},
		{"kafka_topics_records_clear", map[string]any{"topic": "t"}},
		{"kafka_groups_offsets_reset", map[string]any{"group": "g", "resetTo": "latest", "topics": []any{"t"}}},
		{"kafka_topics_delete", map[string]any{"topics": []any{"t"}}},
	} {
		call.args["connectionId"] = "wg-ro"
		_, err := server.Call(call.tool, call.args)
		if err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Fatalf("%s on read-only must be refused: %v", call.tool, err)
		}
	}
	// allow_delete=false：delete/clear 拒绝点名 allow_delete；reset（写非删）
	// 通过门禁进入两阶段（preview 可得、确认到达执行层）。
	_, err := server.Call("kafka_topics_delete", map[string]any{"connectionId": "wg-nodelete", "topics": []any{"t"}})
	if err == nil || !strings.Contains(err.Error(), "allow_delete") {
		t.Fatalf("delete without allow_delete must be refused: %v", err)
	}
	// 拒绝消息必须带可行动出路（stdio 内联默认 readOnly=true 的解除方式，
	// 真机 agent 实测卡点）：只读门点名 "readOnly": false，删除门叠加
	// "allowDelete": true。
	_, roErr := server.Call("kafka_messages_produce", map[string]any{"connectionId": "wg-ro", "topic": "t", "value": "v"})
	if roErr == nil || !strings.Contains(roErr.Error(), `"readOnly": false`) {
		t.Fatalf("read-only refusal must name the writable way out: %v", roErr)
	}
	_, adErr := server.Call("kafka_topics_delete", map[string]any{"connectionId": "wg-nodelete", "topics": []any{"t"}})
	if adErr == nil || !strings.Contains(adErr.Error(), `"allowDelete": true`) {
		t.Fatalf("allow_delete refusal must name the allowDelete way out: %v", adErr)
	}
	_, err = server.Call("kafka_topics_records_clear", map[string]any{"connectionId": "wg-nodelete", "topic": "t"})
	if err == nil || !strings.Contains(err.Error(), "allow_delete") {
		t.Fatalf("clear without allow_delete must be refused: %v", err)
	}
	resetPreview, err := server.Call("kafka_groups_offsets_reset", map[string]any{
		"connectionId": "wg-nodelete", "group": "g", "resetTo": "latest", "topics": []any{"t"}})
	if err != nil || resetPreview["confirmToken"] == "" {
		t.Fatalf("reset is write-not-delete, gate must pass into preview: %v %v", resetPreview, err)
	}
	_, err = server.Call("kafka_groups_offsets_reset", map[string]any{
		"connectionId": "wg-nodelete", "group": "g", "resetTo": "latest", "topics": []any{"t"},
		"confirmToken": resetPreview["confirmToken"]})
	if err == nil || strings.Contains(err.Error(), "read-only") || strings.Contains(err.Error(), "allow_delete") {
		t.Fatalf("reset confirm must reach the execution layer: %v", err)
	}
	// 未注册连接按只读兜底拒绝，且不签发令牌。
	if _, err := server.Call("kafka_groups_offsets_reset", map[string]any{
		"connectionId": "nope", "group": "g", "resetTo": "latest", "topics": []any{"t"}}); err == nil ||
		!strings.Contains(err.Error(), "read-only") {
		t.Fatalf("unconnected profile must fall back to read-only: %v", err)
	}
	if len(server.confirms.items) != 0 {
		t.Fatalf("refused writes must not issue tokens: %d", len(server.confirms.items))
	}
}

// S-WGATE-2 reset timestampMs 越界：0/负数在预检拒绝（不签发令牌——0 等价
// 重置到纪元几乎必是参数缺失）；非整数点名参数带原值；远未来值合法进入
// 两阶段（preview 签发，确认后到达执行层）。
func TestServerResetTimestampMsBounds(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	if err := connect2At(server.svc, "wg-ts", false, true, "127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"connectionId": "wg-ts", "group": "g", "resetTo": "timestamp", "topics": []any{"t"}}
	for name, raw := range map[string]any{"zero": float64(0), "negative": float64(-5), "string": "abc", "bool": true} {
		args := cloneArgs(base)
		args["timestampMs"] = raw
		_, err := server.groupsOffsetsReset(args)
		if err == nil || !strings.Contains(err.Error(), "timestampMs") {
			t.Fatalf("timestampMs=%s(%v) must be refused naming the field: %v", name, raw, err)
		}
	}
	if len(server.confirms.items) != 0 {
		t.Fatalf("refused timestampMs must not burn tokens: %d", len(server.confirms.items))
	}
	// 远未来（2100 年）合法：preview 签发；确认到达执行层（拨号错误）。
	args := cloneArgs(base)
	args["timestampMs"] = float64(4102444800000)
	preview, err := server.groupsOffsetsReset(args)
	if err != nil || preview["confirmToken"] == "" {
		t.Fatalf("far-future timestampMs must pass prevalidation: %v %v", preview, err)
	}
	args["confirmToken"] = preview["confirmToken"]
	if _, err := server.groupsOffsetsReset(args); err == nil ||
		strings.Contains(err.Error(), "confirmToken") {
		t.Fatalf("confirm must reach the execution layer (dial error expected): %v", err)
	}
	if len(server.confirms.items) != 0 {
		t.Fatalf("consumed token must be removed: %d", len(server.confirms.items))
	}
}

// S-WGATE-3 records_clear 两阶段：参数被改 → hash mismatch 作废（重开
// 预览）；正确参数确认到达执行层；token 一次性。
func TestServerClearHashMismatchAndSingleUse(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	if err := connect2At(server.svc, "wg-clear", false, true, "127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	preview, err := server.topicsRecordsClear(map[string]any{"connectionId": "wg-clear", "topic": "orders"})
	if err != nil || preview["confirmToken"] == "" {
		t.Fatalf("preview must issue a token: %v %v", preview, err)
	}
	// 参数被改：换 topic 复用 token → 作废。
	_, err = server.topicsRecordsClear(map[string]any{"connectionId": "wg-clear", "topic": "other",
		"confirmToken": preview["confirmToken"]})
	if err == nil || !strings.Contains(err.Error(), "arguments changed") {
		t.Fatalf("changed params must invalidate: %v", err)
	}
	// 重开预览后正确确认：到达执行层；token 已消费，重放 unknown。
	preview2, err := server.topicsRecordsClear(map[string]any{"connectionId": "wg-clear", "topic": "orders"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.topicsRecordsClear(map[string]any{"connectionId": "wg-clear", "topic": "orders",
		"confirmToken": preview2["confirmToken"]})
	if err == nil || strings.Contains(err.Error(), "confirmToken") {
		t.Fatalf("valid token must reach execution (dial error expected): %v", err)
	}
	_, err = server.topicsRecordsClear(map[string]any{"connectionId": "wg-clear", "topic": "orders",
		"confirmToken": preview2["confirmToken"]})
	if err == nil || !strings.Contains(err.Error(), "unknown or already used") {
		t.Fatalf("token must be single-use: %v", err)
	}
}

// S-WGATE-4 produce 头部预算（对抗输入）：空键 / 超长键（>1024 字节）/
// 大量 header（>64）显式报错带实际上限，不静默截断/丢弃；64 个以内合法
// 通过解析（到达拨号层）；produce 单阶段不签发任何令牌。
func TestServerProduceHeaderBudget(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	if err := connect2At(server.svc, "wg-hdr", false, true, "127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"connectionId": "wg-hdr", "topic": "t", "value": "v"}

	// 空键：Kafka 头部键必须非空。
	args := cloneArgs(base)
	args["headers"] = map[string]any{"": "value"}
	_, err := server.messagesProduce(args)
	if err == nil || !strings.Contains(err.Error(), "headers key cannot be empty") {
		t.Fatalf("empty header key must be refused: %v", err)
	}
	// 超长键（1025 字节）：报错带实际上限与实测长度。
	args = cloneArgs(base)
	args["headers"] = map[string]any{strings.Repeat("k", 1025): "v"}
	_, err = server.messagesProduce(args)
	if err == nil || !strings.Contains(err.Error(), "header name exceeds 1024 bytes") ||
		!strings.Contains(err.Error(), "1025") {
		t.Fatalf("oversized header key must be refused with sizes: %v", err)
	}
	// 大量 header（65 个）：报错带数量对比。
	headers := map[string]any{}
	for i := 0; i < 65; i++ {
		headers[fmt.Sprintf("h%d", i)] = float64(i)
	}
	args = cloneArgs(base)
	args["headers"] = headers
	_, err = server.messagesProduce(args)
	if err == nil || !strings.Contains(err.Error(), "too many headers (65 > 64)") {
		t.Fatalf("65 headers must be refused: %v", err)
	}
	// 64 个以内合法：通过解析——停在拨号前的载荷预算检查（合法 header +
	// 超预算 value → "produce budget" 错误；不真拨号，franz-go 元数据
	// 超时会拖 30s×N）。
	delete(headers, "h64")
	args = cloneArgs(base)
	args["headers"] = headers
	args["value"] = strings.Repeat("x", 64*1024+1)
	if _, err := server.messagesProduce(args); err == nil || !strings.Contains(err.Error(), "produce budget") {
		t.Fatalf("64 headers must pass parsing (budget error expected): %v", err)
	}
	// 空对象 headers：合法（无 header）——同样停在预算检查。
	args = cloneArgs(base)
	args["headers"] = map[string]any{}
	args["value"] = strings.Repeat("x", 64*1024+1)
	if _, err := server.messagesProduce(args); err == nil || !strings.Contains(err.Error(), "produce budget") {
		t.Fatalf("empty headers object must pass: %v", err)
	}
	// produce 不签发令牌（单阶段）。
	if len(server.confirms.items) != 0 {
		t.Fatalf("produce must never issue tokens: %d", len(server.confirms.items))
	}
}
