package server

import "testing"

// registerTestCrew makes Crew/<folder> a Crew owned by owner in the server's owner registry (call it after pointing
// AGENTWORKS_STATE_ROOT at a temp dir). A Crew starts private; shared=true lets every user of the Crew product read it.
func registerTestCrew(t *testing.T, folder, owner string, shared bool) string {
	t.Helper()
	// Owners are cached process-wide for 30s; another test's owner for the same Crew name must not leak in or out.
	resetCrewOwnerCache()
	t.Cleanup(resetCrewOwnerCache)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: folder, OwnerID: owner, Shared: true, Private: !shared}); err != nil {
		t.Fatal(err)
	}
	return "Crew/" + folder
}

func resetCrewOwnerCache() {
	crewOwners.mu.Lock()
	crewOwners.entries = map[string]crewOwnerEntry{}
	crewOwners.mu.Unlock()
}
