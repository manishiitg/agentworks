package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The session prompt is added to <workdir>/AGENTS.md as a marked block. The
// user's own file is kept, overlapping sessions share the block, and the
// last one to finish removes only what it added.
func TestProjectInstructionFileKeepsUsersFileAndSharesBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	const own = "# Our team rules\nRun make test.\n"
	if err := os.WriteFile(path, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := writeClaudeCodeProjectInstructionFile(dir, "PROMPT")
	if err != nil {
		t.Fatal(err)
	}
	b, err := writeClaudeCodeProjectInstructionFile(dir, "PROMPT")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "Run make test.") || !strings.Contains(string(body), "PROMPT") {
		t.Fatalf("file while held: %q", body)
	}
	removeFiles([]string{a})
	removeFiles([]string{a}) // cleanup can run twice; it must not drop b's hold
	if body, _ := os.ReadFile(path); !strings.Contains(string(body), "PROMPT") {
		t.Fatalf("first session's cleanup removed the running session's prompt: %q", body)
	}
	removeFiles([]string{b})
	if body, _ := os.ReadFile(path); string(body) != own {
		t.Fatalf("user's file changed: %q", body)
	}
}

func TestProjectInstructionFileCreatedThenRemoved(t *testing.T) {
	dir := t.TempDir()
	token, err := writeClaudeCodeProjectInstructionFile(dir, "PROMPT")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	removeFiles([]string{token})
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("file left behind: %v", err)
	}
}

func TestWriteClaudeCodeProjectInstructionFileEmptyWorkingDirNoOps(t *testing.T) {
	path, err := writeClaudeCodeProjectInstructionFile("", "anything")
	if err != nil || path != "" {
		t.Errorf("empty workingDir should no-op; got %q, %v", path, err)
	}
}
