package lifecycle

import (
	"encoding/json"
	"testing"
)

func mustParse(t *testing.T, raw string) *Params {
	t.Helper()
	params, err := Parse(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return params
}

// 完整形状对照 M0 文档 §3.1。
const fullParams = `{
  "provider": { "id": "io.dbx.ldap.connection", "databaseType": "ldap" },
  "connection": {
    "id": " conn-1 ",
    "name": "prod-ldap",
    "host": "ldap.example.com",
    "port": 636,
    "username": "mgr",
    "password": "",
    "external_config": {
      "auth_type": "simple",
      "base_dn": "dc=example,dc=com",
      "use_starttls": true,
      "timeout_secs": 15,
      "tls_verify": false,
      "allowed_base_dns": "dc=example,dc=com\n  \ndc=extra,dc=com",
      "blocked_attributes": ["userPassword", "  ", "token"]
    },
    "connection_secrets": { "bind_password": " s3cret " }
  },
  "runtime": { "host": "127.0.0.1", "port": 6360 },
  "operationId": "op-42"
}`

func TestParseFullShape(t *testing.T) {
	params := mustParse(t, fullParams)

	if got := params.Provider.ID; got != "io.dbx.ldap.connection" {
		t.Errorf("provider.id = %q", got)
	}
	if got := params.Provider.DatabaseType; got != "ldap" {
		t.Errorf("provider.databaseType = %q", got)
	}
	if got := params.ConnectionID(); got != "conn-1" {
		t.Errorf("connection.id trim = %q", got)
	}
	if got := params.Connection.Host; got != "ldap.example.com" {
		t.Errorf("connection.host = %q", got)
	}
	if got := params.Connection.Port; got != 636 {
		t.Errorf("connection.port = %d", got)
	}
	if got := params.Runtime.Host; got != "127.0.0.1" {
		t.Errorf("runtime.host = %q", got)
	}
	if got := params.Runtime.Port; got != 6360 {
		t.Errorf("runtime.port = %d", got)
	}
	if got := params.OperationID; got != "op-42" {
		t.Errorf("operationId = %q", got)
	}
}

func TestConfigGetters(t *testing.T) {
	params := mustParse(t, fullParams)

	if got := params.ConfigString("auth_type"); got != "simple" {
		t.Errorf("ConfigString(auth_type) = %q", got)
	}
	if got := params.ConfigString("base_dn"); got != "dc=example,dc=com" {
		t.Errorf("ConfigString(base_dn) = %q", got)
	}
	if got := params.ConfigString("missing"); got != "" {
		t.Errorf("ConfigString(missing) = %q", got)
	}
	if !params.ConfigBool("use_starttls") {
		t.Error("ConfigBool(use_starttls) = false, want true")
	}
	if params.ConfigBool("tls_verify") {
		t.Error("ConfigBool(tls_verify) = true, want false")
	}
	if got := params.ConfigInt("timeout_secs"); got != 15 {
		t.Errorf("ConfigInt(timeout_secs) = %d", got)
	}

	// textarea 多行字符串按行拆分，空行丢弃。
	got := params.ConfigStringSlice("allowed_base_dns")
	want := []string{"dc=example,dc=com", "dc=extra,dc=com"}
	if len(got) != len(want) {
		t.Fatalf("ConfigStringSlice(allowed_base_dns) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ConfigStringSlice()[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// 数组形式同样支持，空白项丢弃。
	got = params.ConfigStringSlice("blocked_attributes")
	if len(got) != 2 || got[0] != "userPassword" || got[1] != "token" {
		t.Errorf("ConfigStringSlice(blocked_attributes) = %v", got)
	}
}

func TestSecretString(t *testing.T) {
	params := mustParse(t, fullParams)
	if got := params.SecretString("bind_password"); got != "s3cret" {
		t.Errorf("SecretString(bind_password) = %q", got)
	}
	if got := params.SecretString("missing"); got != "" {
		t.Errorf("SecretString(missing) = %q", got)
	}
}

func TestOperationIDFallbackUUID(t *testing.T) {
	params := mustParse(t, `{"connection": {"id": "c1"}}`)
	fallback := params.OperationIDOrUUID()
	if fallback == "" || fallback == "op-42" {
		t.Fatalf("OperationIDOrUUID fallback = %q", fallback)
	}
	if len(fallback) != 36 || fallback[8] != '-' {
		t.Errorf("fallback is not a uuid: %q", fallback)
	}

	withOp := mustParse(t, `{"operationId": "op-7"}`)
	if got := withOp.OperationIDOrUUID(); got != "op-7" {
		t.Errorf("OperationIDOrUUID = %q, want op-7", got)
	}
}

func TestParseDisconnectMinimal(t *testing.T) {
	// connection/disconnect 可能只带 {connection:{id}}。
	params := mustParse(t, `{"connection": {"id": "c9"}}`)
	if got := params.ConnectionID(); got != "c9" {
		t.Errorf("ConnectionID = %q", got)
	}
	if params.Runtime.Host != "" {
		t.Errorf("Runtime.Host = %q, want empty", params.Runtime.Host)
	}
}

func TestParseEmptyAndInvalid(t *testing.T) {
	params, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse(nil) error = %v", err)
	}
	if params.ConnectionID() != "" {
		t.Errorf("empty params ConnectionID = %q", params.ConnectionID())
	}

	if _, err := Parse(json.RawMessage("{not json")); err == nil {
		t.Error("Parse(invalid) expected error, got nil")
	}
}
