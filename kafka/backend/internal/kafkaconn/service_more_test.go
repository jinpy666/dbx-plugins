package kafkaconn

// service_more_test.go：连接生命周期收尾（connection/test 的 ZK 错误路径、
// CloseAll）、SR 元数据缓存（subjectVersionKey/lookup/store 族）与
// confluentClientFor 的门禁分支。全部离线（ZK/SR 指向 127.0.0.1:1 不可达
// 或仅校验客户端构造）。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"io.dbx.kafka.plugin/internal/lifecycle"
)

// testParams 构造 service.Test 用的 lifecycle.Params（bootstrap/zookeeper 两形态）。
func testParams(t *testing.T, raw string) *lifecycle.Params {
	t.Helper()
	params, err := lifecycle.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return params
}

func TestServiceTestZKUnreachable(t *testing.T) {
	// connection/test 走 zookeeper 源：ZK 不可达 → 业务错（探活失败语义）。
	service := NewService()
	err := connectWithConfig(t, service, "zk-test",
		`{"connection_source": "zookeeper", "zk_servers": "127.0.0.1:1"}`, `{}`)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	params := testParams(t, `{"connection":{"id":"zk-test","external_config":{
		"connection_source": "zookeeper", "zk_servers": "127.0.0.1:1"}},
		"runtime": {"host": "127.0.0.1", "port": 9092}}`)
	if _, err := service.Test(context.Background(), params); err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("error = %v, want ZK unreachable", err)
	}
}

func TestServiceTestBootstrapDialFails(t *testing.T) {
	// bootstrap 模式：127.0.0.1:1 立即拒绝 → 探活失败（覆盖 Test 主体拨号路径）。
	service := NewService()
	params := testParams(t, `{"connection":{"id":"boot-test","external_config":{
		"bootstrap_servers": "127.0.0.1:1"}},
		"runtime": {"host": "127.0.0.1", "port": 9092}}`)
	if _, err := service.Test(context.Background(), params); err == nil {
		t.Fatal("unreachable bootstrap expected error")
	}
}

func TestCloseAllStopsStreamsAndConns(t *testing.T) {
	service := NewService()
	if err := connectWithConfig(t, service, "ca-1",
		`{"bootstrap_servers": "127.0.0.1:1"}`, `{}`); err != nil {
		t.Fatalf("connect: %v", err)
	}
	service.Streams.inject(newTestSession("s-ca", "ca-1", "orders"))

	service.CloseAll()

	if _, err := service.StreamStatusOf("s-ca"); err == nil {
		t.Error("CloseAll should stop streams")
	}
	// 连接表清空：后续领域调用报连接不存在。
	if _, err := service.ListACLs(context.Background(), ACLsListRequest{
		ConnectionID: "ca-1", Filter: ACLFilter{ResourceType: "topic"},
	}); err == nil {
		t.Error("CloseAll should drop connections")
	}
	// 幂等。
	service.CloseAll()
}

