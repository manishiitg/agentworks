package mcpagent

import (
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// piBridgeOnlyFromOptions reads back the value the adapter will actually see,
// so this asserts the emitted option rather than the branch that produced it.
func piBridgeOnlyFromOptions(t *testing.T, opts []llmtypes.CallOption) bool {
	t.Helper()
	resolved := &llmtypes.CallOptions{}
	for _, opt := range opts {
		opt(resolved)
	}
	if resolved.Metadata == nil || resolved.Metadata.Custom == nil {
		t.Fatal("no metadata was emitted, so bridge-only mode was never configured")
	}
	value, ok := resolved.Metadata.Custom["pi_bridge_only_tools"].(bool)
	if !ok {
		t.Fatalf("pi_bridge_only_tools missing or not a bool: %#v", resolved.Metadata.Custom)
	}
	return value
}

// Confined Full CLI gives Pi its own tools (owner, 2026-10-20: native shell on everywhere); other modes stay bridge-only.
func TestPiBridgeOnlyToolsFollowsAgentToolsMode(t *testing.T) {
	for _, tc := range []struct {
		name           string
		toolsMode      string
		wantBridgeOnly bool
	}{
		{"confined full uses Pi's own tools", codingAgentToolsFull, false},
		{"default is bridge-only", "", true},
		{"explicit mcp_only is bridge-only", "mcp_only", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newPiToolsModeTestAgent(t, tc.toolsMode)
			opts, err := a.appendPiCLIIntegrationOptions(nil)
			if err != nil {
				t.Fatalf("appendPiCLIIntegrationOptions: %v", err)
			}
			if got := piBridgeOnlyFromOptions(t, opts); got != tc.wantBridgeOnly {
				t.Fatalf("pi_bridge_only_tools = %v, want %v for tools mode %q", got, tc.wantBridgeOnly, tc.toolsMode)
			}
		})
	}
}

func newPiToolsModeTestAgent(t *testing.T, toolsMode string) *Agent {
	t.Helper()
	t.Setenv("MCP_BRIDGE_BINARY", "/usr/local/bin/mcpbridge")
	t.Setenv("MCP_API_URL", "http://localhost:8080")
	t.Setenv("MCP_API_TOKEN", "test-token-123")
	return &Agent{logger: loggerv2.NewDefault(), codingAgentToolsMode: toolsMode, cliSecurityPolicy: confinedTestPolicy()}
}
