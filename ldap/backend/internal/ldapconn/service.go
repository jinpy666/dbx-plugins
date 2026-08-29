// Package ldapconn 是 dbx-ldap-plugin 的连接与领域层，移植自 tiny-rdm
// backend/services/ldap_service.go（迁移映射见 docs/IMPL_PLAN_DBX_LDAP.zh-CN.md §3）。
//
// service.go（L-A 路）：连接表与生命周期。
// 相对 tiny-rdm 的改造：
//   - profile 从 SQLite/参数 → lifecycle params 构造（M0 文档 §3.1）；
//   - 去 ConnectionPoolService 多窗口引用跟踪 → 自管 map[connectionID]*connEntry；
//   - withConn 惰性建连 + ldapNeedsReconnect 断线重连一次语义原样保留；
//   - 凭据（bind_password/ntlm_hash）与 Profile 分离，只存内存 connEntry。
package ldapconn

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-ldap/ldap/v3"

	"io.dbx.ldap.plugin/internal/lifecycle"
)

// connTarget 是一次拨号需要的运行时端点（方案 D5：一律拨 runtime.host:port）。
type connTarget struct {
	Host string
	Port int
}

// connEntry 是单个宿主连接的 sidecar 内状态。
type connEntry struct {
	mu sync.Mutex

	profile Profile     // 已 NormalizeProfile 的连接配置（不含凭据）
	secrets bindSecrets // binding: secret；仅存内存，禁止落日志/审计/事件
	target  connTarget  // runtime 拨号端点

	conn        *ldap.Conn
	connectedAt int64 // unix ms
	lastUsedAt  int64 // unix ms
	status      string
	lastError   string
}

// closeLocked 关闭底层连接（调用方须持 entry.mu）。
func (e *connEntry) closeLocked() {
	if e.conn != nil {
		_ = e.conn.Close()
		e.conn = nil
	}
	e.status = "closed"
}

// Service 持有全部活动连接；并发安全（SDK 每请求一个 goroutine）。
type Service struct {
	mu    sync.Mutex
	conns map[string]*connEntry

	// SchemaCache 供 ldap/schema 实现使用（L-B schema.go 提供的类型）。
	SchemaCache *SchemaCache

	// Audit 是写操作审计回调（§5.3）：由 main 注入（audit.jsonl 落盘 +
	// ldap/audit 事件）。ldapconn 不直接依赖 store/SDK。nil 时静默跳过。
	Audit func(rec AuditRecord)

	// Presets 提供预设持久化（main.go 注入 store-backed 实现，见
	// operations.go 的 PresetStore 契约）。nil 时 ldap/presets/* 报错。
	Presets PresetStore
}

// NewService 创建空连接表。
func NewService() *Service {
	return &Service{
		conns:       map[string]*connEntry{},
		SchemaCache: NewSchemaCache(0), // 0 → schema.go 默认 10 分钟 TTL
	}
}

// NewProfileFromLifecycle 把 lifecycle params 映射为 Profile（manifest §4
// 字段表 → binding 落点，M0 文档 §3.1；M3 补 krb_* / SASL 字段接线）。
// 第二个返回值是连接表内保存的 bind 凭据（不进 Profile，只存 connEntry）。
func NewProfileFromLifecycle(params *lifecycle.Params) (Profile, bindSecrets) {
	profile := Profile{
		ID:   params.ConnectionID(),
		Name: params.Connection.Name,
		URL:  params.Connection.Host, // url binding host（M0-T6 待确认形态，备份方案见实施文档 §4）
	}
	profile.BaseDN = params.ConfigString("base_dn")
	profile.AuthType = params.ConfigString("auth_type")
	profile.BindDN = params.ConfigString("bind_dn")
	profile.Username = firstLDAPNonEmpty(params.ConfigString("username"), params.Connection.Username)
	profile.Domain = params.ConfigString("domain")
	profile.AuthzID = params.ConfigString("authz_id")
	profile.NTLMHash = params.SecretString("ntlm_hash")
	profile.UseStartTLS = params.ConfigBool("use_starttls")
	// tls_verify 缺省 true（manifest §4 默认值）：字段未下发时保持 true。
	if _, present := params.Connection.ExternalConfig["tls_verify"]; present {
		profile.TLSVerify = params.ConfigBool("tls_verify")
	} else {
		profile.TLSVerify = true
	}
	profile.TLSCAPath = params.ConfigString("tls_ca_path")
	profile.TLSServerName = params.ConfigString("tls_server_name")
	profile.SASLHost = params.ConfigString("sasl_host")
	profile.SASLQoP = params.ConfigString("sasl_qop")
	profile.SASLMutualAuth = params.ConfigBool("sasl_mutual_auth")
	// M3 kerberos：manifest krb_* 字段（auth_type=kerberos 时 visible）。
	profile.Kerberos = &LDAPKerberosConfig{
		CredentialType: params.ConfigString("krb_credential_type"),
		Username:       params.ConfigString("krb_username"),
		Realm:          params.ConfigString("krb_realm"),
		KDCHost:        params.ConfigString("krb_kdc_host"),
		KDCPort:        params.ConfigInt("krb_kdc_port"),
		KeytabPath:     params.ConfigString("krb_keytab_path"),
		CCachePath:     params.ConfigString("krb_ccache_path"),
		Krb5ConfPath:   params.ConfigString("krb5_conf_path"),
	}
	profile.TimeoutSeconds = params.ConfigInt("timeout_secs")
	// 只读门禁收敛：连接表单 read_only（插件特定配置项）∥ 宿主标准 read_only
	// （ConnectionConfig 通用连接设置）。
	profile.ReadOnly = params.ConfigBool("read_only") || params.Connection.ReadOnly
	profile.AllowedBaseDNs = params.ConfigStringSlice("allowed_base_dns")
	profile.AllowedWriteBaseDNs = params.ConfigStringSlice("allowed_write_base_dns")
	profile.BlockedAttributes = params.ConfigStringSlice("blocked_attributes")
	if len(profile.BlockedAttributes) == 0 {
		profile.BlockedAttributes = DefaultLDAPBlockedAttributes()
	}

	secrets := bindSecrets{
		BindPassword:     params.SecretString("bind_password"),
		KerberosPassword: params.SecretString("krb_password"),
	}
	return NormalizeProfile(profile), secrets
}

