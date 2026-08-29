package ldapconn

// schema.go：schema 元数据（tiny-rdm backend/services/ldap_service.go:773-968 原样移植）
// + RootDSE / subschemaSubentry 发现（:452-506）+ 进程内 schema 缓存结构。
// 持久缓存（cache/schema-<hash>.json）由 store 层（L-A）落盘；本文件提供缓存接口。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// parseLDAPSchemaMetadata 解析 subschema 条目（tiny-rdm :773 原样）。
func parseLDAPSchemaMetadata(schemaDN string, entry LDAPEntry) LDAPSchemaMetadata {
	attributeTypes := parseLDAPAttributeTypes(entry.Attributes["attributeTypes"])
	objectClasses := parseLDAPObjectClasses(entry.Attributes["objectClasses"])
	attributeNames := make([]string, 0, len(attributeTypes))
	seen := map[string]bool{}
	for _, attr := range attributeTypes {
		for _, name := range attr.Names {
			key := strings.ToLower(name)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			attributeNames = append(attributeNames, name)
		}
	}
	sort.Strings(attributeNames)
	return LDAPSchemaMetadata{
		SubschemaSubentry:     schemaDN,
		AttributeNames:        attributeNames,
		AttributeTypes:        attributeTypes,
		ObjectClassAttributes: objectClasses,
	}
}

// filterLDAPSchemaMetadataForProfile 按屏蔽属性策略过滤 schema 元数据（tiny-rdm :797 原样）。
func filterLDAPSchemaMetadataForProfile(profile Profile, metadata LDAPSchemaMetadata) LDAPSchemaMetadata {
	isAllowedAttr := func(name string) bool {
		return firstBlockedLDAPAttribute(profile, []string{name}) == ""
	}
	filterNames := func(values []string) []string {
		filtered := make([]string, 0, len(values))
		seen := map[string]bool{}
		for _, value := range values {
			name := strings.TrimSpace(value)
			key := strings.ToLower(name)
			if key == "" || seen[key] || !isAllowedAttr(name) {
				continue
			}
			seen[key] = true
			filtered = append(filtered, name)
		}
		return filtered
	}

	metadata.AttributeNames = filterNames(metadata.AttributeNames)
	attributeTypes := make([]LDAPSchemaAttributeType, 0, len(metadata.AttributeTypes))
	for _, attr := range metadata.AttributeTypes {
		attr.Names = filterNames(attr.Names)
		if attr.Name != "" && !isAllowedAttr(attr.Name) {
			attr.Name = firstLDAPNonEmpty(attr.Names...)
		}
		if attr.Name == "" && len(attr.Names) == 0 {
			continue
		}
		attributeTypes = append(attributeTypes, attr)
	}
	metadata.AttributeTypes = attributeTypes

	objectClasses := make(map[string]LDAPSchemaObjectClassAttributes, len(metadata.ObjectClassAttributes))
	for key, item := range metadata.ObjectClassAttributes {
		item.Must = filterNames(item.Must)
		item.May = filterNames(item.May)
		objectClasses[key] = item
	}
	metadata.ObjectClassAttributes = objectClasses
	return metadata
}

