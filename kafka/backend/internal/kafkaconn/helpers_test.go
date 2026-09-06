package kafkaconn

// helpers_test.go：领域辅助归一化与映射（partitionUpdates / ACL 取值面 /
// reset 模式 / 常量契约）。

import (
	"errors"
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
		"earliest":         OffsetResetEarliest,
		"latest":           OffsetResetLatest,
		"timestamp":        OffsetResetTimestamp,
		"partitionOffset":  OffsetResetPartitionOffsets,
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

func TestJoinHelpers(t *testing.T) {
	if got := joinNames([]string{"a", "b", "c"}); got != "a,b,c" {
		t.Errorf("joinNames = %q", got)
	}
	// map key 拼接顺序稳定（按字典序）。
	if got := joinMapKeys(map[string]int32{"b": 2, "a": 1, "c": 3}); got != "a,b,c" {
		t.Errorf("joinMapKeys = %q, want a,b,c", got)
	}
	if got := joinMapKeys(nil); got != "" {
		t.Errorf("joinMapKeys(nil) = %q", got)
	}
}

func TestMutationResultsMappers(t *testing.T) {
	okErr := errors.New("topic exists")
	topic := mutationResultsTopic(kadm.CreateTopicResponses{
		"t-ok":  {Topic: "t-ok"},
		"t-err": {Topic: "t-err", Err: okErr},
	})
	if len(topic) != 2 || topic[0].Topic != "t-err" || topic[0].OK || topic[0].Error != "topic exists" {
		t.Errorf("topic = %+v (want sorted with error first)", topic)
	}
	if topic[1].Topic != "t-ok" || !topic[1].OK || topic[1].Error != "" {
		t.Errorf("topic[1] = %+v, want ok", topic[1])
	}

	partitions := mutationResultsCreatePartitions(kadm.CreatePartitionsResponses{
		"p-ok":  {Topic: "p-ok"},
		"p-err": {Topic: "p-err", Err: okErr},
	})
	if len(partitions) != 2 || partitions[0].Topic != "p-err" || partitions[0].OK {
		t.Errorf("partitions = %+v", partitions)
	}

	deletes := mutationResultsTopicDelete(kadm.DeleteTopicResponses{
		"d-ok":  {Topic: "d-ok"},
		"d-err": {Topic: "d-err", Err: okErr},
	})
	if len(deletes) != 2 || deletes[1].Topic != "d-ok" || !deletes[1].OK {
		t.Errorf("deletes = %+v", deletes)
	}

	alters := alterConfigResults(kadm.AlterConfigsResponses{
		{Name: "orders"},
		{Name: "orders", Err: okErr},
	}, "orders")
	if len(alters) != 2 || alters[0].Topic != "orders" || !alters[0].OK {
		t.Errorf("alters = %+v", alters)
	}
	if alters[1].OK || alters[1].Error != "topic exists" {
		t.Errorf("alters[1] = %+v", alters[1])
	}

	// 空响应 → 空切片（非 nil）。
	if got := mutationResultsTopic(kadm.CreateTopicResponses{}); got == nil || len(got) != 0 {
		t.Errorf("empty = %#v", got)
	}
}

func TestACLResourceTypeFullEnum(t *testing.T) {
	cases := map[string]kmsg.ACLResourceType{
		"any":              kmsg.ACLResourceTypeAny,
		"ANY":              kmsg.ACLResourceTypeAny,
		" topic ":          kmsg.ACLResourceTypeTopic,
		"group":            kmsg.ACLResourceTypeGroup,
		"cluster":          kmsg.ACLResourceTypeCluster,
		"transactionalid":  kmsg.ACLResourceTypeTransactionalId,
		"transactional_id": kmsg.ACLResourceTypeTransactionalId,
		"delegationToken":  kmsg.ACLResourceTypeDelegationToken,
		"delegation_token": kmsg.ACLResourceTypeDelegationToken,
		"user":             kmsg.ACLResourceTypeUser,
	}
	for raw, want := range cases {
		got, err := aclResourceType(raw)
		if err != nil || got != want {
			t.Errorf("aclResourceType(%q) = %v, %v (want %v)", raw, got, err, want)
		}
	}
	if _, err := aclResourceType("queue"); err == nil {
		t.Error("unknown resourceType expected error")
	}
}

func TestACLOperationTypeFullEnum(t *testing.T) {
	cases := map[string]kmsg.ACLOperation{
		"":                 kmsg.ACLOperationAny,
		"any":              kmsg.ACLOperationAny,
		"all":              kmsg.ACLOperationAll,
		"read":             kmsg.ACLOperationRead,
		"WRITE":            kmsg.ACLOperationWrite,
		"create":           kmsg.ACLOperationCreate,
		"delete":           kmsg.ACLOperationDelete,
		"alter":            kmsg.ACLOperationAlter,
		"describe":         kmsg.ACLOperationDescribe,
		"clusteraction":    kmsg.ACLOperationClusterAction,
		"cluster_action":   kmsg.ACLOperationClusterAction,
		"describeconfigs":  kmsg.ACLOperationDescribeConfigs,
		"describe_configs": kmsg.ACLOperationDescribeConfigs,
		"alterconfigs":     kmsg.ACLOperationAlterConfigs,
		"alter_configs":    kmsg.ACLOperationAlterConfigs,
		"idempotentwrite":  kmsg.ACLOperationIdempotentWrite,
		"idempotent_write": kmsg.ACLOperationIdempotentWrite,
		"createtokens":     kmsg.ACLOperationCreateTokens,
		"create_tokens":    kmsg.ACLOperationCreateTokens,
		"describetokens":   kmsg.ACLOperationDescribeTokens,
		"describe_tokens":  kmsg.ACLOperationDescribeTokens,
	}
	for raw, want := range cases {
		got, err := aclOperationType(raw)
		if err != nil || got != want {
			t.Errorf("aclOperationType(%q) = %v, %v (want %v)", raw, got, err, want)
		}
	}
	if _, err := aclOperationType("bogus"); err == nil {
		t.Error("unknown operation expected error")
	}
}

func TestACLPermissionTypeFullEnum(t *testing.T) {
	cases := map[string]kmsg.ACLPermissionType{
		"any":   kmsg.ACLPermissionTypeAny,
		"":      kmsg.ACLPermissionTypeAllow,
		"allow": kmsg.ACLPermissionTypeAllow,
		"deny":  kmsg.ACLPermissionTypeDeny,
	}
	for raw, want := range cases {
		got, err := aclPermissionType(raw)
		if err != nil || got != want {
			t.Errorf("aclPermissionType(%q) = %v, %v (want %v)", raw, got, err, want)
		}
	}
	if _, err := aclPermissionType("bogus"); err == nil {
		t.Error("unknown permission expected error")
	}
}
