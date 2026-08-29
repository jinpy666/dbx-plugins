// operations.go（L-B 路实现；签名契约来自 L-A 占位清单，保持不变）。
//
// ldap/* 领域方法（实施文档 §5.2 方法表全量），tiny-rdm
// backend/services/ldap_service.go :356-744 操作语义移植：
//   - 连接获取一律走 s.WithConn(ctx, req.ConnectionID, fn)（未知
//     connectionId 由 service.go 返回 errConnectionNotFound，main 转 -32000）；
//   - 安全策略（§6）：读过 ensureLDAPReadAllowed，写过 ensureLDAPWriteAllowed
//     （read_only × 写白名单 × 屏蔽属性），写值过 normalizeLDAPWriteValues；
//   - 审计（占位契约）：写操作成功 result:"ok"；策略拒绝 result:"denied"；
//     执行失败 result:"error"。target 只记 DN，不记属性值。
package ldapconn

import (
	"context"
	"fmt"
	"strings"

	ldap "github.com/go-ldap/ldap/v3"

	"github.com/google/uuid"
)

// defaultSearchAggregateLimit 分页聚合上限（实施文档 §10：sizeLimit 缺省 500）。
const defaultSearchAggregateLimit = 500

// LDAPSearchPreset 搜索预设（契约 §5.2 ldap/presets/*；存 store presets.json，
// 明文、不含凭据）。
type LDAPSearchPreset struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	BaseDN     string   `json:"baseDn,omitempty"`
	Filter     string   `json:"filter,omitempty"`
	Scope      string   `json:"scope,omitempty"` // base | one | sub
	Attributes []string `json:"attributes,omitempty"`
	SizeLimit  int      `json:"sizeLimit,omitempty"`
}

// PresetStore 是预设持久化接口（main.go 注入 store-backed 实现；
// Service.Presets 为 nil 时 ldap/presets/* 返回业务错误）。
type PresetStore interface {
	LoadPresets() ([]LDAPSearchPreset, error)
	SavePresets(presets []LDAPSearchPreset) error
}

// Search 实现 ldap/search（tiny-rdm Search :356）。
//
// baseDn 缺省 profile.BaseDN（再空报错）；filter RFC 4515 校验（空缺省
// (objectClass=*)）；scope ∈ base|one|sub（缺省 sub）；sizeLimit 传服务器
// 字段保持 tiny-rdm 原样（normalizeLDAPSizeLimit），客户端聚合上限缺省
// 500；pageSize > 0 走 paged search 聚合，达聚合上限截断并置 truncated；
// 请求 attributes 先 sanitizeLDAPAttributes 剔除屏蔽属性；读白名单拒绝时
// EmitAudit action:"read-policy"/result:"denied"；返回条目剔除屏蔽属性。
func (s *Service) Search(ctx context.Context, req LDAPSearchRequest) (LDAPSearchResult, error) {
	profile, err := s.Get(req.ConnectionID)
	if err != nil {
		return LDAPSearchResult{}, err
	}

	baseDN := strings.TrimSpace(req.BaseDN)
	if baseDN == "" {
		baseDN = profile.BaseDN
	}
	if baseDN == "" {
		return LDAPSearchResult{}, fmt.Errorf("baseDn is required")
	}
	filter := strings.TrimSpace(req.Filter)
	if filter == "" {
		filter = "(objectClass=*)"
	}
	if err := validateLDAPFilter(filter); err != nil {
		return LDAPSearchResult{}, err
	}
	if err := ensureLDAPReadAllowed(profile, baseDN); err != nil {
		s.EmitAudit(AuditRecord{ConnectionID: req.ConnectionID, Action: "read-policy", Target: baseDN, Result: "denied", Detail: err.Error()})
		return LDAPSearchResult{}, err
	}

	// 请求属性：去重归一 + 屏蔽属性剔除（不主动拉取敏感列）。
	attrs := sanitizeLDAPAttributes(profile, normalizeLDAPAttributes(req.Attributes))

	var entries []LDAPEntry
	truncated := false
	err = s.WithConn(ctx, req.ConnectionID, func(conn *ldap.Conn) error {
		searchReq := ldap.NewSearchRequest(
			baseDN,
			ldapSearchScope(req.Scope),
			ldapDerefAliases(req.DerefAliases),
			normalizeLDAPSizeLimit(req.SizeLimit),
			0,
			req.TypesOnly,
			filter,
			attrs,
			nil,
		)
		if pageSize := normalizeLDAPPageSize(req.PageSize); pageSize > 0 {
			var result []*ldap.Entry
			var searchErr error
			result, truncated, searchErr = pagedSearchEntries(conn, searchReq, pageSize, aggregateLimit(req.SizeLimit))
			if searchErr != nil {
				return searchErr
			}
			entries = ldapEntriesToTypes(result)
			return nil
		}
		result, searchErr := conn.Search(searchReq)
		if searchErr != nil {
			return searchErr
		}
		entries = ldapEntriesToTypes(result.Entries)
		return nil
	})
	if err != nil {
		return LDAPSearchResult{}, err
	}
	for i := range entries {
		entries[i] = filterLDAPEntryBlockedAttributes(profile, entries[i])
	}
	return LDAPSearchResult{
		Entries:   entries,
		Count:     len(entries),
		Truncated: truncated,
		BaseDN:    baseDN,
		Filter:    filter,
	}, nil
}

