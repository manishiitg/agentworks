package server

import (
	"context"
	"strings"
)

// Crew co-owners (owner decision 2026-10-15). A Crew has ONE main owner (the creator, the registry's OwnerID: it never
// changes, and the Crew's runs, accounts, secrets and schedules are theirs) and any number of co-owners who build it
// with them: change its instructions, skills, functions, settings and knowledge, share it, add and remove owners. The
// list lives in the server-controlled owner registry (project_owner_registry.go), so neither a project file nor an
// agent turn can edit it. Only a Crew at the shared Crew/ root can have co-owners.
//
// Two questions are kept apart on purpose:
//   - PERMISSION ("may this person build here?"): crewProjectOwnedByCaller, crewAccessFor, resolveCrewProjectBinding.
//     True for the main owner and for co-owners.
//   - IDENTITY ("whose Crew, tree, accounts, browser, mailbox is this?"): crewProjectOwnerID, crewProjectOwnedByCallerStrict.
//     The main owner only.

const (
	crewRoleOwner   = "owner"
	crewRoleCoOwner = "co_owner"
	crewRoleReader  = "reader"
	crewRoleNone    = "none"
)

// crewCoOwnerIDs are the Crew's co-owners for PERMISSION: those registered for the Crew at root, plus the joined
// members of its Team while the Team is on (owner, 5 Nov, "A Team is a shared room": members build it like co-owners).
// Read on every call, so removing a member or turning the Team off takes it away at once.
func crewCoOwnerIDs(root string) []string {
	ids := crewRegistryCoOwnerIDs(root)
	for _, id := range crewTeamCoOwnerIDs(root) {
		if !registryListHas(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// crewRegistryCoOwnerIDs are only the co-owners registered for the Crew at root (the owner manages this list).
func crewRegistryCoOwnerIDs(root string) []string {
	rec, found := crewOwnerRecordAt(root)
	if !found {
		return nil
	}
	return append([]string(nil), rec.CoOwners...)
}

func crewOwnerRecordAt(root string) (projectOwnerRecord, bool) {
	id, ok := projectIdentityOf(root)
	if !ok || !isProjectProfileID(id.Product) {
		return projectOwnerRecord{}, false
	}
	return defaultProjectOwners().Lookup(id.Product, id.Folder)
}

// crewTeamCoOwnerIDs are the joined, active members of the Crew's Team while it is on; none on a Mac or without a Team.
func crewTeamCoOwnerIDs(root string) []string {
	if IsLocalMode() {
		return nil
	}
	rec, found := crewOwnerRecordAt(root)
	if !found || rec.ProjectID == "" {
		return nil
	}
	t, err := readCrewTeam(rec.ProjectID)
	if err != nil || !t.On || t.OwnerID != rec.OwnerID {
		return nil
	}
	ids := []string{}
	for _, p := range t.People {
		if !p.Invited && p.UserID != t.OwnerID && crewTeamAccountActive(p.UserID) {
			ids = append(ids, p.UserID)
		}
	}
	return ids
}

// crewTeamOnlyMember: the caller builds this Crew as a Team member, not as a registered co-owner.
func crewTeamOnlyMember(root, userID string) bool {
	return registryListHas(crewTeamCoOwnerIDs(root), userID) && !registryListHas(crewRegistryCoOwnerIDs(root), userID)
}

// crewIsCoOwner says whether userID is a registered co-owner (not the main owner) of the Crew at root.
func crewIsCoOwner(root, userID string) bool {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false
	}
	for _, id := range crewCoOwnerIDs(root) {
		if sanitizeUserIDForPath(id) == sanitizeUserIDForPath(userID) {
			return true
		}
	}
	return false
}

// crewRoleOf is the caller's role in the Crew at root.
func crewRoleOf(root, userID string) string {
	if owner, ok := crewProjectOwnerID(root); ok && owner == sanitizeUserIDForPath(userID) {
		return crewRoleOwner
	}
	if crewIsCoOwner(root, userID) {
		return crewRoleCoOwner
	}
	if crewSharedWith(root, userID) {
		return crewRoleReader
	}
	return crewRoleNone
}

// coOwnerTurnOwner is whose provider accounts and token limits a turn uses when it is not the caller's: a Goal or
// Flow co-owner's turn runs as that Flow's owner (flowOwnerTurnOwner). A Crew's co-owners, registered or through its
// Team, run on their own login (owner, 5 Nov: the owner no longer pays for co-owners' turns), so for a Crew it is "".
func coOwnerTurnOwner(ctx context.Context, callerID, workspacePath string) string {
	return flowOwnerTurnOwner(ctx, callerID, workspacePath)
}
