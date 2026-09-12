package mcp

// digest_test.go：`kafka_messages_digest` 本地聚合（设计 §3/§6.3；验收用例
// S-DIG-* 的 kafka 变体，清单见 shared/frontend/README.zh-CN.md）。

import (
	"strings"
	"testing"

	"io.dbx.kafka.plugin/internal/kafkaconn"
)

func message(topic string, partition int32, offset int64, key, value string, ts int64) kafkaconn.ConsumedMessage {
	return kafkaconn.ConsumedMessage{Topic: topic, Partition: partition, Offset: offset, Timestamp: ts, Key: key, ValueText: value}
}

func TestDigestPerPartitionAndKeys(t *testing.T) {
	messages := []kafkaconn.ConsumedMessage{
		message("orders", 0, 1, "k1", "{}", 1000),
		message("orders", 0, 2, "k1", "{}", 2000),
		message("orders", 1, 1, "k2", "{}", 3000),
		message("orders", 1, 2, "", "{}", 4000), // 无 key → nullKeyCount
	}
	result := AggregateDigest(DigestInput{
		Messages: messages, Topic: "orders",
		Width: 120, GroupLimit: 20, TopN: 10, SampleRows: 5,
	})
	if result.Matched != 4 {
		t.Fatalf("matched mismatch: %d", result.Matched)
	}
	if result.Stats.PerPartition["0"] != 2 || result.Stats.PerPartition["1"] != 2 {
		t.Fatalf("per-partition counts mismatch: %+v", result.Stats.PerPartition)
	}
	if result.Stats.Keys["k1"] != 2 || result.Stats.Keys["k2"] != 1 {
		t.Fatalf("key groupBy mismatch: %+v", result.Stats.Keys)
	}
	if result.Stats.NullKeyCount != 1 {
		t.Fatalf("nullKeyCount mismatch: %d", result.Stats.NullKeyCount)
	}
}

func TestDigestGroupLimitTruncation(t *testing.T) {
	messages := make([]kafkaconn.ConsumedMessage, 0, 30)
	for index := 0; index < 30; index++ {
		messages = append(messages, message("t", int32(index), int64(index), "key-"+strings.Repeat("x", index), "{}", 0))
	}
	result := AggregateDigest(DigestInput{Messages: messages, Topic: "t", GroupLimit: 3, TopN: 2, SampleRows: 1})
	if len(result.Stats.PerPartition) != 3 || !result.Stats.PerPartitionLimit {
		t.Fatalf("partition group clamp mismatch: %+v", result.Stats.PerPartition)
	}
	if len(result.Stats.Keys) != 3 || !result.Stats.KeysLimit {
		t.Fatalf("key group clamp mismatch: %+v", result.Stats.Keys)
	}
}

func TestDigestTimeHistogramBuckets(t *testing.T) {
	// 12 个整数时间点 → 桶数 = min(12, span=12) = 12，每桶 1 条。
	messages := make([]kafkaconn.ConsumedMessage, 0, 12)
	for index := int64(0); index < 12; index++ {
		messages = append(messages, message("t", 0, index, "k", "{}", 1000+index))
	}
	result := AggregateDigest(DigestInput{Messages: messages, Topic: "t", GroupLimit: 20, TopN: 10, SampleRows: 5})
	histogram := result.Stats.TimeHistogram
	if histogram == nil || len(histogram.Buckets) != 12 {
		t.Fatalf("histogram buckets mismatch: %+v", histogram)
	}
	total := 0
	for _, bucket := range histogram.Buckets {
		total += bucket.Count
	}
	if total != 12 {
		t.Fatalf("histogram must cover all timestamps: %d", total)
	}
	// 无时间戳消息不进桶（NoTimestamp 单独计数）。
	result = AggregateDigest(DigestInput{
		Messages: []kafkaconn.ConsumedMessage{message("t", 0, 0, "", "{}", 0), message("t", 0, 1, "", "{}", 1000)},
		Topic:    "t", GroupLimit: 20, TopN: 10, SampleRows: 5,
	})
	histogram = result.Stats.TimeHistogram
	if histogram.NoTimestamp != 1 {
		t.Fatalf("noTimestamp mismatch: %+v", histogram)
	}
}

