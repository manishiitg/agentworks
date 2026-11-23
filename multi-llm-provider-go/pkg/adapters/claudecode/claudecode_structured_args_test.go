package claudecode

import (
	"reflect"
	"testing"
)

func has(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// TestBuildClaudeStructuredArgs pins the structured argv shape and the session
// id the turn runs under. Two invariants matter: (1) resume uses --resume and
// runs under the prior id; a fresh turn uses --session-id and the minted id;
// (2) whenever allowedTools is set, the hard --disallowedTools denylist is
// present — that is what keeps claude on the MCP bridge instead of falling back
// to its native Bash (which --dangerously-skip-permissions would otherwise
// allow).
func TestBuildClaudeStructuredArgs(t *testing.T) {
	t.Run("fresh turn with mcp config and allowed tools", func(t *testing.T) {
		got, sessionID := buildClaudeStructuredArgs("claude-x", "sys", "", "mcp__bridge__do", "", "/tmp/mcp.json", "", "fresh-1", "/work")
		want := []string{
			"-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions",
			"--model", "claude-x",
			"--append-system-prompt", "sys",
			"--allowedTools", "mcp__bridge__do",
			"--disallowedTools", claudeStructuredDisallowedTools,
			"--mcp-config", "/tmp/mcp.json", "--strict-mcp-config",
			"--session-id", "fresh-1",
			"--add-dir", "/work",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("fresh argv mismatch:\n got=%v\nwant=%v", got, want)
		}
		if sessionID != "fresh-1" {
			t.Errorf("fresh turn should run under the minted id, got %q", sessionID)
		}
	})

	t.Run("resume turn: --resume, runs under prior id, no --session-id", func(t *testing.T) {
		got, sessionID := buildClaudeStructuredArgs("claude-x", "", "", "mcp__bridge__do", "", "", "prior-9", "unused-fresh", "")
		if sessionID != "prior-9" {
			t.Errorf("resume turn should run under prior id, got %q", sessionID)
		}
		ri := indexOfClaude(got, "--resume")
		if ri == -1 || got[ri+1] != "prior-9" {
			t.Errorf("expected --resume prior-9, got %v", got)
		}
		if has(got, "--session-id") {
			t.Errorf("resume turn must not also pass --session-id, got %v", got)
		}
	})

	t.Run("disallowedTools tracks allowedTools", func(t *testing.T) {
		with, _ := buildClaudeStructuredArgs("claude-x", "", "", "mcp__bridge__do", "", "", "", "f", "")
		if !has(with, "--disallowedTools") {
			t.Errorf("allowedTools set => --disallowedTools denylist required, got %v", with)
		}
		without, _ := buildClaudeStructuredArgs("claude-x", "", "", "", "", "", "", "f", "")
		if has(without, "--allowedTools") || has(without, "--disallowedTools") {
			t.Errorf("no allowedTools => neither allow/deny flag, got %v", without)
		}
		// The permission bypass is always present regardless of tool flags.
		if !has(without, "--dangerously-skip-permissions") {
			t.Errorf("--dangerously-skip-permissions must always be present, got %v", without)
		}
	})

	t.Run("hybrid provider auto preserves native tools", func(t *testing.T) {
		t.Setenv("AGENTWORKS_CLI_NATIVE_SHELL", "off")
		got, _ := buildClaudeStructuredArgs("claude-x", "sys", "default", "mcp__bridge__do", "auto", "/tmp/mcp.json", "", "fresh-1", "/work")
		if !has(got, "--permission-mode") || !has(got, "auto") {
			t.Fatalf("hybrid argv missing --permission-mode auto: %v", got)
		}
		if !has(got, "--tools") || !has(got, "default") {
			t.Fatalf("hybrid argv missing --tools default: %v", got)
		}
		// PLAT-491: only the native shell tools are denied (escape hatch off).
		if has(got, "--dangerously-skip-permissions") || !has(got, claudeNativeShellDisallowed("default")) {
			t.Fatalf("provider_auto must not bypass approvals and may deny only the native shell: %v", got)
		}
	})
}

func indexOfClaude(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}
