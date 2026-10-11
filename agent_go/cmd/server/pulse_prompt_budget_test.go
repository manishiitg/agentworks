package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPromptBudgetMakesArchitectureDue drives the real worklist path
// (module-state DB, plan.json and step_config.json on disk) across two Pulse
// passes for PLAT-556 decision 2 and the success metric: an over-budget step
// makes Architecture due although Gate skipped it; a completed review of the
// unchanged state is not forced again; every pass records a metric. (The
// Plan Drift deferral of decision 4 is retired: Workflow Review runs before
// runs, PLAT-697 phase 0.)
func TestPromptBudgetMakesArchitectureDue(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	ctx := context.Background()
	ws := "Workflow/budget"
	planning := filepath.Join(root, ws, "planning")
	if err := os.MkdirAll(planning, 0o755); err != nil {
		t.Fatal(err)
	}
	item := []map[string]string{{"id": "work", "type": "user_message", "message": "Do the work."}}
	small := "## Goal\nSmall.\n## Output\nA file.\n## Done when\nWritten."
	plan := map[string]interface{}{"steps": []map[string]interface{}{
		{"id": "big", "type": "agent", "title": "Big", "description": strings.Repeat("Rule text that keeps growing. ", 500), "items": item},
		{"id": "s1", "type": "agent", "title": "S1", "description": small, "items": item},
		{"id": "s2", "type": "agent", "title": "S2", "description": small + " Two.", "items": item},
	}}
	raw, _ := json.Marshal(plan)
	if err := os.WriteFile(filepath.Join(planning, "plan.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planning, "step_config.json"), []byte(`{"steps":[{"id":"big"},{"id":"s1"},{"id":"s2"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := func() []PulseWorklistDecision { return completePulseWorklistDecisions(nil) }
	arch := func(states []PulseModuleState) PulseModuleState {
		for _, s := range states {
			if s.Module == pulseModuleArchitectureReview {
				return s
			}
		}
		t.Fatal("no architecture row")
		return PulseModuleState{}
	}
	evidence := func(s PulseModuleState) string { return strings.Join(s.Evidence, " ") }

	states, err := recordPulseWorklist(ctx, ws, "pulse-1", gate())
	if err != nil {
		t.Fatal(err)
	}
	if a := arch(states); a.LastDecision != "due" || !strings.Contains(evidence(a), "prompt_budget_focus:prompt_design") {
		t.Fatalf("want budget-due Architecture, got %s %v", a.LastDecision, a.Evidence)
	}
	if _, err := markPulseModuleResult(ctx, ws, pulseModuleArchitectureReview, "pulse-1", "done", "Reviewed prompt budget.", nil); err != nil {
		t.Fatal(err)
	}
	states, err = recordPulseWorklist(ctx, ws, "pulse-2", gate())
	if err != nil {
		t.Fatal(err)
	}
	if a := arch(states); strings.Contains(evidence(a), evidencePromptBudgetDue) || !strings.Contains(evidence(a), evidencePromptBudgetReviewed) {
		t.Fatalf("a reviewed, unchanged budget state must not be forced again: %v", a.Evidence)
	}
	metrics, err := getPulsePromptBudgetMetrics(ctx, ws, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 2 || metrics[0].StepsOverBudget != 1 || metrics[0].LargestDescriptionStepID != "big" || metrics[0].ArchitectureRuns != 1 {
		t.Fatalf("want 2 metric rows, newest with 1 over-budget step 'big' and 1 Architecture run, got %+v", metrics)
	}
}
