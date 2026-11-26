package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// TestMCPBridgePreservesShellOutputOnToolFailureE2E reproduces Sales
// Outreach PUL-AAC278EF: a chained execute_shell_command script whose
// trailing command returns nonzero must still surface the actual
// stdout/stderr captured from the earlier successful commands in the
// chain, not a bare generic error that forces a blind retry. Exercises the
// complete production transport -- a real mcpbridge subprocess over stdio,
// forwarding to a real HTTP server -- with no helper called directly.
func TestMCPBridgePreservesShellOutputOnToolFailureE2E(t *testing.T) {
	const (
		token     = "failure-result-e2e-token" // #nosec G101 -- test-only credential for the local httptest server.
		sessionID = "failure-result-e2e-session"
	)
	// Shaped exactly like the finding's own reproduction: a chained script
	// whose early commands succeeded and produced real stdout before the
	// trailing command returned nonzero.
	shellResult, err := json.Marshal(map[string]any{
		"stdout":            "found 3 matching files\nfile-a.txt\nfile-b.txt\nfile-c.txt\n",
		"stderr":            "",
		"exit_code":         1,
		"execution_time_ms": 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tools/custom/execute_shell_command" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"error":   "tool execution failed: exit_code=1",
			"result":  string(shellResult),
		})
	}))
	defer api.Close()

	bridgeBinary := buildLargeOutputE2EBridge(t)
	toolsJSON, err := json.Marshal([]map[string]any{{
		"name":        "execute_shell_command",
		"description": "Run a shell command.",
		"type":        "custom",
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"command": map[string]any{"type": "string"}},
			"required":   []string{"command"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	bridgeClient, err := client.NewStdioMCPClient(bridgeBinary, append(os.Environ(),
		"MCP_API_URL="+api.URL,
		"MCP_API_TOKEN="+token,
		"MCP_SESSION_ID="+sessionID,
		"MCP_TOOLS_FILE="+writeBridgeToolsFileForTest(t, toolsJSON),
	))
	if err != nil {
		t.Fatalf("start real mcpbridge: %v", err)
	}
	defer bridgeClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := bridgeClient.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "failure-result-e2e", Version: "1"},
	}}); err != nil {
		t.Fatalf("initialize real mcpbridge: %v", err)
	}

	request := mcp.CallToolRequest{}
	request.Params.Name = "execute_shell_command"
	request.Params.Arguments = map[string]any{"command": "find . -name '*.txt' | some-failing-filter"}
	result, err := bridgeClient.CallTool(ctx, request)
	if err != nil {
		t.Fatalf("real mcpbridge tool call: %v", err)
	}
	// Deliberately regular text, not an MCP IsError: some CLI providers
	// (Claude Code among them) show only a generic "MCP tool reported an
	// error" and hide the content when IsError=true, so the bridge
	// intentionally keeps this as plain text the LLM always sees in full.
	if result.IsError {
		t.Fatalf("expected regular text (not MCP IsError) so the LLM always sees the content, got: %#v", result.Content)
	}

	text := failureResultE2EText(t, result)
	if !strings.Contains(text, "ERROR:") || !strings.Contains(text, "exit_code=1") {
		t.Fatalf("result lost the error signal entirely: %q", text)
	}
	if !strings.Contains(text, "found 3 matching files") || !strings.Contains(text, "file-b.txt") {
		t.Fatalf("result discarded the actual captured stdout from the chain, forcing a blind retry: %q", text)
	}
}

func failureResultE2EText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	for _, content := range result.Content {
		if text, ok := content.(mcp.TextContent); ok {
			return text.Text
		}
	}
	t.Fatalf("no text content in result: %#v", result.Content)
	return ""
}
