package knowledgebase

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Brain's folder is edited directly (shell, editor, git pull): Brain's tools see the edit, a new file in a new
// directory, and a removal; .git and other dot-files are never notes.
func TestBrainPicksUpDirectEditsInItsFolder(t *testing.T) {
	s, admin, _ := fixture(t, false)
	ctx := context.Background()
	folder(t, s, admin, "", "Company")
	created := create(t, s, admin, "Company", "about.md", "old\n", "about")
	create(t, s, admin, "Company", "gone.md", "bye\n", "gone")
	write := func(rel, content string) {
		p := filepath.Join(s.live, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("Company/about.md", "edited on disk\n")
	write("Teams/Platform/runbook.md", "new from git pull\n")
	write(".git/HEAD", "ref: refs/heads/main\n")
	if err := os.Remove(filepath.Join(s.live, "Company", "gone.md")); err != nil {
		t.Fatal(err)
	}
	read := call(t, s, admin, "read_knowledgebase", map[string]any{"path": "Company/about.md"})
	if read["content"] != "edited on disk\n" || asMap(read["entry"])["version"] == asMap(created["entry"])["version"] {
		t.Fatalf("the disk edit must be the current version: %v", read)
	}
	if got := call(t, s, admin, "read_knowledgebase", map[string]any{"path": "Teams/Platform/runbook.md"}); got["content"] != "new from git pull\n" {
		t.Fatalf("a new file in a new directory must be a note: %v", got)
	}
	if _, err := s.Call(ctx, admin, "read_knowledgebase", map[string]any{"path": "Company/gone.md"}); err == nil {
		t.Fatal("a removed file must not be readable")
	}
	folders := call(t, s, admin, "list_knowledgebase_folders", map[string]any{"folder_path": "", "depth": 5})
	for _, f := range asSlice(folders["items"]) {
		if p, _ := asMap(f)["path"].(string); p == ".git" {
			t.Fatal(".git must not be a Brain folder")
		}
	}
}
