package mcp

// appbridge_test.go：DBX 本地桥客户端 + stdio 桥接兜底（S-BRIDGE-*，
// ldap appbridge_test.go 同表移植）。全部离线：桥存在路径用 httptest 起
// 本地 mock 桥（宿主 /call-plugin-tool 契约形状），桥未发布路径用空
// app-data 目录断言 fail-closed。

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// writeBridgePortFile 在 dir 写 mcp-bridge-port（宿主 write_port_file 同款）。
func writeBridgePortFile(t *testing.T, dir string, port int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, bridgePortFileName), []byte(strconv.Itoa(port)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestBridgePortParsesTrimmedDecimalPorts 端口发现：十进制 + 空白容忍。
func TestBridgePortParsesTrimmedDecimalPorts(t *testing.T) {
	dir := t.TempDir()
	writeBridgePortFile(t, dir, 49152)
	if port, ok := bridgePort(dir); !ok || port != 49152 {
		t.Fatalf("trimmed decimal port: %d %v", port, ok)
	}
	if err := os.WriteFile(filepath.Join(dir, bridgePortFileName), []byte("  54321  "), 0o600); err != nil {
		t.Fatal(err)
	}
	if port, ok := bridgePort(dir); !ok || port != 54321 {
		t.Fatalf("whitespace-tolerant port: %d %v", port, ok)
	}
}

// TestBridgePortRejectsGarbageAndMissingFiles 垃圾/缺失文件一律不可用，
// 绝不猜端口（端口文件比被杀的应用活得久）。
func TestBridgePortRejectsGarbageAndMissingFiles(t *testing.T) {
	dir := t.TempDir()
	if _, ok := bridgePort(dir); ok {
		t.Fatal("missing file must be unusable")
	}
	path := filepath.Join(dir, bridgePortFileName)
	for _, garbage := range []string{"not-a-port", "", "99999", "0", "49152.5"} {
		if err := os.WriteFile(path, []byte(garbage), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok := bridgePort(dir); ok {
			t.Fatalf("garbage %q must be unusable", garbage)
		}
	}
}

// TestBridgeRequestBodyCarriesEveryContractField /call-plugin-tool 契约
// 五字段（snake_case，plugin_id=io.dbx.kafka），nil arguments 折算空对象。
func TestBridgeRequestBodyCarriesEveryContractField(t *testing.T) {
	body, err := bridgeRequestBody("conn-1", "kafka_messages_digest",
		map[string]any{"topic": "orders"}, 300000)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"plugin_id":     bridgePluginID,
		"connection_id": "conn-1",
		"tool":          "kafka_messages_digest",
		"timeout_ms":    int64(300000),
	} {
		got, ok := body[key]
		if !ok || got != want {
			t.Fatalf("field %q = %#v, want %#v", key, got, want)
		}
	}
	if body["arguments"].(map[string]any)["topic"] != "orders" {
		t.Fatalf("arguments mismatch: %#v", body["arguments"])
	}
	empty, err := bridgeRequestBody("c", "t", nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := empty["arguments"].(map[string]any); !ok {
		t.Fatalf("nil arguments must become an empty object: %#v", empty)
	}
}

// TestEnsureBridgeUnreachableFailClosed 桥未发布（空 app-data、launch 是
// no-op）：wait 预算用尽后返回带 "DBX app bridge" 前缀的可行动错误。
func TestEnsureBridgeUnreachableFailClosed(t *testing.T) {
	t.Setenv("DBX_APP_DATA_DIR", t.TempDir())
	t.Setenv("DBX_APP_LAUNCH_CMD", ":")
	started := time.Now()
	_, err := ensureBridge(120 * time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "DBX app bridge unreachable") {
		t.Fatalf("fail-closed error: %v", err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("ensure must respect the wait budget, took %s", time.Since(started))
	}
}

// TestEnsureBridgePicksUpPublishedPort 端口文件 + TCP 探测：launch 后端口
// 就绪即返回（不跑满 wait）。
func TestEnsureBridgePicksUpPublishedPort(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{}"))
	}))
	defer mock.Close()
	dir := t.TempDir()
	t.Setenv("DBX_APP_DATA_DIR", dir)
	t.Setenv("DBX_APP_LAUNCH_CMD", ":")
	writeBridgePortFile(t, dir, mockPort(t, mock))
	port, err := ensureBridge(50 * time.Millisecond)
	if err != nil || port != mockPort(t, mock) {
		t.Fatalf("published port pickup: %d %v", port, err)
	}
}