func TestDigestFieldProjectionDistinctTopN(t *testing.T) {
	values := []string{`{"user":{"id":"u1"}}`, `{"user":{"id":"u1"}}`, `{"user":{"id":"u2"}}`, `{"user":{"id":"u3"}}`}
	messages := make([]kafkaconn.ConsumedMessage, 0, len(values))
	for index, value := range values {
		messages = append(messages, message("t", 0, int64(index), "", value, 0))
	}
	result := AggregateDigest(DigestInput{
		Messages: messages, Topic: "t", Fields: []string{"$.user.id"},
		GroupLimit: 20, TopN: 2, SampleRows: 5,
	})
	if len(result.Stats.Fields) != 1 {
		t.Fatalf("fields stats mismatch: %+v", result.Stats.Fields)
	}
	stats := result.Stats.Fields[0]
	if stats.Field != "$.user.id" || stats.ValueCount != 3 {
		t.Fatalf("distinct mismatch: %+v", stats)
	}
	if stats.Values["u1"] != 2 || len(stats.Values) != 2 || !stats.Truncated {
		t.Fatalf("topN clamp mismatch: %+v", stats.Values)
	}
}

func TestDigestFieldProjectionSkipsNonJSON(t *testing.T) {
	messages := []kafkaconn.ConsumedMessage{message("t", 0, 0, "", "not-json", 0), message("t", 0, 1, "", `{"a":1}`, 0)}
	result := AggregateDigest(DigestInput{
		Messages: messages, Topic: "t", Fields: []string{"$.a"},
		GroupLimit: 20, TopN: 10, SampleRows: 5,
	})
	if stats := result.Stats.Fields[0]; stats.ValueCount != 1 || stats.Values["1"] != 1 {
		t.Fatalf("non-JSON payload must be skipped, valid ones counted: %+v", stats)
	}
}

func TestDigestCellTruncateAndLocatorNeverTruncated(t *testing.T) {
	if got := DigestCellTruncate(strings.Repeat("x", 130), 120); len([]rune(got)) != 121 || !strings.HasSuffix(got, "…") {
		t.Fatalf("cell truncation mismatch: %d", len([]rune(got)))
	}
	if DigestCellTruncate("short", 120) != "short" || DigestCellTruncate("x", 0) != "x" {
		t.Fatal("short/zero-width pass-through failed")
	}
	// 定位字段（topic/partition/offset）不过截断：cursor 行与样本行均保真。
	longTopic := strings.Repeat("t", 200)
	result := AggregateDigest(DigestInput{
		Messages: []kafkaconn.ConsumedMessage{message(longTopic, 7, 12345, strings.Repeat("k", 200), strings.Repeat("v", 200), 0)},
		Topic:    longTopic, Width: 120, GroupLimit: 20, TopN: 10, SampleRows: 5,
	})
	sample := result.Sample[0]
	if sample["topic"] != longTopic || sample["partition"] != int32(7) || sample["offset"] != int64(12345) {
		t.Fatalf("locator truncated: %+v", sample)
	}
	if len([]rune(sample["key"].(string))) != 121 {
		t.Fatalf("key should be truncated: %+v", sample["key"])
	}
}

func TestDigestLargeValuePlaceholder(t *testing.T) {
	message := message("t", 0, 9, "", strings.Repeat("v", 10), 0)
	message.Truncated = true // 模拟 value 超 512KB 被消费层截断
	result := AggregateDigest(DigestInput{Messages: []kafkaconn.ConsumedMessage{message}, Topic: "t", Width: 120, GroupLimit: 20, TopN: 10, SampleRows: 5})
	value := result.Sample[0]["value"].(string)
	if !strings.Contains(value, "partition=0 offset=9") || result.Sample[0]["valueOmitted"] != true {
		t.Fatalf("large value must become a placeholder with a locator: %+v", value)
	}
}

func TestDigestSampleAndRowsClamp(t *testing.T) {
	messages := make([]kafkaconn.ConsumedMessage, 0, 30)
	for index := 0; index < 30; index++ {
		messages = append(messages, message("t", 0, int64(index), "", "{}", 0))
	}
	result := AggregateDigest(DigestInput{Messages: messages, Topic: "t", SampleRows: 5, GroupLimit: 20, TopN: 10, Width: 120})
	if len(result.Sample) != 5 {
		t.Fatalf("sample clamp mismatch: %d", len(result.Sample))
	}
	if len(result.Rows) != 30 {
		t.Fatalf("rows materialize full set for cursor: %d", len(result.Rows))
	}
	if result.Rows[0].Key != "" && result.Rows[0].Topic != "t" {
		t.Fatalf("cursor row shape mismatch: %+v", result.Rows[0])
	}
}
