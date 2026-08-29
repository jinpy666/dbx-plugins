package ldapconn

// policy_test.go：§8 测试计划——纯逻辑单测（不连网）。
// 覆盖：validateLDAPFilter 合法/非法表、dnWithinBase 边界、白名单组合
// （read_only × 白名单 × 写白名单回退）、屏蔽属性过滤（默认表 + 配置追加）、
// normalizeLDAPWriteValues 写值校验、DN/变更归一化、modifyDn 目标 DN。

import (
	"strings"
	"testing"
)

func TestValidateLDAPFilter(t *testing.T) {
	valid := []string{
		"(objectClass=*)",
		"(cn=test)",
		"(&(objectClass=person)(cn=abc))",
		"(|(cn=a)(sn=b))",
		"(!(cn=x))",
		"(cn>=5)",
		"(cn<=5)",
		"(cn~=abc)",
		"(cn=*)",
		"(cn=a*b*c)",
		"(cn=\\2a)",
		"(:dn:2.5.13.2:=x)",
		"(cn:caseExactMatch:=x)",
	}
	for _, filter := range valid {
		if err := validateLDAPFilter(filter); err != nil {
			t.Errorf("validateLDAPFilter(%q) = %v, want nil", filter, err)
		}
	}

	invalid := map[string]string{
		"":              "filter is required",
		"   ":           "filter is required",
		"objectClass=*": "invalid ldap filter",
		"(unclosed":     "invalid ldap filter",
		")(":            "invalid ldap filter",
		"(cn=a))(b)":    "invalid ldap filter",
	}
	for filter, wantPrefix := range invalid {
		err := validateLDAPFilter(filter)
		if err == nil {
			t.Errorf("validateLDAPFilter(%q) = nil, want error", filter)
			continue
		}
		if !strings.HasPrefix(err.Error(), wantPrefix) {
			t.Errorf("validateLDAPFilter(%q) error = %q, want prefix %q", filter, err.Error(), wantPrefix)
		}
	}
}

