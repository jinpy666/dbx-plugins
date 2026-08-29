package ldapconn

import (
	"context"
	"strings"
	"testing"

	"io.dbx.ldap.plugin/internal/lifecycle"
)

// operations_test.go：operations 层纯逻辑单测（不连网）。
// 策略拒绝发生在 WithConn 惰性建连之前，故可在无网络环境下验证门禁与审计。

// connectProfile 经 lifecycle params 注入一个连接表条目（不 dial）。
func connectProfile(t *testing.T, svc *Service, id string, cfg map[string]any) {
	t.Helper()
	params := &lifecycle.Params{
		Connection: lifecycle.Connection{
			ID:             id,
			Name:           "test-" + id,
			Host:           "ldap://127.0.0.1:1",
			Username:       "u",
			ExternalConfig: cfg,
			Secrets:        map[string]any{"bind_password": "s3cret"},
		},
		Runtime: lifecycle.Runtime{Host: "127.0.0.1", Port: 1},
	}
	if err := svc.Connect(params); err != nil {
		t.Fatalf("Connect: %v", err)
	}
}

func newAuditService() (*Service, *[]AuditRecord) {
	records := &[]AuditRecord{}
	svc := NewService()
	svc.Audit = func(rec AuditRecord) {
		*records = append(*records, rec)
	}
	return svc, records
}

func TestUnknownConnectionRejected(t *testing.T) {
	svc, records := newAuditService()
	if _, err := svc.Search(context.Background(), LDAPSearchRequest{ConnectionID: "missing"}); err == nil ||
		!strings.Contains(err.Error(), "not connected") {
		t.Errorf("search unknown conn err = %v", err)
	}
	if err := svc.DeleteEntry(context.Background(), LDAPDeleteEntryRequest{ConnectionID: "missing", DN: "cn=x"}); err == nil {
		t.Errorf("delete unknown conn should fail")
	}
	if len(*records) != 0 {
		t.Errorf("no audit expected for unknown connection, got %v", *records)
	}
}

func TestSearchParameterValidation(t *testing.T) {
	svc, records := newAuditService()
	connectProfile(t, svc, "c1", nil)
	ctx := context.Background()

	// baseDn 缺省（profile 也无）→ 报错
	if _, err := svc.Search(ctx, LDAPSearchRequest{ConnectionID: "c1", Filter: "(objectClass=*)"}); err == nil ||
		err.Error() != "baseDn is required" {
		t.Errorf("empty baseDn err = %v", err)
	}
	// 非法 filter（smoke S10）
	if _, err := svc.Search(ctx, LDAPSearchRequest{ConnectionID: "c1", BaseDN: "dc=x", Filter: "objectClass=*"}); err == nil ||
		!strings.HasPrefix(err.Error(), "invalid ldap filter") {
		t.Errorf("invalid filter err = %v", err)
	}
	// 读白名单拒绝（smoke S8）
	whitelisted, whitelistedRecs := newAuditService()
	connectProfile(t, whitelisted, "c2", map[string]any{"allowed_base_dns": "dc=example,dc=com"})
	_, err := whitelisted.Search(ctx, LDAPSearchRequest{ConnectionID: "c2", BaseDN: "dc=outside,dc=com", Filter: "(objectClass=*)"})
	if err == nil || !strings.Contains(err.Error(), "outside allowed base DNs") {
		t.Fatalf("out-of-whitelist search err = %v", err)
	}
	recs := *whitelistedRecs
	if len(recs) != 1 || recs[0].Action != "read-policy" || recs[0].Result != "denied" {
		t.Errorf("audit records = %+v", recs)
	}
	_ = svc
	_ = records
}

