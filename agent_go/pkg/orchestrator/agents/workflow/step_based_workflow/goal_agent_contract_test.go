package step_based_workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestGoalAgentMigrationPreservesScriptsPhasesAndInstructions(t *testing.T) {
	plan := `{"unknown":17,"steps":[{"type":"agent","id":"main","title":"Research","description":"Task","items":[{"id":"phase","type":"user_message","message":"Independent follow up","retained_reason":"Explicit later phase"}],"predefined_routes":[{"route_id":"worker","route_name":"Researcher","condition":"When needed","sub_agent_step":{"type":"agent","id":"worker","description":"Keep this exact instruction"}},{"route_id":"helper","sub_agent_step":{"type":"scripted","id":"helper"}}]}]}`
	cfg := `{"unknown":19,"steps":[{"id":"main","agent_configs":{"learning_objective":"Keep this exact HOW","knowledgebase_contribution":"Keep these exact facts","knowledgebase_access":"none","db_access":"read-write"}}]}`
	if validateGoalAgentContractStamp(plan, cfg) == nil {
		t.Fatal("stamped an unmigrated plan")
	}
	p, c, err := migrateGoalAgentContent(plan, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Keep this exact instruction", "Keep this exact HOW", "Keep these exact facts", "learnings/_global", "knowledgebase/notes", "Independent follow up", "Explicit later phase", "retired_routes"} {
		if !strings.Contains(p, want) {
			t.Fatalf("lost %q", want)
		}
	}
	var saved PlanningResponse
	if err := json.Unmarshal([]byte(p), &saved); err != nil {
		t.Fatal(err)
	}
	agent := saved.Steps[0].(*AgentPlanStep)
	if len(agent.PredefinedRoutes) != 1 || agent.PredefinedRoutes[0].RouteID != "helper" || len(agent.Items) != 1 || len(agent.GoalAgentMigration) == 0 {
		t.Fatalf("wrong migrated shape: %+v", agent)
	}
	if strings.Contains(c, "learning_objective") || strings.Contains(c, "knowledgebase_contribution") || strings.Contains(c, `"learnings_access"`) || !strings.Contains(c, `"knowledgebase_access": "none"`) {
		t.Fatalf("wrong access/config migration: %s", c)
	}
	if err := validateGoalAgentContractStamp(p, c); err != nil {
		t.Fatal(err)
	}
	again, ac, err := migrateGoalAgentContent(p, c)
	if err != nil || !sameAgentJSON([]byte(p), []byte(again)) || !sameAgentJSON([]byte(c), []byte(ac)) {
		t.Fatal("migration is not idempotent", err)
	}
	raw, _ := json.Marshal(agent)
	if !strings.Contains(string(raw), "Keep this exact instruction") {
		t.Fatal("ordinary plan save dropped the migration note")
	}
}

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

func TestGoalAgentOptionalConfigDistinguishesReadErrors(t *testing.T) {
	content, err := goalAgentConfigForMigration("", fmt.Errorf("read config: %w", os.ErrNotExist))
	if err != nil || content != `{"steps":[]}` {
		t.Fatalf("missing config: %s %v", content, err)
	}
	for _, readErr := range []error{os.ErrPermission, fmt.Errorf("connection refused")} {
		if _, err := goalAgentConfigForMigration("", readErr); err == nil {
			t.Fatalf("discarded real read error: %v", readErr)
		}
	}
	for _, invalid := range []string{"", `{broken`} {
		content, err := goalAgentConfigForMigration(invalid, nil)
		if err != nil {
			t.Fatal(err)
		}
		if validateGoalAgentContractStamp(`{"steps":[]}`, content) == nil {
			t.Fatal("invalid config became empty defaults")
		}
	}
}
