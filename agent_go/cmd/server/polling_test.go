package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
)

func TestGetSessionEventsCursorOnlyOmitsRawEvents(t *testing.T) {
	store := events.NewEventStore(100)
	defer store.Stop()
	const sessionID = "cursor-only-session"
	for i := 0; i < 3; i++ {
		store.AddEvent(sessionID, events.Event{
			ID:        fmt.Sprintf("event-%d", i),
			Type:      "user_message",
			Timestamp: time.Now(),
			SessionID: sessionID,
		})
	}
	api := &StreamingAPI{
		eventStore:         store,
		runtimeCoordinator: NewRuntimeCoordinator(),
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "completed", CreatedAt: time.Now()},
		},
	}

	req := httptest.NewRequest("GET", "/api/sessions/"+sessionID+"/events?cursor_only=1", nil)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	w := httptest.NewRecorder()
	api.handleGetSessionEvents(w, req)

	if w.Code != 200 {
		t.Fatalf("cursor-only status = %d, body=%s", w.Code, w.Body.String())
	}
	var response GetEventsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode cursor-only response: %v", err)
	}
	if len(response.Events) != 0 {
		t.Fatalf("cursor-only response serialized %d raw events", len(response.Events))
	}
	if response.LastProcessedIndex != 2 {
		t.Fatalf("last_processed_index = %d, want 2", response.LastProcessedIndex)
	}
}

func TestGetSessionEventsReadsDurableChatBySequence(t *testing.T) {
	journal, err := events.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := events.NewEventStore(2)
	defer store.Stop()
	store.SetDurableJournal(journal)
	const sessionID = "durable-chat-session"
	if err := store.SetSessionPersistenceClass(sessionID, events.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		store.AddEvent(sessionID, events.Event{ID: fmt.Sprintf("event-%d", i), Type: "user_message", Timestamp: time.Now()})
	}
	api := &StreamingAPI{
		eventStore:         store,
		runtimeCoordinator: NewRuntimeCoordinator(),
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "completed", CreatedAt: time.Now()},
		},
	}

	request := func(query string) GetEventsResponse {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/sessions/"+sessionID+"/events?durable_chat=1&"+query, nil)
		req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
		w := httptest.NewRecorder()
		api.handleGetSessionEvents(w, req)
		if w.Code != 200 {
			t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
		}
		var response GetEventsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}

	tail := request("limit=2")
	if len(tail.Events) != 2 || tail.Events[0].ID != "event-4" || tail.Events[1].ID != "event-5" || !tail.HasMore || tail.OldestSequence != 4 || tail.LatestSequence != 5 {
		t.Fatalf("tail = %+v", tail)
	}
	older := request("limit=2&before_sequence=4")
	if len(older.Events) != 2 || older.Events[0].ID != "event-2" || older.Events[1].ID != "event-3" || !older.HasMore {
		t.Fatalf("older = %+v", older)
	}
	newer := request("since=3")
	if len(newer.Events) != 2 || newer.Events[0].ID != "event-4" || newer.Events[1].ID != "event-5" || newer.LastProcessedIndex != 5 {
		t.Fatalf("newer = %+v", newer)
	}
	caughtUp := request("since=5")
	if len(caughtUp.Events) != 0 || caughtUp.LastProcessedIndex != 5 || caughtUp.LatestSequence != 5 || caughtUp.HasMore {
		t.Fatalf("empty forward page must keep the cursor, not reset it to 0: %+v", caughtUp)
	}
	ahead := request("since=9")
	if len(ahead.Events) != 0 || ahead.LastProcessedIndex != 5 {
		t.Fatalf("a cursor ahead of the journal must clamp to the tip: %+v", ahead)
	}
	fromStart := request("since=0")
	if len(fromStart.Events) != 5 || fromStart.Events[0].ID != "event-1" || fromStart.LastProcessedIndex != 5 {
		t.Fatalf("since=0 = %+v", fromStart)
	}
}