func TestSubjectVersionKeyAndCache(t *testing.T) {
	if got := subjectVersionKey("orders", 3); got != "orders\x003" {
		t.Errorf("key = %q", got)
	}
	if got := subjectVersionKey("orders", 0); got != "orders\x00latest" {
		t.Errorf("latest key = %q", got)
	}
	if got := subjectVersionKey("orders", -1); got != "orders\x00latest" {
		t.Errorf("negative key = %q", got)
	}

	cache := newSchemaMetaCache()
	// 空缓存 miss。
	if _, ok := cache.lookupByID(1); ok {
		t.Error("empty cache should miss by id")
	}
	if _, ok := cache.lookupBySubjectVersion("orders", 1); ok {
		t.Error("empty cache should miss by subject")
	}
	// nil 安全。
	var nilCache *schemaMetaCache
	if _, ok := nilCache.lookupByID(1); ok {
		t.Error("nil cache should miss by id")
	}
	if _, ok := nilCache.lookupBySubjectVersion("s", 1); ok {
		t.Error("nil cache should miss by subject")
	}
	nilCache.store(SchemaMeta{ID: 1})
	nilCache.storeSubjectLookup("s", 1, SchemaMeta{ID: 1})

	// store：ID/Subject/Version 齐全才入两张表。
	cache.store(SchemaMeta{ID: 0, Subject: "s", Version: 1}) // ID 无效 → 忽略
	if _, ok := cache.lookupByID(0); ok {
		t.Error("store with invalid ID should be ignored")
	}
	cache.store(SchemaMeta{ID: 7, Subject: "orders", Version: 2})
	if meta, ok := cache.lookupByID(7); !ok || meta.Subject != "orders" {
		t.Errorf("lookupByID = %+v, %v", meta, ok)
	}
	if meta, ok := cache.lookupBySubjectVersion("orders", 2); !ok || meta.ID != 7 {
		t.Errorf("lookupBySubjectVersion = %+v, %v", meta, ok)
	}
	if _, ok := cache.lookupBySubjectVersion("orders", 0); ok {
		t.Error("store() should not register latest-key")
	}
	if _, ok := cache.lookupBySubjectVersion("", 2); ok {
		t.Error("empty subject should miss")
	}
	if _, ok := cache.lookupByID(-1); ok {
		t.Error("invalid id should miss")
	}

	// storeSubjectLookup：subject lookup 为主，同时回填 byID。
	cache.storeSubjectLookup("payments", 1, SchemaMeta{ID: 9, Subject: "payments", Version: 1})
	if meta, ok := cache.lookupBySubjectVersion("payments", 1); !ok || meta.ID != 9 {
		t.Errorf("storeSubjectLookup = %+v, %v", meta, ok)
	}
	if _, ok := cache.lookupByID(9); !ok {
		t.Error("storeSubjectLookup should also fill byID")
	}
	cache.storeSubjectLookup("payments", 1, SchemaMeta{ID: 0}) // ID 无效 → 忽略
	if _, ok := cache.lookupBySubjectVersion("payments", 1); !ok {
		t.Error("existing entry should survive invalid store")
	}
}

func TestConfluentClientForGateBranches(t *testing.T) {
	// 未连接。
	service := NewService()
	if _, err := service.confluentClientFor("ghost"); err == nil {
		t.Error("ghost expected error")
	}
	// schemaRegistry=none 且 srUrl 缺失 → 客户端构造报错。
	service = NewService()
	if err := connectWithConfig(t, service, "sr-none",
		`{"bootstrap_servers": "k1:9092", "schema_registry": "none"}`, `{}`); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := service.confluentClientFor("sr-none"); err == nil || !strings.Contains(err.Error(), "srUrl is empty") {
		t.Errorf("err = %v, want empty srUrl", err)
	}
	// schemaMountSupported：provider=none → 参数错（-32602）。
	err := service.schemaMountSupported("sr-none", "confluent", "produce")
	var paramErr *InvalidParamsError
	if !errors.As(err, &paramErr) || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("schemaMountSupported(none) = %v, want InvalidParamsError disabled", err)
	}

	// provider=aws_glue → wire format 仅 Confluent，业务错。
	service = NewService()
	if err := connectWithConfig(t, service, "sr-glue",
		`{"bootstrap_servers": "k1:9092", "schema_registry": "aws_glue", "glue_region": "us-east-1", "glue_registry_name": "r"}`, `{}`); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := service.schemaMountSupported("sr-glue", "confluent", "consume"); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("schemaMountSupported(glue) = %v, want not enabled", err)
	}
	// 未知 registry 参数 → 参数错。
	if err := service.schemaMountSupported("sr-glue", "azure", "consume"); !errors.As(err, &paramErr) {
		t.Errorf("schemaMountSupported(unknown) = %v, want InvalidParamsError", err)
	}

	// confluent + sr_url → 客户端构造成功（离线，仅 URL 校验）。
	service = NewService()
	if err := connectWithConfig(t, service, "sr-c",
		`{"bootstrap_servers": "k1:9092", "schema_registry": "confluent", "sr_url": "http://127.0.0.1:1"}`, `{}`); err != nil {
		t.Fatalf("connect: %v", err)
	}
	client, err := service.confluentClientFor("sr-c")
	if err != nil || client == nil {
		t.Fatalf("client = %+v, %v", client, err)
	}
	if err := service.schemaMountSupported("sr-c", "", "produce"); err != nil {
		t.Errorf("schemaMountSupported(default confluent) = %v, want nil", err)
	}
}
