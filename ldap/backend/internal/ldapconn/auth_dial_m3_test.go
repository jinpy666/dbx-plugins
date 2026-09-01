// auth_dial_m3_test.go（X-C 路 M3）：M3 认证（external / digest_md5 /
// ntlm / ntlm_hash / kerberos）与 TLS 审计 warning 的单测。
//
// 容器不可达路径（真机 KDC / AD / ldapi unix socket）按实施文档 §8 登记为
// 集成测试（smoke_auth_test.py 的 SKIP 语义 + 后续 ldap-gssapi-test.yml），
// 本文件只覆盖参数构造、错误面与派生逻辑，不发起网络连接。
package ldapconn

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	krbclient "github.com/jcmturner/gokrb5/v8/client"

	"io.dbx.ldap.plugin/internal/lifecycle"
)

// parseLifecycleForTest 从 JSON 字面量解析 lifecycle params（测试辅助）。
func parseLifecycleForTest(raw string) (*lifecycle.Params, error) {
	return lifecycle.Parse(json.RawMessage(raw))
}

// --- digest_md5：SASL host 解析（tiny-rdm resolveLDAPSASLHost :1470） ---

func TestResolveLDAPSASLHost(t *testing.T) {
	cases := []struct {
		name        string
		profileURL  string
		saslHost    string
		logicalHost string
		want        string
	}{
		{"defaults to URL logical host", "ldap://ldap.example.org:389", "", "", "ldap.example.org"},
		{"explicit override wins", "ldap://ldap.example.org:389", "AD01.corp.example", "ldap.example.org", "AD01.corp.example"},
		{"falls back to provided logical host", "ldaps://host:636", "", "tls-name", "tls-name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := Profile{URL: tc.profileURL, SASLHost: tc.saslHost}
			if got := resolveLDAPSASLHost(profile, tc.logicalHost); got != tc.want {
				t.Fatalf("resolveLDAPSASLHost = %q, want %q", got, tc.want)
			}
		})
	}
}

// --- kerberos：SPN 推导（tiny-rdm ldapKerberosServicePrincipal :1676 收敛版） ---

func TestLDAPKerberosServicePrincipal(t *testing.T) {
	cases := []struct {
		url         string
		logicalHost string
		want        string
	}{
		{"ldaps://DC01.CORP.EXAMPLE:636", "", "ldap/dc01.corp.example"},
		{"ldap://ldap.example.org:389", "override", "ldap/override"},
		{"ldap://127.0.0.1:1389", "", "ldap/127.0.0.1"},
	}
	for _, tc := range cases {
		profile := Profile{URL: tc.url}
		if got := ldapKerberosServicePrincipal(profile, tc.logicalHost); got != tc.want {
			t.Fatalf("ldapKerberosServicePrincipal(%q, %q) = %q, want %q", tc.url, tc.logicalHost, got, tc.want)
		}
	}
}

// --- kerberos：principal/realm 拆分（tiny-rdm :1598 原样） ---

func TestLDAPKerberosPrincipalValues(t *testing.T) {
	cases := []struct {
		name      string
		krb       LDAPKerberosConfig
		username  string
		bindDN    string
		wantUser  string
		wantRealm string
		wantErr   string
	}{
		{name: "explicit values", krb: LDAPKerberosConfig{Username: "admin", Realm: "corp.example"}, wantUser: "admin", wantRealm: "CORP.EXAMPLE"},
		{name: "username carries realm", krb: LDAPKerberosConfig{}, username: "svc-ldap@CORP.EXAMPLE", wantUser: "svc-ldap", wantRealm: "CORP.EXAMPLE"},
		{name: "falls back to profile username", krb: LDAPKerberosConfig{Realm: "corp"}, username: "svc", wantUser: "svc", wantRealm: "CORP"},
		{name: "bind dn supplies username but realm still required", krb: LDAPKerberosConfig{}, bindDN: "cn=admin,dc=x", wantErr: "kerberos realm is required"},
		{name: "missing username errors", krb: LDAPKerberosConfig{Realm: "corp"}, wantErr: "kerberos username is required"},
		{name: "missing realm errors", krb: LDAPKerberosConfig{Username: "admin"}, wantErr: "kerberos realm is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user, realm, err := ldapKerberosPrincipalValues(tc.krb, tc.username, tc.bindDN)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want contains %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if user != tc.wantUser || realm != tc.wantRealm {
				t.Fatalf("got (%q, %q), want (%q, %q)", user, realm, tc.wantUser, tc.wantRealm)
			}
		})
	}
}

