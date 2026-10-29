package server

import (
	"os"
	"strings"
)

// projectSharingEnabled says whether a user may see and work in another user's project (a Crew shared
// through the project directory: listed for every account, opened by non-owners as readers). It is on by
// default; AGENTWORKS_PROJECT_SHARING=off makes every project private to its owner, as Code workspaces always
// are. It is a variable so tests can switch it. A Crew's owner can also make one Crew private (crewSharedWithOthers).
// Why: Crew turns run on the app account (2026-10-04), so a reader does not need to work inside the owner's
// slot folder, and Crews listed by list_crews must also open (the decisions log, 2026-10-08).
var projectSharingEnabled = func() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTWORKS_PROJECT_SHARING")), "off")
}

// crewSharedWithOthers says whether users other than its owner may see and use the Crew at root (a Crew/<folder> or
// an owner-tree project path): project sharing is on and the owner has not made the Crew private. The private flag
// is in the server-controlled owner registry, so neither a project file nor an agent turn can change it. A path that
// names no Crew is not shared.
func crewSharedWithOthers(root string) bool {
	if !projectSharingEnabled() {
		return false
	}
	id, ok := projectIdentityOf(root)
	if !ok || !isProjectProfileID(id.Product) {
		return false
	}
	rec, found := defaultProjectOwners().Lookup(id.Product, id.Folder)
	if !found {
		return false
	}
	return !found || !rec.Private
}

// crewSharedWith says whether this account may use the Crew at root as a Run person: the Crew is shared with
// everyone (crewSharedWithOthers) or the owner named the account (run people on a private Crew).
func crewSharedWith(root, userID string) bool {
	if crewSharedWithOthers(root) {
		return true
	}
	// A co-owner sees and builds the Crew whatever its sharing (crew_co_owners.go).
	if crewIsCoOwner(root, userID) {
		return true
	}
	if !projectSharingEnabled() || strings.TrimSpace(userID) == "" {
		return false
	}
	id, ok := projectIdentityOf(root)
	if !ok || !isProjectProfileID(id.Product) {
		return false
	}
	rec, found := defaultProjectOwners().Lookup(id.Product, id.Folder)
	if !found {
		return false
	}
	return containsID(rec.RunPeople, userID)
}
