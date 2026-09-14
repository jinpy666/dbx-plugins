package main

// main_test.go：入口互斥的纯逻辑（--mcp 标志识别；true 时 main 提前进入
// stdio MCP 服务器分支，不装配 SDK Server/Emitter/流式会话）。

import "testing"

func TestMcpStdioRequested(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{args: []string{"--mcp"}, want: true},
		{args: []string{"--mcp", "--other"}, want: true},
		{args: []string{"--other", "--mcp"}, want: true},
		{args: []string{"--mcp=false"}, want: false},
		{args: []string{"-mcp"}, want: false},
		{args: []string{"--verbose"}, want: false},
		{args: nil, want: false},
	}
	for _, testCase := range cases {
		if got := mcpStdioRequested(testCase.args); got != testCase.want {
			t.Errorf("mcpStdioRequested(%v) = %v, want %v", testCase.args, got, testCase.want)
		}
	}
}