func parseLDAPAttributeTypes(values []string) []LDAPSchemaAttributeType {
	items := make([]LDAPSchemaAttributeType, 0, len(values))
	for _, value := range values {
		tokens := tokenizeLDAPSchemaValue(value)
		if len(tokens) == 0 {
			continue
		}
		names := schemaTokenList(tokens, "NAME")
		item := LDAPSchemaAttributeType{
			OID:         tokens[0],
			Name:        firstLDAPNonEmpty(names...),
			Names:       names,
			Description: schemaTokenValue(tokens, "DESC"),
		}
		if item.Name == "" {
			item.Name = item.OID
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return items
}

func parseLDAPObjectClasses(values []string) map[string]LDAPSchemaObjectClassAttributes {
	items := map[string]LDAPSchemaObjectClassAttributes{}
	for _, value := range values {
		tokens := tokenizeLDAPSchemaValue(value)
		if len(tokens) == 0 {
			continue
		}
		names := schemaTokenList(tokens, "NAME")
		item := LDAPSchemaObjectClassAttributes{
			OID:         tokens[0],
			Name:        firstLDAPNonEmpty(names...),
			Names:       names,
			Description: schemaTokenValue(tokens, "DESC"),
			Must:        schemaTokenList(tokens, "MUST"),
			May:         schemaTokenList(tokens, "MAY"),
		}
		if item.Name == "" {
			item.Name = item.OID
		}
		items[item.Name] = item
		for _, name := range item.Names {
			if name != item.Name {
				items[name] = item
			}
		}
	}
	return items
}

// tokenizeLDAPSchemaValue RFC 4512 值分词（tiny-rdm :894 原样：单引号串、括号、$、空白分隔，支持反斜杠转义）。
func tokenizeLDAPSchemaValue(value string) []string {
	tokens := []string{}
	var current strings.Builder
	inQuote := false
	escaped := false
	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
	}
	for _, ch := range value {
		if escaped {
			current.WriteRune(ch)
			escaped = false
			continue
		}
		if inQuote && ch == '\\' {
			escaped = true
			continue
		}
		if ch == '\'' {
			if inQuote {
				flush()
			}
			inQuote = !inQuote
			continue
		}
		if !inQuote && (ch == '(' || ch == ')' || ch == '$' || ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n') {
			flush()
			continue
		}
		current.WriteRune(ch)
	}
	flush()
	return tokens
}

func schemaTokenValue(tokens []string, key string) string {
	key = strings.ToUpper(key)
	for i := 0; i < len(tokens)-1; i++ {
		if strings.ToUpper(tokens[i]) == key {
			return tokens[i+1]
		}
	}
	return ""
}

func schemaTokenList(tokens []string, key string) []string {
	key = strings.ToUpper(key)
	for i := 0; i < len(tokens)-1; i++ {
		if strings.ToUpper(tokens[i]) != key {
			continue
		}
		values := []string{}
		for j := i + 1; j < len(tokens); j++ {
			if isLDAPSchemaKeyword(tokens[j]) {
				break
			}
			values = append(values, tokens[j])
		}
		return values
	}
	return nil
}

func isLDAPSchemaKeyword(value string) bool {
	switch strings.ToUpper(value) {
	case "NAME", "DESC", "OBSOLETE", "SUP", "EQUALITY", "ORDERING", "SUBSTR", "SYNTAX", "SINGLE-VALUE", "COLLECTIVE", "NO-USER-MODIFICATION", "USAGE", "ABSTRACT", "STRUCTURAL", "AUXILIARY", "MUST", "MAY", "X-ORIGIN":
		return true
	default:
		return false
	}
}

// rootDSEAllowed 沿袭 tiny-rdm :458：配置了 allowed_base_dns 时禁用 RootDSE 读。
func rootDSEAllowed(profile Profile) error {
	if len(profile.AllowedBaseDNs) > 0 {
		return fmt.Errorf("ldap RootDSE reads are disabled when allowed base DNs are configured")
	}
	return nil
}

// schemaAllowed 沿袭 tiny-rdm :478：配置了 allowed_base_dns 时禁用 schema 元数据读。
func schemaAllowed(profile Profile) error {
	if len(profile.AllowedBaseDNs) > 0 {
		return fmt.Errorf("ldap schema metadata reads are disabled when allowed base DNs are configured")
	}
	return nil
}

// defaultSchemaCacheTTL schema 缓存默认 TTL（service.go NewService 使用）。
const defaultSchemaCacheTTL = 10 * time.Minute

// SchemaCache 进程内 schema 缓存（按 connectionId 隔离；TTL 语义对齐
// tiny-rdm useLdapSchemaCache：默认走缓存，refresh=true 强制刷新）。
type SchemaCache struct {
	mu    sync.Mutex
	items map[string]*schemaCacheEntry
	ttl   time.Duration
}

type schemaCacheEntry struct {
	metadata  LDAPSchemaMetadata
	fetchedAt time.Time
}

func NewSchemaCache(ttl time.Duration) *SchemaCache {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &SchemaCache{
		items: map[string]*schemaCacheEntry{},
		ttl:   ttl,
	}
}

func (c *SchemaCache) key(connectionID string) string {
	sum := sha256.Sum256([]byte(connectionID))
	return hex.EncodeToString(sum[:8])
}

// Get 返回未过期缓存；不存在或过期返回 nil。
// 返回深拷贝：调用方修改结果不影响缓存条目。
func (c *SchemaCache) Get(connectionID string) *LDAPSchemaMetadata {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.items[c.key(connectionID)]
	if entry == nil || time.Since(entry.fetchedAt) > c.ttl {
		return nil
	}
	metadata := cloneSchemaMetadata(&entry.metadata)
	return &metadata
}

// Put 写入缓存。
func (c *SchemaCache) Put(connectionID string, metadata LDAPSchemaMetadata) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[c.key(connectionID)] = &schemaCacheEntry{metadata: metadata, fetchedAt: time.Now()}
}

