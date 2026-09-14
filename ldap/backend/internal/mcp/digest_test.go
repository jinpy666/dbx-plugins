package mcp

// digest_test.go：本地聚合（设计 §3 原语 2/3；验收用例 S-DIG-*，清单见
// shared/frontend/README.zh-CN.md）。

import (
	"encoding/json"
	"strings"
	"testing"

	"io.dbx.ldap.plugin/internal/ldapconn"
)

func entry(dn string, attributes map[string][]string) ldapconn.LDAPEntry {
	return ldapconn.LDAPEntry{DN: dn, Attributes: attributes}
}

func TestAggregateObjectClassDistributionAndLimit(t *testing.T) {
	entries := []ldapconn.LDAPEntry{
		entry("uid=a,ou=people,dc=a", map[string][]string{"objectClass": {"inetOrgPerson", "top"}}),
		entry("uid=b,ou=people,dc=a", map[string][]string{"objectClass": {"inetOrgPerson", "top"}}),
		entry("ou=groups,dc=a", map[string][]string{"objectClass": {"organizationalUnit", "top"}}),
	}
	result := AggregateDigest(DigestInput{Entries: entries, BaseDN: "dc=a", GroupLimit: 20, TopN: 10, SampleRows: 5})
	if result.Matched != 3 {
		t.Fatalf("matched mismatch: %d", result.Matched)
	}
	if result.Stats.ObjectClass["inetOrgPerson"] != 2 || result.Stats.ObjectClass["organizationalUnit"] != 1 || result.Stats.ObjectClass["top"] != 3 {
		t.Fatalf("objectClass distribution mismatch: %+v", result.Stats.ObjectClass)
	}
	if result.Stats.ObjectClassLimit {
		t.Fatal("under the limit no truncation flag")
	}
}

func TestAggregateGroupLimitTruncates(t *testing.T) {
	entries := make([]ldapconn.LDAPEntry, 0, 25)
	for index := 0; index < 25; index++ {
		class := "class" + string(rune('a'+index))
		entries = append(entries, entry("cn=x,dc=a", map[string][]string{"objectClass": {class}}))
	}
	result := AggregateDigest(DigestInput{Entries: entries, BaseDN: "dc=a", GroupLimit: 20, TopN: 10, SampleRows: 5})
	if len(result.Stats.ObjectClass) != 20 || !result.Stats.ObjectClassLimit {
		t.Fatalf("group limit mismatch: %d %v", len(result.Stats.ObjectClass), result.Stats.ObjectClassLimit)
	}
}

func TestAggregateSubtreeCountsAndDistinct(t *testing.T) {
	entries := []ldapconn.LDAPEntry{
		entry("uid=a,ou=people,dc=a", map[string][]string{"mail": {"a@x", "dup@x"}}),
		entry("uid=b,ou=people,dc=a", map[string][]string{"mail": {"b@x", "dup@x"}}),
		entry("cn=g,ou=groups,dc=a", nil),
		entry("ou=groups,dc=a", nil),
	}
	result := AggregateDigest(DigestInput{
		Entries: entries, BaseDN: "dc=a", DistinctAttr: "mail",
		GroupLimit: 20, TopN: 10, SampleRows: 5,
	})
	// 子树计数：base 下直接子 DN 聚合（ou=people 收拢两个 uid）。
	if result.Stats.Subtrees["ou=people,dc=a"] != 2 || result.Stats.Subtrees["ou=groups,dc=a"] != 2 {
		t.Fatalf("subtree counts mismatch: %+v", result.Stats.Subtrees)
	}
	// distinct：值域统计 + 计数。
	if result.Stats.Distinct == nil || result.Stats.Distinct.ValueCount != 3 {
		t.Fatalf("distinct mismatch: %+v", result.Stats.Distinct)
	}
	if result.Stats.Distinct.Values["dup@x"] != 2 {
		t.Fatalf("distinct values mismatch: %+v", result.Stats.Distinct.Values)
	}
	// base 自身与 base 直接子条目各自成键（键 = base 直接子祖先或自身）。
	inside := AggregateDigest(DigestInput{
		Entries: []ldapconn.LDAPEntry{entry("dc=a", nil), entry("child,dc=a", nil)},
		BaseDN:  "dc=a", GroupLimit: 20, TopN: 10, SampleRows: 5,
	})
	if inside.Stats.Subtrees["dc=a"] != 1 || inside.Stats.Subtrees["child,dc=a"] != 1 {
		t.Fatalf("base entry subtree key mismatch: %+v", inside.Stats.Subtrees)
	}
	// base 之外的 DN 原样返回键。
	outside := subtreeKey("dc=b", "dc=a")
	if outside != "dc=b" {
		t.Fatalf("outside dn key mismatch: %q", outside)
	}
}

