package schedulerstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "schedule-state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestRunLifecycleAndEvents(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	startedAt := time.Now().UTC()
	run := Run{
		RunID:         "run-1",
		ScopeType:     "workflow",
		ScopeID:       "Workflow/demo",
		LockKey:       "workflow:Workflow/demo",
		ScheduleID:    "schedule-1",
		TriggerSource: "cron",
		StartedAt:     startedAt,
	}
	if err := store.BeginRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	transitions := []Transition{
		{RunID: run.RunID, To: StateWorkflowRunning, SessionID: "session-1", SessionKind: "workflow", Reason: "session started"},
		{RunID: run.RunID, To: StateWorkflowFinished, SessionID: "session-1", RunFolder: "iteration-0", Reason: "workflow finished"},
		{RunID: run.RunID, To: StatePulseRunning, SessionID: "session-1", SessionKind: "pulse", Reason: "Pulse enabled"},
		{RunID: run.RunID, To: StateCompleted, SessionID: "recovery-session", SessionKind: "pulse_recovery", Reason: "done"},
	}
	for _, transition := range transitions {
		if err := store.Transition(ctx, transition); err != nil {
			t.Fatalf("Transition(%s): %v", transition.To, err)
		}
	}

	got, err := store.GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateCompleted || got.CompletedAt == nil || got.ActiveSessionID != "recovery-session" {
		t.Fatalf("run = %+v", got)
	}
	events, err := store.ListEvents(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != len(transitions)+1 {
		t.Fatalf("len(events) = %d, want %d", len(events), len(transitions)+1)
	}
}

