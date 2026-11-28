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

// Sharing is three things (#4545): People (co-owners, Users, Teams) work in a project, Bots are agents others use
// without joining, and the dashboard is shared like a Drive file. The dashboard's audience (the registry's Private flag
// and RunPeople) opens its pages and their live data only: never the project, its chats, agents or files.

// crewDashboardOpenToCompany says whether anyone at the company with the link may open the dashboards of the project at
// root (general access "Anyone at the company with the link"): project sharing is on and the owner has not restricted it.
func crewDashboardOpenToCompany(root string) bool {
	if !projectSharingEnabled() {
		return false
	}
	id, ok := projectIdentityOf(root)
	if !ok || !isProjectProfileID(id.Product) {
		return false
	}
	rec, found := defaultProjectOwners().Lookup(id.Product, id.Folder)
	return found && !rec.Private
}

// crewDashboardSharedWith says whether userID may open the dashboards of the project at root: they work in it, or its
// dashboard is open to the company, or the owner added them to the dashboard.
func crewDashboardSharedWith(root, userID string) bool {
	if crewSharedWith(root, userID) || crewDashboardOpenToCompany(root) {
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
	return found && containsID(rec.RunPeople, userID)
}

// crewSharedWith says whether this account works in the Crew at root besides its main owner: a co-owner, a User or a
// person of a Team in its Users (crew_co_owners.go). The dashboard's audience is not project access.
func crewSharedWith(root, userID string) bool {
	return crewIsCoOwner(root, userID)
}
