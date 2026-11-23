package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestClaudeRetainedToolConfiguration(t *testing.T) {
	options := func(calls ...llmtypes.CallOption) *llmtypes.CallOptions {
		opts := &llmtypes.CallOptions{}
		for _, call := range calls {
			call(opts)
		}
		return opts
	}
	old := options(WithClaudeCodeTools("WebSearch"))
	hook := `{"hooks":{"PreToolUse":[{"matcher":"AskUserQuestion","hooks":[{"type":"command","command":"question-hook"}]}]}}`
	enabled := options(WithClaudeCodeTools("WebSearch,AskUserQuestion"), WithAllowedTools("AskUserQuestion"), WithClaudeCodeSettings(hook))
	if claudeToolConfigFingerprint(old) == claudeToolConfigFingerprint(enabled) {
		t.Fatal("native questions reused an incompatible retained process")
	}
	for _, changed := range []*llmtypes.CallOptions{
		options(WithClaudeCodeTools("WebSearch"), WithAllowedTools("AskUserQuestion"), WithClaudeCodeSettings(hook)),
		options(WithClaudeCodeTools("WebSearch,AskUserQuestion"), WithClaudeCodeSettings(hook)),
		options(WithClaudeCodeTools("WebSearch,AskUserQuestion"), WithAllowedTools("AskUserQuestion")),
	} {
		if claudeToolConfigFingerprint(enabled) == claudeToolConfigFingerprint(changed) {
			t.Fatal("removing a native question tool, permission or hook did not invalidate the process")
		}
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(hook), 0600); err != nil {
		t.Fatal(err)
	}
	fromFile := options(WithClaudeCodeTools("WebSearch,AskUserQuestion"), WithAllowedTools("AskUserQuestion"), WithClaudeCodeSettings(path))
	if claudeToolConfigFingerprint(enabled) != claudeToolConfigFingerprint(fromFile) {
		t.Fatal("equivalent settings churned the retained process")
	}
	if err := os.WriteFile(path, []byte(`{"hooks":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if claudeToolConfigFingerprint(enabled) == claudeToolConfigFingerprint(fromFile) {
		t.Fatal("a hook removed from a settings file was not noticed")
	}
	// Replacement must launch --resume rather than trying --session-id with
	// an existing transcript, and must not mutate the caller's options.
	resuming := claudeOptionsResumingNativeSession(enabled, "native-history")
	if claudeResumeIDFromOptions(enabled) != "" {
		t.Fatal("replacement mutated caller options")
	}
	adapter := NewClaudeCodeInteractiveAdapter("claude-sonnet-5-5", &MockLogger{})
	args, files, err := adapter.buildClaudeArgs(resuming, "", "native-history", "")
	defer removeFiles(files)
	if err != nil || !slices.Contains(args, "--resume") || slices.Contains(args, "--session-id") {
		t.Fatalf("replacement cannot resume native history: %v %v", args, err)
	}
	if claudeToolConfigFingerprint(enabled) != claudeToolConfigFingerprint(resuming) {
		t.Fatal("resume metadata changed the launch configuration")
	}
	if claudeResumeIDFromOptions(claudeOptionsResumingNativeSession(&llmtypes.CallOptions{}, "native-history")) != "native-history" {
		t.Fatal("removing all options could not resume history")
	}
}

func TestClaudeRetainedProcessReloadsQuestionConfiguration(t *testing.T) {
	// Exercise the actual acquisition and close paths without relying on a
	// paid model or local login. Only the tmux boundary is replaced.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\ncase \"$1\" in capture-pane) printf '\\n❯ \\n';; esac\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	adapter := NewClaudeCodeInteractiveAdapter("claude-sonnet-5-5", &MockLogger{})
	owner := "native-question-config-" + randomHex(4)
	t.Cleanup(func() { CloseClaudeCodeInteractiveSessionForOwner(owner, "test completed") })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const native = "3db6f35a-c8ec-4b36-a4df-f83e6cefba12"
	acquire := func(enabled bool) (*claudeInteractivePersistentSession, bool) {
		t.Helper()
		opts := &llmtypes.CallOptions{}
		WithClaudeCodeTools("WebSearch")(opts)
		if enabled {
			WithClaudeCodeTools("WebSearch,AskUserQuestion")(opts)
			WithAllowedTools("AskUserQuestion")(opts)
			WithClaudeCodeSettings(`{"hooks":{"PreToolUse":[{"matcher":"AskUserQuestion","hooks":[{"type":"command","command":"question-hook"}]}]}}`)(opts)
		}
		session, created, err := adapter.acquirePersistentInteractiveSession(ctx, owner, native, opts, "", "")
		if err != nil {
			t.Fatal(err)
		}
		releaseClaudePersistentInteractiveSession(session, adapter.logger)
		return session, created
	}
	old, created := acquire(false)
	if !created {
		t.Fatal("initial process was not created")
	}
	enabled, created := acquire(true)
	if !created || enabled.tmuxSessionName == old.tmuxSessionName || enabled.nativeSessionID != native {
		t.Fatal("enabling questions reused the older process or lost native history")
	}
	same, created := acquire(true)
	if created || same != enabled {
		t.Fatal("unchanged question settings churned the process")
	}
	disabled, created := acquire(false)
	if !created || disabled.tmuxSessionName == enabled.tmuxSessionName || disabled.nativeSessionID != native {
		t.Fatal("removing question support reused the enabled process or lost native history")
	}
}

// PLAT-491: Full mode drops Claude's own shell tools unless the escape hatch is
// on; reads, edits, skills and subagents stay.
func TestClaudeFullModeNativeShellOffByDefault(t *testing.T) {
	full := "WebSearch,Read,Skill,Agent,Bash,Write,Edit,Monitor,PowerShell,BashOutput,KillShell"
	t.Setenv("AGENTWORKS_CLI_NATIVE_SHELL", "off")
	if got := claudeEffectiveTools(full); got != "WebSearch,Read,Skill,Agent,Write,Edit" {
		t.Fatalf("default: %q", got)
	}
	if got := claudeNativeShellDisallowed("default"); got == "" {
		t.Fatal("default tool set must deny the shell tools")
	}
	t.Setenv("AGENTWORKS_CLI_NATIVE_SHELL", "on")
	if got := claudeEffectiveTools(full); got != full {
		t.Fatalf("escape hatch: %q", got)
	}
}
