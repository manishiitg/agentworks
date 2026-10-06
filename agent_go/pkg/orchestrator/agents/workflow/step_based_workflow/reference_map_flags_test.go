package step_based_workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Pins the PLAT-565 decisions: a break that appears after the first look makes
// Plan Drift due whatever wrote the file; a break that was already there does
// not (it would block Technical and Architecture forever); a drift review
// recorded afterwards clears the flag.
func TestNewReferenceBreakMakesPlanDriftDueUntilReviewed(t *testing.T) {
	planDriftCandidateWorkspace(t, "Workflow/ref-flags", twoStepPlan, reviewedStepConfig)
	root := filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), "Workflow", "ref-flags")
	writeNote := func(text string) {
		path := filepath.Join(root, "knowledgebase", "notes", "flow.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	due := func() []PlanDriftDueItem {
		items, err := CollectPlanDriftDueItems("Workflow/ref-flags")
		if err != nil {
			t.Fatal(err)
		}
		return items
	}

	writeNote("Older note: see code/old-step/main.py.") // already broken at the first look
	if items := due(); len(items) != 0 {
		t.Fatalf("a break present at the first look must not make Drift due, got %#v", items)
	}

	writeNote("Older note: see code/old-step/main.py and code/gone-step/run.py.") // a new break, written by anything
	items := due()
	if len(items) != 1 || items[0].StepID != WorkflowDriftReviewStepID || !strings.Contains(items[0].Reason, "code/gone-step/run.py") {
		t.Fatalf("new break must flag the workflow for review, got %#v", items)
	}
	if got, err := CollectPlanDriftCandidates(t.Context(), "Workflow/ref-flags"); err != nil || len(got) != 1 || got[0].StepID != WorkflowDriftReviewStepID {
		t.Fatalf("candidates must agree with the due items: %#v, %v", got, err)
	}

	reviewed := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	config := strings.TrimSuffix(strings.TrimSpace(reviewedStepConfig), "]}") +
		`,{"id":"__workflow_drift_review__","agent_configs":{"drift_review":{"reviewed_at":"` + reviewed + `","reviewed_by":"pulse:plan_drift_review","contract_version":3,"checks":[]}}}]}`
	if err := os.WriteFile(filepath.Join(root, PlanningFolderName, "step_config.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if items := due(); len(items) != 0 {
		t.Fatalf("a review recorded after the flag must clear it, got %#v", items)
	}
}
