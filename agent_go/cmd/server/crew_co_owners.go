package server

import (
	"context"
	"strings"
)

// Who builds a Crew. A Crew has ONE main owner (the creator, the registry's OwnerID: it never changes, and every turn,
// account and schedule runs in their server account) and builders besides them:
//   - a Crew that lives on the server (crewLivesOnServer) has co-owners, a list in the owner registry that only the main
//     owner changes (owner, 21 Nov: server Crews have co-owners and no Team; crew_owners.go);
//   - a Mac Crew's online copy has the joined members of its Team while the Team is on (6 Nov).
//
// Two questions are kept apart on purpose:
//   - PERMISSION ("may this person build here?"): crewProjectOwnedByCaller, crewAccessFor, resolveCrewProjectBinding.
//     True for the main owner and for co-owners.
//   - IDENTITY ("whose Crew, tree, accounts, browser, mailbox is this?"): crewProjectOwnerID, crewProjectOwnedByCallerStrict.
//     The main owner only.

const (
	crewRoleOwner   = "owner"
	crewRoleCoOwner = "co_owner"
	crewRoleUser    = "user"
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

// crewLivesOnServer: the Crew was made on this server, not a Mac Crew's online copy (that one keeps its Team). Such a
// Crew has co-owners and no Team (owner, 21 Nov).
func crewLivesOnServer(projectID string) bool {
	if IsLocalMode() || projectID == "" || crewRelayTwin(projectID) {
		return false
	}
	return ownerOfCrewProjectID(projectID) != ""
}

// crewCoOwnerIDs are everyone with a seat in the Crew besides its main owner, for PERMISSION: a server Crew's
// co-owners and its Users (a User works in the project like a co-owner, with its setup locked: crew_setup_lock.go), or
// the joined, active members of a Mac Crew's Team while it is on; none on a Mac. Read on every call, so a removal takes
// effect at once. Lists of who builds the Crew use crewCoOwnersOnly instead.
func crewCoOwnerIDs(root string) []string {
	if IsLocalMode() {
		return nil
	}
	rec, found := crewOwnerRecordAt(root)
	if !found || rec.ProjectID == "" {
		return nil
	}
	ids := []string{}
	if crewLivesOnServer(rec.ProjectID) {
		for _, id := range append(append(append([]string{}, rec.CoOwners...), rec.Users...), crewProjectTeamUserIDs(rec)...) {
			if id != rec.OwnerID && crewTeamAccountActive(id) && !containsID(ids, id) {
				ids = append(ids, id)
			}
		}
		return ids
	}
	t, err := readCrewTeam(rec.ProjectID)
	if err != nil || !t.On || t.OwnerID != rec.OwnerID {
		return nil
	}
	for _, p := range t.People {
		if !p.Invited && p.UserID != t.OwnerID && crewTeamAccountActive(p.UserID) {
			ids = append(ids, p.UserID)
		}
	}
	return ids
}

// crewCoOwnersOnly are the people who build the Crew besides its main owner and may change everything: crewCoOwnerIDs
// without the Users. The co-owner list, the side panel's names and the Crew vault's owners use it.
func crewCoOwnersOnly(root string) []string {
	users := crewUserIDs(root)
	out := []string{}
	for _, id := range crewCoOwnerIDs(root) {
		if !containsID(users, id) {
			out = append(out, id)
		}
	}
	return out
}

// crewUserIDs are a server Crew's active Users (crew_setup_lock.go).
func crewUserIDs(root string) []string {
	if IsLocalMode() {
		return nil
	}
	rec, found := crewOwnerRecordAt(root)
	if !found || !crewLivesOnServer(rec.ProjectID) {
		return nil
	}
	ids := []string{}
	for _, id := range append(append([]string{}, rec.Users...), crewProjectTeamUserIDs(rec)...) {
		if id != rec.OwnerID && crewTeamAccountActive(id) && !containsID(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// crewProjectTeamUserIDs are the people of the Teams in a server project's Users (#4087): each Team's owner and joined
// members while it is on, read on every call so joining or leaving the Team takes effect at once. The project's owner
// and co-owners keep their own place and are left out.
func crewProjectTeamUserIDs(rec projectOwnerRecord) []string {
	ids := []string{}
	for _, t := range crewTeamsJoinedBy(rec.ProjectID) {
		for _, id := range append([]string{t.OwnerID}, crewTeamJoinedPeople(t)...) {
			if id != "" && id != rec.OwnerID && !containsID(rec.CoOwners, id) && !containsID(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// crewTeamJoinedPeople are a Team's joined people (not open invites).
func crewTeamJoinedPeople(t crewTeam) []string {
	out := []string{}
	for _, p := range t.People {
		if !p.Invited && p.UserID != "" {
			out = append(out, p.UserID)
		}
	}
	return out
}

// crewIsCoOwner says whether userID builds the Crew at root besides its main owner (crewCoOwnerIDs).
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
	if crewIsProjectUser(root, userID) {
		return crewRoleUser
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
