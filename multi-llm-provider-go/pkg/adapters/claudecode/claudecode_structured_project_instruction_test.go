package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/pkg/projectfile"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The structured transport had no CLAUDE.md projection at all: the prompt went
// out solely through --append-system-prompt, so nothing that reads project
// instructions — a nested claude invocation, a sub-agent, tooling pointed at
// the workspace — saw it, and the live prompt could not be inspected on disk.
// The interactive adapter projected it all along, so the two transports
// disagreed about whether the workspace describes the running agent.
//
// These cover the projection decision only. The argv shape stays owned by
// TestBuildClaudeStructuredArgs.

func structuredOptsWithWorkingDir(dir string) *llmtypes.CallOptions {
	opts := &llmtypes.CallOptions{}
	ensureMetadata(opts)
	opts.Metadata.Custom[MetadataKeyWorkingDir] = dir
	return opts
}

// Unlike project-instruction-only mode on the tmux path, projecting here does
// not replace the flag. --append-system-prompt is the structured carrier and
// the stronger channel, so the file is additive.
func TestStructuredProjectionDoesNotReplaceAppendSystemPrompt(t *testing.T) {
	args, _ := buildClaudeStructuredArgs("sonnet", "be concise", "", "", "", "", "", "sess-1", t.TempDir())

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--append-system-prompt be concise") {
		t.Fatalf("structured transport must still carry the prompt through --append-system-prompt; got: %v", args)
	}
}

func TestStructuredProjectionWritesClaudeMdWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	opts := structuredOptsWithWorkingDir(dir)

	if !writeProjectInstructionFromOptions(opts) {
		t.Fatal("projection must default on when the flag is unset")
	}
	path, err := writeClaudeCodeProjectInstructionFile(dir, "video studio rules")
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	if !projectfile.IsLease(path) {
		t.Fatalf("projection returned %q, want a lease token", path)
	}
	defer removeFiles([]string{path})
	body, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "video studio rules") {
		t.Errorf("projected file must carry the prompt body; got:\n%s", body)
	}
}

// An operator opting out must get no file, since the flag exists to protect a
// hand-authored CLAUDE.md.
func TestStructuredProjectionRespectsOptOut(t *testing.T) {
	dir := t.TempDir()
	opts := structuredOptsWithWorkingDir(dir)
	WithWriteProjectInstructionFile(false)(opts)

	if writeProjectInstructionFromOptions(opts) {
		t.Fatal("WithWriteProjectInstructionFile(false) must disable projection")
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Error("opting out must leave no CLAUDE.md behind")
	}
}

// Cleanup must restore, not delete. The structured turn used a bare os.Remove
// loop, which would have destroyed an operator's CLAUDE.md the first time a
// turn ran in a workspace that had one.
func TestStructuredCleanupRestoresOperatorClaudeMd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	operator := []byte("# operator owned\nnever lose this\n")
	if err := os.WriteFile(path, operator, 0o600); err != nil {
		t.Fatal(err)
	}

	written, err := writeClaudeCodeProjectInstructionFile(dir, "session prompt")
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	if body, _ := os.ReadFile(written); strings.Contains(string(body), "operator owned") {
		t.Fatal("projection should have replaced the operator file for the duration of the turn")
	}

	removeFiles([]string{written})

	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("operator CLAUDE.md was deleted rather than restored: %v", err)
	}
	if string(restored) != string(operator) {
		t.Errorf("operator CLAUDE.md not byte-restored:\ngot:  %q\nwant: %q", restored, operator)
	}
}