// Connect 处理 connection/connect：解析 lifecycle params → 存连接表。
// 惰性建连（首个领域调用才 dial+bind，对齐 tiny-rdm withConn 语义）；
// 幂等：重复 connect 覆盖配置并断开旧实例。
func (s *Service) Connect(params *lifecycle.Params) error {
	profile, bindPassword := NewProfileFromLifecycle(params)
	if profile.ID == "" {
		return fmt.Errorf("connection.id is required")
	}
	if profile.URL == "" {
		return fmt.Errorf("connection url is required")
	}

	entry := &connEntry{
		profile: profile,
		secrets: bindPassword,
		target:  connTarget{Host: params.Runtime.Host, Port: params.Runtime.Port},
		status:  "idle",
	}

	s.mu.Lock()
	old := s.conns[profile.ID]
	s.conns[profile.ID] = entry
	s.mu.Unlock()

	// 覆盖旧实例：断开旧连接（closeLocked 置 closed；条目已被替换，直接丢弃）。
	if old != nil {
		old.mu.Lock()
		old.closeLocked()
		old.mu.Unlock()
	}
	// 同 id 重复 connect 可能换了服务器/凭据（连接编辑后重连），旧 schema
	// 元数据必须失效，否则最长 10 分钟内会拿到上一台服务器的 schema。
	s.invalidateSchema(profile.ID)
	return nil
}

// Test 处理 connection/test：立即 dial + bind +（有 baseDn 时）base scope
// 读 1 条；成功返回描述 message。不缓存连接、不残留状态（§5.1）。
func (s *Service) Test(ctx context.Context, params *lifecycle.Params) (string, error) {
	profile, secrets := NewProfileFromLifecycle(params)
	if profile.ID == "" {
		return "", fmt.Errorf("connection.id is required")
	}
	if profile.URL == "" {
		return "", fmt.Errorf("connection url is required")
	}
	target := connTarget{Host: params.Runtime.Host, Port: params.Runtime.Port}

	ctx, cancel := contextWithTimeout(ctx, profile)
	defer cancel()

	conn, err := dialProfile(ctx, profile, target, secrets)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	s.emitTLSInsecureAudit(profile)

	message := fmt.Sprintf("connected to %s (auth=%s)", profile.URL, profile.AuthType)
	if baseDN := profile.BaseDN; baseDN != "" {
		result, err := conn.Search(newBaseProbeRequest(baseDN))
		if err != nil {
			return "", fmt.Errorf("read base dn: %w", err)
		}
		message = fmt.Sprintf("%s; base %s returned %d entrie(s)", message, baseDN, len(result.Entries))
	}
	return message, nil
}

// Disconnect 处理 connection/disconnect：关闭并移除连接表条目；幂等。
func (s *Service) Disconnect(connectionID string) {
	if connectionID == "" {
		return
	}
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
	s.invalidateSchema(connectionID)
}

