package server

import (
	"testing"
	"time"
)

// TestOrphanedProgressMirrorsCannotBlockTurnCompletion reproduces the exact
// social-media 2026-08-16 incident (PLAT-117) through the real snapshot path.
//
// Two per-step progress mirrors were left open by missing end events. The parent
// workflow execution finished normally. Because those mirrors were counted as
// live children, terminal() was unreachable and the turn was guaranteed to end
// in an idle-wait timeout — after which the operator was emailed that the
// workflow never ran, though a post had landed and been verified.
func TestOrphanedProgressMirrorsCannotBlockTurnCompletion(t *testing.T) {
	api := lifecycleTestAPI()
	const sessionID = "session-progress-mirror"
	now := time.Now().UTC()
	doneAt := now.Add(3 * time.Hour)

	api.trackedWorkflowExecutions["root"] = &TrackedWorkflowExecution{
		ExecutionID: "root", SessionID: sessionID, Source: trackedExecutionSourceConversationTurn,
		Status: trackedExecutionStatusCompleted, StartedAt: now, CompletedAt: &doneAt,
	}

	// The parent workflow execution: real work, finished.
	parent := &BackgroundAgent{
		ID: "workflow-full-msvyee8q01", ParentExecutionID: "root", SessionID: sessionID,
		Kind: "full_workflow", Status: BGAgentCompleted, CreatedAt: now, CompletedAt: &doneAt,
	}
	parent.finishCompletionNotification(true)
	api.bgAgentRegistry.Register(sessionID, parent)

	// Two step-0 progress mirrors whose end events never arrived. Same step
	// index, different agentType/agentName keys — exactly what was observed.
	for _, id := range []string{
		"workflow-full-msvyee8q01-step-0-msvyijui03",
		"workflow-full-msvyee8q01-step-0-msw3jq3i0f",
	} {
		api.bgAgentRegistry.Register(sessionID, &BackgroundAgent{
			ID: id, ParentExecutionID: "workflow-full-msvyee8q01", SessionID: sessionID,
			// Production stores these as kind=orchestrator (verified against the
			// live durable log), NOT workflow_step: OnExecutionStart lets a
			// declared kind win, so the workflow_step override never fires here.
			Kind: "orchestrator", Status: BGAgentRunning, CreatedAt: now,
		})
	}

	state := api.conversationTurnTreeSnapshot("root")
	if state.RunningChildren != 0 {
		t.Fatalf("RunningChildren = %d, want 0: a per-step progress mirror must never hold a turn open", state.RunningChildren)
	}
	if !state.terminal() {
		t.Fatalf("turn tree must reach terminal once real work finished: %+v", state)
	}
}

// TestProgressMirrorStillCountsAsProgress pins the other half: a live step is
// real evidence the turn is alive, so it must keep advancing LastProgressAt even
// though it cannot block completion. Otherwise excluding mirrors would make a
// genuinely-working turn look idle and time out faster than before.
func TestProgressMirrorStillCountsAsProgress(t *testing.T) {
	api := lifecycleTestAPI()
	const sessionID = "session-progress-advances"
	start := time.Now().UTC()
	stepAt := start.Add(20 * time.Minute)

	api.trackedWorkflowExecutions["root"] = &TrackedWorkflowExecution{
		ExecutionID: "root", SessionID: sessionID, Source: trackedExecutionSourceConversationTurn,
		Status: trackedExecutionStatusRunning, StartedAt: start,
	}
	api.bgAgentRegistry.Register(sessionID, &BackgroundAgent{
		ID: "workflow-full-x-step-2-abc", ParentExecutionID: "root", SessionID: sessionID,
		Kind: "workflow_step", Status: BGAgentRunning, CreatedAt: stepAt,
	})

	state := api.conversationTurnTreeSnapshot("root")
	if !state.LastProgressAt.Equal(stepAt) {
		t.Fatalf("LastProgressAt = %v, want the step's own timestamp %v", state.LastProgressAt, stepAt)
	}
}

// TestRealBackgroundChildStillBlocksTurnCompletion is the guard against
// over-correcting: only progress mirrors are exempt. A genuine background agent
// must still hold the turn open, or PLAT-117's fix would end turns early —
// a worse failure than ending them late.
func TestRealBackgroundChildStillBlocksTurnCompletion(t *testing.T) {
	api := lifecycleTestAPI()
	const sessionID = "session-real-child"
	now := time.Now().UTC()
	doneAt := now.Add(time.Minute)

	api.trackedWorkflowExecutions["root"] = &TrackedWorkflowExecution{
		ExecutionID: "root", SessionID: sessionID, Source: trackedExecutionSourceConversationTurn,
		Status: trackedExecutionStatusCompleted, StartedAt: now, CompletedAt: &doneAt,
	}
	api.bgAgentRegistry.Register(sessionID, &BackgroundAgent{
		ID: "delegation-real-work", ParentExecutionID: "root", SessionID: sessionID,
		Kind: "delegation", Status: BGAgentRunning, CreatedAt: now,
	})

	state := api.conversationTurnTreeSnapshot("root")
	if state.RunningChildren != 1 {
		t.Fatalf("RunningChildren = %d, want 1: real background work must still hold the turn open", state.RunningChildren)
	}
	if state.terminal() {
		t.Fatal("a turn with genuine live background work must not be terminal")
	}
}

