package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// PLAT-746-809: the one-time import must preserve deletion history, clash
// sides, retry receipts and explicit credential-file sharing, without replay.
func TestCrewTeamLedgerImportsOnceWithoutLosingHistoryOrGrants(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	if err := writeCrewTeam(crewTeam{ProjectID: "import-test", OwnerID: "owner", On: true, Folder: crewTeamFolder{ID: "f1", Whole: true}}); err != nil {
		t.Fatal(err)
	}
	content, err := crewTeamContentDir("import-test")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(content, 0700); err != nil {
		t.Fatal(err)
	}
	file := &crewsync.File{Hash: crewTransferDigest("content"), Size: 7, Executable: true}
	chunks := []transferChunk{{ID: crewTransferDigest("chunk"), Len: 7}}
	before := crewTeamFileState{Revision: 9, Versions: map[string]crewTeamVersion{
		".npmrc":      {File: file, Chunks: chunks, Revision: 8, Author: "owner", ModifiedAt: "2026-11-10T00:00:00Z"},
		"deleted.txt": {Revision: 9, Author: "member", ModifiedAt: "2026-11-10T00:00:01Z"},
	}, Events: []crewTeamEvent{
		{Source: "copy", Path: "deleted.txt", Kind: "write", Revision: 8, Actor: "owner", Author: "owner", At: "2026-11-10T00:00:00Z", File: file, Chunks: chunks},
		{Source: "copy", Path: "deleted.txt", Kind: "delete", Revision: 9, Actor: "member", At: "2026-11-10T00:00:01Z"},
		{Source: "copy", Path: ".npmrc", Kind: "shared", Revision: 9, Actor: "owner", At: "2026-11-10T00:00:02Z", File: file, Chunks: chunks},
	}, Clashes: []crewTeamClash{{Source: "copy", Path: "deleted.txt", WinningRevision: 9, LosingRevision: 8, WinningAuthor: "member", LosingAuthor: "owner", Hunks: 1}},
		Receipts: map[string]crewTeamReceipt{"receipt": {TransferID: "copy/round", Digest: crewTransferDigest("changes"), Revision: 9, Conflicts: []string{"deleted.txt"}}}, OnPurpose: map[string]string{".npmrc": "owner"}}
	raw, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(content, "files.json")
	if err = os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	// An already-locked writer must not try to acquire its own filesystem lock.
	dir, _ := crewTeamDir("import-test")
	unlock, err := wf.LockWorkspace(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	after, err := readCrewTeamFileStateLocked("import-test")
	unlock()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("import lost state: before=%+v after=%+v", before, after)
	}
	// The delta includes a complete revision even beyond the page's path
	// limit, and reshares a file whose own version predates the cursor.
	delta, paths, through, err := readCrewTeamLedgerChanges("import-test", 8, 1)
	if err != nil || through != 9 || len(paths) != 2 || delta.Versions[".npmrc"].Revision != 8 || delta.Versions["deleted.txt"].File != nil || delta.OnPurpose[".npmrc"] != "owner" {
		t.Fatalf("query lost reshare, deletion or whole-revision paging: %+v %v %d %v", delta, paths, through, err)
	}
	current, err := readCrewTeamCurrentState("import-test")
	if err != nil || len(current.Events) != 0 || len(current.Receipts) != 0 || !reflect.DeepEqual(current.Versions, before.Versions) {
		t.Fatal("current-state query lost files or loaded history")
	}
	summary, err := readCrewTeamLedgerSummary("import-test", crewTeamFolder{Whole: true}, false)
	if err != nil || summary.files != 1 || summary.bytes != 7 || summary.last.Path != ".npmrc" || len(summary.clashes) != 1 {
		t.Fatalf("summary differs from saved Team: %+v %v", summary, err)
	}
	if _, err = os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("normal JSON source still exists")
	}
	if backup, err := os.ReadFile(source + ".imported"); err != nil || string(backup) != string(raw) {
		t.Fatal("import backup changed")
	}
	info, err := os.Stat(filepath.Join(dir, "team.sqlite"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("Team ledger is not private")
	}
	if info, err = os.Stat(dir); err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("Team history directory permits slot access")
	}
	// Re-create the interrupted-rename condition after changing the live ledger.
	// The import marker must finish the rename, not replay the older JSON.
	after.Revision = 10
	delete(after.OnPurpose, ".npmrc")
	after.Clashes[0].Resolved = true
	unlock, err = wf.LockWorkspace(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	err = saveCrewTeamFileState("import-test", after)
	unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(source+".imported", source); err != nil {
		t.Fatal(err)
	}
	final, err := readCrewTeamFileState("import-test")
	if err != nil {
		t.Fatal(err)
	}
	if final.Revision != 10 || final.OnPurpose[".npmrc"] != "" || !final.Clashes[0].Resolved || len(final.Events) != 3 {
		t.Fatal("committed history was replayed or a removed grant returned")
	}
	// Failed imports never masquerade as an empty Team or leave an import marker.
	if err = writeCrewTeam(crewTeam{ProjectID: "broken-import", OwnerID: "owner", On: true, Folder: crewTeamFolder{ID: "f1", Whole: true}}); err != nil {
		t.Fatal(err)
	}
	broken, _ := crewTeamContentDir("broken-import")
	if err = os.MkdirAll(broken, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(broken, "files.json"), []byte(`{"revision":`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readCrewTeamFileState("broken-import"); err == nil {
		t.Fatal("malformed import became an empty history")
	}
	if _, err = os.Stat(filepath.Join(broken, "files.json")); err != nil {
		t.Fatal("failed import changed its source")
	}
}
