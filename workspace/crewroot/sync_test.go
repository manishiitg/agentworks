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
}
