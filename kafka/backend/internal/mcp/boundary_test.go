package mcp

// boundary_test.go：MCP 第二轮（2026-09-13）边界覆盖——digest schema 挂载
// 参数解析与解码/投影可见性、cursor 会话容量（Server 层注入时钟）、produce
// 边界矩阵（headers 形状 / schema 形状 / 越界 partition 报文质量）。
// 真集群深水区断言在 scripts/smoke_mcp.py 的 K14/K15（schema 投影、
// reset timestamp 落点、聚合 clamp、翻页上限）。

import (
	"strings"
	"testing"
	"time"

	"io.dbx.kafka.plugin/internal/kafkaconn"
)

// --- digest schema 挂载参数（parseSchemaRef） ---

func TestParseSchemaRefVariants(t *testing.T) {
	// digest 形态：subject 可缺省（按 wire id 查 SR）。
	ref, err := parseSchemaRef(map[string]any{}, false)
	if err != nil || ref == nil || ref.Subject != "" || ref.Version != 0 {
		t.Fatalf("digest empty schema must resolve with defaults: %+v %v", ref, err)
	}
	// produce 形态：subject 必填（编码按 subject+version 取元数据）。
	if _, err := parseSchemaRef(map[string]any{}, true); err == nil || !strings.Contains(err.Error(), "schema.subject is required") {
		t.Fatalf("produce without subject must error: %v", err)
	}
	// version 字符串宽容（LLM 变体）。
	ref, err = parseSchemaRef(map[string]any{"subject": "s", "version": "3"}, true)
	if err != nil || ref.Version != 3 {
		t.Fatalf("string version must be tolerated: %+v %v", ref, err)
	}
	// version 非法/负数报错（静默折 0 会变成"取最新"）。
	if _, err := parseSchemaRef(map[string]any{"subject": "s", "version": "abc"}, true); err == nil || !strings.Contains(err.Error(), "schema.version must be a non-negative integer") {
		t.Fatalf("bad version must error: %v", err)
	}
	if _, err := parseSchemaRef(map[string]any{"subject": "s", "version": float64(-1)}, true); err == nil {
		t.Fatal("negative version must error")
	}
	// 整个 schema 非对象。
	if _, err := parseSchemaRef("orders-v1", true); err == nil || !strings.Contains(err.Error(), "schema must be an object") {
		t.Fatalf("non-object schema must error: %v", err)
	}
}

func TestServerDigestSchemaArgValidation(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	// schema 参数解析在拨号前完成：非法形状/版本在连接解析前报明确错误
	//（不静默丢弃后走 raw——AI 会以为解码生效了）。
	cases := []struct {
		schema any
		marker string
	}{
		{"orders-v1", "schema must be an object"},
		{map[string]any{"version": "v3"}, "schema.version must be a non-negative integer"},
	}
	for _, testCase := range cases {
		_, err := server.Call("kafka_messages_digest", map[string]any{
			"connectionId": "nope", "topic": "t", "schema": testCase.schema,
		})
		if err == nil || !strings.Contains(err.Error(), testCase.marker) {
			t.Fatalf("schema=%v must fail with %q, got: %v", testCase.schema, testCase.marker, err)
		}
	}
}

// --- 解码失败与投影零命中的可见性（纯函数） ---

func wireMessage(topic string, partition int32, offset int64, decodeError string) kafkaconn.ConsumedMessage {
	message := kafkaconn.ConsumedMessage{
		Topic: topic, Partition: partition, Offset: offset,
		Timestamp: intentBase.UnixMilli(), Key: "k",
		ValueText: "\x00\x00\x00\x00\x01{\"raw\":\"wire\"}",
	}
	message.DecodeError = decodeError
	return message
}

// 长错误（SR 真实形态：长 subject 路径 + HTTP 状态码在 120 字符之外）。
const longDecodeError = "schema: schema registry GET /subjects/com.example.telemetry.events-value/versions/999 failed (HTTP 404): {\"error_code\":40402,\"message\":\"Schema not found\"}"

func TestDigestNotesDecodeFailures(t *testing.T) {
	messages := []kafkaconn.ConsumedMessage{
		wireMessage("orders", 0, 0, longDecodeError),
		wireMessage("orders", 0, 1, ""),
	}
	aggregated := AggregateDigest(DigestInput{Messages: messages, Topic: "orders", Fields: []string{"$.v"}, GroupLimit: 20, TopN: 10, SampleRows: 5})
	failures, note := digestNotes(messages, aggregated)
	if failures != 1 || !strings.Contains(note, "1 of 2 matched messages failed decode") {
		t.Fatalf("decodeNotes mismatch: %d %q", failures, note)
	}
	// 样本行带 per-message decodeError：诊断信息用独立上限（200）——SR 错误
	// 的 subject 路径 + HTTP 状态码在默认 120 单元格宽度下会被截掉（本错误
	// 全长超 120，"HTTP 404" 位于 120 之后，恰好覆盖该场景）。
	sample := projectMessage(messages[0], nil, 120)
	decoded, ok := sample["decodeError"].(string)
	if !ok || !strings.Contains(decoded, "HTTP 404") || !strings.Contains(decoded, "/subjects/com.example.telemetry.events-value/") {
		t.Fatalf("sample decodeError mismatch: %q", decoded)
	}
	healthy := AggregateDigest(DigestInput{Messages: messages[1:], Topic: "orders"})
	if failures, note := digestNotes(messages[1:], healthy); failures != 0 || note != "" {
		t.Fatalf("healthy digest must not carry decode notes: %d %q", failures, note)
	}
}