func TestCountParameterValidation(t *testing.T) {
	svc, records := newAuditService()
	connectProfile(t, svc, "c1", nil)
	ctx := context.Background()

	// 未知连接拒绝
	if _, err := svc.Count(ctx, LDAPCountRequest{ConnectionID: "missing"}); err == nil ||
		!strings.Contains(err.Error(), "not connected") {
		t.Errorf("count unknown conn err = %v", err)
	}
	// baseDn 缺省（profile 也无）→ 报错
	if _, err := svc.Count(ctx, LDAPCountRequest{ConnectionID: "c1"}); err == nil ||
		err.Error() != "baseDn is required" {
		t.Errorf("empty baseDn err = %v", err)
	}
	// 非法 filter 拒绝
	if _, err := svc.Count(ctx, LDAPCountRequest{ConnectionID: "c1", BaseDN: "dc=x", Filter: "objectClass=*"}); err == nil ||
		!strings.HasPrefix(err.Error(), "invalid ldap filter") {
		t.Errorf("invalid filter err = %v", err)
	}
	// 读白名单拒绝 + 审计 denied
	whitelisted, whitelistedRecs := newAuditService()
	connectProfile(t, whitelisted, "c2", map[string]any{"allowed_base_dns": "dc=example,dc=com"})
	_, err := whitelisted.Count(ctx, LDAPCountRequest{ConnectionID: "c2", BaseDN: "dc=outside,dc=com"})
	if err == nil || !strings.Contains(err.Error(), "outside allowed base DNs") {
		t.Fatalf("out-of-whitelist count err = %v", err)
	}
	recs := *whitelistedRecs
	if len(recs) != 1 || recs[0].Action != "read-policy" || recs[0].Result != "denied" {
		t.Errorf("audit records = %+v", recs)
	}
	_ = records
}

func TestAddEntryPolicyDenials(t *testing.T) {
	ctx := context.Background()

	// 1) read_only（smoke S6）
	ro, roRecs := newAuditService()
	connectProfile(t, ro, "ro", map[string]any{"read_only": true, "base_dn": "dc=example,dc=com"})
	err := ro.AddEntry(ctx, LDAPAddEntryRequest{ConnectionID: "ro", DN: "cn=x,dc=example,dc=com",
		Attributes: map[string][]string{"cn": {"x"}}})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only add err = %v", err)
	}
	if recs := *roRecs; len(recs) != 1 || recs[0].Action != "write-policy" || recs[0].Result != "denied" {
		t.Errorf("read-only audit = %+v", recs)
	}

	// 2) 写白名单外
	denied, deniedRecs := newAuditService()
	connectProfile(t, denied, "w1", map[string]any{"allowed_base_dns": "dc=example,dc=com"})
	err = denied.AddEntry(ctx, LDAPAddEntryRequest{ConnectionID: "w1", DN: "cn=x,dc=outside,dc=com",
		Attributes: map[string][]string{"cn": {"x"}}})
	if err == nil || !strings.Contains(err.Error(), "outside allowed write base DNs") {
		t.Fatalf("out-of-whitelist add err = %v", err)
	}
	if recs := *deniedRecs; len(recs) != 1 || recs[0].Target != "cn=x,dc=outside,dc=com" {
		t.Errorf("denied audit = %+v", recs)
	}

	// 3) 屏蔽属性（默认表：userPassword）
	blocked, blockedRecs := newAuditService()
	connectProfile(t, blocked, "w2", nil)
	err = blocked.AddEntry(ctx, LDAPAddEntryRequest{ConnectionID: "w2", DN: "cn=x,dc=example,dc=com",
		Attributes: map[string][]string{"cn": {"x"}, "userPassword": {"p"}}})
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("blocked attribute add err = %v", err)
	}

	// 4) 值校验（门禁之前）：空值报错，且不产生 write-policy 审计
	emptyVals, emptyRecs := newAuditService()
	connectProfile(t, emptyVals, "w3", nil)
	err = emptyVals.AddEntry(ctx, LDAPAddEntryRequest{ConnectionID: "w3", DN: "cn=x,dc=example,dc=com",
		Attributes: map[string][]string{"cn": {""}}})
	if err == nil || !strings.Contains(err.Error(), "cannot be empty") {
		t.Fatalf("empty value add err = %v", err)
	}
	if recs := *emptyRecs; len(recs) != 0 {
		t.Errorf("no audit expected before policy gate, got %+v", recs)
	}

	// 审计 detail 只含属性名，不含属性值（凭据红线）
	for _, rec := range append(*blockedRecs, *roRecs...) {
		if strings.Contains(rec.Detail, "s3cret") || strings.Contains(rec.Detail, "\x00") {
			t.Errorf("audit must not carry values: %+v", rec)
		}
	}
}