// Count 实现 ldap/count（A-LDAP 遗留：树徽章精确计数）。scope 固定 one，
// 请求 ["1.1"]（no attributes）+ typesOnly 最小化负载；计数上限 5000，
// 达到上限以 truncated 标记。复用读白名单与 filter 校验，拒绝发审计。
func (s *Service) Count(ctx context.Context, req LDAPCountRequest) (LDAPCountResult, error) {
	profile, err := s.Get(req.ConnectionID)
	if err != nil {
		return LDAPCountResult{}, err
	}
	baseDN := strings.TrimSpace(req.BaseDN)
	if baseDN == "" {
		baseDN = profile.BaseDN
	}
	if baseDN == "" {
		return LDAPCountResult{}, fmt.Errorf("baseDn is required")
	}
	filter := strings.TrimSpace(req.Filter)
	if filter == "" {
		filter = "(objectClass=*)"
	}
	if err := validateLDAPFilter(filter); err != nil {
		return LDAPCountResult{}, err
	}
	if err := ensureLDAPReadAllowed(profile, baseDN); err != nil {
		s.EmitAudit(AuditRecord{ConnectionID: req.ConnectionID, Action: "read-policy", Target: baseDN, Result: "denied", Detail: err.Error()})
		return LDAPCountResult{}, err
	}

	const countLimit = 5000
	var count int
	var truncated bool
	err = s.WithConn(ctx, req.ConnectionID, func(conn *ldap.Conn) error {
		searchReq := ldap.NewSearchRequest(
			baseDN,
			ldap.ScopeSingleLevel,
			ldap.NeverDerefAliases,
			countLimit+1, // 服务端侧截断保护；+1 用于判定 truncated
			0,
			true, // typesOnly：不取属性值
			filter,
			[]string{"1.1"}, // no attributes
			nil,
		)
		result, searchErr := conn.Search(searchReq)
		if searchErr != nil {
			return searchErr
		}
		if len(result.Entries) > countLimit {
			count = countLimit
			truncated = true
			return nil
		}
		count = len(result.Entries)
		return nil
	})
	if err != nil {
		return LDAPCountResult{}, err
	}
	return LDAPCountResult{Count: count, Truncated: truncated}, nil
}

// GetEntry 实现 ldap/entry/get（tiny-rdm GetEntry :426）。
// attributes 经 sanitizeLDAPAttributes 过滤后再下发；读白名单拒绝发审计；
// 结果条目再过 filterLDAPEntryBlockedAttributes。
func (s *Service) GetEntry(ctx context.Context, req LDAPGetEntryRequest) (LDAPEntry, error) {
	profile, err := s.Get(req.ConnectionID)
	if err != nil {
		return LDAPEntry{}, err
	}
	dn := strings.TrimSpace(req.DN)
	if dn == "" {
		return LDAPEntry{}, fmt.Errorf("dn is required")
	}
	if err := ensureLDAPReadAllowed(profile, dn); err != nil {
		s.EmitAudit(AuditRecord{ConnectionID: req.ConnectionID, Action: "read-policy", Target: dn, Result: "denied", Detail: err.Error()})
		return LDAPEntry{}, err
	}
	attrs := sanitizeLDAPAttributes(profile, normalizeLDAPAttributes(req.Attributes))
	entry, err := s.readEntry(ctx, req.ConnectionID, dn, attrs)
	if err != nil {
		return LDAPEntry{}, err
	}
	return filterLDAPEntryBlockedAttributes(profile, entry), nil
}

