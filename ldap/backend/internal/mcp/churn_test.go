package mcp

// churn_test.go：会话/存储长期 churn 可靠性（可靠性纵深轮，S-CHURN-*，
// kafka Go 版同表）：ConfirmStore/CursorStore/IntentStore 大量「登记→消费/
// 过期→淘汰」循环后内部表无无界增长，活跃条目不被误逐，TTL/容量调整
// 不追溯（MCP_ACCEPTANCE §5/§8）。

import (
	"strconv"
	"testing"
	"time"
)

// S-CHURN-CONF-1 ConfirmStore churn：600 轮 issue→consume（ok/expired/mismatch
// 混合）+ 周期性「只要预览不确认」的弃单，内部表必须收敛（Issue 时 prune
// 过期未消费令牌），不随轮数无界增长。
func TestConfirmChurnBounded(t *testing.T) {
	store := NewConfirmStore()
	now := intentBase
	hash := HashParams([]byte(`{"action":"delete"}`))
	const rounds = 600
	for index := 0; index < rounds; index++ {
		now = now.Add(time.Second)
		token, _ := store.Issue(hash, now)
		switch index % 3 {
		case 0: // 正常消费（一次性删除）。
			if got := store.Consume(token, hash, now); got != ConfirmOK {
				t.Fatalf("round %d: fresh token must consume ok: %v", index, got)
			}
		case 1: // 过期消费（TTL 60s 后）→ expired 且删除。
			if got := store.Consume(token, hash, now.Add(2*ConfirmTTL)); got != ConfirmExpired {
				t.Fatalf("round %d: stale token must expire: %v", index, got)
			}
		default: // hash 失配消费 → 作废删除。
			if got := store.Consume(token, hash+"x", now); got != ConfirmHashMismatch {
				t.Fatalf("round %d: changed hash must invalidate: %v", index, got)
			}
		}
		// 每 5 轮弃一张单（签发后从不消费）：只能靠 Issue 的 prune 收敛。
		if index%5 == 0 {
			store.Issue(hash, now)
		}
		if index%97 == 0 && len(store.items) > 2*60+2 {
			t.Fatalf("round %d: token table growing without bound: %d", index, len(store.items))
		}
	}
	// 收敛后只剩「未过期且未消费」的弃单（≤ TTL 窗口内的量）。
	if len(store.items) > 61 {
		t.Fatalf("token table must converge after churn, got %d", len(store.items))
	}
	// 弃单过期后再消费 → unknown（prune 后不存在）。
	now = now.Add(2 * ConfirmTTL)
	store.Issue(hash, now) // 触发 prune
	if len(store.items) != 1 {
		t.Fatalf("prune must clear expired unconsumed tokens, got %d", len(store.items))
	}
}

// S-CHURN-CONF-2 TTL 调整不追溯：已签发令牌按原 ExpiresAt 过期，新令牌用
// 新 TTL（churn 面：短 TTL 下弃单更快收敛）。
func TestConfirmChurnTTLLifecycle(t *testing.T) {
	store := NewConfirmStore()
	now := intentBase
	hash := HashParams([]byte(`{"a":1}`))
	legacy, _ := store.Issue(hash, now)
	store.SetTTL(10 * time.Second)
	// 旧令牌在原 60s 窗口内仍可用（若追溯为 10s 就会 expired）。
	if got := store.Consume(legacy, hash, now.Add(50*time.Second)); got != ConfirmOK {
		t.Fatalf("legacy token keeps its original 60s expiry (no retroactive TTL): %v", got)
	}
	// 弃单 churn 100 轮（TTL 10s → 任意时刻至多 ~10 张活弃单）。
	for index := 0; index < 100; index++ {
		now = now.Add(time.Second)
		store.Issue(hash, now)
	}
	if len(store.items) > 11 {
		t.Fatalf("short-TTL churn must converge tighter: %d", len(store.items))
	}
}

