package server

import (
	"os"
	"path/filepath"
	"testing"
)

// #3878: "Move out" renames a folder. A linked worktree inside it keeps its
// repository's absolute path, so a rename would break it; such folders are
// refused and Git has to move them. Plain checkouts move fine.
func TestFileLimitMoveRefusesFoldersHoldingLinkedWorktrees(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string, gitFile bool) {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		if gitFile {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere/.git/worktrees/x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk("data/plain-repo", false)
	mk("work/feature-x", true)
	if got := linkedWorktreeInside(filepath.Join(root, "data")); got != "" {
		t.Fatalf("plain checkout treated as a worktree: %s", got)
	}
	if got := linkedWorktreeInside(filepath.Join(root, "work")); got != filepath.Join(root, "work", "feature-x") {
		t.Fatalf("worktree not found: %q", got)
	}
}
