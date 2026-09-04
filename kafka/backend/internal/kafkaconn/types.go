// Package kafkaconn 是 dbx-kafka-plugin 的连接与领域层，逻辑参照
// tiny-rdm backend/services/kafka_service.go / kafka_stream_service.go 重写
// （迁移映射见 docs/IMPL_PLAN_DBX_KAFKA.zh-CN.md §1/§5）。
//
// types.go：请求/响应/消息类型。相对 tiny-rdm types/kafka.go 的改造：
//   - ProfileID → ConnectionID（宿主连接模型，§5 契约）；
//   - Profile 收敛为 manifest §4 字段（bootstrap/security_protocol/sasl/tls/
//     read_only/allow_delete），凭据（sasl_password/tls_client_key）与 Profile
//     分离，只存内存 connSecrets；
//   - 删除 SchemaRegistry / Connector / Kerberos / Transport 等宿主替代或
//     Phase 2 延后字段；
//   - 消息形状按 §5.3 二进制保真：valueText（UTF-8 安全预览）恒出 +
//     valueBase64（完整）恒出，key 为非法 UTF-8 时以 keyBase64 透出；
//   - JSON tag 全部 camelCase。
package kafkaconn

import (
	"fmt"
	"strings"
)

// 安全协议与 SASL 机制取值面（manifest §4）。
const (
	SecurityProtocolPlaintext    = "PLAINTEXT"
	SecurityProtocolSSL          = "SSL"
	SecurityProtocolSASLPlaintext = "SASL_PLAINTEXT"
	SecurityProtocolSASLSSL      = "SASL_SSL"

	SASLMechanismPlain        = "PLAIN"
	SASLMechanismSCRAMSHA256  = "SCRAM-SHA-256"
	SASLMechanismSCRAMSHA512  = "SCRAM-SHA-512"
)

// NormalizeSecurityProtocol 归一化安全协议；空值/未知回退 PLAINTEXT。
func NormalizeSecurityProtocol(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "":
		return SecurityProtocolPlaintext
	case SecurityProtocolSSL, SecurityProtocolSASLPlaintext, SecurityProtocolSASLSSL:
		return strings.ToUpper(strings.TrimSpace(value))
	default:
		return SecurityProtocolPlaintext
	}
}

// NormalizeSASLMechanism 归一化 SASL 机制；未知返回空串。
func NormalizeSASLMechanism(value string) string {
	switch strings.TrimSpace(value) {
	case SASLMechanismPlain:
		return SASLMechanismPlain
	case SASLMechanismSCRAMSHA256:
		return SASLMechanismSCRAMSHA256
	case SASLMechanismSCRAMSHA512:
		return SASLMechanismSCRAMSHA512
	default:
		return ""
	}
}

// Profile 是 sidecar 内存的连接配置（由 lifecycle 从宿主 params 构造）。
// 凭据字段（SASL 密码、客户端私钥）不在此结构，见 connSecrets。
type Profile struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	BootstrapServers []string `json:"bootstrapServers"`
	// SecurityProtocol：PLAINTEXT | SSL | SASL_PLAINTEXT | SASL_SSL（§4 默认 PLAINTEXT）。
	SecurityProtocol string `json:"securityProtocol"`
	// SASLMechanism：PLAIN | SCRAM-SHA-256 | SCRAM-SHA-512（含 SASL 时必填）。
	SASLMechanism string `json:"saslMechanism,omitempty"`
	Username      string `json:"username,omitempty"`
	TLSCACert     string `json:"tlsCaCert,omitempty"`
	TLSClientCert string `json:"tlsClientCert,omitempty"`
	TLSInsecureSkipVerify bool `json:"tlsInsecureSkipVerify,omitempty"`
	ClientID      string `json:"clientId,omitempty"`
	// ReadOnly 收敛门禁：连接表单 read_only ∥ 宿主标准 read_only。
	ReadOnly bool `json:"readOnly"`
	// AllowDelete 允许删除类操作；read_only 下强制无效（§6 与门）。
	AllowDelete bool `json:"allowDelete,omitempty"`
}

// NormalizeProfile 归一化 Profile（trim + 协议/机制归一）。
func NormalizeProfile(p Profile) Profile {
	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	p.ClientID = strings.TrimSpace(p.ClientID)
	p.Username = strings.TrimSpace(p.Username)
	p.TLSCACert = strings.TrimSpace(p.TLSCACert)
	p.TLSClientCert = strings.TrimSpace(p.TLSClientCert)
	p.SecurityProtocol = NormalizeSecurityProtocol(p.SecurityProtocol)
	p.SASLMechanism = NormalizeSASLMechanism(p.SASLMechanism)

	servers := make([]string, 0, len(p.BootstrapServers))
	for _, server := range p.BootstrapServers {
		server = strings.TrimSpace(server)
		if server == "" {
			continue
		}
		servers = append(servers, server)
	}
	p.BootstrapServers = servers
	return p
}

