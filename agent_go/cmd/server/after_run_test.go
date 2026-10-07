package server

import "testing"

// PLAT-697 phase 0: pulse_mode leaves schedules. Each schedule keeps exactly
// the backup, publish and notify its mode did (basic, and the retired full,
// did all three after a normal run; off did none), a legacy full keeps the
// workflow on its own Pulse schedule, and pulse_mode stays in step for one
// release.
func TestPulseModeMigratesToAfterRunOptions(t *testing.T) {
	all := ScheduleAfterRun{Backup: true, Publish: true, Notify: true}
	custom := ScheduleAfterRun{Notify: true}
	m := &WorkflowManifest{Schedules: []WorkflowSchedule{
		{ID: "basic", Enabled: true, PulseMode: "basic", PulseModeReason: "Routine"},
		{ID: "off", Enabled: true, PulseMode: "off", PulseModeReason: "Owner turned it off"},
		{ID: "full", Enabled: true, PulseMode: "full", PulseModeReason: "Weekly review"},
		{ID: "inherit", Enabled: true},
		{ID: "hook", Enabled: true, ScheduleType: "webhook", PulseMode: "off"},
		{ID: "chosen", Enabled: true, PulseMode: "basic", PulseModeReason: "Routine", AfterRun: &custom},
	}}
	if !m.MigrateScheduleAfterRun() {
		t.Fatal("migration reported no change")
	}
	want := map[string]ScheduleAfterRun{"basic": all, "off": {}, "full": all, "inherit": all, "chosen": custom}
	for _, schedule := range m.Schedules {
		if schedule.ID == "hook" {
			if schedule.AfterRun != nil {
				t.Fatalf("webhook got after-run options: %+v", schedule.AfterRun)
			}
			continue
		}
		if schedule.AfterRun == nil || *schedule.AfterRun != want[schedule.ID] {
			t.Fatalf("%s after_run = %+v, want %+v", schedule.ID, schedule.AfterRun, want[schedule.ID])
		}
		if got := m.EffectiveAfterRun(schedule); got != want[schedule.ID] {
			t.Fatalf("%s effective = %+v, want %+v", schedule.ID, got, want[schedule.ID])
		}
		mirror := "off"
		if want[schedule.ID].Any() {
			mirror = "basic"
		}
		if schedule.ID != "chosen" && (schedule.PulseMode != mirror || schedule.PulseModeReason == "") {
			t.Fatalf("%s legacy pulse_mode = %q (%q), want %q with a reason", schedule.ID, schedule.PulseMode, schedule.PulseModeReason, mirror)
		}
	}
	// "inherit" read the workflow default: Pulse on via the legacy full schedule.
	if m.Pulse == nil || !m.Pulse.Enabled {
		t.Fatal("a legacy full schedule must keep the workflow's own Pulse on")
	}
	if m.MigrateScheduleAfterRun() {
		t.Fatal("a second migration changed already-migrated schedules")
	}
	// The old field is still read where after_run is absent.
	legacy := &WorkflowManifest{}
	if got := legacy.EffectiveAfterRun(WorkflowSchedule{PulseMode: "off", PulseModeReason: "x"}); got.Any() {
		t.Fatalf("legacy off read as %+v", got)
	}
	if got := legacy.EffectiveAfterRun(WorkflowSchedule{PulseMode: "basic", PulseModeReason: "x"}); got != all {
		t.Fatalf("legacy basic read as %+v", got)
	}
}
