package codexcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The session prompt is added to AGENTS.md as a marked block; a project's own
// AGENTS.md is kept, overlapping sessions share the block, and the last one to
// finish removes only what it added.
func TestWriteCodexProjectAgentsFileKeepsUsersFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	const own = "# Our rules\nRun make test.\n"
	if err := os.WriteFile(path, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := writeCodexProjectAgentsFile(dir, "PROMPT")
	if err != nil {
		t.Fatal(err)
	}
	second, err := writeCodexProjectAgentsFile(dir, "PROMPT")
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(path); !strings.Contains(string(body), "Run make test.") || !strings.Contains(string(body), "PROMPT") {
		t.Fatalf("file while held: %q", body)
	}
	first()
	first()
	if body, _ := os.ReadFile(path); !strings.Contains(string(body), "PROMPT") {
		t.Fatalf("first session's cleanup removed the running session's prompt: %q", body)
	}
	second()
	if body, _ := os.ReadFile(path); string(body) != own {
		t.Fatalf("user's file changed: %q", body)
	}
}

func TestWriteCodexProjectAgentsFileCreatedThenRemoved(t *testing.T) {
	dir := t.TempDir()
	cleanup, err := writeCodexProjectAgentsFile(dir, "PROMPT")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("file left behind: %v", err)
	}
}

func TestWriteCodexProjectAgentsFileEmptyWorkingDirNoOps(t *testing.T) {
	cleanup, err := writeCodexProjectAgentsFile("", "anything")
	if err != nil {
		t.Errorf("empty workingDir should return nil error; got %v", err)
	}
	if cleanup == nil {
		t.Fatal("cleanup should be non-nil even for empty workingDir (no-op cleanup)")
	}
	cleanup() // must not panic
	if _, err := os.Stat("AGENTS.md"); err == nil {
		t.Errorf("AGENTS.md must NOT be created in process cwd when workingDir is empty")
		_ = os.Remove("AGENTS.md")
	}
}
