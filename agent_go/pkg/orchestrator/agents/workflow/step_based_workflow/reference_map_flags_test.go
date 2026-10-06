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

// An unreadable input that was already there at the first look still makes
// Plan Drift due once, so the agent repairs it (Toptal, 2026-10-06).
func TestOldUnreadableInputFlagsPlanDriftOnce(t *testing.T) {
	plan := `{"steps":[
	 {"type":"message_sequence","id":"step-a","description":"A.","context_output":"a.json","validation_schema":{"files":[{"file_name":"a.json"},{"file_name":"selected.json"}]},"items":[{"id":"x","type":"user_message","message":"Do A."}]},
	 {"type":"message_sequence","id":"step-b","description":"B.","context_dependencies":["selected.json"],"items":[{"id":"y","type":"user_message","message":"Do B."}]}]}`
	planDriftCandidateWorkspace(t, "Workflow/strict-old", plan, reviewedStepConfig)
	items, err := CollectPlanDriftDueItems("Workflow/strict-old")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.StepID == "step-b" && strings.Contains(item.Reason, "selected.json") {
			found = true
		}
	}
	if !found {
		t.Fatalf("an old unreadable input must make Plan Drift due for its step, got %#v", items)
	}
}

// A flag file written under older rules is re-evaluated once even when the
// workflow's files did not change, or a new rule never reaches it.
func TestFlagRulesChangeReevaluatesUnchangedWorkflow(t *testing.T) {
	plan := `{"steps":[
	 {"type":"message_sequence","id":"step-a","description":"A.","context_output":"a.json","validation_schema":{"files":[{"file_name":"a.json"},{"file_name":"selected.json"}]},"items":[{"id":"x","type":"user_message","message":"Do A."}]},
	 {"type":"message_sequence","id":"step-b","description":"B.","context_dependencies":["selected.json"],"items":[{"id":"y","type":"user_message","message":"Do B."}]}]}`
	planDriftCandidateWorkspace(t, "Workflow/strict-version", plan, reviewedStepConfig)
	root := filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), "Workflow", "strict-version")
	old := `{"fingerprint":"` + referenceMapFingerprint(root) + `","baseline_breaks":[]}`
	if err := os.WriteFile(filepath.Join(root, PlanningFolderName, referenceMapFlagsFile), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	items, err := CollectPlanDriftDueItems("Workflow/strict-version")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].StepID != "step-b" {
		t.Fatalf("an older flag file must be re-evaluated under the current rules, got %#v", items)
	}
}

// PLAT-629: after the layout upgrade a step whose description loses the layout
// makes Workflow Review due, even when only the description changed; before the
// upgrade it stays silent, and a fixed step that loses the layout again flags again.
func TestLayoutRegressionFlagsWorkflowReviewAfterTheUpgrade(t *testing.T) {
	layout := `## Goal\nG.\n## Inputs\nI.\n## Output\nO.\n## Done when\nD.`
	plan := func(a string) string {
		return `{"steps":[{"id":"step-a","type":"regular","description":"` + a + `"},{"id":"step-b","type":"regular","description":"` + layout + `"}]}`
	}
	planDriftCandidateWorkspace(t, "Workflow/layout", plan("Free text from before."), reviewedStepConfig)
	root := filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), "Workflow", "layout")
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dueFor := func() []string {
		items, err := CollectPlanDriftDueItems("Workflow/layout")
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, item := range items {
			if strings.Contains(item.Reason, "lost the layout") {
				ids = append(ids, item.StepID)
			}
		}
		return ids
	}

	write("workflow.json", `{"version":"1.0.45"}`)
	if ids := dueFor(); len(ids) != 0 {
		t.Fatalf("before the upgrade nothing flags: %v", ids)
	}
	write("workflow.json", `{"version":"1.0.46"}`)
	write("planning/plan.json", plan(layout))
	if ids := dueFor(); len(ids) != 0 {
		t.Fatalf("a converted workflow is clean: %v", ids)
	}
	write("planning/plan.json", plan("Rewritten as free text again."))
	if ids := dueFor(); len(ids) != 1 || ids[0] != "step-a" {
		t.Fatalf("a description-only edit that loses the layout must flag its step: %v", ids)
	}
	if !contractVersionAtLeast("1.0.46", "1.0.46") || contractVersionAtLeast("1.0.45", "1.0.46") || !contractVersionAtLeast("1.1.0", "1.0.46") {
		t.Fatal("contract version comparison")
	}
}
