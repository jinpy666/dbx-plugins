package mcp

// writegate_test.go：ldap_entry_write 对抗输入矩阵（可靠性纵深轮，
// S-WGATE-*）：注入风格 DN（裸换行/空字节/控制字符——go-ldap ParseDN 实测
// 放行，单点加固后本地拒绝）、空 DN、属性名大小写变体（屏蔽属性表）、
// changes 操作名变体、recursive delete 边界（根 DN）、只读/写白名单连接下
// 两阶段 preview 的第二道门（不签发令牌）。全部离线：连接惰性注册不拨号
//（执行类失败断言「到达拨号层」而非依赖容器）。

import (
	"strings"
	"testing"

	"io.dbx.ldap.plugin/internal/ldapconn"
	"io.dbx.ldap.plugin/internal/lifecycle"
)

// connectWriteGateProfile 注册惰性连接配置（不拨号），external 形状自由。
func connectWriteGateProfile(t *testing.T, server *Server, id string, external string) {
	t.Helper()
	params, err := lifecycle.Parse([]byte(`{"connection": {"id": "` + id + `", "name": "` + id + `", "host": "127.0.0.1", "port": 1, "external_config": ` + external + `}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := server.svc.Connect(params); err != nil {
		t.Fatal(err)
	}
}

// S-WGATE-1 注入风格 DN：裸换行 / 空字节 / 制表与转义外控制字符在参数层
// 即拒绝（加固前 go-ldap ParseDN 放行、会直达服务端并原样落 audit.jsonl）；
// 两阶段 delete 的 preview 不签发令牌（预检前置，不白烧令牌）。
func TestServerEntryWriteInjectionStyleDN(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	server.settings.ReportWaitMs = 1
	connectWriteGateProfile(t, server, "wg-inject", `{"base_dn": "dc=example,dc=org"}`)
	for _, dn := range []string{
		"uid=a\nb,dc=example,dc=org",             // 审计日志注入面：换行
		"uid=a\x00b,dc=example,dc=org",           // 空字节
		"uid=a,dc=example,dc=org\nrm -rf /etc",   // 命令风格的垃圾尾
		"uid=a\x1b[31m,dc=example,dc=org",        // ANSI 转义（0x1b）
	} {
		_, err := server.entryWrite(map[string]any{"connectionId": "wg-inject", "action": "delete", "dn": dn})
		if err == nil || !strings.Contains(err.Error(), "invalid dn") {
			t.Fatalf("control-char DN must be refused with invalid dn, got: %v", err)
		}
		_, err = server.entryWrite(map[string]any{"connectionId": "wg-inject", "action": "add", "dn": dn,
			"attributes": map[string]any{"ou": "x"}})
		if err == nil || !strings.Contains(err.Error(), "invalid dn") {
			t.Fatalf("add path must share the DN gate, got: %v", err)
		}
	}
	// RFC 4514 转义形态不受影响：错误只可能来自拨号层（连接不可达）。
	if _, err := server.entryWrite(map[string]any{"connectionId": "wg-inject", "action": "add",
		"dn": `uid=a\0Ab,dc=example,dc=org`,
		"attributes": map[string]any{"objectClass": "inetOrgPerson", "uid": "a"}}); err == nil ||
		strings.Contains(err.Error(), "invalid dn") {
		t.Fatalf("escaped DN must pass the local gate (dial error expected): %v", err)
	}
	if len(server.confirms.items) != 0 {
		t.Fatalf("refused inputs must not burn tokens: %d", len(server.confirms.items))
	}
}

// S-WGATE-2 空 DN / 空白 DN：连接解析之前即拒绝（不查连接不拨号）。
func TestServerEntryWriteEmptyDN(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	server.settings.ReportWaitMs = 1
	for _, dn := range []string{"", "   "} {
		_, err := server.entryWrite(map[string]any{"connectionId": "whatever", "action": "delete", "dn": dn})
		if err == nil || !strings.Contains(err.Error(), "dn is required") {
			t.Fatalf("empty DN must be refused before connection lookup, got: %v", err)
		}
	}
}

// S-WGATE-3 只读连接第二道门：写工具不进只读清单（既有 S-SRV 用例）之外，
// 直接 tools/call 的两阶段 preview 也必须拒绝且不签发令牌。
func TestServerEntryWriteReadOnlyPreviewRefused(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	server.settings.ReportWaitMs = 1
	connectWriteGateProfile(t, server, "wg-ro", `{"read_only": true, "base_dn": "dc=example,dc=org"}`)
	for _, action := range []string{"delete", "modifyDn", "add", "modify"} {
		args := map[string]any{"connectionId": "wg-ro", "action": action, "dn": "uid=a,dc=example,dc=org"}
		if action == "modifyDn" {
			args["newRdn"] = "uid=b"
		}
		if action == "modify" {
			args["changes"] = []any{map[string]any{"operation": "add", "attribute": "mail", "values": []any{"a@x"}}}
		}
		if action == "add" {
			args["attributes"] = map[string]any{"ou": "x"}
		}
		_, err := server.entryWrite(args)
		if err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Fatalf("%s on read-only must be refused: %v", action, err)
		}
	}
	if len(server.confirms.items) != 0 {
		t.Fatalf("read-only preview must not issue tokens: %d", len(server.confirms.items))
	}
}

// S-WGATE-4 写白名单第二道门：越白名单目标在 preview 阶段拒绝（不签发
// 令牌）；白名单内目标照常进入两阶段（preview 可得）。
func TestServerEntryWriteWhitelistPreviewGate(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	server.settings.ReportWaitMs = 1
	connectWriteGateProfile(t, server, "wg-wl",
		`{"allowed_write_base_dns": "ou=people,dc=example,dc=org", "base_dn": "dc=example,dc=org"}`)
	_, err := server.entryWrite(map[string]any{
		"connectionId": "wg-wl", "action": "delete", "dn": "uid=x,dc=elsewhere,dc=org"})
	if err == nil || !strings.Contains(err.Error(), "outside allowed write base DNs") {
		t.Fatalf("out-of-whitelist preview must be refused: %v", err)
	}
	if len(server.confirms.items) != 0 {
		t.Fatalf("out-of-whitelist preview must not issue tokens: %d", len(server.confirms.items))
	}
	// 白名单内：preview 签发（后续失败只会在拨号层）。
	preview, err := server.entryWrite(map[string]any{
		"connectionId": "wg-wl", "action": "delete", "dn": "uid=x,ou=people,dc=example,dc=org"})
	if err != nil || preview["confirmToken"] == "" {
		t.Fatalf("in-whitelist preview must issue a token: %v %v", preview, err)
	}
}

// S-WGATE-5 属性名大小写变体：屏蔽属性表大小写不敏感（USERPASSWORD/
// userPassword 变体都在执行层策略门被拒——策略先于拨号）。
func TestServerEntryWriteBlockedAttrCaseVariants(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	server.settings.ReportWaitMs = 1
	connectWriteGateProfile(t, server, "wg-block", `{}`)
	for _, name := range []string{"userPassword", "USERPASSWORD", "UserPassword", "secret", "SECRET"} {
		_, err := server.entryWrite(map[string]any{
			"connectionId": "wg-block", "action": "add", "dn": "uid=a,dc=example,dc=org",
			"attributes": map[string]any{name: "x", "uid": "a"}})
		if err == nil || !strings.Contains(err.Error(), "blocked by profile policy") {
			t.Fatalf("blocked attr variant %q must be refused: %v", name, err)
		}
	}
}

// S-WGATE-6 changes 操作名变体：大写/混合写法归一化接受（到达拨号层），
// 未知操作名报错列出合法值。
func TestServerEntryWriteChangeOperationVariants(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	server.settings.ReportWaitMs = 1
	connectWriteGateProfile(t, server, "wg-mod", `{}`)
	// ADD/Replace 变体：通过归一化（错误是拨号错误而非 operation 错误）。
	_, err := server.entryWrite(map[string]any{
		"connectionId": "wg-mod", "action": "modify", "dn": "uid=a,dc=example,dc=org",
		"changes": []any{map[string]any{"operation": "ADD", "attribute": "mail", "values": []any{"a@x"}},
			map[string]any{"operation": "Replace", "attribute": "cn", "values": []any{"a"}}}})
	if err == nil || strings.Contains(err.Error(), "operation must be") {
		t.Fatalf("case variants must pass normalization (dial error expected): %v", err)
	}
	// 未知操作名：明确报错列出 add/replace/delete。
	_, err = server.entryWrite(map[string]any{
		"connectionId": "wg-mod", "action": "modify", "dn": "uid=a,dc=example,dc=org",
		"changes": []any{map[string]any{"operation": "upsert", "attribute": "mail", "values": []any{"a@x"}}}})
	if err == nil || !strings.Contains(err.Error(), "operation must be add, replace, or delete") {
		t.Fatalf("unknown operation must be refused: %v", err)
	}
}

// S-WGATE-7 recursive delete 边界：根 DN（后缀自身）可进两阶段（preview
// 带 recursive + childCount 尽力而为），确认后到达执行层（拨号失败而非
// 令牌/策略错误）；确认前 token 未消费（执行失败也作废，重放 unknown）。
func TestServerEntryWriteRecursiveRootEdge(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	server.settings.ReportWaitMs = 1
	connectWriteGateProfile(t, server, "wg-root", `{"base_dn": "dc=example,dc=org"}`)
	root := "dc=example,dc=org"
	preview, err := server.entryWrite(map[string]any{
		"connectionId": "wg-root", "action": "delete", "dn": root, "recursive": true})
	if err != nil {
		t.Fatal(err)
	}
	if preview["confirmToken"] == "" {
		t.Fatalf("root preview must be issued: %v", preview)
	}
	if p, ok := preview["preview"].(map[string]any); !ok || p["recursive"] != true {
		t.Fatalf("preview must carry the recursive flag: %v", preview)
	}
	// 确认：到达执行层（拨号失败），不是 hash/令牌错误。
	_, err = server.entryWrite(map[string]any{
		"connectionId": "wg-root", "action": "delete", "dn": root, "recursive": true,
		"confirmToken": preview["confirmToken"]})
	if err == nil || strings.Contains(err.Error(), "confirmToken") || strings.Contains(err.Error(), "read-only") {
		t.Fatalf("confirm must reach the execution layer (dial error expected): %v", err)
	}
	// 执行失败前 token 已消费：重放即 unknown（一次性）。
	if got := server.confirms.Consume(preview["confirmToken"].(string),
		HashParams([]byte(`{"action":"delete"}`)), intentBase); got != ConfirmUnknown {
		t.Fatalf("consumed token must not be reusable: %v", got)
	}
}

// S-WGATE-8 modifyDn 预检：多段 newRdn 在签发令牌前拒绝（不白烧）。
func TestServerEntryWriteModifyDNPrevalidation(t *testing.T) {
	server := NewServer(ldapconn.NewService(), nil)
	server.settings.ReportWaitMs = 1
	connectWriteGateProfile(t, server, "wg-mdn", `{}`)
	_, err := server.entryWrite(map[string]any{
		"connectionId": "wg-mdn", "action": "modifyDn", "dn": "uid=a,dc=example,dc=org",
		"newRdn": "uid=b,ou=people"})
	if err == nil || !strings.Contains(err.Error(), "newRdn must contain exactly one RDN") {
		t.Fatalf("multi-RDN newRdn must be refused before preview: %v", err)
	}
	if len(server.confirms.items) != 0 {
		t.Fatalf("refused modifyDn must not issue tokens: %d", len(server.confirms.items))
	}
}
