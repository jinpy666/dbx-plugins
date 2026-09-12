package mcp

// server.go：`mcp/call` 分派 + `mcp/settings/get|set` + UI intent 等待
//（设计 §1/§2/§4/§6.3）。main.go 只做方法表转发；本文件持有全部 MCP 状态：
// settings（持久化）、intent 状态表、cursor 会话、confirmToken 表。
// MCP 不订阅 stream（stream 由用户在 UI 操作）；digest 走一次性 Consume。

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"io.dbx.kafka.plugin/internal/kafkaconn"
	"io.dbx.kafka.plugin/internal/store"
)

// Server MCP 工具面状态与分派。并发安全（SDK 每请求一个 goroutine）。
type Server struct {
	svc      *kafkaconn.Service
	st       *store.Store // nil = 数据目录不可用，设置不持久化
	intents  *IntentStore
	cursors  *CursorStore
	confirms *ConfirmStore

	mu       sync.Mutex
	settings Settings

	// emit 把 kafka/ui/intent 事件交还 main.go 的当前 Emitter（nil 时跳过）。
	emit func(method string, params any)

	now func() time.Time
}

// NewServer 构造 MCP Server（settings 从数据目录加载，损坏回落默认）。
func NewServer(svc *kafkaconn.Service, st *store.Store) *Server {
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

// ReportUIState 处理 `kafka/ui/state/report`（前端回调）：
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
	case "kafka_ui_search":
		result, err = s.uiSearch(arguments)
	case "kafka_ui_focus":
		result, err = s.uiFocus(arguments)
	case "kafka_ui_select":
		result, err = s.uiSelect(arguments)
	case "kafka_ui_state":
		result, err = s.uiState(arguments)
	case "kafka_ui_topics":
		result, err = s.uiTopics(arguments)
	case "kafka_messages_digest":
		result, err = s.messagesDigest(arguments)
	case "kafka_cursor_next":
		result, err = s.cursorNext(arguments)
	case "kafka_messages_produce":
		result, err = s.messagesProduce(arguments)
	case "kafka_topics_delete":
		result, err = s.topicsDelete(arguments)
	case "kafka_groups_offsets_reset":
		result, err = s.groupsOffsetsReset(arguments)
	case "kafka_topics_records_clear":
		result, err = s.topicsRecordsClear(arguments)
	default:
		return nil, fmt.Errorf("unknown tool: %s", tool)
	}
	if err != nil {
		return nil, err
	}
	return s.enforceResponseLimit(result), nil
}

// --- UI intent 工具（设计 §1/§6.3） ---

// uiSearch / uiFocus / uiSelect 共用 intent 发起流程：
// 生成 intentId → 状态表登记 pending → 发 `kafka/ui/intent` 事件 → 等
// report（ReportWaitMs）→ 返回 {intentId, state, summary}。
func (s *Server) uiSearch(args map[string]any) (map[string]any, error) {
	topic := strings.TrimSpace(stringField(args, "topic"))
	if topic == "" {
		return nil, errors.New("topic is required")
	}
	params := map[string]any{
		"topic":          topic,
		"connectionId":   strings.TrimSpace(stringField(args, "connectionId")),
		"offsetStrategy": strings.TrimSpace(stringField(args, "offsetStrategy")),
		"limit":          args["limit"],
		"filter":         strings.TrimSpace(stringField(args, "filter")),
		"keyFilter":      strings.TrimSpace(stringField(args, "keyFilter")),
		"valueFilter":    strings.TrimSpace(stringField(args, "valueFilter")),
		"headerFilter":   strings.TrimSpace(stringField(args, "headerFilter")),
		"matchMode":      strings.TrimSpace(stringField(args, "matchMode")),
		"groupId":        strings.TrimSpace(stringField(args, "groupId")),
		"partitions":     stringSlice(args["partitions"]),
		"offsetTime":     strings.TrimSpace(stringField(args, "offsetTime")),
	}
	return s.runIntent("search", params), nil
}

