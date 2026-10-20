package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// The Share popup's Owners section talks to the dashboards endpoint, like Share itself: owners lists a Crew's owners,
// owner_add and owner_remove (the main owner only, checked in changeCrewOwners) change them. The MCP tool crew_owners
// reaches the same code.
func dashboardOwnersAction(action string) bool {
	return action == "owners" || dashboardOwnersWrite(action)
}

func dashboardOwnersWrite(action string) bool {
	return action == "owner_add" || action == "owner_remove"
}

func (api *StreamingAPI) handleDashboardOwners(w http.ResponseWriter, r *http.Request, claims *UserClaims, target dashboardTarget, action string, emails []string) {
	if target.Kind == "relay" {
		api.handleDashboardFlowOwners(w, r, claims, target, action, emails)
		return
	}
	if target.Kind != "crew" {
		externalError(w, http.StatusNotFound, "not_found", "Co-owners are available for Crews and Flows.")
		return
	}
	ref, ok := resolveCrewPath(r.Context(), claims.UserID, target.Root)
	if !ok || crewAccessFor(claims, ref) != crewAccessOwner {
		externalError(w, http.StatusNotFound, "not_found", "Crew not found or not allowed.")
		return
	}
	crew := crewProjectBinding{OwnerID: ref.OwnerID, OwnedByCaller: true}
	crew.Binding.WorkspacePath = ref.Root
	state := crewOwnersStateFor(crew, claims.UserID)
	if dashboardOwnersWrite(action) {
		if !dashboardAuthoringAllowed(claims, &target) {
			externalError(w, http.StatusForbidden, "insufficient_scope", "Changing owners needs the Crew's main owner and existing authoring consent.")
			return
		}
		verb := strings.TrimPrefix(action, "owner_")
		if len(emails) == 0 {
			externalError(w, http.StatusBadRequest, "invalid_arguments", "Give the person's email.")
			return
		}
		for _, email := range emails {
			next, status, code, message := api.changeCrewOwners(claims, crew, verb, strings.TrimSpace(email))
			if status != 0 {
				externalError(w, status, code, message)
				return
			}
			state = next
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

// stopRemovedOwnerSessions ends the running chats and Builder terminals a removed co-owner has in the Crew at root.
// Their next message is refused anyway (access is read from the owner registry on every request); this also stops what
// is already running, whose folder access was granted when it started.
func (api *StreamingAPI) stopRemovedOwnerSessions(userID, root string) {
	root = canonicalCrewWorkspaceRoot(root)
	person := sanitizeUserIDForPath(userID)
	if api == nil || root == "" || person == "" {
		return
	}
	var ids []string
	api.activeSessionsMux.RLock()
	for id, session := range api.activeSessions {
		if session == nil || sanitizeUserIDForPath(session.UserID) != person {
			continue
		}
		at := canonicalCrewWorkspaceRoot(session.WorkspacePath)
		if at == root || strings.HasPrefix(at, root+"/") {
			ids = append(ids, id)
		}
	}
	api.activeSessionsMux.RUnlock()
	for _, id := range ids {
		log.Printf("[CREW_OWNERS] crew=%s stopping session=%s of removed co_owner=%s", root, id, person)
		api.cancelSessionRuntimeWork(id, "no longer an owner of this Crew", runtimePhaseCanceled)
	}
}

func (api *StreamingAPI) handleDashboardFlowOwners(w http.ResponseWriter, r *http.Request, claims *UserClaims, target dashboardTarget, action string, emails []string) {
	level, manifest := workflowAccessForWorkspacePath(r.Context(), claims, target.Root)
	if manifest == nil || manifest.Kind != "relay" || level != WorkflowAccessOwner {
		externalError(w, http.StatusNotFound, "not_found", "Flow not found or not allowed.")
		return
	}
	state := flowOwnersStateFor(manifest, claims.UserID)
	if dashboardOwnersWrite(action) {
		if !dashboardAuthoringAllowed(claims, &target) {
			externalError(w, http.StatusForbidden, "insufficient_scope", "Changing owners needs the Flow's main owner and existing authoring consent.")
			return
		}
		if len(emails) == 0 {
			externalError(w, http.StatusBadRequest, "invalid_arguments", "Give the person's email.")
			return
		}
		for _, email := range emails {
			next, status, code, message := api.changeFlowOwners(r.Context(), claims, target.Root, manifest, strings.TrimPrefix(action, "owner_"), strings.TrimSpace(email))
			if status != 0 {
				externalError(w, status, code, message)
				return
			}
			state = next
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}
