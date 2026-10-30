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

// projectMainOwner is who a Goal or Flow runs as and who cannot be removed: a Goal team's leader, else relayBuilder.
func projectMainOwner(manifest *WorkflowManifest) string {
	if manifest.Team != nil && manifest.Team.LeaderID != "" {
		return manifest.Team.LeaderID
	}
	return manifest.relayBuilder()
}

// flowOwnersStateFor lists a Goal's or Flow's owners for callerID (one sharing model, 2026-10-27).
func flowOwnersStateFor(manifest *WorkflowManifest, callerID string) crewOwnersState {
	dir, _ := readUserDirectoryFile()
	main := projectMainOwner(manifest)
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
	main := projectMainOwner(manifest)
	if main == "" || !containsID(manifest.effectiveOwners(), claims.UserID) {
		return crewOwnersState{}, http.StatusForbidden, "forbidden", "Only an owner can add or remove owners."
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
			return crewOwnersState{}, http.StatusConflict, "creator", "The main owner cannot be removed as an owner."
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
			access.Readers = withoutID(access.Readers, target)
		}
		manifest.Access = access
		writeCtx := ctx
		if manifest.Team != nil {
			writeCtx = context.WithValue(ctx, teamManifestPermitKey{}, true)
		}
		if err := WriteWorkflowManifest(writeCtx, root, manifest); err != nil {
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

// projectAccessKind is the dashboards target kind of a Goal ("workflow") or Flow ("relay").
func projectAccessKind(manifest *WorkflowManifest) string {
	if manifest.Kind == "relay" {
		return "relay"
	}
	return "workflow"
}

// projectAccessState is a Goal's or Flow's People: its owners and who can use it (named people or everyone).
func (api *StreamingAPI) projectAccessState(ctx context.Context, claims *UserClaims, root string, manifest *WorkflowManifest) (map[string]any, error) {
	share, err := api.readRunShare(ctx, claims, dashboardTarget{Kind: projectAccessKind(manifest), ID: manifest.ID, Root: root})
	if err != nil {
		return nil, err
	}
	owners := flowOwnersStateFor(manifest, claims.UserID)
	return map[string]any{"owner": owners.Owner, "co_owners": owners.CoOwners, "can_manage": owners.CanManage, "can_use": share.Mode, "people": share.People}, nil
}

// setProjectAccessLists applies complete owner and "Can use" lists from chat or MCP through the Access panel's own
// operations: each owner added or removed with changeFlowOwners, and the named people saved as the Run share.
// Entries may be user IDs, usernames or emails.
func (api *StreamingAPI) setProjectAccessLists(ctx context.Context, claims *UserClaims, root string, manifest *WorkflowManifest, owners, people []string, setOwners, setPeople bool) (map[string]any, error) {
	dir, _ := readUserDirectoryFile()
	if dir == nil {
		return nil, fmt.Errorf("the account directory is unavailable")
	}
	emailOf := func(key string) (string, error) {
		key = strings.TrimSpace(key)
		if rec := dir.find(key, key, key); rec != nil && rec.Email != "" {
			return rec.Email, nil
		}
		return "", fmt.Errorf("no account with an email matches %q", key)
	}
	// Check every email before changing anything, so a refusal never leaves a half-applied change.
	ownerEmails := map[string]bool{}
	if setOwners {
		for _, key := range owners {
			email, err := emailOf(key)
			if err != nil {
				return nil, err
			}
			ownerEmails[strings.ToLower(email)] = true
		}
	}
	peopleEmails := make([]string, 0, len(people))
	if setPeople {
		for _, key := range people {
			email, err := emailOf(key)
			if err != nil {
				return nil, err
			}
			peopleEmails = append(peopleEmails, email)
		}
		// The company-wide choice is its own setting: a people list never turns "everyone" into named people.
		target := dashboardTarget{Kind: projectAccessKind(manifest), ID: manifest.ID, Root: root}
		if current, err := api.readRunShare(ctx, claims, target); err == nil && current.Mode == runShareEveryone {
			return nil, fmt.Errorf("everyone in the company can already use it; to name people instead, first set the company-wide choice to Can't access with the share tool")
		}
	}
	if setOwners {
		main := ""
		if people := peopleForIDs(dir, []string{projectMainOwner(manifest)}); len(people) == 1 {
			main = strings.ToLower(people[0].Email)
		}
		current := map[string]bool{}
		for _, person := range peopleForIDs(dir, manifest.effectiveOwners()) {
			current[strings.ToLower(person.Email)] = true
		}
		for email := range ownerEmails {
			if !current[email] {
				if _, status, _, message := api.changeFlowOwners(ctx, claims, root, manifest, "add", email); status != 0 {
					return nil, fmt.Errorf("%s", message)
				}
			}
		}
		// The main owner (team leader or run-as owner) always stays, so it is never in the removal set.
		for email := range current {
			if email != "" && email != main && !ownerEmails[email] {
				if _, status, _, message := api.changeFlowOwners(ctx, claims, root, manifest, "remove", email); status != 0 {
					return nil, fmt.Errorf("%s", message)
				}
			}
		}
	}
	if setPeople {
		if _, err := api.applyRunShare(ctx, claims, dashboardTarget{Kind: projectAccessKind(manifest), ID: manifest.ID, Root: root}, runSharePeople, peopleEmails, false); err != nil {
			return nil, err
		}
	}
	fresh, found, err := ReadWorkflowManifest(ctx, root)
	if err != nil || !found || fresh == nil {
		return nil, fmt.Errorf("could not read the saved access")
	}
	return api.projectAccessState(ctx, claims, root, fresh)
}

// stringListArg reads a tool's list argument; the bool says whether the caller gave one.
func stringListArg(raw any) ([]string, bool) {
	switch list := raw.(type) {
	case []string:
		return list, true
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out, true
	}
	return nil, false
}
