package agycli

import (
	"encoding/json"
	"testing"
)

// Under Auto-review only the chat's own AgentWorks MCP tools skip the person: agy names the server in
// rawInput.ServerName (or, before 1.5.8, only in the title). Shell commands and other servers' tools are asked.
func TestAgyOwnMCPToolRequestIsOnlyAgentWorksTools(t *testing.T) {
	for _, c := range []struct {
		title, raw string
		want       bool
	}{
		{"Run mcp_agentworks-api-42-ab12_list_files", `{"ServerName":"agentworks-api-42-ab12","ToolName":"list_files"}`, true},
		{"Run mcp_agentworks-api-42-ab12_list_files?", `{}`, true},
		{"Run mcp_github_create_issue", `{"ServerName":"github","ToolName":"create_issue"}`, false},
		{"Run mcp_agentworks-api-42-ab12_list_files", `{"ServerName":"github"}`, false},
		{"Run mcp_agentworks-api-42-ab12_list_files", `{"CommandLine":"rm -rf build"}`, false},
		{"Run rm -rf build", `{}`, false},
	} {
		if got := agyOwnMCPToolRequest(c.title, json.RawMessage(c.raw)); got != c.want {
			t.Errorf("agyOwnMCPToolRequest(%q, %s) = %t, want %t", c.title, c.raw, got, c.want)
		}
	}
}
