package server

import (
	"encoding/json"
	"net/http"
	"strings"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// The run's checks and notices share the same project read grant as its steps.
func (api *StreamingAPI) handleWorkspaceRunEvidence(w http.ResponseWriter, r *http.Request) {
	workspace, valid := cleanWorkspaceReadPath(r.URL.Query().Get("workspace_path"), true)
	claims := GetUserFromContext(r.Context())
	if IsMultiUserMode() && (claims == nil || strings.TrimSpace(claims.UserID) == "") {
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return
	}
	if !valid || len(strings.Split(workspace, "/")) != 2 || !workflowInspectionProductAllowed(claims, workspace) ||
		!workspaceReadAllowed(r.Context(), claims, workspace, logicalPathIsDefaultUser, false) || !workflowStepInspectionWorkspace(r.Context(), workspace) {
		http.Error(w, "Workspace not found", http.StatusNotFound)
		return
	}
	run := r.URL.Query().Get("run_folder")
	if err := stepworkflow.ValidateWorkflowRunFolder(run); err != nil {
		http.Error(w, "Choose a run from this workflow", http.StatusBadRequest)
		return
	}
	items, err := stepworkflow.ReadPythonRunEvidence(stepworkflow.GetPromptDocsRoot(), workspace, run)
	if err != nil {
		http.Error(w, "Run evidence unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": items})
}
