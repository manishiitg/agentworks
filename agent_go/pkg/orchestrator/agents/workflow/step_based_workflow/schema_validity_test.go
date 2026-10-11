package step_based_workflow

import (
	"encoding/json"
	"testing"
)

func TestAllSchemaFunctionsReturnValidJSON(t *testing.T) {
	schemas := map[string]func() string{
		"UpdateRegularStep":       getUpdateRegularStepSchema,
		"DeletePlanSteps":         getDeletePlanStepsSchema,
		"AddRegularStep":          getAddRegularStepSchema,
		"AddAgentStep":            getAddAgentStepSchema,
		"UpdateAgentStep":         getUpdateAgentStepSchema,
		"AddRoutingStep":          getAddRoutingStepSchema,
		"UpdateRoutingStep":       getUpdateRoutingStepSchema,
		"AddHumanInputStep":       getAddHumanInputStepSchema,
		"AddOrchestratorRoute":    getAddAgentRouteSchema,
		"UpdateOrchestratorRoute": getUpdateAgentRouteSchema,
		"DeleteOrchestratorRoute": getDeleteAgentRouteSchema,
		"UpdateHumanInputStep":    getUpdateHumanInputStepSchema,
		"UpdateValidationSchema":  getUpdateValidationSchemaSchema,
	}
	for name, fn := range schemas {
		var v interface{}
		if err := json.Unmarshal([]byte(fn()), &v); err != nil {
			t.Errorf("%s schema is invalid JSON: %v", name, err)
		}
	}
}
