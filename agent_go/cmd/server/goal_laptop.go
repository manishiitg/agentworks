package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
)

// Laptop settings belong to the project. Goals use their existing Builder
// access; Relays and Run shares do not gain laptop authority.
func laptopProjectAllowed(ctx context.Context, caller *UserClaims, root string) bool {
	if caller == nil || !userAccessForClaims(caller).CanEdit {
		return false
	}
	if isCrewProjectPath(root) {
		return crewProjectOwnedByCaller(caller.UserID, agentProfileRuntimeWorkspace(caller.UserID, root))
	}
	clean, ok := cleanWorkspaceReadPath(root, true)
	if !ok || !strings.HasPrefix(clean, "Workflow/") || !productEnabled("agentworks") || !userAllowedProduct(caller, "agentworks") {
		return false
	}
	level, manifest := workflowAccessForWorkspacePath(ctx, caller, clean)
	if manifest == nil || manifest.Kind == "relay" || !userAllowedWorkflowID(caller, manifest.ID) || (level != WorkflowAccessOwner && level != WorkflowAccessWrite) {
		return false
	}
	return caller.AccessToken == nil || externalFileAuthoringAllowed(caller, manifest)
}

func (api *StreamingAPI) goalLaptopSettingsRoot(w http.ResponseWriter, r *http.Request) (string, bool) {
	caller := GetUserFromContext(r.Context())
	root, valid := cleanWorkspaceReadPath(r.URL.Query().Get("workspace_path"), true)
	if !valid || !strings.HasPrefix(root, "Workflow/") || !websiteDeviceClaims(caller) || !laptopProjectAllowed(r.Context(), caller, root) {
		externalError(w, 403, "forbidden", "Open a Goal you can build with your own website login to manage its laptop access.")
		return "", false
	}
	return root, true
}

func (api *StreamingAPI) handleGoalLaptopSettings(w http.ResponseWriter, r *http.Request) {
	if root, ok := api.goalLaptopSettingsRoot(w, r); ok {
		api.serveCrewLaptopSettings(w, r, root)
	}
}

func (api *StreamingAPI) handleGoalLaptopBrowse(w http.ResponseWriter, r *http.Request) {
	if root, ok := api.goalLaptopSettingsRoot(w, r); ok {
		api.serveLaptopBrowse(w, r, root)
	}
}

func (api *StreamingAPI) externalLaptopRoot(w http.ResponseWriter, r *http.Request, args map[string]any) (string, bool) {
	caller := GetUserFromContext(r.Context())
	workspaceID, workflowID := externalArg(args, "workspace_id"), externalArg(args, "workflow_id")
	if (workspaceID == "") == (workflowID == "") {
		externalError(w, 400, "invalid_arguments", "Pass workspace_id for a Crew or workflow_id for a Goal, exactly one.")
		return "", false
	}
	if workspaceID != "" {
		crew, _, ok := api.resolveExternalWorkspace(r.Context(), caller, workspaceID)
		if ok && laptopProjectAllowed(r.Context(), caller, crew.Binding.WorkspacePath) {
			return agentProfileRuntimeWorkspace(caller.UserID, crew.Binding.WorkspacePath), true
		}
	} else {
		workflows, err := discoverWorkflowManifests(r.Context(), false)
		if err != nil {
			externalError(w, 502, "workspace_unavailable", "Goals could not be loaded.")
			return "", false
		}
		root := ""
		for _, workflow := range workflows {
			if workflow.Manifest != nil && workflow.Manifest.ID == workflowID && laptopProjectAllowed(r.Context(), caller, workflow.WorkspacePath) {
				if root != "" {
					externalError(w, 409, "ambiguous_workflow", "Duplicate Goal IDs must be resolved in AgentWorks.")
					return "", false
				}
				root = workflow.WorkspacePath
			}
		}
		if root != "" {
			return root, true
		}
	}
	externalError(w, 403, "forbidden", "Choose a Goal or Crew you can build with this connection.")
	return "", false
}

// A grant change refreshes the Goal CLI's tools on the next turn, just as a
// changed Crew binding does, without storing folder paths in the policy key.
func (api *StreamingAPI) goalLaptopPolicyKey(caller *UserClaims, root string, readOnly bool) string {
	if caller == nil {
		return ""
	}
	key := api.laptopConnectionPolicyKey(caller, nil, readOnly, root)
	return fmt.Sprintf(":laptop:%x", sha256.Sum256([]byte(key)))
}

// Launch and retained delivery must fingerprint the same server-admitted
// laptop grants. Omitting this suffix cancelled unchanged Builder follow-ups.
func (api *StreamingAPI) workflowChatLaptopPolicyKey(ctx context.Context, req QueryRequest, readOnly bool) string {
	if req.AgentProfileID != "" || !req.admittedWorkflowPhase || req.PhaseID != "workflow-builder" {
		return ""
	}
	return api.goalLaptopPolicyKey(GetUserFromContext(ctx), req.SelectedFolder, readOnly)
}

func (api *StreamingAPI) personalLaptopConnections(caller *UserClaims) map[string]any {
	devices := api.localDeviceListForOwner(caller)
	return map[string]any{"devices": devices, "setup": laptopSetupSteps(len(devices) == 0), "next": "Connect your computer once in My Vault, then select it with manage_laptop_access in any Goal or Crew you build. Each project chooses folders and terminal commands. Steps, schedules and background work never inherit laptop access."}
}
