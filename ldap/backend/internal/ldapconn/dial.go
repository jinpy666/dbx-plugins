// dial.go（L-A 路）：dial + bind + TLS，移植自 tiny-rdm
// ldap_service.go:1052-1346（dialProfile/bindLDAPConnection/ldapTLSConfig）。
//
// 相对 tiny-rdm 的改造（实施文档 §3 ⑤）：
//   - 删除 prepareLDAPTransport / resolveLDAPTarget / endpoint rewrite /
//     ldapi 隧道逻辑（DBX 传输层替代，方案 D5）；
//   - 一律拨 runtime.host:port（connTarget），TLS SNI / 逻辑主机用
//     connection.host（即 URL 的 hostname）；
//   - M1 认证范围：simple / anonymous / unauthenticated；M3 补齐 external /
//     digest_md5 / ntlm / ntlm_hash / kerberos（bindLDAPConnection 分发，
//     Kerberos 细节在 auth_gssapi.go）。
package ldapconn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	ldap "github.com/go-ldap/ldap/v3"
)

// bindSecrets 是一次 bind 需要的凭据集合（binding: secret；仅存内存连接表，
// 禁止写入日志/审计/事件/返回值）。
type bindSecrets struct {
	// BindPassword 服务 simple / digest_md5 / ntlm（unauthenticated 不需要）。
	BindPassword string
	// KerberosPassword 仅 auth_type=kerberos 且 credential_type=password 时使用。
	KerberosPassword string
}

// dialProfile 建立 LDAP 连接：dial → (StartTLS) → bind。
// 返回的 conn 已完成 bind；调用方负责 Close。
func dialProfile(ctx context.Context, profile Profile, target connTarget, secrets bindSecrets) (*ldap.Conn, error) {
	parsed, err := url.Parse(profile.URL)
	if err != nil {
		return nil, fmt.Errorf("parse ldap url: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "ldap" && scheme != "ldaps" && scheme != "ldapi" {
		return nil, fmt.Errorf("unsupported ldap url scheme %q", parsed.Scheme)
	}

	dialURL := profile.URL
	if scheme != "ldapi" {
		// 方案 D5：网络层一律连 runtime.host:port；URL 仅保留 scheme 与逻辑主机。
		host := firstLDAPNonEmpty(target.Host, parsed.Hostname())
		if host == "" {
			return nil, fmt.Errorf("ldap url host is required")
		}
		port := target.Port
		if port <= 0 {
			port = ldapURLPort(parsed)
		}
		host, port = normalizeDialHost(host, port)
		clone := *parsed
		clone.Host = net.JoinHostPort(host, strconv.Itoa(port))
		dialURL = clone.String()
	}

	timeout := time.Duration(profile.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	// 逻辑主机（TLS SNI 兜底）= connection.host = URL hostname。
	logicalHost := firstLDAPNonEmpty(parsed.Hostname())
	tlsConfig, err := ldapTLSConfig(profile, scheme == "ldaps" || profile.UseStartTLS, logicalHost)
	if err != nil {
		return nil, err
	}

	opts := []ldap.DialOpt{ldap.DialWithDialer(&net.Dialer{Timeout: timeout})}
	if scheme == "ldaps" {
		opts = append(opts, ldap.DialWithTLSConfig(tlsConfig))
	}

	conn, err := ldap.DialURL(dialURL, opts...)
	if err != nil {
		return nil, fmt.Errorf("dial ldap: %w", err)
	}
	if profile.UseStartTLS {
		if scheme == "ldaps" {
			_ = conn.Close()
			return nil, fmt.Errorf("startTLS cannot be combined with ldaps url")
		}
		if err := conn.StartTLS(tlsConfig); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("startTLS: %w", err)
		}
	}
	conn.SetTimeout(timeout)

	if err := bindLDAPConnection(ctx, conn, profile, logicalHost, secrets); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("bind ldap: %w", err)
	}
	return conn, nil
}