func TestActiveLeaseRejectsOverlappingWorkflowSchedules(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	first := Run{RunID: "run-1", ScopeType: "workflow", ScopeID: "Workflow/demo", LockKey: "workflow:Workflow/demo", ScheduleID: "schedule-1", TriggerSource: "cron"}
	second := Run{RunID: "run-2", ScopeType: "workflow", ScopeID: "Workflow/demo", LockKey: "workflow:Workflow/demo", ScheduleID: "schedule-2", TriggerSource: "manual"}
	if err := store.BeginRun(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginRun(ctx, second); !errors.Is(err, ErrRunAlreadyActive) {
		t.Fatalf("BeginRun(overlap) error = %v, want ErrRunAlreadyActive", err)
	}
	if err := store.Transition(ctx, Transition{RunID: first.RunID, To: StateStopped, Reason: "user stopped"}); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginRun(ctx, second); err != nil {
		t.Fatalf("BeginRun(after terminal): %v", err)
	}
}

func TestRejectsInvalidAndTerminalRegression(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	run := Run{RunID: "run-1", ScopeType: "workflow", ScopeID: "Workflow/demo", LockKey: "workflow:Workflow/demo", ScheduleID: "schedule-1", TriggerSource: "cron"}
	if err := store.BeginRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := store.Transition(ctx, Transition{RunID: run.RunID, To: StatePulseRunning}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("invalid transition error = %v", err)
	}
	if err := store.Transition(ctx, Transition{RunID: run.RunID, To: StateFailed, Reason: "failed to start"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Transition(ctx, Transition{RunID: run.RunID, To: StateWorkflowRunning}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal regression error = %v", err)
	}
}

func TestPulseCanComplete(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	run := Run{RunID: "run-no-modules", ScopeType: "workflow", ScopeID: "Workflow/demo", LockKey: "workflow:Workflow/demo", ScheduleID: "schedule-1"}
	if err := store.BeginRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	for _, transition := range []Transition{
		{RunID: run.RunID, To: StateWorkflowRunning},
		{RunID: run.RunID, To: StateWorkflowFinished},
		{RunID: run.RunID, To: StatePulseRunning},
		{RunID: run.RunID, To: StateCompleted},
	} {
		if err := store.Transition(ctx, transition); err != nil {
			t.Fatalf("transition to %s: %v", transition.To, err)
		}
	}
}

func TestForceTerminalReleasesStuckLease(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	first := Run{RunID: "run-stuck", ScopeType: "workflow", ScopeID: "Workflow/demo", LockKey: "workflow:Workflow/demo", ScheduleID: "schedule-1"}
	if err := store.BeginRun(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := store.ForceTerminal(ctx, Transition{RunID: first.RunID, To: StateCompleted, Reason: "recover lease"}); err != nil {
		t.Fatalf("force terminal: %v", err)
	}
	second := Run{RunID: "run-next", ScopeType: "workflow", ScopeID: "Workflow/demo", LockKey: first.LockKey, ScheduleID: "schedule-2"}
	if err := store.BeginRun(ctx, second); err != nil {
		t.Fatalf("lease remained stuck: %v", err)
	}
}

func TestInterruptActiveRunsReleasesLeases(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	run := Run{RunID: "run-1", ScopeType: "workflow", ScopeID: "Workflow/demo", LockKey: "workflow:Workflow/demo", ScheduleID: "schedule-1", TriggerSource: "cron"}
	if err := store.BeginRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	count, err := store.InterruptActiveRuns(ctx, "server restarted", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	got, err := store.GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateInterrupted {
		t.Fatalf("state = %s, want interrupted", got.State)
	}
}

func TestActiveRunByLockKey(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	run := Run{RunID: "run-active", ScopeType: "workflow", ScopeID: "demo", LockKey: "workflow/demo", ScheduleID: "daily", StartedAt: now}
	if err := store.BeginRun(ctx, run); err != nil {
		t.Fatalf("begin run: %v", err)
	}

	active, err := store.ActiveRunByLockKey(ctx, run.LockKey)
	if err != nil {
		t.Fatalf("active run: %v", err)
	}
	if active.RunID != run.RunID || active.State != StateStarting {
		t.Fatalf("unexpected active run: %+v", active)
	}

	if err := store.Transition(ctx, Transition{RunID: run.RunID, To: StateStopped, Reason: "test stop", At: now.Add(time.Second)}); err != nil {
		t.Fatalf("stop run: %v", err)
	}
	if _, err := store.ActiveRunByLockKey(ctx, run.LockKey); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("expected no active run after stop, got %v", err)
	}
}

func TestFireDecisionsAreDurableAndScoped(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for i, decision := range []string{"skipped_busy", "started"} {
		if err := store.RecordFireDecision(ctx, FireDecision{
			DecisionID: fmt.Sprintf("decision-%d", i), ScopeType: "workflow", ScopeID: "Workflow/demo",
			ScheduleID: "daily", TriggerSource: "cron", Decision: decision, Reason: "test", FiredAt: now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("record decision: %v", err)
		}
	}
	if err := store.RecordFireDecision(ctx, FireDecision{
		DecisionID: "other", ScopeType: "workflow", ScopeID: "Workflow/other", ScheduleID: "daily",
		TriggerSource: "cron", Decision: "started", Reason: "other", FiredAt: now,
	}); err != nil {
		t.Fatalf("record other decision: %v", err)
	}

	decisions, err := store.ListFireDecisions(ctx, "workflow", "Workflow/demo", "daily", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 2 || decisions[0].Decision != "started" || decisions[1].Decision != "skipped_busy" {
		t.Fatalf("unexpected decisions: %+v", decisions)
	}
}

func TestOpenMigratesLegacyFireDecisionsToOccurrenceIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedule-state.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schedule_fire_decisions (
		decision_id TEXT PRIMARY KEY, scope_type TEXT NOT NULL, scope_id TEXT NOT NULL,
		schedule_id TEXT NOT NULL, trigger_source TEXT NOT NULL, decision TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '', run_id TEXT NOT NULL DEFAULT '', fired_at TEXT NOT NULL
	);
	INSERT INTO schedule_fire_decisions VALUES
		('legacy','workflow','Workflow/demo','daily','cron','started','','run-1','2026-08-07T10:30:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	latest, err := store.LatestFireDecision(context.Background(), "workflow", "Workflow/demo", "daily", "cron")
	if err != nil {
		t.Fatal(err)
	}
	if latest.ScheduledFor.IsZero() || !latest.ScheduledFor.Equal(latest.FiredAt) {
		t.Fatalf("legacy occurrence was not backfilled: %+v", latest)
	}
}

func TestFireDecisionUpdatesOneDurableOccurrence(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	scheduledFor := time.Date(2026, 8, 7, 10, 30, 0, 0, time.UTC)
	for i, decision := range []string{"attempted", "started", "skipped_busy"} {
		if err := store.RecordFireDecision(ctx, FireDecision{
			DecisionID: fmt.Sprintf("decision-%d", i), ScopeType: "workflow", ScopeID: "Workflow/demo",
			ScheduleID: "daily", TriggerSource: "cron", Decision: decision,
			Reason: "test", RunID: fmt.Sprintf("run-%d", i), ScheduledFor: scheduledFor,
			FiredAt: scheduledFor.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("record %s: %v", decision, err)
		}
	}
	decisions, err := store.ListFireDecisions(ctx, "workflow", "Workflow/demo", "daily", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].Decision != "started" || decisions[0].RunID != "run-1" {
		t.Fatalf("occurrence was not updated in place: %+v", decisions)
	}
	latest, err := store.LatestFireDecision(ctx, "workflow", "Workflow/demo", "daily", "cron")
	if err != nil {
		t.Fatal(err)
	}
	if !latest.ScheduledFor.Equal(scheduledFor) {
		t.Fatalf("scheduled_for=%s, want %s", latest.ScheduledFor, scheduledFor)
	}
}

func TestLatestFireDecisionDoesNotMixManualAndCron(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	cronAt := time.Date(2026, 8, 7, 10, 30, 0, 0, time.UTC)
	manualAt := cronAt.Add(time.Hour)
	for _, decision := range []FireDecision{
		{DecisionID: "cron", ScopeType: "workflow", ScopeID: "Workflow/demo", ScheduleID: "daily", TriggerSource: "cron", Decision: "started", ScheduledFor: cronAt, FiredAt: cronAt},
		{DecisionID: "manual", ScopeType: "workflow", ScopeID: "Workflow/demo", ScheduleID: "daily", TriggerSource: "manual", Decision: "started", ScheduledFor: manualAt, FiredAt: manualAt},
	} {
		if err := store.RecordFireDecision(ctx, decision); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := store.LatestFireDecision(ctx, "workflow", "Workflow/demo", "daily", "cron")
	if err != nil {
		t.Fatal(err)
	}
	if !latest.ScheduledFor.Equal(cronAt) {
		t.Fatalf("cron cursor=%s, want %s", latest.ScheduledFor, cronAt)
	}
}

func TestFireDecisionRetentionIsBoundedPerTriggerSource(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < fireDecisionRetentionPerSchedule+4; i++ {
		stamp := now.Add(time.Duration(i) * time.Second)
		if _, err := tx.ExecContext(ctx, `INSERT INTO schedule_fire_decisions
			(decision_id, scope_type, scope_id, schedule_id, trigger_source, decision, scheduled_for, fired_at)
			VALUES (?, 'workflow', 'Workflow/demo', 'frequent', 'cron', 'skipped_paused', ?, ?)`,
			fmt.Sprintf("decision-%06d", i), formatTime(stamp), formatTime(stamp)); err != nil {
			_ = tx.Rollback()
			t.Fatalf("seed decision %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	last := fireDecisionRetentionPerSchedule + 4
	lastStamp := now.Add(time.Duration(last) * time.Second)
	if err := store.RecordFireDecision(ctx, FireDecision{
		DecisionID: fmt.Sprintf("decision-%06d", last), ScopeType: "workflow", ScopeID: "Workflow/demo",
		ScheduleID: "frequent", TriggerSource: "cron", Decision: "skipped_paused",
		ScheduledFor: lastStamp, FiredAt: lastStamp,
	}); err != nil {
		t.Fatalf("record cleanup decision: %v", err)
	}
	// A manual occurrence has its own retention lane and must not be deleted by
	// cron cleanup for the same schedule.
	if err := store.RecordFireDecision(ctx, FireDecision{
		DecisionID: "manual", ScopeType: "workflow", ScopeID: "Workflow/demo",
		ScheduleID: "frequent", TriggerSource: "manual", Decision: "started",
		ScheduledFor: lastStamp.Add(time.Second), FiredAt: lastStamp.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	decisions, err := store.ListFireDecisions(ctx, "workflow", "Workflow/demo", "frequent", fireDecisionRetentionPerSchedule)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != fireDecisionRetentionPerSchedule {
		t.Fatalf("retained decisions = %d, want %d", len(decisions), fireDecisionRetentionPerSchedule)
	}
	var cronCount, manualCount int
	if err := store.db.QueryRowContext(ctx, `SELECT
		SUM(CASE WHEN trigger_source='cron' THEN 1 ELSE 0 END),
		SUM(CASE WHEN trigger_source='manual' THEN 1 ELSE 0 END)
		FROM schedule_fire_decisions WHERE scope_type='workflow' AND scope_id='Workflow/demo' AND schedule_id='frequent'`).Scan(&cronCount, &manualCount); err != nil {
		t.Fatal(err)
	}
	if cronCount != fireDecisionRetentionPerSchedule || manualCount != 1 {
		t.Fatalf("retained cron/manual decisions = %d/%d, want %d/1", cronCount, manualCount, fireDecisionRetentionPerSchedule)
	}
}

func TestRunForScheduleOccurrenceUsesDurableFireDecisionLink(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	dayStart := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	parentOccurrence := dayStart.Add(16 * time.Hour)

	// A later run for the same schedule must not release the dependent
	// occurrence merely because it is the newest run started that day.
	for _, run := range []Run{
		{
			RunID: "matching-parent", ScopeType: "workflow", ScopeID: "Workflow/demo",
			LockKey: "workflow:Workflow/demo", ScheduleID: "market-close", TriggerSource: "cron",
			ScheduledFor: parentOccurrence, StartedAt: parentOccurrence,
		},
		{
			RunID: "unrelated-manual", ScopeType: "workflow", ScopeID: "Workflow/demo",
			LockKey: "workflow:Workflow/demo", ScheduleID: "market-close", TriggerSource: "manual",
			ScheduledFor: parentOccurrence.Add(time.Hour), StartedAt: parentOccurrence.Add(time.Hour),
		},
	} {
		if err := store.BeginRun(ctx, run); err != nil {
			t.Fatalf("begin %s: %v", run.RunID, err)
		}
		if err := store.Transition(ctx, Transition{RunID: run.RunID, To: StateWorkflowRunning}); err != nil {
			t.Fatal(err)
		}
		if err := store.Transition(ctx, Transition{RunID: run.RunID, To: StateWorkflowFinished}); err != nil {
			t.Fatal(err)
		}
		if err := store.Transition(ctx, Transition{RunID: run.RunID, To: StateCompleted}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RecordFireDecision(ctx, FireDecision{
		DecisionID: "parent-fire", ScopeType: "workflow", ScopeID: "Workflow/demo",
		ScheduleID: "market-close", TriggerSource: "cron", Decision: "started",
		RunID: "matching-parent", ScheduledFor: parentOccurrence, FiredAt: parentOccurrence,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := store.RunForScheduleOccurrence(ctx, "workflow", "Workflow/demo", "market-close", dayStart, dayStart.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.RunID != "matching-parent" || !got.ScheduledFor.Equal(parentOccurrence) {
		t.Fatalf("matched run = %+v, want the durably linked parent occurrence", got)
	}
}

func TestPendingOccurrencePoliciesAreDurableAndBounded(t *testing.T) {
	base := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name                string
		policy              string
		wantScheduledFor    time.Time
		wantLatest          time.Time
		wantQueuedAt        time.Time
		wantExpiresAt       time.Time
		wantOccurrenceCount int
	}{
		{
			name: "queue_latest replaces the stale occurrence", policy: "queue_latest",
			wantScheduledFor: base.Add(time.Hour), wantLatest: base.Add(time.Hour),
			wantQueuedAt: base.Add(time.Hour), wantExpiresAt: base.Add(3 * time.Hour), wantOccurrenceCount: 1,
		},
		{
			name: "retry preserves the exact first occurrence and deadline", policy: "retry",
			wantScheduledFor: base, wantLatest: base,
			wantQueuedAt: base, wantExpiresAt: base.Add(2 * time.Hour), wantOccurrenceCount: 1,
		},
		{
			name: "coalesce records one catch-up with first and latest occurrence", policy: "coalesce",
			wantScheduledFor: base, wantLatest: base.Add(time.Hour),
			wantQueuedAt: base, wantExpiresAt: base.Add(2 * time.Hour), wantOccurrenceCount: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			ctx := context.Background()
			for i := 0; i < 2; i++ {
				at := base.Add(time.Duration(i) * time.Hour)
				if err := store.UpsertPendingOccurrence(ctx, PendingOccurrence{
					ScopeType: "workflow", ScopeID: "Workflow/demo", ScheduleID: "signals",
					ScheduledFor: at, QueuedAt: at, ExpiresAt: at.Add(2 * time.Hour),
					TriggerSource: "cron", Policy: tc.policy, Reason: fmt.Sprintf("blocked-%d", i),
				}); err != nil {
					t.Fatalf("upsert %d: %v", i, err)
				}
			}
			got, err := store.GetPendingOccurrence(ctx, "workflow", "Workflow/demo", "signals")
			if err != nil {
				t.Fatal(err)
			}
			if !got.ScheduledFor.Equal(tc.wantScheduledFor) ||
				!got.LatestScheduledFor.Equal(tc.wantLatest) ||
				!got.QueuedAt.Equal(tc.wantQueuedAt) ||
				!got.ExpiresAt.Equal(tc.wantExpiresAt) ||
				got.OccurrenceCount != tc.wantOccurrenceCount {
				t.Fatalf("pending = %+v", got)
			}
		})
	}
}

func TestBeginQueuedRunAtomicallyConsumesPendingOnlyWhenLeaseSucceeds(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	if err := store.UpsertPendingOccurrence(ctx, PendingOccurrence{
		ScopeType: "workflow", ScopeID: "Workflow/demo", ScheduleID: "queued",
		ScheduledFor: now, QueuedAt: now, ExpiresAt: now.Add(time.Hour), Policy: "queue_latest",
	}); err != nil {
		t.Fatal(err)
	}
	blocker := Run{
		RunID: "blocker", ScopeType: "workflow", ScopeID: "Workflow/demo",
		LockKey: "workflow:Workflow/demo", ScheduleID: "other", StartedAt: now,
	}
	if err := store.BeginRun(ctx, blocker); err != nil {
		t.Fatal(err)
	}
	queued := Run{
		RunID: "queued-run", ScopeType: "workflow", ScopeID: "Workflow/demo",
		LockKey: "workflow:Workflow/demo", ScheduleID: "queued", TriggerSource: "queued",
		ScheduledFor: now, StartedAt: now.Add(time.Minute),
	}
	if err := store.BeginQueuedRun(ctx, queued); !errors.Is(err, ErrRunAlreadyActive) {
		t.Fatalf("busy queued claim error = %v, want ErrRunAlreadyActive", err)
	}
	if _, err := store.GetPendingOccurrence(ctx, "workflow", "Workflow/demo", "queued"); err != nil {
		t.Fatalf("busy claim consumed pending occurrence: %v", err)
	}

	if err := store.ForceTerminal(ctx, Transition{RunID: blocker.RunID, To: StateCompleted, At: now.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginQueuedRun(ctx, queued); err != nil {
		t.Fatalf("queued claim after lease release: %v", err)
	}
	if _, err := store.GetPendingOccurrence(ctx, "workflow", "Workflow/demo", "queued"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("pending occurrence remains after atomic claim: %v", err)
	}
	if err := store.ForceTerminal(ctx, Transition{RunID: queued.RunID, To: StateCompleted, At: now.Add(3 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	duplicate := queued
	duplicate.RunID = "duplicate"
	if err := store.BeginQueuedRun(ctx, duplicate); !errors.Is(err, ErrPendingOccurrenceMissing) {
		t.Fatalf("duplicate queued claim error = %v, want ErrPendingOccurrenceMissing", err)
	}
}

func TestWebhookRunFolderBinding(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.BeginRun(ctx, Run{RunID: "hook", ScopeType: "workflow", ScopeID: "test", LockKey: "test", ScheduleID: "trigger", TriggerSource: "webhook"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.AssignRunFolder(ctx, "hook", "iteration-1-hook"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AssignRunFolder(ctx, "hook", "iteration-2-hook"); err == nil {
		t.Fatal("folder reassigned")
	}
	if err := s.Transition(ctx, Transition{RunID: "hook", To: StateFailed}); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignRunFolder(ctx, "hook", "iteration-1-hook"); err == nil {
		t.Fatal("terminal folder modified")
	}
}

func TestActiveRunForScheduleIgnoresConcurrencyLockKeyShape(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	run := Run{RunID: "parallel-run", ScopeType: "workflow", ScopeID: "Workflow/demo", LockKey: "workflow-parallel|Workflow/demo|measurement", ScheduleID: "measurement", TriggerSource: "cron"}
	if err := s.BeginRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	got, err := s.ActiveRunForSchedule(ctx, "workflow", "Workflow/demo", "measurement")
	if err != nil || got.RunID != run.RunID {
		t.Fatalf("active run = (%+v, %v), want %s", got, err, run.RunID)
	}
}

func TestRetiredPulseStagesReleaseLocksOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	run := Run{RunID: "old", ScopeType: "workflow", ScopeID: "Workflow/example", LockKey: "workflow:example", ScheduleID: "daily", TriggerSource: "cron"}
	if err := store.BeginRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DROP INDEX idx_schedule_runs_active_lock; CREATE UNIQUE INDEX idx_schedule_runs_active_lock ON schedule_runs(lock_key) WHERE state IN ('starting','workflow_running','workflow_finished','pulse_gate','pulse_modules','pulse_finalizing'); UPDATE schedule_runs SET state='pulse_modules' WHERE run_id='old'`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	old, err := store.GetRun(ctx, "old")
	if err != nil || old.State != StateInterrupted || old.CompletedAt == nil {
		t.Fatalf("old=%+v err=%v", old, err)
	}
	run.RunID = "new"
	if err := store.BeginRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	for _, state := range []State{StateWorkflowRunning, StateWorkflowFinished, StatePulseRunning} {
		if err := store.Transition(ctx, Transition{RunID: "new", To: state}); err != nil {
			t.Fatal(err)
		}
	}
	run.RunID = "overlap"
	if err := store.BeginRun(ctx, run); !errors.Is(err, ErrRunAlreadyActive) {
		t.Fatalf("Pulse active index lost: %v", err)
	}
}

// A damaged queue timestamp must not hold every schedule in its workflow.
func TestUnreadablePendingTimingDoesNotBlockAdmission(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.UpsertPendingOccurrence(ctx, PendingOccurrence{
		ScopeType: "workflow", ScopeID: "Workflow/demo", ScheduleID: "damaged",
		ScheduledFor: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE schedule_pending_occurrences SET scheduled_for='unreadable'`); err != nil {
		t.Fatal(err)
	}
	pending, err := store.ListPendingOccurrences(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatalf("unreadable row stopped queue scan: %+v %v", pending, err)
	}
	if err := store.BeginRun(ctx, Run{
		RunID: "next", ScopeType: "workflow", ScopeID: "Workflow/demo", LockKey: "workflow:demo",
		ScheduleID: "next", ScheduledFor: now, OrderedScheduleIDs: []string{"damaged", "next"},
	}); err != nil {
		t.Fatalf("unreadable row blocked valid admission: %v", err)
	}
}