// S-CHURN-CUR-1 CursorStore churn：300 轮物化→翻页→淘汰循环，内部
// sessions/order 两表一致且收敛在容量内；被淘汰的旧 cursorId 报 unknown。
func TestCursorChurnBounded(t *testing.T) {
	store := NewCursorStore(time.Hour, 8, 100)
	now := intentBase
	lastID := ""
	for index := 0; index < 300; index++ {
		now = now.Add(time.Second)
		rows := make([]CursorRow, 3)
		for i := range rows {
			rows[i] = row("dn-" + strconv.Itoa(index) + "-" + strconv.Itoa(i))
		}
		session := store.Put(rows, "dc=a", "(objectClass=*)", now)
		lastID = session.ID
		if _, status := store.Next(session.ID, NextRequest{Offset: -1}, now); status != LookupFound {
			t.Fatalf("round %d: fresh session must page: %v", index, status)
		}
		if len(store.sessions) != len(store.order) || len(store.sessions) > 8 {
			t.Fatalf("round %d: store diverged: sessions=%d order=%d",
				index, len(store.sessions), len(store.order))
		}
	}
	// 最新会话存活且行内容 churn 后仍正确（显式 offset=0 重读首页）。
	result, status := store.Next(lastID, NextRequest{Offset: 0}, now)
	if status != LookupFound || len(result.Rows) != 3 || result.Rows[0].DN != "dn-299-0" {
		t.Fatalf("surviving session content drift: %+v %v", result, status)
	}
}

// S-CHURN-CUR-2 真 LRU：持续翻页的活跃会话不被误逐——容量 3 下 A 持续
// 命中，最久未用的 B 才是下一个淘汰对象（纯插入序 FIFO 会错杀 A）。
func TestCursorChurnActiveSessionSurvives(t *testing.T) {
	store := NewCursorStore(time.Hour, 3, 0)
	now := intentBase
	a := store.Put([]CursorRow{row("a")}, "", "", now)
	b := store.Put([]CursorRow{row("b")}, "", "", now)
	c := store.Put([]CursorRow{row("c")}, "", "", now)
	// A 活跃：反复翻页。
	for i := 0; i < 10; i++ {
		now = now.Add(time.Second)
		if _, status := store.Next(a.ID, NextRequest{Offset: -1}, now); status != LookupFound {
			t.Fatalf("active session must stay found: %v", status)
		}
	}
	// 第 4 个会话进来：最久未用的 B 被淘汰，活跃的 A 与次新的 C 存活。
	now = now.Add(time.Second)
	d := store.Put([]CursorRow{row("d")}, "", "", now)
	if _, status := store.Next(a.ID, NextRequest{Offset: -1}, now); status != LookupFound {
		t.Fatal("active session must not be evicted (LRU, not FIFO)")
	}
	if _, status := store.Next(b.ID, NextRequest{Offset: -1}, now); status != LookupUnknown {
		t.Fatal("least-recently-used session must be evicted")
	}
	for _, session := range []*CursorSession{c, d} {
		if _, status := store.Next(session.ID, NextRequest{Offset: -1}, now); status != LookupFound {
			t.Fatalf("session %s must survive: %v", session.Rows[0].DN, status)
		}
	}
	if len(store.sessions) != 3 || len(store.order) != 3 {
		t.Fatalf("capacity must hold: %d/%d", len(store.sessions), len(store.order))
	}
}

// S-CHURN-INT-1 IntentStore churn：500 轮登记→回报→过期循环后表收敛在
// LRU 容量内，快照读取不受 churn 影响，过期回报不复活。
func TestIntentChurnBoundedSnapshotIntact(t *testing.T) {
	store := NewIntentStore(60*time.Second, 20)
	snapshot := map[string]any{"panel": "search", "count": float64(42)}
	store.SetSnapshot(snapshot)
	now := intentBase
	for index := 0; index < 500; index++ {
		now = now.Add(time.Second)
		id := "i-" + strconv.Itoa(index)
		store.Register(id, "search", map[string]any{"n": index}, now)
		if index%2 == 0 {
			if !store.Report(id, IntentApplied, map[string]any{"count": float64(index)}, "", now) {
				t.Fatalf("round %d: report must land", index)
			}
		}
		if len(store.items) > 20 || len(store.order) > 20 {
			t.Fatalf("round %d: intent table over capacity: %d/%d", index, len(store.items), len(store.order))
		}
	}
	// 快照不被 churn 污染。
	got := store.Snapshot()
	if got["panel"] != "search" || got["count"] != float64(42) || len(got) != 2 {
		t.Fatalf("snapshot corrupted by churn: %+v", got)
	}
	// 最新 intent 查得到、终态正确。
	intent, status := store.Get("i-498", now)
	if status != LookupFound || intent.State != IntentApplied {
		t.Fatalf("latest reported intent must survive churn: %+v %v", intent, status)
	}
	// 过期后读取顺手清除（再读 unknown），过期回报不复活。
	now = now.Add(2 * time.Minute)
	store.Register("i-final", "search", nil, now) // 触发 prune
	if _, status := store.Get("i-499", now); status != LookupUnknown {
		t.Fatalf("expired intents must be pruned: %v", status)
	}
	if store.Report("i-final", IntentApplied, nil, "", now.Add(2*time.Minute)) {
		t.Fatal("late report must fail")
	}
}
