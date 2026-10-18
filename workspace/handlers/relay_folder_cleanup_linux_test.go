//go:build linux

package handlers

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	"github.com/manishiitg/coding-agent-loop/workspace/workspaceref"
)

// 847-3/6/7: collect provisioned slot-owned DBOS journals in this Relay's draft
// and published runs; authored symlinks must not redirect the cleanup.
func TestRelaySlotJournalCleanupTargets(t *testing.T) {
	docs := t.TempDir()
	root := filepath.Join(docs, "Workflow", "relay-fixture")
	releases := filepath.Join(docs, filepath.FromSlash(workspaceref.RelayReleaseRoot("Workflow/relay-fixture")))
	paths := []string{
		filepath.Join(root, "runs", "iteration-1-hook", ".relay_dbos"),
		filepath.Join(releases, "v1", "runs", "iteration-2-hook", ".relay_dbos"),
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
	slot := slots.Prefix() + "01"
	// No assignment table entry exists after release; the owning OS account
	// still identifies the one deployment slot permitted to remove its journal.
	t.Setenv(slots.EnvTableFile, filepath.Join(t.TempDir(), "unassigned.json"))
	lookup := func(id string) (*user.User, error) {
		if id != strconv.FormatUint(uint64(uid), 10) {
			return nil, fmt.Errorf("unknown UID")
		}
		return &user.User{Uid: id, Username: slot}, nil
	}
	ownerSlot := func(id uint32) string { return relayCleanupSlot(id, lookup) }
	journals, err := relaySlotJournals(root, releases, ownerSlot)
	if err != nil || len(journals) != 2 {
		t.Fatalf("draft and published journals: %v %v", journals, err)
	}
	for i, journal := range journals {
		if journal.path != paths[i] || journal.slot != slot {
			t.Fatalf("unexpected cleanup target: %+v", journal)
		}
	}
	if journals, err := relaySlotJournals(root, releases, func(id uint32) string {
		return relayCleanupSlot(id, func(uid string) (*user.User, error) { return &user.User{Uid: uid, Username: "other-account"}, nil })
	}); err != nil || len(journals) != 0 {
		t.Fatalf("non-slot ownership must not select a slot: %v %v", journals, err)
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
	if _, err := relaySlotJournals(root, releases, ownerSlot); err == nil {
		t.Fatal("accepted a redirected runs root")
	}
	if raw, err := os.ReadFile(filepath.Join(private, "checkpoints.sqlite")); err != nil || string(raw) != "keep" {
		t.Fatalf("foreign journal changed: %q %v", raw, err)
	}
}