// RootDSE 实现 ldap/rootDSE（tiny-rdm RootDSE :452）。
// AllowedBaseDNs 非空时 rootDSEAllowed 沿袭拒绝；实现 = readEntry(dn="")，
// 返回 LDAPEntry.DN 为空、Attributes 为 RootDSE 属性（屏蔽属性过滤后）。
func (s *Service) RootDSE(ctx context.Context, req LDAPRootDSERequest) (LDAPEntry, error) {
	profile, err := s.Get(req.ConnectionID)
	if err != nil {
		return LDAPEntry{}, err
	}
	if err := rootDSEAllowed(profile); err != nil {
		return LDAPEntry{}, err
	}
	entry, err := s.readEntry(ctx, req.ConnectionID, "", sanitizeLDAPAttributes(profile, normalizeLDAPAttributes(req.Attributes)))
	if err != nil {
		return LDAPEntry{}, err
	}
	return filterLDAPEntryBlockedAttributes(profile, entry), nil
}

// SchemaMetadata 实现 ldap/schema（tiny-rdm GetSchemaMetadata :472）。
// refresh=true 跳缓存；否则走 s.SchemaCache。流程：rootDSE(subschemaSubentry)
// → schemaDN 白名单校验 → subschema 条目 → ResolveSchema（解析 + 策略过滤）。
func (s *Service) SchemaMetadata(ctx context.Context, req LDAPSchemaMetadataRequest) (LDAPSchemaMetadata, error) {
	profile, err := s.Get(req.ConnectionID)
	if err != nil {
		return LDAPSchemaMetadata{}, err
	}
	if !req.Refresh {
		if cached := s.schemaCache().Get(req.ConnectionID); cached != nil {
			return *cached, nil
		}
	}
	if err := schemaAllowed(profile); err != nil {
		return LDAPSchemaMetadata{}, err
	}
	spec := DefaultSchemaSearchSpec()
	rootDSE, err := s.readEntry(ctx, req.ConnectionID, "", spec.RootDSEAttrs)
	if err != nil {
		return LDAPSchemaMetadata{}, err
	}
	schemaDN, err := subschemaDNFromRootDSE(rootDSE)
	if err != nil {
		return LDAPSchemaMetadata{}, err
	}
	if err := ensureLDAPReadAllowed(profile, schemaDN); err != nil {
		return LDAPSchemaMetadata{}, err
	}
	schemaEntry, err := s.readEntry(ctx, req.ConnectionID, schemaDN, spec.SubschemaAttrs)
	if err != nil {
		return LDAPSchemaMetadata{}, err
	}
	metadata := parseLDAPSchemaMetadata(schemaDN, schemaEntry)
	metadata = filterLDAPSchemaMetadataForProfile(profile, metadata)
	s.schemaCache().Put(req.ConnectionID, metadata)
	return metadata, nil
}

