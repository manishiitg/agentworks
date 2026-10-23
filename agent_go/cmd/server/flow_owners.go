package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// Flow co-owners (owner, 2026-10-16). A Flow's owners are the manifest's access.owners list: all of them build it (edit,
// test, publish; workflowAccessForManifest), the main owner (relayBuilder: the creator while still listed, else the first
// listed) is who it runs as, and owners manage the shared owner list. A Flow has one shared draft in version 1 and no
// Builder lock; a publish says whose version it replaced.

// flowOwnersStateFor lists a Flow's owners for callerID.
func flowOwnersStateFor(manifest *WorkflowManifest, callerID string) crewOwnersState {
	dir, _ := readUserDirectoryFile()
	main := manifest.relayBuilder()
	state := crewOwnersState{CanManage: containsID(manifest.effectiveOwners(), callerID), CoOwners: []runSharePerson{}}
	if people := peopleForIDs(dir, []string{main}); len(people) == 1 {
		state.Owner = people[0]
	}
	var others []string
	for _, id := range manifest.effectiveOwners() {
		if id != main {
			others = append(others, id)
		}
	}
	state.CoOwners = peopleForIDs(dir, others)
	return state
}

// changeFlowOwners applies one add or remove by a project owner. It returns the new state, or a refusal (status, code, message).
func (api *StreamingAPI) changeFlowOwners(ctx context.Context, claims *UserClaims, root string, manifest *WorkflowManifest, action, email string) (crewOwnersState, int, string, string) {
	main := manifest.relayBuilder()
	if main == "" || !containsID(manifest.effectiveOwners(), claims.UserID) {
		return crewOwnersState{}, http.StatusForbidden, "forbidden", "Only an owner of this Flow can add or remove owners."
	}
	if email == "" {
		return crewOwnersState{}, http.StatusBadRequest, "invalid_arguments", "Give the person's email."
	}
	dir, _ := readUserDirectoryFile()
	people, unknown := resolveShareEmails(dir, []string{email})
	if len(people) != 1 {
		return crewOwnersState{}, http.StatusNotFound, "not_found", "No active account has the email " + strings.Join(unknown, ", ") + "."
	}
	target := people[0].ID
	state := crewOwnersState{}
	if sanitizeUserIDForPath(target) == sanitizeUserIDForPath(main) {
		if action == "remove" {
			return crewOwnersState{}, http.StatusConflict, "creator", "The main owner of a Flow cannot be removed as an owner."
		}
		return flowOwnersStateFor(manifest, claims.UserID), 0, "", ""
	}
	access := manifest.Access
	if access == nil {
		access = &WorkflowAccess{}
	}
	owners := append([]string(nil), manifest.effectiveOwners()...)
	changed := false
	switch action {
	case "add":
		if !containsID(owners, target) {
			owners = append(owners, target)
			changed = true
		}
	default:
		kept := owners[:0]
		for _, id := range owners {
			if id != target {
				kept = append(kept, id)
			}
		}
		changed = len(kept) != len(owners)
		owners = kept
	}
	if changed {
		// A person holds one tier: an owner is no longer listed to run it only.
		access.Owners = owners
		if action == "add" {
			access.Editors = withoutID(access.Editors, target)
			access.Readers = withoutID(access.Readers, target)
		}
		manifest.Access = access
		if err := WriteWorkflowManifest(ctx, root, manifest); err != nil {
			log.Printf("[FLOW_OWNERS] %s %s on %s failed: %v", action, target, root, err)
			return crewOwnersState{}, http.StatusInternalServerError, "failed", "The owners could not be changed. Try again."
		}
		if action == "remove" {
			api.stopRemovedOwnerSessions(target, root)
		}
	}
	log.Printf("[FLOW_OWNERS] flow=%s %s co_owner=%s by=%s changed=%t", manifest.ID, action, target, claims.UserID, changed)
	state = flowOwnersStateFor(manifest, claims.UserID)
	if action == "add" && !changed {
		state.Note = fmt.Sprintf("%s is already an owner.", people[0].Email)
	}
	return state, 0, "", ""
}

func withoutID(list []string, id string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if v != id {
			out = append(out, v)
		}
	}
	return out
}

// flowOwnerTurnOwner is the main owner of the Flow at workspacePath when callerID is one of its CO-owners, or "": such a
// caller's Builder turn runs as the main owner for provider accounts and token limits (like a Crew co-owner's).
func flowOwnerTurnOwner(ctx context.Context, callerID, workspacePath string) string {
	root := flowRootOfPath(workspacePath)
	if root == "" {
		return ""
	}
	manifest, found, err := ReadWorkflowManifest(ctx, root)
	if err != nil || !found || manifest == nil || manifest.Kind != "relay" {
		return ""
	}
	main := manifest.relayBuilder()
	person := sanitizeUserIDForPath(callerID)
	if main == "" || sanitizeUserIDForPath(main) == person || !containsID(manifest.effectiveOwners(), callerID) {
		return ""
	}
	return main
}

// flowRootOfPath is the Flow folder ("Workflow/<name>") a workspace path is in, or "".
func flowRootOfPath(workspacePath string) string {
	p := strings.Trim(strings.TrimSpace(workspacePath), "/")
	parts := strings.Split(p, "/")
	if len(parts) >= 2 && parts[0] == "Workflow" {
		return parts[0] + "/" + parts[1]
	}
	return ""
}

// relay_owners: the MCP face of the Flow's Owners section (list for anyone who can see the Flow; add and remove for the main owner).
func (api *StreamingAPI) externalRelayOwnersCall(w http.ResponseWriter, r *http.Request, args map[string]any, selected DiscoveredWorkflow) {
	claims := GetUserFromContext(r.Context())
	action, email := externalArg(args, "action"), strings.TrimSpace(externalArg(args, "email"))
	manifest := selected.Manifest
	switch action {
	case "list":
		externalJSON(w, flowOwnersStateFor(manifest, claims.UserID))
	case "add", "remove":
		live, err := validateBuilderGrant(r.Context(), claims, manifest.ID, selected.WorkspacePath)
		if err != nil {
			externalError(w, 403, "builder_not_authorized", err.Error())
			return
		}
		state, status, code, message := api.changeFlowOwners(r.Context(), live, selected.WorkspacePath, manifest, action, email)
		if status != 0 {
			externalError(w, status, code, message)
			return
		}
		externalJSON(w, state)
	default:
		externalError(w, http.StatusBadRequest, "invalid_arguments", "action must be list, add or remove.")
	}
}