func TestModifyDNWriteWhitelistBothDNs(t *testing.T) {
	ctx := context.Background()
	svc, recs := newAuditService()
	// 写白名单只含 dc=a：目标 DN（dc=b 下）越界 → 拒绝
	connectProfile(t, svc, "m1", map[string]any{"allowed_write_base_dns": "dc=a,dc=com"})
	err := svc.ModifyDN(ctx, LDAPModifyDNRequest{ConnectionID: "m1",
		DN: "cn=x,dc=a,dc=com", NewRDN: "cn=y", NewSuperior: "dc=b,dc=com", DeleteOldRDN: true})
	if err == nil || !strings.Contains(err.Error(), "outside allowed write base DNs") {
		t.Fatalf("destination DN outside whitelist err = %v", err)
	}
	if recs := *recs; len(recs) != 1 || recs[0].Target != "cn=y,dc=b,dc=com" {
		t.Errorf("audit should target destination DN: %+v", recs)
	}

	// 源 DN 越界（第一个门禁）
	svc2, recs2 := newAuditService()
	connectProfile(t, svc2, "m2", map[string]any{"allowed_write_base_dns": "dc=a,dc=com"})
	err = svc2.ModifyDN(ctx, LDAPModifyDNRequest{ConnectionID: "m2",
		DN: "cn=x,dc=z,dc=com", NewRDN: "cn=y", DeleteOldRDN: true})
	if err == nil || !strings.Contains(err.Error(), "outside allowed write base DNs") {
		t.Fatalf("source DN outside whitelist err = %v", err)
	}
	if recs := *recs2; len(recs) != 1 || recs[0].Target != "cn=x,dc=z,dc=com" {
		t.Errorf("audit should target source DN: %+v", recs)
	}

	// newRdn 缺失
	svc3, _ := newAuditService()
	connectProfile(t, svc3, "m3", nil)
	if err := svc3.ModifyDN(ctx, LDAPModifyDNRequest{ConnectionID: "m3", DN: "cn=x"}); err == nil ||
		err.Error() != "dn and newRdn are required" {
		t.Errorf("missing newRdn err = %v", err)
	}
}

func TestGetEntryAndRootDSEPolicy(t *testing.T) {
	ctx := context.Background()
	svc, recs := newAuditService()
	connectProfile(t, svc, "g1", map[string]any{"allowed_base_dns": "dc=example,dc=com"})

	// GetEntry 白名单外 → read-policy denied（smoke S8）
	if _, err := svc.GetEntry(ctx, LDAPGetEntryRequest{ConnectionID: "g1", DN: "cn=x,dc=outside,dc=com"}); err == nil {
		t.Fatalf("out-of-whitelist get should fail")
	}
	// GetEntry 空 DN
	if _, err := svc.GetEntry(ctx, LDAPGetEntryRequest{ConnectionID: "g1", DN: " "}); err == nil ||
		err.Error() != "dn is required" {
		t.Errorf("empty dn err = %v", err)
	}

	// RootDSE：AllowedBaseDNs 非空时禁用（沿袭）
	if _, err := svc.RootDSE(ctx, LDAPRootDSERequest{ConnectionID: "g1"}); err == nil ||
		!strings.Contains(err.Error(), "disabled when allowed base DNs are configured") {
		t.Fatalf("restricted rootDSE err = %v", err)
	}
	// Schema 同理
	if _, err := svc.SchemaMetadata(ctx, LDAPSchemaMetadataRequest{ConnectionID: "g1"}); err == nil {
		t.Fatalf("restricted schema should fail")
	}
	if recs := *recs; len(recs) != 1 { // 仅 GetEntry 的 read-policy；rootDSE/schema 沿袭不发审计
		t.Errorf("audit records = %+v", recs)
	}
}

