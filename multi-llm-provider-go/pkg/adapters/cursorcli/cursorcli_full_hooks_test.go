package cursorcli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Full CLI mode: Cursor's own shell, reads and edits are not denied, its shell
// is approved by the hook (never --force), and subagents, cloud/background
// agents and computer use stay denied. The generated hooks.json must be valid.
//
// The built-in shell is OFF by default in Full mode (PLAT-491): the shell tools
// are denied by the preToolUse hook and the shell hook, and Shell(*) is not
// pre-approved. AGENTWORKS_CLI_NATIVE_SHELL=on restores the old behaviour.
func TestCursorFullNativeHooks(t *testing.T) {
	t.Run("shell off by default", func(t *testing.T) {
		t.Setenv("AGENTWORKS_CLI_NATIVE_SHELL", "off")
		cursorDir := filepath.Join(t.TempDir(), ".cursor")
		cleanup, err := writeCursorDenyBuiltinHooks(cursorDir, true)
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		raw, _ := os.ReadFile(filepath.Join(cursorDir, "hooks.json"))
		if !strings.Contains(string(raw), `"command": "./.cursor/hooks/mlp-deny-builtin.sh", "failClosed": true}]`) || strings.Contains(string(raw), `"beforeShellExecution": [{"command": "./.cursor/hooks/mlp-allow-shell.sh"`) {
			t.Errorf("shell hook must deny:\n%s", raw)
		}
		m := cursorDeniedToolMatcher(true)
		if !strings.Contains(m, "Shell") || !strings.Contains(m, "WriteShellStdin") || strings.Contains(m, "Edit") || strings.Contains(m, "Read") {
			t.Errorf("matcher %q", m)
		}
		for _, p := range cursorFullNativeAllow() {
			if strings.HasPrefix(p, "Shell") {
				t.Errorf("Shell must not be pre-approved: %v", p)
			}
		}
		prompt := cursorBridgeOnlySystemPrompt("", true, true)
		if !strings.Contains(prompt, "Native Shell and the Delete tool are disabled") || strings.Contains(prompt, "Your own Shell, Read") || strings.Contains(prompt, "delete files with the shell, e.g. rm") {
			t.Errorf("prompt contradicts the shell-denying hooks: %s", prompt)
		}
		if !strings.Contains(prompt, "MCP shell for commands and file deletion") || !strings.Contains(prompt, "Read, List, Glob") {
			t.Errorf("prompt lost the permitted command route or native file tools: %s", prompt)
		}
	})
	t.Setenv("AGENTWORKS_CLI_NATIVE_SHELL", "on")
	cursorDir := filepath.Join(t.TempDir(), ".cursor")
	cleanup, err := writeCursorDenyBuiltinHooks(cursorDir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	raw, err := os.ReadFile(filepath.Join(cursorDir, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var hooks struct {
		Hooks map[string][]struct {
			Command string `json:"command"`
			Matcher string `json:"matcher"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &hooks); err != nil {
		t.Fatalf("hooks.json is not valid JSON: %v\n%s", err, raw)
	}
	if _, ok := hooks.Hooks["beforeReadFile"]; ok {
		t.Error("full mode must not deny native reads")
	}
	shell := hooks.Hooks["beforeShellExecution"]
	if len(shell) != 1 || shell[0].Command != "./.cursor/hooks/mlp-allow-shell.sh" {
		t.Errorf("shell must be approved by the allow script, got %+v", shell)
	}
	out, err := exec.CommandContext(context.Background(), "/bin/bash", filepath.Join(cursorDir, "hooks", "mlp-allow-shell.sh")).Output()
	if err != nil || strings.TrimSpace(string(out)) != `{"permission":"allow"}` {
		t.Errorf("allow script output %q, %v", out, err)
	}
	denied := map[string]bool{}
	for _, name := range strings.Split(hooks.Hooks["preToolUse"][0].Matcher, "|") {
		denied[name] = true
	}
	for _, allowed := range []string{"Shell", "Read", "Edit", "Write", "Grep"} {
		if denied[allowed] {
			t.Errorf("%s is denied in full mode", allowed)
		}
	}
	for _, blocked := range []string{"Delete", "CloudAgent", "BackgroundAgent", "Task", "Subagent", "ComputerUse"} {
		if !denied[blocked] {
			t.Errorf("%s must stay denied in full mode", blocked)
		}
	}
	if !strings.Contains(cursorBridgeOnlySystemPrompt("", true, true), "Shell, Read") {
		t.Error("the full-mode prompt must tell Cursor its own tools are on")
	}
}
