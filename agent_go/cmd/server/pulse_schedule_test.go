package server

import (
	"testing"
	"time"
)

func TestDecidePulseDueUsesTheChosenTimeWithinGuards(t *testing.T) {
	schedule := (&WorkflowManifest{}).EffectivePulseSchedule()
	last := time.Date(2026, 9, 20, 6, 0, 0, 0, time.UTC)
	chosen := last.Add(72 * time.Hour)
	state := pulseScheduleState{LastStartedAt: last, NextAt: chosen, NextReason: "Tuesday's post results mature Friday"}

	if got := decidePulseDue(schedule, state, false, chosen.Add(-time.Minute)); got.Due {
		t.Fatalf("due one minute before the chosen time: %+v", got)
	}
	got := decidePulseDue(schedule, state, false, chosen)
	if !got.Due || !got.NextAt.Equal(chosen) || got.Reason != state.NextReason {
		t.Fatalf("at the chosen time: %+v", got)
	}
}

func TestDecidePulseDueEnforcesTheSixHourFloorAndAtLeastWeekly(t *testing.T) {
	schedule := (&WorkflowManifest{}).EffectivePulseSchedule()
	last := time.Date(2026, 9, 20, 6, 0, 0, 0, time.UTC)

	tooSoon := decidePulseDue(schedule, pulseScheduleState{LastStartedAt: last, NextAt: last.Add(2 * time.Hour)}, false, last.Add(3*time.Hour))
	if tooSoon.Due || !tooSoon.NextAt.Equal(last.Add(6*time.Hour)) {
		t.Fatalf("chosen time inside the floor must move to last+6h: %+v", tooSoon)
	}
	// A pass that chooses a time within hours gets it once the floor passes.
	soon := decidePulseDue(schedule, pulseScheduleState{LastStartedAt: last, NextAt: last.Add(8 * time.Hour)}, false, last.Add(8*time.Hour))
	if !soon.Due || !soon.NextAt.Equal(last.Add(8*time.Hour)) {
		t.Fatalf("a chosen time 8h out must run then: %+v", soon)
	}
	tooLate := decidePulseDue(schedule, pulseScheduleState{LastStartedAt: last, NextAt: last.Add(30 * 24 * time.Hour)}, false, last.Add(7*24*time.Hour))
	if !tooLate.Due || !tooLate.NextAt.Equal(last.Add(7*24*time.Hour)) {
		t.Fatalf("chosen time beyond a week must be capped at last+7d: %+v", tooLate)
	}
}

func TestDecidePulseDueDefaultsToDailyWhenThePassChoseNothing(t *testing.T) {
	schedule := (&WorkflowManifest{}).EffectivePulseSchedule()
	last := time.Date(2026, 9, 20, 6, 0, 0, 0, time.UTC)
	got := decidePulseDue(schedule, pulseScheduleState{LastStartedAt: last}, false, last.Add(24*time.Hour))
	if !got.Due || !got.NextAt.Equal(last.Add(24*time.Hour)) {
		t.Fatalf("no chosen time: want default one day after the last Pulse, got %+v", got)
	}
	if never := decidePulseDue(schedule, pulseScheduleState{}, false, last); never.Due || !never.NextAt.IsZero() {
		t.Fatalf("no state at all: launcher must persist a first time instead, got %+v", never)
	}
}

func TestDecidePulseDueFastRequestPullsEarlierButKeepsTheFloor(t *testing.T) {
	schedule := (&WorkflowManifest{}).EffectivePulseSchedule()
	last := time.Date(2026, 9, 20, 6, 0, 0, 0, time.UTC)
	state := pulseScheduleState{LastStartedAt: last, NextAt: last.Add(5 * 24 * time.Hour), NextReason: "weekly outcome window"}

	early := decidePulseDue(schedule, state, true, last.Add(2*time.Hour))
	if early.Due || !early.NextAt.Equal(last.Add(6*time.Hour)) {
		t.Fatalf("fast request within the floor must wait until last+6h: %+v", early)
	}
	due := decidePulseDue(schedule, state, true, last.Add(7*time.Hour))
	if !due.Due || due.Reason != "a workflow run requested an earlier Pulse" {
		t.Fatalf("fast request after the guard must run now: %+v", due)
	}
}

func TestFirstPulseTimeIsNextSixAMLocal(t *testing.T) {
	schedule := WorkflowPulseSchedule{Timezone: "Asia/Kolkata", MinIntervalHours: 24, MaxIntervalHours: 168}
	loc, _ := time.LoadLocation("Asia/Kolkata")
	now := time.Date(2026, 9, 23, 13, 0, 0, 0, loc)
	want := time.Date(2026, 9, 24, 6, 0, 0, 0, loc).UTC()
	if got := firstPulseTime(schedule, now); !got.Equal(want) {
		t.Fatalf("first Pulse = %v, want %v", got, want)
	}
	early := time.Date(2026, 9, 23, 5, 0, 0, 0, loc)
	if got := firstPulseTime(schedule, early); !got.Equal(time.Date(2026, 9, 23, 6, 0, 0, 0, loc).UTC()) {
		t.Fatalf("before 06:00 the first Pulse is the same morning, got %v", got)
	}
}

func TestEffectivePulseModeNeverRunsFullOnANormalSchedule(t *testing.T) {
	manifest := &WorkflowManifest{Pulse: &WorkflowPulseConfig{Enabled: true}}
	for mode, want := range map[string]string{"full": "basic", "basic": "basic", "off": "off", "": "basic"} {
		if got := manifest.EffectivePulseMode(WorkflowSchedule{PulseMode: mode}); got != want {
			t.Errorf("pulse_mode %q with Pulse on = %q, want %q", mode, got, want)
		}
	}
	if got := (&WorkflowManifest{}).EffectivePulseMode(WorkflowSchedule{}); got != "off" {
		t.Errorf("empty mode with Pulse off = %q, want off", got)
	}
}

func TestEffectivePulseScheduleDefaultsAndTimezone(t *testing.T) {
	manifest := &WorkflowManifest{Schedules: []WorkflowSchedule{
		{Enabled: true, Timezone: "Asia/Kolkata"}, {Enabled: true, Timezone: "Asia/Kolkata"}, {Enabled: true, Timezone: "UTC"}, {Enabled: false, Timezone: "America/New_York"},
	}}
	got := manifest.EffectivePulseSchedule()
	if got.MinIntervalHours != 6 || got.MaxIntervalHours != 168 || got.Timezone != "Asia/Kolkata" {
		t.Fatalf("defaults = %+v", got)
	}
}
