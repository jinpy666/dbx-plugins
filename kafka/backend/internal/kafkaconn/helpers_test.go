package kafkaconn

// helpers_test.go：领域辅助归一化与映射（partitionUpdates / ACL 取值面 /
// reset 模式 / 常量契约）。

import (
	"testing"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kmsg"
)

func TestNormalizePartitionUpdates(t *testing.T) {
	got, err := normalizePartitionUpdates(map[string]int32{"a": 6, "b": 6, "c": 3})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(got[6]) != 2 || got[6][0] != "a" || got[6][1] != "b" {
		t.Errorf("group 6 = %v", got[6])
	}
	if len(got[3]) != 1 || got[3][0] != "c" {
		t.Errorf("group 3 = %v", got[3])
	}
	if _, err := normalizePartitionUpdates(nil); err == nil {
		t.Error("empty expected error")
	}
	if _, err := normalizePartitionUpdates(map[string]int32{"a": 0}); err == nil {
		t.Error("zero count expected error")
	}
}

func TestACLTokenNormalization(t *testing.T) {
	// resourceType。
	if got, err := aclResourceType("TOPIC"); err != nil || got != kmsg.ACLResourceTypeTopic {
		t.Errorf("TOPIC = %v, %v", got, err)
	}
	if got, err := aclResourceType("group"); err != nil || got != kmsg.ACLResourceTypeGroup {
		t.Errorf("group = %v, %v", got, err)
	}
	if _, err := aclResourceType("bogus"); err == nil {
		t.Error("bogus resourceType expected error")
	}
	// patternType（空 = literal 默认）。
	if got, err := aclPatternType(""); err != nil || got != kmsg.ACLResourcePatternTypeLiteral {
		t.Errorf("default pattern = %v, %v", got, err)
	}
	if got, _ := aclPatternType("prefixed"); got != kmsg.ACLResourcePatternTypePrefixed {
		t.Errorf("prefixed = %v", got)
	}
	// operation。
	if got, _ := aclOperationType("read"); got != kmsg.ACLOperationRead {
		t.Errorf("read = %v", got)
	}
	if got, _ := aclOperationType("clusteraction"); got != kmsg.ACLOperationClusterAction {
		t.Errorf("clusterAction = %v", got)
	}
	if _, err := aclOperationType("bogus"); err == nil {
		t.Error("bogus operation expected error")
	}
	// permission（空 = allow 默认）。
	if got, err := aclPermissionType(""); err != nil || got != kmsg.ACLPermissionTypeAllow {
		t.Errorf("default permission = %v, %v", got, err)
	}
	if got, _ := aclPermissionType("deny"); got != kmsg.ACLPermissionTypeDeny {
		t.Errorf("deny = %v", got)
	}
}

func TestResetModeNormalization(t *testing.T) {
	for raw, want := range map[string]OffsetResetMode{
		"earliest": OffsetResetEarliest,
		"latest":   OffsetResetLatest,
		"timestamp": OffsetResetTimestamp,
		"partitionOffset": OffsetResetPartitionOffsets,
		"partition_offset": OffsetResetPartitionOffsets,
	} {
		got, err := normalizeResetMode(raw)
		if err != nil || got != want {
			t.Errorf("normalizeResetMode(%q) = %v, %v", raw, got, err)
		}
	}
	if _, err := normalizeResetMode("bogus"); err == nil {
		t.Error("bogus resetTo expected error")
	}
}

func TestGroupLag(t *testing.T) {
	if got := groupLag(100, 90); got != 10 {
		t.Errorf("lag = %d, want 10", got)
	}
	if got := groupLag(100, 100); got != 0 {
		t.Errorf("zero lag = %d", got)
	}
	// committed 落后 end（重放）也不为负。
	if got := groupLag(50, 100); got != 0 {
		t.Errorf("negative lag = %d, want 0", got)
	}
	// 未提交（committed=-1）→ 全量未消费。
	if got := groupLag(100, -1); got != 100 {
		t.Errorf("uncommitted lag = %d, want 100", got)
	}
}

func TestAlterConfigsFromMap(t *testing.T) {
	configs, err := alterConfigsFromMap(map[string]string{"retention.ms": "1000"}, []string{"cleanup.policy"})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("configs = %d, want 2", len(configs))
	}
	if configs[0].Op != kadm.SetConfig || configs[0].Name != "retention.ms" {
		t.Errorf("config[0] = %+v", configs[0])
	}
	if configs[1].Op != kadm.DeleteConfig {
		t.Errorf("config[1] op = %v", configs[1].Op)
	}
	if _, err := alterConfigsFromMap(map[string]string{" ": "x"}, nil); err == nil {
		t.Error("empty key expected error")
	}
	if configs, err := alterConfigsFromMap(nil, nil); err != nil || len(configs) != 0 {
		t.Errorf("nil input = %v, %v", configs, err)
	}
}

func TestCreateTopicConfigsFromMap(t *testing.T) {
	configs, err := createTopicConfigsFromMap(map[string]string{"cleanup.policy": "compact"})
	if err != nil || len(configs) != 1 {
		t.Fatalf("configs = %v, %v", configs, err)
	}
	if *configs["cleanup.policy"] != "compact" {
		t.Errorf("value = %q", *configs["cleanup.policy"])
	}
	if configs, _ := createTopicConfigsFromMap(nil); configs != nil {
		t.Error("nil input should return nil")
	}
}

func TestContractConstants(t *testing.T) {
	// 契约常量守卫（§5.3）。
	if maxMessageBytes != 512*1024 {
		t.Errorf("maxMessageBytes = %d, want 524288", maxMessageBytes)
	}
	if maxProduceCount != 1000 {
		t.Errorf("maxProduceCount = %d, want 1000", maxProduceCount)
	}
	if maxExportRecords != 10000 {
		t.Errorf("maxExportRecords = %d, want 10000", maxExportRecords)
	}
}