// hasSASL 报告安全协议是否含 SASL。
func (p Profile) hasSASL() bool {
	return p.SecurityProtocol == SecurityProtocolSASLPlaintext || p.SecurityProtocol == SecurityProtocolSASLSSL
}

// hasTLS 报告安全协议是否含 SSL。
func (p Profile) hasTLS() bool {
	return p.SecurityProtocol == SecurityProtocolSSL || p.SecurityProtocol == SecurityProtocolSASLSSL
}

// Validate 基础校验（bootstrap/SASL 参数齐备性）。
func (p Profile) Validate() error {
	if p.ID == "" {
		return errf("connection.id is required")
	}
	if len(p.BootstrapServers) == 0 {
		return errf("bootstrapServers is required")
	}
	if p.hasSASL() {
		if p.SASLMechanism == "" {
			return errf("saslMechanism is required for %s", p.SecurityProtocol)
		}
		if p.Username == "" {
			return errf("sasl username is required for %s", p.SecurityProtocol)
		}
	}
	return nil
}

// --- 连接状态（kafka/connections/statuses，照 ldap 形态） ---

// ConnectionStatus 连接状态。
type ConnectionStatus struct {
	ConnectionID string `json:"connectionId"`
	Name         string `json:"name"`
	Bootstrap    string `json:"bootstrap"`
	Status       string `json:"status"` // connected | idle | error
	ReadOnly     bool   `json:"readOnly,omitempty"`
	ConnectedAt  int64  `json:"connectedAt,omitempty"`
	LastUsedAt   int64  `json:"lastUsedAt,omitempty"`
	Error        string `json:"error,omitempty"`
}

// --- brokers ---

// BrokerInfo 对应 kafka/brokers/list 返回（契约 §5.2）。
type BrokerInfo struct {
	NodeID int32  `json:"nodeId"`
	Host   string `json:"host"`
	Port   int32  `json:"port"`
	Rack   string `json:"rack,omitempty"`
}

// BrokersListResult 对应 kafka/brokers/list。
type BrokersListResult struct {
	Brokers []BrokerInfo `json:"brokers"`
}

// BrokerConfigRequest 对应 kafka/brokers/config。
type BrokerConfigRequest struct {
	ConnectionID string `json:"connectionId"`
	BrokerID     int32  `json:"brokerId"`
}

// ConfigEntry 配置条目（brokers/config 与 topics/config/get 共用）。
type ConfigEntry struct {
	Name      string `json:"name"`
	Value     string `json:"value,omitempty"`
	Source    string `json:"source,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`
	IsDefault bool   `json:"isDefault,omitempty"`
}

// ConfigEntriesResult 对应配置类方法返回。
type ConfigEntriesResult struct {
	Entries []ConfigEntry `json:"entries"`
}

// --- topics ---

// TopicsListRequest 对应 kafka/topics/list。
type TopicsListRequest struct {
	ConnectionID    string `json:"connectionId"`
	IncludeInternal bool   `json:"includeInternal,omitempty"`
}

// TopicInfo topic 概要。
type TopicInfo struct {
	Name              string `json:"name"`
	TopicID           string `json:"topicId,omitempty"`
	IsInternal        bool   `json:"isInternal,omitempty"`
	PartitionCount    int    `json:"partitionCount"`
	ReplicationFactor int    `json:"replicationFactor"`
	Error             string `json:"error,omitempty"`
}

// TopicsListResult 对应 kafka/topics/list。
type TopicsListResult struct {
	Topics []TopicInfo `json:"topics"`
}

// TopicsDescribeRequest 对应 kafka/topics/describe。
type TopicsDescribeRequest struct {
	ConnectionID string `json:"connectionId"`
	Topic        string `json:"topic"`
}

// PartitionInfo 分区健康视图（isHealthy：ISR 覆盖全部 replicas 且无 offline）。
type PartitionInfo struct {
	Partition       int32   `json:"partition"`
	Leader          int32   `json:"leader"`
	LeaderEpoch     int32   `json:"leaderEpoch,omitempty"`
	Replicas        []int32 `json:"replicas,omitempty"`
	ISR             []int32 `json:"isr,omitempty"`
	OfflineReplicas []int32 `json:"offlineReplicas,omitempty"`
	IsHealthy       bool    `json:"isHealthy"`
	Error           string  `json:"error,omitempty"`
}

// TopicDescribeResult 对应 kafka/topics/describe。
type TopicDescribeResult struct {
	Topic      string          `json:"topic"`
	Partitions []PartitionInfo `json:"partitions"`
}

