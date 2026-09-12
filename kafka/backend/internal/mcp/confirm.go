package mcp

// confirm.go：两阶段写确认令牌（设计 §4）。
//
// delete（含 recursive）与 modifyDn 强制两阶段：无 confirmToken 返回
// preview + 一次性 confirmToken（60s TTL，参数 hash 绑定）；带 token 且
// hash 一致才执行；参数被改即作废重开预览。
// 纯逻辑：时间由调用方注入，便于单测覆盖过期/hash 失配/一次性消费。

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
	"time"
)

// ConfirmTTL 设计 §4：一次性令牌 60 秒。
const ConfirmTTL = 60 * time.Second

// ConfirmResult Consume 的四种结果。
type ConfirmResult string

const (
	ConfirmOK           ConfirmResult = "ok"
	ConfirmUnknown      ConfirmResult = "unknown"
	ConfirmExpired      ConfirmResult = "expired"
	ConfirmHashMismatch ConfirmResult = "hash_mismatch"
)

// ConfirmEntry 已签发的令牌。
type ConfirmEntry struct {
	ParamHash string
	ExpiresAt time.Time
}

// ConfirmStore 令牌表（进程内，一次性消费）。并发安全。
type ConfirmStore struct {
	mu    sync.Mutex
	items map[string]ConfirmEntry
}

// NewConfirmStore 创建令牌表。
func NewConfirmStore() *ConfirmStore {
	return &ConfirmStore{items: map[string]ConfirmEntry{}}
}

// HashParams 计算 write 请求的绑定 hash（sha256 hex）。输入为归一化后的
// canonical JSON 字节（Go encoding/json 对 struct 确定性输出）。
func HashParams(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// Issue 签发一次性令牌（c-<hex12>，60s TTL）。
func (s *ConfirmStore) Issue(paramHash string, now time.Time) (string, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token := fmt.Sprintf("c-%s", randomHex(6))
	expiresAt := now.Add(ConfirmTTL)
	s.items[token] = ConfirmEntry{ParamHash: paramHash, ExpiresAt: expiresAt}
	return token, expiresAt
}

// Consume 校验并消费令牌：
//   - ok：token 存在、未过期、hash 一致（消费后即删除，一次性）；
//   - expired：存在但过 TTL（消费删除）；
//   - hash_mismatch：token 有效但参数被改（消费删除，须重开预览）；
//   - unknown：token 不存在（从未签发/已消费/已过期清理）。
func (s *ConfirmStore) Consume(token, paramHash string, now time.Time) ConfirmResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.items[token]
	if !ok {
		return ConfirmUnknown
	}
	delete(s.items, token) // 一次性：无论哪种失败都作废
	if !now.Before(entry.ExpiresAt) {
		return ConfirmExpired
	}
	if entry.ParamHash != paramHash {
		return ConfirmHashMismatch
	}
	return ConfirmOK
}

// randomHex n 字节随机 hex。
func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := io.ReadFull(cryptorand.Reader, buf); err != nil {
		// crypto/rand 失败极罕见；退化为纳秒时间戳（防阻塞大于防猜）。
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000000")))[:n*2]
	}
	return hex.EncodeToString(buf)
}
