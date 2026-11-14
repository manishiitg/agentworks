package server

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// PLAT-746-904: a Team sharing only team/ listed 231 "private-looking" files from the whole Crew (other repos, test
// evidence). The skipped list covers only what the Team shares.
func TestCrewTeamSkippedListsOnlySharedFolders(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"team/secrets.json", "team/notes.md", "other-repo/.env", "evidence/secrets-1280.json"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := crewTeamSkipped(root, crewTeamFolder{Folders: []string{"team"}}); !slices.Equal(got, []string{"team/secrets.json"}) {
		t.Fatalf("folder share must skip only its own private-looking files: %v", got)
	}
	if got := crewTeamSkipped(root, crewTeamFolder{Folders: []string{"team", "other-repo", "evidence"}}); len(got) != 3 {
		t.Fatalf("multiple selected folders list their private-looking file: %v", got)
	}
}
