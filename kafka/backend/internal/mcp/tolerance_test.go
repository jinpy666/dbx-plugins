package mcp

// tolerance_test.go：MCP 工具面对 LLM 常见输入变体的容错语义（易用性/
// 准确性/容错性专项）。核心断言：
//   - 整数字符串 / 字符串布尔宽容接受（不静默改变语义）；
//   - 存在但非法的参数显式报错（绝不静默折算为 0/丢弃后执行）；
//   - offsets reset 各模式的配套参数在预览前体检（不白烧一次性令牌）。

import (
	"errors"
	"strings"
	"testing"
	"time"

	"io.dbx.kafka.plugin/internal/kafkaconn"
)

func TestCoerceIntVariants(t *testing.T) {
	cases := []struct {
		raw   any
		want  int
		valid bool
	}{
		{float64(3), 3, true},
		{"3", 3, true},
		{" 42 ", 42, true},
		{"-1", -1, true},
		{"0", 0, true},
		{"abc", 0, false},
		{"", 0, false},
		{nil, 0, false},
		{true, 0, false},
	}
	for _, c := range cases {
		got, ok := coerceInt(c.raw)
		if ok != c.valid || (ok && got != c.want) {
			t.Fatalf("coerceInt(%v) = (%d,%v), want (%d,%v)", c.raw, got, ok, c.want, c.valid)
		}
	}
	if _, ok := coerceInt64("1700000000000"); !ok {
		t.Fatal("coerceInt64 must accept numeric strings")
	}
	if ok, valid := coerceBool("TRUE"); !valid || !ok {
		t.Fatal("coerceBool must accept case-insensitive strings")
	}
	// 第二轮对齐 ssh 基线：yes/no/on/off 也接受；其余仍拒绝。
	if ok, valid := coerceBool("yes"); !valid || !ok {
		t.Fatal("coerceBool must accept the yes/no/on/off variants")
	}
	if _, valid := coerceBool("maybe"); valid {
		t.Fatal("coerceBool must refuse unrecognized strings")
	}
}

// 整数参数变体走 locatorOf：partition/offset 字符串数字可定位（与前端
// Number.parseInt 语义一致）；非法值给出带原值的错误。
func TestServerLocatorStringNumberVariants(t *testing.T) {
	if _, _, err := locatorOf(map[string]any{"partition": "0", "offset": "42"}); err != nil {
		t.Fatalf("numeric strings must locate: %v", err)
	}
	if _, _, err := locatorOf(map[string]any{"partition": "abc", "offset": "1"}); err == nil || !strings.Contains(err.Error(), "partition") || !strings.Contains(err.Error(), "abc") {
		t.Fatalf("invalid partition must name the value: %v", err)
	}
	if _, _, err := locatorOf(map[string]any{"partition": float64(-1), "offset": "1"}); err == nil || !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("negative partition must be refused: %v", err)
	}
}

// digest 的 partitions 通道：整数字符串数组与逗号分隔串接受；非法项报错
// 而非静默丢弃；timestamp 窗口存在但非法报错。
func TestServerDigestArgVariants(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	if _, err := server.messagesDigest(map[string]any{"connectionId": "nope", "topic": "t", "partitions": []any{"0", float64(1)}}); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("mixed numeric partitions should pass parsing (fail at connection): %v", err)
	}
	if _, err := server.messagesDigest(map[string]any{"connectionId": "nope", "topic": "t", "partitions": "0, 2"}); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("comma-separated string partitions should pass parsing: %v", err)
	}
	if _, err := server.messagesDigest(map[string]any{"connectionId": "nope", "topic": "t", "partitions": []any{"p0"}}); err == nil || !strings.Contains(err.Error(), "partitions[0]") {
		t.Fatalf("invalid partition item must error with index: %v", err)
	}
	if _, err := server.messagesDigest(map[string]any{"connectionId": "nope", "topic": "t", "timestampFrom": "abc"}); err == nil || !strings.Contains(err.Error(), "timestampFrom") {
		t.Fatalf("invalid time bound must error, not be silently dropped: %v", err)
	}
	if _, err := server.messagesDigest(map[string]any{"connectionId": "nope", "topic": "t", "format": "Bogus"}); err == nil || !strings.Contains(err.Error(), `got "Bogus"`) {
		t.Fatalf("format error must echo the received value: %v", err)
	}
	// format 大小写归一（ROWS → rows）在解析层接受。
	server.settings.ReportWaitMs = 1
}

