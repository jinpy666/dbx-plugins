// dbx-plugin-ldap sidecar 入口（L-A 路）。
//
// 装配：dbxpluginsdk.NewServer + Handler switch。方法表按实施文档 §5.2 全量：
//
//	connection/test | connection/connect | connection/disconnect   （本文件实现）
//	ldap/search | ldap/entry/get | ldap/rootDse | ldap/schema |
//	ldap/entry/add | ldap/entry/modify | ldap/entry/delete |
//	ldap/entry/modifyDn | ldap/connections/statuses |
//	ldap/presets/list | ldap/presets/save | ldap/presets/remove    （转发 internal/ldapconn）
//
// mcp/* 本期（M1）不做。公共约定：参数/返回 camelCase；领域方法必填
// connectionId（ldap/connections/statuses 为全局视图可省）；业务错误统一
// PluginError{-32000, message}；未注册方法 -32601（SDK MethodNotFound）。
// 凭据不进日志/事件/审计。
package main

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"

	"io.dbx.ldap.plugin/internal/ldapconn"
	"io.dbx.ldap.plugin/internal/lifecycle"
	"io.dbx.ldap.plugin/internal/store"
)

// pluginHandler 实现 dbxpluginsdk.Handler。
type pluginHandler struct {
	svc *ldapconn.Service
	st  *store.Store

	mu      sync.Mutex
	emitter *dbxpluginsdk.Emitter // Serve 期间单例，用于 ldap/audit 事件
}

func main() {
	st, err := store.Open()
	if err != nil {
		// 数据目录不可用不阻断连接能力：审计/预设降级为不可用。
		log.Printf("[dbx-plugin-ldap] store disabled: %v", err)
		st = nil
	}

	svc := ldapconn.NewService()
	handler := &pluginHandler{svc: svc, st: st}
	svc.Audit = handler.auditRecord
	svc.Presets = newPresetStore(st)

	metadata := dbxpluginsdk.Metadata{
		ID:           "io.dbx.ldap",
		Version:      "0.1.0",
		Capabilities: []string{"connections", "events"},
	}
	server := dbxpluginsdk.NewServer(metadata, handler)

	// stdin EOF（进程生命周期结束）→ Serve 返回 → 清理全部连接（M0 §3.2）。
	defer svc.CloseAll()
	if err := server.Serve(); err != nil {
		log.Fatal(err)
	}
}

// Handle 方法分发（每请求一个 goroutine，Handler 须并发安全：全部状态在
// ldapconn.Service 内加锁，本结构体仅 emitter 一个可变字段且持锁访问）。
func (h *pluginHandler) Handle(
	_ dbxpluginsdk.RequestContext,
	method string,
	params json.RawMessage,
	emitter *dbxpluginsdk.Emitter,
) (any, *dbxpluginsdk.PluginError) {
	h.mu.Lock()
	h.emitter = emitter
	h.mu.Unlock()

	switch method {
	case "connection/test":
		return h.connectionTest(params)
	case "connection/connect":
		return h.connectionConnect(params)
	case "connection/disconnect":
		return h.connectionDisconnect(params)

	case "ldap/search":
		return h.forwardSearch(params)
	case "ldap/count":
		return h.forwardCount(params)
	case "ldap/entry/get":
		return h.forwardGetEntry(params)
	case "ldap/rootDse":
		return h.forwardRootDSE(params)
	case "ldap/schema":
		return h.forwardSchema(params)
	case "ldap/entry/add":
		return h.forwardAddEntry(params)
	case "ldap/entry/modify":
		return h.forwardModifyEntry(params)
	case "ldap/entry/delete":
		return h.forwardDeleteEntry(params)
	case "ldap/entry/modifyDn":
		return h.forwardModifyDN(params)
	case "ldap/connections/statuses":
		return h.forwardStatuses()
	case "ldap/presets/list":
		return h.forwardPresetsList()
	case "ldap/presets/save":
		return h.forwardPresetsSave(params)
	case "ldap/presets/remove":
		return h.forwardPresetsRemove(params)

	default:
		return nil, dbxpluginsdk.MethodNotFound(method)
	}
}

// --- 生命周期方法（§5.1） ---

func (h *pluginHandler) connectionTest(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	parsed, err := lifecycle.Parse(params)
	if err != nil {
		return nil, invalidParams(err)
	}
	message, err := h.svc.Test(getContext(), parsed)
	if err != nil {
		return nil, bizError(err)
	}
	return map[string]any{"success": true, "message": message}, nil
}

