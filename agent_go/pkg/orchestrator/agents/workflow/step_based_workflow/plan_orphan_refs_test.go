package step_based_workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResolvePlanOrphanStepRefs_ResolvesSharedRouteRef(t *testing.T) {
	const planJSON = `{
		"steps": [
			{
				"type": "todo_task",
				"id": "orchestrator-a",
				"title": "Orchestrator A",
				"description": "Coordinate shared steps.",
				"success_criteria": "All delegated work completes.",
				"context_output": "orchestrator.json",
				"next_step_id": "end",
				"predefined_routes": [
					{
						"route_id": "route-env-check",
						"route_name": "Environment Check",
						"condition": "Use for setup issues.",
						"orphan_step_ref": "shared-env-check"
					}
				]
			}
		],
		"orphan_steps": [
			{
				"type": "regular",
				"id": "shared-env-check",
				"title": "Shared Environment Check",
				"description": "Validate environment prerequisites.",
				"success_criteria": "Environment is ready.",
				"context_output": "env-check.json",
				"shared_with": {
					"orchestrator_ids": ["orchestrator-a"]
				}
			}
		]
	}`

	var plan PlanningResponse
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}
	if err := resolvePlanOrphanStepRefs(&plan); err != nil {
		t.Fatalf("resolvePlanOrphanStepRefs: %v", err)
	}

	orchestratorStep, ok := plan.Steps[0].(*AgentPlanStep)
	if !ok {
		t.Fatalf("expected todo_task step, got %T", plan.Steps[0])
	}
	if len(orchestratorStep.PredefinedRoutes) != 1 {
		t.Fatalf("expected 1 predefined route, got %d", len(orchestratorStep.PredefinedRoutes))
	}
	route := orchestratorStep.PredefinedRoutes[0]
	if route.SubAgentStep == nil {
		t.Fatal("expected orphan_step_ref to resolve into sub_agent_step")
	}
	if got := route.SubAgentStep.GetID(); got != "route-env-check" {
		t.Fatalf("expected resolved sub-agent step id %q, got %q", "route-env-check", got)
	}
	if got := route.SubAgentStep.GetTitle(); got != "Shared Environment Check" {
		t.Fatalf("expected resolved sub-agent step title to preserve orphan title, got %q", got)
	}

	marshaled, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal resolved plan: %v", err)
	}
	marshaledStr := string(marshaled)
	if !strings.Contains(marshaledStr, `"orphan_step_ref":"shared-env-check"`) {
		t.Fatalf("expected marshaled plan to preserve orphan_step_ref, got %s", marshaledStr)
	}
	if strings.Contains(marshaledStr, `"sub_agent_step"`) {
		t.Fatalf("expected marshaled route ref to omit runtime sub_agent_step, got %s", marshaledStr)
	}
}

func TestResolvePlanOrphanStepRefs_RejectsUnsharedRouteRef(t *testing.T) {
	const planJSON = `{
		"steps": [
			{
				"type": "todo_task",
				"id": "orchestrator-a",
				"title": "Orchestrator A",
				"description": "Coordinate shared steps.",
				"success_criteria": "All delegated work completes.",
				"context_output": "orchestrator.json",
				"next_step_id": "end",
				"predefined_routes": [
					{
						"route_id": "route-env-check",
						"route_name": "Environment Check",
						"condition": "Use for setup issues.",
						"orphan_step_ref": "shared-env-check"
					}
				]
			}
		],
		"orphan_steps": [
			{
				"type": "regular",
				"id": "shared-env-check",
				"title": "Shared Environment Check",
				"description": "Validate environment prerequisites.",
				"success_criteria": "Environment is ready.",
				"context_output": "env-check.json",
				"shared_with": {
					"orchestrator_ids": ["other-orchestrator"]
				}
			}
		]
	}`

	var plan PlanningResponse
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}
	if err := resolvePlanOrphanStepRefs(&plan); err == nil {
		t.Fatal("expected resolvePlanOrphanStepRefs to reject unshared orphan_step_ref")
	}
}