// bindLDAPConnection 按 authType 分发 bind（M1：simple/anonymous/unauthenticated；
// M3：external/digest_md5/ntlm/ntlm_hash/kerberos，分发结构对照 tiny-rdm
// bindLDAPConnection :1210-1264 原样，target 收敛为 URL 逻辑主机）。
func bindLDAPConnection(ctx context.Context, conn *ldap.Conn, profile Profile, logicalHost string, secrets bindSecrets) error {
	switch profile.AuthType {
	case LDAPAuthAnonymous, "":
		// AD 拒绝未 bind 连接上的目录操作（Operations Error）：匿名也发
		// RFC 4513 匿名 simple bind（空 DN + 空密码）显式完成 bind。
		_, err := conn.SimpleBind(&ldap.SimpleBindRequest{Username: "", Password: "", AllowEmptyPassword: true})
		return err
	case LDAPAuthUnauthenticated:
		return conn.UnauthenticatedBind(firstLDAPNonEmpty(profile.BindDN, profile.Username))
	case LDAPAuthSimple:
		bindDN := firstLDAPNonEmpty(profile.BindDN, profile.Username)
		if bindDN == "" {
			return fmt.Errorf("bindDn or username is required for simple bind")
		}
		return conn.Bind(bindDN, secrets.BindPassword)
	case LDAPAuthNTLM:
		username := firstLDAPNonEmpty(profile.Username, profile.BindDN)
		if username == "" {
			return fmt.Errorf("username or bindDn is required for NTLM bind")
		}
		return conn.NTLMBind(profile.Domain, username, secrets.BindPassword)
	case LDAPAuthNTLMHash:
		username := firstLDAPNonEmpty(profile.Username, profile.BindDN)
		if username == "" {
			return fmt.Errorf("username or bindDn is required for NTLM hash bind")
		}
		if profile.NTLMHash == "" {
			return fmt.Errorf("ntlmHash is required for NTLM hash bind")
		}
		return conn.NTLMBindWithHash(profile.Domain, username, profile.NTLMHash)
	case LDAPAuthDigestMD5:
		username := firstLDAPNonEmpty(profile.Username, profile.BindDN)
		if username == "" {
			return fmt.Errorf("username or bindDn is required for DIGEST-MD5 bind")
		}
		return conn.MD5Bind(resolveLDAPSASLHost(profile, logicalHost), username, secrets.BindPassword)
	case LDAPAuthKerberos:
		err := bindLDAPKerberosConnection(ctx, conn, profile, logicalHost, secrets.KerberosPassword)
		if err == nil {
			return nil
		}
		retryProfile, ok := ldapKerberosPort88FallbackProfile(profile)
		if !ok || !ldapShouldRetryKerberosWithPort88(profile, err) {
			return err
		}
		log.Printf("NOTICE: [dbx-plugin-ldap] kerberos bind failed on configured KDC endpoint, retrying with KDC port 88 fallback")
		if retryErr := bindLDAPKerberosConnection(ctx, conn, retryProfile, logicalHost, secrets.KerberosPassword); retryErr == nil {
			return nil
		} else {
			return fmt.Errorf("%w; fallback to KDC port 88 failed: %v", err, retryErr)
		}
	case LDAPAuthExternal:
		return conn.ExternalBind()
	default:
		return fmt.Errorf("unsupported ldap authType %q", profile.AuthType)
	}
}

// resolveLDAPSASLHost 解析 DIGEST-MD5 的 SASL host（tiny-rdm
// resolveLDAPSASLHost :1470 同构：显式覆盖 → URL 逻辑主机；logicalHost 为空时
// 从 URL hostname 兜底，等价 tiny-rdm target.LogicalHost ← parsed.Hostname）。
func resolveLDAPSASLHost(profile Profile, logicalHost string) string {
	host := firstLDAPNonEmpty(profile.SASLHost, logicalHost)
	if host == "" {
		if parsed, err := url.Parse(profile.URL); err == nil {
			host = parsed.Hostname()
		}
	}
	return host
}