func (h *pluginHandler) connectionConnect(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	parsed, err := lifecycle.Parse(params)
	if err != nil {
		return nil, invalidParams(err)
	}
	if err := h.svc.Connect(parsed); err != nil {
		return nil, bizError(err)
	}
	return map[string]any{"success": true}, nil
}

func (h *pluginHandler) connectionDisconnect(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	parsed, err := lifecycle.Parse(params)
	if err != nil {
		return nil, invalidParams(err)
	}
	id := parsed.ConnectionID()
	if id == "" {
		return nil, dbxpluginsdk.NewError(-32602, "Missing connection id")
	}
	h.svc.Disconnect(id)
	return map[string]any{"success": true}, nil
}

// --- 领域方法转发（方法体由 L-B 实现；转发与返回包络在此定型） ---

func (h *pluginHandler) forwardSearch(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req ldapconn.LDAPSearchRequest
	if perr := decodeParams(params, &req); perr != nil {
		return nil, perr
	}
	result, err := h.svc.Search(getContext(), req)
	if err != nil {
		return nil, bizError(err)
	}
	return result, nil
}

func (h *pluginHandler) forwardCount(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req ldapconn.LDAPCountRequest
	if perr := decodeParams(params, &req); perr != nil {
		return nil, perr
	}
	result, err := h.svc.Count(getContext(), req)
	if err != nil {
		return nil, bizError(err)
	}
	return result, nil
}

func (h *pluginHandler) forwardGetEntry(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req ldapconn.LDAPGetEntryRequest
	if perr := decodeParams(params, &req); perr != nil {
		return nil, perr
	}
	entry, err := h.svc.GetEntry(getContext(), req)
	if err != nil {
		return nil, bizError(err)
	}
	return map[string]any{"entry": entry}, nil
}

func (h *pluginHandler) forwardRootDSE(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req ldapconn.LDAPRootDSERequest
	if perr := decodeParams(params, &req); perr != nil {
		return nil, perr
	}
	entry, err := h.svc.RootDSE(getContext(), req)
	if err != nil {
		return nil, bizError(err)
	}
	return map[string]any{"attributes": entry.Attributes}, nil
}

func (h *pluginHandler) forwardSchema(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req ldapconn.LDAPSchemaMetadataRequest
	if perr := decodeParams(params, &req); perr != nil {
		return nil, perr
	}
	metadata, err := h.svc.SchemaMetadata(getContext(), req)
	if err != nil {
		return nil, bizError(err)
	}
	return metadata, nil
}

func (h *pluginHandler) forwardAddEntry(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req ldapconn.LDAPAddEntryRequest
	if perr := decodeParams(params, &req); perr != nil {
		return nil, perr
	}
	if err := h.svc.AddEntry(getContext(), req); err != nil {
		return nil, bizError(err)
	}
	return map[string]any{"success": true}, nil
}

func (h *pluginHandler) forwardModifyEntry(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req ldapconn.LDAPModifyEntryRequest
	if perr := decodeParams(params, &req); perr != nil {
		return nil, perr
	}
	if err := h.svc.ModifyEntry(getContext(), req); err != nil {
		return nil, bizError(err)
	}
	return map[string]any{"success": true}, nil
}

func (h *pluginHandler) forwardDeleteEntry(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req ldapconn.LDAPDeleteEntryRequest
	if perr := decodeParams(params, &req); perr != nil {
		return nil, perr
	}
	if err := h.svc.DeleteEntry(getContext(), req); err != nil {
		return nil, bizError(err)
	}
	return map[string]any{"success": true}, nil
}

func (h *pluginHandler) forwardModifyDN(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var req ldapconn.LDAPModifyDNRequest
	if perr := decodeParams(params, &req); perr != nil {
		return nil, perr
	}
	if err := h.svc.ModifyDN(getContext(), req); err != nil {
		return nil, bizError(err)
	}
	return map[string]any{"success": true}, nil
}

func (h *pluginHandler) forwardStatuses() (any, *dbxpluginsdk.PluginError) {
	statuses, err := h.svc.ConnectionStatuses()
	if err != nil {
		return nil, bizError(err)
	}
	return map[string]any{"statuses": statuses}, nil
}

func (h *pluginHandler) forwardPresetsList() (any, *dbxpluginsdk.PluginError) {
	presets, err := h.svc.ListPresets()
	if err != nil {
		return nil, bizError(err)
	}
	return map[string]any{"presets": presets}, nil
}

