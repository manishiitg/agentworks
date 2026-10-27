package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/spf13/viper"
)

func TestLocalCrewDocumentsLabelKeepsIssuedPaths(t *testing.T) {
	base := t.TempDir()
	docs, project, state := filepath.Join(base, "docs"), filepath.Join(base, "existing-project"), filepath.Join(base, "state")
	for _, dir := range []string{filepath.Join(docs, "Crew", "example"), filepath.Join(project, ".agentworks"), filepath.Join(state, "ownership")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
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
	if _, err := crewroot.EnsurePrivateRoot(docs, "example", filepath.Join(docs, "Crew", "example")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "notes.txt"), []byte("project notes"), 0600); err != nil {
		t.Fatal(err)
	}
	previous := viper.Get("docs-dir")
	t.Cleanup(func() { viper.Set("docs-dir", previous) })
	router := setupRouter(docs)
	response := listDocs(t, router, "Crew/example/_files", "owner")
	var labeled int
	for _, doc := range response.Data {
		if doc.FilePath == "Crew/example/_files" {
			labeled++
			if doc.DisplayName != "existing-project" || doc.Type != "folder" {
				t.Fatalf("wrong root label: %+v", doc)
			}
		} else if doc.DisplayName != "" {
			t.Fatalf("child received a root label: %+v", doc)
		}
	}
	if labeled != 1 {
		t.Fatalf("expected one labeled root, got %d", labeled)
	}
	// The label is presentation only: existing reads still use the issued path.
	resolved, bound, err := crewroot.Resolve(docs, "Crew/example/_files/notes.txt")
	if err != nil || !bound || resolved != filepath.Join(project, "notes.txt") {
		t.Fatalf("file routing changed: %s, %t, %v", resolved, bound, err)
	}
}