func TestGetSessionEventsCannotPromoteExecutionToDurableChat(t *testing.T) {
	store := events.NewEventStore(10)
	defer store.Stop()
	const sessionID = "workflow-step-session"
	if err := store.SetSessionPersistenceClass(sessionID, events.SessionPersistenceExecution); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{
		eventStore:         store,
		runtimeCoordinator: NewRuntimeCoordinator(),
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "running", CreatedAt: time.Now()},
		},
	}
	req := httptest.NewRequest("GET", "/api/sessions/"+sessionID+"/events?durable_chat=1&limit=10", nil)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	w := httptest.NewRecorder()
	api.handleGetSessionEvents(w, req)
	if w.Code != 409 {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
}

func TestActiveSessionsIncludesTrackedWorkflowWithoutChatRow(t *testing.T) {
	const sessionID = "workflow-after-restart"
	startedAt := time.Now().Add(-time.Minute).UTC()
	api := &StreamingAPI{
		runtimeCoordinator: NewRuntimeCoordinator(),
		activeSessions:     map[string]*ActiveSessionInfo{},
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			"workflow-1": {
				ExecutionID: "workflow-1", SessionID: sessionID,
				Source: trackedExecutionSourceWorkflowRun, Kind: "workflow", Status: trackedExecutionStatusRunning,
				PresetName: "Daily QA", WorkspacePath: "Workflow/daily-qa", StartedAt: startedAt,
			},
		},
	}

	recorder := httptest.NewRecorder()
	api.handleGetActiveSessions(recorder, httptest.NewRequest("GET", "/api/sessions/active", nil))
	var response GetActiveSessionsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode active sessions: %v", err)
	}
	if len(response.ActiveSessions) != 1 {
		t.Fatalf("active sessions = %#v, want one tracked workflow", response.ActiveSessions)
	}
	session := response.ActiveSessions[0]
	if session.SessionID != sessionID || session.WorkflowName != "Daily QA" || session.RuntimeState == nil {
		t.Fatalf("tracked workflow was not synthesized into runtime index: %#v", session)
	}
	if session.RuntimeState.Phase != runtimePhaseRunning || session.DisplayStatus != sessionExecutionDisplayBusy {
		t.Fatalf("synthesized runtime state = %#v display=%q", session.RuntimeState, session.DisplayStatus)
	}
}

func TestActiveSessionsCarriesPhaseNameForTrackedWorkflowWithoutChatRow(t *testing.T) {
	const sessionID = "workflow-phase-after-restart"
	startedAt := time.Now().Add(-time.Minute).UTC()
	api := &StreamingAPI{
		runtimeCoordinator: NewRuntimeCoordinator(),
		activeSessions:     map[string]*ActiveSessionInfo{},
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			"workflow-1": {
				ExecutionID: "workflow-1", SessionID: sessionID,
				Source: trackedExecutionSourceWorkflowRun, Kind: "workflow", Status: trackedExecutionStatusRunning,
				WorkspacePath: "Workflow/daily-qa", StartedAt: startedAt,
				PhaseID: "step-3", PhaseName: "Validate output",
			},
		},
	}

	recorder := httptest.NewRecorder()
	api.handleGetActiveSessions(recorder, httptest.NewRequest("GET", "/api/sessions/active", nil))
	var response GetActiveSessionsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode active sessions: %v", err)
	}
	if len(response.ActiveSessions) != 1 {
		t.Fatalf("active sessions = %#v, want one tracked workflow", response.ActiveSessions)
	}
	session := response.ActiveSessions[0]
	if session.PhaseID != "step-3" || session.PhaseName != "Validate output" {
		t.Fatalf("phase fields not carried through synthesis: phase_id=%q phase_name=%q", session.PhaseID, session.PhaseName)
	}
}

