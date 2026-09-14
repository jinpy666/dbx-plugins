package mcp

// cursor.go：digest 会话翻页游标（设计 §3 原语 4）。
//
// `ldap_search_digest` 成功后把定位字段（DN + 投影属性）物化进进程内会话
// （上限 1 万条，TTL 10 分钟，LRU ≤8 会话）；AI 用 `ldap_cursor_next
// {cursorId, n≤20}` 分批取行——条件不重发、远端不重扫。
// 纯逻辑：时间由调用方注入，便于单测覆盖 TTL/LRU/上限边界。

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// CursorRow 物化的一行（定位字段 DN 不截断；attributes 为投影属性）。
type CursorRow struct {
	DN         string              `json:"dn"`
	Attributes map[string][]string `json:"attributes,omitempty"`
}

// CursorSession 一次 digest 物化的会话。
type CursorSession struct {
	ID        string      `json:"id"`
	BaseDN    string      `json:"baseDn,omitempty"`
	Filter    string      `json:"filter,omitempty"`
	Rows      []CursorRow `json:"rows"`
	Truncated bool        `json:"truncated"` // 物化时超 maxRows 截断
	Offset    int         `json:"-"`         // 下一批起点（会话内续读）
	ExpiresAt time.Time   `json:"-"`
}

// CursorStore digest 会话表。并发安全。
type CursorStore struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	maxRows  int
	sessions map[string]*CursorSession
	order    []string
}

// NewCursorStore 创建游标表（参数 ≤0 时用设计默认：TTL 10 分钟、8 会话、
// 1 万行上限）。
func NewCursorStore(ttl time.Duration, capacity, maxRows int) *CursorStore {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	if capacity <= 0 {
		capacity = 8
	}
	if maxRows <= 0 {
		maxRows = 10000
	}
	return &CursorStore{
		ttl:      ttl,
		capacity: capacity,
		maxRows:  maxRows,
		sessions: map[string]*CursorSession{},
	}
}

// Configure 运行期调整会话参数（mcp/settings/set 的 cursorTtlSecs /
// maxCursorSessions / maxCursorRows）：下一次 Put 生效；已存在会话的
// ExpiresAt 不追溯。≤0 的项保持原值。
func (s *CursorStore) Configure(ttl time.Duration, capacity, maxRows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ttl > 0 {
		s.ttl = ttl
	}
	if capacity > 0 {
		s.capacity = capacity
	}
	if maxRows > 0 {
		s.maxRows = maxRows
	}
}

// Put 物化一次 digest 的行（超 maxRows 截断并置 Truncated）；新会话把最旧
// 会话按 LRU 淘汰。id 冲突概率可忽略（16 字节随机），冲突时旧会话被覆盖。
func (s *CursorStore) Put(rows []CursorRow, baseDN, filter string, now time.Time) *CursorSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)

	truncated := false
	if len(rows) > s.maxRows {
		rows = append([]CursorRow(nil), rows[:s.maxRows]...)
		truncated = true
	} else {
		rows = append([]CursorRow(nil), rows...)
	}
	session := &CursorSession{
		ID:        newCursorID(),
		BaseDN:    baseDN,
		Filter:    filter,
		Rows:      rows,
		Truncated: truncated,
		ExpiresAt: now.Add(s.ttl),
	}
	s.sessions[session.ID] = session
	s.order = append(s.order, session.ID)
	for len(s.order) > s.capacity {
		delete(s.sessions, s.order[0])
		s.order = s.order[1:]
	}
	return session
}

// NextRequest 分批取行参数（n ≤0 时用缺省 20；n >20 clamp 到 20）。
type NextRequest struct {
	N      int
	Offset int // <0 = 续读会话内游标
}

// NextResult 一批行 + 续读位置；done=true 表示没有更多行。
type NextResult struct {
	Rows       []CursorRow
	Offset     int
	NextOffset int
	Done       bool
}

// Next 分批取行：offset<0 时续读会话内游标（AI 不需要自己记 offset）。
// 命中即顶到淘汰序队尾（真 LRU：持续翻页的活跃会话不被纯插入序淘汰）。
// 会话过期（expired，读取时顺手清除）或不存在（unknown）时不返回行。
func (s *CursorStore) Next(id string, req NextRequest, now time.Time) (*NextResult, LookupStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return nil, LookupUnknown
	}
	if !now.Before(session.ExpiresAt) {
		s.removeLocked(id)
		return nil, LookupExpired
	}
	s.touchLocked(id)
	n := req.N
	if n <= 0 {
		n = 20
	}
	if n > 20 {
		n = 20
	}
	offset := req.Offset
	if offset < 0 {
		offset = session.Offset
	}
	if offset > len(session.Rows) {
		offset = len(session.Rows)
	}
	end := offset + n
	if end > len(session.Rows) {
		end = len(session.Rows)
	}
	rows := append([]CursorRow(nil), session.Rows[offset:end]...)
	session.Offset = end
	return &NextResult{
		Rows:       rows,
		Offset:     offset,
		NextOffset: end,
		Done:       end >= len(session.Rows),
	}, LookupFound
}

// pruneLocked 清除过期会话（调用方持锁）。
func (s *CursorStore) pruneLocked(now time.Time) {
	kept := s.order[:0]
	for _, id := range s.order {
		if session, ok := s.sessions[id]; ok && now.Before(session.ExpiresAt) {
			kept = append(kept, id)
		} else {
			delete(s.sessions, id)
		}
	}
	s.order = kept
}

// removeLocked 按命中删除（调用方持锁）。
func (s *CursorStore) removeLocked(id string) {
	delete(s.sessions, id)
	for index, existing := range s.order {
		if existing == id {
			s.order = append(s.order[:index], s.order[index+1:]...)
			break
		}
	}
}

// touchLocked 命中续读时把会话顶到淘汰序队尾（真 LRU：容量淘汰看最近
// 使用而非纯插入序，活跃会话不被误逐）。调用方持锁。
func (s *CursorStore) touchLocked(id string) {
	for index, existing := range s.order {
		if existing == id {
			s.order = append(s.order[:index], s.order[index+1:]...)
			break
		}
	}
	s.order = append(s.order, id)
}

// newCursorID 生成 "cur-<hex16>" 会话 id。
func newCursorID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 失败极罕见；退化为时间戳（同进程内仍几乎不冲突）。
		return "cur-" + hex.EncodeToString([]byte(time.Now().Format("150405.000000000")))
	}
	return "cur-" + hex.EncodeToString(buf)
}
