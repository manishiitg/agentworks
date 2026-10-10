package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

func TestNormalizeSessionLifecycleStatus(t *testing.T) {
	tests := map[string]sessionLifecycleStatus{
		"running":                   sessionLifecycleRunning,
		"active":                    sessionLifecycleRunning,
		"success":                   sessionLifecycleCompleted,
		"completed":                 sessionLifecycleCompleted,
		"error":                     sessionLifecycleFailed,
		"failed":                    sessionLifecycleFailed,
		legacyCanceledBritishStatus: sessionLifecycleStopped,
		"stopped":                   sessionLifecycleStopped,
		"inactive":                  sessionLifecycleInactive,
	}
	for input, want := range tests {
		if got := normalizeSessionLifecycleStatus(input); got != want {
			t.Fatalf("normalizeSessionLifecycleStatus(%q) = %q, want %q", input, got, want)
		}
	}
	if !isStoppedSessionStatus("failed") || !isStoppedSessionStatus("error") {
		t.Fatal("both failed and legacy error statuses must be terminal")
	}
}

func TestActiveSessionReadsReturnSnapshots(t *testing.T) {
	waitingSince := time.Now().Add(-time.Minute)
	api := &StreamingAPI{
		activeSessions: map[string]*ActiveSessionInfo{
			"session-1": {
				SessionID:    "session-1",
				Status:       "running",
				CreatedAt:    time.Now().Add(-time.Hour),
				LastActivity: time.Now(),
				WaitingSince: &waitingSince,
			},
		},
	}

	snapshot, ok := api.getActiveSession("session-1")
	if !ok {
		t.Fatal("expected active session")
	}
	snapshot.Status = "stopped"
	*snapshot.WaitingSince = time.Time{}
	if api.activeSessions["session-1"].Status != "running" {
		t.Fatal("mutating a returned session changed shared state")
	}
	if api.activeSessions["session-1"].WaitingSince.IsZero() {
		t.Fatal("nested pointer was not copied")
	}

	all := api.getAllActiveSessions()
	if len(all) != 1 {
		t.Fatalf("getAllActiveSessions returned %d sessions, want 1", len(all))
	}
	all[0].Status = "failed"
	if api.activeSessions["session-1"].Status != "running" {
		t.Fatal("mutating an active-session list item changed shared state")
	}
}

func TestInactiveCleanupPreservesLiveTurnAndMarksTrulyIdleSession(t *testing.T) {
	now := time.Now()
	liveCtx, liveCancel := context.WithCancel(context.Background())
	defer liveCancel()
	api := &StreamingAPI{
		runtimeCoordinator: NewRuntimeCoordinator(),
		activeSessions: map[string]*ActiveSessionInfo{
			"live": {
				SessionID: "live", Status: "running", CreatedAt: now.Add(-time.Hour), LastActivity: now.Add(-20 * time.Minute),
			},
			"idle": {
				SessionID: "idle", Status: "running", CreatedAt: now.Add(-time.Hour), LastActivity: now.Add(-20 * time.Minute),
			},
		},
		agentCancelFuncs:         map[string]context.CancelFunc{"live": liveCancel},
		pendingCompletions:       map[string][]string{},
		completionRetryScheduled: map[string]bool{},
		bgAgentRegistry:          NewBackgroundAgentRegistry(),
	}
	_ = liveCtx

	api.cleanupInactiveSessionsAt(now)

	if got := api.activeSessions["live"].Status; got != "running" {
		t.Fatalf("live foreground turn status = %q, want running", got)
	}
	if got := api.activeSessions["idle"].Status; got != "inactive" {
		t.Fatalf("truly idle session status = %q, want inactive", got)
	}
	observed, ok := api.runtimeCoordinator.Snapshot("idle")
	if !ok || observed.RawSessionStatus != "inactive" {
		t.Fatalf("inactive transition was not observed immediately: %#v, ok=%v", observed, ok)
	}
	active := api.getAllActiveSessions()
	if len(active) != 2 {
		t.Fatalf("active sessions count = %d, want retained live plus inactive session", len(active))
	}
	foundLive := false
	for _, session := range active {
		if session.SessionID == "live" {
			foundLive = true
		}
	}
	if !foundLive {
		t.Fatal("old session with a live foreground turn was hidden from active-session list")
	}
}

func TestInactiveCleanupEvictsRuntimeCoordinatorRecord(t *testing.T) {
	now := time.Now()
	coordinator := NewRuntimeCoordinator()
	coordinator.Observe(RuntimeSnapshot{SessionID: "expired", Phase: runtimePhaseCompleted})
	api := &StreamingAPI{
		runtimeCoordinator: coordinator,
		activeSessions: map[string]*ActiveSessionInfo{
			"expired": {
				SessionID:    "expired",
				Status:       "completed",
				CreatedAt:    now.Add(-26 * time.Hour),
				LastActivity: now.Add(-25 * time.Hour),
			},
		},
	}

	api.cleanupInactiveSessionsAt(now)

	if _, ok := api.activeSessions["expired"]; ok {
		t.Fatal("expired active session was not removed")
	}
	if _, ok := coordinator.Snapshot("expired"); ok {
		t.Fatal("expired session left a runtime coordinator record behind")
	}
}