func (s *Server) uiFocus(args map[string]any) (map[string]any, error) {
	panel := strings.TrimSpace(stringField(args, "panel"))
	if panel == "" {
		return nil, errors.New("panel is required (messages | topics | groups | schemas)")
	}
	return s.runIntent("focus", map[string]any{
		"panel":        panel,
		"connectionId": strings.TrimSpace(stringField(args, "connectionId")),
	}), nil
}

func (s *Server) uiSelect(args map[string]any) (map[string]any, error) {
	partition, offset, err := locatorOf(args)
	if err != nil {
		return nil, err
	}
	return s.runIntent("select", map[string]any{
		"topic":        strings.TrimSpace(stringField(args, "topic")),
		"partition":    partition,
		"offset":       offset,
		"connectionId": strings.TrimSpace(stringField(args, "connectionId")),
	}), nil
}

// locatorOf 解析 partition+offset 定位参数（整数必填且非负）。
func locatorOf(args map[string]any) (int, int, error) {
	partition := intArg(args["partition"])
	offset := intArg(args["offset"])
	if _, present := args["partition"]; !present || partition < 0 {
		return 0, 0, errors.New("partition is required (non-negative integer)")
	}
	if _, present := args["offset"]; !present || offset < 0 {
		return 0, 0, errors.New("offset is required (non-negative integer)")
	}
	return partition, offset, nil
}

// runIntent intent 发起 + 等待 report。
func (s *Server) runIntent(action string, params map[string]any) map[string]any {
	intentID := "i-" + randomHex(8)
	s.intents.Register(intentID, action, params, s.now())
	if s.emit != nil {
		s.emit("kafka/ui/intent", map[string]any{
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
				"hint":     "workbench not open or frontend did not respond; fall back to kafka_messages_digest (re-check later with kafka_ui_state)",
			}
		}
	}
}

