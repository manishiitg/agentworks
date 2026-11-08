package server

import (
	"context"
	"net/http"
)

// Laptop settings and tools belong to the current Crew owner.
func laptopProjectAllowed(_ context.Context, caller *UserClaims, root string) bool {
	return caller != nil && userAccessForClaims(caller).CanEdit && isCrewProjectPath(root) &&
		crewProjectOwnedByCaller(caller.UserID, agentProfileRuntimeWorkspace(caller.UserID, root))
}

func (api *StreamingAPI) externalLaptopRoot(w http.ResponseWriter, r *http.Request, args map[string]any) (string, bool) {
	caller := GetUserFromContext(r.Context())
	workspaceID := externalArg(args, "workspace_id")
	// The merged terminal tool passes the same Crew as crew_id.
	if crewID := externalArg(args, "crew_id"); crewID != "" {
		if workspaceID != "" {
			externalError(w, 400, "invalid_arguments", "Pass workspace_id or crew_id for a Crew, not both.")
			return "", false
		}
		workspaceID = crewID
	}
	if workspaceID == "" {
		externalError(w, 400, "invalid_arguments", "Choose a Crew with workspace_id or crew_id.")
		return "", false
	}
	crew, _, ok := api.resolveExternalWorkspace(r.Context(), caller, workspaceID)
	if ok && laptopProjectAllowed(r.Context(), caller, crew.Binding.WorkspacePath) {
		return agentProfileRuntimeWorkspace(caller.UserID, crew.Binding.WorkspacePath), true
	}
	externalError(w, 403, "forbidden", "Choose a Crew you own with this connection.")
	return "", false
}

func (api *StreamingAPI) personalLaptopConnections(caller *UserClaims) map[string]any {
	devices := api.localDeviceListForOwner(caller)
	return map[string]any{"devices": devices, "setup": laptopSetupSteps(len(devices) == 0), "next": "Connect your computer once (the project's Local laptop panel, or ask in chat), then select it with manage_laptop_access in any Crew you own. Each project chooses folders and terminal commands. Every turn of your own chat can use it, automatic ones included; Run people and other people's messages never can."}
}
