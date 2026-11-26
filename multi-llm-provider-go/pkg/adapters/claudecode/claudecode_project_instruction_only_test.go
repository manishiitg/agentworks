package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestBuildClaudeArgsProjectInstructionOnly covers WithProjectInstructionOnly:
// by default the prompt is injected via --system-prompt-file (and also
// projected to AGENTS.md); with the flag on it is carried solely by AGENTS.md;
// and when the AGENTS.md projection cannot run the adapter falls back to
// --system-prompt-file so the prompt is never dropped.
// stubClaudeVersion makes the binary look like the given claude version for a test.
func stubClaudeVersion(t *testing.T, version string, err error) {
	t.Helper()
	claudeAgentsMDSupport.Range(func(key, _ any) bool { claudeAgentsMDSupport.Delete(key); return true })
	previous := claudeBinaryVersion
	claudeBinaryVersion = func() (string, error) { return "/usr/bin/claude " + version + " (Claude Code)", err }
	t.Cleanup(func() {
		claudeBinaryVersion = previous
		claudeAgentsMDSupport.Range(func(key, _ any) bool { claudeAgentsMDSupport.Delete(key); return true })
	})
}

func TestBuildClaudeArgsProjectInstructionOnly(t *testing.T) {
	stubClaudeVersion(t, "2.1.285", nil)
	const promptBody = "ORCHESTRATOR SYSTEM PROMPT BODY"

	t.Run("default: --system-prompt-file present and AGENTS.md also written", func(t *testing.T) {
		adapter := NewClaudeCodeInteractiveAdapter("claude-sonnet-4-6", &MockLogger{})
		dir := t.TempDir()
		opts := &llmtypes.CallOptions{}
		WithWorkingDir(dir)(opts)

		args, tempFiles, err := adapter.buildClaudeArgs(opts, "", "", promptBody)
		if err != nil {
			t.Fatalf("buildClaudeArgs error = %v", err)
		}
		defer removeFiles(tempFiles)

		if argValue(args, "--system-prompt-file") == "" {
			t.Fatalf("default mode must pass --system-prompt-file, args=%v", args)
		}
		body, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
		if err != nil {
			t.Fatalf("default mode should also write AGENTS.md: %v", err)
		}
		if !strings.Contains(string(body), promptBody) {
			t.Fatalf("AGENTS.md missing prompt body, got %q", string(body))
		}
	})

	t.Run("project-instruction-only: drops --system-prompt-file, keeps AGENTS.md", func(t *testing.T) {
		adapter := NewClaudeCodeInteractiveAdapter("claude-sonnet-4-6", &MockLogger{})
		dir := t.TempDir()
		opts := &llmtypes.CallOptions{}
		WithWorkingDir(dir)(opts)
		WithProjectInstructionOnly(true)(opts)

		args, tempFiles, err := adapter.buildClaudeArgs(opts, "", "", promptBody)
		if err != nil {
			t.Fatalf("buildClaudeArgs error = %v", err)
		}
		defer removeFiles(tempFiles)

		if containsArg(args, "--system-prompt-file") {
			t.Fatalf("project-instruction-only must NOT pass --system-prompt-file, args=%v", args)
		}
		body, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
		if err != nil {
			t.Fatalf("project-instruction-only must still write AGENTS.md: %v", err)
		}
		if !strings.Contains(string(body), promptBody) {
			t.Fatalf("AGENTS.md missing prompt body, got %q", string(body))
		}
	})

	t.Run("project-instruction-only with no working dir: falls back to --system-prompt-file", func(t *testing.T) {
		adapter := NewClaudeCodeInteractiveAdapter("claude-sonnet-4-6", &MockLogger{})
		opts := &llmtypes.CallOptions{}
		// No working dir -> AGENTS.md projection is a no-op, so the flag must
		// not strand the session without a prompt.
		WithProjectInstructionOnly(true)(opts)

		args, tempFiles, err := adapter.buildClaudeArgs(opts, "", "", promptBody)
		if err != nil {
			t.Fatalf("buildClaudeArgs error = %v", err)
		}
		defer removeFiles(tempFiles)

		if argValue(args, "--system-prompt-file") == "" {
			t.Fatalf("must fall back to --system-prompt-file when AGENTS.md projection is skipped, args=%v", args)
		}
	})

	t.Run("project-instruction-only off explicitly behaves like default", func(t *testing.T) {
		adapter := NewClaudeCodeInteractiveAdapter("claude-sonnet-4-6", &MockLogger{})
		dir := t.TempDir()
		opts := &llmtypes.CallOptions{}
		WithWorkingDir(dir)(opts)
		WithProjectInstructionOnly(false)(opts)

		args, tempFiles, err := adapter.buildClaudeArgs(opts, "", "", promptBody)
		if err != nil {
			t.Fatalf("buildClaudeArgs error = %v", err)
		}
		defer removeFiles(tempFiles)

		if argValue(args, "--system-prompt-file") == "" {
			t.Fatalf("explicit false must keep --system-prompt-file, args=%v", args)
		}
	})
}

// A claude that does not read AGENTS.md (or whose version cannot be read) must
// still get the prompt: through --system-prompt-file, with no AGENTS.md carrier.
func TestProjectInstructionOnlyFallsBackToFlagForOldClaude(t *testing.T) {
	const promptBody = "ORCHESTRATOR SYSTEM PROMPT BODY"
	cases := []struct {
		name, version string
		err           error
	}{
		{"old claude 2.1.233", "2.1.233", nil},
		{"unreadable version", "", os.ErrNotExist},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubClaudeVersion(t, tc.version, tc.err)
			adapter := NewClaudeCodeInteractiveAdapter("claude-sonnet-4-6", &MockLogger{})
			dir := t.TempDir()
			opts := &llmtypes.CallOptions{}
			WithWorkingDir(dir)(opts)
			WithProjectInstructionOnly(true)(opts)
			args, tempFiles, err := adapter.buildClaudeArgs(opts, "", "", promptBody)
			if err != nil {
				t.Fatal(err)
			}
			defer removeFiles(tempFiles)
			if argValue(args, "--system-prompt-file") == "" {
				t.Fatalf("the prompt must go through --system-prompt-file for %s, args=%v", tc.name, args)
			}
			if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
				t.Fatalf("AGENTS.md must not be written for a claude that ignores it: %v", err)
			}
		})
	}
}

func TestClaudeVersionAtLeast(t *testing.T) {
	for text, want := range map[string]bool{
		"2.1.285 (Claude Code)": true, "2.1.284": true, "2.1.283": false, "2.1.233": false,
		"2.2.0": true, "3.0.0": true, "2.0.999": false, "garbage": false, "": false,
	} {
		if got := claudeVersionAtLeast(text, claudeMinAgentsMDVersion); got != want {
			t.Errorf("claudeVersionAtLeast(%q) = %v, want %v", text, got, want)
		}
	}
}
