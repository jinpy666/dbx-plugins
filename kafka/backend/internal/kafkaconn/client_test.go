package kafkaconn

// client_test.go：TLS/SASL client opts 矩阵与 lifecycle → Profile 映射
// （纯构建，不拨号连网）。

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"io.dbx.kafka.plugin/internal/lifecycle"
)

// testCAPEM 生成自签 CA PEM（测试内动态生成，无凭据字面量）。
func testCAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "dbx-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestBuildSASLOptMatrix(t *testing.T) {
	profile := Profile{
		SecurityProtocol: SecurityProtocolSASLSSL,
		Username:         "test",
	}
	secrets := connSecrets{SASLPassword: "test"}
	for _, mechanism := range []string{SASLMechanismPlain, SASLMechanismSCRAMSHA256, SASLMechanismSCRAMSHA512} {
		profile.SASLMechanism = mechanism
		opt, err := buildSASLOpt(profile, secrets)
		if err != nil {
			t.Fatalf("buildSASLOpt(%s) error = %v", mechanism, err)
		}
		if opt == nil {
			t.Fatalf("buildSASLOpt(%s) = nil", mechanism)
		}
	}
	// 机制缺失 → 明确错误。
	profile.SASLMechanism = ""
	if _, err := buildSASLOpt(profile, secrets); err == nil {
		t.Error("buildSASLOpt(empty mechanism) expected error")
	}
	// GSSAPI（Phase 2 已接入）：缺 principal → 参数错误（完整矩阵见
	// kerberos_test.go）。
	profile.SASLMechanism = SASLMechanismGSSAPI
	if _, err := buildSASLOpt(profile, secrets); err == nil || !strings.Contains(err.Error(), "principal") {
		t.Errorf("buildSASLOpt(GSSAPI without principal) error = %v, want principal error", err)
	}
	// OAUTHBEARER（Phase 3 已接入）：构造期不触网，msk_iam（缺 region →
	// provider 层拒绝）与 static_token（缺 token → 拒绝）见 oauth_test.go；
	// 此处验证合法 msk_iam 参数可构建机制。
	profile.SASLMechanism = SASLMechanismOAUTHBEARER
	profile.OauthTokenSource = OauthTokenSourceMSKIAM
	profile.MSKRegion = "us-east-1"
	oauthOpt, err := buildSASLOpt(profile, connSecrets{})
	if err != nil || oauthOpt == nil {
		t.Errorf("buildSASLOpt(OAUTHBEARER msk_iam) = %v, %v", oauthOpt, err)
	}
	// 未知机制 → 拒绝。
	profile.SASLMechanism = "OAUTHBEARER-BOGUS"
	if _, err := buildSASLOpt(profile, secrets); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("buildSASLOpt(unknown mechanism) error = %v, want unsupported", err)
	}
}

func TestBuildTLSConfigMatrix(t *testing.T) {
	caPEM := testCAPEM(t)

	// 基础：insecure off、无 CA/客户端证书。
	config, err := buildTLSConfig(Profile{}, connSecrets{})
	if err != nil {
		t.Fatalf("buildTLSConfig(base) error = %v", err)
	}
	if config.InsecureSkipVerify {
		t.Error("default InsecureSkipVerify = true, want false")
	}
	if config.RootCAs != nil || len(config.Certificates) != 0 {
		t.Error("default config should have no CA pool / client certificate")
	}
	if config.MinVersion != uint16(tls.VersionTLS12) {
		t.Errorf("MinVersion = %v, want TLS1.2", config.MinVersion)
	}

	// CA 注入。
	config, err = buildTLSConfig(Profile{TLSCACert: caPEM}, connSecrets{})
	if err != nil {
		t.Fatalf("buildTLSConfig(ca) error = %v", err)
	}
	if config.RootCAs == nil {
		t.Error("RootCAs = nil, want populated pool")
	}

	// insecure skip verify。
	config, err = buildTLSConfig(Profile{TLSInsecureSkipVerify: true}, connSecrets{})
	if err != nil {
		t.Fatalf("buildTLSConfig(insecure) error = %v", err)
	}
	if !config.InsecureSkipVerify {
		t.Error("InsecureSkipVerify = false, want true")
	}

	// 无效 CA PEM → 明确错误。
	if _, err := buildTLSConfig(Profile{TLSCACert: "not-a-pem"}, connSecrets{}); err == nil {
		t.Error("buildTLSConfig(invalid CA) expected error")
	}

	// 客户端证书缺 key → 报错；key 经 secret 注入。
	if _, err := buildTLSConfig(Profile{TLSClientCert: caPEM}, connSecrets{}); err == nil {
		t.Error("buildTLSConfig(cert without key) expected error")
	}
}

func TestZooKeeperSelectionIgnoresPreviousBootstrapAndRuntimeSeeds(t *testing.T) {
	entry := &connEntry{
		profile: Profile{ConnectionSource: ConnectionSourceZookeeper, BootstrapServers: []string{"old-broker:9092"}},
		target:  connTarget{Host: "old-tunnel", Port: 19092},
	}
	if got := entry.seedBrokers(); len(got) != 0 {
		t.Fatalf("ZooKeeper mode kept inactive bootstrap seeds: %v", got)
	}
	entry.profile.ConnectionSource = ConnectionSourceBootstrap
	if got := entry.seedBrokers(); len(got) != 1 || got[0] != "old-broker:9092" {
		t.Fatalf("switching back did not restore bootstrap seeds: %v", got)
	}
}

