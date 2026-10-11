//go:build linux

package handlers

import (
	"os"
	"path/filepath"
	"testing"
)

// 847-3: collect only assigned slot-owned DBOS journals in this Relay's draft
// and published runs; authored symlinks must not redirect the cleanup.
func TestRelaySlotJournalCleanupTargets(t *testing.T) {
	root := t.TempDir()
	paths := []string{
		filepath.Join(root, "runs", "iteration-1-hook", ".relay_dbos"),
		filepath.Join(root, ".relay_releases", "v1", "runs", "iteration-2-hook", ".relay_dbos"),
	}
	for _, dir := range paths {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "checkpoints.sqlite"), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	uid := uint32(os.Geteuid())
	journals, err := relaySlotJournals(root, map[uint32]string{uid: "slot01"})
	if err != nil || len(journals) != 2 {
		t.Fatalf("draft and published journals: %v %v", journals, err)
	}
	for i, journal := range journals {
		if journal.path != paths[i] || journal.slot != "slot01" {
			t.Fatalf("unexpected cleanup target: %+v", journal)
		}
	}
	if journals, err := relaySlotJournals(root, map[uint32]string{uid + 1: "slot02"}); err != nil || len(journals) != 0 {
		t.Fatalf("unassigned ownership must not select a slot: %v %v", journals, err)
	}
	outside := t.TempDir()
	private := filepath.Join(outside, ".relay_dbos")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, "checkpoints.sqlite"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "runs")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "runs")); err != nil {
		t.Fatal(err)
	}
	if _, err := relaySlotJournals(root, map[uint32]string{uid: "slot01"}); err == nil {
		t.Fatal("accepted a redirected runs root")
	}
	if raw, err := os.ReadFile(filepath.Join(private, "checkpoints.sqlite")); err != nil || string(raw) != "keep" {
		t.Fatalf("foreign journal changed: %q %v", raw, err)
	}
}