// AddEntry 实现 ldap/entry/add（tiny-rdm AddEntry :508）：
// normalizeLDAPWriteDN → normalizeLDAPAddAttributes（含值校验）→
// ensureLDAPWriteAllowed → WithConn(conn.Add)。审计 action:"add-entry"。
func (s *Service) AddEntry(ctx context.Context, req LDAPAddEntryRequest) error {
	profile, err := s.Get(req.ConnectionID)
	if err != nil {
		return err
	}
	dn, err := normalizeLDAPWriteDN(req.DN)
	if err != nil {
		return err
	}
	attrs, attrNames, err := normalizeLDAPAddAttributes(req.Attributes)
	if err != nil {
		return err
	}
	if err := ensureLDAPWriteAllowed(profile, dn, attrNames); err != nil {
		s.EmitAudit(AuditRecord{ConnectionID: req.ConnectionID, Action: "write-policy", Target: dn, Result: "denied", Detail: err.Error()})
		return err
	}
	err = s.WithConn(ctx, req.ConnectionID, func(conn *ldap.Conn) error {
		addReq := ldap.NewAddRequest(dn, nil)
		for attr, values := range attrs {
			addReq.Attribute(attr, values)
		}
		return conn.Add(addReq)
	})
	if err != nil {
		s.EmitAudit(AuditRecord{ConnectionID: req.ConnectionID, Action: "add-entry", Target: dn, Result: "error", Detail: err.Error()})
		return err
	}
	s.EmitAudit(AuditRecord{ConnectionID: req.ConnectionID, Action: "add-entry", Target: dn, Result: "ok", Detail: "attributes: " + strings.Join(attrNames, ",")})
	return nil
}

// ModifyEntry 实现 ldap/entry/modify（tiny-rdm ModifyEntry :553）：
// 屏蔽属性在 ensureLDAPWriteAllowed 内拒绝修改；逐条 add/replace/delete。
// 审计 action:"modify-entry"。
func (s *Service) ModifyEntry(ctx context.Context, req LDAPModifyEntryRequest) error {
	profile, err := s.Get(req.ConnectionID)
	if err != nil {
		return err
	}
	dn, err := normalizeLDAPWriteDN(req.DN)
	if err != nil {
		return err
	}
	changes, attrs, err := normalizeLDAPModifyChanges(req.Changes)
	if err != nil {
		return err
	}
	if err := ensureLDAPWriteAllowed(profile, dn, attrs); err != nil {
		s.EmitAudit(AuditRecord{ConnectionID: req.ConnectionID, Action: "write-policy", Target: dn, Result: "denied", Detail: err.Error()})
		return err
	}
	err = s.WithConn(ctx, req.ConnectionID, func(conn *ldap.Conn) error {
		modReq := ldap.NewModifyRequest(dn, nil)
		for _, change := range changes {
			switch change.Operation {
			case "add":
				modReq.Add(change.Attribute, change.Values)
			case "replace":
				modReq.Replace(change.Attribute, change.Values)
			case "delete":
				modReq.Delete(change.Attribute, change.Values)
			}
		}
		return conn.Modify(modReq)
	})
	if err != nil {
		s.EmitAudit(AuditRecord{ConnectionID: req.ConnectionID, Action: "modify-entry", Target: dn, Result: "error", Detail: err.Error()})
		return err
	}
	s.EmitAudit(AuditRecord{ConnectionID: req.ConnectionID, Action: "modify-entry", Target: dn, Result: "ok", Detail: "attributes: " + strings.Join(attrs, ",")})
	return nil
}

// DeleteEntry 实现 ldap/entry/delete（tiny-rdm DeleteEntry :606）。
// 审计 action:"delete-entry"。
func (s *Service) DeleteEntry(ctx context.Context, req LDAPDeleteEntryRequest) error {
	profile, err := s.Get(req.ConnectionID)
	if err != nil {
		return err
	}
	dn, err := normalizeLDAPWriteDN(req.DN)
	if err != nil {
		return err
	}
	if err := ensureLDAPWriteAllowed(profile, dn, nil); err != nil {
		s.EmitAudit(AuditRecord{ConnectionID: req.ConnectionID, Action: "write-policy", Target: dn, Result: "denied", Detail: err.Error()})
		return err
	}
	err = s.WithConn(ctx, req.ConnectionID, func(conn *ldap.Conn) error {
		return conn.Del(ldap.NewDelRequest(dn, nil))
	})
	if err != nil {
		s.EmitAudit(AuditRecord{ConnectionID: req.ConnectionID, Action: "delete-entry", Target: dn, Result: "error", Detail: err.Error()})
		return err
	}
	s.EmitAudit(AuditRecord{ConnectionID: req.ConnectionID, Action: "delete-entry", Target: dn, Result: "ok"})
	return nil
}

