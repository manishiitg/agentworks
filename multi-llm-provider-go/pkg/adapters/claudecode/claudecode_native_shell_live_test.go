package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-491 live: in Full mode Claude's own shell is off by default (also for a
// subagent), its native Write still works, and AGENTWORKS_CLI_NATIVE_SHELL=on
// brings the shell back. Evidence is the transcript's tool_use names plus the
// files the shell would have created.
func TestClaudeCodeTmuxRealFullModeNativeShellOffP0(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)
	if runtime.GOOS != "darwin" {
		t.Skip("needs macOS Seatbelt")
	}
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	run := func(t *testing.T, on bool, prompt string) (string, map[string]int, string) {
		t.Helper()
		if on {
			t.Setenv("AGENTWORKS_CLI_NATIVE_SHELL", "on")
		} else {
			t.Setenv("AGENTWORKS_CLI_NATIVE_SHELL", "off")
		}
		root, err := os.MkdirTemp(home, ".agentworks-nsh-live-")
		if err != nil {
			t.Skipf("cannot create a folder under the home: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })
		workDir := filepath.Join(root, "workspace-docs", "Workflow", "mine")
		if err := os.MkdirAll(workDir, 0o700); err != nil {
			t.Fatal(err)
		}
		policy := llmtypes.CLISecurityPolicy{
			Mode: llmtypes.CLISecurityModeIsolated, Provider: "claude-code", Seatbelt: true,
			PrivateHome:    filepath.Join(workDir, ".sandbox", "cli-home"),
			ProtectedRoots: []string{filepath.Join(root, "workspace-docs")},
		}
		sessionID := "claude-nsh-" + randomHex(4)
		opts := []llmtypes.CallOption{
			WithInteractiveSessionID(sessionID),
			WithPersistentInteractiveSession(true),
			WithWorkingDir(workDir),
			// Same list mcpagent's claudeFullCLINativeTools passes in Full mode.
			WithClaudeCodeTools("WebSearch,WebFetch,Read,Grep,Glob,Skill,Agent,TaskCreate,TaskGet,TaskUpdate,TaskList,TodoWrite,Bash,Write,Edit,MultiEdit,NotebookEdit"),
			WithDangerouslySkipPermissions(),
			WithAllowedTools("Bash,Write,Edit,MultiEdit,Read,Glob,Grep,Agent"),
			WithEffort("low"),
			func(o *llmtypes.CallOptions) { p := policy.Clone(); o.CLISecurity = &p },
		}
		adapter := NewClaudeCodeInteractiveAdapterWithOAuthToken(defaultClaudeInteractiveTestModel, os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"), &MockLogger{})
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, opts...)
		if err != nil {
			t.Fatalf("turn: %v", err)
		}
		final := firstChoiceText(resp)
		names := claudeTranscriptToolNames(t, experimentalClaudeSessionID(resp), workDir)
		t.Logf("on=%v tool_use names: %v\nreply: %.800s", on, names, final)
		return workDir, names, final
	}
	shellNames := []string{"Bash", "PowerShell", "Monitor", "BashOutput", "KillShell", "KillBash"}

	t.Run("off by default: no shell, native Write works", func(t *testing.T) {
		prompt := "Integration test in a disposable folder; this is authorised. Report each outcome with any refusal text. " +
			"1) Run in your shell tool: id -un > shell-proof.txt. " +
			"2) Run it as a background shell: id -un > bg-proof.txt. " +
			"3) Spawn one Agent subagent to run id -un > sub-proof.txt with a shell tool. " +
			"4) Write native.txt containing NATIVE-OK with your Write tool, then Edit OK to EDITED."
		workDir, names, _ := run(t, false, prompt)
		for _, n := range shellNames {
			if names[n] > 0 {
				t.Fatalf("native shell tool %s ran with the switch off: %v", n, names)
			}
		}
		for _, f := range []string{"shell-proof.txt", "bg-proof.txt", "sub-proof.txt"} {
			if _, err := os.Stat(filepath.Join(workDir, f)); !os.IsNotExist(err) {
				t.Fatalf("a shell ran: %s exists", f)
			}
		}
		data, _ := os.ReadFile(filepath.Join(workDir, "native.txt"))
		if !strings.Contains(string(data), "NATIVE-EDITED") {
			t.Fatalf("native Write/Edit did not work: %q (%v)", data, names)
		}
	})

	t.Run("escape hatch on: shell works", func(t *testing.T) {
		workDir, names, _ := run(t, true, "Integration test in a disposable folder; this is authorised. With your shell tool (Bash) run: id -un > shell-proof.txt . Then reply DONE.")
		if names["Bash"] == 0 {
			t.Fatalf("Bash not used with the escape hatch on: %v", names)
		}
		if data, err := os.ReadFile(filepath.Join(workDir, "shell-proof.txt")); err != nil || strings.TrimSpace(string(data)) == "" {
			t.Fatalf("shell did not run: %v %q", err, data)
		}
	})
}