// CloseAll 在进程退出前释放全部连接（Serve() 返回后调用，M0 §3.2）。
func (s *Service) CloseAll() {
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

// Get 返回连接的 Profile 副本（凭据字段不在 Profile 上）。
func (s *Service) Get(connectionID string) (Profile, error) {
	entry := s.lookup(connectionID)
	if entry == nil {
		return Profile{}, errConnectionNotFound(connectionID)
	}
	return entry.profile, nil
}

// WithConn 语义对照 tiny-rdm ldap_service.go withConn(:970)：
// 惰性建连（conn == nil 时 dial+bind）→ 执行 fn → 判定可重连错误时
// 关闭旧连接并重连一次重试 fn。同一连接的操作经 entry.mu 串行化。
func (s *Service) WithConn(ctx context.Context, connectionID string, fn func(conn *ldap.Conn) error) error {
	entry := s.lookup(connectionID)
	if entry == nil {
		return errConnectionNotFound(connectionID)
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	ctx, cancel := contextWithTimeout(ctx, entry.profile)
	defer cancel()

	if entry.conn == nil {
		if err := s.connectLocked(ctx, entry); err != nil {
			return err
		}
	}

	entry.conn.SetTimeout(time.Duration(entry.profile.TimeoutSeconds) * time.Second)
	err := fn(entry.conn)
	entry.lastUsedAt = time.Now().UnixMilli()
	if err == nil {
		entry.status = "connected"
		entry.lastError = ""
		return nil
	}
	if !ldapNeedsReconnect(err) {
		entry.status = "error"
		entry.lastError = err.Error()
		return err
	}
	// 断线重连一次（ldapNeedsReconnect :1963 判定）。
	entry.closeLocked()
	if reconnectErr := s.connectLocked(ctx, entry); reconnectErr != nil {
		return reconnectErr
	}
	err = fn(entry.conn)
	entry.lastUsedAt = time.Now().UnixMilli()
	if err != nil {
		entry.status = "error"
		entry.lastError = err.Error()
		return err
	}
	entry.status = "connected"
	entry.lastError = ""
	return nil
}

// SnapshotStatuses 返回连接状态表（契约 §5.2 ldap/connections/statuses；
// operations.go 的占位方法由 L-B 包装此快照）。凭据不出现。
func (s *Service) SnapshotStatuses() []LDAPConnectionStatus {
	s.mu.Lock()
	entries := make([]*connEntry, 0, len(s.conns))
	for _, entry := range s.conns {
		entries = append(entries, entry)
	}
	s.mu.Unlock()

	statuses := make([]LDAPConnectionStatus, 0, len(entries))
	for _, entry := range entries {
		entry.mu.Lock()
		statuses = append(statuses, LDAPConnectionStatus{
			ConnectionID: entry.profile.ID,
			Name:         entry.profile.Name,
			URL:          entry.profile.URL,
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

// EmitAudit 触发审计回调（nil 安全）。领域层写操作成功/拒绝后调用。
func (s *Service) EmitAudit(rec AuditRecord) {
	if s.Audit == nil {
		return
	}
	s.Audit(rec)
}

// connectLocked 拨号并 bind（调用方须持 entry.mu），语义对照
// tiny-rdm connectRuntimeLocked(:1035)。
func (s *Service) connectLocked(ctx context.Context, entry *connEntry) error {
	conn, err := dialProfile(ctx, entry.profile, entry.target, entry.secrets)
	if err != nil {
		entry.status = "error"
		entry.lastError = err.Error()
		return err
	}
	entry.conn = conn
	now := time.Now().UnixMilli()
	entry.connectedAt = now
	entry.lastUsedAt = now
	entry.status = "connected"
	entry.lastError = ""
	s.emitTLSInsecureAudit(entry.profile)
	return nil
}

// emitTLSInsecureAudit 在 tls_verify=false 且本次连接实际启用 TLS（ldaps 或
// StartTLS）时发一条审计 warning（实施文档 §10：tls_verify=false 走
// InsecureSkipVerify，审计记录一条 warning；连接级显式配置，非默认行为）。
// 只在 dial 成功后触发，每次建连一条；target 不含凭据，仅记录 URL scheme。
func (s *Service) emitTLSInsecureAudit(profile Profile) {
	if profile.TLSVerify {
		return
	}
	scheme := ""
	if parsed, err := url.Parse(profile.URL); err == nil {
		scheme = strings.ToLower(parsed.Scheme)
	}
	if scheme != "ldaps" && !profile.UseStartTLS {
		return
	}
	s.EmitAudit(AuditRecord{
		ConnectionID: profile.ID,
		Action:       "tls-insecure",
		Target:       profile.URL,
		Result:       "warning",
		Detail:       "tls_verify=false: TLS server certificate verification skipped (InsecureSkipVerify)",
	})
}

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

func (s *Service) invalidateSchema(connectionID string) {
	if s.SchemaCache != nil {
		s.SchemaCache.Invalidate(connectionID)
	}
}

func errConnectionNotFound(connectionID string) error {
	return fmt.Errorf("connection %q is not connected; call connection/connect first", connectionID)
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
