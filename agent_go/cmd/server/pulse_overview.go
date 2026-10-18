package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// The page projects recorded agent work. It never judges a change's effect or
// starts a Pulse turn; OFF still permits reading existing reports and decisions.
func pulseOverview(ctx context.Context, workspacePath string, now time.Time) (map[string]interface{}, error) {
	goal, err := computeGoalStatus(ctx, workspacePath, now)
	if err != nil {
		return nil, err
	}
	work, err := listPulseGoalWork(ctx, workspacePath, 100)
	if err != nil {
		return nil, err
	}
	var lastChange *PulseGoalWorkItem
	for i := range work {
		item := &work[i]
		if item.Kind == "goal_work" && item.Status == "done" && item.ActionTaken != "" && (lastChange == nil || item.UpdatedAt > lastChange.UpdatedAt) {
			lastChange = item
		}
	}
	impact, err := step.LoadPulseImpactLedger(ctx, workspacePath, 500)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"success": true, "impact": impact, "goal_status": goal, "last_change": lastChange,
		"next_pulse":      pulseNextRunView(ctx, workspacePath),
		"outward_actions": pulseOutwardActionsForView(ctx, workspacePath),
		"has_soul":        workflowHasSoul(ctx, workspacePath), "run_setup": workflowRunSetupView(ctx, workspacePath),
	}, nil
}

func (api *StreamingAPI) handleGetPulseOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	workspace, err := normalizeReportHumanInputWorkspacePath(r.URL.Query().Get("workspace_path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	page, err := pulseOverview(r.Context(), workspace, time.Now().UTC())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}