func TestDigestFieldsNoteOnZeroProjection(t *testing.T) {
	messages := []kafkaconn.ConsumedMessage{wireMessage("orders", 0, 0, ""), wireMessage("orders", 0, 1, "")}
	aggregated := AggregateDigest(DigestInput{Messages: messages, Topic: "orders", Fields: []string{"$.user.id"}, GroupLimit: 20, TopN: 10})
	if note := fieldsNoteOf(aggregated, []string{"$.user.id"}); !strings.Contains(note, "matched 0 values") || !strings.Contains(note, "non-JSON") {
		t.Fatalf("zero-projection note mismatch: %q", note)
	}
	// 命中任一字段即无提示。
	jsonMessages := []kafkaconn.ConsumedMessage{{Topic: "orders", Partition: 0, Offset: 0, ValueText: `{"user":{"id":"u1"}}`}}
	hit := AggregateDigest(DigestInput{Messages: jsonMessages, Topic: "orders", Fields: []string{"$.user.id"}, GroupLimit: 20, TopN: 10})
	if note := fieldsNoteOf(hit, []string{"$.user.id"}); note != "" {
		t.Fatalf("hit projection must not carry a note: %q", note)
	}
	// matched=0 / 未请求 fields 均无提示。
	if note := fieldsNoteOf(aggregated, nil); note != "" {
		t.Fatalf("no fields requested must not carry a note: %q", note)
	}
}

// --- cursor 会话容量（Server 层，注入时钟） ---

func TestServerCursorTTLExpiryMessage(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	now := intentBase
	server.now = func() time.Time { return now }
	digest, err := server.Call("kafka_messages_digest", map[string]any{})
	if err == nil {
		_ = digest // 未连集群 digest 会失败；直接物化会话绕开集群依赖。
	}
	session := server.cursors.Put([]CursorRow{{Topic: "orders", Partition: 0, Offset: 0}}, "orders", now)
	// TTL 内可读；过期后报文带 TTL 与重建指引（注入时钟，不真等 10 分钟）。
	now = now.Add(9 * time.Minute)
	if _, status := server.cursors.Next(session.ID, NextRequest{Offset: -1}, now); status != LookupFound {
		t.Fatal("within TTL the session must be readable")
	}
	now = now.Add(2 * time.Minute)
	_, err = server.Call("kafka_cursor_next", map[string]any{"cursorId": session.ID})
	if err == nil || !strings.Contains(err.Error(), "cursor expired (TTL 600s)") || !strings.Contains(err.Error(), "re-run kafka_messages_digest") {
		t.Fatalf("expired cursor message mismatch: %v", err)
	}
	// TTL 可经 mcp/settings/set 调整，过期报文携带实际生效值（同族 files 语义）。
	if _, err := server.SettingsSet(map[string]any{"cursorTtlSecs": float64(30)}); err != nil {
		t.Fatal(err)
	}
	session2 := server.cursors.Put([]CursorRow{{Topic: "orders", Partition: 0, Offset: 1}}, "orders", now)
	now = now.Add(31 * time.Second)
	_, err = server.Call("kafka_cursor_next", map[string]any{"cursorId": session2.ID})
	if err == nil || !strings.Contains(err.Error(), "cursor expired (TTL 30s)") {
		t.Fatalf("adjusted TTL must surface in the expiry message: %v", err)
	}
	// 过期会话被顺手清除：再读变 unknown。
	if _, status := server.cursors.Next(session.ID, NextRequest{Offset: -1}, now); status != LookupUnknown {
		t.Fatal("expired session must be pruned on read")
	}
}

func TestServerCursorLRUCapacityEight(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	now := intentBase
	server.now = func() time.Time { return now }
	ids := make([]string, 0, 9)
	for index := 0; index < 9; index++ {
		session := server.cursors.Put([]CursorRow{{Topic: "t", Partition: 0, Offset: int64(index)}}, "t", now)
		ids = append(ids, session.ID)
	}
	// 第 9 次物化淘汰最旧会话（LRU ≤8，设计 §3）。
	if _, err := server.Call("kafka_cursor_next", map[string]any{"cursorId": ids[0]}); err == nil || !strings.Contains(err.Error(), "unknown cursorId") {
		t.Fatalf("oldest session must be evicted: %v", err)
	}
	for _, id := range ids[1:] {
		if _, err := server.Call("kafka_cursor_next", map[string]any{"cursorId": id}); err != nil {
			t.Fatalf("surviving session %s must page: %v", id[:10], err)
		}
	}
}

