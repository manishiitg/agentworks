package server

import "testing"

func TestAfterRunDoesNotDependOnPulseToggle(t *testing.T) {
	m := &WorkflowManifest{Pulse: &WorkflowPulseConfig{Enabled: false}}
	schedule := WorkflowSchedule{AfterRun: &ScheduleAfterRun{Backup: true}}
	if m.EffectivePulseMode(schedule) != schedulePulseModeBasic {
		t.Fatal("OFF changed housekeeping")
	}
	m.Pulse.Enabled = true
	if m.EffectivePulseMode(WorkflowSchedule{}) != schedulePulseModeOff {
		t.Fatal("ON invented housekeeping")
	}
}
