// manifest 契约守卫：后端已预留的 SASL/Kerberos 覆盖项必须在 manifest 暴露
// （X-C 遗留 6 决策落地），且七语 label 齐全、visible_when 与消费方一致：
//   - sasl_host        → digest_md5（dial.go MD5Bind 的 SASL host 覆盖）
//   - krb_username     → kerberos（LDAPKerberosConfig.Username 覆盖）
//   - sasl_qop         → kerberos（ldapGSSAPIClientOptions 的 QoP；go-ldap
//     MD5Bind 无 security layer 协商，故不挂 digest_md5 死字段）
//   - sasl_mutual_auth → kerberos（GSSAPI mutual 开关）
package ldapconn

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type manifestField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Binding     string `json:"binding"`
	Required    bool   `json:"required"`
	VisibleWhen *struct {
		Field string   `json:"field"`
		OneOf []string `json:"one_of"`
	} `json:"visible_when"`
	RequiredWhen *struct {
		Field string   `json:"field"`
		OneOf []string `json:"one_of"`
	} `json:"required_when"`
	Options []struct {
		Label string `json:"label"`
		Value string `json:"value"`
	} `json:"options"`
	Default any `json:"default"`
}

type manifestLoc struct {
	Contributions map[string]struct {
		Fields map[string]struct {
			Label       string            `json:"label"`
			Description string            `json:"description"`
			Options     map[string]string `json:"options"`
		} `json:"fields"`
	} `json:"contributions"`
}

