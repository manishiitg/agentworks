package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// Existing GitHub OAuth/PAT tests now exercise an explicit, caller-owned project.
func githubProjectFixture(t *testing.T) {
	t.Helper()
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	files := map[string]string{}
	for _, person := range []string{"alice", "bob"} {
		folder := "github-" + person
		root := registerTestCrew(t, folder, person, true)
		raw, _ := json.Marshal(map[string]any{"schema_version": 1, "product": "work", "id": "crew-" + folder, "title": folder})
		files[root+"/product.json"] = string(raw)
	}
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: files})
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
}

func githubTestRecord(t *testing.T, person string) *githubAccountRecord {
	t.Helper()
	rec, err := projectGitHubConnection(context.Background(), person, "Crew/github-"+person)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}