// Invalidate 移除缓存（disconnect 时调用）。
func (c *SchemaCache) Invalidate(connectionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, c.key(connectionID))
}

// cloneSchemaMetadata 深拷贝 schema 元数据（slice/map 隔离）。
func cloneSchemaMetadata(m *LDAPSchemaMetadata) LDAPSchemaMetadata {
	out := *m
	out.AttributeNames = append([]string{}, m.AttributeNames...)
	out.AttributeTypes = make([]LDAPSchemaAttributeType, len(m.AttributeTypes))
	for i, attr := range m.AttributeTypes {
		attr.Names = append([]string{}, attr.Names...)
		out.AttributeTypes[i] = attr
	}
	out.ObjectClassAttributes = make(map[string]LDAPSchemaObjectClassAttributes, len(m.ObjectClassAttributes))
	for key, item := range m.ObjectClassAttributes {
		item.Names = append([]string{}, item.Names...)
		item.Must = append([]string{}, item.Must...)
		item.May = append([]string{}, item.May...)
		out.ObjectClassAttributes[key] = item
	}
	return out
}

// subschemaDNFromRootDSE 从 RootDSE 条目提取 subschemaSubentry（tiny-rdm :487 语义）。
func subschemaDNFromRootDSE(rootDSE LDAPEntry) (string, error) {
	schemaDN := firstLDAPNonEmpty(rootDSE.Attributes["subschemaSubentry"]...)
	if schemaDN == "" {
		return "", fmt.Errorf("RootDSE subschemaSubentry is not available")
	}
	return schemaDN, nil
}

// SchemaSearchSpec 是 schema 获取需要的两次读取（RootDSE → subschema 条目），
// 由 operations.go 的连接执行器完成，保持 schema.go 纯逻辑可测。
type SchemaSearchSpec struct {
	// RootDSEAttrs RootDSE 读取的属性列表。
	RootDSEAttrs []string
	// SubschemaAttrs subschema 条目读取的属性列表。
	SubschemaAttrs []string
}

// DefaultSchemaSearchSpec 返回默认两段读取规格。
func DefaultSchemaSearchSpec() SchemaSearchSpec {
	return SchemaSearchSpec{
		RootDSEAttrs:   []string{"subschemaSubentry"},
		SubschemaAttrs: []string{"attributeTypes", "objectClasses"},
	}
}

// ResolveSchema 用两段读取结果构建（解析 + 按策略过滤）schema 元数据。
// rootDSE/subschema 由调用方（operations 层）读取；schemaDN 白名单校验在此完成。
func ResolveSchema(profile Profile, rootDSE, subschema LDAPEntry) (LDAPSchemaMetadata, error) {
	if err := schemaAllowed(profile); err != nil {
		return LDAPSchemaMetadata{}, err
	}
	schemaDN, err := subschemaDNFromRootDSE(rootDSE)
	if err != nil {
		return LDAPSchemaMetadata{}, err
	}
	if err := ensureLDAPReadAllowed(profile, schemaDN); err != nil {
		return LDAPSchemaMetadata{}, err
	}
	metadata := parseLDAPSchemaMetadata(schemaDN, subschema)
	return filterLDAPSchemaMetadataForProfile(profile, metadata), nil
}

func firstLDAPNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
