package videoproduct

import (
	"encoding/json"
	"testing"

	sbw "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// The product seeds planning/plan.json directly instead of authoring it through
// the builder's add_*_step tools, so none of the tool-level required-field
// checks ever ran against it. That is how the orchestrator shipped without
// next_step_id: the field is mandatory for todo_task (the tool schema lists it
// as required and validateAgentStepFieldsTyped rejects a step without it),
// but generating raw JSON skipped every one of those gates.
//
// It was not a cosmetic defect. Reading plan.json runs this same validator and
// forgives only a legacy agent "code" item, so the workflow would
// have failed to load rather than run with a missing graph edge.
//
// Running the platform's own exported validator over the generated plan puts
// the product back under the contract every other workflow is held to.
func TestGeneratedPlanSatisfiesThePlatformValidator(t *testing.T) {
	raw, err := json.Marshal(planForAll(pipelineRegistry))
	if err != nil {
		t.Fatal(err)
	}
	var plan sbw.PlanningResponse
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatalf("the generated plan does not even decode as a platform plan: %v", err)
	}
	if err := sbw.ValidatePlanStructure(&plan); err != nil {
		t.Fatalf("generated plan is rejected by the platform validator: %v", err)
	}
}
