package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPromptBudgetMakesArchitectureDueAndDriftDefersAtMostTwice drives the
// real worklist path (module-state DB, plan.json and step_config.json on
// disk) across four Pulse passes for PLAT-556 decisions 2 and 4 and the
// success metric: an over-budget step makes Architecture due although Gate
// skipped it; a due Plan Drift that flagged the same step defers it twice;
// the third pass runs it scoped away from Drift's steps; a completed review
// of the unchanged state is not forced again; every pass records a metric.
func TestPromptBudgetMakesArchitectureDueAndDriftDefersAtMostTwice(t *testing.T) {
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
		{"id": "big", "type": "message_sequence", "title": "Big", "description": strings.Repeat("Rule text that keeps growing. ", 500), "items": item},
		{"id": "s1", "type": "message_sequence", "title": "S1", "description": small, "items": item},
		{"id": "s2", "type": "message_sequence", "title": "S2", "description": small + " Two.", "items": item},
	}}
	raw, _ := json.Marshal(plan)
	if err := os.WriteFile(filepath.Join(planning, "plan.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planning, "step_config.json"), []byte(`{"steps":[{"id":"big"},{"id":"s1"},{"id":"s2"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := func() []PulseWorklistDecision {
		return completePulseWorklistDecisions(map[string]PulseWorklistDecision{
			pulseModulePlanDriftReview: {Module: pulseModulePlanDriftReview, Due: true, Reason: "Steps lack drift receipts."},
		})
	}
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

	for pass, want := range []string{"architecture_drift_deferrals:1", "architecture_drift_deferrals:2"} {
		states, err := recordPulseWorklist(ctx, ws, "pulse-"+string(rune('1'+pass)), gate())
		if err != nil {
			t.Fatal(err)
		}
		a := arch(states)
		if a.LastDecision != "skipped" || !strings.Contains(evidence(a), want) || !strings.Contains(evidence(a), "prompt_budget_focus:prompt_design") {
			t.Fatalf("pass %d: want deferred budget-due Architecture with %s, got %s %v", pass+1, want, a.LastDecision, a.Evidence)
		}
	}
	states, err := recordPulseWorklist(ctx, ws, "pulse-3", gate())
	if err != nil {
		t.Fatal(err)
	}
	a := arch(states)
	if a.LastDecision != "due" || !strings.Contains(evidence(a), evidenceArchitectureScoped) || !strings.Contains(evidence(a), "architecture_scope_excludes:big,s1,s2") {
		t.Fatalf("third pass must run Architecture scoped away from Drift's steps, got %s %v", a.LastDecision, a.Evidence)
	}
	if !pulseArchitectureScopedDuringDrift(ctx, ws, "pulse-3") {
		t.Fatal("scheduler would not dispatch the scoped Architecture review")
	}
	if _, err := markPulseModuleResult(ctx, ws, pulseModuleArchitectureReview, "pulse-3", "done", "Reviewed prompt budget.", nil); err != nil {
		t.Fatal(err)
	}
	states, err = recordPulseWorklist(ctx, ws, "pulse-4", gate())
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
	if len(metrics) != 4 || metrics[0].StepsOverBudget != 1 || metrics[0].LargestDescriptionStepID != "big" || metrics[0].ArchitectureRuns != 1 {
		t.Fatalf("want 4 metric rows, newest with 1 over-budget step 'big' and 1 Architecture run, got %+v", metrics)
	}
}
