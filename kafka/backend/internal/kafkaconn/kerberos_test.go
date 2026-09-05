package kafkaconn

// kerberos_test.go：GSSAPI 参数构造与校验（无真实 KDC——只测 principal 解析、
// keytab/krb5.conf 解析与机制构建；合成 keytab 由测试内按 keytab v2 二进制
// 格式生成，无凭据字面量）。

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kconfig "github.com/jcmturner/gokrb5/v8/config"
)

// writeTestKeytab 按 MIT keytab v2 格式合成一个只含单条目的 keytab 文件。
// （格式见 gokrb5 keytab.Unmarshal：头 2 字节 0x05 0x02，随后 int32 记录长度
// + 记录体，0x00000000 结尾；记录体全大端。）
func writeTestKeytab(t *testing.T, principal, realm string) string {
	t.Helper()
	components := strings.Split(principal, "/")

	var entry bytes.Buffer
	writeBE := func(value any) {
		if err := binary.Write(&entry, binary.BigEndian, value); err != nil {
			t.Fatalf("write keytab: %v", err)
		}
	}
	writeString := func(value string) {
		writeBE(uint16(len(value)))
		entry.WriteString(value)
	}
	writeBE(uint16(len(components))) // numComponents
	writeString(realm)               // realm 在组件之前（gokrb5 解析顺序）
	for _, component := range components {
		writeString(component)
	}
	writeBE(int32(1))                  // nameType
	writeBE(uint32(time.Now().Unix())) // timestamp
	entry.WriteByte(1)                 // kvno8
	writeBE(int16(17))                 // enctype aes256-cts-hmac-sha1-96
	key := make([]byte, 32)            // 32 字节 aes256 key（测试内随机生成）
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	writeBE(uint16(len(key)))
	entry.Write(key)
	writeBE(uint32(1)) // kvno32

	var file bytes.Buffer
	file.WriteByte(0x05)
	file.WriteByte(0x02)
	writeBEint32(&file, int32(entry.Len()))
	file.Write(entry.Bytes())
	writeBEint32(&file, 0)

	path := filepath.Join(t.TempDir(), "test.keytab")
	if err := os.WriteFile(path, file.Bytes(), 0o600); err != nil {
		t.Fatalf("write keytab: %v", err)
	}
	return path
}

func writeBEint32(buf *bytes.Buffer, value int32) {
	_ = binary.Write(buf, binary.BigEndian, value)
}

const testKrb5Conf = `[libdefaults]
 default_realm = EXAMPLE.COM
 udp_preference_limit = 1
[realms]
 EXAMPLE.COM = {
  kdc = 127.0.0.1:88
 }
`

func writeTestKrb5Conf(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "krb5.conf")
	if err := os.WriteFile(path, []byte(testKrb5Conf), 0o600); err != nil {
		t.Fatalf("write krb5.conf: %v", err)
	}
	return path
}

func TestParseKerberosPrincipal(t *testing.T) {
	user, realm, err := parseKerberosPrincipal("kafka-client@EXAMPLE.COM")
	if err != nil || user != "kafka-client" || realm != "EXAMPLE.COM" {
		t.Errorf("parse = %q/%q, %v", user, realm, err)
	}
	// host 服务型 principal（多段）取最后一个 @ 前整段为 user。
	user, realm, err = parseKerberosPrincipal("kafka/broker1.example.com@EXAMPLE.COM")
	if err != nil || user != "kafka/broker1.example.com" || realm != "EXAMPLE.COM" {
		t.Errorf("host principal = %q/%q, %v", user, realm, err)
	}
	for _, bogus := range []string{"", "no-realm", "@REALM", "user@", " user@ ", "user@@REALM"} {
		if _, _, err := parseKerberosPrincipal(bogus); err == nil {
			t.Errorf("parseKerberosPrincipal(%q) expected error", bogus)
		}
	}
}

func TestResolveKrb5ConfPath(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "explicit.conf")
	if got := resolveKrb5ConfPath(explicit, "", ""); got != explicit {
		t.Errorf("explicit = %q", got)
	}
	// 显式路径优先于 env/default。
	if got := resolveKrb5ConfPath(explicit, "/env.conf", "/etc/krb5.conf"); got != explicit {
		t.Errorf("explicit priority = %q", got)
	}
	// env 次之（取冒号分隔首条）。
	if got := resolveKrb5ConfPath("", "/a.conf:/b.conf", ""); got != "/a.conf" {
		t.Errorf("env = %q", got)
	}
	// default 存在才回落。
	if got := resolveKrb5ConfPath("", "", filepath.Join(t.TempDir(), "missing.conf")); got != "" {
		t.Errorf("missing default = %q", got)
	}
	existing := writeTestKrb5Conf(t)
	if got := resolveKrb5ConfPath("", "", existing); got != existing {
		t.Errorf("existing default = %q", got)
	}
}