// S-DIG-ZERO filter 命中 0：stats 的 objectClass/subtrees 键恒在（空 map
// 也输出 {}，不带 omitempty 吞键）——AI 的响应形状在零命中时不漂移；
// distinct 段缺省省略（可选）。
func TestAggregateZeroMatchKeepsShapeKeys(t *testing.T) {
	result := AggregateDigest(DigestInput{Entries: nil, BaseDN: "dc=a", Filter: "(uid=nope)", GroupLimit: 20, TopN: 10, SampleRows: 5})
	if result.Matched != 0 || len(result.Sample) != 0 {
		t.Fatalf("zero match mismatch: %d %d", result.Matched, len(result.Sample))
	}
	payload, err := json.Marshal(result.Stats)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	objectClass, hasObjectClass := decoded["objectClass"].(map[string]any)
	subtrees, hasSubtrees := decoded["subtrees"].(map[string]any)
	if !hasObjectClass || len(objectClass) != 0 || !hasSubtrees || len(subtrees) != 0 {
		t.Fatalf("zero match must keep empty objectClass/subtrees objects: %s", payload)
	}
	if _, hasDistinct := decoded["distinct"]; hasDistinct {
		t.Fatal("distinct must stay absent without distinctAttr")
	}
}

func TestCellTruncationWidth(t *testing.T) {
	long := strings.Repeat("值", 200)
	truncated := DigestCellTruncate(long, 120)
	if len([]rune(truncated)) != 121 || !strings.HasSuffix(truncated, "…") {
		t.Fatalf("cell truncation mismatch: %d runes", len([]rune(truncated)))
	}
	if DigestCellTruncate("short", 120) != "short" {
		t.Fatal("short values must pass through")
	}
	if DigestCellTruncate("anything", 0) != "anything" {
		t.Fatal("width<=0 disables truncation")
	}
}

func TestProjectEntryKeepsDnUntruncated(t *testing.T) {
	entryDN := "uid=" + strings.Repeat("a", 300) + ",dc=a"
	projected := ProjectEntry(entry(entryDN, map[string][]string{"cn": {strings.Repeat("b", 300)}}), 120)
	if projected["dn"] != entryDN {
		t.Fatal("DN locator field must never be truncated")
	}
	values := projected["attributes"].(map[string]any)["cn"].([]string)
	if len([]rune(values[0])) != 121 {
		t.Fatalf("attribute cell should be truncated: %d", len([]rune(values[0])))
	}
}

func TestAggregateSampleClamped(t *testing.T) {
	entries := make([]ldapconn.LDAPEntry, 50)
	for index := range entries {
		entries[index] = entry("dn", nil)
	}
	result := AggregateDigest(DigestInput{Entries: entries, GroupLimit: 20, TopN: 10, SampleRows: 5})
	if len(result.Sample) != 5 {
		t.Fatalf("sample clamp mismatch: %d", len(result.Sample))
	}
	few := AggregateDigest(DigestInput{Entries: entries[:2], GroupLimit: 20, TopN: 10, SampleRows: 5})
	if len(few.Sample) != 2 {
		t.Fatalf("sample below cap mismatch: %d", len(few.Sample))
	}
}

// S-DIG-PROJ digest 远端投影组装：投影 + objectClass + distinctAttr 三者
// 去重合流——distinctAttr 单独给（不带 attributes）时必须进投影，否则
// distinct 聚合恒空（容器实测 M15 的单测锚点）。
func TestBuildDigestSearchAttrs(t *testing.T) {
	onlyDistinct := buildDigestSearchAttrs(nil, "mail")
	if len(onlyDistinct) != 2 || !containsFold(onlyDistinct, "objectClass") || !containsFold(onlyDistinct, "mail") {
		t.Fatalf("distinct-only must project distinctAttr + objectClass: %v", onlyDistinct)
	}
	// 投影里已有时不重复追加（大小写不敏感）。
	deduped := buildDigestSearchAttrs([]string{"Mail", "cn"}, "mail")
	if len(deduped) != 3 {
		t.Fatalf("duplicate distinctAttr must not repeat: %v", deduped)
	}
	// 无 distinctAttr：保持「投影 + objectClass」既有形状。
	plain := buildDigestSearchAttrs([]string{"cn"}, "")
	if len(plain) != 2 || !containsFold(plain, "objectClass") || plain[0] != "cn" {
		t.Fatalf("plain projection mismatch: %v", plain)
	}
}
