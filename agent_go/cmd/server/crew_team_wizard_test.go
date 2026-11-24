package server

import (
	"os"
	"path/filepath"
	"testing"
)

// #3797: a Team folder that a picked online project already has would share that project's own files, and was found
// only after the add failed. The setup now suggests a folder free in every picked project and marks the taken ones.
func TestCrewWizardSuggestsFolderFreeInPickedProjects(t *testing.T) {
	files := t.TempDir()
	if err := os.MkdirAll(filepath.Join(files, "team"), 0o755); err != nil {
		t.Fatal(err)
	}
	picked := []crewWizardPickedRoot{{name: "Support bot", files: files}}
	if got := crewWizardSuggestedFolder("Support Desk", nil); got != "team" {
		t.Fatalf("no projects picked: suggested %q, want team", got)
	}
	if got := crewWizardSuggestedFolder("Support Desk", picked); got != "support-desk-shared" {
		t.Fatalf("picked project has team/: suggested %q, want support-desk-shared", got)
	}
	if crewWizardFolderTaken("team", picked) == "" || crewWizardFolderTaken("docs", picked) != "" {
		t.Fatal("team/ must be marked taken by the picked project and docs/ free")
	}
}
