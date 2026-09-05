package kafkaconn

// policy_test.go：read_only × allow_delete 门禁矩阵与 confirmTopic 守卫（§6）。

import (
	"strings"
	"testing"
)

func TestEnsureWriteAllowedMatrix(t *testing.T) {
	cases := []struct {
		name     string
		readOnly bool
		allowDel bool
		writeOK  bool
		deleteOK bool
	}{
		{"read-write full", false, true, true, true},
		{"read-write no delete", false, false, true, false},
		{"read-only with delete flag", true, true, false, false},
		{"read-only default", true, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := Profile{Name: "test", ReadOnly: tc.readOnly, AllowDelete: tc.allowDel}
			err := ensureWriteAllowed(profile, "topics/create")
			if tc.writeOK && err != nil {
				t.Errorf("write blocked unexpectedly: %v", err)
			}
			if !tc.writeOK && err == nil {
				t.Error("write should be blocked (read_only)")
			}
			err = ensureDeleteAllowed(profile, "topics/delete")
			if tc.deleteOK && err != nil {
				t.Errorf("delete blocked unexpectedly: %v", err)
			}
			if !tc.deleteOK && err == nil {
				t.Error("delete should be blocked (read_only × allow_delete 与门)")
			}
		})
	}
}

func TestEnsureWriteAllowedMessage(t *testing.T) {
	profile := Profile{Name: "prod", ReadOnly: true}
	err := ensureWriteAllowed(profile, "messages/produce")
	if err == nil {
		t.Fatal("expected error")
	}
	// 错误文案含 blocked 语义（main 层映射 -32000），不含凭据。
	if got := err.Error(); !containsAll(got, "read-only", "messages/produce") {
		t.Errorf("error = %q", got)
	}
}

func TestEnsureTopicDeleteConfirm(t *testing.T) {
	// 单 topic：confirmTopic 必须同名。
	if err := ensureTopicDeleteConfirm(TopicsDeleteRequest{Topics: []string{"orders"}}); err == nil {
		t.Error("missing confirmTopic expected error")
	}
	if err := ensureTopicDeleteConfirm(TopicsDeleteRequest{Topics: []string{"orders"}, ConfirmTopic: "other"}); err == nil {
		t.Error("mismatched confirmTopic expected error")
	}
	if err := ensureTopicDeleteConfirm(TopicsDeleteRequest{Topics: []string{"orders"}, ConfirmTopic: " orders "}); err != nil {
		t.Errorf("confirmTopic should trim-match: %v", err)
	}

	// 多 topic：confirmTopics 逐一对齐。
	multi := TopicsDeleteRequest{Topics: []string{"a", "b"}, ConfirmTopics: []string{"a", "b"}}
	if err := ensureTopicDeleteConfirm(multi); err != nil {
		t.Errorf("multi confirm error = %v", err)
	}
	badMulti := TopicsDeleteRequest{Topics: []string{"a", "b"}, ConfirmTopics: []string{"a"}}
	if err := ensureTopicDeleteConfirm(badMulti); err == nil {
		t.Error("incomplete confirmTopics expected error")
	}

	// 空 topics 拒绝。
	if err := ensureTopicDeleteConfirm(TopicsDeleteRequest{}); err == nil {
		t.Error("empty topics expected error")
	}
}

func TestNormalizeTopicNames(t *testing.T) {
	got := normalizeTopicNames([]string{" a ", "", "a", "b"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("normalizeTopicNames = %v", got)
	}
}

// containsAll 报告 s 是否包含全部子串。
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
