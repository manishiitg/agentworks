package server

import "testing"

// registerTestCrew makes Crew/<folder> a Crew owned by owner in the server's owner registry (call it after pointing
// AGENTWORKS_STATE_ROOT at a temp dir). A Crew starts private; shared=true lets every user of the Crew product read it.
func registerTestCrew(t *testing.T, folder, owner string, shared bool) string {
	t.Helper()
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: folder, OwnerID: owner, Shared: true, Private: !shared}); err != nil {
		t.Fatal(err)
	}
	return "Crew/" + folder
}
