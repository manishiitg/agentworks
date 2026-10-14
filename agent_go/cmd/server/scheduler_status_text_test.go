package server

import "testing"

// While a product's timed runs are paused every schedule reads Paused (never
// Missed), and the last-run sentence is plain: the raw error stays in
// last_error only.
func TestScheduleStatusTextIsPlainAndPauseWins(t *testing.T) {
	late := ScheduledJobResponse{ID: "s1", Enabled: true, MissedRunCount: 3, LastStatus: "error", LastError: "workshop turn 1/1 (schedule-message-1) produced no response: message_sequence"}
	jobs := []ScheduledJobResponse{late}
	applyScheduleStatusText(jobs, &SchedulerConfig{})
	if jobs[0].StatusWord != "Needs attention" || jobs[0].LastResult != "The last run did not finish." || jobs[0].LastError == "" {
		t.Fatalf("unpaused: %+v", jobs[0])
	}
	jobs = []ScheduledJobResponse{late}
	applyScheduleStatusText(jobs, &SchedulerConfig{PausedProducts: []string{"agentworks"}})
	if jobs[0].StatusWord != "Paused" || jobs[0].LastResult != "" {
		t.Fatalf("paused product: %+v", jobs[0])
	}
	missed := ScheduledJobResponse{ID: "s2", Enabled: true, MissedRunCount: 2}
	jobs = []ScheduledJobResponse{missed}
	applyScheduleStatusText(jobs, &SchedulerConfig{GloballyPaused: true})
	if jobs[0].StatusWord != "Paused" {
		t.Fatalf("global pause must beat Missed: %+v", jobs[0])
	}
	jobs = []ScheduledJobResponse{missed, {ID: "product:work:s3", Enabled: true}}
	applyScheduleStatusText(jobs, &SchedulerConfig{PausedProducts: []string{"work"}})
	if jobs[0].StatusWord != "Missed" || jobs[1].StatusWord != "Paused" {
		t.Fatalf("a Crew pause must not pause a workflow: %+v", jobs)
	}
}
