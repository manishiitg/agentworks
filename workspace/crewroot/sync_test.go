package crewroot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Showcase, 5 Nov: a Team member's turn in a Mac Crew's relay copy ran as the owner's slot, whose launcher made each
// missing write grant as an empty file, so db/, assets/… showed in Files as files that could not be opened. The
// source-locked Crew's runtime folders are folders before they are granted, and a left-over empty file becomes one.
func TestSourceLockedCrewGrantsRuntimeFoldersAsFolders(t *testing.T) {
	base := t.TempDir()
	docs := filepath.Join(base, "docs")
	state := filepath.Join(base, "state")
	root := filepath.Join(docs, "Crew", "example")
	for _, dir := range []string{root, filepath.Join(state, "ownership")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	if docs, err = filepath.EvalSymlinks(docs); err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(docs, "Crew", "example")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	raw, _ := json.Marshal(map[string]any{"projects": map[string]Record{"work/example": {Product: "work", Folder: "example", Owner: "owner", ID: "stable-id"}}})
	if err = os.WriteFile(filepath.Join(state, "ownership", "projects.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = EnsurePrivateRoot(docs, "stable-id", root); err != nil {
		t.Fatal(err)
	}
	// The relay copy of a Crew edited on its owner's Mac.
	if err = WriteSync("stable-id", SyncBinding{Side: "server", Editing: "local", Mode: "relay", PairID: "pair", PeerProjectID: "mac-project"}); err != nil {
		t.Fatal(err)
	}
	// What the slot launcher left on Showcase.
	if err = os.WriteFile(filepath.Join(root, "assets"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A turn's write grant names the Crew by its virtual root, as the server's turn setup does.
	if grants := SyncWriteGrants(docs, []string{"Crew/example/"}); len(grants) != len(SyncRuntimeDirectories) {
		t.Fatalf("source-locked grants: %v", grants)
	}
	for _, runtime := range SyncRuntimeDirectories {
		if info, err := os.Lstat(filepath.Join(root, runtime)); err != nil || !info.IsDir() {
			t.Fatalf("runtime grant %s is not a folder: %v", runtime, err)
		}
	}
	// Showcase, 6 Nov: a member saving their own copy through Files got 409 "moving online". Team sync owns
	// team-copies/, so the Mac editing lock leaves it writable; the rest of the Crew folder stays locked.
	if err = os.MkdirAll(filepath.Join(root, TeamCopiesFolder, "member"), 0o700); err != nil {
		t.Fatal(err)
	}
	for virtual, allowed := range map[string]bool{"Crew/example/" + TeamCopiesFolder + "/member/merge.txt": true, "Crew/example/notes.md": false} {
		physical, found, err := Resolve(docs, virtual)
		if err != nil || !found {
			t.Fatalf("resolve %s: %v %v", virtual, found, err)
		}
		if err = CheckSyncWrite(docs, physical); (err == nil) != allowed {
			t.Fatalf("write %s while the Mac edits: %v", virtual, err)
		}
	}
	if grants := SyncWriteGrants(docs, []string{"Crew/example/" + TeamCopiesFolder + "/member/"}); len(grants) != 1 {
		t.Fatalf("member copy grant dropped: %v", grants)
	}
}

// PLAT-856-509: a member's copy folder is named by the path-safe form of their id (team-copies/<sanitized id>), but
// the listing matched the raw id, so a member whose id is not path-safe could not see their own copy.
func TestTeamCopyHiddenUsesThePathSafeMemberFolder(t *testing.T) {
	own := "Crew/Example/team-copies/default/notes.md" // the folder of the id "first.last" (not path-safe)
	if TeamCopyHidden(own, "Crew/Example/team-copies/default", "first.last") {
		t.Fatal("a member's own copy is hidden from them")
	}
	if !TeamCopyHidden("Crew/Example/team-copies/other/notes.md", "Crew/Example/team-copies/other", "first.last") {
		t.Fatal("another member's copy is listed")
	}
}