func TestSeedBrokersFallback(t *testing.T) {
	entry := &connEntry{profile: Profile{BootstrapServers: []string{"k1:9092"}}}
	if got := entry.seedBrokers(); len(got) != 1 || got[0] != "k1:9092" {
		t.Errorf("seedBrokers() = %v, want [k1:9092]", got)
	}

	// bootstrap 空 → runtime.host:port 兜底。
	entry = &connEntry{
		profile: Profile{},
		target:  connTarget{Host: "127.0.0.1", Port: 9093},
	}
	got := entry.seedBrokers()
	if len(got) != 1 || got[0] != "127.0.0.1:9093" {
		t.Errorf("seedBrokers() = %v, want [127.0.0.1:9093]", got)
	}

	entry = &connEntry{}
	if got := entry.seedBrokers(); got != nil {
		t.Errorf("seedBrokers() = %v, want nil", got)
	}
}

func TestFingerprintStableAndSensitive(t *testing.T) {
	entry := &connEntry{
		profile: Profile{BootstrapServers: []string{"k1:9092"}, SecurityProtocol: SecurityProtocolSASLSSL, SASLMechanism: SASLMechanismPlain, Username: "u"},
		secrets: connSecrets{SASLPassword: "test"},
	}
	first := entry.computeFingerprint()
	if first == "" || len(first) != 64 {
		t.Fatalf("computeFingerprint() = %q, want sha256 hex", first)
	}
	// 相同配置 → 稳定。
	if again := entry.computeFingerprint(); again != first {
		t.Error("computeFingerprint() not stable")
	}
	// 密码变化 → 摘要变化（凭据参与失效判定）。
	entry.secrets.SASLPassword = "test2"
	if entry.computeFingerprint() == first {
		t.Error("fingerprint unchanged after password change")
	}
	// bootstrap 变化 → 摘要变化。
	entry.secrets.SASLPassword = "test"
	entry.profile.BootstrapServers = []string{"k2:9092"}
	if entry.computeFingerprint() == first {
		t.Error("fingerprint unchanged after bootstrap change")
	}
}

func TestNewProfileFromLifecycle(t *testing.T) {
	params, err := lifecycle.Parse([]byte(`{
	  "connection": {
	    "id": "conn-1",
	    "name": "prod-kafka",
	    "external_config": {
	      "bootstrap_servers": "k1:9092, k2:9092\n k3:9092",
	      "security_protocol": "sasl_ssl",
	      "sasl_mechanism": "SCRAM-SHA-512",
	      "sasl_username": "app",
	      "tls_insecure_skip_verify": true,
	      "read_only": true,
	      "allow_delete": true
	    },
	    "connection_secrets": { "sasl_password": "test" }
	  }
	}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	profile, secrets, err := NewProfileFromLifecycle(params)
	if err != nil {
		t.Fatalf("NewProfileFromLifecycle() error = %v", err)
	}
	if profile.ID != "conn-1" || profile.Name != "prod-kafka" {
		t.Errorf("profile id/name = %q/%q", profile.ID, profile.Name)
	}
	if len(profile.BootstrapServers) != 3 {
		t.Errorf("bootstrap = %v, want 3 servers", profile.BootstrapServers)
	}
	if profile.SecurityProtocol != SecurityProtocolSASLSSL {
		t.Errorf("securityProtocol = %q", profile.SecurityProtocol)
	}
	if profile.SASLMechanism != SASLMechanismSCRAMSHA512 {
		t.Errorf("saslMechanism = %q", profile.SASLMechanism)
	}
	if !profile.TLSInsecureSkipVerify || !profile.ReadOnly || !profile.AllowDelete {
		t.Errorf("flags = %v/%v/%v", profile.TLSInsecureSkipVerify, profile.ReadOnly, profile.AllowDelete)
	}
	if secrets.SASLPassword != "test" {
		t.Errorf("sasl password secret = %q", secrets.SASLPassword)
	}

	// SASL 协议缺用户名 → 明确错误（mechanism 齐备后校验 username）。
	params, _ = lifecycle.Parse([]byte(`{
	  "connection": {
	    "id": "conn-2",
	    "external_config": {
	      "bootstrap_servers": "k1:9092",
	      "security_protocol": "SASL_PLAINTEXT",
	      "sasl_mechanism": "PLAIN"
	    }
	  }
	}`))
	if _, _, err := NewProfileFromLifecycle(params); err == nil || !strings.Contains(err.Error(), "username") {
		t.Errorf("missing username error = %v", err)
	}

	// bootstrap 缺失 → 明确错误。
	params, _ = lifecycle.Parse([]byte(`{"connection": {"id": "conn-3"}}`))
	if _, _, err := NewProfileFromLifecycle(params); err == nil || !strings.Contains(err.Error(), "bootstrap") {
		t.Errorf("missing bootstrap error = %v", err)
	}
}

func TestNormalizeProfileDefaults(t *testing.T) {
	profile := NormalizeProfile(Profile{ID: " c1 "})
	if profile.SecurityProtocol != SecurityProtocolPlaintext {
		t.Errorf("default securityProtocol = %q, want PLAINTEXT", profile.SecurityProtocol)
	}
	if profile.hasTLS() {
		t.Error("hasTLS on PLAINTEXT should be false")
	}
	if profile.hasSASL() {
		t.Error("hasSASL on PLAINTEXT should be false")
	}
	ssl := NormalizeProfile(Profile{ID: "c2", SecurityProtocol: "ssl"})
	if !ssl.hasTLS() || ssl.hasSASL() {
		t.Error("SSL protocol flags wrong")
	}
}
