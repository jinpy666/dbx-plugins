// auth_gssapi.go（X-C 路 M3）：kerberos（GSSAPI）bind 支撑，移植自 tiny-rdm
// ldap_service.go:1493-1712（newLDAPGSSAPIClient / prepareLDAPKerberosRuntime /
// ldapKerberosPrincipalValues / ldapKerberosConfigPath / ldapWriteTempKrb5Conf /
// ldapKerberosServicePrincipal / ldapGSSAPIClientOptions）与 :1268-1316
// （KDC port 88 回退）。
//
// 相对 tiny-rdm 的改造：
//   - 删除 prepareLDAPKerberosRuntime 中的 proxy/SSH 路由与 KDCNetworkAddress
//     拨号改写（DBX 传输层替代，方案 D5）：KDC 直连 krb.KDCHost:KDCPort；
//   - 临时 krb5.conf 落插件数据目录（store.ResolveDataDir 统一解析，含持久
//     fallback）下的 krb5/ 子目录，内容模板原样（实施文档 §3 映射表）；
//   - GSSAPI 客户端在 internal/ldapgssapi（tiny-rdm backend/ldapgssapi 原样搬运）。
//
// 脱敏红线：本文件任何日志/错误不得携带密码、keytab 内容、票据内容。
package ldapconn

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	ldap "github.com/go-ldap/ldap/v3"
	krbclient "github.com/jcmturner/gokrb5/v8/client"

	"io.dbx.ldap.plugin/internal/ldapgssapi"
	"io.dbx.ldap.plugin/internal/store"
)

// bindLDAPKerberosConnection 建立 GSSAPI 客户端并执行 SASL GSSAPI bind
// （tiny-rdm bindLDAPKerberosConnection :1268 同构；authzID 缺省空）。
func bindLDAPKerberosConnection(ctx context.Context, conn *ldap.Conn, profile Profile, logicalHost string, kerberosPassword string) error {
	client, cleanup, err := newLDAPGSSAPIClient(ctx, profile, kerberosPassword)
	if err != nil {
		return err
	}
	defer cleanup()
	defer client.Close() //nolint:errcheck // Destroy() 仅清内存凭据
	return conn.GSSAPIBind(client, ldapKerberosServicePrincipal(profile, logicalHost), strings.TrimSpace(profile.AuthzID))
}

// newLDAPGSSAPIClient 按 credential_type 构造 gokrb5 客户端
// （tiny-rdm newLDAPGSSAPIClient :1493 同构；DisablePAFXFAST(true) 原样）。
// 返回的 cleanup 释放临时 krb5.conf；失败时 cleanup 已被调用。
func newLDAPGSSAPIClient(_ context.Context, profile Profile, kerberosPassword string) (*ldapgssapi.Client, func(), error) {
	krb := NormalizeLDAPKerberosConfig(derefKerberos(profile))
	configPath, cleanup, err := ldapKerberosConfigPath(krb)
	if err != nil {
		return nil, func() {}, err
	}
	noop := func() {}

	settings := []func(*krbclient.Settings){krbclient.DisablePAFXFAST(true)}
	options := ldapGSSAPIClientOptions(profile)

	switch krb.CredentialType {
	case "keytab":
		username, realm, err := ldapKerberosPrincipalValues(krb, profile.Username, profile.BindDN)
		if err != nil {
			cleanup()
			return nil, noop, err
		}
		if krb.KeytabPath == "" {
			cleanup()
			return nil, noop, fmt.Errorf("kerberos keytab file is required")
		}
		client, err := ldapgssapi.NewClientWithKeytab(username, realm, krb.KeytabPath, configPath, options, settings...)
		if err != nil {
			cleanup()
			return nil, noop, err
		}
		return client, cleanup, nil
	case "ccache":
		if krb.CCachePath == "" {
			cleanup()
			return nil, noop, fmt.Errorf("kerberos ccache file is required")
		}
		client, err := ldapgssapi.NewClientFromCCache(krb.CCachePath, configPath, options, settings...)
		if err != nil {
			cleanup()
			return nil, noop, err
		}
		return client, cleanup, nil
	default:
		username, realm, err := ldapKerberosPrincipalValues(krb, profile.Username, profile.BindDN)
		if err != nil {
			cleanup()
			return nil, noop, err
		}
		password := firstLDAPNonEmpty(kerberosPassword)
		if password == "" {
			cleanup()
			return nil, noop, fmt.Errorf("kerberos password is required")
		}
		client, err := ldapgssapi.NewClientWithPassword(username, realm, password, configPath, options, settings...)
		if err != nil {
			cleanup()
			return nil, noop, err
		}
		return client, cleanup, nil
	}
}

