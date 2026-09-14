package mcp

// tools_schema_test.go：tools/list 输出的 schema 形状红线（严格 MCP 宿主
// 兼容轮）。Go nil 切片会序列化成 "required":null，zcode 的 tools/list
// zod 校验直接拒收整个服务器（2026-09-14 真机接入实测：stdio 连接失败，
// 错误 "Invalid result for tools/list: expected array, received null"）。

import (
	"encoding/json"
	"strings"
	"testing"

	"io.dbx.kafka.plugin/internal/kafkaconn"
)

// 全量与只读两种清单都不得出现 "required":null；required 空时键必须缺席。
func TestToolDefinitionsNeverMarshalRequiredNull(t *testing.T) {
	server := NewServer(kafkaconn.NewService(), nil)
	for _, scope := range []string{"", "no-such-connection"} {
		raw, err := json.Marshal(server.Tools(scope))
		if err != nil {
			t.Fatalf("marshal tools(%q): %v", scope, err)
		}
		if strings.Contains(string(raw), `"required":null`) {
			t.Fatalf("tools(%q) contains required:null (strict MCP hosts reject the server): %s", scope, raw)
		}
	}
}