func TestConnectionStatuses(t *testing.T) {
	svc, _ := newAuditService()
	connectProfile(t, svc, "s1", nil)
	statuses, err := svc.ConnectionStatuses()
	if err != nil {
		t.Fatalf("ConnectionStatuses err = %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("statuses = %+v", statuses)
	}
	st := statuses[0]
	if st.ConnectionID != "s1" || st.Status != "idle" {
		t.Errorf("status = %+v", st)
	}
	// 凭据不出现
	raw := strings.Join([]string{st.Name, st.URL, st.Error}, "|")
	if strings.Contains(raw, "s3cret") {
		t.Errorf("status carries secret: %+v", st)
	}
}

type memPresetStore struct {
	presets []LDAPSearchPreset
	fail    bool
}

func (m *memPresetStore) LoadPresets() ([]LDAPSearchPreset, error) {
	if m.fail {
		return nil, context.DeadlineExceeded
	}
	return m.presets, nil
}

func (m *memPresetStore) SavePresets(presets []LDAPSearchPreset) error {
	if m.fail {
		return context.DeadlineExceeded
	}
	m.presets = presets
	return nil
}

func TestPresetsCRUD(t *testing.T) {
	svc := NewService()

	// store 未注入
	if _, err := svc.ListPresets(); err == nil {
		t.Errorf("nil store ListPresets should fail")
	}
	if _, err := svc.SavePreset(LDAPSearchPreset{Name: "x"}); err == nil {
		t.Errorf("nil store SavePreset should fail")
	}

	store := &memPresetStore{}
	svc.Presets = store

	// 空列表（nil → []）
	presets, err := svc.ListPresets()
	if err != nil || len(presets) != 0 {
		t.Fatalf("initial presets = %v, err = %v", presets, err)
	}

	// name 必填
	if _, err := svc.SavePreset(LDAPSearchPreset{}); err == nil || err.Error() != "preset name is required" {
		t.Errorf("nameless preset err = %v", err)
	}

	// 新增：id 空时生成 uuid
	saved, err := svc.SavePreset(LDAPSearchPreset{Name: "all", BaseDN: "dc=example,dc=com", Filter: "(objectClass=*)", Scope: "sub"})
	if err != nil || saved.ID == "" {
		t.Fatalf("save = %+v, err = %v", saved, err)
	}
	// upsert：同 id 覆盖
	if _, err := svc.SavePreset(LDAPSearchPreset{ID: saved.ID, Name: "all-2"}); err != nil {
		t.Fatalf("upsert err = %v", err)
	}
	presets, _ = svc.ListPresets()
	if len(presets) != 1 || presets[0].Name != "all-2" || presets[0].BaseDN != "" {
		t.Errorf("after upsert presets = %+v", presets)
	}
	// 显式 id 保留
	manual, err := svc.SavePreset(LDAPSearchPreset{ID: "  manual-id  ", Name: "m"})
	if err != nil || manual.ID != "manual-id" {
		t.Fatalf("manual id = %+v, err = %v", manual, err)
	}

	// remove：不存在报错
	if err := svc.RemovePreset("nope"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("remove missing err = %v", err)
	}
	if err := svc.RemovePreset(saved.ID); err != nil {
		t.Fatalf("remove err = %v", err)
	}
	presets, _ = svc.ListPresets()
	if len(presets) != 1 || presets[0].ID != "manual-id" {
		t.Errorf("after remove presets = %+v", presets)
	}

	// store 故障透传
	store.fail = true
	if _, err := svc.ListPresets(); err == nil {
		t.Errorf("store failure should propagate")
	}
}

func TestAggregateLimit(t *testing.T) {
	if got := aggregateLimit(0); got != defaultSearchAggregateLimit {
		t.Errorf("aggregateLimit(0) = %d, want %d", got, defaultSearchAggregateLimit)
	}
	if got := aggregateLimit(100); got != 100 {
		t.Errorf("aggregateLimit(100) = %d, want 100", got)
	}
	if got := aggregateLimit(-5); got != defaultSearchAggregateLimit {
		t.Errorf("aggregateLimit(-5) = %d, want %d", got, defaultSearchAggregateLimit)
	}
	if defaultSearchAggregateLimit != 500 {
		t.Errorf("defaultSearchAggregateLimit = %d, want 500 (§10)", defaultSearchAggregateLimit)
	}
}

func TestSchemaCacheNilSafe(t *testing.T) {
	// SchemaCache 为 nil 时 schemaCache() 兜底不 panic
	svc := NewService()
	svc.SchemaCache = nil
	if svc.schemaCache() == nil {
		t.Errorf("schemaCache() nil fallback failed")
	}
}
