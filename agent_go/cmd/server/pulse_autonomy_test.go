package server

import (
	"strings"
	"testing"
)

func TestResolvePulseAutonomyDefaultsAndValidation(t *testing.T) {
	cases := []struct {
		in   *WorkflowPulseAutonomy
		want PulseAutonomyLevels
	}{
		{nil, PulseAutonomyLevels{Level: 1, Run: "auto", Outward: "ask", Change: "ask"}},
		{&WorkflowPulseAutonomy{}, PulseAutonomyLevels{Level: 1, Run: "auto", Outward: "ask", Change: "ask"}},
		{&WorkflowPulseAutonomy{Run: "ask"}, PulseAutonomyLevels{Level: 0, Run: "ask", Outward: "ask", Change: "ask"}},
		{&WorkflowPulseAutonomy{Outward: " AUTO ", Change: "auto"}, PulseAutonomyLevels{Level: 5, Run: "auto", Outward: "auto", Change: "auto"}},
		// Owner 2026-10-08: the six-level ladder; the level decides, and the
		// old switches are derived for older readers.
		{&WorkflowPulseAutonomy{Level: intPtr(3), Run: "ask"}, PulseAutonomyLevels{Level: 3, Run: "auto", Outward: "ask", Change: "auto"}},
	}
	for _, c := range cases {
		got, err := resolvePulseAutonomy(c.in)
		if err != nil || got != c.want {
			t.Errorf("resolvePulseAutonomy(%+v) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []*WorkflowPulseAutonomy{{Run: "always"}, {Outward: "yes"}, {Change: "sometimes"}} {
		if _, err := resolvePulseAutonomy(bad); err == nil {
			t.Errorf("resolvePulseAutonomy(%+v) should reject an unknown level", bad)
		}
	}
	if _, err := resolvePulseAutonomy(&WorkflowPulseAutonomy{Level: intPtr(6)}); err == nil {
		t.Error("a level above 5 must be rejected")
	}
	if _, err := resolvePulseAutonomy(&WorkflowPulseAutonomy{Change: "yes"}); err == nil || !strings.Contains(err.Error(), "pulse.autonomy.change") {
		t.Errorf("error should name the field, got %v", err)
	}
}

func intPtr(v int) *int { return &v }
