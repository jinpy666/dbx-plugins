package ldapconn

import (
	"strings"
	"testing"
	"time"
)

// OpenLDAP 风格 subschema 条目样例（含引号 DESC、多值 NAME、MUST/MAY 列表）。
// 注意：样例刻意不用字面值 'name' 作属性名——上游 tiny-rdm 解析器（原样移植）
// 中 NAME 的值若与关键字同名（如 'name'）会被 isLDAPSchemaKeyword 截断，
// 见 TestParseLDAPAttributeTypesUpstreamQuirk。
var sampleSubschemaEntry = LDAPEntry{
	DN: "cn=Subschema",
	Attributes: map[string][]string{
		"attributeTypes": {
			`( 2.5.4.41 NAME 'fullName' DESC 'name(s) associated with the object' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15{32768} )`,
			`( 2.5.4.3 NAME ( 'cn' 'commonName' ) SUP name )`,
			`( 1.2.3.4 NAME 'userPassword' EQUALITY octetStringMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.40 )`,
			`( 0.9.2342.19200300.100.1.3 NAME ( 'mail' 'rfc822Mailbox' ) DESC 'RFC1274: RFC822 Mailbox' )`,
		},
		"objectClasses": {
			`( 2.5.6.6 NAME ( 'person' 'personObject' ) DESC 'RFC2256: a person' SUP top STRUCTURAL MUST ( sn $ cn ) MAY ( userPassword $ telephoneNumber ) )`,
			`( 2.16.840.1.113730.3.2.2 NAME 'inetOrgPerson' SUP person STRUCTURAL MAY ( mail $ uid ) )`,
		},
	},
}

func TestTokenizeLDAPSchemaValue(t *testing.T) {
	tokens := tokenizeLDAPSchemaValue(`( 2.5.4.3 NAME ( 'cn' 'commonName' ) DESC 'has \(parens\) inside' SUP name )`)
	want := []string{"2.5.4.3", "NAME", "cn", "commonName", "DESC", "has (parens) inside", "SUP", "name"}
	if len(tokens) != len(want) {
		t.Fatalf("tokens = %q, want %q", tokens, want)
	}
	for i := range want {
		if tokens[i] != want[i] {
			t.Errorf("tokens[%d] = %q, want %q", i, tokens[i], want[i])
		}
	}
	// 空串
	if got := tokenizeLDAPSchemaValue(""); len(got) != 0 {
		t.Errorf("empty value tokens = %v", got)
	}
}

func TestParseLDAPAttributeTypes(t *testing.T) {
	values := sampleSubschemaEntry.Attributes["attributeTypes"]
	items := parseLDAPAttributeTypes(values)
	if len(items) != 4 {
		t.Fatalf("items len = %d, want 4", len(items))
	}
	// 排序：按 Name 小写（期望顺序：cn, fullName, mail, userPassword）
	if items[0].Name != "cn" || items[1].Name != "fullName" || items[2].Name != "mail" || items[3].Name != "userPassword" {
		names := []string{items[0].Name, items[1].Name, items[2].Name, items[3].Name}
		t.Errorf("order = %v", names)
	}
	cn := items[0]
	if cn.OID != "2.5.4.3" {
		t.Errorf("cn oid = %q", cn.OID)
	}
	if len(cn.Names) != 2 || cn.Names[0] != "cn" || cn.Names[1] != "commonName" {
		t.Errorf("cn names = %v", cn.Names)
	}
	mail := items[2]
	if mail.Description != "RFC1274: RFC822 Mailbox" {
		t.Errorf("mail desc = %q", mail.Description)
	}
	fullName := items[1]
	if fullName.Description != "name(s) associated with the object" {
		t.Errorf("fullName desc = %q", fullName.Description)
	}
	// 无 NAME 的条目回退 OID
	oidOnly := parseLDAPAttributeTypes([]string{"( 1.2.3.5 DESC 'no name' )"})
	if len(oidOnly) != 1 || oidOnly[0].Name != "1.2.3.5" {
		t.Errorf("oid fallback = %+v", oidOnly)
	}
}

// TestParseLDAPAttributeTypesUpstreamQuirk 记录 tiny-rdm 原样移植的已知边界：
// NAME 的值与关键字同名（'name'、'desc' 等）时被 isLDAPSchemaKeyword 截断，
// Names 为空、Name 回退 OID。保留上游行为（原样移植原则），前端样例数据避开。
func TestParseLDAPAttributeTypesUpstreamQuirk(t *testing.T) {
	items := parseLDAPAttributeTypes([]string{`( 2.5.4.41 NAME 'name' DESC 'x' )`})
	if len(items) != 1 {
		t.Fatalf("items len = %d", len(items))
	}
	if items[0].Name != "2.5.4.41" || len(items[0].Names) != 0 {
		t.Errorf("quirk expectation changed: %+v", items[0])
	}
}

