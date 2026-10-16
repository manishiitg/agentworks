package server

import (
	"encoding/json"
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
	if target.Kind != "crew" {
		externalError(w, http.StatusNotFound, "not_found", "Co-owners are available for Crews.")
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
