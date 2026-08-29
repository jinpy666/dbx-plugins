package ldapconn

// policy.go：安全策略移植（§6）。
// 来源 tiny-rdm backend/services/ldap_service.go：
//   - :78-90   defaultLDAPBlockedAttributes
//   - :1706    validateLDAPFilter
//   - :1717    normalizeLDAPWriteDN
//   - :1728    normalizeLDAPAddAttributes
//   - :1761    normalizeLDAPModifyChanges
//   - :1799    normalizeLDAPWriteValues
//   - :1813    uniqueLDAPAttributeNames
//   - :1831    ldapModifyDNDestinationDN
//   - :1862    ensureLDAPReadAllowed
//   - :1872    ensureLDAPWriteAllowed
//   - :1892    firstBlockedLDAPAttribute
//   - :1915/:1925/:1951  dnWithinAnyBase / dnWithinBase / canonicalRDN
// 策略参数从连接 Profile 读取（§3 改造点：profile → lifecycle 构造的 Profile）。

import (
	"fmt"
	"sort"
	"strings"

	ldap "github.com/go-ldap/ldap/v3"
)

// defaultLDAPBlockedAttributes 默认屏蔽属性表（tiny-rdm ldap_service.go:78-90，原样 11 项）。
var defaultLDAPBlockedAttributes = []string{
	"userPassword",
	"unicodePwd",
	"password",
	"pwd",
	"secret",
	"token",
	"apiKey",
	"privateKey",
	"objectSid",
	"objectGUID",
	"memberOf",
}

// DefaultLDAPBlockedAttributes 返回默认屏蔽属性表的副本（manifest 默认值 / 展示用）。
func DefaultLDAPBlockedAttributes() []string {
	out := make([]string, len(defaultLDAPBlockedAttributes))
	copy(out, defaultLDAPBlockedAttributes)
	return out
}

// validateLDAPFilter RFC 4515 过滤器校验（tiny-rdm :1706 原样）。
func validateLDAPFilter(filter string) error {
	if strings.TrimSpace(filter) == "" {
		return fmt.Errorf("filter is required")
	}
	if _, err := ldap.CompileFilter(filter); err != nil {
		return fmt.Errorf("invalid ldap filter: %w", err)
	}
	return nil
}

// normalizeLDAPWriteDN 写操作目标 DN 校验（tiny-rdm :1717 原样）。
func normalizeLDAPWriteDN(rawDN string) (string, error) {
	dn := strings.TrimSpace(rawDN)
	if dn == "" {
		return "", fmt.Errorf("dn is required")
	}
	if _, err := ldap.ParseDN(dn); err != nil {
		return "", fmt.Errorf("invalid dn: %w", err)
	}
	return dn, nil
}

// normalizeLDAPAddAttributes add 请求属性归一化（tiny-rdm :1728 原样）。
func normalizeLDAPAddAttributes(attrs map[string][]string) (map[string][]string, []string, error) {
	if len(attrs) == 0 {
		return nil, nil, fmt.Errorf("attributes is required")
	}
	keys := make([]string, 0, len(attrs))
	for attr := range attrs {
		keys = append(keys, attr)
	}
	sort.Strings(keys)

	normalized := make(map[string][]string, len(attrs))
	names := make([]string, 0, len(attrs))
	seen := map[string]struct{}{}
	for _, rawAttr := range keys {
		attr := strings.TrimSpace(rawAttr)
		if attr == "" {
			return nil, nil, fmt.Errorf("attribute name cannot be empty")
		}
		key := strings.ToLower(attr)
		if _, exists := seen[key]; exists {
			return nil, nil, fmt.Errorf("duplicate attribute %q", attr)
		}
		seen[key] = struct{}{}
		values, err := normalizeLDAPWriteValues(attr, attrs[rawAttr])
		if err != nil {
			return nil, nil, err
		}
		normalized[attr] = values
		names = append(names, attr)
	}
	return normalized, names, nil
}

