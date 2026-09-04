package kafkaconn

// filter_test.go：匹配器与过滤引擎（matchMode / 分通道 / fieldFilters /
// JSON path / 数值比较），纯内存不连网。

import (
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

func testRecord() *kgo.Record {
	return &kgo.Record{
		Topic:     "orders",
		Partition: 1,
		Offset:    42,
		Timestamp: time.UnixMilli(1700000000000),
		Key:       []byte(`"order-7"`),
		Value:     []byte(`{"user":{"id":7,"name":"alice"},"items":[{"sku":"a1","qty":2}]}`),
		Headers:   []kgo.RecordHeader{{Key: "trace", Value: []byte("t-1")}},
	}
}

func TestTextMatcherMatchModes(t *testing.T) {
	matcher := textMatcher{}
	if !matcher.match("hello world", "WORLD") {
		t.Error("contains should be case-insensitive")
	}
	// contains 即子串匹配。
	if !matcher.match("hello", "ell") {
		t.Error("contains should match substring")
	}
	matcher.mode = "prefix"
	if !matcher.match("hello", "HE") || matcher.match("hello", "ell") {
		t.Error("prefix mode mismatch")
	}
	matcher.mode = "exact"
	if !matcher.match("Hello", "hello") || matcher.match("hello", "hell") {
		t.Error("exact mode mismatch")
	}
	matcher.mode = "regex"
	if !matcher.match("hello123", `h.*\d+`) || matcher.match("hello", `^\d+$`) {
		t.Error("regex mode mismatch")
	}
	// 空 query 恒真（通道未启用语义）。
	if !matcher.match("anything", "") {
		t.Error("empty query should always match")
	}
}

func TestNormalizeMatchMode(t *testing.T) {
	for raw, want := range map[string]string{
		"": "contains", "CONTAINS": "contains", "starts_with": "prefix",
		"regex": "regex", "equals": "exact",
	} {
		got, err := normalizeMatchMode(raw)
		if err != nil || got != want {
			t.Errorf("normalizeMatchMode(%q) = %q, %v", raw, got, err)
		}
	}
	if _, err := normalizeMatchMode("bogus"); err == nil {
		t.Error("bogus matchMode expected error")
	}
}

func TestRecordMatchesChannels(t *testing.T) {
	params := ConsumeParams{Partitions: []int32{1}}
	matcher := textMatcher{}

	// 分区白名单。
	params.Partitions = []int32{0}
	if recordMatches(params, matcher, testRecord()) {
		t.Error("partition filter should exclude partition 1")
	}
	params.Partitions = []int32{1}

	// 全文通道（key+value+headers 拼接）。
	params.Filter = "alice"
	if !recordMatches(params, matcher, testRecord()) {
		t.Error("fulltext filter should hit value")
	}
	params.Filter = "t-1"
	if !recordMatches(params, matcher, testRecord()) {
		t.Error("fulltext filter should hit header")
	}
	params.Filter = "missing-token"
	if recordMatches(params, matcher, testRecord()) {
		t.Error("fulltext filter should not match")
	}
	params.Filter = ""

	// 分通道。
	params.KeyFilter = "order-7"
	params.ValueFilter = "alice"
	params.HeaderFilter = "trace=t-1"
	if !recordMatches(params, matcher, testRecord()) {
		t.Error("channel filters should all hit")
	}
	params.ValueFilter = "bob"
	if recordMatches(params, matcher, testRecord()) {
		t.Error("value filter should exclude")
	}
	params.ValueFilter = ""

	// 时间/offset 范围。
	rangeParams := ConsumeParams{TimestampFrom: int64Ptr(1700000000001)}
	if recordMatches(rangeParams, matcher, testRecord()) {
		t.Error("timestampFrom should exclude")
	}
	rangeParams = ConsumeParams{OffsetTo: int64Ptr(41)}
	if recordMatches(rangeParams, matcher, testRecord()) {
		t.Error("offsetTo should exclude")
	}
}

func TestFieldFiltersBasics(t *testing.T) {
	record := testRecord()
	value := record.Value
	matcher := textMatcher{}

	// exists / not_exists。
	filters := []ConsumeFieldFilter{{Source: "value", Path: "$.user.id", Operator: "exists"}}
	if !fieldFiltersMatch(filters, matcher, record, value) {
		t.Error("$.user.id should exist")
	}
	filters = []ConsumeFieldFilter{{Source: "value", Path: "$.user.missing", Operator: "not_exists"}}
	if !fieldFiltersMatch(filters, matcher, record, value) {
		t.Error("$.user.missing should not exist")
	}

	// 数值比较（gt/lt）。
	filters = []ConsumeFieldFilter{{Source: "value", Path: "user.id", Operator: "gt", Value: "6"}}
	if !fieldFiltersMatch(filters, matcher, record, value) {
		t.Error("7 > 6 should match")
	}
	filters = []ConsumeFieldFilter{{Source: "value", Path: "user.id", Operator: "lt", Value: "6"}}
	if fieldFiltersMatch(filters, matcher, record, value) {
		t.Error("7 < 6 should not match")
	}

	// 数组索引 + exact。
	filters = []ConsumeFieldFilter{{Source: "value", Path: "items[0].sku", Operator: "exact", Value: "a1"}}
	if !fieldFiltersMatch(filters, matcher, record, value) {
		t.Error("items[0].sku == a1 should match")
	}

	// header 通道。
	filters = []ConsumeFieldFilter{{Source: "header", Path: "trace", Operator: "exact", Value: "t-1"}}
	if !fieldFiltersMatch(filters, matcher, record, value) {
		t.Error("header trace == t-1 should match")
	}

	// 元数据通道。
	filters = []ConsumeFieldFilter{{Source: "partition", Operator: "exact", Value: "1"}}
	if !fieldFiltersMatch(filters, matcher, record, value) {
		t.Error("partition == 1 should match")
	}
	filters = []ConsumeFieldFilter{{Source: "offset", Operator: "gt", Value: "41"}}
	if !fieldFiltersMatch(filters, matcher, record, value) {
		t.Error("offset > 41 should match")
	}

	// enabled=false 过滤不参与。
	enabled := false
	filters = []ConsumeFieldFilter{{Source: "value", Path: "user.missing", Operator: "exists", Enabled: &enabled}}
	if !fieldFiltersMatch(filters, matcher, record, value) {
		t.Error("disabled filter should be ignored")
	}

	// 非 JSON value 走原文匹配。
	rawValue := []byte("plain text")
	filters = []ConsumeFieldFilter{{Source: "value", Operator: "contains", Value: "ain"}}
	if !fieldFiltersMatch(filters, matcher, record, rawValue) {
		t.Error("non-JSON contains should match raw text")
	}
}

func TestValidateFieldFilters(t *testing.T) {
	if err := validateFieldFilters([]ConsumeFieldFilter{{Source: "value", Operator: "exists"}}); err != nil {
		t.Errorf("valid filter error = %v", err)
	}
	if err := validateFieldFilters([]ConsumeFieldFilter{{Source: "bogus"}}); err == nil {
		t.Error("bogus source expected error")
	}
	if err := validateFieldFilters([]ConsumeFieldFilter{{Source: "value", Operator: "bogus"}}); err == nil {
		t.Error("bogus operator expected error")
	}
	if err := validateFieldFilters([]ConsumeFieldFilter{{Source: "value", Operator: "gt", Value: "abc"}}); err == nil {
		t.Error("non-numeric gt expected error")
	}
	if err := validateFieldFilters([]ConsumeFieldFilter{{Source: "value", Operator: "regex", Value: "["}}); err == nil {
		t.Error("invalid regex expected error")
	}
	if err := validateFieldFilters([]ConsumeFieldFilter{{Source: "value", Path: "a[", Operator: "exists"}}); err == nil {
		t.Error("invalid path expected error")
	}
}

func TestJSONPath(t *testing.T) {
	data := []byte(`{"a":{"b":[1,{"c":"x"}]}}`)
	if got, ok := jsonPathValue(data, "a.b[1].c"); !ok || got != "x" {
		t.Errorf("jsonPath a.b[1].c = %q, %v", got, ok)
	}
	if got, ok := jsonPathValue(data, "$.a.b[0]"); !ok || got != "1" {
		t.Errorf("jsonPath a.b[0] = %q, %v", got, ok)
	}
	if _, ok := jsonPathValue(data, "a.b[5]"); ok {
		t.Error("out of range index should not exist")
	}
	if _, ok := jsonPathValue([]byte("not json"), "a"); ok {
		t.Error("non-JSON should not resolve")
	}
	if _, err := parseJSONPath("a..b"); err == nil {
		t.Error("empty segment expected error")
	}
	if got, ok := jsonPathValue([]byte(`{"a":1.5}`), "a"); !ok || got != "1.5" {
		t.Errorf("float formatting = %q, %v", got, ok)
	}
}

func TestFieldFiltersNeedValue(t *testing.T) {
	if !fieldFiltersNeedValue([]ConsumeFieldFilter{{Source: "value"}}) {
		t.Error("value source needs value")
	}
	if fieldFiltersNeedValue([]ConsumeFieldFilter{{Source: "header"}}) {
		t.Error("header source does not need decoded value")
	}
}