func TestScheduleStopInvalidatesLateCompletion(t *testing.T) {
	canceled := false
	startedAt := time.Now().Add(-time.Minute)
	runtimeKey := workflowScheduleRuntimeKey("/tmp/workflow", "schedule-1")
	svc := &SchedulerService{
		runtimeStates: map[string]*ScheduleRuntimeState{
			runtimeKey: {
				ActiveRunID: "run-1", LastStatus: "running", LastRunAt: &startedAt,
			},
		},
		runCancels: map[string]context.CancelFunc{"run-1": func() { canceled = true }},
	}

	svc.StopRunningJob("schedule-1")
	state := svc.getRuntimeStateByKey(runtimeKey)
	if state.LastStatus != "stopped" || state.LastError != "stopped by user" {
		t.Fatalf("stopped state = %#v", state)
	}
	if !canceled {
		t.Fatal("StopRunningJob did not cancel the run context")
	}
	sctx := &ScheduleContext{WorkspacePath: "/tmp/workflow", Schedule: WorkflowSchedule{ID: "schedule-1"}}
	if _, err := svc.runJob(context.Background(), sctx, "run-1"); !errors.Is(err, errWorkshopSequenceInterrupted) {
		t.Fatalf("late run error = %v, want errWorkshopSequenceInterrupted", err)
	}
	state = svc.getRuntimeStateByKey(runtimeKey)
	if state.LastStatus != "stopped" {
		t.Fatalf("late completion overwrote stopped state with %q", state.LastStatus)
	}
}

func TestScheduleRuntimeReadReturnsSnapshot(t *testing.T) {
	lastRun := time.Now()
	duration := int64(10)
	svc := &SchedulerService{runtimeStates: map[string]*ScheduleRuntimeState{
		"schedule-1": {LastStatus: "running", LastRunAt: &lastRun, LastDurationMs: &duration},
	}}

	snapshot := svc.getRuntimeStateByKey("schedule-1")
	snapshot.LastStatus = "stopped"
	*snapshot.LastRunAt = time.Time{}
	*snapshot.LastDurationMs = 999
	state := svc.runtimeStates["schedule-1"]
	if state.LastStatus != "running" || state.LastRunAt.IsZero() || *state.LastDurationMs != 10 {
		t.Fatalf("mutating schedule snapshot changed shared state: %#v", state)
	}
}

func TestScheduleReconciliationPreservesStoppedStatus(t *testing.T) {
	now := time.Now()
	svc := &SchedulerService{api: &StreamingAPI{
		activeSessions: map[string]*ActiveSessionInfo{
			"schedule-session": {SessionID: "schedule-session", Status: "stopped", LastActivity: now},
		},
	}}
	status, _, terminal := svc.reconciledScheduleRunStatus(&ScheduleRunEntry{
		SessionID: "schedule-session",
		StartedAt: now.Add(-time.Minute),
	}, now)
	if !terminal || status != "stopped" {
		t.Fatalf("reconciled stopped session = status %q terminal=%t", status, terminal)
	}
}

