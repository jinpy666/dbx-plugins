package kafkaconn

// kerberos.go：Kerberos/GSSAPI（SASL mechanism = GSSAPI，Phase 2，IMPL_PLAN
// §0.2/§5）。依赖 gokrb5/v8 + github.com/twmb/franz-go/pkg/sasl/kerberos
// （tinyrdm 同款）。
//
// 配置面（manifest §4 Phase 2 新增字段，keytab 只收文件路径不收内容）：
//   kerberos_service_name  默认 kafka
//   kerberos_realm         可空（空则从 principal@REALM 解析）
//   kerberos_principal     必填（user@REALM）
//   kerberos_keytab_path   必填（keytab 文件路径，gokrb5 keytab.Load）
//   kerberos_krb5_conf_path 可空（空则回落 $KRB5_CONFIG，再回落 /etc/krb5.conf）
//
// 无真实 KDC 的单测只覆盖参数构造/校验（principal 解析、keytab 必填、
// krb5.conf 回落、合成 keytab + krb5.conf 下机制可构建）——不发起认证。

import (
	"fmt"
	"os"
	"strings"

	"github.com/jcmturner/gokrb5/v8/client"
	"github.com/jcmturner/gokrb5/v8/config"
	"github.com/jcmturner/gokrb5/v8/keytab"
	franzkerberos "github.com/twmb/franz-go/pkg/sasl/kerberos"
)

// SASL 机制常量 GSSAPI 定义在 types.go 取值面；此处仅 kerberos 专属默认值。
const (
	defaultKerberosServiceName = "kafka"
	defaultKrb5ConfPath        = "/etc/krb5.conf"
)

// kerberosParams 是一次 GSSAPI 拨号的已解析参数（buildKerberosSASLMechanism 输入）。
type kerberosParams struct {
	ServiceName  string
	Principal    string // user@REALM 原样
	Username     string // principal 的 user 段
	Realm        string // 显式 kerberos_realm 优先，否则取 principal 段
	KeytabPath   string
	Krb5ConfPath string // 已解析（显式 > $KRB5_CONFIG > /etc/krb5.conf > 空）
}

// normalizeKerberosServiceName 服务名归一（空回 kafka）。
func normalizeKerberosServiceName(service string) string {
	service = strings.TrimSpace(service)
	if service == "" {
		return defaultKerberosServiceName
	}
	return service
}

// parseKerberosPrincipal 解析 user@REALM（无 @ 报错；user/realm 均去空白）。
func parseKerberosPrincipal(principal string) (username, realm string, err error) {
	principal = strings.TrimSpace(principal)
	if principal == "" {
		return "", "", errf("kerberos principal is required")
	}
	at := strings.LastIndexByte(principal, '@')
	if at <= 0 || at == len(principal)-1 {
		return "", "", errf("kerberos principal %q must be in user@REALM form", principal)
	}
	username = strings.TrimSpace(principal[:at])
	realm = strings.TrimSpace(principal[at+1:])
	if username == "" || realm == "" {
		return "", "", errf("kerberos principal %q must be in user@REALM form", principal)
	}
	// user 段内不允许再出现 @（"user@@REALM" 这类形状视为非法）。
	if strings.Contains(username, "@") {
		return "", "", errf("kerberos principal %q must be in user@REALM form", principal)
	}
	return username, realm, nil
}

// resolveKrb5ConfPath krb5.conf 解析顺序：显式路径 > $KRB5_CONFIG（首个条目）
// > 系统默认（defaultPath）。返回空串表示找不到（调用方决定是否报错）。
// 参数注入便于单测（不依赖宿主机 /etc/krb5.conf）。
func resolveKrb5ConfPath(explicit, envValue, defaultPath string) string {
	if path := strings.TrimSpace(explicit); path != "" {
		return path
	}
	if env := strings.TrimSpace(envValue); env != "" {
		// $KRB5_CONFIG 冒号分隔多路径，取第一个存在性交给 Load 判定。
		if first := strings.SplitN(env, ":", 2)[0]; first != "" {
			return strings.TrimSpace(first)
		}
	}
	if defaultPath != "" {
		if info, err := os.Stat(defaultPath); err == nil && !info.IsDir() {
			return defaultPath
		}
	}
	return ""
}

// buildKerberosParams 由 Profile 构造 GSSAPI 参数（纯校验、不拨号）。
func buildKerberosParams(profile Profile) (kerberosParams, error) {
	if profile.SASLMechanism != SASLMechanismGSSAPI {
		return kerberosParams{}, errf("sasl mechanism %q is not GSSAPI", profile.SASLMechanism)
	}
	params := kerberosParams{
		ServiceName: normalizeKerberosServiceName(profile.KerberosServiceName),
		Principal:   strings.TrimSpace(profile.KerberosPrincipal),
		KeytabPath:  strings.TrimSpace(profile.KerberosKeytabPath),
	}
	username, realm, err := parseKerberosPrincipal(params.Principal)
	if err != nil {
		return kerberosParams{}, err
	}
	params.Username = username
	params.Realm = strings.TrimSpace(profile.KerberosRealm)
	if params.Realm == "" {
		params.Realm = realm
	}
	if params.KeytabPath == "" {
		return kerberosParams{}, errf("kerberos keytab path is required for GSSAPI (file path, not content)")
	}
	params.Krb5ConfPath = resolveKrb5ConfPath(profile.KerberosKrb5ConfPath, os.Getenv("KRB5_CONFIG"), defaultKrb5ConfPath)
	return params, nil
}

// buildKerberosSASLMechanism 构建 franz-go GSSAPI 机制（keytab/krb5.conf 在
// 此处落盘读取；真实认证发生在 kgo 拨号时，构建本身不访问 KDC）。
func buildKerberosSASLMechanism(params kerberosParams) (franzkerberos.Auth, error) {
	kt, err := keytab.Load(params.KeytabPath)
	if err != nil {
		return franzkerberos.Auth{}, fmt.Errorf("load kerberos keytab %q: %w", params.KeytabPath, err)
	}
	if params.Krb5ConfPath == "" {
		return franzkerberos.Auth{}, errf("krb5.conf not found: set kerberosKrb5ConfPath, KRB5_CONFIG, or install /etc/krb5.conf")
	}
	cfg, err := config.Load(params.Krb5ConfPath)
	if err != nil {
		return franzkerberos.Auth{}, fmt.Errorf("load kerberos config %q: %w", params.Krb5ConfPath, err)
	}
	// tinyrdm 同款：禁 PA-FX-FAST（部分 KDC 不支持）+ 持久 client（kgo 关闭
	// 时随机制销毁，避免每连接泄漏续期 goroutine）。
	krbClient := client.NewWithKeytab(params.Username, params.Realm, kt, cfg, client.DisablePAFXFAST(true))
	return franzkerberos.Auth{
		Client:           krbClient,
		Service:          params.ServiceName,
		PersistAfterAuth: true,
	}, nil
}