func TestParseLDAPObjectClasses(t *testing.T) {
	items := parseLDAPObjectClasses(sampleSubschemaEntry.Attributes["objectClasses"])
	person, ok := items["person"]
	if !ok {
		t.Fatalf("person missing: %v", items)
	}
	if person.OID != "2.5.6.6" {
		t.Errorf("person oid = %q", person.OID)
	}
	if strings.Join(person.Must, ",") != "sn,cn" {
		t.Errorf("person must = %v", person.Must)
	}
	if strings.Join(person.May, ",") != "userPassword,telephoneNumber" {
		t.Errorf("person may = %v", person.May)
	}
	// 别名索引：personObject 是 person 的别名
	if alias, ok := items["personObject"]; !ok || alias.Name != "person" {
		t.Errorf("alias personObject = %+v", alias)
	}
	iop := items["inetOrgPerson"]
	if strings.Join(iop.May, ",") != "mail,uid" {
		t.Errorf("inetOrgPerson may = %v", iop.May)
	}
}

func TestParseLDAPSchemaMetadata(t *testing.T) {
	metadata := parseLDAPSchemaMetadata("cn=Subschema", sampleSubschemaEntry)
	if metadata.SubschemaSubentry != "cn=Subschema" {
		t.Errorf("subschemaSubentry = %q", metadata.SubschemaSubentry)
	}
	// attributeNames 去重排序：cn, commonName, fullName, mail, rfc822Mailbox, userPassword
	want := "cn,commonName,fullName,mail,rfc822Mailbox,userPassword"
	if got := strings.Join(metadata.AttributeNames, ","); got != want {
		t.Errorf("attributeNames = %q, want %q", got, want)
	}
}

func TestFilterLDAPSchemaMetadataForProfile(t *testing.T) {
	profile := Profile{} // 默认屏蔽表含 userPassword
	metadata := filterLDAPSchemaMetadataForProfile(profile, parseLDAPSchemaMetadata("cn=Subschema", sampleSubschemaEntry))

	for _, name := range metadata.AttributeNames {
		if strings.EqualFold(name, "userPassword") {
			t.Errorf("userPassword should be filtered from attributeNames")
		}
	}
	for _, attr := range metadata.AttributeTypes {
		if strings.EqualFold(attr.Name, "userPassword") {
			t.Errorf("userPassword should be filtered from attributeTypes")
		}
	}
	person := metadata.ObjectClassAttributes["person"]
	for _, m := range person.May {
		if strings.EqualFold(m, "userPassword") {
			t.Errorf("userPassword should be filtered from person.May")
		}
	}
	if len(person.Must) != 2 {
		t.Errorf("person.Must should be intact, got %v", person.Must)
	}
}

func TestRootDSEAndSchemaAllowed(t *testing.T) {
	restricted := Profile{AllowedBaseDNs: []string{"dc=example,dc=com"}}
	open := Profile{}

	if err := rootDSEAllowed(open); err != nil {
		t.Errorf("open profile rootDSE should be allowed: %v", err)
	}
	if err := rootDSEAllowed(restricted); err == nil ||
		!strings.Contains(err.Error(), "disabled when allowed base DNs are configured") {
		t.Errorf("restricted rootDSE error = %v", err)
	}
	if err := schemaAllowed(open); err != nil {
		t.Errorf("open profile schema should be allowed: %v", err)
	}
	if err := schemaAllowed(restricted); err == nil {
		t.Errorf("restricted schema should be rejected")
	}
}

func TestSubschemaDNFromRootDSE(t *testing.T) {
	if dn, err := subschemaDNFromRootDSE(LDAPEntry{Attributes: map[string][]string{
		"subschemaSubentry": {"cn=Subschema"},
	}}); err != nil || dn != "cn=Subschema" {
		t.Errorf("dn = %q, err = %v", dn, err)
	}
	if _, err := subschemaDNFromRootDSE(LDAPEntry{Attributes: map[string][]string{}}); err == nil ||
		err.Error() != "RootDSE subschemaSubentry is not available" {
		t.Errorf("missing subschemaSubentry error = %v", err)
	}
}

func TestResolveSchema(t *testing.T) {
	profile := Profile{}
	rootDSE := LDAPEntry{Attributes: map[string][]string{"subschemaSubentry": {"cn=Subschema"}}}

	metadata, err := ResolveSchema(profile, rootDSE, sampleSubschemaEntry)
	if err != nil {
		t.Fatalf("ResolveSchema error = %v", err)
	}
	if metadata.SubschemaSubentry != "cn=Subschema" || len(metadata.AttributeTypes) != 3 {
		t.Errorf("metadata = %+v", metadata)
	}

	// RootDSE 缺 subschemaSubentry
	if _, err := ResolveSchema(profile, LDAPEntry{}, sampleSubschemaEntry); err == nil {
		t.Errorf("missing rootDSE subschemaSubentry should error")
	}

	// 白名单禁用
	restricted := Profile{AllowedBaseDNs: []string{"dc=example,dc=com"}}
	if _, err := ResolveSchema(restricted, rootDSE, sampleSubschemaEntry); err == nil {
		t.Errorf("restricted profile schema should be rejected")
	}

	// subschemaSubentry 落在白名单外：schemaDN = cn=Subschema 不在
	// dc=example,dc=com 之下 → ensureLDAPReadAllowed 拒绝
	if _, err := ResolveSchema(Profile{AllowedBaseDNs: []string{"dc=example,dc=com"}}, rootDSE, sampleSubschemaEntry); err == nil {
		t.Errorf("schema DN outside whitelist should be rejected")
	}
}

