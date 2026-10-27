package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-856-145 review: shell HOME and database write guards must resolve the
// selected folder, without creating generated files in the person's project.
func TestBoundCrewShellPathsKeepHomePrivateAndResolveWrites(t *testing.T) {
	base := t.TempDir()
	docs, project, state := filepath.Join(base, "docs"), filepath.Join(base, "project"), filepath.Join(base, "state")
	for _, p := range []string{filepath.Join(docs, "Crew", "example"), filepath.Join(project, ".agentworks"), filepath.Join(state, "ownership")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	docs, _ = filepath.EvalSymlinks(docs)
	project, _ = filepath.EvalSymlinks(project)
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	raw, _ := json.Marshal(map[string]any{"projects": map[string]crewroot.Record{"work/example": {Product: "work", Folder: "example", Owner: "owner", ID: "example", LocalRoot: project}}})
	if err := os.WriteFile(filepath.Join(state, "ownership", "projects.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	home := interactiveShellHome(docs, []string{"Crew/example/_files"}, project)
	if home != filepath.Join(docs, "Crew", "example", ".sandbox-cache", "home") {
		t.Fatalf("wrong home %s", home)
	}
	if _, err := os.Stat(filepath.Join(project, ".sandbox-cache")); !os.IsNotExist(err) {
		t.Fatal("generated home in chosen project", err)
	}
	candidate := filepath.Join(project, ".agentworks", "reports", "db.sqlite")
	if !pathCoveredByWriteGuard(candidate, docs, []string{"Crew/example/reports"}) {
		t.Fatal("resolved write grant lost")
	}
	if pathCoveredByWriteGuard(candidate, docs, []string{"Crew/example/other"}) {
		t.Fatal("unrelated write grant admitted")
	}
}
