package musecli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/nativeshell"
)

func runFullPolicyHook(t *testing.T, path, payload string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "node", path)
	cmd.Stdin = strings.NewReader(payload)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hook exited with an error: %v", err)
	}
	return string(out)
}

// Full CLI mode passes no allowlist and keeps --yolo, but with the bridge mounted it gets a default-refuse
// policy: Muse's own cron, goals, memory, messaging of other sessions and reminders (and any tool a later
// Muse update adds) are refused; the tools chats use stay allowed, and MCP tools are never blocked.
func TestFullModePolicyRefusesUnlistedNativeTools(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	path, err := museWriteToolPolicyHook(museFullNativeTools, museFullPolicyReason)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"get_goal", "create_goal", "update_goal", "report_progress",
		"read_memory", "add_memory", "edit_memory",
		"list_peer_sessions", "send_session_message",
		"cron_create", "cron_delete", "cron_list", "snooze_reminder", "workflow",
		"some_tool_a_later_update_adds",
	} {
		out := runFullPolicyHook(t, path, `{"tool_name":"`+name+`","tool_input":{}}`)
		var decision struct {
			Hook struct {
				Decision string `json:"permissionDecision"`
				Reason   string `json:"permissionDecisionReason"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(out), &decision); err != nil || decision.Hook.Decision != "deny" {
			t.Errorf("%s was not refused: %q", name, out)
			continue
		}
		if !strings.Contains(decision.Hook.Reason, "handled by the platform") {
			t.Errorf("%s: reason %q is not the Full-mode reason", name, decision.Hook.Reason)
		}
	}
	for _, name := range append(append([]string{}, museFullNativeTools...), "tool_search", "submit_reminder_decision", "mcp__api_bridge__execute_shell_command", "mcp__other__thing") {
		if out := runFullPolicyHook(t, path, `{"tool_name":"`+name+`","tool_input":{}}`); out != "" {
			t.Errorf("%s was refused: %s", name, out)
		}
	}
}

// The policy is installed from the settings step, not from the launch flags: no allowlist plus a bridge gives
// the hook, no allowlist and no bridge gives nothing, and the bridge-only allowlist keeps its own reason.
func TestApplyMCPConfigInstallsFullModePolicyOnlyWithABridge(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	bridge := `{"mcpServers":{"api-bridge":{"command":"/bin/true","env":{"MCP_API_URL":"http://127.0.0.1:1"}}}}`
	hookFiles := func(settings string) []string {
		var parsed struct {
			Hooks struct {
				PreToolUse []struct {
					Hooks []struct {
						Command string `json:"command"`
					} `json:"hooks"`
				} `json:"PreToolUse"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
			t.Fatal(err)
		}
		var files []string
		for _, entry := range parsed.Hooks.PreToolUse {
			for _, h := range entry.Hooks {
				files = append(files, h.Command)
			}
		}
		return files
	}
	read := func(path string) string {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}

	path := redirectMuseConfigHome(t)
	restore, err := museApplyMCPConfigAtPath(path, bridge, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := read(path)
	policy := 0
	for _, f := range hookFiles(got) {
		if strings.Contains(f, "native-tool-policy-") {
			policy++
		}
	}
	if policy != 1 {
		t.Errorf("Full mode with a bridge installed %d policy hooks, want 1: %s", policy, got)
	}
	if !strings.Contains(got, `"workflow_trigger_mode": "off"`) && !strings.Contains(got, `"workflow_trigger_mode":"off"`) {
		t.Errorf("Full mode policy did not turn workflow triggers off: %s", got)
	}
	restore()

	path2 := redirectMuseConfigHome(t)
	restore2, err := museApplyMCPConfigAtPath(path2, `{"mcpServers":{"other":{"command":"/bin/true"}}}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restore2()
	if strings.Contains(read(path2), "native-tool-policy-") {
		t.Errorf("a policy hook was installed without a bridge: %s", read(path2))
	}
}

// PLAT-491: Full mode drops Muse's shell tools unless the escape hatch is on; the hook names the bridge shell.
func TestFullModeShellToolsFollowTheNativeShellSwitch(t *testing.T) {
	t.Setenv(nativeshell.EnvVar, "off")
	for _, name := range museShellTools {
		if slices.Contains(museFullAllowedTools(), name) {
			t.Errorf("%s allowed with the switch off", name)
		}
	}
	if !slices.Contains(museFullAllowedTools(), "edit_file") {
		t.Error("edit_file was dropped")
	}
	t.Setenv(nativeshell.EnvVar, "on")
	if !slices.Equal(museFullAllowedTools(), museFullNativeTools) {
		t.Error("switch on must keep the full list")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	t.Setenv(nativeshell.EnvVar, "off")
	path, err := museWriteToolPolicyHookShell(museFullAllowedTools(), museFullPolicyReason, museShellOffReason)
	if err != nil {
		t.Fatal(err)
	}
	if out := runFullPolicyHook(t, path, `{"tool_name":"bash","tool_input":{}}`); !strings.Contains(out, "execute_shell_command") {
		t.Errorf("bash refusal does not point at the bridge shell: %q", out)
	}
	if out := runFullPolicyHook(t, path, `{"tool_name":"cron_create","tool_input":{}}`); !strings.Contains(out, "handled by the platform") {
		t.Errorf("other refusals changed: %q", out)
	}
}
