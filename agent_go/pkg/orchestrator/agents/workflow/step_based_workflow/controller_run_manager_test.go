package step_based_workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIsPartialGroupRun(t *testing.T) {
	hcpo := &StepBasedWorkflowOrchestrator{
		variablesManifest: &VariablesManifest{Groups: []VariableGroup{
			{Name: "instagram", Enabled: true},
			{Name: "linkedin", Enabled: true},
		}},
	}

	hcpo.executionOptions = &ExecutionOptions{EnabledGroupNames: []string{"instagram"}}
	if !hcpo.isPartialGroupRun() {
		t.Fatal("one of two enabled groups should reuse the current run")
	}

	hcpo.executionOptions.EnabledGroupNames = []string{"instagram", "linkedin"}
	if hcpo.isPartialGroupRun() {
		t.Fatal("all enabled groups should rotate the current run")
	}
}

func TestRetainedIterationNamesExcludesActiveAndSortsNumerically(t *testing.T) {
	got := retainedIterationNames([]string{"iteration-12", "draft", "iteration-0", "iteration-3", "iteration-2"})
	want := []string{"iteration-2", "iteration-3", "iteration-12"}
	if len(got) != len(want) {
		t.Fatalf("retained iterations = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("retained iterations = %v, want %v", got, want)
		}
	}
}

func TestCategorizedIterationNamesSeparatesBuilderScheduleAndWebhookRuns(t *testing.T) {
	all, builder, scheduled, webhook := categorizedIterationNames([]string{
		"iteration-0", "iteration-9-sched", "iteration-4", "iteration-7-hook",
	})
	if strings.Join(all, ",") != "iteration-4,iteration-7-hook,iteration-9-sched" {
		t.Fatalf("all = %v", all)
	}
	if strings.Join(builder, ",") != "iteration-4" || strings.Join(scheduled, ",") != "iteration-9-sched" || strings.Join(webhook, ",") != "iteration-7-hook" {
		t.Fatalf("categorized = builder:%v scheduled:%v webhook:%v", builder, scheduled, webhook)
	}
}

func TestParseWorkshopIterationNumberAcceptsScheduledRun(t *testing.T) {
	if got := parseWorkshopIterationNumber("iteration-421-sched"); got != 421 {
		t.Fatalf("scheduled iteration number = %d, want 421", got)
	}
}

func TestPlanRevisionForFilesIsCanonicalAndChangesWithContract(t *testing.T) {
	leftPlan, err := canonicalJSONDocument(`{"steps":[{"id":"one","title":"First"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	rightPlan, err := canonicalJSONDocument(`{ "steps": [ { "title": "First", "id": "one" } ] }`)
	if err != nil {
		t.Fatal(err)
	}
	leftID, _, err := planRevisionForFiles(map[string]interface{}{"planning/plan.json": leftPlan})
	if err != nil {
		t.Fatal(err)
	}
	rightID, payload, err := planRevisionForFiles(map[string]interface{}{"planning/plan.json": rightPlan})
	if err != nil {
		t.Fatal(err)
	}
	if leftID != rightID {
		t.Fatalf("semantically identical JSON produced revisions %q and %q", leftID, rightID)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("revision payload is invalid JSON: %v", err)
	}

	changed, _ := canonicalJSONDocument(`{"steps":[{"id":"one","title":"Changed"}]}`)
	changedID, _, err := planRevisionForFiles(map[string]interface{}{"planning/plan.json": changed})
	if err != nil {
		t.Fatal(err)
	}
	if changedID == leftID {
		t.Fatal("behavioral plan change did not change the revision")
	}
}
