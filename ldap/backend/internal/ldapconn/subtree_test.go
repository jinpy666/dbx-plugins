package ldapconn

// subtree_test.go：N1 子树删除 + childrenCount 纯逻辑单测（不连网）。
// 覆盖：recursive 参数解析、Tree Delete 控件挂载、回退深度倒序、1000 上限、
// childrenCount 门禁、聚合审计记录格式、read_only × recursive 拒绝。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	ldap "github.com/go-ldap/ldap/v3"
)

func TestDeleteEntryRecursiveParamParsing(t *testing.T) {
	// 缺省 false：单条语义保持向后兼容。
	var plain LDAPDeleteEntryRequest
	if err := json.Unmarshal([]byte(`{"connectionId":"c1","dn":"cn=x"}`), &plain); err != nil {
		t.Fatalf("unmarshal plain: %v", err)
	}
	if plain.Recursive {
		t.Errorf("recursive default = true, want false")
	}
	// 显式 true。
	var recursive LDAPDeleteEntryRequest
	if err := json.Unmarshal([]byte(`{"connectionId":"c1","dn":"cn=x","recursive":true}`), &recursive); err != nil {
		t.Fatalf("unmarshal recursive: %v", err)
	}
	if !recursive.Recursive {
		t.Errorf("recursive = false, want true")
	}

	// 未知连接 + recursive=true：先于任何门禁/网络失败，不发审计。
	svc, records := newAuditService()
	err := svc.DeleteEntry(context.Background(), LDAPDeleteEntryRequest{ConnectionID: "missing", DN: "cn=x", Recursive: true})
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("recursive unknown conn err = %v", err)
	}
	if len(*records) != 0 {
		t.Errorf("no audit expected for unknown connection, got %+v", *records)
	}
}

func TestSubtreeDeleteRequestCarriesTreeDeleteControl(t *testing.T) {
	dn := "ou=tree,dc=example,dc=com"
	req := newSubtreeDeleteRequest(dn)
	if req.DN != dn {
		t.Errorf("DelRequest dn = %q, want %q", req.DN, dn)
	}
	if len(req.Controls) != 1 {
		t.Fatalf("controls = %d, want 1", len(req.Controls))
	}
	control := req.Controls[0]
	if control.GetControlType() != "1.2.840.113556.1.4.805" {
		t.Errorf("control type = %q, want tree delete OID 1.2.840.113556.1.4.805", control.GetControlType())
	}
	// criticality=true：服务端不支持时必须回错误（而非静默忽略控件），
	// 这样回退路径才会被触发。
	encoded := control.Encode()
	if encoded == nil {
		t.Fatalf("control encode returned nil packet")
	}
	critical := false
	for _, child := range encoded.Children {
		if child.Tag == berTagBoolean {
			critical = critical || child.Value.(bool)
		}
	}
	if !critical {
		t.Errorf("tree delete control must be critical")
	}
}

// berTagBoolean 是 BER universal BOOLEAN 的 tag number（go-ldap ber 包常量
// 的本地镜像，避免仅测试引入 asn1-ber 依赖）。
const berTagBoolean = 1

func TestOrderDeepestFirst(t *testing.T) {
	input := []string{
		"dc=example,dc=com",
		"cn=leaf,ou=mid,ou=tree,dc=example,dc=com",
		"ou=tree,dc=example,dc=com",
		"ou=mid,ou=tree,dc=example,dc=com",
	}
	got, err := orderDeepestFirst(input)
	if err != nil {
		t.Fatalf("orderDeepestFirst: %v", err)
	}
	want := []string{
		"cn=leaf,ou=mid,ou=tree,dc=example,dc=com",
		"ou=mid,ou=tree,dc=example,dc=com",
		"ou=tree,dc=example,dc=com",
		"dc=example,dc=com",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("order = %v, want %v", got, want)
	}

	// 非法 DN 报错（不该在回退中段才发现）。
	if _, err := orderDeepestFirst([]string{"not a dn"}); err == nil {
		t.Errorf("malformed dn should error")
	}

	// 空输入安全。
	if out, err := orderDeepestFirst(nil); err != nil || len(out) != 0 {
		t.Errorf("nil input = %v, %v", out, err)
	}
}

func TestSubtreeDeleteLimitGuards(t *testing.T) {
	if maxSubtreeDeleteEntries != 1000 {
		t.Fatalf("maxSubtreeDeleteEntries = %d, want 1000（N1 防误删契约）", maxSubtreeDeleteEntries)
	}

	// 清点搜索：sub scope、typesOnly、no attributes、SizeLimit = 上限+1。
	req := newSubtreeListRequest("ou=tree,dc=example,dc=com")
	if req.Scope != ldap.ScopeWholeSubtree {
		t.Errorf("scope = %v, want wholeSubtree", req.Scope)
	}
	if req.SizeLimit != maxSubtreeDeleteEntries+1 {
		t.Errorf("sizeLimit = %d, want %d", req.SizeLimit, maxSubtreeDeleteEntries+1)
	}
	if !req.TypesOnly || strings.Join(req.Attributes, ",") != "1.1" {
		t.Errorf("typesOnly = %v, attributes = %v, want typesOnly + [1.1]", req.TypesOnly, req.Attributes)
	}

	// 超限错误面：含目标 DN 与上限数字。
	err := subtreeDeleteLimitError("ou=big,dc=example,dc=com")
	if err == nil || !strings.Contains(err.Error(), "ou=big,dc=example,dc=com") ||
		!strings.Contains(err.Error(), "1000") {
		t.Errorf("limit error = %v", err)
	}
}

