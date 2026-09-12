package mcp

// server.go：`mcp/call` 分派 + `mcp/settings/get|set` + UI intent 等待
//（设计 §1/§2/§4）。main.go 只做方法表转发；本文件持有全部 MCP 状态：
// settings（持久化）、intent 状态表、cursor 会话、confirmToken 表。

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"io.dbx.ldap.plugin/internal/ldapconn"
	"io.dbx.ldap.plugin/internal/store"
)

// Server MCP 工具面状态与分派。并发安全（SDK 每请求一个 goroutine）。
type Server struct {
	svc      *ldapconn.Service
	st       *store.Store // nil = 数据目录不可用，设置不持久化
	intents  *IntentStore
	cursors  *CursorStore
	confirms *ConfirmStore

	mu       sync.Mutex
	settings Settings

	// emit 把 ldap/ui/intent 事件交还 main.go 的当前 Emitter（nil 时跳过）。
	emit func(method string, params any)

	now func() time.Time
}

// NewServer 构造 MCP Server（settings 从数据目录加载，损坏回落默认）。
func NewServer(svc *ldapconn.Service, st *store.Store) *Server {
	return &Server{
		svc:      svc,
		st:       st,
		intents:  NewIntentStore(0, 0),
		cursors:  NewCursorStore(0, 0, 0),
		confirms: NewConfirmStore(),
		settings: LoadSettings(st),
		now:      time.Now,
	}
}

// SetEmitter 注入事件回调（main.go 持锁转发到当前 Emitter）。
func (s *Server) SetEmitter(emit func(method string, params any)) {
	s.emit = emit
}

// SettingsGet 返回 `mcp/settings/get` 响应（当前生效值）。
func (s *Server) SettingsGet() map[string]any {
	s.mu.Lock()
	settings := s.settings
	s.mu.Unlock()
	return map[string]any{
		"settings":           settings,
		"responseLimitBytes": settings.ResponseLimitBytes,
	}
}