func TestBuildActiveSessionInfoSummaryKeepsCompletedStatusForBackgroundAgents(t *testing.T) {
	const sessionID = "session-bg-completed"

	api := &StreamingAPI{
		bgAgentRegistry: NewBackgroundAgentRegistry(),
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			"bg-agent": {
				ExecutionID:   "bg-agent",
				SessionID:     sessionID,
				Source:        trackedExecutionSourceWorkshopBackground,
				Status:        trackedExecutionStatusRunning,
				Name:          "Background follow-up",
				WorkspacePath: "Workflow/test",
				StartedAt:     time.Now(),
			},
		},
	}

	api.bgAgentRegistry.Register(sessionID, &BackgroundAgent{
		ID:        "bg-agent",
		Name:      "Background follow-up",
		SessionID: sessionID,
		Status:    BGAgentRunning,
		CreatedAt: time.Now(),
		Metadata: map[string]string{
			"workflow_path": "Workflow/test",
		},
	})

	summary := api.buildActiveSessionInfoSummary(&ActiveSessionInfo{
		SessionID: sessionID,
		Status:    "completed",
		CreatedAt: time.Now(),
	})

	if summary.Status != "completed" {
		t.Fatalf("expected completed foreground status, got %q", summary.Status)
	}
	if !summary.HasRunningBackgroundAgents {
		t.Fatal("expected running background agent flag")
	}
	if summary.RunningBackgroundAgentCount != 1 {
		t.Fatalf("expected 1 running background agent, got %d", summary.RunningBackgroundAgentCount)
	}
	if summary.CurrentExecutionName != "Background follow-up" {
		t.Fatalf("expected current background name, got %q", summary.CurrentExecutionName)
	}
}

func TestBuildActiveSessionInfoSummaryKeepsOldRunningBackgroundAgentActive(t *testing.T) {
	const sessionID = "session-bg-old-running"

	api := &StreamingAPI{
		bgAgentRegistry: NewBackgroundAgentRegistry(),
	}
	api.bgAgentRegistry.Register(sessionID, &BackgroundAgent{
		ID:        "bg-old-running",
		Name:      "Old background follow-up",
		SessionID: sessionID,
		Status:    BGAgentRunning,
		CreatedAt: time.Now().Add(-time.Hour),
	})

	summary := api.buildActiveSessionInfoSummary(&ActiveSessionInfo{
		SessionID: sessionID,
		Status:    "completed",
		CreatedAt: time.Now().Add(-time.Hour),
	})

	if !summary.HasRunningBackgroundAgents {
		t.Fatal("old running background agent should keep the session active")
	}
	if summary.RunningBackgroundAgentCount != 1 {
		t.Fatalf("expected 1 running background agent, got %d", summary.RunningBackgroundAgentCount)
	}
	if summary.CurrentExecutionName != "Old background follow-up" {
		t.Fatalf("expected current execution name for old running agent, got %q", summary.CurrentExecutionName)
	}
}

func TestBuildActiveSessionInfoSummaryReportsRetainedTmuxWithoutBusyStatus(t *testing.T) {
	const sessionID = "session-retained-tmux"
	store := terminals.NewStore()
	store.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(time.Now(), sessionID, "main:"+sessionID, "mlp-codex-cli-retained"))
	api := &StreamingAPI{terminalStore: store}

	summary := api.buildActiveSessionInfoSummary(&ActiveSessionInfo{
		SessionID: sessionID,
		Status:    "completed",
		CreatedAt: time.Now(),
	})
	if !summary.HasRetainedTmuxSession {
		t.Fatal("expected active tmux pane to be reported even when session status is completed")
	}

	if _, ok := store.MarkCompleted(sessionID + ":main:" + sessionID); !ok {
		t.Fatal("expected terminal to be marked completed")
	}
	summary = api.buildActiveSessionInfoSummary(&ActiveSessionInfo{
		SessionID: sessionID,
		Status:    "completed",
		CreatedAt: time.Now(),
	})
	if summary.HasRetainedTmuxSession {
		t.Fatal("completed terminal snapshot should not be reported as retained tmux")
	}
}

func TestShouldCompleteIdleForegroundSessionDoesNotCompleteBusySession(t *testing.T) {
	const sessionID = "session-busy-foreground"
	api := &StreamingAPI{
		sessionBusy:      map[string]bool{sessionID: true},
		sessionBusySince: map[string]time.Time{sessionID: time.Now().Add(-time.Minute)},
	}

	if api.shouldCompleteIdleForegroundSession(sessionID, "running", false) {
		t.Fatal("stale busy foreground session should not be completed by passive status polling")
	}
	if !api.isSessionBusy(sessionID) {
		t.Fatal("status polling should not clear the busy flag")
	}
}

