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

// 完整形状对照 M0 文档 §3.1（kafka 字段按 manifest §4）。
const fullParams = `{
  "provider": { "id": "io.dbx.kafka.connection", "databaseType": "kafka" },
  "connection": {
    "id": " conn-1 ",
    "name": "prod-kafka",
    "username": "",
    "password": "",
    "external_config": {
      "bootstrap_servers": "k1:9092, k2:9092\nk3:9092",
      "security_protocol": "SASL_SSL",
      "sasl_mechanism": "SCRAM-SHA-256",
      "sasl_username": "app",
      "tls_insecure_skip_verify": true,
      "read_only": true,
      "allow_delete": false
    },
    "connection_secrets": { "sasl_password": " s3cret " }
  },
  "runtime": { "host": "127.0.0.1", "port": 9092 },
  "operationId": "op-42"
}`

func TestParseFullShape(t *testing.T) {
	params := mustParse(t, fullParams)

	if got := params.Provider.ID; got != "io.dbx.kafka.connection" {
		t.Errorf("provider.id = %q", got)
	}
	if got := params.Provider.DatabaseType; got != "kafka" {
		t.Errorf("provider.databaseType = %q", got)
	}
	if got := params.ConnectionID(); got != "conn-1" {
		t.Errorf("connection.id trim = %q", got)
	}
	if got := params.Runtime.Host; got != "127.0.0.1" {
		t.Errorf("runtime.host = %q", got)
	}
	if got := params.Runtime.Port; got != 9092 {
		t.Errorf("runtime.port = %d", got)
	}
	if got := params.OperationID; got != "op-42" {
		t.Errorf("operationId = %q", got)
	}
}

func TestConfigGetters(t *testing.T) {
	params := mustParse(t, fullParams)

	if got := params.ConfigString("security_protocol"); got != "SASL_SSL" {
		t.Errorf("ConfigString(security_protocol) = %q", got)
	}
	if got := params.ConfigString("missing"); got != "" {
		t.Errorf("ConfigString(missing) = %q", got)
	}
	if !params.ConfigBool("tls_insecure_skip_verify") {
		t.Error("ConfigBool(tls_insecure_skip_verify) = false, want true")
	}
	if params.ConfigBool("allow_delete") {
		t.Error("ConfigBool(allow_delete) = true, want false")
	}

	// textarea 换行 + 逗号混拆，空项丢弃。
	got := params.ConfigStringSlice("bootstrap_servers")
	want := []string{"k1:9092", "k2:9092", "k3:9092"}
	if len(got) != len(want) {
		t.Fatalf("ConfigStringSlice(bootstrap_servers) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ConfigStringSlice()[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// 数组形式（[]any）同样支持。
	arrayParams := mustParse(t, `{"connection":{"external_config":{"bootstrap_servers":["a:9092"," b:9092 ",""]}}}`)
	arrayGot := arrayParams.ConfigStringSlice("bootstrap_servers")
	if len(arrayGot) != 2 || arrayGot[0] != "a:9092" || arrayGot[1] != "b:9092" {
		t.Errorf("ConfigStringSlice(array) = %v", arrayGot)
	}
}

func TestSecretString(t *testing.T) {
	params := mustParse(t, fullParams)
	if got := params.SecretString("sasl_password"); got != "s3cret" {
		t.Errorf("SecretString(sasl_password) = %q", got)
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
