// manifest 契约守卫：连接 provider 字段与七语 label 必须与后端消费的
// binding/取值面对齐（IMPL_PLAN §4）。并行实施期 manifest 由 C 路产出，
// 文件缺失时 t.Skip("manifest not ready")（收口主线重跑本测试）。
package kafkaconn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type manifestField struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Binding  string `json:"binding"`
	Required bool   `json:"required"`
	VisibleWhen *struct {
		Field string   `json:"field"`
		OneOf []string `json:"one_of"`
	} `json:"visible_when"`
	Options []struct {
		Label string `json:"label"`
		Value string `json:"value"`
	} `json:"options"`
	Default any `json:"default"`
}

type manifestDoc struct {
	ID            string `json:"id"`
	Contributions []struct {
		Type         string          `json:"type"`
		ID           string          `json:"id"`
		DatabaseType string          `json:"database_type"`
		Capabilities []string        `json:"capabilities"`
		Fields       []manifestField `json:"fields"`
	} `json:"contributions"`
	Localizations map[string]struct {
		Contributions map[string]struct {
			Fields map[string]struct {
				Label       string            `json:"label"`
				Description string            `json:"description"`
				Options     map[string]string `json:"options"`
			} `json:"fields"`
		} `json:"contributions"`
	} `json:"localizations"`
}

func loadKafkaManifest(t *testing.T) *manifestDoc {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "manifest.json"))
	if err != nil {
		if os.IsNotExist(err) || strings.Contains(err.Error(), "no such file") {
			t.Skip("manifest not ready")
		}
		t.Fatalf("read manifest.json: %v", err)
	}
	var doc manifestDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse manifest.json: %v", err)
	}
	return &doc
}

func kafkaProviderFields(doc *manifestDoc) map[string]manifestField {
	fields := map[string]manifestField{}
	for _, contribution := range doc.Contributions {
		if contribution.Type != "connection-provider" {
			continue
		}
		for _, field := range contribution.Fields {
			fields[field.Key] = field
		}
	}
	return fields
}

func TestManifestConnectionProvider(t *testing.T) {
	doc := loadKafkaManifest(t)
	var provider *struct {
		ID           string
		DatabaseType string
		Capabilities []string
	}
	for _, contribution := range doc.Contributions {
		if contribution.Type == "connection-provider" {
			provider = &struct {
				ID           string
				DatabaseType string
				Capabilities []string
			}{contribution.ID, contribution.DatabaseType, contribution.Capabilities}
		}
	}
	if provider == nil {
		t.Fatal("connection-provider contribution missing")
	}
	if provider.ID != "io.dbx.kafka.connection" {
		t.Errorf("provider id = %q, want io.dbx.kafka.connection", provider.ID)
	}
	if provider.DatabaseType != "kafka" {
		t.Errorf("database_type = %q, want kafka", provider.DatabaseType)
	}
	for _, capability := range []string{"test", "connect", "disconnect"} {
		found := false
		for _, item := range provider.Capabilities {
			if item == capability {
				found = true
			}
		}
		if !found {
			t.Errorf("capability %q missing", capability)
		}
	}
}

