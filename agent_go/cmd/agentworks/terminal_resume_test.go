package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// PLAT-856-569: resuming must never silently replace any differing local
// journal, even if the incoming bytes would be a valid native ancestor.
func TestTerminalResumeKeepsDifferingJournalAndRefusesLinks(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	root := t.TempDir()
	stage := t.TempDir()
	store := t.TempDir()
	raw := []byte(`{"type":"user","sessionId":"` + id + `","message":{"role":"user","content":"Hello"}}` + "\n")
	sum := sha256.Sum256(raw)
	if e := os.WriteFile(filepath.Join(stage, "session.jsonl"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	b := &sessiontransfer.Bundle{Provider: "claude-code", Format: "claude-jsonl", Transport: "structured", SessionID: id, Workspace: root, Root: stage, Files: map[string]sessiontransfer.File{"session.jsonl": {Size: int64(len(raw)), SHA256: hex.EncodeToString(sum[:])}}}
	dest, e := installTerminalJournal(store, root, b)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = installTerminalJournal(store, root, b); e != nil {
		t.Fatal("identical journal must be reusable", e)
	}
	private := []byte("an unrelated local conversation")
	if e = os.WriteFile(dest, private, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = installTerminalJournal(store, root, b); e == nil {
		t.Fatal("differing local journal replaced")
	}
	got, _ := os.ReadFile(dest)
	if string(got) != string(private) {
		t.Fatal("local conversation was overwritten")
	}
	linked := t.TempDir()
	if e = os.Symlink(store, filepath.Join(linked, "projects")); e != nil {
		t.Fatal(e)
	}
	if _, e = installTerminalJournal(linked, root, b); e == nil {
		t.Fatal("linked provider path accepted")
	}
}
