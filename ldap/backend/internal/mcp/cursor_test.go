package mcp

// cursor_test.go：digest 会话翻页游标（设计 §3 原语 4；验收用例 S-CUR-*，
// 清单见 shared/frontend/README.zh-CN.md）。

import (
	"strconv"
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

// S-CUR-OFFSET offset 越界：clamp 到行数末尾（空批 + done + offset 回显
// clamp 后的位置），不报错也不返回越界数据——AI 用错 offset 时拿到的是
// 可解释的空页而非模糊异常（同族 kafka/files cursor.go 相同语义）。
func TestCursorOffsetBeyondEndClamps(t *testing.T) {
	store := NewCursorStore(0, 0, 0)
	rows := []CursorRow{row("a"), row("b"), row("c")}
	session := store.Put(rows, "", "", intentBase)

	result, status := store.Next(session.ID, NextRequest{Offset: 999}, intentBase)
	if status != LookupFound || len(result.Rows) != 0 || !result.Done {
		t.Fatalf("offset beyond end must clamp to an empty done page: %+v %v", result, status)
	}
	if result.Offset != 3 || result.NextOffset != 3 {
		t.Fatalf("clamped offset mismatch: %+v", result)
	}
	// offset == len(rows)：同样空页 done。
	edge, _ := store.Next(store.Put(rows, "", "", intentBase).ID, NextRequest{Offset: 3}, intentBase)
	if len(edge.Rows) != 0 || !edge.Done {
		t.Fatalf("offset == len must be an empty done page: %+v", edge)
	}
}

// S-CUR-REPEAT 同会话重复翻页：显式 offset 重复读同一段返回相同行（幂等
// 读内容），并把会话游标推进到所读窗口末尾；缺省续读从推进后的位置继续。
func TestCursorRepeatedPagingSameSession(t *testing.T) {
	store := NewCursorStore(0, 0, 0)
	rows := make([]CursorRow, 10)
	for index := range rows {
		rows[index] = CursorRow{DN: itoa10(index)}
	}
	session := store.Put(rows, "", "", intentBase)

	first, _ := store.Next(session.ID, NextRequest{N: 3, Offset: 0}, intentBase)
	again, _ := store.Next(session.ID, NextRequest{N: 3, Offset: 0}, intentBase)
	if first.Rows[0].DN != again.Rows[0].DN || first.Rows[2].DN != again.Rows[2].DN {
		t.Fatalf("explicit-offset reread must return the same window: %+v vs %+v", first, again)
	}
	// 两次显式读都会写游标：最后停在 3。
	if session.Offset != 3 {
		t.Fatalf("session cursor must follow the last explicit window end: %d", session.Offset)
	}
	continuation, _ := store.Next(session.ID, NextRequest{Offset: -1}, intentBase)
	if len(continuation.Rows) != 7 || continuation.Rows[0].DN != itoa10(3) || !continuation.Done {
		t.Fatalf("continuation must start at the cursor and run to the end: %+v", continuation)
	}
	final, status := store.Next(session.ID, NextRequest{Offset: -1}, intentBase)
	if status != LookupFound || len(final.Rows) != 0 || !final.Done {
		t.Fatalf("reading past the end stays an empty found page: %+v %v", final, status)
	}
}

// S-CUR-CONFIG settings 接线：Configure 后下一次 Put 用新 TTL/容量/行上限
// （已存在会话的 ExpiresAt 不追溯）。
func TestCursorConfigureAppliesToNewSessions(t *testing.T) {
	store := NewCursorStore(0, 0, 0)
	legacy := store.Put([]CursorRow{row("old")}, "", "", intentBase)
	store.Configure(50*time.Millisecond, 1, 100)
	if _, status := store.Next(legacy.ID, NextRequest{}, intentBase.Add(40*time.Millisecond)); status != LookupFound {
		t.Fatal("existing sessions must keep their original expiry (no retroactive TTL)")
	}
	// 新容量 1：新会话把旧会话淘汰；fresh（Configure 后首个 Put）用新 TTL。
	fresh := store.Put([]CursorRow{row("new")}, "", "", intentBase)
	if _, status := store.Next(legacy.ID, NextRequest{}, intentBase.Add(41*time.Millisecond)); status != LookupUnknown {
		t.Fatal("configured capacity must evict legacy sessions on the next Put")
	}
	if _, status := store.Next(fresh.ID, NextRequest{}, intentBase.Add(60*time.Millisecond)); status != LookupExpired {
		t.Fatal("sessions created after Configure must use the new TTL")
	}
	// 新 TTL 50ms：Configure 后 Put 的会话 60ms 过期；新行上限 100：截断置标志。
	bigRows := make([]CursorRow, 150)
	for index := range bigRows {
		bigRows[index] = row("x")
	}
	session := store.Put(bigRows, "", "", intentBase)
	if !session.Truncated || len(session.Rows) != 100 {
		t.Fatalf("configured row cap mismatch: truncated=%v rows=%d", session.Truncated, len(session.Rows))
	}
	if _, status := store.Next(session.ID, NextRequest{}, intentBase.Add(60*time.Millisecond)); status != LookupExpired {
		t.Fatal("configured TTL must expire sessions created after Configure")
	}
}

func itoa10(value int) string {
	return "dn-" + strconv.Itoa(value)
}
