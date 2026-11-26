package claudecode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-364: Claude Code started under the Landlock launcher keeps its native
// tools and MCP bridge inside its folder, and the kernel refuses its native
// Read of another folder. Runs on a Linux host with the launcher:
// AGENTWORKS_LANDLOCK_RUNNER=/path/to/video-studio-landlock-runner.
func TestClaudeCodeTmuxRealLandlockConfinesNativeToolsP0(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)
	runner := os.Getenv("AGENTWORKS_LANDLOCK_RUNNER")
	if runtime.GOOS != "linux" || runner == "" {
		t.Skip("needs Linux and AGENTWORKS_LANDLOCK_RUNNER")
	}
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })
	workDir := t.TempDir()
	otherDir := t.TempDir()
	privateHome := filepath.Join(t.TempDir(), "cli-home")
	own := "OWN-" + randomHex(4)
	foreign := "FOREIGN-" + randomHex(4)
	if err := os.WriteFile(filepath.Join(workDir, "own.txt"), []byte(own+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	foreignPath := filepath.Join(otherDir, "secret-test.txt")
	if err := os.WriteFile(foreignPath, []byte(foreign+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	markerDir := t.TempDir() // the test MCP server writes a marker here
	mcpServerPath := writeClaudeInteractiveSlowMCPServer(t, filepath.Join(markerDir, "slow-tool-started"))
	opts := []llmtypes.CallOption{
		WithInteractiveSessionID("claude-landlock-" + randomHex(4)),
		WithPersistentInteractiveSession(true),
		WithWorkingDir(workDir),
		WithMCPConfig(fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":%q}}}`, mcpServerPath)),
		WithClaudeCodeTools(claudeHybridLiveTools),
		WithPermissionMode("auto"),
		WithAllowedTools("mcp__api-bridge__*,WebSearch"),
		WithEffort("low"),
	}
	policy := llmtypes.CLISecurityPolicy{
		Mode:           llmtypes.CLISecurityModeIsolated,
		Provider:       "claude-code",
		LandlockRunner: runner,
		PrivateHome:    privateHome,
		HostWritePaths: []string{markerDir},
	}
	opts = append(opts, func(o *llmtypes.CallOptions) { p := policy.Clone(); o.CLISecurity = &p })
	// An env-token account (RTS's server account): the adapter passes the
	// token, so nothing from the shared home is needed.
	adapter := NewClaudeCodeInteractiveAdapterWithOAuthToken(defaultClaudeInteractiveTestModel, os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"), &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	prompt := "Integration test in disposable scratch folders. Using ONLY your native Read tool, read own.txt, then read " + foreignPath +
		". Then call the api-bridge slow_contract MCP tool with token BRIDGE-OK and delay_ms 100. " +
		"Reply on three lines: the first line of own.txt, the result or exact error for the second file, and the MCP result."
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, opts...)
	if err != nil {
		t.Fatalf("GenerateContent under Landlock: %v", err)
	}
	final := firstChoiceText(resp)
	t.Logf("reply: %q", final)
	if !strings.Contains(final, own) || !strings.Contains(final, "BRIDGE-OK") {
		t.Fatalf("confined Claude lost its own folder or MCP bridge: %q", final)
	}
	if strings.Contains(final, foreign) {
		t.Fatalf("confined Claude read another folder: %q", final)
	}
	// Claude's transcript is in the private home, where the adapter read it.
	if matches, _ := filepath.Glob(filepath.Join(privateHome, ".claude", "projects", "*", "*.jsonl")); len(matches) == 0 {
		t.Fatalf("no transcript in the private home %s", privateHome)
	}
}

// The structured (stream-json) lane, used by workflow steps and background
// turns, is confined the same way.
func TestClaudeCodeStructuredRealLandlockConfinesNativeToolsP0(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)
	runner := os.Getenv("AGENTWORKS_LANDLOCK_RUNNER")
	if runtime.GOOS != "linux" || runner == "" {
		t.Skip("needs Linux and AGENTWORKS_LANDLOCK_RUNNER")
	}
	workDir := t.TempDir()
	otherDir := t.TempDir()
	privateHome := filepath.Join(t.TempDir(), "cli-home")
	own := "OWN-" + randomHex(4)
	foreign := "FOREIGN-" + randomHex(4)
	if err := os.WriteFile(filepath.Join(workDir, "own.txt"), []byte(own+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	foreignPath := filepath.Join(otherDir, "secret-test.txt")
	if err := os.WriteFile(foreignPath, []byte(foreign+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Provider: "claude-code", LandlockRunner: runner, PrivateHome: privateHome}
	opts := []llmtypes.CallOption{
		WithClaudeStructuredTransport(true),
		WithWorkingDir(workDir),
		WithClaudeCodeTools(claudeHybridLiveTools),
		WithPermissionMode("auto"),
		WithEffort("low"),
		func(o *llmtypes.CallOptions) { p := policy.Clone(); o.CLISecurity = &p },
	}
	adapter := NewClaudeCodeInteractiveAdapterWithOAuthToken(defaultClaudeInteractiveTestModel, os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"), &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	prompt := "Integration test in disposable scratch folders. Using ONLY your native Read tool, read own.txt, then read " + foreignPath +
		". Reply on two lines: the first line of own.txt, and the result or exact error for the second file."
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, opts...)
	if err != nil {
		t.Fatalf("structured GenerateContent under Landlock: %v", err)
	}
	final := firstChoiceText(resp)
	t.Logf("reply: %q", final)
	if !strings.Contains(final, own) {
		t.Fatalf("confined structured Claude lost its own folder: %q", final)
	}
	if strings.Contains(final, foreign) {
		t.Fatalf("confined structured Claude read another folder: %q", final)
	}
}

// Full CLI (PLAT-364 phase 4): Claude's own Bash and file edits, confined by
// the Landlock launcher: it writes and runs commands in its folder, and
// cannot write another folder or read the service's real home.
func TestClaudeCodeTmuxRealLandlockFullCLIP0(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)
	runner := os.Getenv("AGENTWORKS_LANDLOCK_RUNNER")
	if runtime.GOOS != "linux" || runner == "" {
		t.Skip("needs Linux and AGENTWORKS_LANDLOCK_RUNNER")
	}
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })
	workDir := t.TempDir()
	otherDir := t.TempDir()
	realHome, _ := os.UserHomeDir()
	policy := llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Provider: "claude-code", LandlockRunner: runner, PrivateHome: filepath.Join(t.TempDir(), "cli-home")}
	opts := []llmtypes.CallOption{
		WithInteractiveSessionID("claude-fullcli-" + randomHex(4)),
		WithPersistentInteractiveSession(true),
		WithWorkingDir(workDir),
		WithClaudeCodeTools(claudeHybridLiveTools + ",Bash,Write,Edit,MultiEdit"),
		// Full CLI: the Landlock lock is the boundary, so Claude must not stop
		// the turn to ask (mcpagent passes the same option in full mode).
		WithDangerouslySkipPermissions(),
		WithAllowedTools("Bash,Write,Edit,MultiEdit,Read,Glob,Grep"),
		WithEffort("low"),
		func(o *llmtypes.CallOptions) { p := policy.Clone(); o.CLISecurity = &p },
	}
	token := "FULL-" + randomHex(4)
	prompt := "Integration test in disposable scratch folders; this is authorised. Do each step with your own tools and report each outcome: " +
		"1) Use Write to create note.txt here containing " + token + ". " +
		"2) Use Bash to run: echo " + token + " > shell.txt && cat shell.txt. " +
		"3) Use Bash to run: echo x > " + filepath.Join(otherDir, "escape.txt") + " ; and report the exact error. " +
		"4) Use Write to create " + filepath.Join(otherDir, "escape2.txt") + " and report the exact error. " +
		"5) Use Bash to run: ls " + filepath.Join(realHome, ".claude") + " ; and report the exact error."
	adapter := NewClaudeCodeInteractiveAdapterWithOAuthToken(defaultClaudeInteractiveTestModel, os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"), &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, opts...)
	if err != nil {
		t.Fatalf("Full CLI turn: %v", err)
	}
	t.Logf("reply: %.1200s", firstChoiceText(resp))
	for _, name := range []string{"note.txt", "shell.txt"} {
		data, readErr := os.ReadFile(filepath.Join(workDir, name))
		if readErr != nil || !strings.Contains(string(data), token) {
			t.Fatalf("Full CLI could not write %s in its own folder: %v %q", name, readErr, data)
		}
	}
	for _, name := range []string{"escape.txt", "escape2.txt"} {
		if _, statErr := os.Stat(filepath.Join(otherDir, name)); !os.IsNotExist(statErr) {
			t.Fatalf("Full CLI wrote %s outside its folder", name)
		}
	}
}

// Full CLI on a person's own Mac: Claude's own Bash and Write run under
// Seatbelt. The person's home stays open (their files read fine), while
// another workflow in AgentWorks' workspace data, a blocked path inside its
// own folder, and scripting other apps are refused by the kernel.
func TestClaudeCodeTmuxRealSeatbeltFullCLIP0(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)
	if runtime.GOOS != "darwin" {
		t.Skip("needs macOS")
	}
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	root, err := os.MkdirTemp(home, ".agentworks-seatbelt-live-")
	if err != nil {
		t.Skipf("cannot create a folder under the home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	workspace := filepath.Join(root, "workspace-docs")
	workDir := filepath.Join(workspace, "Workflow", "mine")
	otherWorkflow := filepath.Join(workspace, "Workflow", "other")
	personal := filepath.Join(root, "my-notes")
	planning := filepath.Join(workDir, "planning")
	for _, dir := range []string{workDir, otherWorkflow, personal, planning} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	note := "PERSONAL-" + randomHex(4)
	foreign := "FOREIGN-" + randomHex(4)
	for path, body := range map[string]string{
		filepath.Join(personal, "note.txt"):     note,
		filepath.Join(otherWorkflow, "key.txt"): foreign,
		filepath.Join(planning, "plan.json"):    "{}",
	} {
		if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	policy := llmtypes.CLISecurityPolicy{
		Mode: llmtypes.CLISecurityModeIsolated, Provider: "claude-code", Seatbelt: true,
		PrivateHome:       filepath.Join(workDir, ".sandbox", "cli-home"),
		ProtectedRoots:    []string{workspace},
		BlockedWritePaths: []string{planning},
	}
	opts := []llmtypes.CallOption{
		WithInteractiveSessionID("claude-seatbelt-" + randomHex(4)),
		WithPersistentInteractiveSession(true),
		WithWorkingDir(workDir),
		WithClaudeCodeTools(claudeHybridLiveTools + ",Bash,Write,Edit,MultiEdit"),
		WithDangerouslySkipPermissions(),
		WithAllowedTools("Bash,Write,Edit,MultiEdit,Read,Glob,Grep"),
		WithEffort("low"),
		func(o *llmtypes.CallOptions) { p := policy.Clone(); o.CLISecurity = &p },
	}
	token := "FULL-" + randomHex(4)
	prompt := "Integration test in disposable scratch folders; this is authorised. Do each step with Bash and report each outcome with the exact error if any: " +
		"1) echo " + token + " > shell.txt && cat shell.txt. " +
		"2) cat " + filepath.Join(personal, "note.txt") + ". " +
		"3) cat " + filepath.Join(otherWorkflow, "key.txt") + ". " +
		"4) echo x > " + filepath.Join(otherWorkflow, "escape.txt") + ". " +
		"5) echo x > " + filepath.Join(planning, "plan.json") + ". " +
		"6) /usr/bin/osascript -e 'return 7'."
	adapter := NewClaudeCodeInteractiveAdapterWithOAuthToken(claudeInteractiveIntegrationModel(), os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"), &MockLogger{})
	adapter.SetSDKTransport(os.Getenv("CLAUDE_CODE_TRANSPORT") == "sdk")
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, opts...)
	if err != nil {
		t.Fatalf("Full CLI turn under Seatbelt: %v", err)
	}
	final := firstChoiceText(resp)
	t.Logf("reply: %.1500s", final)
	if data, err := os.ReadFile(filepath.Join(workDir, "shell.txt")); err != nil || !strings.Contains(string(data), token) {
		t.Fatalf("Claude could not write its own folder: %v %q", err, data)
	}
	if !strings.Contains(final, note) {
		t.Errorf("the person's own files must stay readable: %q", final)
	}
	if strings.Contains(final, foreign) {
		t.Errorf("Claude read another workflow: %q", final)
	}
	if _, err := os.Stat(filepath.Join(otherWorkflow, "escape.txt")); !os.IsNotExist(err) {
		t.Error("Claude wrote another workflow")
	}
	if data, _ := os.ReadFile(filepath.Join(planning, "plan.json")); strings.TrimSpace(string(data)) != "{}" {
		t.Errorf("Claude wrote a blocked path: %q", data)
	}
	if _, err := os.Stat(filepath.Join(policy.PrivateHome, "agentworks-cli-seatbelt.sb")); err != nil {
		t.Errorf("Claude did not start under Seatbelt: %v", err)
	}
}

// A Builder chat runs from a private runtime folder with the workflow linked
// in as project/. Under Seatbelt, Claude's own Edit must change a workflow
// file through that link (the folder is granted), while the sandbox still
// refuses the protected planning/ (owner test 2026-10-04: dontAsk refused every
// edit outside the runtime folder before the sandbox decided).
func TestClaudeCodeTmuxRealSeatbeltEditsLinkedProjectP0(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)
	if runtime.GOOS != "darwin" {
		t.Skip("needs macOS")
	}
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	root, err := os.MkdirTemp(home, ".agentworks-seatbelt-link-")
	if err != nil {
		t.Skipf("cannot create a folder under the home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	workspace := filepath.Join(root, "workspace-docs")
	workflow := filepath.Join(workspace, "Workflow", "mine")
	planning := filepath.Join(workflow, "planning")
	runtimeDir := filepath.Join(root, "state", "cli-runtimes", "v1", "abc")
	for _, dir := range []string{filepath.Join(workflow, "soul"), planning, runtimeDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	soul := filepath.Join(workflow, "soul", "soul.md")
	if err := os.WriteFile(soul, []byte("# Soul\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planning, "plan.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(workflow, filepath.Join(runtimeDir, "project")); err != nil {
		t.Fatal(err)
	}
	policy := llmtypes.CLISecurityPolicy{
		Mode: llmtypes.CLISecurityModeIsolated, Provider: "claude-code", Seatbelt: true,
		PrivateHome:         filepath.Join(runtimeDir, ".sandbox-cache", "cli-home", "claude-code"),
		WorkspaceWritePaths: []string{runtimeDir, workflow},
		ProtectedRoots:      []string{workspace, filepath.Join(root, "state")},
		BlockedWritePaths:   []string{planning},
	}
	token := "EDIT-" + randomHex(4)
	opts := []llmtypes.CallOption{
		WithInteractiveSessionID("claude-seatbelt-link-" + randomHex(4)),
		WithPersistentInteractiveSession(true),
		WithWorkingDir(runtimeDir),
		WithClaudeCodeTools(claudeHybridLiveTools + ",Bash,Write,Edit,MultiEdit"),
		WithDangerouslySkipPermissions(),
		WithAllowedTools("Bash,Write,Edit,MultiEdit,Read,Glob,Grep"),
		WithEffort("low"),
		func(o *llmtypes.CallOptions) { p := policy.Clone(); o.CLISecurity = &p },
	}
	prompt := "Integration test in disposable scratch folders; this is authorised. " +
		"1) Use your Edit tool to append the line " + token + " to project/soul/soul.md. " +
		"2) Use Bash to run: echo x >> project/planning/plan.json . Report each outcome with the exact error."
	adapter := NewClaudeCodeInteractiveAdapterWithOAuthToken(defaultClaudeInteractiveTestModel, os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"), &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, opts...)
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	t.Logf("reply: %.1200s", firstChoiceText(resp))
	if data, _ := os.ReadFile(soul); !strings.Contains(string(data), token) {
		t.Errorf("Claude's Edit did not change the linked workflow file: %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(planning, "plan.json")); strings.TrimSpace(string(data)) != "{}" {
		t.Errorf("the protected planning/ was written: %q", data)
	}
}