// TestProgressMirrorClassificationUsesDeclaredKindAndLegacyMetadata pins both
// halves of the classification, matching isWorkflowStepTrackingExecution: a
// declared kind wins, and the legacy execution_type metadata is the fallback for
// records that predate declared kinds.
func TestProgressMirrorClassificationUsesDeclaredKindAndLegacyMetadata(t *testing.T) {
	for name, tc := range map[string]struct {
		snapshot BackgroundAgentSnapshot
		want     bool
	}{
		"declared kind": {BackgroundAgentSnapshot{Kind: "workflow_step"}, true},
		"real orphan shape — kind=orchestrator, id says step": {BackgroundAgentSnapshot{
			ID: "workflow-full-msvyee8q01-step-0-msvyijui03", Kind: "orchestrator"}, true},
		"parent full-run must NOT match": {BackgroundAgentSnapshot{
			ID: "workflow-full-msvyee8q01", Kind: "full_run"}, false},
		"agent item is real work": {BackgroundAgentSnapshot{
			ID: "msgseq-execute-remediate-123", Kind: "agent_item"}, false},
		"sub agent is real work": {BackgroundAgentSnapshot{
			ID: "todo-sub-execute-remediate-123", Kind: "sub_agent"}, false},
		"legacy metadata fallback": {BackgroundAgentSnapshot{Metadata: map[string]string{"execution_type": "workflow-step"}}, true},
		"real delegation":          {BackgroundAgentSnapshot{Kind: "delegation"}, false},
		"full workflow parent":     {BackgroundAgentSnapshot{Kind: "full_workflow"}, false},
		"unclassified":             {BackgroundAgentSnapshot{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.snapshot.IsProgressMirror(); got != tc.want {
				t.Fatalf("IsProgressMirror() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestReconcileOrphanedProgressChildrenSettlesCompletely pins the second half of
// the original omission: settling status but not the notified flag left the
// record reported as running anyway, because an unnotified terminal child is
// deliberately held open by conversationTurnTreeSnapshot.
func TestReconcileOrphanedProgressChildrenSettlesCompletely(t *testing.T) {
	registry := NewBackgroundAgentRegistry()
	const sessionID = "session-reconcile"
	registry.Register(sessionID, &BackgroundAgent{
		ID: "workflow-full-p1-step-0-aaa", ParentExecutionID: "workflow-full-p1", SessionID: sessionID,
		Kind: "workflow_step", Status: BGAgentRunning, CreatedAt: time.Now().UTC(),
	})

	settled := registry.ReconcileOrphanedProgressChildren(sessionID, "workflow-full-p1", "parent finished without an end event")
	if len(settled) != 1 {
		t.Fatalf("settled = %v, want exactly one orphan", settled)
	}

	snap := registry.Get(sessionID, "workflow-full-p1-step-0-aaa").GetSnapshot()
	if snap.Status != BGAgentFailed {
		t.Fatalf("status = %q, want failed", snap.Status)
	}
	if snap.CompletedAt == nil {
		t.Fatal("a settled orphan must carry a CompletedAt")
	}
	if !snap.CompletionNotified {
		t.Fatal("a settled orphan must be marked notified, or the turn snapshot keeps reporting it as running")
	}
}

// TestParentCompletionRemovesOrphanedProgressMirrorFromGlobalMonitor reproduces
// the Social Media 2026-08-20 incident through the product completion path.
// The background-agent and terminal copies of the child were terminal, but its
// unified tracked-execution copy stayed running, so /api/workflow/running and
// the authoritative runtime snapshot continued to report the workflow busy.
func TestParentCompletionRemovesOrphanedProgressMirrorFromGlobalMonitor(t *testing.T) {
	api := lifecycleTestAPI()
	const (
		sessionID = "schedule-manual--social-media"
		parentID  = "workflow-full-mt12p13b01"
		orphanID  = "workflow-full-mt12p13b01-step-0-mt17a9lh09"
	)
	now := time.Now().UTC()

	api.bgAgentRegistry.Register(sessionID, &BackgroundAgent{
		ID: parentID, Name: "full workflow", SessionID: sessionID,
		Kind: "full_workflow", Status: BGAgentRunning, CreatedAt: now,
	})
	api.bgAgentRegistry.Register(sessionID, &BackgroundAgent{
		ID: orphanID, ParentExecutionID: parentID, Name: "[Execute] Run Today's Actions",
		SessionID: sessionID, Kind: "orchestrator", Status: BGAgentRunning, CreatedAt: now,
	})
	api.trackExecutionStart(&TrackedWorkflowExecution{
		ExecutionID: parentID, SessionID: sessionID,
		Source: trackedExecutionSourceWorkshopBackground, Kind: "full_workflow",
		WorkspacePath: "Workflow/social-media", Status: trackedExecutionStatusRunning,
		StartedAt: now,
	})
	api.trackExecutionStart(&TrackedWorkflowExecution{
		ExecutionID: orphanID, SessionID: sessionID,
		Source: trackedExecutionSourceWorkshopBackground, Kind: "workflow_builder_task",
		Name: "[Execute] Run Today's Actions", WorkspacePath: "Workflow/social-media",
		Status: trackedExecutionStatusRunning, StartedAt: now,
	})

	before := api.listRunningWorkflowExecutions("")
	foundOrphan := false
	for _, exec := range before {
		foundOrphan = foundOrphan || exec.QueryID == orphanID
	}
	if len(before) != 2 || !foundOrphan {
		t.Fatalf("precondition: Global Monitor running entries = %#v, want parent plus orphan %q", before, orphanID)
	}

	notifier := &workshopExecutionBgNotifier{api: api, sessionID: sessionID}
	notifier.OnExecutionComplete(parentID, "full workflow", "done", nil, nil)

	tracked := api.trackedWorkflowExecutions[orphanID]
	if tracked == nil || tracked.Status != trackedExecutionStatusFailed || tracked.CompletedAt == nil {
		t.Fatalf("orphan tracked execution was not settled: %#v", tracked)
	}
	if got := api.listRunningWorkflowExecutions(""); len(got) != 0 {
		t.Fatalf("Global Monitor still reports completed workflow as running: %#v", got)
	}
}

// TestUnnotifiedChildHoldsTurnOnlyBriefly pins both directions of the bounded
// continuation hand-off (PLAT-117).
//
// A child that just finished must still hold the turn open — that is the race
// the hold exists for. A child that finished long ago and was never notified
// must NOT, because unbounded that turns every stuck-notification path into a
// permanently unfinishable turn rather than a delay.
func TestUnnotifiedChildHoldsTurnOnlyBriefly(t *testing.T) {
	for name, tc := range map[string]struct {
		finishedAgo     time.Duration
		wantRunningKids int
	}{
		"just finished — hand-off may be in flight": {time.Second, 1},
		"finished well inside the grace":            {continuationHandoffGrace / 2, 1},
		"finished long ago and never notified":      {continuationHandoffGrace + time.Minute, 0},
	} {
		t.Run(name, func(t *testing.T) {
			api := lifecycleTestAPI()
			const sessionID = "session-handoff"
			start := time.Now().UTC().Add(-2 * continuationHandoffGrace)
			rootDone := start.Add(time.Second)
			api.trackedWorkflowExecutions["root"] = &TrackedWorkflowExecution{
				ExecutionID: "root", SessionID: sessionID, Source: trackedExecutionSourceConversationTurn,
				Status: trackedExecutionStatusCompleted, StartedAt: start, CompletedAt: &rootDone,
			}
			completedAt := time.Now().Add(-tc.finishedAgo)
			// A real delegation, not a progress mirror: this is the notification
			// hold under test, not the mirror exclusion.
			api.bgAgentRegistry.Register(sessionID, &BackgroundAgent{
				ID: "delegation-child", ParentExecutionID: "root", SessionID: sessionID,
				Kind: "delegation", Status: BGAgentCompleted, CreatedAt: start, CompletedAt: &completedAt,
			})

			state := api.conversationTurnTreeSnapshot("root")
			if state.RunningChildren != tc.wantRunningKids {
				t.Fatalf("RunningChildren = %d, want %d", state.RunningChildren, tc.wantRunningKids)
			}
			if terminal := state.terminal(); terminal != (tc.wantRunningKids == 0) {
				t.Fatalf("terminal() = %v, want %v", terminal, tc.wantRunningKids == 0)
			}
		})
	}
}

// TestNotifiedChildNeverHoldsTurn is the control: once the continuation has
// actually been handed off, the child stops holding the turn immediately,
// regardless of the grace window.
func TestNotifiedChildNeverHoldsTurn(t *testing.T) {
	api := lifecycleTestAPI()
	const sessionID = "session-notified"
	now := time.Now().UTC()
	doneAt := now.Add(time.Second)
	api.trackedWorkflowExecutions["root"] = &TrackedWorkflowExecution{
		ExecutionID: "root", SessionID: sessionID, Source: trackedExecutionSourceConversationTurn,
		Status: trackedExecutionStatusCompleted, StartedAt: now, CompletedAt: &doneAt,
	}
	child := &BackgroundAgent{
		ID: "delegation-child", ParentExecutionID: "root", SessionID: sessionID,
		Kind: "delegation", Status: BGAgentCompleted, CreatedAt: now, CompletedAt: &doneAt,
	}
	child.finishCompletionNotification(true)
	api.bgAgentRegistry.Register(sessionID, child)

	if state := api.conversationTurnTreeSnapshot("root"); !state.terminal() {
		t.Fatalf("a notified terminal child must not hold the turn open: %+v", state)
	}
}
