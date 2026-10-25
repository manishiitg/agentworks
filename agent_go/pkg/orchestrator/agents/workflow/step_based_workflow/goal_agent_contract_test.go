package step_based_workflow

import (
	"fmt"
	"os"
	"testing"
)

func TestGoalAgentRetiresOnlyLLMChildren(t *testing.T) {
	routes := []PlanOrchestrationRoute{{RouteID: "llm", SubAgentStep: &AgentPlanStep{}}, {RouteID: "python", SubAgentStep: &ScriptedPlanStep{}}}
	kept := goalScriptedRoutes(routes)
	if len(kept) != 1 || kept[0].RouteID != "python" || len(routes) != 2 {
		t.Fatal("runtime must retain scripts and leave stored routes unchanged")
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