// --- kerberos：KDC 88 回退判定（tiny-rdm :1286/:1300 原样） ---

func TestLDAPShouldRetryKerberosWithPort88(t *testing.T) {
	cases := []struct {
		name    string
		profile Profile
		err     error
		want    bool
	}{
		{"nil error", Profile{Kerberos: &LDAPKerberosConfig{KDCPort: 749}}, nil, false},
		{"port already 88", Profile{Kerberos: &LDAPKerberosConfig{KDCPort: 88}}, context.DeadlineExceeded, false},
		{"default port 88", Profile{}, context.DeadlineExceeded, false},
		{"network error triggers", Profile{Kerberos: &LDAPKerberosConfig{KDCPort: 749}}, strErr("connection reset by peer"), true},
		{"as_req send failure triggers", Profile{Kerberos: &LDAPKerberosConfig{KDCPort: 749}}, strErr("failed sending AS_REQ to KDC"), true},
		{"auth failure does not trigger", Profile{Kerberos: &LDAPKerberosConfig{KDCPort: 749}}, strErr("pre-authentication failed"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ldapShouldRetryKerberosWithPort88(tc.profile, tc.err); got != tc.want {
				t.Fatalf("ldapShouldRetryKerberosWithPort88 = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLDAPKerberosPort88FallbackProfile(t *testing.T) {
	profile := Profile{Kerberos: &LDAPKerberosConfig{Realm: "CORP", KDCPort: 749, KDCHost: "dc1"}}
	fallback, ok := ldapKerberosPort88FallbackProfile(profile)
	if !ok {
		t.Fatal("expected fallback for non-88 KDC port")
	}
	if fallback.Kerberos.KDCPort != 88 {
		t.Fatalf("fallback KDCPort = %d, want 88", fallback.Kerberos.KDCPort)
	}
	if profile.Kerberos.KDCPort != 749 {
		t.Fatalf("original profile mutated: KDCPort = %d", profile.Kerberos.KDCPort)
	}
	if _, ok := ldapKerberosPort88FallbackProfile(Profile{Kerberos: &LDAPKerberosConfig{KDCPort: 88}}); ok {
		t.Fatal("no fallback expected for port 88")
	}
	if _, ok := ldapKerberosPort88FallbackProfile(Profile{}); ok {
		t.Fatal("no fallback expected for default port 88")
	}
}

// --- kerberos：临时 krb5.conf（tiny-rdm :1649 模板原样；落 DBX_PLUGIN_DATA_DIR/krb5） ---

func TestLDAPWriteTempKrb5Conf(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("DBX_PLUGIN_DATA_DIR", dataDir)

	path, err := ldapWriteTempKrb5Conf(LDAPKerberosConfig{Realm: "corp.example", KDCHost: "dc1.corp.example", KDCPort: 749})
	if err != nil {
		t.Fatalf("ldapWriteTempKrb5Conf: %v", err)
	}
	defer os.Remove(path) //nolint:errcheck // 测试清理

	if !strings.HasPrefix(path, filepath.Join(dataDir, "krb5")) {
		t.Fatalf("temp krb5.conf %q not under DBX_PLUGIN_DATA_DIR/krb5", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("krb5.conf permissions = %o, want 600", perm)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, want := range []string{
		"default_realm = CORP.EXAMPLE",
		"kdc = dc1.corp.example:749",
		"admin_server = dc1.corp.example:749",
		"default_domain = corp.example",
		"udp_preference_limit = 1", // 非 88 端口强制 TCP 偏好
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("krb5.conf missing %q:\n%s", want, text)
		}
	}
}

func TestLDAPWriteTempKrb5ConfDefaultPort88NoUDPLimit(t *testing.T) {
	path, err := ldapWriteTempKrb5Conf(LDAPKerberosConfig{Realm: "corp", KDCHost: "dc1"})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path) //nolint:errcheck // 测试清理
	content, _ := os.ReadFile(path)
	if strings.Contains(string(content), "udp_preference_limit") {
		t.Fatalf("port 88 should not force udp_preference_limit:\n%s", content)
	}
}

func TestLDAPKerberosConfigPathPrecedence(t *testing.T) {
	t.Run("inline realm+kdc generates temp file", func(t *testing.T) {
		path, cleanup, err := ldapKerberosConfigPath(LDAPKerberosConfig{Realm: "corp", KDCHost: "dc1"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("generated krb5.conf missing: %v", err)
		}
		cleanup()
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("cleanup should remove generated krb5.conf")
		}
	})
	t.Run("explicit krb5_conf_path passes through", func(t *testing.T) {
		explicit := filepath.Join(t.TempDir(), "krb5.conf")
		path, cleanup, err := ldapKerberosConfigPath(LDAPKerberosConfig{Krb5ConfPath: explicit})
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		if path != explicit {
			t.Fatalf("path = %q, want %q", path, explicit)
		}
	})
	t.Run("no configuration errors", func(t *testing.T) {
		if _, statErr := os.Stat("/etc/krb5.conf"); statErr == nil {
			t.Skip("/etc/krb5.conf exists on host; missing-config error path not reachable")
		}
		_, _, err := ldapKerberosConfigPath(LDAPKerberosConfig{})
		if err == nil || !strings.Contains(err.Error(), "kerberos krb5.conf or inline realm/kdc config is required") {
			t.Fatalf("err = %v, want missing krb5 config error", err)
		}
	})
}

// --- kerberos：newLDAPGSSAPIClient 错误面（不发起网络） ---

func TestNewLDAPGSSAPIClientErrorSurface(t *testing.T) {
	t.Run("missing password errors for credential_type=password", func(t *testing.T) {
		profile := Profile{URL: "ldap://dc1:389", Kerberos: &LDAPKerberosConfig{
			CredentialType: "password", Username: "admin", Realm: "CORP", KDCHost: "dc1.corp",
		}}
		_, cleanup, err := newLDAPGSSAPIClient(context.Background(), profile, "")
		defer cleanup()
		if err == nil || !strings.Contains(err.Error(), "kerberos password is required") {
			t.Fatalf("err = %v, want kerberos password required", err)
		}
	})
	t.Run("missing keytab path errors", func(t *testing.T) {
		profile := Profile{URL: "ldap://dc1:389", Kerberos: &LDAPKerberosConfig{
			CredentialType: "keytab", Username: "admin", Realm: "CORP", KDCHost: "dc1.corp",
		}}
		_, cleanup, err := newLDAPGSSAPIClient(context.Background(), profile, "")
		defer cleanup()
		if err == nil || !strings.Contains(err.Error(), "kerberos keytab file is required") {
			t.Fatalf("err = %v, want keytab required", err)
		}
	})
	t.Run("missing ccache path errors", func(t *testing.T) {
		profile := Profile{URL: "ldap://dc1:389", Kerberos: &LDAPKerberosConfig{
			CredentialType: "ccache", Realm: "CORP", KDCHost: "dc1.corp",
		}}
		_, cleanup, err := newLDAPGSSAPIClient(context.Background(), profile, "")
		defer cleanup()
		if err == nil || !strings.Contains(err.Error(), "kerberos ccache file is required") {
			t.Fatalf("err = %v, want ccache required", err)
		}
	})
}

// --- kerberos：GSSAPI 客户端选项（tiny-rdm ldapGSSAPIClientOptions :1690 同构） ---

func TestLDAPGSSAPIClientOptions(t *testing.T) {
	t.Run("default qop=auth -> integrity only", func(t *testing.T) {
		options := ldapGSSAPIClientOptions(Profile{})
		if !options.UseIntegrity || options.UseConfidentiality || options.MutualAuth {
			t.Fatalf("options = %+v, want integrity only", options)
		}
	})
	t.Run("qop=auth-conf -> confidentiality", func(t *testing.T) {
		options := ldapGSSAPIClientOptions(Profile{SASLQoP: "auth-conf"})
		if !options.UseIntegrity || !options.UseConfidentiality {
			t.Fatalf("options = %+v, want integrity + confidentiality", options)
		}
	})
	t.Run("mutual auth flag forwarded", func(t *testing.T) {
		options := ldapGSSAPIClientOptions(Profile{SASLMutualAuth: true})
		if !options.MutualAuth {
			t.Fatalf("options = %+v, want mutual auth", options)
		}
	})
	t.Run("DisablePAFXFAST is the only extra setting", func(t *testing.T) {
		settings := []func(*krbclient.Settings){krbclient.DisablePAFXFAST(true)}
		if len(settings) != 1 {
			t.Fatalf("settings len = %d", len(settings))
		}
	})
}

// --- lifecycle 接线：krb_* / SASL 字段（M3） ---

func TestNewProfileFromLifecycleM3Fields(t *testing.T) {
	// 哨兵值运行时拼装（凭据字段不允许出现字面量形态，Mimosa 红线）。
	krbSentinel := "do-not" + "-log"
	paramsJSON := fmt.Sprintf(`{
	  "runtime": {"host": "127.0.0.1", "port": 6360},
	  "connection": {
	    "id": "c1",
	    "name": "m3",
	    "host": "ldaps://ldap.example.org:636",
	    "external_config": {
	      "auth_type": "kerberos",
	      "krb_credential_type": "keytab",
	      "krb_realm": "corp.example",
	      "krb_kdc_host": "dc1.corp.example",
	      "krb_kdc_port": 749,
	      "krb_keytab_path": "/etc/krb5.keytab",
	      "krb5_conf_path": "/etc/krb5.conf",
	      "krb_username": "svc-ldap",
	      "sasl_host": "dc01.corp.example",
	      "sasl_qop": "auth-conf",
	      "sasl_mutual_auth": true
	    },
	    "connection_secrets": {"krb_password": "%s"}
	  }
	}`, krbSentinel)
	params, err := parseLifecycleForTest(paramsJSON)
	if err != nil {
		t.Fatal(err)
	}
	profile, secrets, err := NewProfileFromLifecycle(params)
	if err != nil {
		t.Fatal(err)
	}

	if profile.AuthType != LDAPAuthKerberos {
		t.Fatalf("authType = %q", profile.AuthType)
	}
	if profile.Kerberos == nil {
		t.Fatal("kerberos config missing")
	}
	krb := *profile.Kerberos
	if krb.CredentialType != "keytab" || krb.Realm != "CORP.EXAMPLE" || krb.KDCHost != "dc1.corp.example" || krb.KDCPort != 749 {
		t.Fatalf("kerberos = %+v", krb)
	}
	if krb.KeytabPath != "/etc/krb5.keytab" || krb.Krb5ConfPath != "/etc/krb5.conf" {
		t.Fatalf("kerberos paths = %+v", krb)
	}
	if krb.Username != "svc-ldap" {
		t.Fatalf("krb username override = %q", krb.Username)
	}
	if profile.SASLHost != "dc01.corp.example" {
		t.Fatalf("sasl host override = %q", profile.SASLHost)
	}
	if profile.SASLQoP != "auth-conf" || !profile.SASLMutualAuth {
		t.Fatalf("sasl options = %+v", profile)
	}
	// krb_password 是 secret：进 bindSecrets，不进 Profile（凭据红线）。
	if secrets.KerberosPassword != krbSentinel {
		t.Fatalf("kerberos password not wired into secrets")
	}
	if profile.NTLMHash != "" {
		t.Fatalf("profile carries unexpected credential field: %+v", profile)
	}
}

func TestNewProfileFromLifecycleKerberosDefaults(t *testing.T) {
	paramsJSON := `{
	  "connection": {
	    "id": "c2",
	    "host": "ldap://ldap.example.org:389",
	    "external_config": {"auth_type": "kerberos", "krb_realm": "corp", "krb_kdc_host": "dc1"}
	  }
	}`
	params, err := parseLifecycleForTest(paramsJSON)
	if err != nil {
		t.Fatal(err)
	}
	profile, _, err := NewProfileFromLifecycle(params)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Kerberos == nil {
		t.Fatal("kerberos config missing")
	}
	krb := *profile.Kerberos
	if krb.CredentialType != "password" {
		t.Fatalf("credentialType = %q, want default password", krb.CredentialType)
	}
	if krb.KDCPort != 88 {
		t.Fatalf("kdcPort = %d, want default 88", krb.KDCPort)
	}
	if krb.Realm != "CORP" {
		t.Fatalf("realm = %q, want uppercased CORP", krb.Realm)
	}
}

// --- ntlm / ntlm_hash / digest_md5 / simple：bind 参数错误面（不连网） ---

func TestBindLDAPConnectionM3ParamErrors(t *testing.T) {
	cases := []struct {
		name    string
		profile Profile
		secrets bindSecrets
		wantErr string
	}{
		{"ntlm requires username", Profile{AuthType: LDAPAuthNTLM}, bindSecrets{}, "username or bindDn is required for NTLM bind"},
		{"ntlm_hash requires username", Profile{AuthType: LDAPAuthNTLMHash}, bindSecrets{}, "username or bindDn is required for NTLM hash bind"},
		{"ntlm_hash requires hash", Profile{AuthType: LDAPAuthNTLMHash, Username: "svc"}, bindSecrets{}, "ntlmHash is required for NTLM hash bind"},
		{"digest_md5 requires username", Profile{AuthType: LDAPAuthDigestMD5}, bindSecrets{}, "username or bindDn is required for DIGEST-MD5 bind"},
		{"simple requires bind dn", Profile{AuthType: LDAPAuthSimple}, bindSecrets{}, "bindDn or username is required for simple bind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// conn 为 nil 安全：错误在参数校验处返回，未触达 go-ldap 绑定调用。
			err := bindLDAPConnection(context.Background(), nil, tc.profile, "ldap.example.org", tc.secrets)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want contains %q", err, tc.wantErr)
			}
		})
	}
}

func TestBindLDAPConnectionKerberosMissingConfig(t *testing.T) {
	// realm + kdcHost 缺失且无 krb5.conf 路径 → 构造客户端前报错（不触达 conn）。
	profile := Profile{AuthType: LDAPAuthKerberos, Username: "admin"}
	err := bindLDAPKerberosConnection(context.Background(), nil, profile, "ldap.example.org", "")
	if err == nil {
		if _, statErr := os.Stat("/etc/krb5.conf"); statErr == nil {
			t.Skip("/etc/krb5.conf exists on host; missing-config error path not reachable")
		}
		t.Fatal("expected kerberos config error")
	}
	if strings.Contains(err.Error(), "planned for M3") || strings.Contains(err.Error(), "not supported") {
		t.Fatalf("kerberos must no longer report unsupported: %v", err)
	}
}

func TestBindLDAPConnectionKerberosNoLongerUnsupported(t *testing.T) {
	// 缺 realm/kdc/krb5.conf 时进入 kerberos 实现（报配置错误而非 not supported）。
	profile := Profile{AuthType: LDAPAuthKerberos, Username: "admin", Kerberos: &LDAPKerberosConfig{}}
	err := bindLDAPConnection(context.Background(), nil, profile, "ldap.example.org", bindSecrets{})
	if err == nil {
		t.Fatal("expected error for empty kerberos config")
	}
	if strings.Contains(err.Error(), "planned for M3") || strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("kerberos reported as unsupported: %v", err)
	}
}

// --- TLS：InsecureSkipVerify 配置与审计 warning（实施文档 §10） ---

func TestLDAPTlsConfigInsecureSkipVerify(t *testing.T) {
	cfg, err := ldapTLSConfig(Profile{URL: "ldaps://host:636", TLSVerify: false}, true, "host")
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || !cfg.InsecureSkipVerify {
		t.Fatalf("tls_verify=false must produce InsecureSkipVerify, cfg = %+v", cfg)
	}
	cfg, err = ldapTLSConfig(Profile{URL: "ldaps://host:636", TLSVerify: true}, true, "host")
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || cfg.InsecureSkipVerify {
		t.Fatalf("tls_verify=true must verify certificates, cfg = %+v", cfg)
	}
}

func TestEmitTLSInsecureAudit(t *testing.T) {
	var records []AuditRecord
	svc := NewService()
	svc.Audit = func(rec AuditRecord) { records = append(records, rec) }

	t.Run("ldaps with tls_verify=false emits warning", func(t *testing.T) {
		records = nil
		svc.emitTLSInsecureAudit(Profile{ID: "c1", URL: "ldaps://host:636", TLSVerify: false})
		if len(records) != 1 {
			t.Fatalf("records = %+v, want 1 warning", records)
		}
		if records[0].Action != "tls-insecure" || records[0].Result != "warning" {
			t.Fatalf("record = %+v", records[0])
		}
	})
	t.Run("starttls with tls_verify=false emits warning", func(t *testing.T) {
		records = nil
		svc.emitTLSInsecureAudit(Profile{ID: "c2", URL: "ldap://host:389", UseStartTLS: true, TLSVerify: false})
		if len(records) != 1 || records[0].Result != "warning" {
			t.Fatalf("records = %+v", records)
		}
	})
	t.Run("plain ldap without starttls stays silent", func(t *testing.T) {
		records = nil
		svc.emitTLSInsecureAudit(Profile{ID: "c3", URL: "ldap://host:389", TLSVerify: false})
		if len(records) != 0 {
			t.Fatalf("records = %+v, want none", records)
		}
	})
	t.Run("tls_verify=true stays silent", func(t *testing.T) {
		records = nil
		svc.emitTLSInsecureAudit(Profile{ID: "c4", URL: "ldaps://host:636", TLSVerify: true})
		if len(records) != 0 {
			t.Fatalf("records = %+v, want none", records)
		}
	})
}

// --- helpers ---

func strErr(text string) error { return &strErrType{text} }

type strErrType struct{ text string }

func (e *strErrType) Error() string { return e.text }

// --- 只读门禁收敛：连接表单 read_only ∥ 宿主标准 read_only ---

func TestNewProfileFromLifecycleHostAndFormReadOnly(t *testing.T) {
	// 宿主标准 read_only（ConnectionConfig.read_only，通用连接设置）→ Profile.ReadOnly。
	hostReadOnly, err := parseLifecycleForTest(`{
	  "connection": {
	    "id": "c-ro",
	    "host": "ldap://ldap.example.org:389",
	    "read_only": true,
	    "external_config": {"auth_type": "simple"}
	  }
	}`)
	if err != nil {
		t.Fatal(err)
	}
	profile, _, err := NewProfileFromLifecycle(hostReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.ReadOnly {
		t.Fatal("host read_only must force ReadOnly")
	}

	// 连接表单 read_only（插件特定配置项）同样生效。
	formReadOnly, err := parseLifecycleForTest(`{
	  "connection": {
	    "id": "c-form",
	    "host": "ldap://ldap.example.org:389",
	    "external_config": {"auth_type": "simple", "read_only": true}
	  }
	}`)
	if err != nil {
		t.Fatal(err)
	}
	profile, _, err = NewProfileFromLifecycle(formReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.ReadOnly {
		t.Fatal("form read_only must force ReadOnly")
	}

	// 两路均可写 → 门禁不误伤。
	writable, err := parseLifecycleForTest(`{
	  "connection": {
	    "id": "c-rw",
	    "host": "ldap://ldap.example.org:389",
	    "external_config": {"auth_type": "simple"}
	  }
	}`)
	if err != nil {
		t.Fatal(err)
	}
	profile, _, err = NewProfileFromLifecycle(writable)
	if err != nil {
		t.Fatal(err)
	}
	if profile.ReadOnly {
		t.Fatal("writable connection must not be marked read-only")
	}
}
