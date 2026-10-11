package step_based_workflow

import (
	"strings"
	"testing"
)

func planStep(id, title, output string, deps []string) PlanStepInterface {
	return &ScriptedPlanStep{
		CommonStepFields: CommonStepFields{
			ID:                  id,
			Title:               title,
			ContextOutput:       FlexibleContextOutput(output),
			ContextDependencies: deps,
		},
	}
}

// A step cannot see the plan's shape from its own description, so it could not
// tell whether it was mid-sequence or final. Position plus the next step's title
// is the minimum needed to judge scope.
func TestBuildPlanPositionSummaryReportsPositionAndNextStep(t *testing.T) {
	steps := []PlanStepInterface{
		planStep("a", "Fetch data", "raw.json", nil),
		planStep("b", "Transform", "clean.json", nil),
		planStep("c", "Report", "report.md", nil),
	}
	got := buildPlanPositionSummary(steps, 1)
	for _, want := range []string{"step 2 of 3", "Runs after you: Report"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "final step") {
		t.Fatalf("mid-sequence step must not be called final:\n%s", got)
	}
}

func TestBuildPlanPositionSummaryMarksFinalStep(t *testing.T) {
	steps := []PlanStepInterface{
		planStep("a", "Fetch data", "raw.json", nil),
		planStep("b", "Report", "report.md", nil),
	}
	got := buildPlanPositionSummary(steps, 1)
	if !strings.Contains(got, "You are the final step") {
		t.Fatalf("last step must be marked final:\n%s", got)
	}
	if strings.Contains(got, "Runs after you") {
		t.Fatalf("final step must not claim a successor:\n%s", got)
	}
}

// Consumers are only claimed when a later step actually declares the dependency.
// Most plans leave context_dependencies empty, and asserting a consumer that the
// plan never declared would be a confident lie about the run's shape.
func TestBuildPlanPositionSummaryOnlyClaimsDeclaredConsumers(t *testing.T) {
	declared := []PlanStepInterface{
		planStep("a", "Fetch data", "raw.json", nil),
		planStep("b", "Transform", "clean.json", []string{"raw.json"}),
	}
	if got := buildPlanPositionSummary(declared, 0); !strings.Contains(got, "consumed by: Transform") {
		t.Fatalf("declared consumer should be listed:\n%s", got)
	}

	undeclared := []PlanStepInterface{
		planStep("a", "Fetch data", "raw.json", nil),
		planStep("b", "Transform", "clean.json", nil),
	}
	if got := buildPlanPositionSummary(undeclared, 0); strings.Contains(got, "consumed by") {
		t.Fatalf("must not invent a consumer when no dependency is declared:\n%s", got)
	}
}

func TestBuildPlanPositionSummaryEmptyOnBadInput(t *testing.T) {
	if got := buildPlanPositionSummary(nil, 0); got != "" {
		t.Fatalf("empty plan should yield no block, got %q", got)
	}
	steps := []PlanStepInterface{planStep("a", "Only", "out.json", nil)}
	if got := buildPlanPositionSummary(steps, 5); got != "" {
		t.Fatalf("out-of-range index should yield no block, got %q", got)
	}
}