// normalizeLDAPModifyChanges modify 请求变更归一化（tiny-rdm :1761 原样）。
func normalizeLDAPModifyChanges(changes []LDAPModifyChange) ([]LDAPModifyChange, []string, error) {
	if len(changes) == 0 {
		return nil, nil, fmt.Errorf("changes is required")
	}
	normalized := make([]LDAPModifyChange, 0, len(changes))
	attrs := make([]string, 0, len(changes))
	for index, change := range changes {
		attr := strings.TrimSpace(change.Attribute)
		if attr == "" {
			return nil, nil, fmt.Errorf("change %d attribute is required", index)
		}
		op := strings.ToLower(strings.TrimSpace(change.Operation))
		switch op {
		case "add", "replace":
			values, err := normalizeLDAPWriteValues(attr, change.Values)
			if err != nil {
				return nil, nil, fmt.Errorf("change %d %s", index, err.Error())
			}
			normalized = append(normalized, LDAPModifyChange{
				Operation: op,
				Attribute: attr,
				Values:    values,
			})
		case "delete":
			values := append([]string{}, change.Values...)
			normalized = append(normalized, LDAPModifyChange{
				Operation: op,
				Attribute: attr,
				Values:    values,
			})
		default:
			return nil, nil, fmt.Errorf("change %d operation must be add, replace, or delete", index)
		}
		attrs = append(attrs, attr)
	}
	return normalized, uniqueLDAPAttributeNames(attrs), nil
}

// normalizeLDAPWriteValues 写值校验（tiny-rdm :1799 原样）：
// 拒绝空值列表与空白值，逐值原样返回（防注入的第一道防线是 filter/DN 解析，
// 此处保证空值不会写入目录）。
func normalizeLDAPWriteValues(attr string, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("attribute %q requires values", attr)
	}
	out := make([]string, len(values))
	for index, value := range values {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("attribute %q value %d cannot be empty", attr, index)
		}
		out[index] = value
	}
	return out, nil
}

