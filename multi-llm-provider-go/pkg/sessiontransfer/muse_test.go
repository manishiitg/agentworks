package sessiontransfer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Transfers cannot turn journal names or filesystem links into writes outside
// the selected account store, even when the root journal itself is valid.
func TestMuseNativeHandoffRejectsPathsLinksAndForeignSession(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	workspace := t.TempDir()
	row := map[string]any{"stream": map[string]string{"kind": "session", "id": id}, "payload_type": "runtime.session.metadata", "payload": map[string]any{"record": map[string]any{"workspace_root": workspace, "build": map[string]string{"semver": "1.4.14"}}}}
	raw, _ := json.Marshal(row)
	raw = append(raw, '\n')
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	descriptor, _, err := inspectMuseJournal(path, true, id)
	if err != nil {
		t.Fatal(err)
	}
	b := &Bundle{Provider: "muse-cli", Format: "muse-jsonl", Transport: "structured", SessionID: id, Date: "2026/11/01", Workspace: workspace, Root: root, Files: map[string]File{"session.jsonl": descriptor}}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	b.Files["../../auth.json"] = descriptor
	if err := b.Validate(); err == nil {
		t.Fatal("accepted a credential path")
	}
	delete(b.Files, "../../auth.json")
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(raw), id, "22222222-2222-4222-8222-222222222222")), 0600); err != nil {
		t.Fatal(err)
	}
	if err := b.ValidateFiles(); err == nil {
		t.Fatal("accepted a different native session")
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}

	lockHome := t.TempDir()
	if err := ImportMuse(lockHome, workspace, b); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(lockHome, "muse", "sessions", "2026", "11", "01", id, ".session.lock")
	unlock, err := lockMuseSession(lockHome, locked)
	if err != nil {
		t.Fatal(err)
	}
	if err := ImportMuse(lockHome, workspace, b); err == nil || !strings.Contains(err.Error(), "still using this session") {
		unlock()
		t.Fatalf("active writer was not excluded: %v", err)
	}
	unlock()

	home, outside := t.TempDir(), t.TempDir()
	parent := filepath.Join(home, "muse", "sessions", "2026", "11")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(parent, "01")); err != nil {
		t.Fatal(err)
	}
	if err := ImportMuse(home, workspace, b); err == nil {
		t.Fatal("followed a journal directory symlink")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("import wrote outside the store")
	}
}

// PLAT-856-551: a Muse journal from any Muse 1.x moves, and current Muse writes no build version at all (live test,
// 6 Nov: "Muse journal format \"\" is unsupported"); only another major version is refused, naming it.
func TestMuseJournalAcceptsOlderSameMajorVersion(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	for _, c := range []struct {
		semver string
		ok     bool
	}{{"1.3.2", true}, {"1.4.14-R7309.1", true}, {"", true}, {"2.0.0", false}} {
		row := map[string]any{"stream": map[string]string{"kind": "session", "id": id}, "payload_type": "runtime.session.metadata", "payload": map[string]any{"record": map[string]any{"workspace_root": t.TempDir(), "build": map[string]string{"semver": c.semver}}}}
		raw, _ := json.Marshal(row)
		path := filepath.Join(t.TempDir(), "session.jsonl")
		if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
		_, _, err := inspectMuseJournal(path, true, id)
		if c.ok && err != nil || !c.ok && (err == nil || !strings.Contains(err.Error(), c.semver)) { // "": current Muse writes no build version
			t.Fatalf("Muse %s: %v", c.semver, err)
		}
	}
}

// Live round 3, 6 Nov: on a server the CLI runs as another account, and its approval-review folder in the Muse session
// is unreadable to the app; exporting the session must leave that folder out instead of failing the hand back.
func TestExportMuseSkipsAnUnreadableApprovalReviewFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every folder")
	}
	id := "11111111-1111-4111-8111-111111111111"
	workspace := t.TempDir()
	home := t.TempDir()
	dir := filepath.Join(home, "muse", "sessions", "2026", "11", "06", id)
	if err := os.MkdirAll(filepath.Join(dir, "approval-review"), 0o700); err != nil {
		t.Fatal(err)
	}
	row := map[string]any{"stream": map[string]string{"kind": "session", "id": id}, "payload_type": "runtime.session.metadata", "payload": map[string]any{"record": map[string]any{"workspace_root": workspace}}}
	raw, _ := json.Marshal(row)
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "approval-review"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(dir, "approval-review"), 0o700)
	b, err := ExportMuse(home, id, workspace, "structured")
	if err != nil || b.Files["session.jsonl"].Size == 0 {
		t.Fatalf("an unreadable approval-review folder must not fail the export: %v", err)
	}
}

// PLAT-856 final round, 6 Nov: the Mac's own Muse journal ended with its own session.end while the server's copy went on
// from the same point; importing the server's copy was refused as "changed on both". The local session.end alone does
// not make the copies diverge.
func TestImportMuseOverALocalCopyThatOnlyHasItsOwnSessionEnd(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	workspace := t.TempDir()
	row := func(payloadType string, payload map[string]any) string {
		raw, _ := json.Marshal(map[string]any{"stream": map[string]string{"kind": "session", "id": id}, "payload_type": payloadType, "payload": payload})
		return string(raw) + "\n"
	}
	metadata := row("runtime.session.metadata", map[string]any{"record": map[string]any{"workspace_root": workspace, "build": map[string]string{"semver": "1.4.14"}}})
	bundleOf := func(content string) *Bundle {
		root := t.TempDir()
		path := filepath.Join(root, "session.jsonl")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		descriptor, _, err := inspectMuseJournal(path, true, id)
		if err != nil {
			t.Fatal(err)
		}
		return &Bundle{Provider: "muse-cli", Format: "muse-jsonl", Transport: "structured", SessionID: id, Date: "2026/11/01", Workspace: workspace, Root: root, Files: map[string]File{"session.jsonl": descriptor}}
	}
	home := t.TempDir()
	shared := metadata + row("message", map[string]any{"n": 1})
	if err := ImportMuse(home, workspace, bundleOf(shared+row("session.end", nil))); err != nil {
		t.Fatal(err)
	}
	server := bundleOf(shared + row("session.end", map[string]any{"server": true}) + row("message", map[string]any{"n": 2}))
	if err := ImportMuse(home, workspace, server); err != nil {
		t.Fatalf("a local copy that only ends with its own session.end is older than the server's: %v", err)
	}
}