func (h *pluginHandler) forwardPresetsSave(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var body struct {
		Preset ldapconn.LDAPSearchPreset `json:"preset"`
	}
	if err := json.Unmarshal(params, &body); err != nil {
		return nil, invalidParams(err)
	}
	preset, err := h.svc.SavePreset(body.Preset)
	if err != nil {
		return nil, bizError(err)
	}
	return map[string]any{"success": true, "preset": preset}, nil
}

func (h *pluginHandler) forwardPresetsRemove(params json.RawMessage) (any, *dbxpluginsdk.PluginError) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &body); err != nil {
		return nil, invalidParams(err)
	}
	if body.ID == "" {
		return nil, dbxpluginsdk.NewError(-32602, "Missing preset id")
	}
	if err := h.svc.RemovePreset(body.ID); err != nil {
		return nil, bizError(err)
	}
	return map[string]any{"success": true}, nil
}

// --- 审计与公共 helper ---

// auditRecord 是 ldapconn.Service.Audit 回调：audit.jsonl 落盘 +
// ldap/audit 事件（§5.3，供工作台轻提示；两条通道携带同一份非敏感数据；
// rec.Target 只含 DN，不含属性值）。
func (h *pluginHandler) auditRecord(rec ldapconn.AuditRecord) {
	if h.st != nil {
		if err := h.st.AppendAudit(store.AuditRecord{
			ConnectionID: rec.ConnectionID,
			Action:       auditAction(rec.Action),
			Target:       rec.Target,
			Result:       auditResultForStore(rec.Result),
		}); err != nil {
			log.Printf("[dbx-plugin-ldap] audit write failed: %v", err)
		}
	}
	h.mu.Lock()
	emitter := h.emitter
	h.mu.Unlock()
	if emitter != nil {
		if err := emitter.Event("ldap/audit", rec); err != nil {
			log.Printf("[dbx-plugin-ldap] audit event failed: %v", err)
		}
	}
}

// auditAction 统一审计 action 命名（ldap/<action>）。
func auditAction(action string) string {
	if strings.HasPrefix(action, "ldap/") {
		return action
	}
	return "ldap/" + action
}

// auditResultForStore 把内部 result（success|blocked|error）折算为
// audit.jsonl 的 ok|denied|error（M0 §4 格式，对齐 ssh-sftp）。
func auditResultForStore(result string) string {
	switch result {
	case "success":
		return "ok"
	case "blocked":
		return "denied"
	default:
		return result
	}
}

// decodeParams 解析领域方法参数并校验必填 connectionId
// （ldap/connections/statuses 为全局视图，不经此函数）。
func decodeParams(params json.RawMessage, out any) *dbxpluginsdk.PluginError {
	if err := json.Unmarshal(params, out); err != nil {
		return invalidParams(err)
	}
	var holder struct {
		ConnectionID string `json:"connectionId"`
	}
	if err := json.Unmarshal(params, &holder); err == nil && holder.ConnectionID == "" {
		return dbxpluginsdk.NewError(-32602, "Missing connectionId")
	}
	return nil
}

func invalidParams(err error) *dbxpluginsdk.PluginError {
	return dbxpluginsdk.NewError(-32602, err.Error())
}

// bizError 业务错误统一 -32000（对齐 ssh-sftp to_plugin_error）。
func bizError(err error) *dbxpluginsdk.PluginError {
	return dbxpluginsdk.NewError(-32000, err.Error())
}

// getContext 返回请求级 context（jsonl SDK 无 ctx 传递，超时由
// ldapconn.contextWithTimeout 按连接配置控制）。
func getContext() context.Context {
	return context.Background()
}

// presetStore 把 store.Store 适配为 ldapconn.PresetStore（presets.json 全量
// 读写；预设明文、不含凭据）。
type presetStore struct {
	st *store.Store
}

func newPresetStore(st *store.Store) ldapconn.PresetStore {
	if st == nil {
		return nil
	}
	return &presetStore{st: st}
}

func (p *presetStore) LoadPresets() ([]ldapconn.LDAPSearchPreset, error) {
	var doc struct {
		Presets []ldapconn.LDAPSearchPreset `json:"presets"`
	}
	exists, err := p.st.LoadJSON("presets.json", &doc)
	if err != nil {
		return nil, err
	}
	if !exists || doc.Presets == nil {
		return []ldapconn.LDAPSearchPreset{}, nil
	}
	return doc.Presets, nil
}

func (p *presetStore) SavePresets(presets []ldapconn.LDAPSearchPreset) error {
	if presets == nil {
		presets = []ldapconn.LDAPSearchPreset{}
	}
	return p.st.SaveJSON("presets.json", map[string]any{"presets": presets})
}