func uniqueLDAPAttributeNames(attrs []string) []string {
	out := make([]string, 0, len(attrs))
	seen := map[string]struct{}{}
	for _, attr := range attrs {
		trimmed := strings.TrimSpace(attr)
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

// ldapModifyDNDestinationDN 计算 modifyDn 目标 DN（tiny-rdm :1831 原样）。
func ldapModifyDNDestinationDN(rawDN, rawNewRDN, rawNewSuperior string) (string, error) {
	dn := strings.TrimSpace(rawDN)
	newRDN := strings.TrimSpace(rawNewRDN)
	newSuperior := strings.TrimSpace(rawNewSuperior)
	if dn == "" || newRDN == "" {
		return "", fmt.Errorf("dn and newRdn are required")
	}
	parsedNewRDN, err := ldap.ParseDN(newRDN)
	if err != nil || len(parsedNewRDN.RDNs) != 1 {
		if err != nil {
			return "", fmt.Errorf("parse newRdn: %w", err)
		}
		return "", fmt.Errorf("newRdn must contain exactly one RDN")
	}
	parentDN := newSuperior
	if parentDN == "" {
		parsedDN, err := ldap.ParseDN(dn)
		if err != nil {
			return "", fmt.Errorf("parse dn: %w", err)
		}
		if len(parsedDN.RDNs) <= 1 {
			return newRDN, nil
		}
		parentDN = (&ldap.DN{RDNs: parsedDN.RDNs[1:]}).String()
	}
	if _, err := ldap.ParseDN(parentDN); err != nil {
		return "", fmt.Errorf("parse newSuperior: %w", err)
	}
	return newRDN + "," + parentDN, nil
}

// ensureLDAPReadAllowed 读白名单门禁（tiny-rdm :1862 原样；空 baseDn 或空白名单 = 不限）。
func ensureLDAPReadAllowed(profile Profile, dn string) error {
	if strings.TrimSpace(dn) == "" || len(profile.AllowedBaseDNs) == 0 {
		return nil
	}
	if dnWithinAnyBase(dn, profile.AllowedBaseDNs) {
		return nil
	}
	return fmt.Errorf("ldap dn %q is outside allowed base DNs", strings.TrimSpace(dn))
}

// ensureLDAPWriteAllowed 写门禁（tiny-rdm :1872 原样）：
// read_only → 写白名单（空则回退读白名单，再空 = 不限）→ 屏蔽属性拒绝。
func ensureLDAPWriteAllowed(profile Profile, dn string, attrs []string) error {
	if profile.ReadOnly {
		return fmt.Errorf("ldap profile %q is read-only", profile.Name)
	}
	if strings.TrimSpace(dn) == "" {
		return fmt.Errorf("dn is required")
	}
	writeBases := profile.AllowedWriteBaseDNs
	if len(writeBases) == 0 {
		writeBases = profile.AllowedBaseDNs
	}
	if len(writeBases) > 0 && !dnWithinAnyBase(dn, writeBases) {
		return fmt.Errorf("ldap dn %q is outside allowed write base DNs", strings.TrimSpace(dn))
	}
	if blocked := firstBlockedLDAPAttribute(profile, attrs); blocked != "" {
		return fmt.Errorf("ldap attribute %q is blocked by profile policy", blocked)
	}
	return nil
}

// firstBlockedLDAPAttribute 返回 attrs 中第一个被屏蔽的属性（tiny-rdm :1892 原样；
// BlockedAttributes 为配置追加项，与默认表取并集）。
func firstBlockedLDAPAttribute(profile Profile, attrs []string) string {
	if len(attrs) == 0 {
		return ""
	}
	blocked := map[string]struct{}{}
	for _, attr := range defaultLDAPBlockedAttributes {
		blocked[strings.ToLower(strings.TrimSpace(attr))] = struct{}{}
	}
	for _, attr := range profile.BlockedAttributes {
		blocked[strings.ToLower(strings.TrimSpace(attr))] = struct{}{}
	}
	for _, attr := range attrs {
		key := strings.ToLower(strings.TrimSpace(attr))
		if key == "" {
			continue
		}
		if _, ok := blocked[key]; ok {
			return attr
		}
	}
	return ""
}

func dnWithinAnyBase(dn string, bases []string) bool {
	for _, base := range bases {
		ok, err := dnWithinBase(dn, base)
		if err == nil && ok {
			return true
		}
	}
	return false
}

// dnWithinBase 判断 dn 是否落在 base 之内（tiny-rdm :1925 原样）。
// 空 base = 不限；RDN 逐段大小写不敏感比较（多值 RDN 内部排序后拼接）。
func dnWithinBase(dn, base string) (bool, error) {
	dn = strings.TrimSpace(dn)
	base = strings.TrimSpace(base)
	if base == "" {
		return true, nil
	}
	parsedDN, err := ldap.ParseDN(dn)
	if err != nil {
		return false, fmt.Errorf("parse dn: %w", err)
	}
	parsedBase, err := ldap.ParseDN(base)
	if err != nil {
		return false, fmt.Errorf("parse base dn: %w", err)
	}
	if len(parsedDN.RDNs) < len(parsedBase.RDNs) {
		return false, nil
	}
	offset := len(parsedDN.RDNs) - len(parsedBase.RDNs)
	for i := range parsedBase.RDNs {
		if canonicalRDN(parsedDN.RDNs[offset+i]) != canonicalRDN(parsedBase.RDNs[i]) {
			return false, nil
		}
	}
	return true, nil
}

func canonicalRDN(rdn *ldap.RelativeDN) string {
	if rdn == nil {
		return ""
	}
	parts := make([]string, 0, len(rdn.Attributes))
	for _, attr := range rdn.Attributes {
		parts = append(parts, strings.ToLower(strings.TrimSpace(attr.Type))+"="+strings.ToLower(strings.TrimSpace(attr.Value)))
	}
	sort.Strings(parts)
	return strings.Join(parts, "+")
}

// sanitizeLDAPAttributes 请求侧屏蔽属性过滤：请求 attributes 中出现屏蔽属性即剔除，
// 避免主动拉取敏感列（§6.2"返回/修改请求中出现即剔除/拒绝"的请求侧）。
func sanitizeLDAPAttributes(profile Profile, attrs []string) []string {
	if len(attrs) == 0 {
		return attrs
	}
	out := make([]string, 0, len(attrs))
	for _, attr := range attrs {
		if firstBlockedLDAPAttribute(profile, []string{attr}) != "" {
			continue
		}
		out = append(out, attr)
	}
	return out
}

// filterLDAPEntryBlockedAttributes 结果侧屏蔽属性过滤（§5.2 GetEntry"屏蔽属性过滤后返回"、
// smoke S7：结果中不含 userPassword）。返回剔除后的副本；输入 nil Attributes 时保留 nil。
func filterLDAPEntryBlockedAttributes(profile Profile, entry LDAPEntry) LDAPEntry {
	if entry.Attributes == nil {
		return entry
	}
	out := make(map[string][]string, len(entry.Attributes))
	for name, values := range entry.Attributes {
		if firstBlockedLDAPAttribute(profile, []string{name}) != "" {
			continue
		}
		out[name] = values
	}
	entry.Attributes = out
	return entry
}
