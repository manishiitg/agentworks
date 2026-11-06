package server

import (
	"context"
	"strings"
)

// Who builds a Crew (owner decision 2026-11-06, replacing the 15 Oct co-owner list). A Crew has ONE main owner (the
// creator, the registry's OwnerID: it never changes, and the Crew's runs, accounts, secrets and schedules are theirs) and,
// while its Team is on, the Team's joined members, who build it with them: change its instructions, skills, functions,
// settings and knowledge. Nothing else makes someone a builder, and with the Team off there is only the main owner.
// "Co-owner" in this code means such a joined member.
//
// Two questions are kept apart on purpose:
//   - PERMISSION ("may this person build here?"): crewProjectOwnedByCaller, crewAccessFor, resolveCrewProjectBinding.
//     True for the main owner and for joined Team members.
//   - IDENTITY ("whose Crew, tree, accounts, browser, mailbox is this?"): crewProjectOwnerID, crewProjectOwnedByCallerStrict.
//     The main owner only.

const (
	crewRoleOwner   = "owner"
	crewRoleCoOwner = "co_owner"
	crewRoleReader  = "reader"
	crewRoleNone    = "none"
)

func crewOwnerRecordAt(root string) (projectOwnerRecord, bool) {
	id, ok := projectIdentityOf(root)
	if !ok || !isProjectProfileID(id.Product) {
		return projectOwnerRecord{}, false
	}
	return defaultProjectOwners().Lookup(id.Product, id.Folder)
}

// crewCoOwnerIDs are the Crew's builders besides its main owner: the joined, active members of its Team while it is on;
// none on a Mac or without a Team. Read on every call, so removing a member or turning the Team off takes it away at once.
func crewCoOwnerIDs(root string) []string {
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

// crewIsCoOwner says whether userID builds the Crew at root as a joined Team member (not the main owner).
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
// Flow co-owner's turn runs as that Flow's owner (flowOwnerTurnOwner). In a Crew everyone is just another person
// (owner, 5 Nov): their chat runs on the models they chose, so for a Crew it is "".
func coOwnerTurnOwner(ctx context.Context, callerID, workspacePath string) string {
	return flowOwnerTurnOwner(ctx, callerID, workspacePath)
}
