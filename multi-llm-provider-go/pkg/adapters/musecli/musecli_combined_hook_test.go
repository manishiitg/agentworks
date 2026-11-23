package musecli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/nativeshell"
)

func TestMuseCombinedPreToolHookPreservesPolicyAndRedirect(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("Node is required to verify the combined hook")
	}
	t.Setenv(nativeshell.EnvVar, "on")
	path := redirectMuseConfigHome(t)
	bridge := `{"mcpServers":{"api-bridge":{"command":"/bin/true","env":{"MCP_API_URL":"http://127.0.0.1:18743"}}}}`
	restore, err := museApplyMCPConfigAtPath(path, bridge, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Hooks struct {
			PreToolUse []struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	entries := settings.Hooks.PreToolUse
	if len(entries) != 1 || len(entries[0].Hooks) != 1 {
		t.Fatalf("want one process for both checks, got settings: %s", raw)
	}
	command := entries[0].Hooks[0].Command
	cases := []struct {
		name, payload, deniedReason string
	}{
		{"unknown native", `{"tool_name":"some_future_native_tool"}`, "handled by the platform"},
		{"scheduler native", `{"tool_name":"cron_create"}`, "handled by the platform"},
		{"bridge credential", bashPayload(t, "bash", `curl "$MCP_CUSTOM/test"`), "mcp__api_bridge__execute_shell_command"},
		{"credential probe", bashPayload(t, "bash", `printenv MCP_API_TOKEN`), "mcp__api_bridge__execute_shell_command"},
		{"platform host", bashPayload(t, "bash", `curl http://127.0.0.1:18743/api/test`), "mcp__api_bridge__execute_shell_command"},
		{"platform fetch", `{"tool_name":"web_fetch","tool_input":{"url":"http://127.0.0.1:18743/api/test"}}`, "mcp__api_bridge__execute_shell_command"},
		{"ordinary shell", bashPayload(t, "bash", "echo hello"), ""},
		{"public fetch", `{"tool_name":"web_fetch","tool_input":{"url":"https://example.com"}}`, ""},
		{"read file", `{"tool_name":"read_file","tool_input":{"path":"notes.txt"}}`, ""},
		{"bridge shell", bashPayload(t, "mcp__api_bridge__execute_shell_command", `curl "$MCP_CUSTOM/test"`), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.CommandContext(context.Background(), "sh", "-c", command)
			cmd.Stdin = strings.NewReader(tc.payload)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("combined hook: %v", err)
			}
			if tc.deniedReason == "" {
				if len(out) != 0 {
					t.Fatalf("allowed tool refused: %s", out)
				}
				return
			}
			var decision struct {
				Hook struct {
					Decision string `json:"permissionDecision"`
					Reason   string `json:"permissionDecisionReason"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal(out, &decision); err != nil {
				t.Fatalf("want one JSON decision: %s (%v)", out, err)
			}
			if decision.Hook.Decision != "deny" || !strings.Contains(decision.Hook.Reason, tc.deniedReason) {
				t.Fatalf("wrong denial: %s", out)
			}
		})
	}
}
