package server

import (
	"errors"
	"os"
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

func TestProviderUsageSessionSubmitsAllowlistedCommand(t *testing.T) {
	original := providerSetupCommands
	providerSetupCommands = map[string]map[string]providerSetupCommand{
		"claude-code": {
			"usage": {
				command: "/bin/sh",
				args:    []string{"-c", `printf '❯'; IFS= read -r command; printf '\nsubmitted:%s\npwd:%s\n' "$command" "$PWD"`},
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

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		output := session.outputText()
		if strings.Contains(output, "submitted:/usage") {
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
}

func TestProviderSetupRejectsCommandsOutsideAllowlist(t *testing.T) {
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
		"claude-code": {command: "claude", args: []string{"--tools", ""}},
		"codex-cli":   {command: "codex", args: []string{"--sandbox", "read-only", "--ask-for-approval", "never"}},
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
