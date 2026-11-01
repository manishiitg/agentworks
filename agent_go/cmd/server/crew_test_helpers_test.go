package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// registerTestCrew makes Crew/<folder> a Crew owned by owner in the server's owner registry (call it after pointing
// AGENTWORKS_STATE_ROOT at a temp dir). A Crew starts private; shared=true lets every user of the Crew product read it.
func registerTestCrew(t *testing.T, folder, owner string, shared bool) string {
	t.Helper()
	// Real creation registers the Crew's project ID; the workspace keeps its private records under it (df0a2e472).
	// Owners are cached process-wide for 30s; another test's owner for the same Crew name must not leak in or out.
	resetCrewOwnerCache()
	t.Cleanup(resetCrewOwnerCache)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: folder, OwnerID: owner, ProjectID: "crew-" + folder, Shared: true, Private: !shared}); err != nil {
		t.Fatal(err)
	}
	// Real creation also makes the Crew's folder and its private-record folder; the workspace refuses a Crew
	// whose folders are missing.
	if docs := os.Getenv("WORKSPACE_DOCS_PATH"); docs != "" {
		private, _ := crewroot.PrivateRoot(docs, "crew-"+folder)
		for _, dir := range []string{filepath.Join(docs, "Crew", folder), private} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	return "Crew/" + folder
}

func resetCrewOwnerCache() {
	crewOwners.mu.Lock()
	crewOwners.entries = map[string]crewOwnerEntry{}
	crewOwners.mu.Unlock()
}

func materializeWorkspaceFixtureFile(t *testing.T, workspace *mockWorkspaceAPI, docs, logical string) {
	t.Helper()
	workspace.mu.Lock()
	content, found := workspace.files[logical]
	workspace.mu.Unlock()
	if !found {
		t.Fatal("workspace writer did not save fixture", logical)
	}
	file := filepath.Join(docs, filepath.FromSlash(logical))
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
