// Package lifecycle 解析宿主 → sidecar 的连接生命周期 params。
//
// JSON 形状契约见 shared/IMPL_PLAN_M0_COMMON.zh-CN.md §3.1
// （与 ssh-sftp backend/src/model.rs::from_lifecycle_params 同构）：
//
//	{
//	  "provider":  { "id": "io.dbx.ldap.connection", "databaseType": "ldap" },
//	  "connection": {
//	    "id": "uuid", "name": "prod-ldap", "host": "ldap.example.com",
//	    "port": 636, "username": "", "password": "",
//	    "external_config":     { "auth_type": "simple", "base_dn": "…" },  // binding: config
//	    "connection_secrets":  { "bind_password": "…" }                    // binding: secret
//	  },
//	  "runtime": { "host": "127.0.0.1", "port": 6360 },   // DBX 传输层拨号端点
//	  "operationId": "…"                                  // Host 1.1 才有，缺失本地 uuid 兜底
//	}
//
// binding 落点：config → external_config.<field_key>（snake_case）；
// secret → connection_secrets.<field_key>。凭据只允许经 SecretString 读取，
// 禁止进入日志/审计/事件（M0 文档 §4 红线）。
package lifecycle

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Params 是 connection/test|connect|disconnect 的 params 顶层结构。
type Params struct {
	Provider    Provider   `json:"provider,omitempty"`
	Connection  Connection `json:"connection"`
	Runtime     Runtime    `json:"runtime,omitempty"`
	OperationID string     `json:"operationId,omitempty"`
}

// Provider 标识贡献该连接的 connection-provider。
type Provider struct {
	ID           string `json:"id,omitempty"`
	DatabaseType string `json:"databaseType,omitempty"`
}

// Connection 是宿主连接对象（标准字段 + config/secret 两个绑定落点）。
type Connection struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	// Password 是 binding:"password" 的标准密码字段（manifest 中无 password
	// binding 字段时恒为空；凭据红线：不得写入日志/审计/事件）。
	Password string `json:"password,omitempty"`
	// ReadOnly 是宿主 ConnectionConfig.read_only（连接级通用只读设置）。
	ReadOnly bool `json:"read_only,omitempty"`
	// ExternalConfig 存 binding:"config" 字段，key = manifest field key
	// （snake_case，如 auth_type / base_dn）。
	ExternalConfig map[string]any `json:"external_config,omitempty"`
	// Secrets 存 binding:"secret" 字段，由宿主加密存储、传输时下发明文。
	Secrets map[string]any `json:"connection_secrets,omitempty"`
}

// Runtime 是 DBX 传输层拨号端点（方案 D5：网络层一律连 runtime.host:port）。
type Runtime struct {
	Host string `json:"host,omitempty"`
	Port int    `json:"port,omitempty"`
}

// Parse 解析 lifecycle params。宿主不同版本可能省略 provider/runtime，
// 仅 connection/disconnect 可能只带 {connection:{id}}，故各段均可缺省。
func Parse(raw json.RawMessage) (*Params, error) {
	var params Params
	if len(raw) == 0 {
		return &params, nil
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("parse lifecycle params: %w", err)
	}
	return &params, nil
}

// ConnectionID 返回 connection.id（去空白）。
func (p *Params) ConnectionID() string {
	return strings.TrimSpace(p.Connection.ID)
}

// OperationIDOrUUID 返回 operationId；宿主未携带（Host API 1.0）时用 uuid 兜底。
func (p *Params) OperationIDOrUUID() string {
	if id := strings.TrimSpace(p.OperationID); id != "" {
		return id
	}
	return uuid.NewString()
}

// ConfigString 取 external_config 中的字符串值（兼容 number/bool 的字符串化）。
func (p *Params) ConfigString(key string) string {
	return anyToString(p.Connection.ExternalConfig[key])
}

// ConfigBool 取 external_config 中的布尔值。
func (p *Params) ConfigBool(key string) bool {
	switch value := p.Connection.ExternalConfig[key].(type) {
	case bool:
		return value
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return false
		}
		return parsed
	default:
		return false
	}
}

// ConfigInt 取 external_config 中的整数值。
func (p *Params) ConfigInt(key string) int {
	switch value := p.Connection.ExternalConfig[key].(type) {
	case float64:
		return int(value)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0
		}
		return parsed
	case json.Number:
		parsed, err := strconv.Atoi(value.String())
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}

// ConfigStringSlice 取列表值；textarea 多行字符串按行拆分，空行丢弃。
func (p *Params) ConfigStringSlice(key string) []string {
	switch value := p.Connection.ExternalConfig[key].(type) {
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s := anyToString(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return value
	case string:
		return splitLines(value)
	default:
		return nil
	}
}

// SecretString 取 connection_secrets 中的字符串值。
func (p *Params) SecretString(key string) string {
	return anyToString(p.Connection.Secrets[key])
}

func anyToString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return typed.String()
	case bool:
		return strconv.FormatBool(typed)
	default:
		return ""
	}
}

func splitLines(value string) []string {
	out := []string{}
	for _, line := range strings.Split(value, "\n") {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimSuffix(strings.TrimPrefix(trimmed, ","), ",")
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
