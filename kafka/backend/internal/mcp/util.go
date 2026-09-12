package mcp

// util.go：mcp 包内参数解析 / 投影小工具（纯函数；与 ldap/files 版同构）。

import (
	"context"
	"encoding/json"
	"strings"
)

// getContext 请求级 context（jsonl SDK 无 ctx 传递；超时由 kafkaconn 层
// 按操作控制，与 main.go 同语义）。
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

// clampStrings 名称清单截断（上限内保留）。
func clampStrings(names []string, limit int) []string {
	if limit <= 0 || len(names) <= limit {
		return names
	}
	return names[:limit]
}

// payloadSize 序列化体积（响应上限判定用）。
func payloadSize(value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return len(data)
}

// cloneMap 浅复制（响应截断前先复制，不污染调用方数据）。
func cloneMap(source map[string]any) map[string]any {
	out := make(map[string]any, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
