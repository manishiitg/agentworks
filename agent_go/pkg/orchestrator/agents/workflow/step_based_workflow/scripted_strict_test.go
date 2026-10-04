package step_based_workflow

import "testing"

// PLAT-436: scripted steps never self-heal in a run.
func TestScriptedRunIsStrictUnlessTheBuilderRepairs(t *testing.T) {
	scripted := &RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "fetch"}}
	scriptOnly := &RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "fetch"}, ScriptOnly: true}
	for name, tc := range map[string]struct {
		ctx    *ExecutionContext
		step   PlanStepInterface
		strict bool
	}{
		"no context":                       {nil, scripted, true},
		"a run (schedule, webhook, route)": {&ExecutionContext{}, scripted, true},
		"saved-script-only test":           {&ExecutionContext{SavedScriptOnly: true, AllowScriptRepair: true}, scripted, true},
		"Builder execute_step":             {&ExecutionContext{AllowScriptRepair: true}, scripted, false},
		"script_only step, even Builder":   {&ExecutionContext{AllowScriptRepair: true}, scriptOnly, true},
	} {
		if got := scriptedRunIsStrict(tc.ctx, tc.step); got != tc.strict {
			t.Errorf("%s: strict = %v, want %v", name, got, tc.strict)
		}
	}
}