// cursor_next 的 n 通道：整数字符串接受并生效；非法值显式报错。
func TestServerCursorNextStringN(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	rows := make([]CursorRow, 5)
	for index := range rows {
		rows[index] = CursorRow{Topic: "t", Partition: 0, Offset: int64(index)}
	}
	session := server.cursors.Put(rows, "t", time.Now())
	result, err := server.cursorNext(map[string]any{"cursorId": session.ID, "n": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result["rows"].([]map[string]any)) != 2 {
		t.Fatalf("string n must size the batch: %+v", result)
	}
	if _, err := server.cursorNext(map[string]any{"cursorId": session.ID, "n": "many"}); err == nil || !strings.Contains(err.Error(), "positive integer") {
		t.Fatalf("invalid n must error: %v", err)
	}
}

// uiFocus 面板枚举：大小写归一后放行；未知面板在 sidecar 侧报明确错误
// （不等前端 rejected / stdio pending）。
func TestServerUIFocusPanelValidation(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	server.settings.ReportWaitMs = 1
	if _, err := server.uiFocus(map[string]any{"panel": "TOPICS"}); err != nil {
		t.Fatalf("case-insensitive panel must pass: %v", err)
	}
	if _, err := server.uiFocus(map[string]any{"panel": "banana"}); err == nil || !strings.Contains(err.Error(), "messages | topics | groups | schemas") || !strings.Contains(err.Error(), "banana") {
		t.Fatalf("unknown panel must error listing panels: %v", err)
	}
}

// offsets reset 预检：各模式配套参数缺失在预览前报错（不签发令牌）；
// timestampMs 整数字符串宽容折算进 canonical 请求。
func TestServerOffsetsResetPrevalidation(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	if err := connect2(server.svc, "rw-reset", false, true); err != nil {
		t.Fatal(err)
	}

	_, err := server.groupsOffsetsReset(map[string]any{"connectionId": "rw-reset", "group": "g", "resetTo": "timestamp", "topics": []any{"t"}})
	if err == nil || !strings.Contains(err.Error(), "timestampMs") {
		t.Fatalf("timestamp without timestampMs must be refused before preview: %v", err)
	}
	_, err = server.groupsOffsetsReset(map[string]any{"connectionId": "rw-reset", "group": "g", "resetTo": "latest"})
	if err == nil || !strings.Contains(err.Error(), "topics is required") {
		t.Fatalf("latest without topics must be refused before preview: %v", err)
	}
	_, err = server.groupsOffsetsReset(map[string]any{"connectionId": "rw-reset", "group": "g", "resetTo": "partitionOffset"})
	if err == nil || !strings.Contains(err.Error(), "partitionOffsets is required") {
		t.Fatalf("partitionOffset without map must be refused before preview: %v", err)
	}
	_, err = server.groupsOffsetsReset(map[string]any{"connectionId": "rw-reset", "group": "g", "resetTo": "banana"})
	if err == nil || !strings.Contains(err.Error(), "resetTo must be") {
		t.Fatalf("unknown resetTo must error listing options: %v", err)
	}
	// 上面全部在预览前拒绝：不应有任何令牌被签发。
	if len(server.confirms.items) != 0 {
		t.Fatalf("prevalidation must not issue tokens: %d", len(server.confirms.items))
	}

	// timestampMs 整数字符串宽容折算：preview 的 canonical 请求携带真实值。
	preview, err := server.groupsOffsetsReset(map[string]any{
		"connectionId": "rw-reset", "group": "g", "resetTo": "TIMESTAMP",
		"topics": []any{"t"}, "timestampMs": "1700000000000",
	})
	if err != nil {
		t.Fatalf("string timestampMs must pass: %v", err)
	}
	canonical := preview["preview"].(offsetsResetArgs)
	if canonical.TimestampMs != 1700000000000 || canonical.ResetTo != "TIMESTAMP" {
		t.Fatalf("canonical request must carry coerced timestampMs: %+v", canonical)
	}
}

// produce 的 partition 通道：字符串数字接受；非法值在执行前报错。
// 借助"超预算值在参数解析之后、执行（拨号）之前被拦"的顺序，封闭验证
// 合法字符串 partition 通过解析层（不触网、不拖 30s 拨号超时）。
func TestServerProducePartitionVariants(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	if err := connect2(server.svc, "pw", false, true); err != nil {
		t.Fatal(err)
	}
	oversized := strings.Repeat("x", 64*1024+1)
	_, err := server.messagesProduce(map[string]any{"connectionId": "pw", "topic": "t", "value": oversized, "partition": "abc"})
	if err == nil || !strings.Contains(err.Error(), "partition") || !strings.Contains(err.Error(), "abc") {
		t.Fatalf("invalid string partition must error before the budget check: %v", err)
	}
	// 合法字符串 partition 通过解析：错误发生在预算检查（而非 partition 报错）。
	_, err = server.messagesProduce(map[string]any{"connectionId": "pw", "topic": "t", "value": oversized, "partition": "3"})
	if err == nil || !strings.Contains(err.Error(), "produce budget") {
		t.Fatalf("valid string partition should pass parsing (fail at budget): %v", err)
	}
}

// 读错误指引：连接未注册 / topic 疑似不存在的错误附可行动 hint。
func TestAnnotateReadError(t *testing.T) {
	notConnected := annotateClusterError(errors.New(`connection "c" is not connected; call connection/connect first`))
	if !strings.Contains(notConnected.Error(), "verify the connectionId") {
		t.Fatalf("not-connected hint missing: %v", notConnected)
	}
	noTopic := annotateClusterError(errors.New("UNKNOWN_TOPIC_OR_PARTITION: topic x not found"))
	if !strings.Contains(noTopic.Error(), "kafka_ui_topics") {
		t.Fatalf("topic hint missing: %v", noTopic)
	}
	if !strings.Contains(noTopic.Error(), "stdio has no topic listing") {
		t.Fatalf("topic hint must note the stdio gap: %v", noTopic)
	}
	plain := annotateClusterError(errors.New("some unrelated failure"))
	if plain.Error() != "some unrelated failure" {
		t.Fatalf("unrelated errors pass through untouched: %v", plain)
	}
}

// produce 载荷超限报错带实际字节数（易用性：AI 能自行折算削减量）。
func TestServerProduceBudgetMessage(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	if err := connect2(server.svc, "pb", false, true); err != nil {
		t.Fatal(err)
	}
	_, err := server.messagesProduce(map[string]any{"connectionId": "pb", "topic": "t", "value": strings.Repeat("x", 64*1024+1)})
	if err == nil || !strings.Contains(err.Error(), "65537 > 65536") {
		t.Fatalf("budget error must include actual vs limit: %v", err)
	}
}