// --- produce 边界矩阵 ---

func TestServerProduceBoundaryMatrix(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	// 每个用例的参数错误都在连接解析（ensureWritable 对未知连接报
	// read-only 之前不可行——所以这里注册一个真实策略连接但指向不可达
	// 端口，让形状错误先于拨号被拦）。
	if err := connect2At(server.svc, "pb-conn", false, true, "127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"connectionId": "pb-conn", "topic": "t"}

	// headers 非法形状（数组 / 标量 / 嵌套值）：显式报错而非静默丢 header。
	for name, headers := range map[string]any{
		"array":   []any{"a", "b"},
		"scalar":  "trace-id",
		"nested":  map[string]any{"h": []any{1, 2}},
		"numeric": map[string]any{"retry": []any{"3"}},
	} {
		args := cloneArgs(base)
		args["headers"] = headers
		args["value"] = "v"
		_, err := server.messagesProduce(args)
		if err == nil {
			t.Fatalf("headers=%v (%s) must be refused", headers, name)
		}
		if !strings.Contains(err.Error(), "headers") {
			t.Fatalf("headers error must name the field: %v", err)
		}
	}
	// headers 值为标量（数字/布尔/null）折字符串，合法。
	args := cloneArgs(base)
	args["headers"] = map[string]any{"retry": float64(3), "trace": "abc", "sampled": true, "empty": nil}
	if _, err := server.messagesProduce(args); err == nil || strings.Contains(err.Error(), "headers[") {
		t.Fatalf("scalar header values must be accepted (dial error expected): %v", err)
	}
	// schema 形状：非对象显式报错。
	args = cloneArgs(base)
	args["schema"] = "orders-v1"
	if _, err := server.messagesProduce(args); err == nil || !strings.Contains(err.Error(), "schema must be an object") {
		t.Fatalf("non-object schema must error: %v", err)
	}
	// key/value 全空（含 base64 均空）：kafkaconn 层要求 value 至少一项，
	// 错误明确指出需要哪个字段（Kafka tombstone 语义属工作台能力，MCP 面
	// 保持"发一条小消息"的单一语义）。
	args = cloneArgs(base)
	if _, err := server.messagesProduce(args); err == nil || !strings.Contains(err.Error(), "value (or valueBase64) is required") {
		t.Fatalf("empty key/value must state the missing field: %v", err)
	}
	// partition 越界（broker 侧拒绝）：错误必须落到拨号/集群层，而不是
	// 参数校验层静默放过或 sidecar 崩溃；此处连接不可达，预期连接错误。
	//（JSON-RPC 数字是 float64，与真实 MCP 路径同形状。）
	args = cloneArgs(base)
	args["partition"] = float64(999)
	if _, err := server.messagesProduce(args); err == nil || strings.Contains(err.Error(), "partition must be") {
		t.Fatalf("out-of-range partition reaches the broker (dial error expected): %v", err)
	}
	// partition 非整数仍参数层报错（带原值）。
	args = cloneArgs(base)
	args["partition"] = "main"
	if _, err := server.messagesProduce(args); err == nil || !strings.Contains(err.Error(), `got main`) {
		t.Fatalf("non-integer partition must error with the raw value: %v", err)
	}
}

func cloneArgs(base map[string]any) map[string]any {
	out := make(map[string]any, len(base)+2)
	for key, value := range base {
		out[key] = value
	}
	return out
}

// --- 同族一致性（ssh 基线对齐） ---

func TestUnknownToolMessageSuggestions(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	// 分隔符/大小写变体建议注册名（ssh unknown_tool_message 同构）。
	_, err := server.Call("kafka-messages-digest", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "Did you mean 'kafka_messages_digest'?") {
		t.Fatalf("variant tool name must suggest: %v", err)
	}
	_, err = server.Call("KAFKA_CURSOR_NEXT", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "Did you mean 'kafka_cursor_next'?") {
		t.Fatalf("case variant must suggest: %v", err)
	}
	// 完全 miss：仍指向工具发现面，附工具数。
	_, err = server.Call("banana", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "unknown tool: banana") ||
		!strings.Contains(err.Error(), "mcp/tools") || !strings.Contains(err.Error(), "11 available tools") {
		t.Fatalf("unknown tool message mismatch: %v", err)
	}
}

func TestCoerceBoolWideVariants(t *testing.T) {
	for raw, want := range map[string]bool{
		"true": true, "1": true, "yes": true, "on": true, "ON": true, "Yes": true,
		"false": false, "0": false, "no": false, "off": false, "Off": false, "NO": false,
	} {
		got, ok := coerceBool(raw)
		if !ok || got != want {
			t.Fatalf("coerceBool(%q) = %v,%v; want %v", raw, got, ok, want)
		}
	}
	if _, ok := coerceBool("maybe"); ok {
		t.Fatal("invalid boolean text must not parse")
	}
}
