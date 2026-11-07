package server

import (
	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"net/http"
)

// handleGetGoalMetrics reads saved history for the remaining workflow screen.
// registerWorkflowReadRoutes supplies the existing workflow access check.
func (api *StreamingAPI) handleGetGoalMetrics(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	workspacePath, ok := requireWorkspacePath(w, r)
	if !ok {
		return
	}
	ledger, err := step.LoadGoalMeasurements(r.Context(), workspacePath, 500)
	if err != nil {
		http.Error(w, "Could not read this Goal's measurements", http.StatusInternalServerError)
		return
	}
	writeAIJSON(w, ledger)
}
