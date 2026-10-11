package step_based_workflow

import (
	"encoding/json"
	"testing"
)

func TestAllSchemaFunctionsReturnValidJSON(t *testing.T) {
	schemas := map[string]func() string{
		"UpdateScriptedStep":      getUpdateScriptedStepSchema,
		"DeletePlanSteps":         getDeletePlanStepsSchema,
		"AddScriptedStep":         getAddScriptedStepSchema,
		"AddAgentStep":            getAddAgentStepSchema,
		"UpdateAgentStep":         getUpdateAgentStepSchema,
		"AddRoutingStep":          getAddRoutingStepSchema,
		"UpdateRoutingStep":       getUpdateRoutingStepSchema,
		"AddOrchestratorRoute":    getAddAgentRouteSchema,
		"UpdateOrchestratorRoute": getUpdateAgentRouteSchema,
		"DeleteOrchestratorRoute": getDeleteAgentRouteSchema,
		"UpdateValidationSchema":  getUpdateValidationSchemaSchema,
	}
	for name, fn := range schemas {
		var v interface{}
		if err := json.Unmarshal([]byte(fn()), &v); err != nil {
			t.Errorf("%s schema is invalid JSON: %v", name, err)
		}
	}
}