func loadManifestForContract(t *testing.T) (map[string]manifestField, map[string]manifestLoc) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	var doc struct {
		Contributions []struct {
			Fields []manifestField `json:"fields"`
		} `json:"contributions"`
		Localizations map[string]manifestLoc `json:"localizations"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse manifest.json: %v", err)
	}
	fields := map[string]manifestField{}
	for _, contribution := range doc.Contributions {
		for _, field := range contribution.Fields {
			fields[field.Key] = field
		}
	}
	return fields, doc.Localizations
}

func TestManifestExposesSASLKerberosOverrides(t *testing.T) {
	fields, locs := loadManifestForContract(t)

	wantVisible := map[string]string{
		"sasl_host":        "digest_md5",
		"krb_username":     "kerberos",
		"sasl_qop":         "kerberos",
		"sasl_mutual_auth": "kerberos",
	}
	for key, authType := range wantVisible {
		field, ok := fields[key]
		if !ok {
			t.Fatalf("manifest field %q missing", key)
		}
		if field.Binding != "config" {
			t.Fatalf("%s binding = %q, want config", key, field.Binding)
		}
		if field.VisibleWhen == nil {
			t.Fatalf("%s visible_when missing", key)
		}
		if field.VisibleWhen.Field != "auth_type" || len(field.VisibleWhen.OneOf) != 1 || field.VisibleWhen.OneOf[0] != authType {
			t.Fatalf("%s visible_when = %+v, want auth_type ∈ [%s]", key, field.VisibleWhen, authType)
		}
		if field.Label == "" || field.Description == "" {
			t.Fatalf("%s label/description incomplete: %q / %q", key, field.Label, field.Description)
		}
	}

	// sasl_qop 三选项与后端 ldapGSSAPIClientOptions 的取值面一致。
	qopValues := map[string]bool{}
	for _, option := range fields["sasl_qop"].Options {
		qopValues[option.Value] = true
	}
	for _, want := range []string{"auth", "auth-int", "auth-conf"} {
		if !qopValues[want] {
			t.Fatalf("sasl_qop options missing %q: %v", want, qopValues)
		}
	}
	if fields["sasl_mutual_auth"].Default != false {
		t.Fatalf("sasl_mutual_auth default = %v, want false", fields["sasl_mutual_auth"].Default)
	}

	wantLocales := []string{"en", "zh-CN", "zh-TW", "es", "it", "ja", "pt-BR"}
	if len(locs) != len(wantLocales) {
		t.Fatalf("localizations = %d blocks, want %d", len(locs), len(wantLocales))
	}
	for _, lang := range wantLocales {
		loc, ok := locs[lang]
		if !ok {
			t.Fatalf("localization %q missing", lang)
		}
		connFields := loc.Contributions["io.dbx.ldap.connection"].Fields
		for _, key := range append([]string(nil), "sasl_host", "krb_username", "sasl_qop", "sasl_mutual_auth",
			"host", "port", "tls_mode") {
			entry, ok := connFields[key]
			if !ok || entry.Label == "" {
				t.Fatalf("localization %s/%s label missing", lang, key)
			}
		}
		tlsModeLoc := connFields["tls_mode"]
		for _, want := range []string{"none", "starttls", "ldaps"} {
			if tlsModeLoc.Options[want] == "" {
				t.Fatalf("localization %s tls_mode option %q label missing", lang, want)
			}
		}
		qopLoc := connFields["sasl_qop"]
		for _, want := range []string{"auth", "auth-int", "auth-conf"} {
			if qopLoc.Options[want] == "" {
				t.Fatalf("localization %s sasl_qop option %q label missing", lang, want)
			}
		}
	}
}

// --- auth_type 显隐/必填矩阵（复现宿主 pluginFieldConditions 语义） ---
//
// 宿主求值规则（dbx-plugin-host-worktree apps/desktop/src/lib/plugins/
// pluginFieldConditions.ts）：单条件 one_of；引用字段取「当前表单值，隐藏字段
// 保留 default」；缺条件恒可见。保存时 visible && required && 空 → 阻止并提示
// 缺失字段。本矩阵守卫 manifest 与该语义及后端 bindLDAPConnection 参数校验
// （auth_dial_m3_test.go）的一致性。

type manifestFormState map[string]string

// manifestEvaluateVisible 复现宿主 pluginFieldIsVisible。
func manifestValue(key string, state manifestFormState, fields map[string]manifestField) string {
	if value, ok := state[key]; ok {
		return value
	}
	if value := fields[key].Default; value != nil {
		return fmt.Sprint(value)
	}
	return ""
}

func manifestEvaluateVisible(field manifestField, state manifestFormState, fields map[string]manifestField) bool {
	if field.VisibleWhen == nil {
		return true
	}
	target := field.VisibleWhen.Field
	value := manifestValue(target, state, fields)
	for _, allowed := range field.VisibleWhen.OneOf {
		if allowed == value {
			return manifestEvaluateVisible(fields[target], state, fields)
		}
	}
	return false
}

// manifestRequiredMissing 复现宿主保存校验：visible && required && 空值 → 缺失。
func manifestRequiredMissing(fields map[string]manifestField, state manifestFormState) []string {
	missing := []string{}
	for _, key := range []string{
		"display_name", "url", "bind_dn", "username", "domain", "bind_password",
		"ntlm_hash", "krb_password", "krb_keytab_path", "krb_ccache_path",
	} {
		field, ok := fields[key]
		if !ok {
			continue
		}
		if !manifestEvaluateVisible(field, state, fields) {
			continue
		}
		required := field.Required
		if field.RequiredWhen != nil {
			value := manifestValue(field.RequiredWhen.Field, state, fields)
			for _, allowed := range field.RequiredWhen.OneOf {
				if allowed == value {
					required = true
				}
			}
		}
		if !required {
			continue
		}
		// 字段自身取值：表单值 ?? default（宿主 pluginFieldValue 语义）。
		value := state[field.Key]
		if value == "" {
			if def, isString := field.Default.(string); isString {
				value = def
			}
		}
		if value == "" {
			missing = append(missing, field.Key)
		}
	}
	return missing
}

func TestManifestAuthTypeVisibilityMatrix(t *testing.T) {
	fields, _ := loadManifestForContract(t)

	// M5-a 联动显隐：tls_verify/tls_ca_path/tls_server_name 挂
	// visible_when(tls_mode ∈ starttls|ldaps)，不再是恒可见字段——它们的
	// 可见性由下方 tls_mode 条件场景单独守卫。
	common := []string{
		"display_name", "host", "port", "tls_mode", "base_dn", "auth_type",
		"timeout_secs", "read_only",
		"allowed_base_dns", "allowed_write_base_dns", "blocked_attributes",
	}
	tlsFields := []string{"tls_verify", "tls_ca_path", "tls_server_name"}
	simpleCommon := append(append([]string{}, common...), "bind_dn", "username", "bind_password")
	cases := []struct {
		name  string
		state manifestFormState
		want  []string
	}{
		{"anonymous", manifestFormState{"auth_type": "anonymous"}, common},
		{"external", manifestFormState{"auth_type": "external"}, common},
		{"unauthenticated", manifestFormState{"auth_type": "unauthenticated"},
			append(append([]string{}, common...), "bind_dn")},
		{"simple", manifestFormState{"auth_type": "simple"},
			append(append([]string{}, common...), "bind_dn", "username", "bind_password")},
		{"ntlm", manifestFormState{"auth_type": "ntlm"},
			append(append([]string{}, common...), "username", "domain", "bind_password")},
		{"ntlm_hash", manifestFormState{"auth_type": "ntlm_hash"},
			append(append([]string{}, common...), "username", "domain", "ntlm_hash")},
		{"digest_md5", manifestFormState{"auth_type": "digest_md5"},
			append(append([]string{}, common...), "username", "domain", "bind_password", "sasl_host")},
		// krb_credential_type 无 manifest default（§24 兜底旧宿主：隐藏字段
		// 默认值会穿透宿主非级联校验制造幽灵必填），用户显式选择后凭据
		// 字段才可见。
		{"kerberos (credential type unselected)", manifestFormState{"auth_type": "kerberos"},
			append(append([]string{}, common...),
				"username", "krb_credential_type", "krb_realm", "krb_kdc_host", "krb_kdc_port",
				"krb5_conf_path", "krb_username", "sasl_qop", "sasl_mutual_auth")},
		{"kerberos + keytab", manifestFormState{"auth_type": "kerberos", "krb_credential_type": "keytab"},
			append(append([]string{}, common...),
				"username", "krb_credential_type", "krb_realm", "krb_kdc_host", "krb_kdc_port",
				"krb5_conf_path", "krb_username", "sasl_qop", "sasl_mutual_auth", "krb_keytab_path")},
		{"kerberos + ccache", manifestFormState{"auth_type": "kerberos", "krb_credential_type": "ccache"},
			append(append([]string{}, common...),
				"username", "krb_credential_type", "krb_realm", "krb_kdc_host", "krb_kdc_port",
				"krb5_conf_path", "krb_username", "sasl_qop", "sasl_mutual_auth", "krb_ccache_path")},
		// TLS 字段随 tls_mode 联动（manifest default = "none" → 隐藏；
		// 上面各场景未给 tls_mode，走 default 回退，同样应隐藏）。
		{"simple + tls_mode=none (TLS fields hidden)", manifestFormState{"auth_type": "simple", "tls_mode": "none"}, simpleCommon},
		{"simple + tls_mode=starttls (TLS fields visible)",
			manifestFormState{"auth_type": "simple", "tls_mode": "starttls"},
			append(append([]string{}, simpleCommon...), tlsFields...)},
		{"simple + tls_mode=ldaps (TLS fields visible)",
			manifestFormState{"auth_type": "simple", "tls_mode": "ldaps"},
			append(append([]string{}, simpleCommon...), tlsFields...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := []string{}
			for key, field := range fields {
				if manifestEvaluateVisible(field, tc.state, fields) {
					got = append(got, key)
				}
			}
			want := map[string]bool{}
			for _, key := range tc.want {
				want[key] = true
				if _, ok := fields[key]; !ok {
					t.Fatalf("expected field %q missing from manifest", key)
				}
			}
			for _, key := range got {
				if !want[key] {
					t.Fatalf("field %q unexpectedly visible in scenario %q", key, tc.name)
				}
			}
			for key := range want {
				if !manifestEvaluateVisible(fields[key], tc.state, fields) {
					t.Fatalf("field %q should be visible in scenario %q", key, tc.name)
				}
			}
		})
	}
}

func TestManifestAuthTypeRequiredMatrix(t *testing.T) {
	fields, _ := loadManifestForContract(t)

	// 静态必填项必须有非空 default，否则空表单永远无法保存。
	for _, key := range []string{"display_name", "host"} {
		if !fields[key].Required {
			t.Fatalf("%s must be statically required", key)
		}
		if def, _ := fields[key].Default.(string); def == "" {
			t.Fatalf("%s is required but has empty default", key)
		}
	}

	// 连接表单三件套契约：host 裸主机名（ssh 族约定）、port 独立字段、
	// tls_mode 三态与后端 buildLDAPURL 取值面一致。
	for key, wantBinding := range map[string]string{"host": "host", "port": "port", "tls_mode": "config"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("manifest field %q missing", key)
		}
		if fields[key].Binding != wantBinding {
			t.Fatalf("%s binding = %q, want %q", key, fields[key].Binding, wantBinding)
		}
	}
	if _, exists := fields["url"]; exists {
		t.Fatal(`legacy field "url" must be removed from the manifest`)
	}
	if _, exists := fields["use_starttls"]; exists {
		t.Fatal(`legacy field "use_starttls" must be removed from the manifest`)
	}
	tlsValues := map[string]bool{}
	for _, option := range fields["tls_mode"].Options {
		tlsValues[option.Value] = true
	}
	for _, want := range []string{"none", "starttls", "ldaps"} {
		if !tlsValues[want] {
			t.Fatalf("tls_mode options missing %q: %v", want, tlsValues)
		}
	}

	cases := []struct {
		name        string
		state       manifestFormState
		wantMissing []string
	}{
		{"anonymous", manifestFormState{"auth_type": "anonymous"}, nil},
		{"external", manifestFormState{"auth_type": "external"}, nil},
		{"unauthenticated requires bind_dn", manifestFormState{"auth_type": "unauthenticated"}, []string{"bind_dn"}},
		{"simple requires bind_password", manifestFormState{"auth_type": "simple"}, []string{"bind_password"}},
		{"ntlm requires username + password", manifestFormState{"auth_type": "ntlm"}, []string{"bind_password", "username"}},
		{"ntlm_hash requires username + hash", manifestFormState{"auth_type": "ntlm_hash"}, []string{"ntlm_hash", "username"}},
		{"digest_md5 requires username + password", manifestFormState{"auth_type": "digest_md5"}, []string{"bind_password", "username"}},
		{"kerberos password is required", manifestFormState{"auth_type": "kerberos", "krb_credential_type": "password"}, []string{"krb_password"}},
		{"kerberos unselected credential type defers requirement", manifestFormState{"auth_type": "kerberos"}, nil},
		{"kerberos keytab path is required", manifestFormState{"auth_type": "kerberos", "krb_credential_type": "keytab"}, []string{"krb_keytab_path"}},
		{"kerberos cache path is required", manifestFormState{"auth_type": "kerberos", "krb_credential_type": "ccache"}, []string{"krb_ccache_path"}},
		{"anonymous ignores stale Kerberos credential selection", manifestFormState{"auth_type": "anonymous", "krb_credential_type": "keytab"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			missing := manifestRequiredMissing(fields, tc.state)
			if len(missing) != len(tc.wantMissing) {
				t.Fatalf("missing required = %v, want %v", missing, tc.wantMissing)
			}
			want := map[string]bool{}
			for _, key := range tc.wantMissing {
				want[key] = true
			}
			for _, key := range missing {
				if !want[key] {
					t.Fatalf("missing required = %v, want %v", missing, tc.wantMissing)
				}
			}
		})
	}
}

func TestManifestSecretBindingsNeverPersistedAsConfig(t *testing.T) {
	fields, _ := loadManifestForContract(t)
	for _, key := range []string{"bind_password", "ntlm_hash", "krb_password"} {
		if fields[key].Binding != "secret" {
			t.Fatalf("%s binding = %q, want secret (凭据红线)", key, fields[key].Binding)
		}
	}
}
