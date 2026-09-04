// service.go（A 路）：连接表与生命周期。
// 相对 tiny-rdm 的改造：
//   - profile 从 SQLite/参数 → lifecycle params 构造（M0 文档 §3.1）；
//   - 连接持久化走宿主 ConnectionConfig，sidecar 只存内存连接表；
//   - admin client 按 connectionId 缓存复用（client.go），consume/stream
//     per-request 新建；
//   - 凭据（sasl_password/tls_client_key）与 Profile 分离，只存内存 connEntry。

package kafkaconn

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"io.dbx.kafka.plugin/internal/lifecycle"
)

// uuidNewString 短别名（预设 id 兜底）。
func uuidNewString() string {
	return uuid.NewString()
}

// Service 持有全部活动连接；并发安全（SDK 每请求一个 goroutine）。
type Service struct {
	mu    sync.Mutex
	conns map[string]*connEntry

	// Audit 是写操作审计回调（§5.4）：由 main 注入（audit.jsonl 落盘 +
	// kafka/audit 事件）。kafkaconn 不直接依赖 store/SDK。nil 时静默跳过。
	Audit func(rec AuditRecord)

	// Presets 提供预设持久化（main.go 注入 store-backed 实现）。
	// nil 时 kafka/presets/* 返回业务错误。
	Presets PresetStore

	// Streams 提供流式会话管理（stream.go；NewService 时初始化）。
	Streams *StreamRegistry
}

// NewService 创建空连接表。
func NewService() *Service {
	return &Service{
		conns:   map[string]*connEntry{},
		Streams: NewStreamRegistry(),
	}
}

// Connect 处理 connection/connect：解析 lifecycle params → 存连接表。
// 惰性建连（首个领域调用才拨号）；幂等：重复 connect 覆盖配置并断开旧实例
// （指纹失效自动重建 client；旧流式会话全部停止）。
func (s *Service) Connect(params *lifecycle.Params) error {
	profile, secrets, err := NewProfileFromLifecycle(params)
	if err != nil {
		return err
	}

	entry := &connEntry{
		profile: profile,
		secrets: secrets,
		target:  connTarget{Host: params.Runtime.Host, Port: params.Runtime.Port},
		status:  "idle",
	}

	s.mu.Lock()
	old := s.conns[profile.ID]
	s.conns[profile.ID] = entry
	s.mu.Unlock()

	if old != nil {
		old.mu.Lock()
		old.closeLocked()
		old.mu.Unlock()
		// 同 id 重连可能换了集群/凭据：旧流式会话立刻停止（它们的
		// per-request client 已随 session 关闭，这里只是兜底清理状态）。
		s.Streams.StopAllForConnection(profile.ID)
	}
	return nil
}

// Test 处理 connection/test：立即拨号 + ListBrokers 真实 metadata 探活；
// 成功返回描述 message。不缓存连接、不残留状态（§5.1）。
func (s *Service) Test(ctx context.Context, params *lifecycle.Params) (string, error) {
	profile, secrets, err := NewProfileFromLifecycle(params)
	if err != nil {
		return "", err
	}
	entry := &connEntry{
		profile: profile,
		secrets: secrets,
		target:  connTarget{Host: params.Runtime.Host, Port: params.Runtime.Port},
		status:  "idle",
	}

	opts, err := entry.buildClientOpts()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return "", err
	}
	defer client.Close()

	admin := kadm.NewClient(client)
	brokers, err := admin.ListBrokers(ctx)
	if err != nil {
		return "", err
	}
	message := fmt.Sprintf("connected to %s (%d broker(s), protocol=%s)",
		strings.Join(profile.BootstrapServers, ","), len(brokers), profile.SecurityProtocol)
	return message, nil
}

// Disconnect 处理 connection/disconnect：关闭并移除连接表条目 + 停全部
// 流式会话；幂等。
func (s *Service) Disconnect(connectionID string) {
	if connectionID == "" {
		return
	}
	s.Streams.StopAllForConnection(connectionID)

	s.mu.Lock()
	entry := s.conns[connectionID]
	delete(s.conns, connectionID)
	s.mu.Unlock()
	if entry == nil {
		return
	}
	entry.mu.Lock()
	entry.closeLocked()
	entry.mu.Unlock()
}

// CloseAll 在进程退出前释放全部连接与流式会话（Serve() 返回后调用，M0 §3.2）。
func (s *Service) CloseAll() {
	s.Streams.StopAll()

	s.mu.Lock()
	entries := make([]*connEntry, 0, len(s.conns))
	for id, entry := range s.conns {
		entries = append(entries, entry)
		delete(s.conns, id)
	}
	s.mu.Unlock()
	for _, entry := range entries {
		entry.mu.Lock()
		entry.closeLocked()
		entry.mu.Unlock()
	}
}