func TestSchemaCache(t *testing.T) {
	cache := NewSchemaCache(50 * time.Millisecond)
	metadata := LDAPSchemaMetadata{SubschemaSubentry: "cn=Subschema", AttributeNames: []string{"cn"}}

	if cache.Get("conn-1") != nil {
		t.Errorf("empty cache should miss")
	}
	cache.Put("conn-1", metadata)
	got := cache.Get("conn-1")
	if got == nil || got.SubschemaSubentry != "cn=Subschema" {
		t.Fatalf("cache hit = %+v", got)
	}
	// 深拷贝语义：修改返回值不影响缓存
	got.AttributeNames[0] = "mutated"
	if again := cache.Get("conn-1"); again.AttributeNames[0] != "cn" {
		t.Errorf("cache entry mutated: %+v", again)
	}
	// 隔离
	if cache.Get("conn-2") != nil {
		t.Errorf("conn-2 should miss")
	}
	// 过期
	time.Sleep(60 * time.Millisecond)
	if cache.Get("conn-1") != nil {
		t.Errorf("expired entry should miss")
	}
	// Invalidate
	cache.Put("conn-1", metadata)
	cache.Invalidate("conn-1")
	if cache.Get("conn-1") != nil {
		t.Errorf("invalidated entry should miss")
	}
}

// —— schema 缓存与连接生命周期（第 3 轮审查补充） ————————————————

// schema 读取需要真连接，这里只验证缓存失效路径：Put 一个已知条目，
// 走连接生命周期动作，断言缓存被清除（不 dial 网络）。
func TestSchemaCacheInvalidatedByDisconnect(t *testing.T) {
	svc := NewService()
	connectProfile(t, svc, "c1", nil)
	svc.SchemaCache.Put("c1", LDAPSchemaMetadata{SubschemaSubentry: "cn=Subschema"})
	if svc.SchemaCache.Get("c1") == nil {
		t.Fatalf("precondition: cache should hold c1 entry")
	}
	svc.Disconnect("c1")
	if svc.SchemaCache.Get("c1") != nil {
		t.Errorf("schema cache must be invalidated on disconnect")
	}
	// 幂等：重复 disconnect 不 panic、不残留
	svc.Disconnect("c1")
}

// 缺陷回归：同 id 重复 connect（连接编辑后重连，服务器/凭据可能已变）时
// 旧 schema 缓存必须失效，否则 TTL 内会继续返回上一台服务器的元数据。
func TestSchemaCacheInvalidatedByConnectOverwrite(t *testing.T) {
	svc := NewService()
	connectProfile(t, svc, "c1", nil)
	svc.SchemaCache.Put("c1", LDAPSchemaMetadata{SubschemaSubentry: "cn=Subschema"})
	connectProfile(t, svc, "c1", map[string]any{"base_dn": "dc=example,dc=com"})
	if svc.SchemaCache.Get("c1") != nil {
		t.Errorf("schema cache must be invalidated when connect overwrites the same connection id")
	}
}

func TestSchemaCacheDefaultTTLAndIndependentKeys(t *testing.T) {
	// ttl<=0 落回 10 分钟默认值（NewService 路径）。
	cache := NewSchemaCache(0)
	if cache.ttl != 10*time.Minute {
		t.Errorf("default ttl = %v", cache.ttl)
	}
	cache.Put("a", LDAPSchemaMetadata{AttributeNames: []string{"cn"}})
	cache.Put("b", LDAPSchemaMetadata{AttributeNames: []string{"sn"}})
	if got := cache.Get("a"); got == nil || got.AttributeNames[0] != "cn" {
		t.Errorf("key a = %+v", got)
	}
	if got := cache.Get("b"); got == nil || got.AttributeNames[0] != "sn" {
		t.Errorf("key b = %+v", got)
	}
	cache.Invalidate("a")
	if cache.Get("a") != nil || cache.Get("b") == nil {
		t.Errorf("invalidate must be key-scoped")
	}
	// 深拷贝：ObjectClassAttributes map / AttributeTypes slice 隔离。
	cache.Put("a", LDAPSchemaMetadata{
		ObjectClassAttributes: map[string]LDAPSchemaObjectClassAttributes{
			"person": {Must: []string{"sn"}},
		},
		AttributeTypes: []LDAPSchemaAttributeType{{Name: "cn", Names: []string{"cn"}}},
	})
	got := cache.Get("a")
	got.ObjectClassAttributes["person"].Must[0] = "mutated"
	got.AttributeTypes[0].Names[0] = "mutated"
	again := cache.Get("a")
	if again.ObjectClassAttributes["person"].Must[0] != "sn" || again.AttributeTypes[0].Names[0] != "cn" {
		t.Errorf("cache entry mutated through deep copy: %+v", again)
	}
}
