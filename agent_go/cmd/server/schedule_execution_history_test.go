package server

import (
	"testing"
	"time"
)

// A schedule saved with a date that never comes (30 February) made Next return
// zero time, and the missed-run scan appended zero times until the server ran
// out of memory on the next schedules list.
func TestMissedStatusStopsForADateThatNeverComes(t *testing.T) {
	now := time.Now().UTC()
	sched := WorkflowSchedule{ID: "s", Enabled: true, ScheduleType: "cron", CronExpression: "0 9 30 2 *", Timezone: "UTC"}
	tracker := &WorkflowScheduleExecutionTrack{ScheduleID: "s", Enabled: true, WindowStartAt: now.Add(-48 * time.Hour)}
	done := make(chan WorkflowScheduleMissedStatus, 1)
	go func() { done <- ComputeWorkflowScheduleMissedStatus(sched, tracker, now) }()
	select {
	case missed := <-done:
		if missed.MissedRunCount != 0 {
			t.Fatalf("a date that never comes counted %d missed runs", missed.MissedRunCount)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missed-run scan did not stop")
	}
	if err := ValidateCronExpression("0 9 30 2 *"); err == nil {
		t.Fatal("saving a schedule that never runs was accepted")
	}
}

// #4373: a reload replaced the registration after the clock selected a slot but before it advanced the cursor; the new
// job kept the older cursor and fired the same slot again on the next tick.
func TestFiredSlotAdvancesTheReloadedRegistration(t *testing.T) {
	slot := time.Date(2026, 11, 27, 10, 1, 0, 0, time.UTC)
	selected := &registeredJob{lastFired: slot.Add(-time.Minute)}
	reloaded := &registeredJob{lastFired: slot.Add(-time.Minute)}
	s := &SchedulerService{jobs: map[string]*registeredJob{"crew/report": reloaded}}

	s.advanceCursorLocked(dueRegisteredJob{key: "crew/report", job: selected, scheduledFor: slot})

	for name, job := range map[string]*registeredJob{"selected": selected, "reloaded": reloaded} {
		if !job.lastFired.Equal(slot) {
			t.Errorf("%s job cursor = %s, want the fired slot %s", name, job.lastFired, slot)
		}
	}
}
