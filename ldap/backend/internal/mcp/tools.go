package mcp

// tools.go：`mcp/tools` 工具注册（JSON Schema，形状照 ssh mcp.rs
// tool_definitions）。命名 <域>_<动作>、字段 camelCase。
//
// 只读连接：`mcp/tools {connectionId}` 显式给到只读连接时写工具
// （ldap_entry_write）不进清单（设计 §4），omittedWriteTools 说明原因；
// 未给 connectionId 时全量列出（调用时仍按连接只读门拒绝，纵深防御）。

// connectionProperty 各连接类工具共享的 connectionId 声明（照 ssh
// connection_properties：凭据由宿主 lifecycle 转发，参数里不出现密码）。
func connectionProperty() map[string]any {
	return map[string]any{
		"type":        "string",
		"description": "DBX saved LDAP connection id; credentials are resolved by the DBX host and never travel in tool arguments",
	}
}

// toolEntry 单个工具定义。required 为空时省略键：nil 切片的 JSON 形状是
// "required":null，严格校验的 MCP 宿主（zcode tools/list zod 校验）会据此
// 拒收整个服务器（2026-09-14 真机接入实测）。
func toolEntry(name, description string, required []string, properties map[string]any) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return map[string]any{
		"name":        name,
		"description": description,
		"inputSchema": schema,
	}
}

// writeToolName 写类工具名单（只读连接不进工具清单的唯一条目）。
const writeToolName = "ldap_entry_write"