func derefKerberos(profile Profile) LDAPKerberosConfig {
	if profile.Kerberos != nil {
		return *profile.Kerberos
	}
	return LDAPKerberosConfig{}
}

// ldapKerberosPrincipalValues 从 username/bindDn 拆出 principal 与 realm
// （tiny-rdm :1598 原样：username 含 @REALM 时 realm 可从 username 兜底）。
func ldapKerberosPrincipalValues(krb LDAPKerberosConfig, username, bindDN string) (string, string, error) {
	rawUsername := firstLDAPNonEmpty(krb.Username, username, bindDN)
	rawRealm := firstLDAPNonEmpty(krb.Realm)
	if strings.Contains(rawUsername, "@") {
		parts := strings.SplitN(rawUsername, "@", 2)
		rawUsername = strings.TrimSpace(parts[0])
		if rawRealm == "" {
			rawRealm = strings.TrimSpace(parts[1])
		}
	}
	if rawUsername == "" {
		return "", "", fmt.Errorf("kerberos username is required")
	}
	if rawRealm == "" {
		return "", "", fmt.Errorf("kerberos realm is required")
	}
	return rawUsername, strings.ToUpper(rawRealm), nil
}

// ldapKerberosConfigPath 解析 krb5.conf 来源（tiny-rdm :1625 原样）：
// inline realm+kdcHost → 临时文件；显式 krb5_conf_path → 原路径；否则
// /etc/krb5.conf 存在则用之。
func ldapKerberosConfigPath(krb LDAPKerberosConfig) (string, func(), error) {
	if strings.TrimSpace(krb.Realm) != "" && strings.TrimSpace(krb.KDCHost) != "" {
		path, err := ldapWriteTempKrb5Conf(krb)
		if err != nil {
			return "", func() {}, err
		}
		return path, func() { _ = os.Remove(path) }, nil
	}
	if trimmed := strings.TrimSpace(krb.Krb5ConfPath); trimmed != "" {
		return trimmed, func() {}, nil
	}
	const defaultPath = "/etc/krb5.conf"
	if _, err := os.Stat(defaultPath); err == nil {
		return defaultPath, func() {}, nil
	}
	return "", func() {}, fmt.Errorf("kerberos krb5.conf or inline realm/kdc config is required")
}