func TestNormalizeLDAPWriteDN(t *testing.T) {
	cases := []struct {
		raw    string
		want   string
		wantEr bool
	}{
		{"", "", true},
		{"   ", "", true},
		{"cn=Test,dc=example,dc=com", "cn=Test,dc=example,dc=com", false},
		{"  cn=Test,dc=example,dc=com  ", "cn=Test,dc=example,dc=com", false},
		{"not a dn", "", true},
	}
	for _, c := range cases {
		got, err := normalizeLDAPWriteDN(c.raw)
		if c.wantEr {
			if err == nil {
				t.Errorf("normalizeLDAPWriteDN(%q) = %q, want error", c.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeLDAPWriteDN(%q) error = %v", c.raw, err)
			continue
		}
		if got != c.want {
			t.Errorf("normalizeLDAPWriteDN(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestDNWithinBase(t *testing.T) {
	cases := []struct {
		name    string
		dn      string
		base    string
		want    bool
		wantErr bool
	}{
		{"empty base allows all", "cn=x,dc=a,dc=com", "", true, false},
		{"empty base allows empty dn", "", "", true, false},
		{"exact match", "dc=example,dc=com", "dc=example,dc=com", true, false},
		{"case insensitive", "dc=Example,dc=COM", "dc=example,dc=com", true, false},
		{"child", "ou=people,dc=example,dc=com", "dc=example,dc=com", true, false},
		{"deep child", "cn=john,ou=people,dc=example,dc=com", "dc=example,dc=com", true, false},
		{"shallower than base", "dc=com", "dc=example,dc=com", false, false},
		{"sibling", "dc=example,dc=org", "dc=example,dc=com", false, false},
		{"unrelated", "ou=other,c=us", "dc=example,dc=com", false, false},
		{"rdn order is tree path", "dc=com,dc=example", "dc=example,dc=com", false, false},
		{"multi-valued rdn", "cn=a+sn=b,dc=example,dc=com", "dc=example,dc=com", true, false},
		{"multi-valued rdn order", "sn=b+cn=a,dc=example,dc=com", "dc=example,dc=com", true, false},
		// 多值 RDN 比较在 canonicalRDN 内部排序后进行（base 同位多值才覆盖该语义）
		{"multi-valued base match", "cn=a+sn=b,dc=example,dc=com", "sn=b+cn=a,dc=example,dc=com", true, false},
		{"multi-valued base differs", "cn=a+sn=b,dc=example,dc=com", "cn=b+sn=a,dc=example,dc=com", false, false},
		{"spaces trimmed", " cn=x , dc=example , dc=com ", "dc=example,dc=com", true, false},
		{"invalid dn", "not a dn", "dc=example,dc=com", false, true},
		{"invalid base", "cn=x", "not a dn", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := dnWithinBase(c.dn, c.base)
			if c.wantErr != (err != nil) {
				t.Fatalf("dnWithinBase(%q,%q) err = %v, wantErr %v", c.dn, c.base, err, c.wantErr)
			}
			if got != c.want {
				t.Errorf("dnWithinBase(%q,%q) = %v, want %v", c.dn, c.base, got, c.want)
			}
		})
	}
}

func TestDNWithinAnyBase(t *testing.T) {
	bases := []string{"dc=example,dc=com", "ou=people,dc=corp,dc=com"}
	cases := []struct {
		dn   string
		want bool
	}{
		{"cn=x,dc=example,dc=com", true},
		{"cn=x,ou=people,dc=corp,dc=com", true},
		{"cn=x,dc=other,dc=com", false},
	}
	for _, c := range cases {
		if got := dnWithinAnyBase(c.dn, bases); got != c.want {
			t.Errorf("dnWithinAnyBase(%q) = %v, want %v", c.dn, got, c.want)
		}
	}
	// 空白名单不匹配任何 DN（"空 = 不限"语义由 ensureLDAPReadAllowed 处理）
	if dnWithinAnyBase("any", nil) {
		t.Errorf("dnWithinAnyBase with nil bases should be false")
	}
}

func TestEnsureLDAPReadAllowed(t *testing.T) {
	profile := Profile{
		AllowedBaseDNs: []string{"DC=Example,DC=Com", "ou=people,dc=corp,dc=com"},
	}
	// 空 DN / 空白名单 = 不限
	if err := ensureLDAPReadAllowed(profile, ""); err != nil {
		t.Errorf("empty dn should be allowed, got %v", err)
	}
	if err := ensureLDAPReadAllowed(Profile{}, "cn=x,dc=any,dc=com"); err != nil {
		t.Errorf("empty whitelist should be unrestricted, got %v", err)
	}
	// 白名单内（大小写不敏感）
	if err := ensureLDAPReadAllowed(profile, "cn=john,dc=example,dc=com"); err != nil {
		t.Errorf("in-whitelist dn rejected: %v", err)
	}
	if err := ensureLDAPReadAllowed(profile, "cn=j,DC=Example,DC=Com"); err != nil {
		t.Errorf("case-insensitive whitelist dn rejected: %v", err)
	}
	// 白名单外
	err := ensureLDAPReadAllowed(profile, "cn=x,dc=outside,dc=com")
	if err == nil || !strings.Contains(err.Error(), "outside allowed base DNs") {
		t.Errorf("out-of-whitelist dn error = %v", err)
	}
}

func TestEnsureLDAPWriteAllowed(t *testing.T) {
	readOnly := Profile{Name: "ro", ReadOnly: true, AllowedBaseDNs: []string{"dc=example,dc=com"}}
	if err := ensureLDAPWriteAllowed(readOnly, "cn=x,dc=example,dc=com", nil); err == nil ||
		!strings.Contains(err.Error(), "read-only") {
		t.Errorf("read-only profile write should be rejected, got %v", err)
	}

	profile := Profile{Name: "rw", AllowedBaseDNs: []string{"dc=example,dc=com"}}

	// 读白名单回退：写白名单为空时用读白名单
	if err := ensureLDAPWriteAllowed(profile, "cn=x,dc=example,dc=com", nil); err != nil {
		t.Errorf("write within read whitelist rejected: %v", err)
	}
	if err := ensureLDAPWriteAllowed(profile, "cn=x,dc=outside,dc=com", nil); err == nil {
		t.Errorf("write outside whitelist should be rejected")
	}

	// 写白名单覆盖读白名单：写白名单放行读白名单外的 DN
	wide := Profile{Name: "wide", AllowedBaseDNs: []string{"dc=example,dc=com"}, AllowedWriteBaseDNs: []string{"dc=other,dc=com"}}
	if err := ensureLDAPWriteAllowed(wide, "cn=x,dc=other,dc=com", nil); err != nil {
		t.Errorf("write whitelist should override read whitelist: %v", err)
	}
	if err := ensureLDAPWriteAllowed(wide, "cn=x,dc=example,dc=com", nil); err == nil {
		t.Errorf("write outside write-whitelist should be rejected even if read-allowed")
	}

	// 空 DN 拒绝
	if err := ensureLDAPWriteAllowed(profile, "", nil); err == nil || !strings.Contains(err.Error(), "dn is required") {
		t.Errorf("empty dn write error = %v", err)
	}

	// 屏蔽属性拒绝（默认表）
	if err := ensureLDAPWriteAllowed(profile, "cn=x,dc=example,dc=com", []string{"cn", "userPassword"}); err == nil ||
		!strings.Contains(err.Error(), "blocked") {
		t.Errorf("blocked attribute write error = %v", err)
	}
	// 大小写不敏感
	if err := ensureLDAPWriteAllowed(profile, "cn=x,dc=example,dc=com", []string{"USERPASSWORD"}); err == nil {
		t.Errorf("blocked attribute should be case-insensitive")
	}
	// 普通属性放行
	if err := ensureLDAPWriteAllowed(profile, "cn=x,dc=example,dc=com", []string{"cn", "sn", "mail"}); err != nil {
		t.Errorf("normal attributes should be allowed: %v", err)
	}
}

func TestFirstBlockedLDAPAttribute(t *testing.T) {
	profile := Profile{BlockedAttributes: []string{"customSecret"}}
	cases := []struct {
		attrs []string
		want  string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"cn", "sn"}, ""},
		{[]string{"cn", "userPassword"}, "userPassword"},
		{[]string{"unicodePwd"}, "unicodePwd"},
		{[]string{"OBJECTGUID"}, "OBJECTGUID"},         // 大小写不敏感
		{[]string{" customSecret "}, " customSecret "}, // 配置追加项；返回原始写法（tiny-rdm :1908 原样）
		{[]string{"token"}, "token"},
		{[]string{""}, ""}, // 空属性名跳过
	}
	for _, c := range cases {
		if got := firstBlockedLDAPAttribute(profile, c.attrs); got != c.want {
			t.Errorf("firstBlockedLDAPAttribute(%v) = %q, want %q", c.attrs, got, c.want)
		}
	}
	// 默认表共 11 项
	defaults := DefaultLDAPBlockedAttributes()
	if len(defaults) != 11 {
		t.Errorf("default blocked attributes len = %d, want 11", len(defaults))
	}
	for _, name := range []string{"userPassword", "unicodePwd", "password", "pwd", "secret", "token", "apiKey", "privateKey", "objectSid", "objectGUID", "memberOf"} {
		found := false
		for _, d := range defaults {
			if strings.EqualFold(d, name) {
				found = true
			}
		}
		if !found {
			t.Errorf("default table missing %q", name)
		}
	}
}

func TestSanitizeLDAPAttributes(t *testing.T) {
	profile := Profile{BlockedAttributes: []string{"internalRef"}}
	got := sanitizeLDAPAttributes(profile, []string{"cn", "userPassword", "sn", "InternalRef", "mail"})
	want := []string{"cn", "sn", "mail"}
	if len(got) != len(want) {
		t.Fatalf("sanitizeLDAPAttributes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sanitizeLDAPAttributes[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if out := sanitizeLDAPAttributes(profile, nil); out != nil {
		t.Errorf("sanitizeLDAPAttributes(nil) = %v, want nil", out)
	}
}

func TestFilterLDAPEntryBlockedAttributes(t *testing.T) {
	profile := Profile{}
	entry := LDAPEntry{
		DN: "cn=x,dc=example,dc=com",
		Attributes: map[string][]string{
			"cn":           {"x"},
			"userPassword": {"s3cret"},
			"objectSid":    {"S-1-5"},
			"mail":         {"x@example.com"},
		},
	}
	filtered := filterLDAPEntryBlockedAttributes(profile, entry)
	if _, ok := filtered.Attributes["userPassword"]; ok {
		t.Errorf("userPassword should be filtered from result")
	}
	if _, ok := filtered.Attributes["objectSid"]; ok {
		t.Errorf("objectSid should be filtered from result")
	}
	if v, ok := filtered.Attributes["mail"]; !ok || len(v) != 1 {
		t.Errorf("mail should be kept, got %v", filtered.Attributes)
	}
	if v, ok := filtered.Attributes["cn"]; !ok || len(v) != 1 || v[0] != "x" {
		t.Errorf("cn should be kept, got %v", filtered.Attributes)
	}
	// nil attributes 保留 nil
	nilEntry := filterLDAPEntryBlockedAttributes(profile, LDAPEntry{DN: "cn=x"})
	if nilEntry.Attributes != nil {
		t.Errorf("nil attributes should stay nil, got %v", nilEntry.Attributes)
	}
}

func TestNormalizeLDAPWriteValues(t *testing.T) {
	// 合法：原样透传
	values, err := normalizeLDAPWriteValues("cn", []string{"John", "  spaced  "})
	if err != nil {
		t.Fatalf("normalizeLDAPWriteValues error = %v", err)
	}
	if len(values) != 2 || values[0] != "John" || values[1] != "  spaced  " {
		t.Errorf("values = %v", values)
	}

	errors := []struct {
		attr   string
		values []string
		want   string
	}{
		{"cn", nil, `attribute "cn" requires values`},
		{"cn", []string{}, `attribute "cn" requires values`},
		{"cn", []string{"ok", ""}, `attribute "cn" value 1 cannot be empty`},
		{"cn", []string{"   "}, `attribute "cn" value 0 cannot be empty`},
	}
	for _, c := range errors {
		_, err := normalizeLDAPWriteValues(c.attr, c.values)
		if err == nil || err.Error() != c.want {
			t.Errorf("normalizeLDAPWriteValues(%q,%v) error = %v, want %q", c.attr, c.values, err, c.want)
		}
	}
}

func TestNormalizeLDAPAddAttributes(t *testing.T) {
	// 空 attributes
	if _, _, err := normalizeLDAPAddAttributes(nil); err == nil || err.Error() != "attributes is required" {
		t.Errorf("nil attrs error = %v", err)
	}
	normalized, names, err := normalizeLDAPAddAttributes(map[string][]string{
		"sn":   {"Doe"},
		"cn":   {"John"},
		"mail": {" j@x.com "},
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if strings.Join(names, ",") != "cn,mail,sn" {
		t.Errorf("names = %v, want sorted [cn mail sn]", names)
	}
	if len(normalized["mail"]) != 1 || normalized["mail"][0] != " j@x.com " {
		t.Errorf("mail values = %v", normalized["mail"])
	}

	errCases := []struct {
		attrs map[string][]string
		want  string
	}{
		{map[string][]string{"cn": {}}, `attribute "cn" requires values`},
		{map[string][]string{"cn": {""}}, `attribute "cn" value 0 cannot be empty`},
		{map[string][]string{"  ": {"x"}}, "attribute name cannot be empty"},
		{map[string][]string{"CN": {"a"}, "cn": {"b"}}, `duplicate attribute "cn"`}, // 报错用 trim 后的属性名（tiny-rdm 原样）
	}
	for _, c := range errCases {
		_, _, err := normalizeLDAPAddAttributes(c.attrs)
		if err == nil || err.Error() != c.want {
			t.Errorf("normalizeLDAPAddAttributes(%v) error = %v, want %q", c.attrs, err, c.want)
		}
	}
}

func TestNormalizeLDAPModifyChanges(t *testing.T) {
	if _, _, err := normalizeLDAPModifyChanges(nil); err == nil || err.Error() != "changes is required" {
		t.Errorf("nil changes error = %v", err)
	}

	changes := []LDAPModifyChange{
		{Operation: "ADD", Attribute: " mail ", Values: []string{"a@x.com"}},
		{Operation: "replace", Attribute: "cn", Values: []string{"new"}},
		{Operation: "delete", Attribute: "sn", Values: nil},
		{Operation: "delete", Attribute: "MAIL", Values: []string{"b@x.com"}},
	}
	normalized, attrs, err := normalizeLDAPModifyChanges(changes)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(normalized) != 4 {
		t.Fatalf("normalized len = %d, want 4", len(normalized))
	}
	if normalized[0].Operation != "add" || normalized[0].Attribute != "mail" {
		t.Errorf("change[0] = %+v", normalized[0])
	}
	if len(normalized[2].Values) != 0 {
		t.Errorf("delete values should be preserved as empty, got %v", normalized[2].Values)
	}
	if len(normalized[3].Values) != 1 || normalized[3].Values[0] != "b@x.com" {
		t.Errorf("delete values should be preserved, got %v", normalized[3].Values)
	}
	if strings.Join(attrs, ",") != "mail,cn,sn" {
		t.Errorf("attrs = %v, want unique [mail cn sn]", attrs)
	}

	errCases := []struct {
		changes []LDAPModifyChange
		want    string
	}{
		{[]LDAPModifyChange{{Operation: "add", Attribute: "", Values: []string{"x"}}}, "change 0 attribute is required"},
		{[]LDAPModifyChange{{Operation: "upsert", Attribute: "cn", Values: []string{"x"}}}, "change 0 operation must be add, replace, or delete"},
		{[]LDAPModifyChange{{Operation: "add", Attribute: "cn", Values: nil}}, `change 0 attribute "cn" requires values`},
		{[]LDAPModifyChange{{Operation: "replace", Attribute: "cn", Values: []string{""}}}, `change 0 attribute "cn" value 0 cannot be empty`},
	}
	for _, c := range errCases {
		_, _, err := normalizeLDAPModifyChanges(c.changes)
		if err == nil || err.Error() != c.want {
			t.Errorf("normalizeLDAPModifyChanges(%+v) error = %v, want %q", c.changes, err, c.want)
		}
	}
}

func TestLDAPModifyDNDestinationDN(t *testing.T) {
	cases := []struct {
		name        string
		dn          string
		newRDN      string
		newSuperior string
		want        string
		wantErr     string
	}{
		{"rename in place", "cn=old,ou=people,dc=example,dc=com", "cn=new", "", "cn=new,ou=people,dc=example,dc=com", ""},
		{"move to new parent", "cn=x,ou=a,dc=com", "cn=x", "ou=b,dc=com", "cn=x,ou=b,dc=com", ""},
		{"single rdn rename", "cn=x", "cn=y", "", "cn=y", ""},
		// go-ldap 语义：cn=a+sn=b 是单个多值 RDN（len(RDNs)==1），允许作为 newRdn
		{"multi-valued rdn allowed", "cn=x,ou=a,dc=com", "cn=a+sn=b", "", "cn=a+sn=b,ou=a,dc=com", ""},
		{"missing dn", "", "cn=x", "", "", "dn and newRdn are required"},
		{"missing rdn", "cn=x", "", "", "", "dn and newRdn are required"},
		{"multi rdn rejected", "cn=x,dc=com", "cn=a,cn=b", "", "", "newRdn must contain exactly one RDN"},
		{"invalid rdn", "cn=x,dc=com", "bad rdn", "", "", "parse newRdn"},
		{"invalid superior", "cn=x,dc=com", "cn=y", "bad superior", "", "parse newSuperior"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ldapModifyDNDestinationDN(c.dn, c.newRDN, c.newSuperior)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("error = %v, want contains %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if got != c.want {
				t.Errorf("destination = %q, want %q", got, c.want)
			}
		})
	}
}

func TestNormalizeProfile(t *testing.T) {
	p := NormalizeProfile(Profile{
		Name:                "  ldap  ",
		URL:                 " ldap://host:389 ",
		AuthType:            " SIMPLE ",
		TimeoutSeconds:      0,
		AllowedBaseDNs:      []string{" dc=A,dc=com ", "dc=a,dc=com", ""},
		AllowedWriteBaseDNs: []string{" dc=a,dc=com "},
		BlockedAttributes:   []string{"UserPassword"},
	})
	if p.Name != "ldap" || p.URL != "ldap://host:389" {
		t.Errorf("trim failed: %+v", p)
	}
	if p.AuthType != LDAPAuthSimple {
		t.Errorf("authType = %q", p.AuthType)
	}
	if p.TimeoutSeconds != 30 {
		t.Errorf("timeout = %d, want 30", p.TimeoutSeconds)
	}
	if len(p.AllowedBaseDNs) != 1 || p.AllowedBaseDNs[0] != "dc=A,dc=com" {
		t.Errorf("allowedBaseDNs = %v", p.AllowedBaseDNs)
	}
	if len(p.AllowedWriteBaseDNs) != 1 {
		t.Errorf("allowedWriteBaseDNs dedupe failed: %v", p.AllowedWriteBaseDNs)
	}
	if len(p.BlockedAttributes) != 1 || p.BlockedAttributes[0] != "UserPassword" {
		t.Errorf("blockedAttributes = %v", p.BlockedAttributes)
	}
}
