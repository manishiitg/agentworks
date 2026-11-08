package step_based_workflow

import (
	"strings"
	"testing"
)

func TestValidateAndResolveScriptParametersAppliesDefaults(t *testing.T) {
	step := &ScriptedPlanStep{CommonStepFields: CommonStepFields{
		ID: "collector",
		ScriptParameters: map[string]ScriptParameterDefinition{
			"market": {Type: "string", Description: "Market", Required: true, Enum: []interface{}{"india", "dubai"}},
			"limit":  {Type: "integer", Description: "Maximum rows", Default: float64(25)},
		},
	}}
	resolved, err := validateAndResolveScriptParameters(step, map[string]interface{}{"market": "dubai"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved["market"] != "dubai" || resolved["limit"] != float64(25) {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestValidateAndResolveScriptParametersRejectsBadCalls(t *testing.T) {
	step := &ScriptedPlanStep{CommonStepFields: CommonStepFields{
		ID: "collector",
		ScriptParameters: map[string]ScriptParameterDefinition{
			"market": {Type: "string", Description: "Market", Required: true},
		},
	}}
	for _, test := range []struct {
		name   string
		values map[string]interface{}
		want   string
	}{
		{name: "missing", values: nil, want: "missing required parameter"},
		{name: "unknown", values: map[string]interface{}{"region": "dubai"}, want: "unknown parameter"},
		{name: "wrong type", values: map[string]interface{}{"market": float64(3)}, want: "must be string"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := validateAndResolveScriptParameters(step, test.values)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadedPlanRejectsInvalidScriptParameterContract(t *testing.T) {
	step := &ScriptedPlanStep{CommonStepFields: CommonStepFields{
		ID: "collector",
		ScriptParameters: map[string]ScriptParameterDefinition{
			"market-name": {Type: "string", Description: "Market"},
		},
	}}
	err := validateLoadedPlanStep(step, 0)
	if err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("error = %v, want invalid parameter-name failure", err)
	}
}
