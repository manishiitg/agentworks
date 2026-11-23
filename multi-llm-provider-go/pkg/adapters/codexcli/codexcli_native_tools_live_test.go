package codexcli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/nativeshell"
)

// TestCodexCLIRealNativeToolsP0 certifies Codex's "Native agent tools" (Full
// CLI) mode: its native shell and subagents ON (WithNativeTools) in its
// workspace-write sandbox. Native reads and a native write in the working
// directory must work, and the MCP bridge must keep working for everything else.
func TestCodexCLIRealNativeToolsP0(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Setenv(nativeshell.EnvVar, "on") // this test exercises the CLI's own shell
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })
	workDir := untrustedCodexDir(t)
	secret := "CODEX-READ-" + codexRandomHex(4)
	needle := "CODEX-NEEDLE-" + codexRandomHex(4)
	if err := os.WriteFile(filepath.Join(workDir, "witness.txt"), []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, "deep"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "deep", "notes.md"), []byte("x "+needle+" HIT-"+codexRandomHex(3)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	mcpServerPath := writeCodexSlowContractMCPServer(t, filepath.Join(t.TempDir(), "slow-tool-started"))
	mcpCommandOverride, err := codexStringConfigOverride("mcp_servers.api-bridge.command", mcpServerPath)
	if err != nil {
		t.Fatalf("build MCP command override: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	token := "BRIDGE_" + codexRandomHex(4)
	prompt := fmt.Sprintf("Integration test in a disposable directory. Use your own shell: 1) print witness.txt, 2) search the directory recursively for %s and note the HIT token on that line, "+
		"3) create a file with: touch native-write-attempt (report whether it was allowed). Then call the api-bridge slow_contract MCP tool with token %s and delay_ms 100. "+
		"Finally reply on one line: the witness contents, the HIT token, whether the write was allowed, and the MCP result.", needle, token)
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}},
		WithInteractiveSessionID("codex-native-"+codexRandomHex(4)),
		WithPersistentInteractiveSession(true),
		WithProjectDirID(workDir),
		WithSandbox("workspace-write"),
		WithNativeTools(),
		liveConfined(t, "codex-cli", workDir),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
		WithConfigOverrides([]string{mcpCommandOverride}),
	)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	final := strings.TrimSpace(resp.Choices[0].Content)
	t.Logf("final: %s", final)
	if !strings.Contains(final, secret) {
		t.Fatalf("native read failed: final %q lacks %s", final, secret)
	}
	if !strings.Contains(final, "HIT-") {
		t.Fatalf("native search failed: final %q lacks the HIT token", final)
	}
	if !strings.Contains(final, "SLOW_BRIDGE_TOOL_OK_"+token) && !strings.Contains(final, token) {
		t.Fatalf("MCP bridge call missing from final: %q", final)
	}
	if _, err := os.Stat(filepath.Join(workDir, "native-write-attempt")); err != nil {
		t.Fatalf("workspace-write sandbox refused a native write in the working directory: %v", err)
	}
}

// TestCodexCLIRealNativeToolsSubagentP0: in Full CLI mode a Codex subagent can
// read and write in the working directory (it inherits workspace-write).
func TestCodexCLIRealNativeToolsSubagentP0(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Setenv(nativeshell.EnvVar, "on") // this test exercises the subagent's own shell
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })
	workDir := t.TempDir()
	secret := "CODEX-CHILD-" + codexRandomHex(4)
	if err := os.WriteFile(filepath.Join(workDir, "witness.txt"), []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	started := time.Now()
	prompt := "Integration test in a disposable directory. Spawn exactly one subagent (do not do this work yourself). " +
		"Tell it to read witness.txt with its shell and to run: touch child-write-attempt, and to report both outcomes. " +
		"Wait for it, then reply with the witness contents it reported and whether its write was allowed."
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}},
		WithInteractiveSessionID("codex-native-agent-"+codexRandomHex(4)),
		WithPersistentInteractiveSession(true),
		WithProjectDirID(workDir),
		WithSandbox("workspace-write"),
		WithNativeTools(),
		liveConfined(t, "codex-cli", workDir),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
	)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	final := strings.TrimSpace(resp.Choices[0].Content)
	t.Logf("final: %s", final)
	if !strings.Contains(final, secret) {
		t.Fatalf("subagent read failed: final %q lacks %s", final, secret)
	}
	if _, err := os.Stat(filepath.Join(workDir, "child-write-attempt")); err != nil {
		t.Fatalf("a subagent could not write in the working directory: %v", err)
	}
	// Prove a subagent actually ran: Codex records spawned agents in its
	// rollouts (the spawn tool call in the parent's rollout).
	// Under Seatbelt (and Landlock) Codex keeps its rollouts in the private
	// CODEX_HOME the platform gave it; look there and in the person's own home.
	home, _ := os.UserHomeDir()
	var rollouts []string
	for _, codexHome := range []string{
		filepath.Join(workDir, ".agentworks-test-sandbox", "cli-home", "codex-cli", ".codex"),
		filepath.Join(home, ".codex"),
	} {
		found, _ := filepath.Glob(filepath.Join(codexHome, "sessions", "*", "*", "*", "rollout-*.jsonl"))
		rollouts = append(rollouts, found...)
	}
	spawned := false
	for _, f := range rollouts {
		info, statErr := os.Stat(f)
		if statErr != nil || info.ModTime().Before(started) {
			continue
		}
		raw, _ := os.ReadFile(f)
		if strings.Contains(string(raw), workDir) && strings.Contains(string(raw), "spawn_agent") {
			spawned = true
			break
		}
	}
	if !spawned {
		t.Fatalf("no subagent spawn recorded in this run's rollouts")
	}
}

