package step_based_workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTodoRouteHasOneDurableContextChannel(t *testing.T) {
	legacy := []byte(`{
		"route_id":"scan",
		"route_name":"Scan",
		"condition":"when due",
		"context_to_pass":"pass everything",
		"sub_agent_step":{
			"type":"agent",
			"id":"scan",
			"title":"Scan",
			"description":"Inspect the declared input.",
			"context_dependencies":["strategy_today.json"],
			"items":[{"type":"user_message","message":"Do the scan."}]
		}
	}`)

	var route PlanOrchestrationRoute
	if err := json.Unmarshal(legacy, &route); err != nil {
		t.Fatalf("unmarshal legacy route: %v", err)
	}
	if got := route.SubAgentStep.GetContextDependencies(); len(got) != 1 || got[0] != "strategy_today.json" {
		t.Fatalf("declared file handoff was not preserved: %v", got)
	}

	encoded, err := json.Marshal(route)
	if err != nil {
		t.Fatalf("marshal route: %v", err)
	}
	if strings.Contains(string(encoded), "context_to_pass") {
		t.Fatalf("dead context channel was persisted: %s", encoded)
	}
	if !strings.Contains(string(encoded), "context_dependencies") {
		t.Fatalf("canonical context channel missing: %s", encoded)
	}
}

func TestTodoRouteMutationSchemasDoNotAdvertiseDeadContextChannel(t *testing.T) {
	for name, schema := range map[string]string{
		"add":    getAddAgentRouteSchema(),
		"update": getUpdateAgentRouteSchema(),
	} {
		if strings.Contains(schema, "context_to_pass") {
			t.Fatalf("%s route schema still advertises context_to_pass", name)
		}
		if !strings.Contains(schema, "context_dependencies") {
			t.Fatalf("%s route schema omits canonical context_dependencies", name)
		}
	}
}
