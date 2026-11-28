package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// registerTestCrew makes Crew/<folder> a Crew owned by owner in the server's owner registry (call it after pointing
// AGENTWORKS_STATE_ROOT at a temp dir). A Crew starts private; shared=true lets every user of the Crew product read it.
// The registry names the Crew's project ID and resolution finds a Crew by it, so a test whose product.json carries
// another id passes that id as projectID (default "crew-<folder>").
func registerTestCrew(t *testing.T, folder, owner string, shared bool, projectID ...string) string {
	t.Helper()
	id := "crew-" + folder
	if len(projectID) > 0 {
		id = projectID[0]
	}
	// Real creation registers the Crew's project ID; the workspace keeps its private records under it (df0a2e472).
	// Owners are cached process-wide for 30s; another test's owner for the same Crew name must not leak in or out.
	resetCrewOwnerCache()
	t.Cleanup(resetCrewOwnerCache)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: folder, OwnerID: owner, ProjectID: id, Shared: true, Private: !shared}); err != nil {
		t.Fatal(err)
	}
	// Real creation also makes the Crew's folder and its private-record folder; the workspace refuses a Crew
	// whose folders are missing.
	if docs := os.Getenv("WORKSPACE_DOCS_PATH"); docs != "" {
		private, _ := crewroot.PrivateRoot(docs, id)
		for _, dir := range []string{filepath.Join(docs, "Crew", folder), private} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	return "Crew/" + folder
}

// shareCrewBotForTest opens the project at root in Run mode to people the way the product does it: through a bot of its
// owner (#4545: the dashboard's audience opens its pages only, never the project). A bot admits active accounts with the
// projects product on a project on the server, so people without a directory record get one.
func shareCrewBotForTest(t *testing.T, root string, people ...string) {
	t.Helper()
	rec, ok := crewOwnerRecordAt(root)
	if !ok || rec.ProjectID == "" {
		t.Fatalf("%s needs a registered project id to have a bot", root)
	}
	activateTestAccounts(t, people...)
	if err := writeCrewBot(root, rec.ProjectID+codeChatSideMarker+"test-bot", &crewBot{Maker: rec.OwnerID, People: people}); err != nil {
		t.Fatal(err)
	}
	resetCrewOwnerCache()
}

// stopCrewBotForTest stops the bot shareCrewBotForTest made.
func stopCrewBotForTest(t *testing.T, root string) {
	t.Helper()
	rec, _ := crewOwnerRecordAt(root)
	if err := writeCrewBot(root, rec.ProjectID+codeChatSideMarker+"test-bot", nil); err != nil {
		t.Fatal(err)
	}
	resetCrewOwnerCache()
}

// activateTestAccounts gives each id without a directory record an active creator account (every product).
func activateTestAccounts(t *testing.T, ids ...string) {
	t.Helper()
	dir, _ := readUserDirectoryFile()
	if dir == nil {
		dir = &userDirectory{}
	}
	users := append([]UserRecord{}, dir.Users...)
	for _, id := range ids {
		if dir.byID(id) == nil {
			users = append(users, UserRecord{ID: id, Username: id, Email: id + "@example.com", Role: UserRoleCreator})
		}
	}
	raw, err := json.Marshal(userDirectoryFile{Users: users, AccessRequests: dir.AccessRequests})
	if err != nil {
		t.Fatal(err)
	}
	if store, ok := userDirectoryStore.(*memoryUserDirectory); ok {
		store.content = string(raw)
		invalidateUserDirectoryCache()
		return
	}
	withMemoryUserDirectory(t, string(raw))
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
	file, err := fsutil.WorkspacePath(docs, logical)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

// Seed saved registry data to exercise selection/access rules for existing
// duplicate records. New creation must use registerCrewExecutable and refuse it.
func seedSavedCrewExecutableForTest(t *testing.T, binding crewExecutableBinding) {
	t.Helper()
	crewExecutablesMu.Lock()
	defer crewExecutablesMu.Unlock()
	binding.Root = crewExecutableKey(binding.Root)
	if binding.RuntimeRoot == "" {
		binding.RuntimeRoot = "automations/" + binding.ID
	}
	file, err := crewExecutableFile(binding.Root)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