// ldapTLSConfig 构造 TLS 配置（改造自 tiny-rdm ldapTLSConfig :1319）：
// required（ldaps 或 StartTLS）时即使未显式开启也启用；ServerName 缺省用
// connection.host；tls_verify=false → InsecureSkipVerify（连接级显式配置）。
func ldapTLSConfig(profile Profile, required bool, logicalHost string) (*tls.Config, error) {
	enabled := required || profile.TLSServerName != "" || profile.TLSCAPath != ""
	if !enabled {
		return nil, nil
	}
	cfg := &tls.Config{
		InsecureSkipVerify: !profile.TLSVerify, //nolint:gosec // 显式用户配置（tls_verify=false）
		ServerName:         firstLDAPNonEmpty(profile.TLSServerName, logicalHost),
		MinVersion:         tls.VersionTLS12,
	}
	if path := strings.TrimSpace(profile.TLSCAPath); path != "" {
		pem, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read tls_ca_path: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("tls_ca_path %q contains no usable certificate", path)
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}

// ldapURLPort 解析 URL 端口，缺省按 scheme（ldaps=636，其余 389）。
func ldapURLPort(parsed *url.URL) int {
	if port := parsed.Port(); port != "" {
		if value, err := strconv.Atoi(port); err == nil && value > 0 {
			return value
		}
	}
	if strings.EqualFold(parsed.Scheme, "ldaps") {
		return 636
	}
	return 389
}

// normalizeDialHost 容错 runtime.host 里混入的完整 URL：宿主直连路径把
// connection.host 原文透传进 runtime.host（host 仓库 dbx-core
// connection_host_port，不做 scheme 剥离），而 LDAP 连接的 host 绑定按
// manifest 约定是完整 ldap/ldaps URL；不剥离会被 net.JoinHostPort 包进
// []，得到 ldaps://[ldaps:%2F%2F…]:636 这类不可解析目标。
func normalizeDialHost(host string, port int) (string, int) {
	if !strings.Contains(host, "://") {
		return host, port
	}
	parsed, err := url.Parse(host)
	if err != nil || parsed.Hostname() == "" {
		return host, port
	}
	if port <= 0 {
		port = ldapURLPort(parsed)
	}
	return parsed.Hostname(), port
}

// newBaseProbeRequest 是 connection/test 的 base scope 探测（读 1 条）。
func newBaseProbeRequest(baseDN string) *ldap.SearchRequest {
	return ldap.NewSearchRequest(
		baseDN,
		ldap.ScopeBaseObject,
		ldap.NeverDerefAliases,
		1,
		0,
		false,
		"(objectClass=*)",
		[]string{"1.1"}, // no attributes，只探存在性
		nil,
	)
}

// --- 以下 helper 供连接层与领域操作共用（L-B 实现领域方法时直接复用，
// 勿在 policy.go/schema.go 重复定义）。 ---

// ldapNeedsReconnect 判定是否断线类错误（tiny-rdm :1963 原样）。
func ldapNeedsReconnect(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if ldap.IsErrorAnyOf(err, ldap.ErrorNetwork, ldap.LDAPResultServerDown, ldap.LDAPResultTimeout, ldap.LDAPResultConnectError) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "use of closed network connection") ||
		strings.Contains(text, "connection reset") ||
		strings.Contains(text, "unexpected eof") ||
		strings.HasSuffix(text, ": eof") ||
		text == "eof" ||
		strings.Contains(text, "unable to read ldap response packet")
}

// ldapSearchScope 把 base/one/sub 映射为 go-ldap scope（缺省 sub）。
func ldapSearchScope(scope string) int {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "base":
		return ldap.ScopeBaseObject
	case "one":
		return ldap.ScopeSingleLevel
	default:
		return ldap.ScopeWholeSubtree
	}
}

// ldapDerefAliases 把 never/searching/finding/always 映射为 go-ldap deref
// （缺省 never）。
func ldapDerefAliases(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "searching":
		return ldap.DerefInSearching
	case "finding":
		return ldap.DerefFindingBaseObj
	case "always":
		return ldap.DerefAlways
	default:
		return ldap.NeverDerefAliases
	}
}

// normalizeLDAPAttributes 去重去空白（保序）。
func normalizeLDAPAttributes(attrs []string) []string {
	out := make([]string, 0, len(attrs))
	seen := map[string]struct{}{}
	for _, attr := range attrs {
		attr = strings.TrimSpace(attr)
		if attr == "" {
			continue
		}
		key := strings.ToLower(attr)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, attr)
	}
	return out
}

// normalizeLDAPSizeLimit 负值归零（0 = 不限，服务端约束生效）。
func normalizeLDAPSizeLimit(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

// normalizeLDAPPageSize 非正值归零（0 = 不分页）；上限 uint32。
func normalizeLDAPPageSize(value int) uint32 {
	if value <= 0 {
		return 0
	}
	const maxUint32AsInt = int(^uint32(0))
	if value > maxUint32AsInt {
		return ^uint32(0)
	}
	return uint32(value)
}

// contextWithTimeout 按连接 timeout 配置补 ctx deadline
// （tiny-rdm contextWithTimeout :2063 同构）。
func contextWithTimeout(ctx context.Context, profile Profile) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	timeout := time.Duration(profile.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return context.WithTimeout(ctx, timeout)
}