func TestRuntimeEndpointsUseSameDisplayStatus(t *testing.T) {
	store := events.NewEventStore(10)
	defer store.Stop()
	const sessionID = "session-busy"
	now := time.Now()
	api := &StreamingAPI{
		runtimeCoordinator: NewRuntimeCoordinator(),
		eventStore:         store,
		bgAgentRegistry:    NewBackgroundAgentRegistry(),
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "running", CreatedAt: now, LastActivity: now},
		},
		sessionBusy:      map[string]bool{sessionID: true},
		sessionBusySince: map[string]time.Time{sessionID: now},
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			"workflow-1": {
				ExecutionID: "workflow-1", SessionID: sessionID,
				Source: trackedExecutionSourceWorkflowRun, Kind: "workflow", Status: trackedExecutionStatusRunning,
				StartedAt: now,
			},
		},
	}

	eventsRequest := httptest.NewRequest("GET", "/api/sessions/"+sessionID+"/events?since=0", nil)
	eventsRequest = mux.SetURLVars(eventsRequest, map[string]string{"session_id": sessionID})
	eventsResponse := httptest.NewRecorder()
	api.handleGetSessionEvents(eventsResponse, eventsRequest)
	var pollingBody GetEventsResponse
	if err := json.Unmarshal(eventsResponse.Body.Bytes(), &pollingBody); err != nil {
		t.Fatalf("decode polling response: %v", err)
	}

	statusRequest := httptest.NewRequest("GET", "/api/sessions/"+sessionID+"/status", nil)
	statusRequest = mux.SetURLVars(statusRequest, map[string]string{"session_id": sessionID})
	statusResponse := httptest.NewRecorder()
	api.handleGetSessionStatus(statusResponse, statusRequest)
	var statusBody struct {
		DisplayStatus string          `json:"display_status"`
		RuntimeState  RuntimeSnapshot `json:"runtime_state"`
	}
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &statusBody); err != nil {
		t.Fatalf("decode status response: %v", err)
	}

	activeRequest := httptest.NewRequest("GET", "/api/sessions/active", nil)
	activeResponse := httptest.NewRecorder()
	api.handleGetActiveSessions(activeResponse, activeRequest)
	var activeBody GetActiveSessionsResponse
	if err := json.Unmarshal(activeResponse.Body.Bytes(), &activeBody); err != nil {
		t.Fatalf("decode active-session response: %v", err)
	}
	if len(activeBody.ActiveSessions) != 1 || activeBody.ActiveSessions[0].RuntimeState == nil {
		t.Fatalf("active-session runtime state missing: %#v", activeBody.ActiveSessions)
	}

	tree := api.buildSessionExecutionTree(api.activeSessions[sessionID])
	if tree == nil || tree.RuntimeState == nil {
		t.Fatal("execution-tree runtime state missing")
	}
	runningWorkflows := api.listRunningWorkflowExecutions(GetDefaultUserID())
	if len(runningWorkflows) != 1 || runningWorkflows[0].RuntimeState == nil {
		t.Fatalf("running-workflow runtime state missing: %#v", runningWorkflows)
	}

	if pollingBody.DisplayStatus != sessionExecutionDisplayBusy || statusBody.DisplayStatus != pollingBody.DisplayStatus {
		t.Fatalf("display status mismatch: polling=%q status=%q", pollingBody.DisplayStatus, statusBody.DisplayStatus)
	}
	wantRevision := pollingBody.RuntimeState.Revision
	if wantRevision == 0 || statusBody.RuntimeState.Revision != wantRevision ||
		activeBody.ActiveSessions[0].RuntimeState.Revision != wantRevision || tree.RuntimeState.Revision != wantRevision ||
		runningWorkflows[0].RuntimeState.Revision != wantRevision {
		t.Fatalf("runtime revision mismatch: polling=%d status=%d active=%d tree=%d workflow=%d",
			wantRevision, statusBody.RuntimeState.Revision,
			activeBody.ActiveSessions[0].RuntimeState.Revision, tree.RuntimeState.Revision,
			runningWorkflows[0].RuntimeState.Revision)
	}
	sseRuntime, _ := api.authoritativeRuntimeSnapshot(sessionID)
	sseStatus := sessionDisplayStatusFromRuntime(sseRuntime)
	sseBody := sseStatusMessage{DisplayStatus: sseStatus.Status, RuntimeState: &sseRuntime}
	if sseBody.DisplayStatus != pollingBody.DisplayStatus {
		t.Fatalf("SSE display status=%q, polling=%q", sseBody.DisplayStatus, pollingBody.DisplayStatus)
	}
	if sseBody.RuntimeState.Revision != wantRevision {
		t.Fatalf("SSE runtime revision=%d, want %d", sseBody.RuntimeState.Revision, wantRevision)
	}
}

// TestReconcilerDoesNotCallAnInFlightRunSuccessful covers the bug that made
// every other run symptom unreadable.
//
// A scheduled run is many sequential turns sharing one session, and between
// them the session reads "completed". Finalizing on that stamped the whole run
// success while it was on turn 1 of 10: latency-report's 2026-07-31 02:30 run was
// recorded success/84,599 ms while its log shows turns running for another hour.
// Because the record never corrected itself, runs later killed by a restart also
// read as successful, and the workflow looked healthy while its digest had not
// shipped for days.
func TestReconcilerDoesNotCallAnInFlightRunSuccessful(t *testing.T) {
	now := time.Now()
	svc := &SchedulerService{api: &StreamingAPI{
		activeSessions: map[string]*ActiveSessionInfo{
			"run-session": {SessionID: "run-session", Status: "completed", LastActivity: now},
		},
	}}

	// 84 seconds in — exactly where latency-report was stamped success.
	status, _, terminal := svc.reconciledScheduleRunStatus(&ScheduleRunEntry{
		SessionID: "run-session",
		StartedAt: now.Add(-84 * time.Second),
	}, now)
	if terminal {
		t.Fatalf("a run between turns was finalized as %q; the scheduler had not recorded a result yet", status)
	}

	// A long real run must also survive: healthy latency-report runs reach 2.5 hours.
	if _, _, terminal := svc.reconciledScheduleRunStatus(&ScheduleRunEntry{
		SessionID: "run-session",
		StartedAt: now.Add(-150 * time.Minute),
	}, now); terminal {
		t.Fatal("a 2.5-hour run still between turns was finalized")
	}

	// Past the abandoned window it is reported interrupted — never success,
	// because success is the scheduler's verdict and it never recorded one.
	status, msg, terminal := svc.reconciledScheduleRunStatus(&ScheduleRunEntry{
		SessionID: "run-session",
		StartedAt: now.Add(-scheduleRunAbandonedAfter - time.Minute),
	}, now)
	if !terminal || status == "success" {
		t.Fatalf("an abandoned run reconciled to status %q terminal=%t; must never be success", status, terminal)
	}
	if !strings.Contains(msg, "never recorded a terminal result") {
		t.Fatalf("abandoned-run message does not say what happened: %q", msg)
	}
}
