package step_based_workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestGoalAgentRetiresOnlyLLMChildren(t *testing.T) {
	routes := []PlanOrchestrationRoute{{RouteID: "llm", SubAgentStep: &AgentPlanStep{}}, {RouteID: "python", SubAgentStep: &ScriptedPlanStep{}}}
	before, _ := json.Marshal(routes)
	step := &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "old-manager"}, PredefinedRoutes: routes}
	c := newAgentClosingTestOrchestrator(t)
	_, _, err := c.executeDelegatingAgentStep(t.Context(), step, 0, nil, nil, nil, 0, nil, nil, "")
	if err == nil || !strings.Contains(err.Error(), "main chat") {
		t.Fatalf("old LLM helper must refuse with a conversion next step, got %v", err)
	}
	after, _ := json.Marshal(routes)
	if string(after) != string(before) {
		t.Fatal("runtime refusal changed saved helper instructions")
	}
	if err := validateGoalScriptedRouteAuthoring(routes[1:]); err != nil {
		t.Fatalf("saved script helper was refused: %v", err)
	}
	if goalScriptHelperTool("call_sub_agent") || goalScriptHelperTool("call_generic_agent") || !goalScriptHelperTool("call_scripted_sub_agent") {
		t.Fatal("wrong child tool surface")
	}
}

func TestWorkflowSkillsOptionalConfigDistinguishesReadErrors(t *testing.T) {
	content, err := workflowSkillsConfigForMigration("", fmt.Errorf("read config: %w", os.ErrNotExist))
	if err != nil || content != `{"steps":[]}` {
		t.Fatalf("missing config: %s %v", content, err)
	}
	for _, readErr := range []error{os.ErrPermission, fmt.Errorf("connection refused")} {
		if _, err := workflowSkillsConfigForMigration("", readErr); err == nil {
			t.Fatalf("discarded real read error: %v", readErr)
		}
	}
	for _, invalid := range []string{"", `{broken`} {
		content, err := workflowSkillsConfigForMigration(invalid, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, parseErr := skillsMigrationDocument(content); parseErr == nil {
			t.Fatal("invalid config became empty defaults")
		}
	}
}