// liveConfined runs a Full CLI live test under the lock a real chat gets
// (Seatbelt on a Mac, Landlock on Linux), or skips: Full CLI never runs
// unconfined.
func liveConfined(t *testing.T, provider, workDir string) llmtypes.CallOption {
	t.Helper()
	policy, ok := clisandbox.TestConfinement(provider, workDir)
	if !ok {
		t.Skip("this host cannot confine a coding CLI (set CODING_TEST_LANDLOCK_RUNNER on Linux)")
	}
	if policy.SeatbeltEnforced() {
		// Only SeatbeltArgs writes this profile: proof the CLI started inside it.
		profile := filepath.Join(policy.PrivateHome, "agentworks-cli-seatbelt.sb")
		t.Cleanup(func() {
			if _, err := os.Stat(profile); err != nil {
				t.Errorf("the CLI did not start under Seatbelt: %v", err)
			}
		})
	}
	return func(o *llmtypes.CallOptions) { p := policy.Clone(); o.CLISecurity = &p }
}

// untrustedCodexDir is a folder Codex has never trusted (temp folders may be
// trusted by earlier runs), so a launch that does not pre-trust it in the
// config Codex actually reads stops on Codex's "Trust this folder?" screen.
func untrustedCodexDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		return t.TempDir()
	}
	dir, err := os.MkdirTemp(home, ".agentworks-codex-untrusted-")
	if err != nil {
		return t.TempDir()
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestCodexCLIRealFullModeNativeShellOff pins PLAT-491 on the real CLI: in Full
// mode the CLI's own shell is gone by default (a `touch` through it must not
// happen) while native file edits still work, and AGENTWORKS_CLI_NATIVE_SHELL=on
// brings the shell back.
func TestCodexCLIRealFullModeNativeShellOff(t *testing.T) {
	requireRealCodexCLIE2E(t)
	run := func(t *testing.T, shellOn bool) (final, workDir string) {
		if shellOn {
			t.Setenv(nativeshell.EnvVar, "on")
		} else {
			t.Setenv(nativeshell.EnvVar, "off")
		}
		t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })
		workDir = untrustedCodexDir(t)
		adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		prompt := "Integration test in a disposable directory. Step 1: run the shell command `id -un > shell-ran.txt` with your own shell/command tool; if you have no such tool, say NO_SHELL. " +
			"Step 2: create the file edit-ok.txt containing exactly EDIT_OK using your file edit/patch tool (not the shell). Reply on one line: step 1 outcome (the user name, or NO_SHELL), step 2 outcome."
		resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}},
			WithInteractiveSessionID("codex-nsh-"+codexRandomHex(4)),
			WithPersistentInteractiveSession(true),
			WithProjectDirID(workDir),
			WithSandbox("workspace-write"),
			WithNativeTools(),
			liveConfined(t, "codex-cli", workDir),
			WithApprovalPolicy("never"),
			WithReasoningEffort("low"),
		)
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
		final = strings.TrimSpace(resp.Choices[0].Content)
		t.Logf("shellOn=%v final: %s", shellOn, final)
		return final, workDir
	}
	t.Run("default off", func(t *testing.T) {
		final, dir := run(t, false)
		if _, err := os.Stat(filepath.Join(dir, "shell-ran.txt")); err == nil {
			t.Fatalf("the CLI's built-in shell ran in Full mode by default; final: %s", final)
		}
		if b, err := os.ReadFile(filepath.Join(dir, "edit-ok.txt")); err != nil || !strings.Contains(string(b), "EDIT_OK") {
			t.Fatalf("native file edit did not work with the shell off: %v", err)
		}
	})
	t.Run("escape hatch on", func(t *testing.T) {
		final, dir := run(t, true)
		if _, err := os.Stat(filepath.Join(dir, "shell-ran.txt")); err != nil {
			t.Fatalf("the CLI's shell did not run with %s=on: %v; final: %s", nativeshell.EnvVar, err, final)
		}
	})
}

// TestCodexNativeToolsShellFeaturesFollowSwitch pins the decision: Full mode
// keeps shell_tool/unified_exec disabled unless the escape hatch is on.
func TestCodexNativeToolsShellFeaturesFollowSwitch(t *testing.T) {
	has := func(list []string, f string) bool {
		for _, x := range list {
			if x == f {
				return true
			}
		}
		return false
	}
	t.Setenv(nativeshell.EnvVar, "off")
	off := CodexNativeDisabledFeatures()
	if !has(off, "shell_tool") || !has(off, "unified_exec") || has(off, "multi_agent") {
		t.Fatalf("default: %v", off)
	}
	t.Setenv(nativeshell.EnvVar, "on")
	on := CodexNativeDisabledFeatures()
	if has(on, "shell_tool") || has(on, "unified_exec") {
		t.Fatalf("escape hatch on: %v", on)
	}
}