func TestBuildKerberosParams(t *testing.T) {
	profile := Profile{
		SASLMechanism:       SASLMechanismGSSAPI,
		KerberosPrincipal:   "kafka-client@EXAMPLE.COM",
		KerberosKeytabPath:  "/tmp/client.keytab",
		KerberosServiceName: " ", // 空 → 默认 kafka
	}
	params, err := buildKerberosParams(profile)
	if err != nil {
		t.Fatalf("buildKerberosParams() error = %v", err)
	}
	if params.ServiceName != defaultKerberosServiceName {
		t.Errorf("service = %q, want %q", params.ServiceName, defaultKerberosServiceName)
	}
	if params.Username != "kafka-client" || params.Realm != "EXAMPLE.COM" {
		t.Errorf("user/realm = %q/%q", params.Username, params.Realm)
	}
	if params.KeytabPath != "/tmp/client.keytab" {
		t.Errorf("keytab = %q", params.KeytabPath)
	}

	// 显式 realm 覆盖 principal 派生值。
	profile.KerberosRealm = "OTHER.COM"
	params, err = buildKerberosParams(profile)
	if err != nil || params.Realm != "OTHER.COM" {
		t.Errorf("explicit realm = %q, %v", params.Realm, err)
	}

	// keytab 必填（只收路径）。
	profile.KerberosKeytabPath = ""
	if _, err := buildKerberosParams(profile); err == nil || !strings.Contains(err.Error(), "keytab") {
		t.Errorf("missing keytab error = %v", err)
	}
	// principal 必填。
	profile.KerberosKeytabPath = "/tmp/client.keytab"
	profile.KerberosPrincipal = ""
	if _, err := buildKerberosParams(profile); err == nil || !strings.Contains(err.Error(), "principal") {
		t.Errorf("missing principal error = %v", err)
	}
	// 非 GSSAPI 机制 → 拒绝。
	profile.SASLMechanism = SASLMechanismPlain
	if _, err := buildKerberosParams(profile); err == nil {
		t.Error("non-GSSAPI mechanism expected error")
	}
}

func TestBuildKerberosSASLMechanism(t *testing.T) {
	confPath := writeTestKrb5Conf(t)
	keytabPath := writeTestKeytab(t, "kafka-client", "EXAMPLE.COM")

	// 合成 keytab + krb5.conf → 机制可构建（不访问 KDC）。
	auth, err := buildKerberosSASLMechanism(kerberosParams{
		ServiceName:  "kafka",
		Principal:    "kafka-client@EXAMPLE.COM",
		Username:     "kafka-client",
		Realm:        "EXAMPLE.COM",
		KeytabPath:   keytabPath,
		Krb5ConfPath: confPath,
	})
	if err != nil {
		t.Fatalf("buildKerberosSASLMechanism() error = %v", err)
	}
	if auth.Service != "kafka" {
		t.Errorf("auth service = %q", auth.Service)
	}
	if auth.Client == nil {
		t.Error("auth client = nil")
	}
	if !auth.PersistAfterAuth {
		t.Error("PersistAfterAuth = false, want true（kgo 关闭时销毁）")
	}

	// keytab 文件不存在 → 明确错误。
	if _, err := buildKerberosSASLMechanism(kerberosParams{
		ServiceName: "kafka", Username: "u", Realm: "EXAMPLE.COM",
		KeytabPath: filepath.Join(t.TempDir(), "missing.keytab"), Krb5ConfPath: confPath,
	}); err == nil || !strings.Contains(err.Error(), "keytab") {
		t.Errorf("missing keytab file error = %v", err)
	}

	// 无 krb5.conf → 明确错误。
	if _, err := buildKerberosSASLMechanism(kerberosParams{
		ServiceName: "kafka", Username: "u", Realm: "EXAMPLE.COM",
		KeytabPath: keytabPath, Krb5ConfPath: "",
	}); err == nil || !strings.Contains(err.Error(), "krb5.conf") {
		t.Errorf("missing krb5.conf error = %v", err)
	}

	// 坏 krb5.conf：gokrb5 的 ini 解析较宽松（可能不报错），此处只断言
	// 合成 conf 的 default_realm 被正确解析（gokrb5 config.Load 直连验证）。
	cfg, err := kconfig.Load(confPath)
	if err != nil {
		t.Fatalf("config.Load(%q) error = %v", confPath, err)
	}
	if cfg.LibDefaults.DefaultRealm != "EXAMPLE.COM" {
		t.Errorf("DefaultRealm = %q, want EXAMPLE.COM", cfg.LibDefaults.DefaultRealm)
	}
}

func TestBuildSASLOptGSSAPI(t *testing.T) {
	confPath := writeTestKrb5Conf(t)
	keytabPath := writeTestKeytab(t, "kafka-client", "EXAMPLE.COM")
	profile := Profile{
		SecurityProtocol:     SecurityProtocolSASLPlaintext,
		SASLMechanism:        SASLMechanismGSSAPI,
		KerberosPrincipal:    "kafka-client@EXAMPLE.COM",
		KerberosKeytabPath:   keytabPath,
		KerberosKrb5ConfPath: confPath,
	}
	opt, err := buildSASLOpt(profile, connSecrets{})
	if err != nil {
		t.Fatalf("buildSASLOpt(GSSAPI) error = %v", err)
	}
	if opt == nil {
		t.Fatal("buildSASLOpt(GSSAPI) = nil")
	}
}
