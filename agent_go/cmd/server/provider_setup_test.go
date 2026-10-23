package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/llm"
)

func environmentValue(environment []string, name string) (string, bool) {
	prefix := name + "="
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix), true
		}
	}
	return "", false
}

func TestServerClaudeSetupEnvironmentUsesChatCredentialWithoutOtherSecrets(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "ambient-token")
	t.Setenv("CLAUDE_CONFIG_DIR", "/server/claude")
	t.Setenv("ANTHROPIC_API_KEY", "unrelated-api-key")
	t.Setenv("CODEX_API_KEY", "unrelated-codex-key")
	t.Setenv("AUTH_SECRET", "unrelated-server-secret")
	token := "resolved-chat-token"
	environment := serverClaudeSetupEnvironment(&llm.ProviderAPIKeys{ClaudeCodeOAuthToken: &token})
	if value, _ := environmentValue(environment, "CLAUDE_CODE_OAUTH_TOKEN"); value != token {
		t.Fatal("usage terminal did not receive the resolved chat credential")
	}
	if value, _ := environmentValue(environment, "CLAUDE_CONFIG_DIR"); value != "/server/claude" {
		t.Fatal("usage terminal lost the server account's config directory")
	}
	for _, name := range []string{"ANTHROPIC_API_KEY", "CODEX_API_KEY", "AUTH_SECRET"} {
		if _, present := environmentValue(environment, name); present {
			t.Fatalf("unrelated variable %s reached the Claude terminal", name)
		}
	}
	if _, present := environmentValue(serverClaudeSetupEnvironment(&llm.ProviderAPIKeys{}), "CLAUDE_CODE_OAUTH_TOKEN"); present {
		t.Fatal("an absent resolved token fell back to an ambient credential")
	}
}

func TestWorkflowProviderSetupEnvironmentUsesClaudeTokenInIsolation(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "ambient-api-key")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "ambient-auth-token")
	t.Setenv("ANTHROPIC_BASE_URL", "https://ambient.invalid")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "ambient-setup-token")
	t.Setenv("CLAUDE_CONFIG_DIR", "/ambient/claude")
	token := "workflow-setup-token"

	environment, cleanup, err := workflowProviderSetupEnvironment("claude-code", &llm.ProviderAPIKeys{ClaudeCodeOAuthToken: &token})
	if err != nil {
		t.Fatalf("workflowProviderSetupEnvironment: %v", err)
	}
	configDir, ok := environmentValue(environment, "CLAUDE_CONFIG_DIR")
	if !ok || configDir == "" || configDir == "/ambient/claude" {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want isolated temporary directory", configDir)
	}
	if value, _ := environmentValue(environment, "CLAUDE_CODE_OAUTH_TOKEN"); value != token {
		t.Fatalf("CLAUDE_CODE_OAUTH_TOKEN = %q, want workflow token", value)
	}
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL"} {
		if value, present := environmentValue(environment, name); present {
			t.Fatalf("%s leaked into workflow terminal as %q", name, value)
		}
	}
	cleanup()
	if _, statErr := os.Stat(configDir); !os.IsNotExist(statErr) {
		t.Fatalf("temporary Claude config directory was not removed: %v", statErr)
	}
}

func TestWorkflowProviderSetupEnvironmentUsesCursorKeyInIsolation(t *testing.T) {
	t.Setenv("CURSOR_API_KEY", "ambient-api-key")
	t.Setenv("CURSOR_AUTH_TOKEN", "ambient-auth-token")
	key := "workflow-cursor-key"

	environment, cleanup, err := workflowProviderSetupEnvironment("cursor-cli", &llm.ProviderAPIKeys{CursorCLI: &key})
	if err != nil {
		t.Fatalf("workflowProviderSetupEnvironment: %v", err)
	}
	defer cleanup()
	if value, _ := environmentValue(environment, "CURSOR_API_KEY"); value != key {
		t.Fatalf("CURSOR_API_KEY = %q, want workflow key", value)
	}
	if value, present := environmentValue(environment, "CURSOR_AUTH_TOKEN"); present {
		t.Fatalf("CURSOR_AUTH_TOKEN leaked into workflow terminal as %q", value)
	}
}

func TestWorkflowProviderSetupEnvironmentRequiresScopedCredential(t *testing.T) {
	for _, provider := range []string{"claude-code", "cursor-cli"} {
		if _, _, err := workflowProviderSetupEnvironment(provider, &llm.ProviderAPIKeys{}); err == nil {
			t.Fatalf("%s accepted a workflow terminal without its credential", provider)
		}
	}
}

