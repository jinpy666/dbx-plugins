package mcp

// cursor_test.go：digest 会话翻页游标（设计 §3 原语 4；验收用例 S-CUR-*，
// 清单见 shared/frontend/README.zh-CN.md）。

import (
	"testing"
	"time"
)

func row(dn string) CursorRow { return CursorRow{DN: dn} }

func TestCursorBatchDefaultsAndContinuation(t *testing.T) {
	store := NewCursorStore(0, 0, 0)
	rows := make([]CursorRow, 45)
	for index := range rows {
		rows[index] = row("dn-" + string(rune('a'+index%26)) + string(rune('0'+index/26)))
	}
	session := store.Put(rows, "dc=a", "(objectClass=*)", intentBase)

	// 缺省 n=20，会话内游标续读（Offset<0 = 续读；服务端把缺省字段折算为 -1）。
	first, status := store.Next(session.ID, NextRequest{Offset: -1}, intentBase)
	if status != LookupFound || len(first.Rows) != 20 || first.Offset != 0 || first.Done {
		t.Fatalf("first batch mismatch: %+v %v", first, status)
	}
	second, _ := store.Next(session.ID, NextRequest{Offset: -1}, intentBase)
	if len(second.Rows) != 20 || second.Offset != 20 || second.Done {
		t.Fatalf("second batch mismatch: %+v", second)
	}
	third, _ := store.Next(session.ID, NextRequest{Offset: -1}, intentBase)
	if len(third.Rows) != 5 || third.NextOffset != 45 || !third.Done {
		t.Fatalf("final batch mismatch: %+v", third)
	}
	if _, status := store.Next(session.ID, NextRequest{Offset: -1}, intentBase); status != LookupFound {
		t.Fatal("reading past the end stays found with empty rows")
	}
	// 显式 offset 回读；n>20 clamp 到 20。
	jump, _ := store.Next(session.ID, NextRequest{N: 99, Offset: 0}, intentBase)
	if len(jump.Rows) != 20 || jump.Offset != 0 {
		t.Fatalf("clamp/offset mismatch: %+v", jump)
	}
}

func TestCursorExpiry(t *testing.T) {
	store := NewCursorStore(10*time.Minute, 0, 0)
	session := store.Put([]CursorRow{row("a")}, "", "", intentBase)
	if _, status := store.Next(session.ID, NextRequest{}, intentBase.Add(9*time.Minute)); status != LookupFound {
		t.Fatal("within TTL should be found")
	}
	if _, status := store.Next(session.ID, NextRequest{}, intentBase.Add(11*time.Minute)); status != LookupExpired {
		t.Fatal("past TTL should be expired")
	}
	if _, status := store.Next(session.ID, NextRequest{}, intentBase.Add(12*time.Minute)); status != LookupUnknown {
		t.Fatal("expired session should be pruned")
	}
}

func TestCursorLRUCapacity(t *testing.T) {
	store := NewCursorStore(time.Hour, 8, 0)
	ids := make([]string, 0, 9)
	for index := 0; index < 9; index++ {
		ids = append(ids, store.Put([]CursorRow{row("x")}, "", "", intentBase).ID)
	}
	if _, status := store.Next(ids[0], NextRequest{}, intentBase); status != LookupUnknown {
		t.Fatal("oldest session should be evicted (LRU 8)")
	}
	for _, id := range ids[1:] {
		if _, status := store.Next(id, NextRequest{}, intentBase); status != LookupFound {
			t.Fatalf("session %s should survive", id[:12])
		}
	}
}

func TestCursorRowCapMaterialization(t *testing.T) {
	store := NewCursorStore(time.Hour, 0, 10000)
	rows := make([]CursorRow, 10001)
	for index := range rows {
		rows[index] = row("dn")
	}
	session := store.Put(rows, "", "", intentBase)
	if !session.Truncated || len(session.Rows) != 10000 {
		t.Fatalf("materialization cap mismatch: truncated=%v rows=%d", session.Truncated, len(session.Rows))
	}
}

func TestCursorAttributesCarriedIntoRows(t *testing.T) {
	store := NewCursorStore(0, 0, 0)
	session := store.Put([]CursorRow{{DN: "uid=a,dc=a", Attributes: map[string][]string{"uid": {"a"}}}}, "", "", intentBase)
	result, _ := store.Next(session.ID, NextRequest{Offset: -1}, intentBase)
	if result.Rows[0].Attributes["uid"][0] != "a" {
		t.Fatalf("projection lost: %+v", result.Rows[0])
	}
}
