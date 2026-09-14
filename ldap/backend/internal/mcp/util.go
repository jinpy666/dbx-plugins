package mcp

// util.go：mcp 包内参数解析 / 投影小工具（纯函数，main.go 的 getContext
// 语义对齐）。

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"io.dbx.ldap.plugin/internal/ldapconn"
)

// getContext 请求级 context（jsonl SDK 无 ctx 传递；超时由
// ldapconn.contextWithTimeout 按连接配置控制，与 main.go 同语义）。
func getContext() context.Context {
	return context.Background()
}

// stringField 读取字符串字段（缺失/类型不符返回空串）。
func stringField(params map[string]any, key string) string {
	if value, ok := params[key].(string); ok {
		return value
	}
	return ""
}

// missingRequired 一次枚举全部缺失的 required 参数（ssh 同款语义，
// MCP_ACCEPTANCE §3.9）：`Missing required parameters: a, b`——LLM 调用方
// 一轮补齐所有缺口，而不是逐个 fail-fast 往返。keys 按 schema required
// 顺序传入，报错顺序即该顺序。缺失判定 = 键不存在或显式 null；present-but
// 类型错误（空串/类型不符）不在此点名，由后续各参数的精确校验单独报出。
func missingRequired(args map[string]any, keys ...string) error {
	missing := make([]string, 0, len(keys))
	for _, key := range keys {
		if value, present := args[key]; !present || value == nil {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("Missing required parameters: %s", strings.Join(missing, ", "))
}

// intArg 读取整数参数：JSON number（float64）或字符串数字（LLM 常见变体，
// 如 sizeLimit:"50"）；缺失/非法返回 0（调用方按各自的缺省语义兜底）。
func intArg(raw any) int {
	number, ok := numberArg(raw)
	if !ok {
		return 0
	}
	return int(number)
}

// numberArg 数字参数宽容解析：JSON number 直通；字符串 TrimSpace 后按
// float 解析（"20"/"20.0" 都接受）；其他类型不接受（返回 false，由调用方
// 决定报错还是缺省）。
func numberArg(raw any) (float64, bool) {
	switch value := raw.(type) {
	case float64:
		return value, true
	case string:
		text := strings.TrimSpace(value)
		if text == "" {
			return 0, false
		}
		number, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0, false
		}
		return number, true
	default:
		return 0, false
	}
}

// scalarString 标量（字符串/数字/布尔）→ 字符串（LDAP 属性值本质是字符串，
// 数字如 employeeNumber:[123] 应转写而非报错）；其他类型不转换。
func scalarString(raw any) (string, bool) {
	switch value := raw.(type) {
	case string:
		return value, true
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(value), true
	default:
		return "", false
	}
}

// boolArg 读取布尔参数（缺省 false）。
func boolArg(raw any) bool {
	value, _ := raw.(bool)
	return value
}

// offsetArg 读取分页 offset：字段缺失 = -1（续读会话内游标）；显式给出
// （含 0）时按调用方指定的起点。
func offsetArg(args map[string]any) int {
	if _, present := args["offset"]; !present {
		return -1
	}
	return intArg(args["offset"])
}

// stringSlice 读取字符串数组参数（宽容变体）：数组元素接受字符串/数字/布尔
// （非字符串标量转字符串，防 LLM 传 mail:[123] 被静默丢值）；顶层字符串按
// 逗号拆分（LLM 常见 "cn,mail" 形态）。空白项丢弃。
func stringSlice(raw any) []string {
	switch value := raw.(type) {
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := scalarString(item)
			if !ok {
				continue
			}
			if trimmed := strings.TrimSpace(text); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case string:
		out := make([]string, 0)
		for _, part := range strings.Split(value, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	default:
		return nil
	}
}

// attributeMap 解析 add 的 attributes（attr → values[]）。宽容变体：单字符串
// 值折算单元素数组（LLM 常见单值形态）、数组元素接受字符串/数字/布尔。
func attributeMap(raw any) (map[string][]string, error) {
	object, ok := raw.(map[string]any)
	if !ok || len(object) == 0 {
		return nil, fmt.Errorf("attributes is required for add")
	}
	out := make(map[string][]string, len(object))
	for name, values := range object {
		attr := strings.TrimSpace(name)
		if attr == "" {
			return nil, fmt.Errorf("attribute name cannot be empty")
		}
		list, err := stringValuesList(values)
		if err != nil {
			return nil, fmt.Errorf("attribute %q %v", attr, err)
		}
		if len(list) == 0 {
			return nil, fmt.Errorf("attribute %q requires a non-empty values array", attr)
		}
		out[attr] = list
	}
	return out, nil
}

// stringValuesList 属性值列表宽容解析：单字符串 → 单元素数组；数组元素接受
// 字符串/数字/布尔；其他形状明确报错（不静默吞值）。
func stringValuesList(raw any) ([]string, error) {
	switch value := raw.(type) {
	case string:
		if strings.TrimSpace(value) == "" {
			return nil, nil
		}
		return []string{value}, nil
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := scalarString(item)
			if !ok {
				return nil, fmt.Errorf("values must be strings")
			}
			out = append(out, text)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("values must be an array of strings")
	}
}

// changeList 解析 modify 的 changes。
func changeList(raw any) ([]ldapconn.LDAPModifyChange, error) {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return nil, fmt.Errorf("changes is required for modify")
	}
	out := make([]ldapconn.LDAPModifyChange, 0, len(items))
	for index, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("change %d must be an object", index)
		}
		change := ldapconn.LDAPModifyChange{
			Operation: stringField(object, "operation"),
			Attribute: stringField(object, "attribute"),
			Values:    stringSlice(object["values"]),
		}
		out = append(out, change)
	}
	return out, nil
}

