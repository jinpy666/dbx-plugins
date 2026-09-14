package mcp

// util_test.go：参数宽容解析（易用性/容错性专项）——LLM 常见输入变体
//（字符串数字、逗号串、单值字符串、大小写枚举）必须清晰解析或清晰报错，
// 不允许静默吞值或含糊失败。

import (
	"reflect"
	"strings"
	"testing"

	"io.dbx.ldap.plugin/internal/ldapconn"
)

func TestIntArgAcceptsStringNumbers(t *testing.T) {
	cases := []struct {
		raw  any
		want int
	}{
		{float64(50), 50},     // JSON number（stdlib 解码形态）
		{"50", 50},            // 字符串数字
		{" 20 ", 20},          // 带空白
		{"20.0", 20},          // 浮点字面量字符串
		{nil, 0},              // 缺失
		{"", 0},               // 空串
		{"fast", 0},           // 非数字 → 0（调用方按缺省语义兜底）
		{true, 0},             // 非标量数字类型
		{map[string]any{}, 0}, // 对象
	}
	for _, tc := range cases {
		if got := intArg(tc.raw); got != tc.want {
			t.Fatalf("intArg(%#v) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestStringSliceVariants(t *testing.T) {
	// 逗号分隔字符串（LLM 常见形态）。
	if got := stringSlice("cn, mail"); !reflect.DeepEqual(got, []string{"cn", "mail"}) {
		t.Fatalf("comma string: %#v", got)
	}
	// 数组元素含数字（LDAP 值本质是字符串，转写而非丢弃）。
	got := stringSlice([]any{"cn", float64(123), true, "  ", nil, []any{}})
	want := []string{"cn", "123", "true"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mixed array: got %#v want %#v", got, want)
	}
	// 非数组非字符串 → nil（调用方按缺省处理）。
	if got := stringSlice(float64(7)); got != nil {
		t.Fatalf("scalar passthrough must be nil: %#v", got)
	}
}

func TestAttributeMapTolerantVariants(t *testing.T) {
	// 单值字符串折算单元素数组；数字/布尔值转字符串。
	values, err := attributeMap(map[string]any{
		"ou":             "people",
		"employeeNumber": []any{float64(42)},
		"active":         []any{true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(values["ou"], []string{"people"}) {
		t.Fatalf("single string value: %#v", values["ou"])
	}
	if !reflect.DeepEqual(values["employeeNumber"], []string{"42"}) {
		t.Fatalf("numeric value: %#v", values["employeeNumber"])
	}
	if !reflect.DeepEqual(values["active"], []string{"true"}) {
		t.Fatalf("boolean value: %#v", values["active"])
	}
	// 明确报错路径保持：非数组对象值、空值列表、空属性名。
	for name, raw := range map[string]any{
		"object":     map[string]any{"nested": "x"},
		"emptyArray": []any{},
		"emptyStr":   "  ",
	} {
		if _, err := attributeMap(map[string]any{"cn": raw}); err == nil {
			t.Fatalf("attributeMap(%s) must error", name)
		}
	}
	if _, err := attributeMap(map[string]any{"": []any{"x"}}); err == nil {
		t.Fatal("empty attribute name must error")
	}
}

func TestNormalizeScopeArg(t *testing.T) {
	cases := map[string]string{
		"":              "", // 缺省：交给连接/服务端（缺省 sub）
		"sub":           "sub",
		"SUB":           "sub",
		" Sub ":         "sub",
		"subtree":       "sub", // LLM 常见别名
		"whole-subtree": "sub", // go-ldap 术语别名
		"one":           "one", // RFC 4511 single level
		"SingleLevel":   "one",
		"BASE":          "base",
		"baseObject":    "base",
	}
	for raw, want := range cases {
		got, err := normalizeScopeArg(raw)
		if err != nil {
			t.Fatalf("normalizeScopeArg(%q) unexpected error: %v", raw, err)
		}
		if got != want {
			t.Fatalf("normalizeScopeArg(%q) = %q, want %q", raw, got, want)
		}
	}
	_, err := normalizeScopeArg("children")
	if err == nil {
		t.Fatal("unknown scope must be refused, not silently coerced")
	}
}

func TestNormalizePanelArg(t *testing.T) {
	if got, err := normalizePanelArg("TREE"); err != nil || got != "tree" {
		t.Fatalf("normalizePanelArg(TREE) = %q, %v", got, err)
	}
	if _, err := normalizePanelArg("main"); err == nil {
		t.Fatal("unknown panel must be refused")
	}
}

func TestOffsetArgExplicitZero(t *testing.T) {
	// 缺失 = 续读会话内游标（-1）；显式 0 = 从头取（字符串数字同样识别）。
	if got := offsetArg(map[string]any{}); got != -1 {
		t.Fatalf("missing offset = %d, want -1", got)
	}
	if got := offsetArg(map[string]any{"offset": float64(0)}); got != 0 {
		t.Fatalf("explicit zero offset = %d, want 0", got)
	}
	if got := offsetArg(map[string]any{"offset": "0"}); got != 0 {
		t.Fatalf("string zero offset = %d, want 0", got)
	}
}

// S-REQ-ENUM 缺参枚举（ssh 同款，MCP_ACCEPTANCE §3.9）：一次报出全部缺失
// required 参数；单缺只点名其一；显式 null 视同缺失；present-but-空串不进
// 枚举（由逐参数精确校验点名）。
func TestMissingRequiredEnumeration(t *testing.T) {
	err := missingRequired(map[string]any{}, "connectionId", "action", "dn")
	if err == nil || err.Error() != "Missing required parameters: connectionId, action, dn" {
		t.Fatalf("double-missing must enumerate in schema order: %v", err)
	}
	err = missingRequired(map[string]any{"action": "delete"}, "connectionId", "action", "dn")
	if err == nil || err.Error() != "Missing required parameters: connectionId, dn" {
		t.Fatalf("partial missing must enumerate the gaps only: %v", err)
	}
	err = missingRequired(map[string]any{"connectionId": "c", "action": nil, "dn": "x"}, "connectionId", "action", "dn")
	if err == nil || err.Error() != "Missing required parameters: action" {
		t.Fatalf("explicit null must count as missing: %v", err)
	}
	if err := missingRequired(map[string]any{"connectionId": "c", "action": "delete", "dn": "x"}, "connectionId", "action", "dn"); err != nil {
		t.Fatalf("all present must pass: %v", err)
	}
}

// S-REQ-ENUM 工具入口接线：entry_write 三缺全点名、单缺只其一、空串走
// 精确点名（不混入枚举）。
func TestEntryWriteMissingParamsEnumerated(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	_, err := server.Call("ldap_entry_write", map[string]any{})
	if err == nil || err.Error() != "Missing required parameters: connectionId, action, dn" {
		t.Fatalf("entry_write {} must enumerate all three: %v", err)
	}
	_, err = server.Call("ldap_entry_write", map[string]any{"action": "delete", "dn": "uid=a,dc=x"})
	if err == nil || err.Error() != "Missing required parameters: connectionId" {
		t.Fatalf("single missing must name it only: %v", err)
	}
	// present-but-空串：精确点名 action（枚举不接管空串）；action 归一
	// 在连接解析之后，需先登记连接才会走到 action 分派。
	server2 := NewServer(ldapconn.NewService(), nil)
	connectLazy(t, server2, "enum-conn")
	_, err = server2.Call("ldap_entry_write", map[string]any{"connectionId": "enum-conn", "action": "", "dn": "uid=a,dc=x"})
	if err == nil || !strings.Contains(err.Error(), "action must be add, modify, delete, or modifyDn") {
		t.Fatalf("empty action must hit the precise per-param check: %v", err)
	}
}

// S-REQ-ENUM search_digest：filter 与 schema required 对齐（缺参枚举，不再
// 静默回退 (objectClass=*) 全扫）；双缺按 schema 顺序枚举。
func TestSearchDigestMissingParamsEnumerated(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	_, err := server.Call("ldap_search_digest", map[string]any{})
	if err == nil || err.Error() != "Missing required parameters: connectionId, filter" {
		t.Fatalf("digest {} must enumerate both in schema order: %v", err)
	}
	_, err = server.Call("ldap_search_digest", map[string]any{"connectionId": "c"})
	if err == nil || err.Error() != "Missing required parameters: filter" {
		t.Fatalf("digest without filter must enumerate filter: %v", err)
	}
	_, err = server.Call("ldap_search_digest", map[string]any{"connectionId": "c", "filter": nil})
	if err == nil || !strings.Contains(err.Error(), "Missing required parameters: filter") {
		t.Fatalf("null filter must count as missing: %v", err)
	}
	// present-but-空串：精确点名（RFC 4515 提示），不进枚举。
	_, err = server.Call("ldap_search_digest", map[string]any{"connectionId": "c", "filter": "  "})
	if err == nil || !strings.Contains(err.Error(), "filter is required (RFC 4515") {
		t.Fatalf("blank filter must hit the precise per-param check: %v", err)
	}
}
