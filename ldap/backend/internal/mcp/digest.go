package mcp

// digest.go：`ldap_search_digest` 的本地聚合（设计 §3 原语 2/3）。
//
// filter 已在 LDAP 服务端执行（Search 下推）；本文件只对回传条目做本地
// 聚合：count、groupBy objectClass（≤20 组）、指定 attr distinct、子树
// 计数、样本/行投影 + 单元格截断。扫描过程数据一条不出 sidecar——只有
// 聚合结论与定位字段进 MCP 返回。
// 纯函数：输入 []ldapconn.LDAPEntry + 参数，输出 digest 结构，便于单测。

import (
	"sort"
	"strings"

	"io.dbx.ldap.plugin/internal/ldapconn"
)

// DigestCellTruncate 单元格截断（§3：每单元格 120 字符；DN 定位字段不截断，
// 截断在调用方按字段判断）。width ≤0 不截断。
func DigestCellTruncate(value string, width int) string {
	if width <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	return string(runes[:width]) + "…"
}

// DistinctStats 指定属性的值域统计。
type DistinctStats struct {
	Attribute  string         `json:"attribute"`
	ValueCount int            `json:"valueCount"`
	Values     map[string]int `json:"values,omitempty"` // top ≤ DigestTopN（按出现次数）
	Truncated  bool           `json:"truncated,omitempty"`
}

// DigestStats 聚合结论（digest 的 stats 段）。objectClass/subtrees 空聚合
// 也输出 {}（无 omitempty）：键恒在，AI 的响应形状假设稳定（distinct 指针
// 仅在显式 distinctAttr 时出现，属可选段）。
type DigestStats struct {
	ObjectClass      map[string]int `json:"objectClass"`
	ObjectClassLimit bool           `json:"objectClassLimit,omitempty"` // 组数超上限被截
	Subtrees         map[string]int `json:"subtrees"`
	SubtreesLimit    bool           `json:"subtreesLimit,omitempty"`
	Distinct         *DistinctStats `json:"distinct,omitempty"`
	DistinctLimit    bool           `json:"distinctLimit,omitempty"`
}

// topGroups 组计数取前 limit 组（按计数降序、组名升序稳定排序）。
func topGroups(counts map[string]int, limit int) (map[string]int, bool) {
	if len(counts) == 0 || limit <= 0 {
		return map[string]int{}, false
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return names[i] < names[j]
	})
	truncated := len(names) > limit
	if truncated {
		names = names[:limit]
	}
	out := make(map[string]int, len(names))
	for _, name := range names {
		out[name] = counts[name]
	}
	return out, truncated
}

// DigestInput 聚合参数。
type DigestInput struct {
	Entries      []ldapconn.LDAPEntry
	BaseDN       string
	Filter       string
	DistinctAttr string
	// Width 单元格截断宽度（DN 定位字段不截断）；≤0 不截断。
	Width int
	// GroupLimit / TopN / SampleRows 设计硬上限内的可调值。
	GroupLimit int
	TopN       int
	SampleRows int
}

// DigestResult 聚合输出（匹配 digest / rows 两种 format 的公共字段）。
type DigestResult struct {
	Matched   int
	Truncated bool // 服务端/聚合层截断（透传 Search.Truncated）
	Stats     DigestStats
	Sample    []map[string]any
}

// AggregateDigest 对条目做本地聚合（objectClass 分布、子树计数、distinct、
// 样本投影）。投影单元格过 DigestCellTruncate，DN 不截断。
func AggregateDigest(input DigestInput) DigestResult {
	stats := DigestStats{ObjectClass: map[string]int{}, Subtrees: map[string]int{}}
	base := strings.ToLower(strings.TrimSpace(input.BaseDN))
	classCounts := map[string]int{}
	subtreeCounts := map[string]int{}
	distinctCounts := map[string]int{}
	distinctAttr := strings.TrimSpace(input.DistinctAttr)

	for _, entry := range input.Entries {
		for _, class := range entry.Attributes["objectClass"] {
			classCounts[class]++
		}
		subtreeCounts[subtreeKey(entry.DN, base)]++
		if distinctAttr != "" {
			for _, value := range entry.Attributes[distinctAttr] {
				distinctCounts[value]++
			}
		}
	}

	stats.ObjectClass, stats.ObjectClassLimit = topGroups(classCounts, input.GroupLimit)
	stats.Subtrees, stats.SubtreesLimit = topGroups(subtreeCounts, input.GroupLimit)
	if distinctAttr != "" {
		values, truncated := topGroups(distinctCounts, input.TopN)
		stats.Distinct = &DistinctStats{
			Attribute:  distinctAttr,
			ValueCount: len(distinctCounts),
			Values:     values,
			Truncated:  truncated,
		}
	}

	sampleRows := input.SampleRows
	if sampleRows <= 0 {
		sampleRows = 5
	}
	if sampleRows > len(input.Entries) {
		sampleRows = len(input.Entries)
	}
	sample := make([]map[string]any, 0, sampleRows)
	for _, entry := range input.Entries[:sampleRows] {
		sample = append(sample, ProjectEntry(entry, input.Width))
	}

	return DigestResult{
		Matched:   len(input.Entries),
		Truncated: false,
		Stats:     stats,
		Sample:    sample,
	}
}

// ProjectEntry 把条目投影为 {dn, attributes}（dn 定位字段不截断；属性值
// 逐值截断）。
func ProjectEntry(entry ldapconn.LDAPEntry, width int) map[string]any {
	attributes := make(map[string]any, len(entry.Attributes))
	for name, values := range entry.Attributes {
		if width <= 0 {
			attributes[name] = append([]string(nil), values...)
			continue
		}
		projected := make([]string, len(values))
		for index, value := range values {
			projected[index] = DigestCellTruncate(value, width)
		}
		attributes[name] = projected
	}
	return map[string]any{"dn": entry.DN, "attributes": attributes}
}

// subtreeKey 返回 entry.DN 相对 base 的直接子 DN（`uid=x,ou=people,dc=a` →
// `ou=people,dc=a`）；base 为空或 DN 不在 base 内时返回 DN 本身，base 自身
// 条目归入 base 键。
func subtreeKey(dn, base string) string {
	dn = strings.TrimSpace(dn)
	if base == "" {
		return dn
	}
	lowered := strings.ToLower(dn)
	if lowered == base {
		return dn
	}
	if !strings.HasSuffix(lowered, ","+base) {
		return dn
	}
	rest := dn[:len(dn)-len(base)-1]
	if index := strings.Index(rest, ","); index >= 0 {
		return rest[index+1:] + "," + base
	}
	return dn // base 直接子节点
}