func TestIsSubtreeDeleteUnsupported(t *testing.T) {
	unsupported := []uint16{
		ldap.LDAPResultUnavailableCriticalExtension, // 未知 critical 控件的典型回码
		ldap.LDAPResultUnavailable,
		ldap.LDAPResultUnwillingToPerform,
	}
	for _, code := range unsupported {
		err := &ldap.Error{ResultCode: code, Err: context.DeadlineExceeded}
		if !isSubtreeDeleteUnsupported(err) {
			t.Errorf("code %d should trigger fallback", code)
		}
	}
	// 业务失败不能触发回退（否则普通删除失败会被误判为控件不支持）。
	for _, code := range []uint16{ldap.LDAPResultNoSuchObject, ldap.LDAPResultEntryAlreadyExists, ldap.LDAPResultInsufficientAccessRights} {
		err := &ldap.Error{ResultCode: code, Err: context.DeadlineExceeded}
		if isSubtreeDeleteUnsupported(err) {
			t.Errorf("code %d must not trigger fallback", code)
		}
	}
	// 非 go-ldap 错误不回退。
	if isSubtreeDeleteUnsupported(context.DeadlineExceeded) {
		t.Errorf("plain error must not trigger fallback")
	}
}

func TestChildrenCountValidation(t *testing.T) {
	// 未知连接拒绝。
	svc, _ := newAuditService()
	if _, err := svc.ChildrenCount(context.Background(), LDAPChildrenCountRequest{ConnectionID: "missing", DN: "ou=x"}); err == nil ||
		!strings.Contains(err.Error(), "not connected") {
		t.Errorf("childrenCount unknown conn err = %v", err)
	}
	// dn 必填（区别于 ldap/count 的 baseDn 提示）。
	connectProfile(t, svc, "c1", nil)
	if _, err := svc.ChildrenCount(context.Background(), LDAPChildrenCountRequest{ConnectionID: "c1", DN: " "}); err == nil ||
		err.Error() != "dn is required" {
		t.Errorf("empty dn err = %v", err)
	}
	// 读白名单拒绝 + read-policy/denied 审计（白名单约束同读操作）。
	whitelisted, recs := newAuditService()
	connectProfile(t, whitelisted, "c2", map[string]any{"allowed_base_dns": "dc=example,dc=com"})
	if _, err := whitelisted.ChildrenCount(context.Background(), LDAPChildrenCountRequest{ConnectionID: "c2", DN: "ou=outside,dc=com"}); err == nil ||
		!strings.Contains(err.Error(), "outside allowed base DNs") {
		t.Fatalf("out-of-whitelist childrenCount err = %v", err)
	}
	audit := *recs
	if len(audit) != 1 || audit[0].Action != "read-policy" || audit[0].Result != "denied" || audit[0].Target != "ou=outside,dc=com" {
		t.Errorf("audit records = %+v", audit)
	}
}

func TestSubtreeDeleteAuditRecordShape(t *testing.T) {
	rec := subtreeDeleteAuditRecord("conn-1", "ou=tree,dc=example,dc=com", 3, "")
	if rec.Action != "subtree_delete" || rec.Result != "ok" || rec.DeletedCount != 3 {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Target != "ou=tree,dc=example,dc=com" {
		t.Errorf("target = %q, want the deleted subtree DN", rec.Target)
	}
	// 事件/落盘字段 camelCase；单条删除记录不得出现 deletedCount。
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := generic["deletedCount"]; !ok {
		t.Errorf("aggregate record must carry deletedCount: %s", raw)
	}
	if _, ok := generic["deleted_count"]; ok {
		t.Errorf("audit fields must be camelCase: %s", raw)
	}
	plain, err := json.Marshal(AuditRecord{ConnectionID: "c", Action: "delete-entry", Target: "cn=x", Result: "ok"})
	if err != nil {
		t.Fatalf("marshal plain: %v", err)
	}
	if strings.Contains(string(plain), "deletedCount") {
		t.Errorf("single-delete audit must omit deletedCount: %s", plain)
	}
}

func TestDeleteEntryRecursivePolicyDenials(t *testing.T) {
	ctx := context.Background()

	// read_only × recursive：拒绝且只记一条 write-policy（smoke 组合）。
	ro, roRecs := newAuditService()
	connectProfile(t, ro, "ro", map[string]any{"read_only": true, "base_dn": "dc=example,dc=com"})
	err := ro.DeleteEntry(ctx, LDAPDeleteEntryRequest{ConnectionID: "ro", DN: "ou=tree,dc=example,dc=com", Recursive: true})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only recursive delete err = %v", err)
	}
	if recs := *roRecs; len(recs) != 1 || recs[0].Action != "write-policy" || recs[0].Result != "denied" {
		t.Errorf("read-only audit = %+v", recs)
	}

	// 写白名单外 + recursive：拒绝（只校验目标 DN，子条目隐式落在目标子树内）。
	denied, deniedRecs := newAuditService()
	connectProfile(t, denied, "w1", map[string]any{"allowed_write_base_dns": "dc=a,dc=com"})
	err = denied.DeleteEntry(ctx, LDAPDeleteEntryRequest{ConnectionID: "w1", DN: "ou=tree,dc=b,dc=com", Recursive: true})
	if err == nil || !strings.Contains(err.Error(), "outside allowed write base DNs") {
		t.Fatalf("out-of-whitelist recursive delete err = %v", err)
	}
	if recs := *deniedRecs; len(recs) != 1 || recs[0].Target != "ou=tree,dc=b,dc=com" {
		t.Errorf("denied audit = %+v", recs)
	}
}
