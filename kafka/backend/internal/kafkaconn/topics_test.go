package kafkaconn

// topics_test.go：kafka/topics/records/clear 门禁矩阵与 rows 形状（Phase 3
// §12.2.1 / §12.4 G 矩阵）+ topics/list 健康聚合（§12.2.4）。全部离线：
// 门禁在 withAdmin 之前收敛，rows 形状走纯函数，健康聚合走内存 TopicDetails。

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/twmb/franz-go/pkg/kadm"
)

func TestClearTopicRecordsGateMatrix(t *testing.T) {
	cases := []struct {
		name        string
		readOnly    bool
		allowDelete bool
		confirm     string
		wantErrSub  string
		wantInvalid bool // -32602（confirmTopic 不匹配，§12.2.1）
		wantBlocked bool // 审计 blocked
	}{
		{name: "read_only blocked", readOnly: true, allowDelete: true, confirm: "orders", wantErrSub: "read-only", wantBlocked: true},
		{name: "no allow_delete blocked", readOnly: false, allowDelete: false, confirm: "orders", wantErrSub: "does not allow delete", wantBlocked: true},
		{name: "read_only wins over allow_delete", readOnly: true, allowDelete: true, confirm: "orders", wantErrSub: "read-only", wantBlocked: true},
		{name: "confirm missing -32602", readOnly: false, allowDelete: true, confirm: "", wantErrSub: "confirmTopic must match", wantInvalid: true, wantBlocked: true},
		{name: "confirm mismatch -32602", readOnly: false, allowDelete: true, confirm: "other", wantErrSub: "does not match topic", wantInvalid: true, wantBlocked: true},
		{name: "confirm trim match ok", readOnly: false, allowDelete: true, confirm: " orders ", wantErrSub: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := NewService()
			// 127.0.0.1:1 = 立即 connection refused：门禁放行用例快速收敛到
			// 传输层错误，不依赖真实 broker。
			config := `{"bootstrap_servers": "127.0.0.1:1", "allow_delete": ` + boolJSON(tc.allowDelete) + `, "read_only": ` + boolJSON(tc.readOnly) + `}`
			if err := connectWithConfig(t, service, "clear-gate", config, "{}"); err != nil {
				t.Fatalf("connect: %v", err)
			}
			var audits []AuditRecord
			service.Audit = func(rec AuditRecord) { audits = append(audits, rec) }

			result, err := service.ClearTopicRecords(context.Background(), TopicRecordsClearRequest{
				ConnectionID: "clear-gate",
				Topic:        "orders",
				ConfirmTopic: tc.confirm,
			})
			if tc.wantErrSub == "" {
				// 门禁放行后进入 withAdmin（无 broker）→ 业务错而非 nil。
				if err == nil {
					t.Fatal("expected dial/transport error (no broker), got nil")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("error = %v, want contains %q", err, tc.wantErrSub)
			}
			if result != nil {
				t.Errorf("result = %+v, want nil on gate block", result)
			}
			var paramErr *InvalidParamsError
			isInvalid := errors.As(err, &paramErr)
			if tc.wantInvalid != isInvalid {
				t.Errorf("InvalidParamsError = %v, want %v (-32602 mapping)", isInvalid, tc.wantInvalid)
			}
			if tc.wantBlocked {
				found := false
				for _, rec := range audits {
					if rec.Action == "topics.records.clear" && rec.Result == "blocked" {
						found = true
					}
				}
				if !found {
					t.Errorf("no blocked audit for action topics.records.clear: %+v", audits)
				}
			}
		})
	}
}

