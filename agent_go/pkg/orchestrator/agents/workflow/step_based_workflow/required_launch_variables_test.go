package step_based_workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequiredLaunchVariablesFollowSelectedPath(t *testing.T) {
	mode := &RoutingPlanStep{CommonStepFields: CommonStepFields{ID: "mode"}, Routes: []RoutingRoute{{RouteID: "audit", NextStepID: "audit"}, {RouteID: "summary", NextStepID: "summary"}}}
	audit := &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "audit", RequiredVariables: []string{"MONTH"}}, NextStepID: "end"}
	summary := &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "summary"}, NextStepID: "end"}
	steps := []PlanStep{mode, audit, summary}
	if err := validateRequiredLaunchVariables(steps, nil, map[string]string{"mode": "summary"}, 0, false); err != nil {
		t.Fatal(err)
	}
	if err := validateRequiredLaunchVariables(steps, map[string]string{"MONTH": " "}, map[string]string{"mode": "audit"}, 0, false); err == nil || !strings.Contains(err.Error(), "MONTH") {
		t.Fatalf("missing month: %v", err)
	}
	if err := validateRequiredLaunchVariables(steps, map[string]string{"MONTH": "Oct26"}, map[string]string{"mode": "audit"}, 0, false); err != nil {
		t.Fatal(err)
	}
	if err := validateStepRequiredVariables(audit, nil); err == nil {
		t.Fatal("direct execution must enforce the same input")
	}
	updated := mergePartialStepUpdate(audit, PartialPlanStep{RequiredVariables: []string{}})
	if err := validateStepRequiredVariables(updated, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRetiredHumanInputStepFailsWithMigrationAction(t *testing.T) {
	var plan PlanningResponse
	err := json.Unmarshal([]byte(`{"steps":[{"id":"question","type":"human_input","question":"Month?"}]}`), &plan)
	if err == nil || !strings.Contains(err.Error(), "migrate_human_input_steps.py") {
		t.Fatalf("retired saved type: %v", err)
	}
	for _, stepType := range consolidatedStepTypes {
		if stepType == "human_input" {
			t.Fatal("retired step must not be offered to Builder")
		}
	}
}