// TopicsCreateRequest 对应 kafka/topics/create。
type TopicsCreateRequest struct {
	ConnectionID      string            `json:"connectionId"`
	Topics            []string          `json:"topics"`
	Partitions        int32             `json:"partitions"`
	ReplicationFactor int16             `json:"replicationFactor"`
	Config            map[string]string `json:"config,omitempty"`
}

// TopicsDeleteRequest 对应 kafka/topics/delete（critical 门禁：confirmTopic
// 必须与待删 topic 一致，防误删；多 topic 时要求全部同名或用 confirmTopics）。
type TopicsDeleteRequest struct {
	ConnectionID string `json:"connectionId"`
	Topics       []string `json:"topics"`
	// ConfirmTopic 单 topic 删除的确认字段（§6：与 topic 同名才放行）。
	ConfirmTopic string `json:"confirmTopic,omitempty"`
	// ConfirmTopics 多 topic 删除的确认列表（与 Topics 逐一同名）。
	ConfirmTopics []string `json:"confirmTopics,omitempty"`
}

// PartitionsUpdateRequest 对应 kafka/topics/partitions/update（只增）。
type PartitionsUpdateRequest struct {
	ConnectionID string         `json:"connectionId"`
	Partitions   map[string]int32 `json:"partitions"`
}

// TopicConfigGetRequest 对应 kafka/topics/config/get。
type TopicConfigGetRequest struct {
	ConnectionID string `json:"connectionId"`
	Topic        string `json:"topic"`
}

// TopicConfigAlterRequest 对应 kafka/topics/config/alter。
type TopicConfigAlterRequest struct {
	ConnectionID string            `json:"connectionId"`
	Topic        string            `json:"topic"`
	Config       map[string]string `json:"config,omitempty"`
	DeleteKeys   []string          `json:"deleteKeys,omitempty"`
}

// TopicOffsetsListRequest 对应 kafka/topics/offsets/list。
type TopicOffsetsListRequest struct {
	ConnectionID string   `json:"connectionId"`
	Topics       []string `json:"topics"`
	// OffsetTime：earliest | latest | RFC3339 | unix ms（§5.2）。
	OffsetTime string `json:"offsetTime,omitempty"`
}

// TopicOffsetRow offset 行。
type TopicOffsetRow struct {
	Topic       string `json:"topic"`
	Partition   int32  `json:"partition"`
	Offset      int64  `json:"offset"`
	Timestamp   int64  `json:"timestamp,omitempty"`
	LeaderEpoch int32  `json:"leaderEpoch,omitempty"`
	Error       string `json:"error,omitempty"`
}

// TopicOffsetsListResult 对应 kafka/topics/offsets/list。
type TopicOffsetsListResult struct {
	Rows []TopicOffsetRow `json:"rows"`
}

// --- groups ---

// GroupsListResult 对应 kafka/groups/list。
type GroupsListResult struct {
	Groups []GroupInfo `json:"groups"`
}

// GroupInfo 消费组概要。
type GroupInfo struct {
	Group        string `json:"group"`
	State        string `json:"state,omitempty"`
	ProtocolType string `json:"protocolType,omitempty"`
	Coordinator  int32  `json:"coordinator,omitempty"`
}

// GroupsDescribeRequest 对应 kafka/groups/describe。
type GroupsDescribeRequest struct {
	ConnectionID string `json:"connectionId"`
	Group        string `json:"group"`
}

// GroupMemberInfo 组成员。
type GroupMemberInfo struct {
	MemberID    string             `json:"memberId"`
	InstanceID  string             `json:"instanceId,omitempty"`
	ClientID    string             `json:"clientId,omitempty"`
	ClientHost  string             `json:"clientHost,omitempty"`
	Assignments map[string][]int32 `json:"assignments,omitempty"`
}

// GroupDescribeResult 对应 kafka/groups/describe。
type GroupDescribeResult struct {
	Group        string             `json:"group"`
	State        string             `json:"state,omitempty"`
	ProtocolType string             `json:"protocolType,omitempty"`
	Protocol     string             `json:"protocol,omitempty"`
	Coordinator  int32              `json:"coordinator,omitempty"`
	Members      []GroupMemberInfo  `json:"members,omitempty"`
	Error        string             `json:"error,omitempty"`
}

// GroupOffsetsListRequest 对应 kafka/groups/offsets/list（topics 空 =
// committed 全量）。
type GroupOffsetsListRequest struct {
	ConnectionID string   `json:"connectionId"`
	Group        string   `json:"group"`
	Topics       []string `json:"topics,omitempty"`
}

// GroupOffsetRow 消费组 offset 行（lag = endOffset - committedOffset）。
type GroupOffsetRow struct {
	Topic           string `json:"topic"`
	Partition       int32  `json:"partition"`
	StartOffset     int64  `json:"startOffset"`
	EndOffset       int64  `json:"endOffset"`
	CommittedOffset int64  `json:"committedOffset"`
	Lag             int64  `json:"lag"`
	Error           string `json:"error,omitempty"`
}

