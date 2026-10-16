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

// crewCoOwnerIDs are the co-owners registered for the Crew at root, or nil.
func crewCoOwnerIDs(root string) []string {
	id, ok := projectIdentityOf(root)
	if !ok || id.Product != "work" || !id.Shared {
		return nil
	}
	rec, found := defaultProjectOwners().Lookup("work", id.Folder)
	if !found {
		return nil
	}
	return append([]string(nil), rec.CoOwners...)
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

// coOwnerTurnOwner is the main owner of the Crew at workspacePath when callerID is one of its CO-owners, or "". Such a
// caller's Builder turn runs as that owner for provider accounts and token limits (their cost is billed to the owner;
// the cost record still names the caller).
func coOwnerTurnOwner(ctx context.Context, callerID, workspacePath string) string {
	if owner := flowOwnerTurnOwner(ctx, callerID, workspacePath); owner != "" {
		return owner
	}
	ref, ok := resolveCrewPath(ctx, callerID, workspacePath)
	if !ok || !ref.Shared || ref.OwnerID == "" || ref.OwnerID == sanitizeUserIDForPath(callerID) || !crewIsCoOwner(ref.Root, callerID) {
		return ""
	}
	return ref.OwnerID
}