// SettingsSet 处理 `mcp/settings/set`：白名单部分更新 + 持久化。
func (s *Server) SettingsSet(updates map[string]any) (map[string]any, error) {
	if updates == nil {
		return nil, errors.New("updates object is required")
	}
	s.mu.Lock()
	settings, err := applySettingsUpdate(s.settings, updates)
	if err == nil {
		s.settings = settings
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := SaveSettings(s.st, settings); err != nil {
		return nil, fmt.Errorf("persist mcp settings: %w", err)
	}
	return s.SettingsGet(), nil
}

// ReportUIState 处理 `ldap/ui/state/report`（前端回调）：
// 带 intentId = intent 回报（applied/rejected + summary）；
// 无 intentId = 快照型 report（sidecar 缓存最新快照）。
func (s *Server) ReportUIState(params map[string]any) error {
	intentID := strings.TrimSpace(stringField(params, "intentId"))
	summary, _ := params["summary"].(map[string]any)
	if summary == nil {
		summary = map[string]any{}
	}
	state := strings.TrimSpace(stringField(params, "status"))
	if intentID == "" {
		s.intents.SetSnapshot(summary)
		return nil
	}
	var intentState IntentState
	switch state {
	case "applied":
		intentState = IntentApplied
	case "rejected":
		intentState = IntentRejected
	default:
		return fmt.Errorf("status must be applied or rejected")
	}
	if !s.intents.Report(intentID, intentState, summary, strings.TrimSpace(stringField(params, "reason")), s.now()) {
		return fmt.Errorf("intent %q is unknown or expired", intentID)
	}
	return nil
}

// Call 处理 `mcp/call`：工具分派 + 16 KiB 响应上限（超出截断置 truncated）。
func (s *Server) Call(tool string, arguments map[string]any) (map[string]any, error) {
	if arguments == nil {
		arguments = map[string]any{}
	}
	var (
		result map[string]any
		err    error
	)
	switch tool {
	case "ldap_ui_search":
		result, err = s.uiSearch(arguments)
	case "ldap_ui_focus":
		result, err = s.uiFocus(arguments)
	case "ldap_ui_select":
		result, err = s.uiSelect(arguments)
	case "ldap_ui_state":
		result, err = s.uiState(arguments)
	case "ldap_ui_schema":
		result, err = s.uiSchema(arguments)
	case "ldap_search_digest":
		result, err = s.searchDigest(arguments)
	case "ldap_cursor_next":
		result, err = s.cursorNext(arguments)
	case "ldap_entry_write":
		result, err = s.entryWrite(arguments)
	default:
		return nil, fmt.Errorf("unknown tool: %s", tool)
	}
	if err != nil {
		return nil, err
	}
	return s.enforceResponseLimit(result), nil
}

// --- UI intent 工具（设计 §1） ---

// uiSearch / uiFocus / uiSelect 共用 intent 发起流程：
// 生成 intentId → 状态表登记 pending → 发 `ldap/ui/intent` 事件 → 等
// report（ReportWaitMs）→ 返回 {intentId, state, summary}。
func (s *Server) uiSearch(args map[string]any) (map[string]any, error) {
	filter := strings.TrimSpace(stringField(args, "filter"))
	if filter == "" {
		return nil, errors.New("filter is required")
	}
	params := map[string]any{
		"baseDn":       strings.TrimSpace(stringField(args, "baseDn")),
		"filter":       filter,
		"scope":        strings.TrimSpace(stringField(args, "scope")),
		"sizeLimit":    args["sizeLimit"],
		"connectionId": strings.TrimSpace(stringField(args, "connectionId")),
	}
	if attributes, ok := args["attributes"].([]any); ok {
		names := make([]string, 0, len(attributes))
		for _, raw := range attributes {
			if name := strings.TrimSpace(fmt.Sprintf("%v", raw)); name != "" {
				names = append(names, name)
			}
		}
		params["attributes"] = names
	}
	return s.runIntent("search", params), nil
}

func (s *Server) uiFocus(args map[string]any) (map[string]any, error) {
	panel := strings.TrimSpace(stringField(args, "panel"))
	if panel == "" {
		return nil, errors.New("panel is required (search | tree | schema)")
	}
	return s.runIntent("focus", map[string]any{
		"panel":        panel,
		"connectionId": strings.TrimSpace(stringField(args, "connectionId")),
	}), nil
}

func (s *Server) uiSelect(args map[string]any) (map[string]any, error) {
	dn := strings.TrimSpace(stringField(args, "dn"))
	if dn == "" {
		return nil, errors.New("dn is required")
	}
	return s.runIntent("select", map[string]any{
		"dn":           dn,
		"connectionId": strings.TrimSpace(stringField(args, "connectionId")),
	}), nil
}

// runIntent intent 发起 + 等待 report。
func (s *Server) runIntent(action string, params map[string]any) map[string]any {
	intentID := "i-" + randomHex(8)
	s.intents.Register(intentID, action, params, s.now())
	if s.emit != nil {
		s.emit("ldap/ui/intent", map[string]any{
			"intentId": intentID,
			"action":   action,
			"params":   params,
		})
	}
	return s.waitIntent(intentID)
}

// waitIntent 轮询 intent 状态表直到终态或超时（默认 5s，settings 可调）。
func (s *Server) waitIntent(intentID string) map[string]any {
	s.mu.Lock()
	wait := time.Duration(s.settings.ReportWaitMs) * time.Millisecond
	s.mu.Unlock()
	deadline := s.now().Add(wait)
	for {
		time.Sleep(25 * time.Millisecond)
		intent, status := s.intents.Get(intentID, s.now())
		if status == LookupExpired {
			return map[string]any{"intentId": intentID, "state": "expired"}
		}
		if status == LookupFound && intent.State != IntentPending {
			out := map[string]any{"intentId": intentID, "state": string(intent.State)}
			if intent.Summary != nil {
				out["summary"] = intent.Summary
			}
			if intent.Reason != "" {
				out["reason"] = intent.Reason
			}
			return out
		}
		if !s.now().Before(deadline) {
			return map[string]any{
				"intentId": intentID,
				"state":    "pending",
				"hint":     "workbench not open or frontend did not respond; fall back to ldap_search_digest (re-check later with ldap_ui_state)",
			}
		}
	}
}

// uiState 读 intent 结果（带 intentId）或最新 UI 快照（不带）。
func (s *Server) uiState(args map[string]any) (map[string]any, error) {
	intentID := strings.TrimSpace(stringField(args, "intentId"))
	if intentID == "" {
		return map[string]any{"snapshot": s.intents.Snapshot()}, nil
	}
	intent, status := s.intents.Get(intentID, s.now())
	switch status {
	case LookupFound:
		out := map[string]any{"intentId": intentID, "state": string(intent.State)}
		if intent.Summary != nil {
			out["summary"] = intent.Summary
		}
		if intent.Reason != "" {
			out["reason"] = intent.Reason
		}
		return out, nil
	case LookupExpired:
		return map[string]any{"intentId": intentID, "state": "expired"}, nil
	default:
		return nil, fmt.Errorf("unknown intentId: %s", intentID)
	}
}

// --- 元发现 ---

// uiSchema 工具 `ldap_ui_schema`：schema 缓存的 objectClass/attributeType
// 名称清单（帮 AI 构造合法 filter；名称截到 300 个防响应膨胀）。
func (s *Server) uiSchema(args map[string]any) (map[string]any, error) {
	connectionID := strings.TrimSpace(stringField(args, "connectionId"))
	if connectionID == "" {
		return nil, errors.New("connectionId is required")
	}
	metadata, err := s.svc.SchemaMetadata(getContext(), ldapconn.LDAPSchemaMetadataRequest{ConnectionID: connectionID})
	if err != nil {
		return nil, err
	}
	const nameLimit = 300
	attributeNames := clampStrings(metadata.AttributeNames, nameLimit)
	classNames := make([]string, 0, len(metadata.ObjectClassAttributes))
	for name := range metadata.ObjectClassAttributes {
		classNames = append(classNames, name)
	}
	sortStrings(classNames)
	classNames = clampStrings(classNames, nameLimit)
	return map[string]any{
		"attributeNames":          attributeNames,
		"attributeNamesTruncated": len(metadata.AttributeNames) > nameLimit,
		"objectClassNames":        classNames,
		"objectClassTruncated":    len(metadata.ObjectClassAttributes) > nameLimit,
	}, nil
}

// --- 本地读（设计 §3） ---

// digestScanLimit 未显式给 sizeLimit 时的扫描上限（本地聚合在 sidecar 完成，
// 上限只约束远端扫描量）。
const digestScanLimit = 1000

// searchDigest 工具 `ldap_search_digest`：filter 服务端执行 + 本地聚合 +
// 物化 cursor。
func (s *Server) searchDigest(args map[string]any) (map[string]any, error) {
	connectionID := strings.TrimSpace(stringField(args, "connectionId"))
	if connectionID == "" {
		return nil, errors.New("connectionId is required")
	}
	filter := strings.TrimSpace(stringField(args, "filter"))
	if filter == "" {
		filter = "(objectClass=*)"
	}
	requested := stringSlice(args["attributes"])
	projected := normalizedAttrNames(requested)
	// objectClass 供本地聚合； projected 保持"显式请求的投影"。
	searchAttrs := append([]string{}, projected...)
	if !containsFold(searchAttrs, "objectClass") {
		searchAttrs = append(searchAttrs, "objectClass")
	}

	sizeLimit := intArg(args["sizeLimit"])
	if sizeLimit <= 0 {
		sizeLimit = digestScanLimit
	}
	result, err := s.svc.Search(getContext(), ldapconn.LDAPSearchRequest{
		ConnectionID: connectionID,
		BaseDN:       strings.TrimSpace(stringField(args, "baseDn")),
		Filter:       filter,
		Scope:        strings.TrimSpace(stringField(args, "scope")),
		Attributes:   searchAttrs,
		SizeLimit:    sizeLimit,
		PageSize:     500,
	})
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	settings := s.settings
	s.mu.Unlock()

	// 物化 cursor：DN + 投影属性（DN 不截断，属性值过截断宽度）。
	rows := make([]CursorRow, 0, len(result.Entries))
	for _, entry := range result.Entries {
		rows = append(rows, CursorRow{DN: entry.DN, Attributes: projectCursorAttributes(entry, projected, settings.CellWidth)})
	}
	session := s.cursors.Put(rows, result.BaseDN, result.Filter, s.now())

	baseDigest := map[string]any{
		"matched":         result.Count,
		"truncated":       result.Truncated,
		"baseDn":          result.BaseDN,
		"filter":          result.Filter,
		"cursorId":        session.ID,
		"cursorTruncated": session.Truncated,
	}
	if strings.EqualFold(strings.TrimSpace(stringField(args, "format")), "rows") {
		rowLimit := settings.DigestRowLimit
		outRows := make([]map[string]any, 0, rowLimit)
		for index, entry := range result.Entries {
			if index >= rowLimit {
				break
			}
			outRows = append(outRows, ProjectEntry(entry, settings.CellWidth))
		}
		baseDigest["rows"] = outRows
		return baseDigest, nil
	}

	aggregated := AggregateDigest(DigestInput{
		Entries:      result.Entries,
		BaseDN:       result.BaseDN,
		Filter:       result.Filter,
		DistinctAttr: strings.TrimSpace(stringField(args, "distinctAttr")),
		Width:        settings.CellWidth,
		GroupLimit:   settings.DigestGroupLimit,
		TopN:         settings.DigestTopN,
		SampleRows:   settings.DigestSampleRows,
	})
	baseDigest["scanned"] = aggregated.Matched
	baseDigest["stats"] = aggregated.Stats
	baseDigest["sample"] = aggregated.Sample
	return baseDigest, nil
}

// cursorNext 工具 `ldap_cursor_next`：分批取定位字段行（n ≤20/批）。
func (s *Server) cursorNext(args map[string]any) (map[string]any, error) {
	cursorID := strings.TrimSpace(stringField(args, "cursorId"))
	if cursorID == "" {
		return nil, errors.New("cursorId is required")
	}
	result, status := s.cursors.Next(cursorID, NextRequest{N: intArg(args["n"]), Offset: offsetArg(args)}, s.now())
	switch status {
	case LookupExpired:
		return nil, errors.New("cursor expired (10 minutes); re-run ldap_search_digest")
	case LookupUnknown:
		return nil, fmt.Errorf("unknown cursorId: %s", cursorID)
	}
	return map[string]any{
		"rows":       result.Rows,
		"offset":     result.Offset,
		"nextOffset": result.NextOffset,
		"done":       result.Done,
	}, nil
}

// --- 写（设计 §4 两阶段） ---

// writeRequest 两阶段 hash 绑定的 canonical 形状（json.Marshal 结构体输出
// 确定，hash 稳定）。
type writeRequest struct {
	Action       string                      `json:"action"`
	DN           string                      `json:"dn"`
	Recursive    bool                        `json:"recursive,omitempty"`
	Attributes   map[string][]string         `json:"attributes,omitempty"`
	Changes      []ldapconn.LDAPModifyChange `json:"changes,omitempty"`
	NewRDN       string                      `json:"newRdn,omitempty"`
	DeleteOldRDN bool                        `json:"deleteOldRdn,omitempty"`
	NewSuperior  string                      `json:"newSuperior,omitempty"`
}

const sourceMCP = "mcp"

// entryWrite 工具 `ldap_entry_write`：add/modify 单阶段直执行；delete（含
// recursive）与 modifyDn 强制两阶段（preview + confirmToken）。
func (s *Server) entryWrite(args map[string]any) (map[string]any, error) {
	connectionID := strings.TrimSpace(stringField(args, "connectionId"))
	dn := strings.TrimSpace(stringField(args, "dn"))
	action := strings.ToLower(strings.TrimSpace(stringField(args, "action")))
	if connectionID == "" {
		return nil, errors.New("connectionId is required")
	}
	if dn == "" {
		return nil, errors.New("dn is required")
	}
	profile, err := s.svc.Get(connectionID)
	if err != nil {
		return nil, err
	}
	if profile.ReadOnly {
		return nil, fmt.Errorf("connection %q is read-only; write tools are refused", profile.Name)
	}

	req := writeRequest{Action: action, DN: dn}
	switch action {
	case "add":
		attributes, err := attributeMap(args["attributes"])
		if err != nil {
			return nil, err
		}
		req.Attributes = attributes
		if err := s.svc.AddEntry(getContext(), ldapconn.LDAPAddEntryRequest{
			ConnectionID: connectionID, DN: dn, Attributes: attributes, Source: sourceMCP,
		}); err != nil {
			return nil, err
		}
		return map[string]any{"success": true, "action": action, "dn": dn}, nil

	case "modify":
		changes, err := changeList(args["changes"])
		if err != nil {
			return nil, err
		}
		req.Changes = changes
		if err := s.svc.ModifyEntry(getContext(), ldapconn.LDAPModifyEntryRequest{
			ConnectionID: connectionID, DN: dn, Changes: changes, Source: sourceMCP,
		}); err != nil {
			return nil, err
		}
		return map[string]any{"success": true, "action": action, "dn": dn}, nil

	case "delete":
		req.Recursive = boolArg(args["recursive"])
		return s.twoPhaseWrite(connectionID, req, args, func() error {
			return s.svc.DeleteEntry(getContext(), ldapconn.LDAPDeleteEntryRequest{
				ConnectionID: connectionID, DN: dn, Recursive: req.Recursive, Source: sourceMCP,
			})
		}, nil)

	case "modifydn":
		req.NewRDN = strings.TrimSpace(stringField(args, "newRdn"))
		req.DeleteOldRDN = boolArg(args["deleteOldRdn"])
		req.NewSuperior = strings.TrimSpace(stringField(args, "newSuperior"))
		if req.NewRDN == "" {
			return nil, errors.New("newRdn is required for modifyDn")
		}
		destination, err := ldapconn.ModifyDNDestinationDN(dn, req.NewRDN, req.NewSuperior)
		if err != nil {
			return nil, err
		}
		return s.twoPhaseWrite(connectionID, req, args, func() error {
			return s.svc.ModifyDN(getContext(), ldapconn.LDAPModifyDNRequest{
				ConnectionID: connectionID, DN: dn, NewRDN: req.NewRDN,
				DeleteOldRDN: req.DeleteOldRDN, NewSuperior: req.NewSuperior, Source: sourceMCP,
			})
		}, map[string]any{"destinationDn": destination})

	default:
		return nil, errors.New("action must be add, modify, delete, or modifyDn")
	}
}

// twoPhaseWrite delete/modifyDn 的两阶段：
// 无 confirmToken → preview + 一次性令牌（60s TTL，hash 绑定，不执行写）；
// 带 token → Consume（一次性 + hash 一致）→ 执行。
func (s *Server) twoPhaseWrite(connectionID string, req writeRequest, args map[string]any, execute func() error, previewExtra map[string]any) (map[string]any, error) {
	canonical, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	paramHash := HashParams(canonical)
	token := strings.TrimSpace(stringField(args, "confirmToken"))
	if token == "" {
		preview := map[string]any{"action": req.Action, "dn": req.DN}
		for key, value := range previewExtra {
			preview[key] = value
		}
		if req.Action == "delete" {
			// 子树清点尽力而为：计数只用于预览，失败不阻断签发。
			if count, err := s.svc.ChildrenCount(getContext(), ldapconn.LDAPChildrenCountRequest{ConnectionID: connectionID, DN: req.DN}); err == nil {
				preview["childCount"] = count.Count
				if count.Truncated {
					preview["childCountTruncated"] = true
				}
			}
			preview["recursive"] = req.Recursive
		}
		issued, expiresAt := s.confirms.Issue(paramHash, s.now())
		return map[string]any{
			"preview":      preview,
			"confirmToken": issued,
			"expiresAt":    expiresAt.UTC().Format(time.RFC3339),
			"note":         "nothing written yet; repeat the same arguments with confirmToken to execute",
		}, nil
	}
	switch result := s.confirms.Consume(token, paramHash, s.now()); result {
	case ConfirmOK:
		if err := execute(); err != nil {
			return nil, err
		}
		out := map[string]any{"success": true, "action": req.Action, "dn": req.DN}
		if req.Action == "delete" && req.Recursive {
			// 递归删除的聚合审计在 svc 内发出；响应补条数没有单独回传通道，
			// 保持 {success, action, dn} 形状即可。
		}
		return out, nil
	case ConfirmExpired:
		return nil, errors.New("confirmToken expired (60s); request a new preview")
	case ConfirmHashMismatch:
		return nil, errors.New("arguments changed since the preview; request a new confirmToken")
	default:
		return nil, errors.New("confirmToken unknown or already used; request a new preview")
	}
}

// --- 响应上限（设计 §3：单工具响应 16 KiB） ---

// enforceResponseLimit 超限时按 sample → rows → stats 顺序丢弃重字段并置
// truncated；仍超限返回占位（指引调小请求或调大 responseLimitBytes）。
func (s *Server) enforceResponseLimit(result map[string]any) map[string]any {
	s.mu.Lock()
	limit := s.settings.ResponseLimitBytes
	s.mu.Unlock()
	if payloadSize(result) <= limit {
		return result
	}
	trimmed := cloneMap(result)
	for _, key := range []string{"sample", "rows", "stats"} {
		delete(trimmed, key)
		trimmed["truncated"] = true
		if payloadSize(trimmed) <= limit {
			return trimmed
		}
	}
	return map[string]any{
		"truncated": true,
		"note":      "response exceeded the configured size limit; narrow the request or raise responseLimitBytes via mcp/settings/set",
	}
}

func payloadSize(value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return len(data)
}

func cloneMap(source map[string]any) map[string]any {
	out := make(map[string]any, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