// TestManifestBackendFieldContract 守卫后端 NewProfileFromLifecycle 消费的
// 字段 key/binding 与 manifest 一致（config vs secret 落点，§4）。
func TestManifestBackendFieldContract(t *testing.T) {
	doc := loadKafkaManifest(t)
	fields := kafkaProviderFields(doc)

	// 后端 ConfigString/ConfigStringSlice/ConfigBool 消费的 config 字段。
	wantConfig := []string{
		"bootstrap_servers", "security_protocol", "sasl_mechanism", "sasl_username",
		"tls_ca_cert", "tls_client_cert", "tls_insecure_skip_verify", "client_id",
		"read_only", "allow_delete",
	}
	for _, key := range wantConfig {
		field, ok := fields[key]
		if !ok {
			t.Fatalf("manifest field %q missing", key)
		}
		if field.Binding != "config" {
			t.Errorf("%s binding = %q, want config", key, field.Binding)
		}
		if field.Label == "" {
			t.Errorf("%s label empty", key)
		}
	}

	// 凭据红线：sasl_password / tls_client_key 必须 secret binding。
	for _, key := range []string{"sasl_password", "tls_client_key"} {
		field, ok := fields[key]
		if !ok {
			t.Fatalf("manifest field %q missing", key)
		}
		if field.Binding != "secret" {
			t.Fatalf("%s binding = %q, want secret (凭据红线)", key, field.Binding)
		}
	}

	// display_name 是 name binding 且 required。
	if field, ok := fields["display_name"]; !ok || field.Binding != "name" || !field.Required {
		t.Errorf("display_name = %+v, want name binding + required", fields["display_name"])
	}

	// security_protocol 取值面与后端 NormalizeSecurityProtocol 一致。
	protocolValues := map[string]bool{}
	for _, option := range fields["security_protocol"].Options {
		protocolValues[option.Value] = true
	}
	for _, want := range []string{"PLAINTEXT", "SSL", "SASL_PLAINTEXT", "SASL_SSL"} {
		if !protocolValues[want] {
			t.Errorf("security_protocol options missing %q", want)
		}
	}
	// sasl_mechanism 取值面与 buildSASLOpt 一致。
	mechanismValues := map[string]bool{}
	for _, option := range fields["sasl_mechanism"].Options {
		mechanismValues[option.Value] = true
	}
	for _, want := range []string{"PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512"} {
		if !mechanismValues[want] {
			t.Errorf("sasl_mechanism options missing %q", want)
		}
	}

	// visible_when：SASL 字段挂在 security_protocol ∈ SASL 两态上。
	saslProtocols := map[string]bool{"SASL_PLAINTEXT": true, "SASL_SSL": true}
	for _, key := range []string{"sasl_mechanism", "sasl_username", "sasl_password"} {
		field := fields[key]
		if field.VisibleWhen == nil || field.VisibleWhen.Field != "security_protocol" {
			t.Errorf("%s visible_when missing security_protocol", key)
			continue
		}
		for _, value := range field.VisibleWhen.OneOf {
			if !saslProtocols[value] {
				t.Errorf("%s visible_when contains non-SASL protocol %q", key, value)
			}
		}
	}
}

// TestManifestSevenLanguages 七语 localizations 完整性（M0 硬性规则）。
func TestManifestSevenLanguages(t *testing.T) {
	doc := loadKafkaManifest(t)
	wantLocales := []string{"en", "zh-CN", "zh-TW", "es", "it", "ja", "pt-BR"}
	if len(doc.Localizations) != len(wantLocales) {
		t.Fatalf("localizations = %d blocks, want %d", len(doc.Localizations), len(wantLocales))
	}
	for _, lang := range wantLocales {
		loc, ok := doc.Localizations[lang]
		if !ok {
			t.Fatalf("localization %q missing", lang)
		}
		connFields := loc.Contributions["io.dbx.kafka.connection"].Fields
		for _, key := range append([]string(nil),
			"display_name", "bootstrap_servers", "security_protocol", "sasl_mechanism",
			"sasl_username", "sasl_password", "tls_ca_cert", "tls_client_cert",
			"tls_client_key", "tls_insecure_skip_verify", "client_id", "read_only", "allow_delete") {
			entry, ok := connFields[key]
			if !ok || entry.Label == "" {
				t.Fatalf("localization %s/%s label missing", lang, key)
			}
		}
		securityLoc := connFields["security_protocol"]
		for _, want := range []string{"PLAINTEXT", "SSL", "SASL_PLAINTEXT", "SASL_SSL"} {
			if securityLoc.Options[want] == "" {
				t.Fatalf("localization %s security_protocol option %q label missing", lang, want)
			}
		}
		mechanismLoc := connFields["sasl_mechanism"]
		for _, want := range []string{"PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512"} {
			if mechanismLoc.Options[want] == "" {
				t.Fatalf("localization %s sasl_mechanism option %q label missing", lang, want)
			}
		}
	}
}

// TestManifestIDMatchesPluginID manifest 顶层 id 与 sidecar Metadata 一致。
func TestManifestIDMatchesPluginID(t *testing.T) {
	doc := loadKafkaManifest(t)
	if doc.ID != "io.dbx.kafka" {
		t.Errorf("manifest id = %q, want io.dbx.kafka", doc.ID)
	}
}
