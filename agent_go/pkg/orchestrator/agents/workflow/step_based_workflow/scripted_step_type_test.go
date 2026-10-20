package step_based_workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

// Persisted scripted types round-trip without altering user text.
func TestScriptedStepTypeReadsLegacyAndWritesCanonical(t *testing.T) {
	for _, typ := range []string{"regular", "scripted"} {
		input := `{"steps":[{"id":"owner","type":"agent","items":[],"predefined_routes":[{"route_id":"fetch","sub_agent_step":{"id":"nested","type":"` + typ + `","description":"keep regular in user text"}}]}],"orphan_steps":[{"id":"saved","type":"` + typ + `"}]}`
		var plan PlanningResponse
		if err := json.Unmarshal([]byte(input), &plan); err != nil {
			t.Fatal(err)
		}
		nested := plan.Steps[0].(*AgentPlanStep).PredefinedRoutes[0].SubAgentStep.(*ScriptedPlanStep)
		if nested.Type != StepTypeScripted || plan.OrphanSteps[0].StepType() != StepTypeScripted {
			t.Fatal("saved alias must normalize at the read boundary")
		}
		data, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `"type":"regular"`) || !strings.Contains(string(data), "keep regular in user text") {
			t.Fatalf("write must canonicalize types and preserve user text: %s", data)
		}
	}
}