// ldapWriteTempKrb5Conf 生成临时 krb5.conf（tiny-rdm :1649 模板原样）。
// 落点：插件数据目录（store.ResolveDataDir 统一解析）/krb5/，不可用时退回
// 系统临时目录。文件权限 0600；内容仅 realm/kdc 拓扑，不含凭据。
func ldapWriteTempKrb5Conf(krb LDAPKerberosConfig) (string, error) {
	realm := strings.ToUpper(strings.TrimSpace(krb.Realm))
	kdcHost := strings.TrimSpace(krb.KDCHost)
	if realm == "" || kdcHost == "" {
		return "", fmt.Errorf("kerberos realm and kdcHost are required")
	}
	kdcPort := krb.KDCPort
	if kdcPort <= 0 {
		kdcPort = 88
	}
	defaultDomain := strings.ToLower(realm)
	udpPreferenceLine := ""
	if kdcPort != 88 {
		// Non-standard KDC ports in enterprise environments are often exposed only via TCP.
		udpPreferenceLine = " udp_preference_limit = 1\n"
	}
	content := fmt.Sprintf(`[libdefaults]
 default_realm = %s
 dns_lookup_realm = false
 dns_lookup_kdc = false
 rdns = false
%s

[realms]
 %s = {
  kdc = %s:%d
  admin_server = %s:%d
  default_domain = %s
 }

[domain_realm]
 .%s = %s
 %s = %s
`, realm, udpPreferenceLine, realm, kdcHost, kdcPort, kdcHost, kdcPort, defaultDomain, defaultDomain, realm, defaultDomain, realm)

	dir := krb5TempDir()
	file, err := os.CreateTemp(dir, "dbx-ldap-krb5-*.conf")
	if err != nil {
		return "", fmt.Errorf("create temp krb5.conf: %w", err)
	}
	defer file.Close() //nolint:errcheck // 只写一次，写错误在下方返回
	if _, err := file.WriteString(content); err != nil {
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("write temp krb5.conf: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("chmod temp krb5.conf: %w", err)
	}
	return file.Name(), nil
}

// krb5TempDir 返回临时 krb5.conf 的目录：复用插件数据目录统一解析
// （store.ResolveDataDir，与 store 包 fallback 顺序一致）下的 krb5/ 子目录，
// MkdirAll 失败时退回 os.TempDir()。文件在连接结束即删，不留残留。
func krb5TempDir() string {
	dir := filepath.Join(store.ResolveDataDir(os.Getenv, runtime.GOOS), "krb5")
	if err := os.MkdirAll(dir, 0o700); err == nil {
		return dir
	}
	return os.TempDir()
}

// ldapKerberosServicePrincipal 推导服务 SPN（tiny-rdm :1676 收敛版）：
// 本仓无 endpoint rewrite / 显式 SPN / serviceName 字段（manifest §4 未暴露），
// 统一 ldap/<URL 逻辑主机小写>；logicalHost 为空时从 URL hostname 兜底
// （等价 tiny-rdm target.KerberosSPNHost ← parsed.Hostname 链）。
func ldapKerberosServicePrincipal(profile Profile, logicalHost string) string {
	host := firstLDAPNonEmpty(logicalHost)
	if host == "" {
		if parsed, err := url.Parse(profile.URL); err == nil {
			host = parsed.Hostname()
		}
	}
	return fmt.Sprintf("ldap/%s", strings.ToLower(host))
}

// ldapGSSAPIClientOptions GSSAPI 上下文选项（tiny-rdm :1690 同构）：
// integrity 恒开；confidentiality 仅 qop=auth-conf；mutual 随显式开关。
// qop 缺省 auth。
func ldapGSSAPIClientOptions(profile Profile) ldapgssapi.ClientOptions {
	qop := strings.ToLower(strings.TrimSpace(profile.SASLQoP))
	if qop == "" {
		qop = "auth"
	}
	return ldapgssapi.ClientOptions{
		UseIntegrity:       true,
		UseConfidentiality: qop == "auth-conf",
		MutualAuth:         profile.SASLMutualAuth,
	}
}

// ldapShouldRetryKerberosWithPort88 判定是否应做 KDC 88 回退
// （tiny-rdm :1286 原样：仅当配置端口非 88 且错误是 KDC 网络类）。
func ldapShouldRetryKerberosWithPort88(profile Profile, err error) bool {
	if err == nil {
		return false
	}
	krb := NormalizeLDAPKerberosConfig(derefKerberos(profile))
	if krb.KDCPort <= 0 || krb.KDCPort == 88 {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	networkHints := []string{
		"communication error with kdc via tcp",
		"failed sending as_req to kdc",
		"error reading response size header",
		"error sending to a kdc",
		"connection reset",
		"eof",
	}
	for _, hint := range networkHints {
		if strings.Contains(message, hint) {
			return true
		}
	}
	return false
}

// ldapKerberosPort88FallbackProfile 构造 KDC 88 回退用的 Profile 副本
// （tiny-rdm :1300 同构；本仓无 KDCNetworkAddress 覆盖项，仅改端口）。
func ldapKerberosPort88FallbackProfile(profile Profile) (Profile, bool) {
	krb := NormalizeLDAPKerberosConfig(derefKerberos(profile))
	if krb.KDCPort <= 0 || krb.KDCPort == 88 {
		return profile, false
	}
	fallback := profile
	normalized := krb
	fallback.Kerberos = &normalized
	fallback.Kerberos.KDCPort = 88
	return fallback, true
}