// uiState 读 intent 结果（带 intentId）或最新 UI 快照（不带）；
// kafka 域内扩展：快照响应附带 stream 会话状态（设计 §6.3）。
func (s *Server) uiState(args map[string]any) (map[string]any, error) {
	intentID := strings.TrimSpace(stringField(args, "intentId"))
	if intentID == "" {
		out := map[string]any{"snapshot": s.intents.Snapshot()}
		if connectionID := strings.TrimSpace(stringField(args, "connectionId")); connectionID != "" {
			out["streams"] = s.svc.Streams.StatusesFor(connectionID)
		}
		return out, nil
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

// uiTopicsLimit `kafka_ui_topics` 的硬上限（设计 §2：limit 硬上限 50，纯定位用）。
const uiTopicsLimit = 50

// uiTopics 工具 `kafka_ui_topics`：topic 名清单（limit 硬上限 50；帮 AI
// 在 digest/ui_search 前定位真实 topic 名）。
func (s *Server) uiTopics(args map[string]any) (map[string]any, error) {
	connectionID := strings.TrimSpace(stringField(args, "connectionId"))
	if connectionID == "" {
		return nil, errors.New("connectionId is required")
	}
	result, err := s.svc.ListTopics(getContext(), kafkaconn.TopicsListRequest{ConnectionID: connectionID})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(result.Topics))
	for _, topic := range result.Topics {
		names = append(names, topic.Name)
	}
	sortStrings(names)
	truncated := len(names) > uiTopicsLimit
	names = clampStrings(names, uiTopicsLimit)
	return map[string]any{
		"topics":    names,
		"count":     len(names),
		"truncated": truncated,
		"limit":     uiTopicsLimit,
	}, nil
}

// --- 本地读（设计 §3/§6.3） ---

// messagesDigest 工具 `kafka_messages_digest`：复用 consume 的
// maxScanRecords 扫描语义与 filter 各通道，sidecar 本地聚合 + 物化 cursor。
func (s *Server) messagesDigest(args map[string]any) (map[string]any, error) {
	connectionID := strings.TrimSpace(stringField(args, "connectionId"))
	if connectionID == "" {
		return nil, errors.New("connectionId is required")
	}
	topic := strings.TrimSpace(stringField(args, "topic"))
	if topic == "" {
		return nil, errors.New("topic is required")
	}
	format := strings.ToLower(strings.TrimSpace(stringField(args, "format")))
	if format == "" {
		format = "digest"
	}
	if format != "digest" && format != "rows" {
		return nil, errors.New("format must be digest or rows")
	}

	s.mu.Lock()
	settings := s.settings
	s.mu.Unlock()

	// 扫描语义缺省 earliest（digest 面向存量数据；显式 offsetStrategy 覆盖）。
	offsetStrategy := strings.TrimSpace(stringField(args, "offsetStrategy"))
	if offsetStrategy == "" {
		offsetStrategy = "earliest"
	}
	params := kafkaconn.ConsumeParams{
		ConnectionID:   connectionID,
		Topic:          topic,
		OffsetStrategy: offsetStrategy,
		MaxScanRecords: digestScanLimit(args, settings.DigestScanLimit),
		Filter:         strings.TrimSpace(stringField(args, "filter")),
		KeyFilter:      strings.TrimSpace(stringField(args, "keyFilter")),
		ValueFilter:    strings.TrimSpace(stringField(args, "valueFilter")),
		HeaderFilter:   strings.TrimSpace(stringField(args, "headerFilter")),
		MatchMode:      strings.TrimSpace(stringField(args, "matchMode")),
		IsolationLevel: strings.TrimSpace(stringField(args, "isolationLevel")),
		Decode:         strings.TrimSpace(stringField(args, "decode")),
		Decompression:  strings.TrimSpace(stringField(args, "decompression")),
	}
	if partitions := intSlice(args["partitions"]); len(partitions) > 0 {
		params.Partitions = partitions
	}
	if groupId := strings.TrimSpace(stringField(args, "groupId")); groupId != "" {
		params.GroupID = groupId
	}
	if offsetTime := strings.TrimSpace(stringField(args, "offsetTime")); offsetTime != "" {
		params.OffsetTime = offsetTime
	}
	if timestampFrom, ok := optionalInt64(args["timestampFrom"]); ok {
		params.TimestampFrom = &timestampFrom
	}
	if timestampTo, ok := optionalInt64(args["timestampTo"]); ok {
		params.TimestampTo = &timestampTo
	}
	if offsetFrom, ok := optionalInt64(args["offsetFrom"]); ok {
		params.OffsetFrom = &offsetFrom
	}
	if offsetTo, ok := optionalInt64(args["offsetTo"]); ok {
		params.OffsetTo = &offsetTo
	}
	if fieldFilters, err := parseFieldFilters(args["fieldFilters"]); err != nil {
		return nil, err
	} else if len(fieldFilters) > 0 {
		params.FieldFilters = fieldFilters
	}
	// 保留上限 = 扫描上限：命中消息全量留存做本地聚合（仍不出 sidecar）。
	params.Limit = params.MaxScanRecords
	result, err := s.svc.Consume(getContext(), params)
	if err != nil {
		return nil, err
	}

	fields := stringSlice(args["fields"])
	aggregated := AggregateDigest(DigestInput{
		Messages:   result.Messages,
		Topic:      topic,
		Fields:     fields,
		Width:      settings.CellWidth,
		GroupLimit: settings.DigestGroupLimit,
		TopN:       settings.DigestTopN,
		SampleRows: settings.DigestSampleRows,
	})
	session := s.cursors.Put(aggregated.Rows, topic, s.now())

	payload := map[string]any{
		"connectionId":    connectionID,
		"topic":           topic,
		"matched":         aggregated.Matched,
		"scanned":         result.Scanned,
		"scanTruncated":   result.HasMore,
		"cursorId":        session.ID,
		"cursorTruncated": session.Truncated,
	}
	if format == "rows" {
		rowLimit := settings.DigestRowLimit
		if rowLimit > len(aggregated.Rows) {
			rowLimit = len(aggregated.Rows)
		}
		payload["rows"] = aggregated.Rows[:rowLimit]
		return payload, nil
	}
	payload["stats"] = aggregated.Stats
	payload["sample"] = aggregated.Sample
	return payload, nil
}

// digestScanLimit 扫描上限：显式 maxScanRecords（clamp ≤100000）优先，
// 否则用 settings.DigestScanLimit。
func digestScanLimit(args map[string]any, fallback int) int {
	limit := intArg(args["maxScanRecords"])
	if limit <= 0 {
		limit = fallback
	}
	if limit <= 0 {
		limit = 1000
	}
	if limit > 100000 {
		limit = 100000
	}
	return limit
}

// cursorNext 工具 `kafka_cursor_next`：分批取定位字段行（n ≤20/批）。
func (s *Server) cursorNext(args map[string]any) (map[string]any, error) {
	cursorID := strings.TrimSpace(stringField(args, "cursorId"))
	if cursorID == "" {
		return nil, errors.New("cursorId is required")
	}
	result, status := s.cursors.Next(cursorID, NextRequest{N: intArg(args["n"]), Offset: offsetArg(args)}, s.now())
	switch status {
	case LookupExpired:
		return nil, errors.New("cursor expired (10 minutes); re-run kafka_messages_digest")
	case LookupUnknown:
		return nil, fmt.Errorf("unknown cursorId: %s", cursorID)
	}
	rows := make([]map[string]any, 0, len(result.Rows))
	for _, row := range result.Rows {
		out := map[string]any{
			"topic":     row.Topic,
			"partition": row.Partition,
			"offset":    row.Offset,
		}
		if row.Key != "" {
			out["key"] = row.Key
		}
		if row.Fields != nil {
			out["fields"] = row.Fields
		}
		rows = append(rows, out)
	}
	return map[string]any{
		"rows":       rows,
		"offset":     result.Offset,
		"nextOffset": result.NextOffset,
		"done":       result.Done,
	}, nil
}

// --- 写（设计 §4 两阶段） ---

const sourceMCP = "mcp"

// produceArgs 两阶段 hash 绑定的 canonical 形状（produce 单阶段也走结构体
// 归一，形状对齐）。
type produceArgs struct {
	ConnectionID string               `json:"connectionId"`
	Topic        string               `json:"topic"`
	Key          string               `json:"key,omitempty"`
	Value        string               `json:"value,omitempty"`
	KeyBase64    string               `json:"keyBase64,omitempty"`
	ValueBase64  string               `json:"valueBase64,omitempty"`
	Headers      map[string]string    `json:"headers,omitempty"`
	Partition    *int32               `json:"partition,omitempty"`
	Compression  string               `json:"compression,omitempty"`
	Schema       *kafkaconn.SchemaRef `json:"schema,omitempty"`
}

// messagesProduce 工具 `kafka_messages_produce`：单条小消息单阶段直执行
// （设计 §4：非破坏、可逆）；照常过连接只读门并审计 source:"mcp"。
// value/valueBase64 合计 ≤64 KiB（MCP 侧建议上限，超出提示走工作台）。
func (s *Server) messagesProduce(args map[string]any) (map[string]any, error) {
	connectionID := strings.TrimSpace(stringField(args, "connectionId"))
	topic := strings.TrimSpace(stringField(args, "topic"))
	if connectionID == "" {
		return nil, errors.New("connectionId is required")
	}
	if topic == "" {
		return nil, errors.New("topic is required")
	}
	if err := s.ensureWritable(connectionID); err != nil {
		return nil, err
	}
	req := produceArgs{ConnectionID: connectionID, Topic: topic}
	req.Key = stringField(args, "key")
	req.Value = stringField(args, "value")
	req.KeyBase64 = stringField(args, "keyBase64")
	req.ValueBase64 = stringField(args, "valueBase64")
	req.Compression = stringField(args, "compression")
	if headers, ok := args["headers"].(map[string]any); ok {
		req.Headers = make(map[string]string, len(headers))
		for name, raw := range headers {
			req.Headers[name] = fmt.Sprintf("%v", raw)
		}
	}
	if raw, ok := args["partition"].(float64); ok && raw >= 0 {
		partition := int32(raw)
		req.Partition = &partition
	}
	if schema, ok := args["schema"].(map[string]any); ok {
		subject := stringField(schema, "subject")
		if subject == "" {
			return nil, errors.New("schema.subject is required")
		}
		req.Schema = &kafkaconn.SchemaRef{
			Registry: stringField(schema, "registry"),
			Subject:  subject,
			Version:  int64(intArg(schema["version"])),
			Format:   stringField(schema, "format"),
		}
	}
	if len(req.Value)+len(req.ValueBase64) > mcpProduceMaxBytes {
		return nil, fmt.Errorf("value exceeds the MCP produce budget (%d bytes); use the workbench for large payloads", mcpProduceMaxBytes)
	}

	result, err := s.svc.Produce(getContext(), kafkaconn.ProduceRequest{
		ConnectionID: connectionID,
		Topic:        topic,
		Key:          req.Key,
		Value:        req.Value,
		KeyBase64:    req.KeyBase64,
		ValueBase64:  req.ValueBase64,
		Headers:      req.Headers,
		Partition:    req.Partition,
		Compression:  req.Compression,
		Schema:       req.Schema,
		Source:       sourceMCP,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"success":   true,
		"topic":     result.Topic,
		"partition": result.Partition,
		"offset":    result.Offset,
		"timestamp": result.Timestamp,
	}, nil
}

// mcpProduceMaxBytes MCP 单条消息载荷预算（value 或 valueBase64 解码前
// 合计 ≤64 KiB；大载荷走工作台/生产面板）。
const mcpProduceMaxBytes = 64 * 1024

// deleteArgs 两阶段 hash 绑定的 canonical 形状（结构体输出确定，hash 稳定）。
// ConfirmTopic/ConfirmTopics 由 MCP 侧按 topics 填充，第二阶段执行时仍满足
// kafkaconn 的 confirmTopic 门禁（防误删，纵深防御）。
type deleteArgs struct {
	ConnectionID  string   `json:"connectionId"`
	Topics        []string `json:"topics"`
	ConfirmTopic  string   `json:"confirmTopic,omitempty"`
	ConfirmTopics []string `json:"confirmTopics,omitempty"`
}

// offsetsResetArgs 两阶段 hash 绑定的 canonical 形状。
type offsetsResetArgs struct {
	ConnectionID     string                     `json:"connectionId"`
	Group            string                     `json:"group"`
	Topics           []string                   `json:"topics,omitempty"`
	ResetTo          string                     `json:"resetTo"`
	TimestampMs      int64                      `json:"timestampMs,omitempty"`
	PartitionOffsets map[string]map[int32]int64 `json:"partitionOffsets,omitempty"`
}

// clearArgs 两阶段 hash 绑定的 canonical 形状。
type clearArgs struct {
	ConnectionID string `json:"connectionId"`
	Topic        string `json:"topic"`
}

// topicsDelete 工具 `kafka_topics_delete`：强制两阶段（preview +
// confirmToken；60s TTL、参数 hash 绑定）。confirmTopic 门禁在第二阶段
// 执行时仍走 kafkaconn 原语义（纵深防御）。
func (s *Server) topicsDelete(args map[string]any) (map[string]any, error) {
	connectionID := strings.TrimSpace(stringField(args, "connectionId"))
	topics := stringSlice(args["topics"])
	if connectionID == "" {
		return nil, errors.New("connectionId is required")
	}
	if len(topics) == 0 {
		return nil, errors.New("topics is required")
	}
	if err := s.ensureDeleteAllowed(connectionID, "topics/delete"); err != nil {
		return nil, err
	}
	req := deleteArgs{ConnectionID: connectionID, Topics: topics}
	if len(topics) == 1 {
		req.ConfirmTopic = topics[0]
	} else {
		req.ConfirmTopics = topics
	}
	return s.twoPhase(connectionID, req, args, func() (any, error) {
		results, err := s.svc.DeleteTopics(getContext(), kafkaconn.TopicsDeleteRequest{
			ConnectionID:  connectionID,
			Topics:        topics,
			ConfirmTopic:  req.ConfirmTopic,
			ConfirmTopics: req.ConfirmTopics,
			Source:        sourceMCP,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"success": true, "action": "delete", "results": results}, nil
	})
}

// groupsOffsetsReset 工具 `kafka_groups_offsets_reset`：强制两阶段。
func (s *Server) groupsOffsetsReset(args map[string]any) (map[string]any, error) {
	connectionID := strings.TrimSpace(stringField(args, "connectionId"))
	group := strings.TrimSpace(stringField(args, "group"))
	if connectionID == "" {
		return nil, errors.New("connectionId is required")
	}
	if group == "" {
		return nil, errors.New("group is required")
	}
	if err := s.ensureWritable(connectionID); err != nil {
		return nil, err
	}
	req := offsetsResetArgs{
		ConnectionID: connectionID,
		Group:        group,
		Topics:       stringSlice(args["topics"]),
		ResetTo:      strings.TrimSpace(stringField(args, "resetTo")),
		TimestampMs:  int64(intArg(args["timestampMs"])),
	}
	if req.ResetTo == "" {
		return nil, errors.New("resetTo is required (earliest | latest | timestamp | partitionOffset)")
	}
	if partitionOffsets, err := parsePartitionOffsets(args["partitionOffsets"]); err != nil {
		return nil, err
	} else if partitionOffsets != nil {
		req.PartitionOffsets = partitionOffsets
	}
	return s.twoPhase(connectionID, req, args, func() (any, error) {
		result, err := s.svc.ResetGroupOffsets(getContext(), kafkaconn.GroupOffsetResetRequest{
			ConnectionID:     connectionID,
			Group:            group,
			Topics:           req.Topics,
			ResetTo:          req.ResetTo,
			TimestampMs:      req.TimestampMs,
			PartitionOffsets: req.PartitionOffsets,
			Source:           sourceMCP,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"success": true, "action": "offsetsReset", "rows": result.Rows}, nil
	})
}

// topicsRecordsClear 工具 `kafka_topics_records_clear`：强制两阶段
// （purge 同级红线：read_only/allow_delete 与门在预览与执行两道都拒绝）。
func (s *Server) topicsRecordsClear(args map[string]any) (map[string]any, error) {
	connectionID := strings.TrimSpace(stringField(args, "connectionId"))
	topic := strings.TrimSpace(stringField(args, "topic"))
	if connectionID == "" {
		return nil, errors.New("connectionId is required")
	}
	if topic == "" {
		return nil, errors.New("topic is required")
	}
	if err := s.ensureDeleteAllowed(connectionID, "topics/records/clear"); err != nil {
		return nil, err
	}
	req := clearArgs{ConnectionID: connectionID, Topic: topic}
	return s.twoPhase(connectionID, req, args, func() (any, error) {
		result, err := s.svc.ClearTopicRecords(getContext(), kafkaconn.TopicRecordsClearRequest{
			ConnectionID: connectionID,
			Topic:        topic,
			// MCP 面无 confirmTopic 参数（两阶段令牌即确认）；执行层仍带
			// 同名确认满足 kafkaconn 防误清空门禁（纵深防御）。
			ConfirmTopic: topic,
			Source:       sourceMCP,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"success": true, "action": "recordsClear", "rows": result.Rows}, nil
	})
}

// twoPhase 两阶段通用骨架：
// 无 confirmToken → preview + 一次性令牌（60s TTL，hash 绑定，不执行写）；
// 带 token → Consume（一次性 + hash 一致）→ 执行。
func (s *Server) twoPhase(connectionID string, req any, args map[string]any, execute func() (any, error)) (map[string]any, error) {
	canonical, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	paramHash := HashParams(canonical)
	token := strings.TrimSpace(stringField(args, "confirmToken"))
	if token == "" {
		issued, expiresAt := s.confirms.Issue(paramHash, s.now())
		return map[string]any{
			"preview":      req,
			"confirmToken": issued,
			"expiresAt":    expiresAt.UTC().Format(time.RFC3339),
			"note":         "nothing written yet; repeat the same arguments with confirmToken to execute",
		}, nil
	}
	switch result := s.confirms.Consume(token, paramHash, s.now()); result {
	case ConfirmOK:
		out, err := execute()
		if err != nil {
			return nil, err
		}
		if typed, ok := out.(map[string]any); ok {
			return typed, nil
		}
		return map[string]any{"success": true}, nil
	case ConfirmExpired:
		return nil, errors.New("confirmToken expired (60s); request a new preview")
	case ConfirmHashMismatch:
		return nil, errors.New("arguments changed since the preview; request a new confirmToken")
	default:
		return nil, errors.New("confirmToken unknown or already used; request a new preview")
	}
}

// ensureWritable mcp/call 侧只读门（纵深防御：工具清单已剔除写工具，
// 直接调用仍拒绝）。
func (s *Server) ensureWritable(connectionID string) error {
	readOnly, _ := s.svc.PolicyOf(connectionID)
	if readOnly {
		return fmt.Errorf("connection %q is read-only; write tools are refused", connectionID)
	}
	return nil
}

// ensureDeleteAllowed 删除类门（read_only ∥ !allow_delete）。
func (s *Server) ensureDeleteAllowed(connectionID, action string) error {
	readOnly, allowDelete := s.svc.PolicyOf(connectionID)
	if readOnly {
		return fmt.Errorf("connection %q is read-only; %s is refused", connectionID, action)
	}
	if !allowDelete {
		return fmt.Errorf("connection %q does not allow delete operations (allow_delete=false); %s is refused", connectionID, action)
	}
	return nil
}

// --- 参数解析辅助（kafkaconn 形状对齐） ---

// intSlice 整数数组参数（JSON number → int32）。
func intSlice(raw any) []int32 {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]int32, 0, len(items))
	for _, item := range items {
		number, ok := item.(float64)
		if !ok || number < 0 {
			continue
		}
		out = append(out, int32(number))
	}
	return out
}

// optionalInt64 可选整数字段（缺失/非数值 = 未提供）。
func optionalInt64(raw any) (int64, bool) {
	number, ok := raw.(float64)
	if !ok {
		return 0, false
	}
	return int64(number), true
}

// parseFieldFilters 解析字段级过滤（ConsumeFieldFilter 形状直通）。
func parseFieldFilters(raw any) ([]kafkaconn.ConsumeFieldFilter, error) {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return nil, nil
	}
	out := make([]kafkaconn.ConsumeFieldFilter, 0, len(items))
	for index, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("fieldFilters[%d] must be an object", index)
		}
		filter := kafkaconn.ConsumeFieldFilter{
			Source:   stringField(object, "source"),
			Path:     stringField(object, "path"),
			Operator: stringField(object, "operator"),
			Value:    stringField(object, "value"),
		}
		if enabled, ok := object["enabled"].(bool); ok {
			filter.Enabled = &enabled
		}
		out = append(out, filter)
	}
	return out, nil
}

