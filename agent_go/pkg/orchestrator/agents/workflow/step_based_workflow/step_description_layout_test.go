package step_based_workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const layoutDescription = "## Goal\nScore leads.\n\n## Inputs\nleads.json\n\n## Output\nscores.json\n\n## Done when:\nEvery lead has a score."

// A nested agent step out of layout blocks the 1.0.46 stamp and is named; a
// scripted step or a routing step with an empty description never does (the
// layout is for agent steps only).
func TestStepDescriptionLayoutStampRefusedUntilEveryStepIsConverted(t *testing.T) {
	workflowDir := t.TempDir()
	planPath := filepath.Join(workflowDir, PlanningFolderName, "plan.json")
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(nested string) {
		plan := `{"steps":[{"id":"score","type":"regular","description":"Run the scoring script."},{"id":"route-mode","type":"routing","description":"","routing_question":"Which mode?"},
{"id":"lead","type":"orchestrator","description":` + jsonString(layoutDescription) + `,"predefined_routes":[{"route_id":"r1","sub_agent_step":{"id":"research","type":"agent","description":` + jsonString(nested) + `}}]}],
"orphan_steps":[{"id":"old","description":"free text"}]}`
		if err := os.WriteFile(planPath, []byte(plan), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("## Goal\nResearch the lead. Measured on 2026-08-10 it was slow.")
	err := validateStepDescriptionLayoutStamp(StepDescriptionLayoutContractVersion, workflowDir)
	if err == nil || !strings.Contains(err.Error(), "research: missing ## Inputs, ## Output, ## Done when") || strings.Contains(err.Error(), "score") || strings.Contains(err.Error(), "route-mode") || strings.Contains(err.Error(), "old") {
		t.Fatalf("stamp with a nested free-text step = %v, want a refusal naming only research", err)
	}
	if err := validateStepDescriptionLayoutStamp(ManagedDBScriptsContractVersion, workflowDir); err != nil {
		t.Errorf("another version is not gated by this check: %v", err)
	}
	write(layoutDescription)
	if err := validateStepDescriptionLayoutStamp(StepDescriptionLayoutContractVersion, workflowDir); err != nil {
		t.Errorf("a converted workflow must stamp: %v", err)
	}
}

// Plan Drift reports description_layout per pending step with the same rule.
func TestPlanDriftCandidatesCheckDescriptionLayout(t *testing.T) {
	plan := `{"steps":[{"id":"step-a","type":"agent","description":` + jsonString(layoutDescription) + `,"items":[{"id":"x","type":"user_message","message":"Do A."}]},{"id":"step-b","type":"agent","description":"Do the thing well.","items":[{"id":"y","type":"user_message","message":"Do B."}]},{"id":"step-c","type":"routing","description":"","routing_question":"Which?"}]}`
	planDriftCandidateWorkspace(t, "Workflow/layout", plan, "")
	got, err := CollectPlanDriftCandidates(context.Background(), "Workflow/layout")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"step-a": stepDriftCheckStatusPass, "step-b": stepDriftCheckStatusFail, "step-c": stepDriftCheckStatusPass}
	for _, candidate := range got {
		found := false
		for _, check := range candidate.Checks {
			if check.CheckID != descriptionLayoutDriftCheckID {
				continue
			}
			found = true
			if check.Status != want[candidate.StepID] {
				t.Errorf("%s description_layout = %s (%s), want %s", candidate.StepID, check.Status, check.Evidence, want[candidate.StepID])
			}
			if check.Status == stepDriftCheckStatusFail && !strings.Contains(check.Evidence, "## Goal, ## Inputs, ## Output, ## Done when") {
				t.Errorf("fail evidence does not name the missing headings: %s", check.Evidence)
			}
		}
		if !found {
			t.Errorf("%s has no description_layout check", candidate.StepID)
		}
	}
	if len(got) != 3 {
		t.Fatalf("candidates = %d, want 3", len(got))
	}
}

func jsonString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
}
