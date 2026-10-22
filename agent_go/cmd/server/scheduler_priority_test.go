package server

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

// PLAT-871-9: a waiting 02:30 occurrence must beat both the 03:50 and
// 04:00 watcher, even when retries have refreshed its queue insertion time.
func TestScheduleOldestDueWinsAdmission(t *testing.T) {
	ctx := context.Background()
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "schedule-state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := NewSchedulerService(nil)
	svc.stateStore = store
	day := time.Now().UTC().Truncate(24 * time.Hour)
	due := func(hour, minute int) time.Time {
		return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
	}
	at := due(4, 5)
	manifest := &WorkflowManifest{ID: "priority", Schedules: []WorkflowSchedule{
		{ID: "morning", Enabled: true}, {ID: "watcher", Enabled: true},
		{ID: "parallel", Enabled: true, ConcurrencyMode: "parallel", ParallelRiskAcknowledged: true},
		{ID: "disabled", Enabled: false},
	}}
	queue := func(id string, scheduledFor, queuedAt, expiresAt time.Time) {
		t.Helper()
		if err := store.UpsertPendingOccurrence(ctx, schedulerstate.PendingOccurrence{
			ScopeType: "workflow", ScopeID: "Workflow/priority", ScheduleID: id,
			ScheduledFor: scheduledFor, QueuedAt: queuedAt, ExpiresAt: expiresAt, Policy: "retry",
		}); err != nil {
			t.Fatal(err)
		}
	}
	queue("morning", due(2, 30), due(4, 4), due(5, 0))
	queue("watcher", due(3, 50), due(3, 51), due(5, 0))
	queue("morning", due(4, 4), due(4, 4), due(6, 0)) // retry cannot move the original due/deadline
	pending, err := store.ListPendingOccurrences(ctx)
	if err != nil || len(pending) != 2 || pending[0].ScheduleID != "morning" || !pending[0].ScheduledFor.Equal(due(2, 30)) || !pending[0].ExpiresAt.Equal(due(5, 0)) {
		t.Fatalf("original due order/deadline lost: %+v, %v", pending, err)
	}
	watcher := buildScheduleContext("Workflow/priority", manifest, manifest.Schedules[1])
	for i, scheduledFor := range []time.Time{due(3, 50), due(4, 0)} {
		for _, source := range []string{"cron", "manual", "queued"} {
			watcher.ScheduledFor, watcher.TriggerSource = scheduledFor, source
			if err := svc.claimScheduleRun(ctx, watcher, "overtaking", at); !errors.Is(err, schedulerstate.ErrRunAlreadyActive) {
				t.Fatalf("watcher %d/%s jumped the 02:30 occurrence: %v", i, source, err)
			}
		}
	}
	if _, err := store.GetPendingOccurrence(ctx, "workflow", "Workflow/priority", "watcher"); err != nil {
		t.Fatalf("rejected queued admission lost its row: %v", err)
	}
	// Independent lanes do not join this queue.
	for _, independent := range []*ScheduleContext{
		buildScheduleContext("Workflow/priority", manifest, manifest.Schedules[2]),
		buildScheduleContext("Workflow/other", manifest, manifest.Schedules[1]),
		buildScheduleContext("Workflow/priority", manifest, WorkflowSchedule{ID: "hook", ScheduleType: "webhook"}),
	} {
		independent.ScheduledFor = due(4, 0)
		if err := svc.claimScheduleRun(ctx, independent, independent.WorkspacePath+independent.Schedule.ID, at); err != nil {
			t.Fatalf("independent lane blocked: %v", err)
		}
	}
	morning := buildScheduleContext("Workflow/priority", manifest, manifest.Schedules[0])
	morning.ScheduledFor, morning.TriggerSource = due(2, 30), "queued"
	if err := svc.claimScheduleRun(ctx, morning, "morning-first", at); err != nil {
		t.Fatal(err)
	}
	if err := store.ForceTerminal(ctx, schedulerstate.Transition{RunID: "morning-first", To: schedulerstate.StateCompleted, At: at}); err != nil {
		t.Fatal(err)
	}
	// Expired and disabled rows cannot block the next waiting occurrence.
	queue("morning", due(2, 30), due(4, 4), at)
	queue("disabled", due(1, 0), due(4, 4), due(5, 0))
	watcher.ScheduledFor, watcher.TriggerSource = due(3, 50), "queued"
	if err := svc.claimScheduleRun(ctx, watcher, "watcher-after-morning", at); err != nil {
		t.Fatalf("expired/disabled occurrence blocked the next admission: %v", err)
	}
	if err := store.ForceTerminal(ctx, schedulerstate.Transition{RunID: "watcher-after-morning", To: schedulerstate.StateCompleted, At: at}); err != nil {
		t.Fatal(err)
	}
	// Cancellation removes the priority, and equal-due dependencies may proceed.
	if err := store.DeletePendingOccurrence(ctx, "workflow", "Workflow/priority", "morning"); err != nil {
		t.Fatal(err)
	}
	queue("morning", due(4, 0), due(4, 4), due(5, 0))
	watcher.ScheduledFor, watcher.TriggerSource = due(4, 0), "cron"
	if err := svc.claimScheduleRun(ctx, watcher, "same-slot", at); err != nil {
		t.Fatalf("equal due time blocked a same-slot dependency: %v", err)
	}
}