func TestAutoNotificationDoesNotClearStaleBusyWhenCodingTmuxLooksBusy(t *testing.T) {
	const sessionID = "session-busy-tmux"
	store := terminals.NewStore()
	store.HandleEvent(sessionID, terminalRouteChunkEvent(
		sessionID,
		"workflow-step:review-plan",
		"mlp-cursor-cli-int-test",
		"Cursor Agent\n\n ⠰⠰ Composing  1.87k tokens\n\n  → Add a follow-up  ctrl+c to stop\n",
		1,
	))
	api := &StreamingAPI{
		terminalStore:    store,
		sessionBusy:      map[string]bool{sessionID: true},
		sessionBusySince: map[string]time.Time{sessionID: time.Now().Add(-autoNotificationStaleBusyAfter - time.Second)},
	}

	if !api.isSessionBusyForAutoNotification(sessionID) {
		t.Fatal("busy coding tmux pane should keep auto-notification serialized behind foreground turn")
	}
	if !api.isSessionBusy(sessionID) {
		t.Fatal("auto-notification busy check should not clear busy while tmux pane looks active")
	}
}

// PLAT-131. A running scheduled Pulse session resolved its own identity at
// registration (server.go sets WorkspacePath and derives WorkflowName from it
// together), but enrichment then overwrote PresetQueryID/PresetName/
// WorkspacePath unconditionally from a tracked execution. A workflow-builder
// background execution legitimately carries none of those, so all three were
// erased while WorkflowName — guarded by `if active.PresetName != ""` — stayed.
//
// The live rtslatency session showed exactly that asymmetry:
//
//	workflow_name: "rtslatency"   (survived)
//	workspace_path: absent        (erased)
//	preset_query_id: absent       (erased)
//
// The frontend resolves a session's workflow by preset_query_id or
// workspace_path (findWorkflowPresetForSession). With both erased, resolution
// returned undefined, the canonical workflow-navigation path was skipped, and
// clicking the activity pill opened a Schedule tab under whichever workflow
// was already on screen instead of switching to rtslatency.
func TestBuildActiveSessionInfoSummaryKeepsSessionIdentityWhenTrackedExecutionOmitsIt(t *testing.T) {
	const sessionID = "schedule-cron--identity_1787009443095002000"

	api := &StreamingAPI{
		runtimeCoordinator: NewRuntimeCoordinator(),
		activeSessions:     map[string]*ActiveSessionInfo{},
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			// A workflow-builder background execution: running, but carrying no
			// preset or workspace identity of its own.
			"bg-pulse-review": {
				ExecutionID: "bg-pulse-review", SessionID: sessionID,
				Source: trackedExecutionSourceWorkshopBackground, Kind: "workflow_builder_task",
				Status: trackedExecutionStatusRunning, StartedAt: time.Now().Add(-time.Minute).UTC(),
			},
		},
	}

	summary := api.buildActiveSessionInfoSummary(&ActiveSessionInfo{
		SessionID:     sessionID,
		Status:        "running",
		CreatedAt:     time.Now(),
		AgentMode:     "workflow_phase",
		WorkspacePath: "Workflow/rtslatency",
		PresetQueryID: "wf_rtslatency",
		PresetName:    "rtslatency",
		WorkflowName:  "rtslatency",
		WorkflowLabel: "rtslatency",
	})

	if summary.WorkspacePath != "Workflow/rtslatency" {
		t.Fatalf("workspace_path = %q, want it preserved: the frontend cannot resolve the workflow without it", summary.WorkspacePath)
	}
	if summary.PresetQueryID != "wf_rtslatency" {
		t.Fatalf("preset_query_id = %q, want it preserved: it is the primary preset-resolution key", summary.PresetQueryID)
	}
	if summary.PresetName != "rtslatency" {
		t.Fatalf("preset_name = %q, want it preserved", summary.PresetName)
	}
	if summary.WorkflowName != "rtslatency" {
		t.Fatalf("workflow_name = %q, want it preserved", summary.WorkflowName)
	}
}