// normalizedAttrNames 属性名去重（大小写不敏感，保留首个书写形态）。
func normalizedAttrNames(names []string) []string {
	out := make([]string, 0, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

// containsFold 大小写不敏感包含判定。
func containsFold(names []string, needle string) bool {
	for _, name := range names {
		if strings.EqualFold(name, needle) {
			return true
		}
	}
	return false
}

// normalizePanelArg UI focus 面板名归一化（大小写不敏感；schema 声明 enum
// search|tree|schema）：非法值报错并列出合法值——与其发一个必然被前端
// rejected 的 intent 浪费一轮，不如本地直接给出可行动错误。
func normalizePanelArg(raw string) (string, error) {
	switch panel := strings.ToLower(strings.TrimSpace(raw)); panel {
	case "search", "tree", "schema":
		return panel, nil
	default:
		return "", fmt.Errorf("panel must be search, tree, or schema (got %q)", strings.TrimSpace(raw))
	}
}

// normalizeScopeArg 搜索 scope 归一化（大小写不敏感 + 常见别名；schema 声明
// enum base|one|sub）：空串返回空（由连接/服务端按缺省 sub）；非法值报错
// 并列出合法值，不静默回退——scope 决定结果集范围，含糊兜底会让 AI 误判
// 匹配数量。
func normalizeScopeArg(raw string) (string, error) {
	scope := strings.ToLower(strings.TrimSpace(raw))
	switch scope {
	case "":
		return "", nil
	case "base", "baseobject":
		return "base", nil
	case "one", "singlelevel", "single-level":
		return "one", nil
	case "sub", "subtree", "whole-subtree", "wholesubtree":
		return "sub", nil
	default:
		return "", fmt.Errorf("scope must be base, one, or sub (aliases baseObject/singleLevel/subtree accepted; got %q)", strings.TrimSpace(raw))
	}
}

// projectCursorAttributes cursor 行的投影属性（仅显式请求的属性；DN 定位
// 字段不截断，属性值过截断宽度）。
func projectCursorAttributes(entry ldapconn.LDAPEntry, projected []string, width int) map[string][]string {
	if len(projected) == 0 || entry.Attributes == nil {
		return nil
	}
	out := make(map[string][]string, len(projected))
	for _, name := range projected {
		values, ok := entry.Attributes[name]
		if !ok {
			continue
		}
		clamped := make([]string, len(values))
		for index, value := range values {
			clamped[index] = DigestCellTruncate(value, width)
		}
		out[name] = clamped
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// clampStrings 名称清单截断（上限内保留）。
func clampStrings(names []string, limit int) []string {
	if limit <= 0 || len(names) <= limit {
		return names
	}
	return names[:limit]
}

// sortStrings 就地排序（对象类名清单稳定输出）。
func sortStrings(names []string) {
	sort.Strings(names)
}
