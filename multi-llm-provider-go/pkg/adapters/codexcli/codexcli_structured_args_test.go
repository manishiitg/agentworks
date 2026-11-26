package codexcli

import (
	"reflect"
	"testing"
)

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

// TestBuildCodexStructuredArgs pins the resume-vs-fresh argv shape. The
// load-bearing case is resume: `codex exec resume` rejects subcommand-level
// --profile/--sandbox/-C, so those MUST appear as GLOBAL flags BEFORE "exec".
// A refactor that reorders them (or drops the -c sandbox override) silently
// breaks native resume, which this test catches without touching a CLI.
func TestBuildCodexStructuredArgs(t *testing.T) {
	t.Run("fresh turn: exec with subcommand-level flags after exec", func(t *testing.T) {
		got := buildCodexStructuredArgs("", "prof", "workspace-write", "/work", "gpt-5-codex", "", nil, []string{"a=b"}, "hello")
		want := []string{
			"exec", "--json", "--skip-git-repo-check",
			"-C", "/work",
			"--model", "gpt-5-codex",
			"--sandbox", "workspace-write",
			"--profile", "prof",
			"-c", "a=b",
			"hello",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("fresh argv mismatch:\n got=%v\nwant=%v", got, want)
		}
	})

	t.Run("bridge-only: --disable feature flags are GLOBAL, before exec", func(t *testing.T) {
		// resume + disabled features: every --disable must precede exec resume,
		// since they are global (-c features.X=false) flags.
		got := buildCodexStructuredArgs("sess-9", "prof", "read-only", "/work", "codex-cli", "",
			[]string{"shell_tool", "unified_exec", "shell_tool"}, nil, "go")
		execIdx := indexOf(got, "exec")
		disableIdx := indexOf(got, "--disable")
		if disableIdx == -1 || disableIdx > execIdx {
			t.Fatalf("--disable must precede exec: --disable=%d exec=%d (%v)", disableIdx, execIdx, got)
		}
		// deduped: shell_tool appears once as a --disable value.
		n := 0
		for i := 0; i+1 < len(got); i++ {
			if got[i] == "--disable" && got[i+1] == "shell_tool" {
				n++
			}
		}
		if n != 1 {
			t.Errorf("shell_tool should be disabled exactly once (deduped), got %d (%v)", n, got)
		}
		if indexOf(got, "unified_exec") == -1 {
			t.Errorf("expected unified_exec disabled, got %v", got)
		}
		if got[execIdx+1] != "resume" || got[execIdx+2] != "sess-9" {
			t.Errorf("expected exec resume <id> after the global disables, got %v", got[execIdx:])
		}
	})

	t.Run("resume turn: global --profile and -c sandbox precede exec resume", func(t *testing.T) {
		got := buildCodexStructuredArgs("sess-123", "prof", "danger-full-access", "/work", "gpt-5-codex", "", nil, []string{"a=b"}, "hello")
		want := []string{
			"--profile", "prof",
			"-c", `sandbox_mode="danger-full-access"`,
			"exec", "resume", "sess-123", "--json", "--skip-git-repo-check",
			"--model", "gpt-5-codex",
			"-c", "a=b",
			"hello",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("resume argv mismatch:\n got=%v\nwant=%v", got, want)
		}
		// Explicit ordering invariant (independent of the exact slice above):
		// every global flag must come before the exec subcommand.
		execIdx := indexOf(got, "exec")
		if p := indexOf(got, "--profile"); p == -1 || p > execIdx {
			t.Errorf("--profile (%d) must precede exec (%d)", p, execIdx)
		}
		if got[execIdx+1] != "resume" || got[execIdx+2] != "sess-123" {
			t.Errorf("expected `exec resume <id>` sequence, got %v", got[execIdx:execIdx+3])
		}
	})

	t.Run("resume without a session profile: no --profile, still -c sandbox before exec", func(t *testing.T) {
		got := buildCodexStructuredArgs("sess-123", "", "workspace-write", "/work", "codex-cli", "", nil, nil, "hi")
		if indexOf(got, "--profile") != -1 {
			t.Errorf("no profile => no --profile flag, got %v", got)
		}
		cIdx, execIdx := indexOf(got, "-c"), indexOf(got, "exec")
		if cIdx == -1 || cIdx > execIdx {
			t.Errorf("-c sandbox_mode must precede exec: -c=%d exec=%d (%v)", cIdx, execIdx, got)
		}
		// model "codex-cli" is the placeholder id and must be omitted.
		if indexOf(got, "--model") != -1 {
			t.Errorf("placeholder model codex-cli must not add --model, got %v", got)
		}
	})

	t.Run("provider auto approval is global before exec", func(t *testing.T) {
		got := buildCodexStructuredArgs("", "prof", "workspace-write", "/work", "gpt-5-codex", "untrusted", nil, []string{`approvals_reviewer="auto_review"`}, "hello")
		execIdx := indexOf(got, "exec")
		askIdx := indexOf(got, "--ask-for-approval")
		if askIdx == -1 || askIdx > execIdx || got[askIdx+1] != "untrusted" {
			t.Fatalf("approval policy must be global before exec: %v", got)
		}
	})
}
