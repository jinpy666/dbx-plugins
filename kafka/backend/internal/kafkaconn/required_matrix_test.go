// required_matrix_test.go（agent I）：schema_registry 决策开关 + required_when
// 矩阵的后端兜底校验（validateRequiredCombination → -32602）与 SR provider
// 开关语义（none 禁用 / 旧连接自动探测回退）单测。
package kafkaconn

import (
	"errors"
	"strings"
	"testing"

	"io.dbx.kafka.plugin/internal/lifecycle"
)

// connectWithConfig 便捷构造：external_config JSON + secrets → Connect。
func connectWithConfig(t *testing.T, service *Service, id, configJSON, secretsJSON string) error {
	t.Helper()
	params, err := lifecycle.Parse([]byte(`{
	  "connection": {
	    "id": "` + id + `",
	    "external_config": ` + configJSON + `,
	    "connection_secrets": ` + secretsJSON + `
	  }
	}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return service.Connect(params)
}

const baseBootstrap = `"bootstrap_servers": "k1:9092"`

func TestRequiredCombinationMatrix(t *testing.T) {
	cases := []struct {
		name        string
		config      string
		secrets     string
		wantErrSub  string // 空 = 期望成功
		wantInvalid bool   // 期望 *InvalidParamsError（-32602）
	}{
		{
			name:    "schema_registry none without sr config ok",
			config:  `{` + baseBootstrap + `, "schema_registry": "none"}`,
			secrets: `{}`,
		},
		{
			name:        "confluent without sr_url",
			config:      `{` + baseBootstrap + `, "schema_registry": "confluent"}`,
			secrets:     `{}`,
			wantErrSub:  "srUrl is required",
			wantInvalid: true,
		},
		{
			name:    "confluent with sr_url ok",
			config:  `{` + baseBootstrap + `, "schema_registry": "confluent", "sr_url": "http://sr:8081"}`,
			secrets: `{}`,
		},
		{
			name:        "aws_glue without region",
			config:      `{` + baseBootstrap + `, "schema_registry": "aws_glue", "glue_registry_name": "r"}`,
			secrets:     `{}`,
			wantErrSub:  "glueRegion is required",
			wantInvalid: true,
		},
		{
			name:        "aws_glue without registry name",
			config:      `{` + baseBootstrap + `, "schema_registry": "aws_glue", "glue_region": "us-east-1"}`,
			secrets:     `{}`,
			wantErrSub:  "glueRegistryName is required",
			wantInvalid: true,
		},
		{
			name:        "aws_glue static without access key id",
			config:      `{` + baseBootstrap + `, "schema_registry": "aws_glue", "glue_region": "us-east-1", "glue_registry_name": "r", "glue_auth_mode": "static"}`,
			secrets:     `{"glue_secret_access_key": "sk"}`,
			wantErrSub:  "glueAccessKeyId is required",
			wantInvalid: true,
		},
		{
			name:        "aws_glue static without secret access key",
			config:      `{` + baseBootstrap + `, "schema_registry": "aws_glue", "glue_region": "us-east-1", "glue_registry_name": "r", "glue_auth_mode": "static", "glue_access_key_id": "ak"}`,
			secrets:     `{}`,
			wantErrSub:  "glueSecretAccessKey is required",
			wantInvalid: true,
		},
		{
			name:    "aws_glue default chain without AK/SK ok",
			config:  `{` + baseBootstrap + `, "schema_registry": "aws_glue", "glue_region": "us-east-1", "glue_registry_name": "r"}`,
			secrets: `{}`,
		},
		{
			name:        "sasl plain without password",
			config:      `{` + baseBootstrap + `, "security_protocol": "SASL_SSL", "sasl_mechanism": "PLAIN", "sasl_username": "app"}`,
			secrets:     `{}`,
			wantErrSub:  "sasl password is required",
			wantInvalid: true,
		},
		{
			name:        "sasl scram without username",
			config:      `{` + baseBootstrap + `, "security_protocol": "SASL_PLAINTEXT", "sasl_mechanism": "SCRAM-SHA-256"}`,
			secrets:     `{"sasl_password": "pw"}`,
			wantErrSub:  "sasl username is required",
			wantInvalid: true,
		},
		{
			name:    "sasl plain with credentials ok",
			config:  `{` + baseBootstrap + `, "security_protocol": "SASL_SSL", "sasl_mechanism": "PLAIN", "sasl_username": "app"}`,
			secrets: `{"sasl_password": "pw"}`,
		},
		{
			name:        "gssapi without principal",
			config:      `{` + baseBootstrap + `, "security_protocol": "SASL_SSL", "sasl_mechanism": "GSSAPI", "kerberos_keytab_path": "/tmp/k.keytab"}`,
			secrets:     `{}`,
			wantErrSub:  "kerberosPrincipal is required",
			wantInvalid: true,
		},
		{
			name:        "gssapi without keytab",
			config:      `{` + baseBootstrap + `, "security_protocol": "SASL_SSL", "sasl_mechanism": "GSSAPI", "kerberos_principal": "app@REALM"}`,
			secrets:     `{}`,
			wantErrSub:  "kerberosKeytabPath is required",
			wantInvalid: true,
		},
		{
			name:    "gssapi with principal and keytab ok (no sasl password needed)",
			config:  `{` + baseBootstrap + `, "security_protocol": "SASL_SSL", "sasl_mechanism": "GSSAPI", "kerberos_principal": "app@REALM", "kerberos_keytab_path": "/tmp/k.keytab"}`,
			secrets: `{}`,
		},
		// --- Phase 3 OAUTHBEARER（§12.2.3）：SASL_SSL 约束 + token 来源矩阵 ---
		{
			name:        "oauthbearer without sasl_ssl",
			config:      `{` + baseBootstrap + `, "security_protocol": "SASL_PLAINTEXT", "sasl_mechanism": "OAUTHBEARER", "msk_region": "us-east-1"}`,
			secrets:     `{}`,
			wantErrSub:  "OAUTHBEARER requires security_protocol SASL_SSL",
			wantInvalid: true,
		},
		{
			name:    "switching to plaintext ignores dormant OAuth credentials",
			config:  `{` + baseBootstrap + `, "security_protocol": "PLAINTEXT", "sasl_mechanism": "OAUTHBEARER"}`,
			secrets: `{}`,
		},
		{
			name:    "switching to TLS without SASL ignores dormant OAuth credentials",
			config:  `{` + baseBootstrap + `, "security_protocol": "SSL", "sasl_mechanism": "OAUTHBEARER"}`,
			secrets: `{}`,
		},
		{
			name:        "oauthbearer msk_iam without region",
			config:      `{` + baseBootstrap + `, "security_protocol": "SASL_SSL", "sasl_mechanism": "OAUTHBEARER"}`,
			secrets:     `{}`,
			wantErrSub:  "mskRegion is required",
			wantInvalid: true,
		},
		{
			name:    "oauthbearer msk_iam with region ok (no sasl username/password needed)",
			config:  `{` + baseBootstrap + `, "security_protocol": "SASL_SSL", "sasl_mechanism": "OAUTHBEARER", "msk_region": "us-east-1"}`,
			secrets: `{}`,
		},
		{
			name:        "oauthbearer msk_iam partial static credentials",
			config:      `{` + baseBootstrap + `, "security_protocol": "SASL_SSL", "sasl_mechanism": "OAUTHBEARER", "msk_region": "us-east-1", "msk_access_key_id": "ak"}`,
			secrets:     `{}`,
			wantErrSub:  "must be provided together",
			wantInvalid: true,
		},
		{
			name:    "oauthbearer msk_iam explicit credentials ok",
			config:  `{` + baseBootstrap + `, "security_protocol": "SASL_SSL", "sasl_mechanism": "OAUTHBEARER", "msk_region": "us-east-1", "msk_access_key_id": "ak"}`,
			secrets: `{"msk_secret_access_key": "sk"}`,
		},
		{
			name:        "oauthbearer static_token without token",
			config:      `{` + baseBootstrap + `, "security_protocol": "SASL_SSL", "sasl_mechanism": "OAUTHBEARER", "oauth_token_source": "static_token"}`,
			secrets:     `{}`,
			wantErrSub:  "oauthStaticToken is required",
			wantInvalid: true,
		},
		{
			name:    "oauthbearer static_token with token ok",
			config:  `{` + baseBootstrap + `, "security_protocol": "SASL_SSL", "sasl_mechanism": "OAUTHBEARER", "oauth_token_source": "static_token"}`,
			secrets: `{"oauth_static_token": "tok"}`,
		},
	}
	for _, tc := range cases {
		service := NewService()
		err := connectWithConfig(t, service, "conn-"+strings.ReplaceAll(tc.name, " ", "-"), tc.config, tc.secrets)
		if tc.wantErrSub == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
			t.Errorf("%s: error = %v, want contains %q", tc.name, err, tc.wantErrSub)
			continue
		}
		if tc.wantInvalid {
			var paramErr *InvalidParamsError
			if !errors.As(err, &paramErr) {
				t.Errorf("%s: error = %T, want *InvalidParamsError (-32602)", tc.name, err)
			}
		}
	}
}

func TestSchemaRegistrySwitchResolvesProvider(t *testing.T) {
	cases := []struct {
		name     string
		profile  Profile
		registry string
		want     string
		wantErr  string
	}{
		// 开关为准：none 显式禁用（即使旧参数残留）。
		{"switch none disables SR", Profile{SchemaRegistry: SchemaRegistryNone, SRURL: "http://sr"}, "", schemaProviderNone, ""},
		{"switch none rejects explicit confluent", Profile{SchemaRegistry: SchemaRegistryNone, SRURL: "http://sr"}, "confluent", "", `disabled`},
		{"switch none rejects explicit glue", Profile{SchemaRegistry: SchemaRegistryNone, GlueRegion: "us-east-1", GlueRegistryName: "r"}, "glue", "", `disabled`},
		// 开关为准：confluent / aws_glue 直取，不再自动探测另一后端。
		{"switch confluent ignores glue fields", Profile{SchemaRegistry: SchemaRegistryConfluent, SRURL: "http://sr", GlueRegion: "us-east-1", GlueRegistryName: "r"}, "", schemaProviderConfluent, ""},
		{"switch aws_glue ignores sr_url", Profile{SchemaRegistry: SchemaRegistryAWSGlue, SRURL: "http://sr", GlueRegion: "us-east-1", GlueRegistryName: "r"}, "", schemaProviderGlue, ""},
		{"switch aws_glue rejects explicit confluent", Profile{SchemaRegistry: SchemaRegistryAWSGlue, GlueRegion: "us-east-1", GlueRegistryName: "r"}, "confluent", "", `not enabled`},
		{"switch confluent rejects explicit glue", Profile{SchemaRegistry: SchemaRegistryConfluent, SRURL: "http://sr"}, "glue", "", `not enabled`},
		// 开关缺失（旧连接）：sr_url / glue_* 自动探测回退，双配置须显式。
		{"legacy auto confluent", Profile{SRURL: "http://sr"}, "", schemaProviderConfluent, ""},
		{"legacy auto glue", Profile{GlueRegion: "us-east-1", GlueRegistryName: "r"}, "", schemaProviderGlue, ""},
		{"legacy both ambiguous", Profile{SRURL: "http://sr", GlueRegion: "us-east-1", GlueRegistryName: "r"}, "", "", "explicitly"},
		{"legacy explicit glue with region only", Profile{GlueRegion: "us-east-1"}, "glue", schemaProviderGlue, ""},
	}
	for _, tc := range cases {
		got, err := resolveSchemaProvider(tc.profile, tc.registry)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: error = %v, want contains %q", tc.name, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: provider = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSchemaRegistryStatusesExposeMode(t *testing.T) {
	service := NewService()
	// confluent 开关连接：statuses mode=confluent。
	if err := connectWithConfig(t, service, "sr-mode-on",
		`{`+baseBootstrap+`, "schema_registry": "confluent", "sr_url": "http://sr:8081"}`, `{}`); err != nil {
		t.Fatalf("connect confluent: %v", err)
	}
	// none 开关连接：statuses mode=none、enabled=false（即使残留 sr_url）。
	if err := connectWithConfig(t, service, "sr-mode-none",
		`{`+baseBootstrap+`, "schema_registry": "none", "sr_url": "http://legacy:8081"}`, `{}`); err != nil {
		t.Fatalf("connect none: %v", err)
	}
	byID := map[string]ConnectionStatus{}
	for _, status := range service.SnapshotStatuses() {
		byID[status.ConnectionID] = status
	}
	srOn := byID["sr-mode-on"].SchemaRegistry
	if srOn == nil || !srOn.Enabled || srOn.Provider != schemaProviderConfluent || srOn.Mode != SchemaRegistryConfluent {
		t.Errorf("confluent status schemaRegistry = %+v", srOn)
	}
	srOff := byID["sr-mode-none"].SchemaRegistry
	if srOff == nil || srOff.Enabled || srOff.Provider != schemaProviderNone || srOff.Mode != SchemaRegistryNone {
		t.Errorf("none status schemaRegistry = %+v", srOff)
	}
}

func TestNormalizeSchemaRegistry(t *testing.T) {
	cases := map[string]string{
		"":           "", // 旧连接：保留空（自动探测回退）
		"none":       "none",
		"Confluent":  "confluent",
		" AWS_GLUE ": "aws_glue",
		"vault":      "none", // 未知值保守回退 none
	}
	for input, want := range cases {
		if got := NormalizeSchemaRegistry(input); got != want {
			t.Errorf("NormalizeSchemaRegistry(%q) = %q, want %q", input, got, want)
		}
	}
}
