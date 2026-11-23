package codexcli

import (
	"encoding/json"
	"testing"
)

func TestCodexStructuredToolItemSemanticFields(t *testing.T) {
	tests := []struct {
		name       string
		item       *codexExecItem
		wantName   string
		wantArgs   string
		wantResult string
	}{
		{
			name: "mcp tool",
			item: &codexExecItem{
				Type:      "mcp_tool_call",
				Server:    "api-bridge",
				Tool:      "echo_contract",
				Arguments: json.RawMessage(`{ "token": "abc" }`),
				Result:    json.RawMessage(`{ "content": "ok" }`),
			},
			wantName:   "echo_contract",
			wantArgs:   `{"token":"abc"}`,
			wantResult: `{"content":"ok"}`,
		},
		{
			name: "native command",
			item: &codexExecItem{
				Type:             "command_execution",
				Command:          "pwd",
				AggregatedOutput: "/work\n",
			},
			wantName:   "exec_command",
			wantArgs:   `{"command":"pwd"}`,
			wantResult: "/work\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexToolItemName(tt.item); got != tt.wantName {
				t.Fatalf("codexToolItemName() = %q, want %q", got, tt.wantName)
			}
			if got := codexToolItemArgs(tt.item); got != tt.wantArgs {
				t.Fatalf("codexToolItemArgs() = %q, want %q", got, tt.wantArgs)
			}
			if got := codexToolItemResult(tt.item); got != tt.wantResult {
				t.Fatalf("codexToolItemResult() = %q, want %q", got, tt.wantResult)
			}
		})
	}
}