// The guard must not become "never update": a tracked execution that DOES
// carry identity is still the more specific record and must win.
func TestBuildActiveSessionInfoSummaryStillAdoptsTrackedExecutionIdentityWhenPresent(t *testing.T) {
	const sessionID = "session-tracked-identity-wins"

	api := &StreamingAPI{
		runtimeCoordinator: NewRuntimeCoordinator(),
		activeSessions:     map[string]*ActiveSessionInfo{},
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			"workflow-run": {
				ExecutionID: "workflow-run", SessionID: sessionID,
				Source: trackedExecutionSourceWorkflowRun, Kind: "workflow",
				Status: trackedExecutionStatusRunning, StartedAt: time.Now().Add(-time.Minute).UTC(),
				PresetQueryID: "wf_actual", PresetName: "Actual Workflow",
				WorkspacePath: "Workflow/actual",
			},
		},
	}

	summary := api.buildActiveSessionInfoSummary(&ActiveSessionInfo{
		SessionID:     sessionID,
		Status:        "running",
		CreatedAt:     time.Now(),
		WorkspacePath: "Workflow/stale",
		PresetQueryID: "wf_stale",
		PresetName:    "Stale",
	})

	if summary.WorkspacePath != "Workflow/actual" {
		t.Fatalf("workspace_path = %q, want the tracked execution's value to win", summary.WorkspacePath)
	}
	if summary.PresetQueryID != "wf_actual" {
		t.Fatalf("preset_query_id = %q, want the tracked execution's value to win", summary.PresetQueryID)
	}
	if summary.PresetName != "Actual Workflow" {
		t.Fatalf("preset_name = %q, want the tracked execution's value to win", summary.PresetName)
	}
}

func TestGetSessionEventsCompactViewIsOptInAndDefaultUnchanged(t *testing.T) {
	journal, err := events.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := events.NewEventStore(2)
	defer store.Stop()
	store.SetDurableJournal(journal)
	const sessionID = "compact-chat-session"
	if err := store.SetSessionPersistenceClass(sessionID, events.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	for turn := 1; turn <= 3; turn++ {
		store.AddEvent(sessionID, events.Event{ID: fmt.Sprintf("u%d", turn), Type: "user_message", Timestamp: time.Now()})
		for i := 0; i < 3; i++ {
			store.AddEvent(sessionID, events.Event{ID: fmt.Sprintf("t%d-%d", turn, i), Type: "tool_call_end", Timestamp: time.Now()})
		}
		store.AddEvent(sessionID, events.Event{ID: fmt.Sprintf("a%d", turn), Type: "unified_completion", Timestamp: time.Now()})
	}
	api := &StreamingAPI{
		eventStore:         store,
		runtimeCoordinator: NewRuntimeCoordinator(),
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "completed", CreatedAt: time.Now()},
		},
	}
	request := func(query string) GetEventsResponse {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/sessions/"+sessionID+"/events?durable_chat=1&"+query, nil)
		req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
		w := httptest.NewRecorder()
		api.handleGetSessionEvents(w, req)
		if w.Code != 200 {
			t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
		}
		var response GetEventsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	ids := func(response GetEventsResponse) []string {
		out := []string{}
		for _, event := range response.Events {
			out = append(out, event.ID)
		}
		return out
	}

	full := request("limit=100")
	if len(full.Events) != 15 {
		t.Fatalf("default path must keep every row: %v", ids(full))
	}
	again := request("limit=100&view=other")
	fullEvents, _ := json.Marshal(full.Events)
	againEvents, _ := json.Marshal(again.Events)
	if string(fullEvents) != string(againEvents) || full.HasMore != again.HasMore || full.OldestSequence != again.OldestSequence {
		t.Fatal("an unknown view value must leave the default response unchanged")
	}

	compact := request("limit=40&view=messages")
	want := []string{"u1", "a1", "u2", "a2", "u3", "t3-0", "t3-1", "t3-2", "a3"}
	if fmt.Sprint(ids(compact)) != fmt.Sprint(want) || compact.HasMore || compact.OldestSequence != 1 || compact.LatestSequence != 15 {
		t.Fatalf("compact = %v hasMore=%v oldest=%d latest=%d", ids(compact), compact.HasMore, compact.OldestSequence, compact.LatestSequence)
	}
	older := request("limit=2&view=messages&before_sequence=11")
	// the page is cut at the turn start, and u1/a1 remain behind it.
	if fmt.Sprint(ids(older)) != fmt.Sprint([]string{"u2", "a2"}) || !older.HasMore {
		t.Fatalf("older = %v hasMore=%v", ids(older), older.HasMore)
	}
}
