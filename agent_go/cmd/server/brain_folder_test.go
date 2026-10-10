package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Brain's notes move from the state folder into the documents tree once (PLAT-633): copied whole, the old folder kept
// aside, and a second start changes nothing. Only administrators reach Brain/ through the Files proxy.
func TestBrainNotesMoveIntoDocumentsOnceAndStayAdminOnly(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "state", "live")
	if err := os.MkdirAll(filepath.Join(old, "project-a", "Latency"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "project-a", "Latency", "notes.md"), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	docs := filepath.Join(root, "docs", brainFolderName)
	for range 2 {
		if err := moveBrainNotesIntoDocuments(old, docs); err != nil {
			t.Fatal(err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(docs, "project-a", "Latency", "notes.md")); err != nil || string(b) != "kept" {
		t.Fatalf("note not moved: %q %v", b, err)
	}
	if moved, _ := filepath.Glob(old + ".moved-*"); len(moved) != 1 {
		t.Fatalf("the old folder must be kept aside once: %v", moved)
	}

	policy := workspaceProxyPolicy{ctx: context.Background(), claims: &UserClaims{UserID: "priya"}}
	if policy.denies("filepath", "Brain/project-a/Latency/notes.md") == "" {
		t.Fatal("a non-admin read Brain's raw files through the Files proxy")
	}
	policy.admin = true
	if policy.denies("filepath", "Brain/project-a/Latency/notes.md") != "" {
		t.Fatal("an administrator must reach Brain's folder")
	}
}
