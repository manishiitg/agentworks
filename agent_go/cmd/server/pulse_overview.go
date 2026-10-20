package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

func pulseOverview(ctx context.Context, workspacePath string, _ time.Time) (map[string]interface{}, error) {
	manifest, _, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil {
		return nil, err
	}
	outward := "ask"
	if manifest != nil && manifest.Pulse != nil {
		outward, _ = normalizePulseOutwardActions(manifest.Pulse.OutwardActions)
	}
	return map[string]interface{}{"success": true, "outward_actions": outward, "has_soul": workflowHasSoul(ctx, workspacePath), "run_setup": workflowRunSetupView(ctx, workspacePath)}, nil
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

func (api *StreamingAPI) handleGetGoalLead(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	path, err := normalizeReportHumanInputWorkspacePath(r.URL.Query().Get("workspace_path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "conversation": api.goalLeadConversationView(r.Context(), path)})
}
