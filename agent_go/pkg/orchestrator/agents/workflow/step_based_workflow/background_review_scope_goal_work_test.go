package step_based_workflow

import "testing"

func TestPulsePermissionsMatchTwoSettings(t *testing.T) {
	for raw, want := range map[string]GoalWorkPermissions{
		`{}`: {},
		`{"pulse":{"enabled":false,"outward_actions":"allow"}}`: {},
		`{"pulse":{"enabled":true,"outward_actions":"ask"}}`:    {Run: true, Change: true, Reshape: true},
		`{"pulse":{"enabled":true,"outward_actions":"allow"}}`:  {Run: true, Change: true, Reshape: true, Outward: true},
	} {
		if got := PulsePermissions(raw); got != want {
			t.Fatalf("permissions %s = %+v, want %+v", raw, got, want)
		}
	}
}
