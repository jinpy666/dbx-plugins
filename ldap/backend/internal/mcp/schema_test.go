package mcp

// schema_test.go：ldap_ui_schema 响应形状（S-SCHEMA-*）。截断语义纯函数
// renderSchemaNames 直接断言；缓存冷/热/TTL 在 ldapconn/schema_test.go 与
// 容器场景 M16 覆盖。

import (
	"strconv"
	"strings"
	"testing"

	"io.dbx.ldap.plugin/internal/ldapconn"
)

func schemaMetadata(attrNames []string, classNames []string) ldapconn.LDAPSchemaMetadata {
	metadata := ldapconn.LDAPSchemaMetadata{AttributeNames: attrNames, ObjectClassAttributes: map[string]ldapconn.LDAPSchemaObjectClassAttributes{}}
	for _, name := range classNames {
		metadata.ObjectClassAttributes[name] = ldapconn.LDAPSchemaObjectClassAttributes{}
	}
	return metadata
}

// S-SCHEMA-1 大 schema：属性名超 300 截断且恰好 300 + truncated 标志；
// objectClass 不超限时原样输出且标志为 false。
func TestRenderSchemaNamesTruncatesAtLimit(t *testing.T) {
	attrNames := make([]string, 0, schemaNameLimit+7)
	for index := 0; index < schemaNameLimit+7; index++ {
		attrNames = append(attrNames, "attr"+strings.Repeat("x", 1)+"-"+strconv.Itoa(index))
	}
	classNames := []string{"inetOrgPerson", "top", "organizationalUnit"}
	rendered := renderSchemaNames(schemaMetadata(attrNames, classNames))
	if got := len(rendered["attributeNames"].([]string)); got != schemaNameLimit {
		t.Fatalf("attributeNames must clamp to %d, got %d", schemaNameLimit, got)
	}
	if rendered["attributeNamesTruncated"] != true {
		t.Fatal("attributeNamesTruncated must be true above the limit")
	}
	if got := len(rendered["objectClassNames"].([]string)); got != 3 {
		t.Fatalf("objectClassNames under the limit must pass through: %d", got)
	}
	if rendered["objectClassTruncated"] != false {
		t.Fatal("objectClassTruncated must be false under the limit")
	}
	// objectClassNames 排序输出（稳定形状）。
	classList := rendered["objectClassNames"].([]string)
	for index := 1; index < len(classList); index++ {
		if classList[index-1] > classList[index] {
			t.Fatalf("objectClassNames must be sorted: %v", classList)
		}
	}
}

// S-SCHEMA-2 边界恰好 300：不置截断标志（截断 = 真的丢了名字）。
func TestRenderSchemaNamesExactlyAtLimit(t *testing.T) {
	attrNames := make([]string, 0, schemaNameLimit)
	for index := 0; index < schemaNameLimit; index++ {
		attrNames = append(attrNames, "attr-"+strconv.Itoa(index))
	}
	rendered := renderSchemaNames(schemaMetadata(attrNames, nil))
	if len(rendered["attributeNames"].([]string)) != schemaNameLimit || rendered["attributeNamesTruncated"] != false {
		t.Fatalf("at-limit must not flag truncation: %v", rendered["attributeNamesTruncated"])
	}
}

// S-SCHEMA-3 空 schema（条目缺 subschema 属性的极端情况）：空清单 + false，
// 不 panic。
func TestRenderSchemaNamesEmpty(t *testing.T) {
	rendered := renderSchemaNames(schemaMetadata(nil, nil))
	if len(rendered["attributeNames"].([]string)) != 0 || len(rendered["objectClassNames"].([]string)) != 0 {
		t.Fatalf("empty schema must render empty lists: %#v", rendered)
	}
	if rendered["attributeNamesTruncated"] != false || rendered["objectClassTruncated"] != false {
		t.Fatalf("empty schema must not flag truncation: %#v", rendered)
	}
}