// TestCallPluginToolForwardsContractAndEnvelope mock 桥转发：请求形状
// （路径 + 五字段 body）、200 envelope 逐字、非 200 错误、非法 JSON 错误。
func TestCallPluginToolForwardsContractAndEnvelope(t *testing.T) {
	var seenPath string
	var seenBody map[string]any
	envelope := map[string]any{
		"content": []any{map[string]any{"type": "text", "text": `{"matched":3}`}},
		"isError": false,
	}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &seenBody); err != nil {
			t.Errorf("bridge body not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(envelope)
	}))
	defer mock.Close()
	dir := t.TempDir()
	t.Setenv("DBX_APP_DATA_DIR", dir)
	writeBridgePortFile(t, dir, mockPort(t, mock))

	got, err := callPluginTool(mockPort(t, mock), "saved-1", "kafka_messages_digest",
		map[string]any{"topic": "orders"}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if seenPath != "/call-plugin-tool" {
		t.Fatalf("bridge path: %s", seenPath)
	}
	if seenBody["plugin_id"] != bridgePluginID || seenBody["connection_id"] != "saved-1" ||
		seenBody["tool"] != "kafka_messages_digest" || seenBody["timeout_ms"] != float64(2000) {
		t.Fatalf("bridge contract body: %#v", seenBody)
	}
	if args := seenBody["arguments"].(map[string]any); args["topic"] != "orders" {
		t.Fatalf("bridge arguments: %#v", args)
	}
	// 200 envelope 逐字（content/isError 形状不被再包一层）。
	if _, hasContent := got["content"]; !hasContent {
		t.Fatalf("envelope must pass through verbatim: %#v", got)
	}

	// 非 200：错误带 "DBX app bridge returned HTTP" 前缀与宿主错误体。
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Connection with id 'x' not found"}`))
	}))
	defer bad.Close()
	if _, err := callPluginTool(mockPort(t, bad), "x", "kafka_messages_digest", nil, time.Second); err == nil ||
		!strings.Contains(err.Error(), "DBX app bridge returned HTTP 404") {
		t.Fatalf("non-200 must fail closed: %v", err)
	}

	// 非法 JSON：明确报错。
	junk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer junk.Close()
	if _, err := callPluginTool(mockPort(t, junk), "x", "kafka_messages_digest", nil, time.Second); err == nil ||
		!strings.Contains(err.Error(), "DBX app bridge returned invalid JSON") {
		t.Fatalf("invalid JSON must fail closed: %v", err)
	}
}

// TestStdioBridgeFallbackForwardsUnknownConnection stdio 桥接兜底主路径：
// 未池化 connectionId 的调用被转发到 mock 桥，应用侧 envelope 成为 stdio
// 响应；本地池/连接表不被污染。
func TestStdioBridgeFallbackForwardsUnknownConnection(t *testing.T) {
	calls := 0
	envelope := map[string]any{
		"content": []any{map[string]any{"type": "text", "text": `{"matched":7,"cursorId":"cur-bridge"}`}},
		"isError": false,
	}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if body["plugin_id"] != bridgePluginID || body["tool"] != "kafka_messages_digest" {
			t.Errorf("unexpected forward body: %#v", body)
		}
		_ = json.NewEncoder(w).Encode(envelope)
	}))
	defer mock.Close()
	dir := t.TempDir()
	t.Setenv("DBX_APP_DATA_DIR", dir)
	writeBridgePortFile(t, dir, mockPort(t, mock))

	server := newTestStdioServer()
	result := server.callTool("kafka_messages_digest", map[string]any{
		"connectionId": "saved-orders", "topic": "orders", "timeoutSecs": "30",
	})
	if calls != 1 {
		t.Fatalf("bridge must be called exactly once, got %d", calls)
	}
	decoded := decodeResponse(t, result)
	if decoded["isError"] == true {
		t.Fatalf("forwarded call must succeed: %#v", result)
	}
	text := decoded["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, `"matched":7`) {
		t.Fatalf("app-side payload must pass through: %s", text)
	}
	// 本地连接池不被污染（转发调用没有池化注册）。
	if len(server.hash) != 0 || len(server.ids) != 0 {
		t.Fatalf("forward must not pollute the inline pool: %#v", server.hash)
	}
}

// TestStdioBridgeFallbackWrapsNonEnvelope 非 envelope 形状（防御性）按成功
// content 包装。
func TestStdioBridgeFallbackWrapsNonEnvelope(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"unexpected":"shape"}`))
	}))
	defer mock.Close()
	dir := t.TempDir()
	t.Setenv("DBX_APP_DATA_DIR", dir)
	writeBridgePortFile(t, dir, mockPort(t, mock))

	server := newTestStdioServer()
	result := server.callTool("kafka_messages_produce", map[string]any{
		"connectionId": "saved-1", "topic": "orders", "key": "k", "value": "v",
	})
	decoded := decodeResponse(t, result)
	if decoded["isError"] != false {
		t.Fatalf("non-envelope payload must wrap as success: %#v", result)
	}
	text := decoded["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, `"unexpected"`) {
		t.Fatalf("payload must be carried: %s", text)
	}
}

// TestStdioBridgeFallbackDisabledForOtherTools 桥兜底只作用于连接类工具：
// cursor_next 等会话类工具不走桥（unknown cursorId 是本地错误）。
func TestStdioBridgeFallbackDisabledForOtherTools(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DBX_APP_DATA_DIR", dir)
	writeBridgePortFile(t, dir, 1) // 端口在但没有监听者：若误走桥会探测失败

	server := newTestStdioServer()
	result := server.callTool("kafka_cursor_next", map[string]any{"cursorId": "cur-nope"})
	text := result["content"].([]map[string]any)[0]["text"].(string)
	if result["isError"] != true || !strings.Contains(text, "unknown cursorId") {
		t.Fatalf("session tools must stay local: %v", result)
	}
}

func mockPort(t *testing.T, server *httptest.Server) int {
	t.Helper()
	port := server.URL
	idx := strings.LastIndex(port, ":")
	if idx < 0 {
		t.Fatalf("cannot parse port from %s", server.URL)
	}
	value := 0
	for _, ch := range port[idx+1:] {
		if ch < '0' || ch > '9' {
			t.Fatalf("cannot parse port from %s", server.URL)
		}
		value = value*10 + int(ch-'0')
	}
	return value
}
