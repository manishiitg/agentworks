package server

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

// PLAT-871-33: a prerequisite that never runs must not leave a timer waiting
// forever. Real SQLite also checks repeated waiting ticks update one receipt.
func TestCrewDependencyWaitExpiresAtLocalDayEnd(t *testing.T) {
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "scheduler.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scheduler := &SchedulerService{stateStore: store}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 11, 1, 0, 0, 0, 0, location) // 25-hour local day
	occurrence := time.Date(2026, 11, 1, 9, 0, 0, 0, location)
	job := productScheduleJob{ProjectID: "crew", Profile: agentprofiles.Profile{ID: "work"},
		Schedule: productschedule.Schedule{ID: "report", Enabled: true, CronExpression: "0 9 * * *", Timezone: location.String(), CreatedAt: day.Format(time.RFC3339),
			When: &projectactions.When{After: &projectactions.After{Schedules: []string{"collect"}}}}}
	ctx := context.Background()
	for _, at := range []time.Time{occurrence, occurrence.Add(time.Minute), time.Date(2026, 11, 1, 23, 59, 0, 0, location)} {
		decision, _ := scheduler.crewTimerDependencyDecision(ctx, job, at)
		if decision.Run || !decision.Deferred || !decision.ScheduledFor.Equal(occurrence) {
			t.Fatalf("wait lost its occurrence or expired before local midnight: %+v", decision)
		}
		job.State.LastAttemptAt = decision.ScheduledFor.Format(time.RFC3339)
	}
	other := job
	other.Schedule.ID, other.State.LastAttemptAt = "other-report", ""
	if decision, _ := scheduler.crewTimerDependencyDecision(ctx, other, occurrence); !decision.Deferred || !decision.ScheduledFor.Equal(occurrence) {
		t.Fatalf("waiting receipt identity collided with another timer: %+v", decision)
	}
	pending, err := store.GetPendingOccurrence(ctx, "crew", "crew", "report")
	midnight := time.Date(2026, 11, 2, 0, 0, 0, 0, location)
	if err != nil || !pending.ExpiresAt.Equal(midnight) {
		t.Fatalf("wait expiry does not follow the local day: %+v %v", pending, err)
	}
	receipts, err := store.ListFireDecisions(ctx, "crew", "crew", "report", 10)
	if err != nil || len(receipts) != 1 || receipts[0].Decision != "waiting_for_dependency" {
		t.Fatalf("waiting added a receipt per tick: %+v %v", receipts, err)
	}
	decision, _ := scheduler.crewTimerDependencyDecision(ctx, job, midnight)
	if decision.Run || decision.Deferred || !decision.ScheduledFor.Equal(occurrence) {
		t.Fatalf("unresolved dependency did not expire: %+v", decision)
	}
	if _, err := store.GetPendingOccurrence(ctx, "crew", "crew", "report"); !errors.Is(err, schedulerstate.ErrRunNotFound) {
		t.Fatalf("expired wait remains pending: %v", err)
	}
	receipts, err = store.ListFireDecisions(ctx, "crew", "crew", "report", 10)
	if err != nil || len(receipts) != 1 || receipts[0].Decision != "expired_dependency" {
		t.Fatalf("expiry is not visible in the occurrence ledger: %+v %v", receipts, err)
	}
}