func TestProviderSetupSessionStreamsInteractivePTY(t *testing.T) {
	original := providerSetupCommands
	providerSetupCommands = map[string]map[string]providerSetupCommand{
		"codex-cli": {
			"authenticate": {
				command: "/bin/sh",
				args:    []string{"-c", `printf "Choose an option: "; read answer; printf "selected:%s\n" "$answer"`},
			},
		},
	}
	t.Cleanup(func() { providerSetupCommands = original })

	manager := newProviderSetupManager()
	session, err := manager.start("owner-1", "codex-cli", "authenticate", 100, 24, nil, nil, false)
	if err != nil {
		t.Fatalf("start setup: %v", err)
	}
	defer manager.remove(session.id, true)

	seed, chunks, unsubscribe := session.subscribe()
	defer unsubscribe()
	output := string(seed)
	deadline := time.After(5 * time.Second)
	inputSent := false
	for {
		if !inputSent && strings.Contains(output, "Choose an option:") {
			if err := session.write("two\n"); err != nil {
				t.Fatalf("write input: %v", err)
			}
			inputSent = true
		}
		if strings.Contains(output, "selected:two") {
			break
		}
		select {
		case chunk, ok := <-chunks:
			if !ok {
				t.Fatalf("session ended before expected output: %q", output)
			}
			output += string(chunk)
		case <-deadline:
			t.Fatalf("timed out waiting for interactive output: %q", output)
		}
	}

	for session.isRunning() {
		select {
		case <-deadline:
			t.Fatal("session did not finish")
		case <-time.After(10 * time.Millisecond):
		}
	}
	snapshot := session.snapshot()
	if snapshot.Status != "completed" || snapshot.ExitCode == nil || *snapshot.ExitCode != 0 {
		t.Fatalf("unexpected final snapshot: %+v", snapshot)
	}
}

func TestProviderSetupSessionPassesSuppliedEnvironmentToTerminalProcess(t *testing.T) {
	original := providerSetupCommands
	providerSetupCommands = map[string]map[string]providerSetupCommand{
		"cursor-cli": {
			"inspect": {
				command: "/bin/sh",
				args:    []string{"-c", `printf 'scope:%s' "$CURSOR_API_KEY"`},
			},
		},
	}
	t.Cleanup(func() { providerSetupCommands = original })

	manager := newProviderSetupManager()
	session, err := manager.start(
		"owner-1",
		"cursor-cli",
		"inspect",
		100,
		24,
		[]string{"CURSOR_API_KEY=workflow-key"},
		nil,
		false,
	)
	if err != nil {
		t.Fatalf("start setup: %v", err)
	}
	defer manager.remove(session.id, true)

	seed, chunks, unsubscribe := session.subscribe()
	defer unsubscribe()
	output := string(seed)
	deadline := time.After(5 * time.Second)
	for !strings.Contains(output, "scope:workflow-key") {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				t.Fatalf("session ended before expected environment output: %q", output)
			}
			output += string(chunk)
		case <-deadline:
			t.Fatalf("timed out waiting for environment output: %q", output)
		}
	}
}

