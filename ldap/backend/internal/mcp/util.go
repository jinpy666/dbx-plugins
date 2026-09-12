package mcp

// util.go：mcp 包内参数解析 / 投影小工具（纯函数，main.go 的 getContext
// 语义对齐）。

import (
	"context"
	"fmt"
	"sort"
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

// intArg 读取整数参数（float64 = JSON number；缺失/非法返回 0）。
func intArg(raw any) int {
	if number, ok := raw.(float64); ok {
		return int(number)
	}
	return 0
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

// stringSlice 读取字符串数组参数。
func stringSlice(raw any) []string {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			out = append(out, strings.TrimSpace(text))
		}
	}
	return out
}

// attributeMap 解析 add 的 attributes（attr → values[]）。
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
		list, ok := values.([]any)
		if !ok || len(list) == 0 {
			return nil, fmt.Errorf("attribute %q requires a non-empty values array", attr)
		}
		parsed := make([]string, 0, len(list))
		for _, item := range list {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("attribute %q values must be strings", attr)
			}
			parsed = append(parsed, text)
		}
		out[attr] = parsed
	}
	return out, nil
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
