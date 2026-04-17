package mcp

import (
	"testing"
)

func TestFormatToolParams_NoSchema(t *testing.T) {
	tool := ToolInfo{ServerName: "srv", ToolName: "echo"}
	got := FormatToolParams(tool)
	if got != "content: <json args>" {
		t.Errorf("expected default param hint, got %q", got)
	}
}

func TestFormatToolParams_WithSchema(t *testing.T) {
	tool := ToolInfo{
		ServerName: "srv",
		ToolName:   "echo",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"message": map[string]any{"type": "string"}},
		},
	}
	got := FormatToolParams(tool)
	if got == "content: <json args>" {
		t.Error("expected non-default params with schema set")
	}
}

func TestToolInfo_Kind(t *testing.T) {
	tool := ToolInfo{ServerName: "my-server", ToolName: "do-thing"}
	want := "mcp__my-server__do-thing"
	if got := tool.Kind(); got != want {
		t.Errorf("Kind() = %q, want %q", got, want)
	}
}

func TestIsMCPTool(t *testing.T) {
	cases := []struct {
		kind string
		want bool
	}{
		{"mcp__srv__tool", true},
		{"read_file", false},
		{"mcp__", true},
		{"", false},
	}
	for _, tc := range cases {
		got := IsMCPTool(tc.kind)
		if got != tc.want {
			t.Errorf("IsMCPTool(%q) = %v, want %v", tc.kind, got, tc.want)
		}
	}
}

func TestExpandEnv_NoVars(t *testing.T) {
	in := map[string]string{"KEY": "value"}
	out := ExpandEnv(in)
	if out["KEY"] != "value" {
		t.Errorf("expected value, got %q", out["KEY"])
	}
}

func TestExpandEnv_WithVar(t *testing.T) {
	t.Setenv("TEST_MCP_VAR", "expanded")
	in := map[string]string{"KEY": "${TEST_MCP_VAR}"}
	out := ExpandEnv(in)
	if out["KEY"] != "expanded" {
		t.Errorf("expected 'expanded', got %q", out["KEY"])
	}
}

func TestExpandEnv_MissingVar(t *testing.T) {
	in := map[string]string{"KEY": "${NONEXISTENT_VAR_XYZ}"}
	out := ExpandEnv(in)
	if out["KEY"] != "${NONEXISTENT_VAR_XYZ}" {
		t.Errorf("expected original placeholder, got %q", out["KEY"])
	}
}
