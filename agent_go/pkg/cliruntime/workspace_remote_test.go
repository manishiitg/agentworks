package cliruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareForRemoteWorkflowNeedsNoLocalFolder(t *testing.T) {
	state := t.TempDir()
	docs := t.TempDir()
	missing := filepath.Join(docs, "Workflow", "remote")
	if _, err := Prepare(state, docs, "u", missing, "s", "claude-code", "workshop"); err == nil {
		t.Fatal("Prepare must still require the local workflow folder")
	}
	dir, err := PrepareForRemoteWorkflow(state, docs, "u", missing, "s", "claude-code", "workshop")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("runtime dir not created: %v", err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("remote workflow folder must not be created locally")
	}
	// Same identity once the folder exists locally: saved chats keep their runtime.
	if err := os.MkdirAll(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	local, err := Prepare(state, docs, "u", missing, "s", "claude-code", "workshop")
	if err != nil || local != dir {
		t.Fatalf("runtime dir changed between remote and local placement: %q vs %q (%v)", dir, local, err)
	}
	if _, err := PrepareForRemoteWorkflow(state, docs, "u", filepath.Join(docs, "..", "escape"), "s", "claude-code", "workshop"); err == nil {
		t.Fatal("escaping workflow path must be rejected")
	}
}