// allToolDefinitions 全量工具定义（M1 设计 §2/§6.1：UI 驱动 4 + 本地读 2 +
// 元发现 1 + 写 1）。
func allToolDefinitions() []map[string]any {
	return []map[string]any{
		toolEntry(
			"ldap_ui_search",
			"Fill the LDAP workbench search form with the given conditions and trigger the search. Results stay visible in the UI (the user can keep working with them); returns the applied state plus a small summary (count + first rows). Requires the DBX workbench to be open: without a frontend the call reports state=pending with a hint, fall back to ldap_search_digest instead.",
			[]string{"filter"},
			map[string]any{
				"connectionId": connectionProperty(),
				"baseDn":       map[string]any{"type": "string", "description": "Base DN; defaults to the connection base DN"},
				"filter":       map[string]any{"type": "string", "description": "RFC 4515 filter, e.g. (objectClass=inetOrgPerson)"},
				"scope":        map[string]any{"type": "string", "enum": []string{"base", "one", "sub"}, "description": "Search scope (default sub)"},
				"attributes":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Requested attributes shown in the result table"},
				"sizeLimit":    map[string]any{"type": "integer", "description": "Max entries fetched"},
			},
		),
		toolEntry(
			"ldap_ui_focus",
			"Focus a workbench panel (search | tree | schema). Requires the DBX workbench to be open; without a frontend the call reports state=pending.",
			nil,
			map[string]any{
				"connectionId": connectionProperty(),
				"panel":        map[string]any{"type": "string", "enum": []string{"search", "tree", "schema"}, "description": "Panel to focus"},
			},
		),
		toolEntry(
			"ldap_ui_select",
			"Locate an entry by DN in the current result table and highlight it (the entry editor opens). Requires the DBX workbench to be open with matching results; reports rejected when the DN is not in the current results.",
			[]string{"dn"},
			map[string]any{
				"connectionId": connectionProperty(),
				"dn":           map[string]any{"type": "string", "description": "Distinguished name of the entry to select"},
			},
		),
		toolEntry(
			"ldap_ui_state",
			"Read a UI intent result by intentId, or (without intentId) the latest workbench snapshot reported by the frontend (current panel, form values, result count, selected entry). Use it to re-check an intent that returned pending.",
			nil,
			map[string]any{
				"connectionId": connectionProperty(),
				"intentId":     map[string]any{"type": "string", "description": "intentId from a previous ldap_ui_* call; omit to read the latest snapshot"},
			},
		),
		toolEntry(
			"ldap_ui_schema",
			"List objectClass and attributeType names from the sidecar schema cache, so a valid LDAP filter can be constructed before searching.",
			[]string{"connectionId"},
			map[string]any{"connectionId": connectionProperty()},
		),
		toolEntry(
			"ldap_search_digest",
			"Run an LDAP search with the filter executed server-side and aggregate the matches locally in the sidecar. Default format=digest returns counts and distributions only (objectClass distribution, subtree counts, optional distinct attribute values, <=5 sample rows); format=rows returns at most 20 rows. Every digest also materializes a cursor for ldap_cursor_next so pages are fetched without re-running the query. Full entries never leave the sidecar.",
			[]string{"connectionId", "filter"},
			map[string]any{
				"connectionId": connectionProperty(),
				"baseDn":       map[string]any{"type": "string", "description": "Base DN; defaults to the connection base DN"},
				"filter":       map[string]any{"type": "string", "description": "RFC 4515 filter (default (objectClass=*))"},
				"scope":        map[string]any{"type": "string", "enum": []string{"base", "one", "sub"}, "description": "Search scope (default sub)"},
				"attributes":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Projected attributes carried into cursor rows and samples"},
				"distinctAttr": map[string]any{"type": "string", "description": "Aggregate the distinct values of this attribute (e.g. mail)"},
				"format":       map[string]any{"type": "string", "enum": []string{"digest", "rows"}, "description": "digest (default) = counts + sample; rows = at most 20 rows"},
			},
		),
		toolEntry(
			"ldap_cursor_next",
			"Fetch the next batch (n<=20) of locator rows from a digest session: DN + projected attributes only, no filter re-send and no re-scan. Expired cursors (10 minutes) return an explicit error suggesting a fresh ldap_search_digest.",
			[]string{"cursorId"},
			map[string]any{
				"cursorId": map[string]any{"type": "string", "description": "cursorId returned by ldap_search_digest"},
				"n":        map[string]any{"type": "integer", "description": "Batch size (default 20, max 20)"},
				"offset":   map[string]any{"type": "integer", "description": "Start offset; omit to continue where the previous batch stopped"},
			},
		),
		toolEntry(
			"ldap_entry_write",
			"Write one LDAP entry. action=add|modify execute directly; action=delete (including recursive) and action=modifyDn are two-phase: the first call without confirmToken returns a preview plus a one-time confirmToken (60s TTL, bound to the parameter hash) and writes nothing — repeat the same arguments with the token to execute. Refused on read-only connections. Every write is audited with source=mcp.",
			[]string{"connectionId", "action", "dn"},
			map[string]any{
				"connectionId": connectionProperty(),
				"action":       map[string]any{"type": "string", "enum": []string{"add", "modify", "delete", "modifyDn"}, "description": "Write operation"},
				"dn":           map[string]any{"type": "string", "description": "Target entry DN"},
				"attributes":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "description": "add: attribute values map"},
				"changes":      map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"operation": map[string]any{"type": "string", "enum": []string{"add", "replace", "delete"}}, "attribute": map[string]any{"type": "string"}, "values": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}}, "description": "modify: change list"},
				"recursive":    map[string]any{"type": "boolean", "description": "delete: remove the whole subtree (limit 1000 entries)"},
				"newRdn":       map[string]any{"type": "string", "description": "modifyDn: new RDN"},
				"deleteOldRdn": map[string]any{"type": "boolean", "description": "modifyDn: remove the old RDN value"},
				"newSuperior":  map[string]any{"type": "string", "description": "modifyDn: new parent DN to move under"},
				"confirmToken": map[string]any{"type": "string", "description": "One-time token returned by the preview call (delete/modifyDn only)"},
			},
		),
	}
}

// toolNames 全部已注册工具名（unknown tool 错误自纠提示用）。
func toolNames() []string {
	definitions := allToolDefinitions()
	names := make([]string, 0, len(definitions))
	for _, tool := range definitions {
		names = append(names, tool["name"].(string))
	}
	return names
}

// Tools 返回工具清单：给出 connectionId 且该连接只读时剔除写工具并附原因
// （设计 §4：不注册而非注册了再报错）。
func (s *Server) Tools(connectionID string) map[string]any {
	tools := allToolDefinitions()
	result := map[string]any{"tools": tools}
	if connectionID == "" {
		return result
	}
	profile, err := s.svc.Get(connectionID)
	if err != nil {
		return result
	}
	if !profile.ReadOnly {
		return result
	}
	omitted := make([]map[string]any, 0, 1)
	for _, tool := range tools {
		if tool["name"] == writeToolName {
			omitted = append(omitted, map[string]any{
				"name":   writeToolName,
				"reason": "connection is configured read-only; write tools are not registered (design §4)",
			})
			break
		}
	}
	filtered := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if tool["name"] != writeToolName {
			filtered = append(filtered, tool)
		}
	}
	return map[string]any{"tools": filtered, "omittedWriteTools": omitted}
}
