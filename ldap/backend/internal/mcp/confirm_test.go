package mcp

// confirm_test.go：两阶段写确认令牌（设计 §4；验收用例 S-CONF-*，清单见
// shared/frontend/README.zh-CN.md）。

import (
	"testing"
	"time"
)

func TestConfirmHashStableAndDistinct(t *testing.T) {
	first := HashParams([]byte(`{"action":"delete","dn":"uid=a,dc=a"}`))
	second := HashParams([]byte(`{"action":"delete","dn":"uid=a,dc=a"}`))
	changed := HashParams([]byte(`{"action":"delete","dn":"uid=b,dc=a"}`))
	if first == "" || first != second {
		t.Fatal("same canonical params must hash identically")
	}
	if first == changed {
		t.Fatal("changed params must change the hash")
	}
}

func TestConfirmOneTimeConsumption(t *testing.T) {
	store := NewConfirmStore()
	now := intentBase
	token, expiresAt := store.Issue(HashParams([]byte(`{"a":1}`)), now)
	if !expiresAt.After(now) || expiresAt.Sub(now) != ConfirmTTL {
		t.Fatalf("unexpected TTL: %v", expiresAt.Sub(now))
	}
	if got := store.Consume(token, HashParams([]byte(`{"a":1}`)), now.Add(time.Second)); got != ConfirmOK {
		t.Fatalf("first consume should be ok: %v", got)
	}
	// 一次性：第二次消费即 unknown。
	if got := store.Consume(token, HashParams([]byte(`{"a":1}`)), now.Add(2*time.Second)); got != ConfirmUnknown {
		t.Fatalf("token must be single-use: %v", got)
	}
}

func TestConfirmExpiryAndHashMismatch(t *testing.T) {
	store := NewConfirmStore()
	now := intentBase
	expired, _ := store.Issue(HashParams([]byte(`{"a":1}`)), now)
	if got := store.Consume(expired, HashParams([]byte(`{"a":1}`)), now.Add(61*time.Second)); got != ConfirmExpired {
		t.Fatalf("past 60s TTL must be expired: %v", got)
	}
	token, _ := store.Issue(HashParams([]byte(`{"a":1}`)), now)
	if got := store.Consume(token, HashParams([]byte(`{"a":2}`)), now.Add(time.Second)); got != ConfirmHashMismatch {
		t.Fatalf("changed params must invalidate: %v", got)
	}
	// hash 失配同样作废令牌（防止用同一令牌再试改参）。
	if got := store.Consume(token, HashParams([]byte(`{"a":1}`)), now.Add(2*time.Second)); got != ConfirmUnknown {
		t.Fatalf("mismatched token must be burned: %v", got)
	}
	if got := store.Consume("c-nonexistent", "", now); got != ConfirmUnknown {
		t.Fatalf("unknown token: %v", got)
	}
}

// S-CONF-TTL settings 接线：SetTTL 后下一次 Issue 用新 TTL（已签发令牌
// 不追溯）；Sanitized 下限 10s（files confirmTtlSecs 同名同范围 10–600）。
func TestConfirmSetTLTAppliesToNewTokens(t *testing.T) {
	store := NewConfirmStore()
	now := intentBase
	legacy, _ := store.Issue(HashParams([]byte(`{"a":1}`)), now)
	store.SetTTL(10 * time.Second)
	if got := store.Consume(legacy, HashParams([]byte(`{"a":1}`)), now.Add(20*time.Second)); got != ConfirmOK {
		t.Fatalf("pre-existing token keeps its original expiry: %v", got)
	}
	fresh, expiresAt := store.Issue(HashParams([]byte(`{"b":1}`)), now)
	if store.TTL() != 10*time.Second || expiresAt.Sub(now) != 10*time.Second {
		t.Fatalf("new token must use the configured TTL: %v %v", store.TTL(), expiresAt.Sub(now))
	}
	if got := store.Consume(fresh, HashParams([]byte(`{"b":1}`)), now.Add(11*time.Second)); got != ConfirmExpired {
		t.Fatalf("new token must expire on the configured TTL: %v", got)
	}
	// ≤0 的 SetTTL 被忽略（防误配清零）。
	store.SetTTL(0)
	if store.TTL() != 10*time.Second {
		t.Fatalf("non-positive SetTTL must be ignored: %v", store.TTL())
	}
}