// ModifyDN 实现 ldap/entry/modifyDn（tiny-rdm ModifyDN :641）：
// 新旧 DN 均过写白名单（目标 DN 先 ldapModifyDNDestinationDN 计算）。
// 审计 action:"modify-dn"。
func (s *Service) ModifyDN(ctx context.Context, req LDAPModifyDNRequest) error {
	profile, err := s.Get(req.ConnectionID)
	if err != nil {
		return err
	}
	dn, err := normalizeLDAPWriteDN(req.DN)
	if err != nil {
		return err
	}
	newRDN := strings.TrimSpace(req.NewRDN)
	if newRDN == "" {
		return fmt.Errorf("dn and newRdn are required")
	}
	if err := ensureLDAPWriteAllowed(profile, dn, nil); err != nil {
		s.EmitAudit(AuditRecord{ConnectionID: req.ConnectionID, Action: "write-policy", Target: dn, Result: "denied", Detail: err.Error()})
		return err
	}
	destinationDN, err := ldapModifyDNDestinationDN(dn, newRDN, req.NewSuperior)
	if err != nil {
		return err
	}
	if err := ensureLDAPWriteAllowed(profile, destinationDN, nil); err != nil {
		s.EmitAudit(AuditRecord{ConnectionID: req.ConnectionID, Action: "write-policy", Target: destinationDN, Result: "denied", Detail: err.Error()})
		return err
	}
	err = s.WithConn(ctx, req.ConnectionID, func(conn *ldap.Conn) error {
		return conn.ModifyDN(ldap.NewModifyDNRequest(dn, newRDN, req.DeleteOldRDN, strings.TrimSpace(req.NewSuperior)))
	})
	if err != nil {
		s.EmitAudit(AuditRecord{ConnectionID: req.ConnectionID, Action: "modify-dn", Target: dn, Result: "error", Detail: err.Error()})
		return err
	}
	s.EmitAudit(AuditRecord{ConnectionID: req.ConnectionID, Action: "modify-dn", Target: dn, Result: "ok", Detail: "destinationDn: " + destinationDN})
	return nil
}

// ConnectionStatuses 实现 ldap/connections/statuses（tiny-rdm
// ConnectionStatuses :695）：全局视图，直接快照 service 连接表。
func (s *Service) ConnectionStatuses() ([]LDAPConnectionStatus, error) {
	return s.SnapshotStatuses(), nil
}

// ListPresets 实现 ldap/presets/list（预设明文存 presets.json，不含凭据）。
// Service.Presets 未注入时返回业务错误。
func (s *Service) ListPresets() ([]LDAPSearchPreset, error) {
	store := s.presetStore()
	if store == nil {
		return nil, fmt.Errorf("preset store is not available")
	}
	presets, err := store.LoadPresets()
	if err != nil {
		return nil, err
	}
	if presets == nil {
		presets = []LDAPSearchPreset{}
	}
	return presets, nil
}

