package kafkaconn

// topics_more_test.go：topics 纯映射函数（configEntries / listedOffsetRows /
// deref）与 topics/delete confirmTopic 守卫的 -32602 冻结语义（§3.2）离线矩阵。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kmsg"
)

func strPtr(value string) *string { return &value }

func TestConfigEntriesMapping(t *testing.T) {
	entries := configEntries([]kadm.Config{
		{Key: "cleanup.policy", Value: strPtr("delete"), Source: kmsg.ConfigSourceDynamicTopicConfig},
		{Key: "retention.ms", Value: strPtr("604800000"), Source: kmsg.ConfigSourceDefaultConfig},
		{Key: "password", Sensitive: true, Source: kmsg.ConfigSourceDynamicBrokerConfig},
	})
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	if entries[0].Name != "cleanup.policy" || entries[0].Value != "delete" || entries[0].IsDefault {
		t.Errorf("entries[0] = %+v", entries[0])
	}
	// DEFAULT_CONFIG → IsDefault。
	if !entries[1].IsDefault {
		t.Errorf("entries[1] = %+v, want IsDefault", entries[1])
	}
	// 敏感项 Value 为空字符串（MaybeValue nil 兜底）。
	if !entries[2].Sensitive || entries[2].Value != "" {
		t.Errorf("entries[2] = %+v", entries[2])
	}
	if got := configEntries(nil); len(got) != 0 {
		t.Errorf("nil configs = %+v", got)
	}
}

func TestListedOffsetRowsMapping(t *testing.T) {
	rows := listedOffsetRows(kadm.ListedOffsets{
		"t-b": {
			0: {Topic: "t-b", Partition: 0, Offset: 10, Timestamp: 1700000000000, LeaderEpoch: 3},
			1: {Topic: "t-b", Partition: 1, Offset: -1, Timestamp: -1, LeaderEpoch: -1, Err: errors.New("unknown topic")},
		},
		"t-a": {
			5: {Topic: "t-a", Partition: 5, Offset: 1, Timestamp: 1, LeaderEpoch: 0},
		},
	})
	// topic 升序、分区升序。
	if len(rows) != 3 || rows[0].Topic != "t-a" || rows[1].Topic != "t-b" || rows[1].Partition != 0 || rows[2].Partition != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	// 正常行原样透出。
	if rows[0].Offset != 1 || rows[0].Timestamp != 1 || rows[0].LeaderEpoch != 0 {
		t.Errorf("rows[0] = %+v", rows[0])
	}
	// 负值（无 offset/无时间/无 epoch）钳到 0；错误透传。
	if rows[2].Offset != 0 || rows[2].Timestamp != 0 || rows[2].LeaderEpoch != 0 {
		t.Errorf("rows[2] = %+v, want negatives clamped to 0", rows[2])
	}
	if rows[2].Error != "unknown topic" {
		t.Errorf("rows[2].Error = %q", rows[2].Error)
	}
	if rows := listedOffsetRows(nil); len(rows) != 0 {
		t.Errorf("nil = %+v", rows)
	}
}

func TestDeref(t *testing.T) {
	if got := deref(nil); got != "" {
		t.Errorf("deref(nil) = %q", got)
	}
	value := "x"
	if got := deref(&value); got != "x" {
		t.Errorf("deref = %q", got)
	}
}

func TestDeleteTopicsConfirmGateMapsInvalidParams(t *testing.T) {
	// §3.2 冻结语义：confirmTopic 不匹配是参数错 → *InvalidParamsError
	// （main 层 bizError 映射 -32602），与 topics/records/clear 对齐。
	cases := []struct {
		name       string
		req        TopicsDeleteRequest
		wantErrSub string
	}{
		{"confirm missing", TopicsDeleteRequest{ConnectionID: "dt", Topics: []string{"orders"}}, "confirmTopic must match"},
		{"confirm mismatch", TopicsDeleteRequest{ConnectionID: "dt", Topics: []string{"orders"}, ConfirmTopic: "other"}, "does not match topic"},
		{"confirmTopics partial", TopicsDeleteRequest{ConnectionID: "dt", Topics: []string{"a", "b"}, ConfirmTopics: []string{"a"}}, "confirmTopics"},
		{"topics empty", TopicsDeleteRequest{ConnectionID: "dt"}, "topics is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := NewService()
			// 门禁放行（127.0.0.1:1 快速失败，不会真拨号——confirm 先拦）。
			if err := connectWithConfig(t, service, "dt",
				`{"bootstrap_servers": "127.0.0.1:1", "allow_delete": true}`, `{}`); err != nil {
				t.Fatalf("connect: %v", err)
			}
			var audits []AuditRecord
			service.Audit = func(rec AuditRecord) { audits = append(audits, rec) }

			results, err := service.DeleteTopics(context.Background(), tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("error = %v, want contains %q", err, tc.wantErrSub)
			}
			if results != nil {
				t.Errorf("results = %+v, want nil", results)
			}
			var paramErr *InvalidParamsError
			if !errors.As(err, &paramErr) {
				t.Errorf("error = %T, want *InvalidParamsError (-32602)", err)
			}
			if len(audits) != 1 || audits[0].Result != "blocked" || audits[0].Action != "topics-delete" {
				t.Errorf("audits = %+v, want one blocked topics-delete", audits)
			}
		})
	}

	// 未连接 + 合法 confirm → 连接不存在（confirm 通过后正常路径）。
	service := NewService()
	if err := connectWithConfig(t, service, "dt-ok",
		`{"bootstrap_servers": "127.0.0.1:1", "allow_delete": true}`, `{}`); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := service.DeleteTopics(context.Background(), TopicsDeleteRequest{
		ConnectionID: "dt-missing", Topics: []string{"orders"}, ConfirmTopic: "orders",
	}); err == nil {
		t.Error("missing connection expected error")
	}
}
