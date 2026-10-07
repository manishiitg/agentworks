package step_based_workflow

import (
	"strings"
	"testing"
)

func TestPulseAutonomyPermissionsDefaults(t *testing.T) {
	cases := map[string]GoalWorkPermissions{
		``:                                       {Run: true},
		`{}`:                                     {Run: true},
		`{"pulse":{"enabled":true}}`:             {Run: true},
		`{"pulse":{"autonomy":{"run":"auto"}}}`:  {Run: true},
		`{"pulse":{"autonomy":{"run":"ask"}}}`:   {},
		`{"pulse":{"autonomy":{"run":" ASK "}}}`: {},
		`{"pulse":{"autonomy":{"outward":"auto","change":"auto"}}}`:            {Run: true, Outward: true, Change: true},
		`{"pulse":{"autonomy":{"run":"ask","outward":"ask","change":"Auto"}}}`: {Change: true},
	}
	for raw, want := range cases {
		if got := PulseAutonomyPermissions(raw); got != want {
			t.Errorf("PulseAutonomyPermissions(%s) = %+v, want %+v", raw, got, want)
		}
	}
}

func TestGoalWorkPermissionInstructionsNameEachLevel(t *testing.T) {
	ask := goalWorkPermissionInstructions(GoalWorkPermissions{})
	for _, want := range []string{"Run permission: ask", "Outward permission: ask", "Change permission: ask"} {
		if !strings.Contains(ask, want) {
			t.Errorf("ask instructions missing %q: %s", want, ask)
		}
	}
	auto := goalWorkPermissionInstructions(GoalWorkPermissions{Run: true, Outward: true, Change: true})
	for _, want := range []string{"Run permission: auto", "Outward permission: auto", "Change permission: auto", "challenge them through a decision"} {
		if !strings.Contains(auto, want) {
			t.Errorf("auto instructions missing %q: %s", want, auto)
		}
	}
}
