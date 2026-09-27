package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Goal setup is an automation's initial setup, like a Crew template's setup
// bar: goal -> plan -> metrics, each done in the Builder chat (/setup-goals,
// /design-plan). Every check reads the workflow's real state; nothing is
// self-reported. Goals are optional, so the owner can dismiss the bar, and it
// only shows during initial setup: once the automation has run, it is gone.

const workflowGoalSetupDismissFile = ".goal-setup.json"

type workflowGoalSetupCheck struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Done    bool   `json:"done"`
	Command string `json:"command"` // the chat command that completes it
}

type workflowGoalSetupStatus struct {
	Show      bool                     `json:"show"`
	Complete  bool                     `json:"complete"`
	Dismissed bool                     `json:"dismissed"`
	HasRuns   bool                     `json:"has_runs"`
	Checks    []workflowGoalSetupCheck `json:"checks"`
	Next      *workflowGoalSetupCheck  `json:"next,omitempty"`
}

func workflowGoalSetupReadFile(ctx context.Context, filePath string) (string, error) {
	content, exists, err := readFileFromWorkspace(ctx, filePath)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	return content, nil
}

func buildWorkflowGoalSetupStatus(ctx context.Context, workspacePath string) workflowGoalSetupStatus {
	objective, successCriteria, _ := step.ReadWorkflowObjectiveFromSoul(ctx, workspacePath, workflowGoalSetupReadFile)
	goalDone := step.SoulSectionWritten(objective) && step.SoulSectionWritten(successCriteria)

	planDone := false
	if raw, _ := workflowGoalSetupReadFile(ctx, workspacePath+"/planning/plan.json"); strings.TrimSpace(raw) != "" {
		var plan struct {
			Steps []json.RawMessage `json:"steps"`
		}
		planDone = json.Unmarshal([]byte(raw), &plan) == nil && len(plan.Steps) > 0
	}

	metricsDone := false
	if ledger, err := step.LoadPulseImpactLedger(ctx, workspacePath, 1); err == nil && ledger != nil {
		metricsDone = len(ledger.Metrics) > 0
	}

	checks := []workflowGoalSetupCheck{
		{ID: "goal", Label: "Goal", Done: goalDone, Command: "setup-goals"},
		{ID: "plan", Label: "Plan", Done: planDone, Command: "design-plan"},
		{ID: "metrics", Label: "Metrics", Done: metricsDone, Command: "setup-goals"},
	}
	status := workflowGoalSetupStatus{Checks: checks, Complete: goalDone && planDone && metricsDone}
	for i := range checks {
		if !checks[i].Done {
			next := checks[i]
			status.Next = &next
			break
		}
	}
	if raw, _ := workflowGoalSetupReadFile(ctx, workspacePath+"/"+workflowGoalSetupDismissFile); strings.TrimSpace(raw) != "" {
		status.Dismissed = true
	}
	if runs, err := ReadScheduleRuns(ctx, workspacePath); err == nil && len(runs) > 0 {
		status.HasRuns = true
	}
	status.Show = !status.Complete && !status.Dismissed && !status.HasRuns
	return status
}

// handleWorkflowGoalSetup serves GET (status) and POST (dismiss).
func (api *StreamingAPI) handleWorkflowGoalSetup(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	workspacePath := strings.Trim(strings.TrimSpace(r.URL.Query().Get("workspace_path")), "/")
	if workspacePath == "" {
		http.Error(w, "workspace_path parameter is required", http.StatusBadRequest)
		return
	}
	manifest, exists, err := ReadWorkflowManifest(r.Context(), workspacePath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read manifest: %v", err), http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "No workflow.json found at this workspace", http.StatusNotFound)
		return
	}
	access := workflowAccessForManifest(GetUserFromContext(r.Context()), manifest)
	if access == WorkflowAccessNone {
		writeWorkflowPermissionDenied(w, "read")
		return
	}
	if r.Method == http.MethodPost {
		if access == WorkflowAccessRead {
			writeWorkflowPermissionDenied(w, "write")
			return
		}
		body, _ := json.Marshal(map[string]string{"dismissed_at": time.Now().UTC().Format(time.RFC3339)})
		if err := writeFileToWorkspace(r.Context(), workspacePath+"/"+workflowGoalSetupDismissFile, string(body)+"\n"); err != nil {
			http.Error(w, fmt.Sprintf("Failed to dismiss goal setup: %v", err), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(buildWorkflowGoalSetupStatus(r.Context(), workspacePath))
}