// parsePartitionOffsets 解析 resetTo=partitionOffset 的嵌套映射
// （topic → partition → offset）。
func parsePartitionOffsets(raw any) (map[string]map[int32]int64, error) {
	object, ok := raw.(map[string]any)
	if !ok || len(object) == 0 {
		return nil, nil
	}
	out := make(map[string]map[int32]int64, len(object))
	for topic, partitions := range object {
		partitionMap, ok := partitions.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("partitionOffsets[%q] must be an object", topic)
		}
		inner := make(map[int32]int64, len(partitionMap))
		for partition, offset := range partitionMap {
			number, err := strconv.ParseFloat(fmt.Sprintf("%v", offset), 64)
			if err != nil || number < 0 {
				return nil, fmt.Errorf("partitionOffsets[%q][%q] must be a non-negative integer", topic, partition)
			}
			partitionNumber, err := strconv.ParseInt(strings.TrimSpace(partition), 10, 32)
			if err != nil || partitionNumber < 0 {
				return nil, fmt.Errorf("partitionOffsets[%q] partition %q must be a non-negative integer", topic, partition)
			}
			inner[int32(partitionNumber)] = int64(number)
		}
		out[topic] = inner
	}
	return out, nil
}

// sortStrings 就地排序（topic 名清单稳定输出）。
func sortStrings(names []string) {
	sort.Strings(names)
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