// SnapshotStatuses 返回连接状态表（契约 §5.2 kafka/connections/statuses）。
// 凭据不出现；Bootstrap 是服务端列表逗号拼接。
func (s *Service) SnapshotStatuses() []ConnectionStatus {
	s.mu.Lock()
	entries := make([]*connEntry, 0, len(s.conns))
	for _, entry := range s.conns {
		entries = append(entries, entry)
	}
	s.mu.Unlock()

	statuses := make([]ConnectionStatus, 0, len(entries))
	for _, entry := range entries {
		entry.mu.Lock()
		statuses = append(statuses, ConnectionStatus{
			ConnectionID: entry.profile.ID,
			Name:         entry.profile.Name,
			Bootstrap:    strings.Join(entry.profile.BootstrapServers, ","),
			Status:       statusForContract(entry.status),
			ReadOnly:     entry.profile.ReadOnly,
			ConnectedAt:  entry.connectedAt,
			LastUsedAt:   entry.lastUsedAt,
			Error:        entry.lastError,
		})
		entry.mu.Unlock()
	}
	return statuses
}

// lookup 返回连接条目（含空白 trim；kafka/connections/statuses 为全局视图
// 可省 connectionId）。
func (s *Service) lookup(connectionID string) *connEntry {
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" {
		return nil
	}
	s.mu.Lock()
	entry := s.conns[connectionID]
	s.mu.Unlock()
	return entry
}

// profileOf 返回连接 Profile 副本（策略判定用；未连接时返回空 Profile，
// 门禁以 read_only=true 语义兜底拒绝写）。
func (s *Service) profileOf(connectionID string) Profile {
	entry := s.lookup(connectionID)
	if entry == nil {
		return Profile{ReadOnly: true}
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.profile
}

// emitAudit 触发审计回调（nil 安全）。领域层写操作成功/拒绝后调用；
// Target 只含资源名，Detail 不含消息值/凭据。
func (s *Service) emitAudit(connectionID, action, target, result, detail string) {
	if s.Audit == nil {
		return
	}
	s.Audit(AuditRecord{
		ConnectionID: connectionID,
		Action:       action,
		Target:       target,
		Result:       result,
		Detail:       detail,
	})
}

// --- 预设（kafka/presets/*，照 ldap/presets 形态） ---

// ListPresets 返回消费/过滤预设。
func (s *Service) ListPresets() ([]ConsumePreset, error) {
	if s.Presets == nil {
		return nil, errf("preset store is unavailable")
	}
	return s.Presets.LoadPresets()
}

// SavePreset 保存预设（id 空 = 新建，uuid 兜底；非空 = 覆盖）。
func (s *Service) SavePreset(preset ConsumePreset) (*ConsumePreset, error) {
	if s.Presets == nil {
		return nil, errf("preset store is unavailable")
	}
	preset.ID = trimSpace(preset.ID)
	if preset.ID == "" {
		preset.ID = uuidNewString()
	}
	preset.Name = trimSpace(preset.Name)
	if preset.Name == "" {
		return nil, errf("preset name is required")
	}
	presets, err := s.Presets.LoadPresets()
	if err != nil {
		return nil, err
	}
	replaced := false
	for i := range presets {
		if presets[i].ID == preset.ID {
			presets[i] = preset
			replaced = true
			break
		}
	}
	if !replaced {
		presets = append(presets, preset)
	}
	if err := s.Presets.SavePresets(presets); err != nil {
		return nil, err
	}
	return &preset, nil
}

// RemovePreset 删除预设；不存在报错。
func (s *Service) RemovePreset(id string) error {
	if s.Presets == nil {
		return errf("preset store is unavailable")
	}
	id = trimSpace(id)
	if id == "" {
		return errf("preset id is required")
	}
	presets, err := s.Presets.LoadPresets()
	if err != nil {
		return err
	}
	out := presets[:0]
	found := false
	for _, preset := range presets {
		if preset.ID == id {
			found = true
			continue
		}
		out = append(out, preset)
	}
	if !found {
		return errf("preset %q not found", id)
	}
	return s.Presets.SavePresets(out)
}

func errConnectionNotFound(connectionID string) error {
	return errf("connection %q is not connected; call connection/connect first", connectionID)
}

// statusForContract 把内部 status 折算为契约三态：connected | idle | error。
func statusForContract(status string) string {
	switch status {
	case "connected":
		return "connected"
	case "error":
		return "error"
	default: // idle（connect 后未建连）、closed
		return "idle"
	}
}