func TestProviderUsageSessionWaitsForManualKeys(t *testing.T) {
	original := providerSetupCommands
	providerSetupCommands = map[string]map[string]providerSetupCommand{
		"claude-code": {
			"usage": {
				command: "/bin/sh",
				args: []string{"-c", `stty raw -echo
printf '❯\033[1CNo,\033[1Cexit\r\nYes,\033[1CI\033[1Ctrust\033[1Cthis\033[1Cfolder'
selection=$(dd bs=1 count=4 2>/dev/null)
[ "$selection" = "$(printf '\033[B\r')" ] || exit 1
stty sane -echo
printf '\ntrust-accepted\n❯'
IFS= read -r command
printf '\nsubmitted:%s\npwd:%s\n' "$command" "$PWD"`},
			},
		},
	}
	t.Cleanup(func() { providerSetupCommands = original })

	manager := newProviderSetupManager()
	session, err := manager.start("owner-1", "claude-code", "usage", 100, 24, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.remove(session.id, true)
	waitFor := func(text string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(session.outputText(), text) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("terminal did not reach %q; output=%q", text, session.outputText())
	}
	waitFor("folder")
	time.Sleep(350 * time.Millisecond)
	if !session.isRunning() || strings.Contains(session.outputText(), "trust-accepted") {
		t.Fatal("interactive usage answered its own startup prompt")
	}
	// These browser-generated reports must not become Escape/cancel at the
	// trust picker. Only the person's Down+Enter should reach the PTY.
	if err := session.userInput("\x1b[I\x1b[?1;2c\x1b[1;1R\x1b]11;rgb:0000/0000/0000\x07"); err != nil {
		t.Fatal(err)
	}
	if err := session.userInput("\x1b[B\r"); err != nil {
		t.Fatal(err)
	}
	waitFor("trust-accepted")
	if err := session.userInput("/usage\r"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		output := session.outputText()
		if strings.Contains(output, "submitted:/usage") {
			if !strings.Contains(output, "trust-accepted") {
				t.Fatal("usage command ran without accepting the cursor-positioned trust prompt")
			}
			if !strings.Contains(output, "agentworks-provider-usage-") {
				t.Fatalf("usage command did not run in a throwaway workspace: %q", output)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("usage command was not submitted; output=%q", session.outputText())
}

func TestProviderUsagePromptAndTrustDetection(t *testing.T) {
	if providerUsagePromptReady("claude-code", "❯ No, exit\n❯ Yes, I trust this folder") {
		t.Fatal("trust redraw treated as a command prompt")
	}
	if claudeUsageTrustChoice("❯No,exit\n❯Yes,Itrustthisfolder") != 1 {
		t.Fatal("old selection won over redraw")
	}
	if !providerUsageTrustPrompt("claude-code", "❯ No, exit\n  Yes, I trust this folder") {
		t.Fatal("Claude trust prompt was not detected")
	}
	if !providerUsageTrustPrompt("codex-cli", "Do you trust the contents of this directory?\nPress enter to continue") {
		t.Fatal("Codex trust prompt was not detected")
	}
	if !providerUsageTrustPrompt("muse-cli", "Do you trust this workspace?\n> 1 Trust and continue") {
		t.Fatal("Muse trust prompt was not detected")
	}
	for provider, prompt := range map[string]string{"claude-code": "❯", "codex-cli": "›", "muse-cli": "❯"} {
		if !providerUsagePromptReady(provider, prompt) {
			t.Fatalf("%s prompt was not detected", provider)
		}
	}
	// Codex 0.161 draws rows with cursor moves, not newlines (server B 2026-10-08).
	codexScreen := "\x1b[2;1H>_ OpenAI Codex (v0.161.0)\x1b[5;1Hloading\x1b[7;1H\x1b[1m›\x1b[0m Ask Codex to do anything\x1b[9;1H? for shortcuts"
	if !providerUsagePromptReady("codex-cli", providerUsageScreenText(codexScreen)) {
		t.Fatal("cursor-addressed Codex prompt was not detected")
	}
	if !providerUsagePromptReady("claude-code", providerUsageScreenText("❯\u00a0Try a suggestion")) {
		t.Fatal("Claude's non-breaking-space prompt was not detected")
	}
}

func TestCollectedProviderUsageWaitsForTrustSelectionAndReturnsOnlyUsage(t *testing.T) {
	original := providerSetupCommands
	providerSetupCommands = map[string]map[string]providerSetupCommand{"claude-code": {"usage": {command: "/bin/sh", args: []string{"-c", `stty raw -echo
printf '❯No,exit\r\nYes,Itrustthisfolder\r\nEnter to confirm'
selection=$(dd bs=1 count=3 2>/dev/null)
[ "$selection" = "$(printf '\033[B')" ] || exit 1
# Reproduce the real CLI's transient selection, then startup redraw to No.
printf '\r\n❯Yes,Itrustthisfolder'
sleep 0.05
printf '\r\n❯No,exit\r\nYes,Itrustthisfolder'
selection=$(dd bs=1 count=3 2>/dev/null)
[ "$selection" = "$(printf '\033[B')" ] || exit 1
printf '\r\nNo,exit\r\n❯Yes,Itrustthisfolder'
confirm=$(dd bs=1 count=1 2>/dev/null)
[ "$confirm" = "$(printf '\r')" ] || exit 1
stty sane -echo
# The real CLI draws a prompt before its usage command is ready.
(sleep 1; : > provider-ready) &
printf '\ntrust-accepted\n❯ '
IFS= read -r command
[ "$command" = /usage ] || exit 1
printf '\nLoading usage data...\n'
[ -f provider-ready ] || exit 1
sleep 2.5
printf '\nCurrent session: 42%% used\n'`}}}}
	t.Cleanup(func() { providerSetupCommands = original })
	manager := newProviderSetupManager()
	session, err := manager.start("owner", "claude-code", "usage", 100, 24, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.remove(session.id, true)
	session.mu.Lock()
	session.readOnly = true
	session.mu.Unlock()
	go driveProviderUsage(t.Context(), session)
	output := collectProviderUsageOutput(session, 8*time.Second)
	if !strings.Contains(output, "Current session: 42% used") || strings.Contains(output, "trust") || strings.Contains(output, "No,exit") {
		t.Fatalf("collected usage: %q", output)
	}
}

func TestProviderSetupRejectsCommandsOutsideAllowlist(t *testing.T) {
	originalOS := providerInstallHostOS
	providerInstallHostOS = "linux"
	t.Cleanup(func() { providerInstallHostOS = originalOS })
	manager := newProviderSetupManager()
	if _, err := manager.start("owner-1", "unknown", "install", 100, 24, nil, nil, false); err == nil {
		t.Fatal("expected unsupported provider error")
	}
	if _, err := manager.start("owner-1", "codex-cli", "shell", 100, 24, nil, nil, false); err == nil {
		t.Fatal("expected unsupported action error")
	}
	if _, err := manager.start("owner-1", "codex-cli", "install", 100, 24, nil, nil, false); err == nil {
		t.Fatal("provider installation belongs to deployment, not an interactive setup session")
	}
}

func TestProviderSetupCanReplaceRunningSession(t *testing.T) {
	original := providerSetupCommands
	providerSetupCommands = map[string]map[string]providerSetupCommand{
		"codex-cli": {
			"authenticate": {command: "/bin/sh", args: []string{"-c", "read answer"}},
		},
	}
	t.Cleanup(func() { providerSetupCommands = original })

	manager := newProviderSetupManager()
	first, err := manager.start("owner-1", "codex-cli", "authenticate", 100, 24, nil, nil, false)
	if err != nil {
		t.Fatalf("start first setup: %v", err)
	}
	if _, err := manager.start("owner-1", "codex-cli", "authenticate", 100, 24, nil, nil, false); err == nil {
		t.Fatal("expected a conflict without replacement")
	} else {
		var conflict *providerSetupConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("expected providerSetupConflictError, got %T: %v", err, err)
		}
	}

	replacement, err := manager.start("owner-1", "codex-cli", "authenticate", 100, 24, nil, nil, true)
	if err != nil {
		t.Fatalf("replace setup: %v", err)
	}
	defer manager.remove(replacement.id, true)
	if first.snapshot().Status != "cancelled" {
		t.Fatalf("first setup status = %q, want cancelled", first.snapshot().Status)
	}
	if replacement.id == first.id || !replacement.isRunning() {
		t.Fatalf("replacement was not started: %+v", replacement.snapshot())
	}
}

func TestProviderSetupAllowlistIncludesReviewedProviderActions(t *testing.T) {
	want := map[string]providerSetupCommand{
		"claude-code": {command: "claude", args: []string{"auth", "login"}},
		"codex-cli":   {command: "codex", args: []string{"login", "--device-auth"}},
		"cursor-cli":  {command: "cursor-agent", args: []string{"login"}},
		"pi-cli":      {command: "pi"},
		"muse-cli":    {command: "muse", args: []string{"login"}},
		"agy-cli":     {command: "agy"},
	}
	for provider, expected := range want {
		actions, ok := providerSetupCommands[provider]
		if !ok {
			t.Fatalf("%s is missing from the guided setup allowlist", provider)
		}
		if _, exists := actions["install"]; exists {
			t.Fatalf("%s unexpectedly exposes installation through the user terminal", provider)
		}
		authenticate := actions["authenticate"]
		if authenticate.command != expected.command || strings.Join(authenticate.args, "\x00") != strings.Join(expected.args, "\x00") {
			t.Fatalf("unexpected %s authentication command: %#v", provider, authenticate)
		}
	}

	inspect := map[string]providerSetupCommand{
		"claude-code": {command: "claude", args: []string{"--tools", ""}},
		"codex-cli":   {command: "codex", args: []string{"--sandbox", "read-only", "--ask-for-approval", "never"}},
		"cursor-cli":  {command: "cursor-agent", args: []string{"--mode", "ask", "--sandbox", "enabled"}},
		"pi-cli":      {command: "pi"},
		"muse-cli":    {command: "muse", args: []string{"--disable-shell", "--disable-write"}},
		"agy-cli":     {command: "agy", args: []string{"--sandbox"}},
	}
	for provider, expected := range inspect {
		actual, ok := providerSetupCommands[provider]["inspect"]
		if !ok {
			t.Fatalf("%s is missing its reviewed inspection command", provider)
		}
		if actual.command != expected.command || strings.Join(actual.args, "\x00") != strings.Join(expected.args, "\x00") {
			t.Fatalf("unexpected %s inspection command: %#v", provider, actual)
		}
	}

	usage := map[string]providerSetupCommand{
		"claude-code": {command: "claude", args: []string{"--tools", "", "--strict-mcp-config"}},
		"codex-cli":   {command: "codex", args: []string{"--sandbox", "read-only", "--ask-for-approval", "never", "--disable", "shell_tool"}},
		"muse-cli":    {command: "muse", args: []string{"--disable-shell", "--disable-write"}},
	}
	for provider, expected := range usage {
		actual, ok := providerSetupCommands[provider]["usage"]
		if !ok {
			t.Fatalf("%s is missing its reviewed usage command", provider)
		}
		if actual.command != expected.command || strings.Join(actual.args, "\x00") != strings.Join(expected.args, "\x00") {
			t.Fatalf("unexpected %s usage command: %#v", provider, actual)
		}
	}
}

// The account terminal must not open on Claude's first-run theme picker; a chosen theme and
// the rest of the file are kept.
func TestSeedClaudeThemeKeepsExistingSettings(t *testing.T) {
	home := t.TempDir()
	seedClaudeTheme(home)
	raw, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if !strings.Contains(string(raw), `"theme": "dark"`) {
		t.Fatalf("theme not seeded: %s", raw)
	}
	_ = os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"theme":"light","hasCompletedOnboarding":true}`), 0o600)
	seedClaudeTheme(home)
	raw, _ = os.ReadFile(filepath.Join(home, ".claude.json"))
	if !strings.Contains(string(raw), `"theme":"light"`) {
		t.Fatalf("overwrote a chosen theme: %s", raw)
	}
	_ = os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{broken`), 0o600)
	seedClaudeTheme(home)
	if raw, _ = os.ReadFile(filepath.Join(home, ".claude.json")); string(raw) != `{broken` {
		t.Fatalf("rewrote an unreadable file: %s", raw)
	}
}

// The usage and inspect terminals start on the account's first allowed model, not the CLI's own default.
func TestProviderSetupTerminalStartsOnAnAllowedModel(t *testing.T) {
	if got := providerSetupModelArgs("codex-cli", []string{"gpt-6-luna", "gpt-6-sol"}); len(got) != 2 || got[0] != "-m" || got[1] != "gpt-6-luna" {
		t.Fatalf("codex: %v", got)
	}
	if got := providerSetupModelArgs("claude-code", []string{"claude-haiku-5-5"}); len(got) != 2 || got[0] != "--model" || got[1] != "claude-haiku-5-5" {
		t.Fatalf("claude: %v", got)
	}
	if providerSetupModelArgs("codex-cli", nil) != nil || providerSetupModelArgs("cursor-cli", []string{"x"}) != nil {
		t.Fatal("no model flag when every model is allowed or the provider's flag is unknown")
	}
}

// A failed startup must end collection while the CLI is still alive, so the
// HTTP handler can remove the session and release its busy account promptly.
func TestCollectedProviderUsageReportsStartupFailurePromptly(t *testing.T) {
	terminal, err := os.CreateTemp(t.TempDir(), "closed-terminal")
	if err != nil {
		t.Fatal(err)
	}
	_ = terminal.Close()
	now := time.Now()
	session := &providerSetupSession{id: "usage-failure", provider: "claude-code", action: "usage", status: "running", terminal: terminal, createdAt: now, updatedAt: now, done: make(chan struct{}), usageStartupDone: make(chan struct{})}
	session.appendOutput([]byte("❯No,exit\nYes,Itrustthisfolder\nEnter to confirm"))
	go driveProviderUsage(t.Context(), session)
	started := time.Now()
	_ = collectProviderUsageOutput(session, 5*time.Second)
	session.mu.Lock()
	failed, submitted := session.usageStartupErr, session.usageSubmitted
	session.mu.Unlock()
	if failed == nil || !submitted.IsZero() || time.Since(started) > 2*time.Second {
		t.Fatalf("startup failure was not reported promptly: %v, %v", failed, time.Since(started))
	}
}

func TestProviderUsageLoadingWaitsForResultOrProviderError(t *testing.T) {
	for _, check := range []struct {
		output  string
		pending bool
	}{
		{"Loading usage data…\nWhat's contributing to your limits usage?", true},
		{"Loading usage data…\nCurrent session: 42% used", false},
		{"Loading usage data…\nLogin expired. Run /login", false},
		{"Loading usage data…\nUsage unavailable for this account", false},
		{"Loading usage data…\nCurrent week 42%\nLoading usage data…", true},
	} {
		if got := providerUsageResultPending("claude-code", check.output); got != check.pending {
			t.Errorf("pending(%q)=%t", check.output, got)
		}
	}
}