// boolJSON 是测试内 bool → "true"/"false" 的小工具。
func boolJSON(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func TestClearTopicRecordsRequiresTopic(t *testing.T) {
	service := NewService()
	if err := connectWithConfig(t, service, "clear-topic-req",
		`{"bootstrap_servers": "k1:9092", "read_only": false, "allow_delete": true}`, `{}`); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := service.ClearTopicRecords(context.Background(), TopicRecordsClearRequest{ConnectionID: "clear-topic-req"}); err == nil {
		t.Error("empty topic expected error")
	}
	// 未连接 → 连接不存在。
	if _, err := service.ClearTopicRecords(context.Background(), TopicRecordsClearRequest{Topic: "orders", ConfirmTopic: "orders"}); err == nil {
		t.Error("missing connection expected error")
	}
}

func TestClearRecordsRowsShape(t *testing.T) {
	before := map[int32]int64{0: 5, 1: 3, 2: 7}
	beforeErr := map[int32]error{3: errors.New("unknown topic or partition")}
	deleted := kadm.DeleteRecordsResponses{
		"orders": {
			0: {Topic: "orders", Partition: 0, LowWatermark: 0},
			1: {Topic: "orders", Partition: 1, LowWatermark: 3},
			2: {Topic: "orders", Partition: 2, Err: errors.New("unsupported version")},
			// 分区 3 无响应（ListOffsets 已失败）。
			5: {Topic: "orders", Partition: 5, LowWatermark: 9}, // 幽灵响应，不应出现在 rows
		},
	}
	rows := clearRecordsRows("orders", before, beforeErr, deleted)
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4 (partitions 0-3)", len(rows))
	}
	// 分区升序。
	for i, p := range []int32{0, 1, 2, 3} {
		if rows[i].Partition != p {
			t.Errorf("rows[%d].Partition = %d, want %d", i, rows[i].Partition, p)
		}
	}
	if rows[0].Deleted == nil || *rows[0].Deleted != 5 || rows[0].LowWatermark == nil || *rows[0].LowWatermark != 0 || !rows[0].OK {
		t.Errorf("row0 = %+v, want deleted=5 lowWatermark=0 ok", rows[0])
	}
	if rows[1].Deleted == nil || *rows[1].Deleted != 0 || rows[1].LowWatermark == nil || *rows[1].LowWatermark != 3 || !rows[1].OK {
		t.Errorf("row1 = %+v, want deleted=0 lowWatermark=3 ok", rows[1])
	}
	if rows[2].OK || rows[2].Deleted != nil || rows[2].LowWatermark != nil ||
		!strings.Contains(rows[2].Error, "unsupported version") {
		t.Errorf("row2 = %+v, want ok=false null offsets with error", rows[2])
	}
	if rows[3].OK || rows[3].Deleted != nil || !strings.Contains(rows[3].Error, "unknown topic or partition") {
		t.Errorf("row3 = %+v, want list-offset error row", rows[3])
	}

	// JSON 形状：错误行 deleted/lowWatermark 必须是 null（long|null 契约）。
	raw, err := json.Marshal(rows[2])
	if err != nil {
		t.Fatalf("marshal row: %v", err)
	}
	if !strings.Contains(string(raw), `"deleted":null`) || !strings.Contains(string(raw), `"lowWatermark":null`) {
		t.Errorf("row2 JSON = %s, want null offsets", raw)
	}

	// 空输入 → 空 rows。
	if rows := clearRecordsRows("orders", nil, nil, kadm.DeleteRecordsResponses{}); len(rows) != 0 {
		t.Errorf("empty rows = %+v", rows)
	}
}

// --- 12.2.4 topics/list 健康聚合 ---

func healthyPartition(topic string, p int32) kadm.PartitionDetail {
	return kadm.PartitionDetail{
		Topic: topic, Partition: p,
		Leader: 1, Replicas: []int32{1, 2}, ISR: []int32{1, 2},
	}
}

func TestTopicInfosHealthAggregation(t *testing.T) {
	details := kadm.TopicDetails{
		"t-healthy": {Topic: "t-healthy", Partitions: kadm.PartitionDetails{
			0: healthyPartition("t-healthy", 0),
			1: healthyPartition("t-healthy", 1),
		}},
		"t-under-replicated": {Topic: "t-under-replicated", Partitions: kadm.PartitionDetails{
			0: healthyPartition("t-under-replicated", 0),
			1: {Topic: "t-under-replicated", Partition: 1, Leader: 1, Replicas: []int32{1, 2}, ISR: []int32{1}},
			2: {Topic: "t-under-replicated", Partition: 2, Leader: -1, Replicas: []int32{1, 2}, ISR: []int32{}},
		}},
		"t-offline": {Topic: "t-offline", Partitions: kadm.PartitionDetails{
			0: {Topic: "t-offline", Partition: 0, Leader: 1, Replicas: []int32{1, 2}, ISR: []int32{1, 2}, OfflineReplicas: []int32{2}},
		}},
		"t-load-error": {Topic: "t-load-error", Err: errors.New("leader not available")},
	}
	infos := topicInfos(details)
	byName := map[string]TopicInfo{}
	for _, info := range infos {
		byName[info.Name] = info
	}
	if got := byName["t-healthy"]; !got.IsHealthy || got.UnhealthyPartitions != 0 {
		t.Errorf("t-healthy = %+v, want healthy", got)
	}
	if got := byName["t-under-replicated"]; got.IsHealthy || got.UnhealthyPartitions != 2 {
		t.Errorf("t-under-replicated = %+v, want unhealthy=2", got)
	}
	if got := byName["t-offline"]; got.IsHealthy || got.UnhealthyPartitions != 1 {
		t.Errorf("t-offline = %+v, want unhealthy=1", got)
	}
	if got := byName["t-load-error"]; got.IsHealthy || got.Error == "" {
		t.Errorf("t-load-error = %+v, want isHealthy=false with error", got)
	}
}

func TestPartitionInfosHealthRuleUnchanged(t *testing.T) {
	detail := kadm.TopicDetail{Topic: "t", Partitions: kadm.PartitionDetails{
		0: healthyPartition("t", 0),
		1: {Topic: "t", Partition: 1, Leader: -1, Replicas: []int32{1}, ISR: []int32{}},
	}}
	infos := partitionInfos(detail)
	if len(infos) != 2 {
		t.Fatalf("partitions = %d, want 2", len(infos))
	}
	if !infos[0].IsHealthy {
		t.Errorf("partition 0 = %+v, want healthy", infos[0])
	}
	if infos[1].IsHealthy {
		t.Errorf("partition 1 (no leader) = %+v, want unhealthy", infos[1])
	}
}
