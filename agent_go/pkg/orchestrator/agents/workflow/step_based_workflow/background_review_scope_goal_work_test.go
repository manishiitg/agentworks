package step_based_workflow

import (
	"strings"
	"testing"
)

// Owner 2026-10-08: Pulse autonomy is a six-level ladder. The level decides;
// an older run/outward/change setting maps to the highest level it fully
// allowed, never more (run ask is Advise only, whatever else it said).
func TestPulseAutonomyPermissionsDefaults(t *testing.T) {
	cases := map[string]GoalWorkPermissions{
		``:                                      PermissionsForLevel(1),
		`{}`:                                    PermissionsForLevel(1),
		`{"pulse":{"enabled":true}}`:            PermissionsForLevel(1),
		`{"pulse":{"autonomy":{"run":"auto"}}}`: PermissionsForLevel(1),
		`{"pulse":{"autonomy":{"run":"ask"}}}`:  PermissionsForLevel(0),
		`{"pulse":{"autonomy":{"outward":"auto","change":"auto"}}}`:            PermissionsForLevel(5),
		`{"pulse":{"autonomy":{"change":"auto"}}}`:                             PermissionsForLevel(3),
		`{"pulse":{"autonomy":{"run":"ask","outward":"ask","change":"Auto"}}}`: PermissionsForLevel(0),
		`{"pulse":{"autonomy":{"level":4,"run":"ask"}}}`:                       PermissionsForLevel(4),
	}
	for raw, want := range cases {
		if got := PulseAutonomyPermissions(raw); got != want {
			t.Errorf("PulseAutonomyPermissions(%s) = %+v, want %+v", raw, got, want)
		}
	}
	if p := PermissionsForLevel(4); !p.Run || !p.Change || !p.Outward || p.Reshape {
		t.Errorf("Publish allows running, step edits and posting but not reshaping: %+v", p)
	}
}

func TestGoalWorkPermissionInstructionsNameTheLadder(t *testing.T) {
	text := goalWorkPermissionInstructions(PermissionsForLevel(3))
	for _, want := range []string{"level 3 of 5, Tune", "✓ 2 Fix", "✓ 3 Tune", "  4 Publish", "spending money", "The tools hold the level"} {
		if !strings.Contains(text, want) {
			t.Errorf("instructions missing %q: %s", want, text)
		}
	}
}