// SavePreset 实现 ldap/presets/save：全量 upsert 单个预设（id 空时生成
// uuid，name 必填）。
func (s *Service) SavePreset(preset LDAPSearchPreset) (LDAPSearchPreset, error) {
	store := s.presetStore()
	if store == nil {
		return LDAPSearchPreset{}, fmt.Errorf("preset store is not available")
	}
	preset.ID = strings.TrimSpace(preset.ID)
	preset.Name = strings.TrimSpace(preset.Name)
	if preset.Name == "" {
		return LDAPSearchPreset{}, fmt.Errorf("preset name is required")
	}
	if preset.ID == "" {
		preset.ID = uuid.NewString()
	}
	presets, err := store.LoadPresets()
	if err != nil {
		return LDAPSearchPreset{}, err
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
	if err := store.SavePresets(presets); err != nil {
		return LDAPSearchPreset{}, err
	}
	return preset, nil
}

// RemovePreset 实现 ldap/presets/remove：按 id 删除；不存在返回业务错误。
func (s *Service) RemovePreset(id string) error {
	store := s.presetStore()
	if store == nil {
		return fmt.Errorf("preset store is not available")
	}
	id = strings.TrimSpace(id)
	presets, err := store.LoadPresets()
	if err != nil {
		return err
	}
	out := make([]LDAPSearchPreset, 0, len(presets))
	found := false
	for _, preset := range presets {
		if preset.ID == id {
			found = true
			continue
		}
		out = append(out, preset)
	}
	if !found {
		return fmt.Errorf("preset %q is not found", id)
	}
	return store.SavePresets(out)
}

// presetStore nil 安全取预设存储。
func (s *Service) presetStore() PresetStore {
	return s.Presets
}

// readEntry base scope 读单条（tiny-rdm getEntry :744 语义：scope=base、
// sizeLimit=1、filter=(objectClass=*)、never deref；空结果报 entry not found）。
func (s *Service) readEntry(ctx context.Context, connectionID, dn string, attributes []string) (LDAPEntry, error) {
	var entry LDAPEntry
	err := s.WithConn(ctx, connectionID, func(conn *ldap.Conn) error {
		result, err := conn.Search(ldap.NewSearchRequest(
			dn,
			ldap.ScopeBaseObject,
			ldap.NeverDerefAliases,
			1,
			0,
			false,
			"(objectClass=*)",
			normalizeLDAPAttributes(attributes),
			nil,
		))
		if err != nil {
			return err
		}
		if len(result.Entries) == 0 {
			return fmt.Errorf("ldap entry not found")
		}
		entry = ldapEntryToType(result.Entries[0])
		return nil
	})
	if err != nil {
		return LDAPEntry{}, err
	}
	return entry, nil
}

// schemaCache nil 安全取缓存。
func (s *Service) schemaCache() *SchemaCache {
	if s.SchemaCache == nil {
		return NewSchemaCache(defaultSchemaCacheTTL)
	}
	return s.SchemaCache
}

// aggregateLimit 聚合上限：sizeLimit 显式给定时用之，否则缺省 500（§10）。
func aggregateLimit(sizeLimit int) int {
	if sizeLimit > 0 {
		return sizeLimit
	}
	return defaultSearchAggregateLimit
}

// pagedSearchEntries 分页搜索聚合（对照 go-ldap Conn.SearchWithPaging 手动展开，
// 加客户端聚合上限：达到 limit 截断并返回 truncated=true，用于前端提示）。
func pagedSearchEntries(conn *ldap.Conn, searchReq *ldap.SearchRequest, pageSize uint32, limit int) ([]*ldap.Entry, bool, error) {
	pagingControl := ldap.NewControlPaging(pageSize)
	searchReq.Controls = append(searchReq.Controls, pagingControl)

	var entries []*ldap.Entry
	truncated := false
	for {
		result, err := conn.Search(searchReq)
		if err != nil {
			return entries, truncated, err
		}
		entries = append(entries, result.Entries...)

		// 聚合上限（limit>0 时生效）：截断并停止翻页。
		if limit > 0 && len(entries) >= limit {
			entries = entries[:limit]
			truncated = true
			return entries, truncated, nil
		}

		// 读取服务器返回的 paging control cookie；无 cookie 表示翻页结束。
		var received *ldap.ControlPaging
		for _, control := range result.Controls {
			if control.GetControlType() == ldap.ControlTypePaging {
				received = control.(*ldap.ControlPaging)
				break
			}
		}
		if received == nil || len(received.Cookie) == 0 {
			return entries, truncated, nil
		}
		pagingControl.SetCookie(received.Cookie)
	}
}

// ldapEntriesToTypes / ldapEntryToType（tiny-rdm :2042/:2050 原样）。
func ldapEntriesToTypes(entries []*ldap.Entry) []LDAPEntry {
	out := make([]LDAPEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, ldapEntryToType(entry))
	}
	return out
}

func ldapEntryToType(entry *ldap.Entry) LDAPEntry {
	if entry == nil {
		return LDAPEntry{}
	}
	attrs := make(map[string][]string, len(entry.Attributes))
	for _, attr := range entry.Attributes {
		values := make([]string, len(attr.Values))
		copy(values, attr.Values)
		attrs[attr.Name] = values
	}
	return LDAPEntry{DN: entry.DN, Attributes: attrs}
}
