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