// GroupOffsetsListResult 对应 kafka/groups/offsets/list。
// Option 语义：组从未提交过 offset 时 hasCommitted=false（与零 lag 区分）。
type GroupOffsetsListResult struct {
	Rows         []GroupOffsetRow `json:"rows"`
	TotalLag     int64            `json:"totalLag"`
	HasCommitted bool             `json:"hasCommitted"`
}

// GroupDeleteRequest 对应 kafka/groups/delete（critical 门禁）。
type GroupDeleteRequest struct {
	ConnectionID string `json:"connectionId"`
	Group        string `json:"group"`
}

// OffsetResetMode 是 kafka/groups/offsets/reset 的 resetTo 取值。
type OffsetResetMode string

const (
	OffsetResetEarliest         OffsetResetMode = "earliest"
	OffsetResetLatest           OffsetResetMode = "latest"
	OffsetResetTimestamp        OffsetResetMode = "timestamp"
	OffsetResetPartitionOffsets OffsetResetMode = "partitionOffset"
)

// GroupOffsetResetRequest 对应 kafka/groups/offsets/reset。
type GroupOffsetResetRequest struct {
	ConnectionID string   `json:"connectionId"`
	Group        string   `json:"group"`
	Topics       []string `json:"topics,omitempty"`
	ResetTo      string   `json:"resetTo"`
	// TimestampMs 仅 resetTo=timestamp 时使用。
	TimestampMs int64 `json:"timestampMs,omitempty"`
	// PartitionOffsets 仅 resetTo=partitionOffset 时使用：
	// topic → partition → offset（分区号 JSON 序列化为字符串 key）。
	PartitionOffsets map[string]map[int32]int64 `json:"partitionOffsets,omitempty"`
}

// OffsetResetRow 重置结果行。
type OffsetResetRow struct {
	Topic     string `json:"topic"`
	Partition int32  `json:"partition"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}

// OffsetResetResult 对应 kafka/groups/offsets/reset。
type OffsetResetResult struct {
	Rows []OffsetResetRow `json:"rows"`
}

// --- acls ---

// ACLFilter ACL 过滤/定义（§5.2 filter{} 拒绝过宽：至少一个资源维度非空）。
type ACLFilter struct {
	ResourceType string `json:"resourceType,omitempty"`
	ResourceName string `json:"resourceName,omitempty"`
	PatternType  string `json:"patternType,omitempty"`
	Principal    string `json:"principal,omitempty"`
	Host         string `json:"host,omitempty"`
	Operation    string `json:"operation,omitempty"`
	Permission   string `json:"permission,omitempty"`
}

// ACLBinding 一条 ACL（list 行 / create 请求体）。
type ACLBinding struct {
	ResourceType string `json:"resourceType"`
	ResourceName string `json:"resourceName"`
	PatternType  string `json:"patternType,omitempty"`
	Principal    string `json:"principal"`
	Host         string `json:"host,omitempty"`
	Operation    string `json:"operation"`
	Permission   string `json:"permission"`
	Error        string `json:"error,omitempty"`
}

// ACLsListRequest 对应 kafka/acls/list。
type ACLsListRequest struct {
	ConnectionID string    `json:"connectionId"`
	Filter       ACLFilter `json:"filter"`
}

// ACLsListResult 对应 kafka/acls/list。
type ACLsListResult struct {
	ACLs []ACLBinding `json:"acls"`
}

// ACLsCreateRequest 对应 kafka/acls/create。
type ACLsCreateRequest struct {
	ConnectionID string     `json:"connectionId"`
	ACL          ACLBinding `json:"acl"`
}

// ACLsDeleteRequest 对应 kafka/acls/delete。
type ACLsDeleteRequest struct {
	ConnectionID string    `json:"connectionId"`
	Filter       ACLFilter `json:"filter"`
}

// ACLsDeleteResult 对应 kafka/acls/delete。
type ACLsDeleteResult struct {
	Matched []ACLBinding `json:"matched"`
}

// --- 预设（kafka/presets/*，照 ldap/presets 形态；存 store presets.json） ---

// ConsumePreset 消费/过滤预设（Params 为值拷贝，不含凭据）。
type ConsumePreset struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Params ConsumeParams `json:"params"`
}

// PresetStore 是预设持久化接口（main.go 注入 store-backed 实现；
// Service.Presets 为 nil 时 kafka/presets/* 返回业务错误）。
type PresetStore interface {
	LoadPresets() ([]ConsumePreset, error)
	SavePresets(presets []ConsumePreset) error
}

// errf 是包内 fmt.Errorf 的短别名（types/policy 层错误统一走业务 -32000）。
func errf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
