package step_based_workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestValidatePlanChangeProvesDeclaredStructuralInvariants(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&RegularPlanStep{
			Type: StepTypeRegular,
			CommonStepFields: CommonStepFields{
				ID:            "producer",
				Title:         "Producer",
				ContextOutput: FlexibleContextOutput("current.json"),
			},
			NextStepID: "consumer",
		},
		&AgentPlanStep{
			Type: StepTypeAgent,
			CommonStepFields: CommonStepFields{
				ID:                  "consumer",
				Title:               "Consumer",
				Description:         "Read the current producer output.",
				ContextDependencies: []string{"current.json"},
			},
			Items:      []AgentItem{{ID: "work", Type: "user_message", Message: "Read and summarize the current output."}},
			NextStepID: "end",
		},
	}}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	readFile := func(_ context.Context, path string) (string, error) {
		switch {
		case strings.HasSuffix(path, "planning/plan.json"):
			return string(planJSON), nil
		case strings.HasSuffix(path, "planning/step_config.json"):
			return `{"steps":[]}`, nil
		default:
			return "", errors.New("not found")
		}
	}
	executor := createValidatePlanChangeExecutor("Workflow/test", readFile)

	resultJSON, err := executor(context.Background(), map[string]interface{}{
		"forbidden_references": []interface{}{"removed-step", "old/nested/output.json"},
		"expected_context_dependencies": map[string]interface{}{
			"consumer": []interface{}{"current.json"},
		},
	})
	if err != nil {
		t.Fatalf("validation failed: %v", err)
	}
	var result struct {
		Passed bool `json:"passed"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Fatalf("expected validation receipt to pass: %s", resultJSON)
	}
}

func TestValidatePlanChangeReportsStaleReferencesAndDependencyDrift(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&AgentPlanStep{
			Type: StepTypeAgent,
			CommonStepFields: CommonStepFields{
				ID:                  "consumer",
				Title:               "Consumer",
				Description:         "Still reads old/nested/output.json from removed-step.",
				ContextDependencies: []string{"old.json"},
			},
			Items:      []AgentItem{{ID: "work", Type: "user_message", Message: "Read and summarize the output."}},
			NextStepID: "end",
		},
	}}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	readFile := func(_ context.Context, path string) (string, error) {
		if strings.HasSuffix(path, "planning/plan.json") {
			return string(planJSON), nil
		}
		return "", errors.New("not found")
	}
	executor := createValidatePlanChangeExecutor("Workflow/test", readFile)

	resultJSON, err := executor(context.Background(), map[string]interface{}{
		"forbidden_references": []interface{}{"removed-step", "old/nested/output.json"},
		"expected_context_dependencies": map[string]interface{}{
			"consumer": []interface{}{"current.json"},
		},
	})
	if err != nil {
		t.Fatalf("validator should return a failing receipt, not a transport error: %v", err)
	}
	for _, want := range []string{
		`"passed":false`,
		`forbidden reference \"removed-step\" remains`,
		`forbidden reference \"old/nested/output.json\" remains`,
		`context_dependencies differ`,
	} {
		if !strings.Contains(resultJSON, want) {
			t.Fatalf("failing receipt missing %q: %s", want, resultJSON)
		}
	}
}
