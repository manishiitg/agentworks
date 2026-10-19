package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	agent "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type shutdownTestWorkshopSession struct {
	closed        bool
	mainSessionID string
}

func (s *shutdownTestWorkshopSession) Close() {
	s.closed = true
}

func (s *shutdownTestWorkshopSession) MainSessionID() string {
	return s.mainSessionID
}

func TestCancelActiveWorkForShutdown(t *testing.T) {
	agentCanceled := false
	workflowCanceled := false
	workshop := &shutdownTestWorkshopSession{}

	api := &StreamingAPI{
		agentCancelFuncs: map[string]context.CancelFunc{
			"session-1": func() { agentCanceled = true },
		},
		workflowOrchestratorContexts: map[string]context.CancelFunc{
			"query-1": func() { workflowCanceled = true },
		},
		activeSessions: map[string]*ActiveSessionInfo{
			"session-1": {
				SessionID:       "session-1",
				Status:          "running",
				IsSyntheticTurn: true,
			},
		},
		sessionQueryIDs: map[string][]string{
			"session-1": {"query-1"},
		},
		sessionBusy: map[string]bool{
			"session-1": true,
		},
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			"exec-1": {
				ExecutionID: "exec-1",
				SessionID:   "session-1",
				Status:      trackedExecutionStatusRunning,
			},
		},
		bgAgentRegistry: NewBackgroundAgentRegistry(),
	}
	api.workshopChatSessions.Store("session-1", workshop)

	api.cancelActiveWorkForShutdown()

	if !agentCanceled {
		t.Fatalf("agent cancel func was not called")
	}
	if !workflowCanceled {
		t.Fatalf("workflow cancel func was not called")
	}
	if len(api.agentCancelFuncs) != 0 {
		t.Fatalf("agent cancel funcs were not cleared: %+v", api.agentCancelFuncs)
	}
	if len(api.workflowOrchestratorContexts) != 0 {
		t.Fatalf("workflow contexts were not cleared: %+v", api.workflowOrchestratorContexts)
	}
	if len(api.sessionQueryIDs) != 0 {
		t.Fatalf("session query IDs were not cleared: %+v", api.sessionQueryIDs)
	}
	if api.sessionBusy["session-1"] {
		t.Fatalf("session busy flag was not cleared")
	}
	if api.activeSessions["session-1"].IsSyntheticTurn {
		t.Fatalf("synthetic turn flag was not cleared")
	}
	if !workshop.closed {
		t.Fatalf("workshop session was not closed")
	}
	if _, ok := api.workshopChatSessions.Load("session-1"); ok {
		t.Fatalf("workshop session was not removed")
	}
	if got := api.trackedWorkflowExecutions["exec-1"].Status; got != trackedExecutionStatusCanceled {
		t.Fatalf("tracked execution status = %q, want %q", got, trackedExecutionStatusCanceled)
	}
}

func TestHandleStopSessionCancelsActiveWorkAndPreventsPaneReuse(t *testing.T) {
	workspace.SetSessionFolderGuard("session-1", []string{"Workflow/test"}, []string{"Workflow/test"})
	defer workspace.ClearSessionShellConfig("session-1")

	agentCanceled := false
	workflowCanceled := false
	backgroundCanceled := false
	workshop := &shutdownTestWorkshopSession{mainSessionID: "session-1"}
	registry := NewBackgroundAgentRegistry()
	registry.Register("session-1", &BackgroundAgent{
		ID:        "bg-1",
		Name:      "background-check",
		SessionID: "session-1",
		Status:    BGAgentRunning,
		cancel:    func() { backgroundCanceled = true },
	})
	registry.Register("session-1", &BackgroundAgent{ID: "bg-finished", SessionID: "session-1", Status: BGAgentCompleted})

	api := &StreamingAPI{
		agentCancelFuncs: map[string]context.CancelFunc{
			"session-1": func() { agentCanceled = true },
		},
		workflowOrchestratorContexts: map[string]context.CancelFunc{
			"query-1": func() { workflowCanceled = true },
		},
		activeWorkflowExecutions: map[string]*ActiveWorkflowExecution{
			"query-1": {QueryID: "query-1", SessionID: "session-1", Status: "running"},
		},
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			"exec-1": {ExecutionID: "exec-1", SessionID: "session-1", Status: trackedExecutionStatusRunning},
		},
		sessionQueryIDs: map[string][]string{
			"session-1": {"query-1"},
		},
		workflowObjectives: map[string]string{
			"session-1": "objective",
		},
		activeSessions: map[string]*ActiveSessionInfo{
			"session-1": {
				SessionID:       "session-1",
				Status:          "running",
				IsSyntheticTurn: true,
			},
		},
		stoppedSessions: map[string]bool{},
		bgAgentRegistry: registry,
		sessionBusy: map[string]bool{
			"session-1": true,
		},
		pendingCompletions: map[string][]string{
			"session-1": {"bg-finished"},
		},
		lastQueryRequests: map[string]QueryRequest{
			"session-1": {Query: "old"},
		},
		sessionWorkspaceFolders: map[string]string{
			"session-1": "Workflow/test",
		},
		sessionAgents: map[string]*agent.LLMAgentWrapper{},
		completionLoopStarted: map[string]bool{
			"session-1": true,
		},
	}
	api.workshopChatSessions.Store("drifted-workshop-key", workshop)
	// A retry already armed before Stop may still fire afterward. Capture it
	// without a timer so the full stop path must keep delivery paused.
	previousRetry := completionRetryAfterFunc
	t.Cleanup(func() { completionRetryAfterFunc = previousRetry })
	var retry func()
	retryCount := 0
	completionRetryAfterFunc = func(_ time.Duration, run func()) {
		retry = run
		retryCount++
	}
	api.schedulePendingCompletionRetry("session-1")

	req := httptest.NewRequest(http.MethodPost, "/api/session/stop", nil)
	req.Header.Set("X-Session-ID", "session-1")
	rec := httptest.NewRecorder()

	api.handleStopSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("stop status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if !agentCanceled {
		t.Fatalf("agent cancel func was not called")
	}
	if !workflowCanceled {
		t.Fatalf("workflow cancel func was not called")
	}
	if !backgroundCanceled {
		t.Fatalf("background agent cancel func was not called")
	}
	if !workshop.closed {
		t.Fatalf("workshop session was not closed")
	}
	if _, ok := api.workshopChatSessions.Load("drifted-workshop-key"); ok {
		t.Fatalf("workshop session was not removed")
	}
	if !api.stoppedSessions["session-1"] {
		t.Fatalf("session was not marked stopped")
	}
	if got := api.activeSessions["session-1"].Status; got != "stopped" {
		t.Fatalf("active session status = %q, want stopped", got)
	}
	if api.activeSessions["session-1"].IsSyntheticTurn {
		t.Fatalf("synthetic turn flag was not cleared")
	}
	if api.sessionBusy["session-1"] {
		t.Fatalf("session busy flag was not cleared")
	}
	if _, ok := api.agentCancelFuncs["session-1"]; ok {
		t.Fatalf("agent cancel func was not removed")
	}
	if _, ok := api.workflowOrchestratorContexts["query-1"]; ok {
		t.Fatalf("workflow cancel func was not removed")
	}
	if _, ok := api.sessionQueryIDs["session-1"]; ok {
		t.Fatalf("session query IDs were not removed")
	}
	if _, ok := api.activeWorkflowExecutions["query-1"]; ok {
		t.Fatalf("active workflow execution was not removed")
	}
	if got := api.trackedWorkflowExecutions["exec-1"].Status; got != trackedExecutionStatusCanceled {
		t.Fatalf("tracked execution status = %q, want canceled", got)
	}
	if registry.Get("session-1", "bg-1") != nil {
		t.Fatalf("background agent registry was not cleaned up")
	}
	if !api.autoNotificationSessionUnreachable("session-1") {
		t.Fatal("full Stop did not suppress automatic result delivery")
	}
	if retry == nil {
		t.Fatal("expected a completion retry armed before Stop")
	}
	retry()
	if got := api.pendingCompletions["session-1"]; len(got) != 1 || got[0] != "bg-finished" {
		t.Fatalf("pending completion IDs changed after stopped retry: %v", got)
	}
	if retryCount != 1 || api.completionRetryScheduled["session-1"] {
		t.Fatal("stopped completion delivery re-armed itself")
	}
	if api.isSyntheticTurn("session-1") || api.activeSessions["session-1"].Status != "stopped" {
		t.Fatal("completion retry restarted the stopped chat")
	}
	if _, ok := api.lastQueryRequests["session-1"]; ok {
		t.Fatalf("last query request was not cleared")
	}
	if _, ok := api.sessionWorkspaceFolders["session-1"]; ok {
		t.Fatalf("session workspace folder was not cleared")
	}
	if _, ok := api.completionLoopStarted["session-1"]; ok {
		t.Fatalf("completion loop marker was not cleared")
	}
	if _, ok := api.workflowObjectives["session-1"]; ok {
		t.Fatalf("workflow objective was not cleared")
	}
	if got := workspace.GetSessionShellConfig("session-1"); got != nil {
		t.Fatalf("session shell config was not cleared: %+v", got)
	}
	if body := rec.Body.String(); !strings.Contains(body, "Session stopped") {
		t.Fatalf("unexpected stop response body: %q", body)
	}
}

func TestHandleStopSessionPreservesFinishedResultsForPreservedConversation(t *testing.T) {
	const sessionID = "preserved-results"
	registry := NewBackgroundAgentRegistry()
	finished := &BackgroundAgent{ID: "finished", SessionID: sessionID, Status: BGAgentCompleted, Result: "saved result"}
	registry.Register(sessionID, finished)
	api := &StreamingAPI{
		activeSessions:     map[string]*ActiveSessionInfo{sessionID: {SessionID: sessionID, Status: "running"}},
		stoppedSessions:    map[string]bool{},
		bgAgentRegistry:    registry,
		pendingCompletions: map[string][]string{sessionID: {finished.ID}},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/session/stop?preserveConversation=true", nil)
	request.Header.Set("X-Session-ID", sessionID)
	response := httptest.NewRecorder()
	api.handleStopSession(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("stop failed: %d %s", response.Code, response.Body.String())
	}
	if got := registry.Get(sessionID, finished.ID); got == nil || got.GetSnapshot().Result != "saved result" {
		t.Fatal("preserved conversation lost the completed result payload")
	}
	if got := api.pendingCompletions[sessionID]; len(got) != 1 || got[0] != finished.ID {
		t.Fatalf("preserved conversation lost its queued completion: %v", got)
	}
	if !api.autoNotificationSessionUnreachable(sessionID) {
		t.Fatal("preserved conversation resumed automatic delivery before a new message")
	}
}

func TestHandleStopSessionKeepsInteractiveConversationResumable(t *testing.T) {
	const sessionID = "interactive-workflow-chat"
	canceled := false
	nextTurn := make(chan QueryRequest, 1)
	api := &StreamingAPI{
		agentCancelFuncs: map[string]context.CancelFunc{
			sessionID: func() { canceled = true },
		},
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, AgentMode: "workflow_phase", Status: "running"},
		},
		stoppedSessions: map[string]bool{},
		bgAgentRegistry: NewBackgroundAgentRegistry(),
		lastQueryRequests: map[string]QueryRequest{
			sessionID: {AgentMode: "workflow_phase", SelectedFolder: "Workflow/test", Query: "first message"},
		},
		sessionWorkspaceFolders: map[string]string{sessionID: "Workflow/test"},
		internalQueryHandler: func(_ http.ResponseWriter, r *http.Request) {
			var request QueryRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode resumed query: %v", err)
				return
			}
			nextTurn <- request
		},
	}

	stop := httptest.NewRequest(http.MethodPost, "/api/session/stop?cancelAgents=true&preserveConversation=true", nil)
	stop.Header.Set("X-Session-ID", sessionID)
	response := httptest.NewRecorder()
	api.handleStopSession(response, stop)
	if response.Code != http.StatusOK || !canceled || !api.stoppedSessions[sessionID] {
		t.Fatalf("stop = %d canceled=%v stopped=%v: %s", response.Code, canceled, api.stoppedSessions[sessionID], response.Body.String())
	}
	if got := api.lastQueryRequests[sessionID].SelectedFolder; got != "Workflow/test" {
		t.Fatalf("resumable request workspace = %q", got)
	}
	if got := api.sessionWorkspaceFolders[sessionID]; got != "Workflow/test" {
		t.Fatalf("resumable session workspace = %q", got)
	}

	message := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", nil)
	message.Header.Set("X-Session-ID", sessionID)
	resumed := httptest.NewRecorder()
	if !api.startNextTurnFromLiveInput(resumed, message, sessionID, "next message", nil) || resumed.Code != http.StatusOK {
		t.Fatalf("next turn = %d: %s", resumed.Code, resumed.Body.String())
	}
	select {
	case request := <-nextTurn:
		if request.Query != "next message" || request.SelectedFolder != "Workflow/test" {
			t.Fatalf("resumed request = %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("next user message did not start a turn")
	}
}

func TestWorkflowChatAcceptsLiveInputAfterStop(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_edit":true}]}`)
	workspaceServer, docs := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", workspaceServer.URL)
	docs.files["Workflow/test/workflow.json"] = `{"id":"wf_test","access":{"owners":["owner"]}}`
	const sessionID = "stop-then-send-workflow-chat"
	query := QueryRequest{AgentMode: "workflow_phase", SelectedFolder: "Workflow/test", Query: "first message"}
	nextTurn := make(chan QueryRequest, 1)
	api := &StreamingAPI{
		internalChatSubmissionStore: newTestChatSubmissionStore(),
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, UserID: "owner", AgentMode: "workflow_phase", Status: "running"},
		},
		stoppedSessions:         map[string]bool{},
		bgAgentRegistry:         NewBackgroundAgentRegistry(),
		lastQueryRequests:       map[string]QueryRequest{sessionID: query},
		sessionWorkspaceFolders: map[string]string{sessionID: "Workflow/test"},
		lastChatPolicyBySession: map[string]string{},
		internalQueryHandler: func(_ http.ResponseWriter, r *http.Request) {
			var request QueryRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode next turn: %v", err)
				return
			}
			nextTurn <- request
		},
	}
	api.lastChatPolicyBySession[sessionID] = api.chatPolicySessionKey(resolveWorkflowChatPolicy(sessionID, query, nil, false))
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	stop := httptest.NewRequest(http.MethodPost, "/api/session/stop?cancelAgents=true&preserveConversation=true", nil).WithContext(ctx)
	stop.Header.Set("X-Session-ID", sessionID)
	stopResponse := httptest.NewRecorder()
	api.handleStopSession(stopResponse, stop)
	if stopResponse.Code != http.StatusOK {
		t.Fatalf("stop status = %d: %s", stopResponse.Code, stopResponse.Body.String())
	}

	message := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", strings.NewReader(`{"message":"next message"}`)).WithContext(ctx)
	message = mux.SetURLVars(message, map[string]string{"session_id": sessionID})
	messageResponse := httptest.NewRecorder()
	api.handleLiveInputMessage(messageResponse, message)
	if messageResponse.Code != http.StatusOK {
		t.Fatalf("next message status = %d: %s", messageResponse.Code, messageResponse.Body.String())
	}
	select {
	case request := <-nextTurn:
		if request.Query != "next message" || request.SelectedFolder != "Workflow/test" {
			t.Fatalf("next turn = %+v", request)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("live input did not start the next workflow turn")
	}
}

func TestHandleStopSessionCannotPreserveTriggeredRun(t *testing.T) {
	for _, tc := range []struct {
		name, sessionID, triggeredBy, botPlatform string
	}{
		{name: "schedule", sessionID: "schedule-cron--test_1", triggeredBy: "cron"},
		{name: "webhook", sessionID: "webhook-run", triggeredBy: "webhook"},
		{name: "bot", sessionID: "bot-run", botPlatform: "whatsapp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &StreamingAPI{
				activeSessions: map[string]*ActiveSessionInfo{
					tc.sessionID: {SessionID: tc.sessionID, Status: "running", TriggeredBy: tc.triggeredBy, BotPlatform: tc.botPlatform},
				},
				stoppedSessions: map[string]bool{},
				bgAgentRegistry: NewBackgroundAgentRegistry(),
				lastQueryRequests: map[string]QueryRequest{
					tc.sessionID: {SelectedFolder: "Workflow/test"},
				},
				sessionWorkspaceFolders: map[string]string{tc.sessionID: "Workflow/test"},
			}
			stop := httptest.NewRequest(http.MethodPost, "/api/session/stop?preserveConversation=true", nil)
			stop.Header.Set("X-Session-ID", tc.sessionID)
			response := httptest.NewRecorder()
			api.handleStopSession(response, stop)
			if response.Code != http.StatusOK || !api.stoppedSessions[tc.sessionID] {
				t.Fatalf("stop = %d stopped=%v: %s", response.Code, api.stoppedSessions[tc.sessionID], response.Body.String())
			}
			if _, ok := api.lastQueryRequests[tc.sessionID]; ok {
				t.Fatal("triggered run retained a resumable request")
			}
			if _, ok := api.sessionWorkspaceFolders[tc.sessionID]; ok {
				t.Fatal("triggered run retained a workspace binding")
			}
		})
	}
}

func TestHandleCancelCurrentTurnPreservesSessionAndBackgroundWork(t *testing.T) {
	const sessionID = "schedule-cron--test_1"
	turnCanceled := false
	backgroundCanceled := false
	registry := NewBackgroundAgentRegistry()
	registry.Register(sessionID, &BackgroundAgent{
		ID:        "bg-1",
		Name:      "background-check",
		SessionID: sessionID,
		Status:    BGAgentRunning,
		cancel:    func() { backgroundCanceled = true },
	})

	api := &StreamingAPI{
		agentCancelFuncs: map[string]context.CancelFunc{
			sessionID: func() { turnCanceled = true },
		},
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "running"},
		},
		stoppedSessions: map[string]bool{},
		bgAgentRegistry: registry,
	}

	req := httptest.NewRequest(http.MethodPost, "/api/session/cancel-turn", nil)
	req.Header.Set("X-Session-ID", sessionID)
	rec := httptest.NewRecorder()

	api.handleCancelCurrentTurn(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("cancel-turn status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
	if !turnCanceled {
		t.Fatalf("foreground turn cancel func was not called")
	}
	if backgroundCanceled {
		t.Fatalf("background work was canceled by foreground turn stop")
	}
	if registry.Get(sessionID, "bg-1") == nil {
		t.Fatalf("background agent was removed by foreground turn stop")
	}
	if api.stoppedSessions[sessionID] {
		t.Fatalf("session was marked stopped by foreground turn stop")
	}
	if got := api.activeSessions[sessionID].Status; got != "running" {
		t.Fatalf("active session status = %q, want running", got)
	}
	if _, ok := api.agentCancelFuncs[sessionID]; ok {
		t.Fatalf("foreground cancel func was not removed after cancellation")
	}
	if !api.consumeSessionTurnInterrupted(sessionID) {
		t.Fatalf("foreground turn cancellation did not record a sequence interruption")
	}
}

func TestHandleCancelCurrentTurnRecordsInterruptionBetweenTurns(t *testing.T) {
	const sessionID = "schedule-cron--test_2"
	api := &StreamingAPI{
		agentCancelFuncs: map[string]context.CancelFunc{},
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "running"},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/session/cancel-turn", nil)
	req.Header.Set("X-Session-ID", sessionID)
	rec := httptest.NewRecorder()

	api.handleCancelCurrentTurn(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("cancel-turn status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
	if !api.consumeSessionTurnInterrupted(sessionID) {
		t.Fatalf("cancel between turns did not preserve the user's sequence-stop intent")
	}
}

// Stop on a chat whose turn nothing owns any more (a Muse chat showed "Braiding" for hours) ends the turn on the server,
// so the chat returns to Ready (PLAT-861-228).
func TestStopSettlesATurnNothingOwns(t *testing.T) {
	const sessionID = "muse-orphaned-turn"
	store := internalevents.NewEventStore(100)
	defer store.Stop()
	store.EnableTurnPhase()
	store.AddEvent(sessionID, internalevents.Event{ID: "u1", Type: "user_message", SessionID: sessionID, Timestamp: time.Now()})
	if !store.TurnInProgress(sessionID) {
		t.Fatal("setup: the turn should be open")
	}
	api := &StreamingAPI{eventStore: store, agentCancelFuncs: map[string]context.CancelFunc{}}
	old := stoppedTurnSettleWait
	stoppedTurnSettleWait = 0
	defer func() { stoppedTurnSettleWait = old }()
	api.settleStoppedTurnWithNoOwner(sessionID)
	if store.TurnInProgress(sessionID) {
		t.Fatal("the turn is still open after Stop found nothing to end it")
	}
}

func TestHandleCancelCurrentTurnInterruptsMuseBeforeCancelWithoutClosingConversation(t *testing.T) {
	const sessionID = "muse-conversation-stop"
	agent := testCodingAgent(llm.ProviderMuseCLI, "muse-spark-1.3-contributor")
	if !mcpagent.AgentSupportsSteering(agent) {
		t.Fatal("test Muse agent must use the interactive transport")
	}
	oldDeliver := deliverCLITurnInterrupt
	t.Cleanup(func() { deliverCLITurnInterrupt = oldDeliver })
	var order []string
	deliverCLITurnInterrupt = func(_ context.Context, got *mcpagent.Agent, owner string) error {
		if got != agent || owner != sessionID {
			t.Fatalf("Escape targeted wrong session %q", owner)
		}
		order = append(order, "Escape")
		return nil
	}
	api := &StreamingAPI{
		agentCancelFuncs: map[string]context.CancelFunc{sessionID: func() { order = append(order, "cancel") }},
		activeSessions:   map[string]*ActiveSessionInfo{sessionID: {SessionID: sessionID, Status: "running"}},
		runningAgents:    map[string]*mcpagent.Agent{sessionID: agent},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/session/cancel-turn", nil)
	req.Header.Set("X-Session-ID", sessionID)
	rec := httptest.NewRecorder()
	api.handleCancelCurrentTurn(rec, req)
	if rec.Code != http.StatusNoContent || len(order) != 2 || order[0] != "Escape" || order[1] != "cancel" {
		t.Fatalf("Stop response=%d, operations=%v", rec.Code, order)
	}
	const scheduledID = "schedule-cron--muse-stop"
	scheduled := &StreamingAPI{
		agentCancelFuncs: map[string]context.CancelFunc{scheduledID: func() { order = append(order, "scheduled cancel") }},
		activeSessions:   map[string]*ActiveSessionInfo{scheduledID: {SessionID: scheduledID, Status: "running"}},
		runningAgents:    map[string]*mcpagent.Agent{scheduledID: agent},
	}
	req = httptest.NewRequest(http.MethodPost, "/api/session/cancel-turn", nil)
	req.Header.Set("X-Session-ID", scheduledID)
	rec = httptest.NewRecorder()
	scheduled.handleCancelCurrentTurn(rec, req)
	if rec.Code != http.StatusNoContent || len(order) != 3 || order[2] != "scheduled cancel" {
		t.Fatalf("scheduled Stop response=%d, operations=%v; must not send Escape", rec.Code, order)
	}
}

func TestHandleClearSessionRefusesActiveWorkAndPreservesGuard(t *testing.T) {
	const sessionID = "session-clear-active"
	workspace.SetSessionFolderGuard(sessionID, []string{"Workflow/test"}, []string{"Workflow/test"})
	defer workspace.ClearSessionShellConfig(sessionID)

	api := &StreamingAPI{
		agentCancelFuncs: map[string]context.CancelFunc{
			sessionID: func() {},
		},
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "running"},
		},
		conversationHistory: map[string][]llmtypes.MessageContent{
			sessionID: {},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/session/clear", nil)
	req.Header.Set("X-Session-ID", sessionID)
	rec := httptest.NewRecorder()

	api.handleClearSession(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("clear status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	if workspace.GetSessionShellConfig(sessionID) == nil {
		t.Fatal("active clear removed the session permission guard")
	}
	if _, ok := api.conversationHistory[sessionID]; !ok {
		t.Fatal("active clear removed conversation history")
	}
}

func TestCleanupCodingAgentInteractiveSessionsCallsEveryProvider(t *testing.T) {
	oldClaude := cleanupClaudeCodeProviderSessions
	oldCodex := cleanupCodexCLIProviderSessions
	oldCursor := cleanupCursorCLIProviderSessions
	oldPi := cleanupPiCLIProviderSessions
	t.Cleanup(func() {
		cleanupClaudeCodeProviderSessions = oldClaude
		cleanupCodexCLIProviderSessions = oldCodex
		cleanupCursorCLIProviderSessions = oldCursor
		cleanupPiCLIProviderSessions = oldPi
	})

	called := map[string]bool{}
	cleanupClaudeCodeProviderSessions = func(context.Context) error {
		called["claude"] = true
		return errors.New("simulated claude cleanup error")
	}
	cleanupCodexCLIProviderSessions = func(context.Context) error {
		called["codex"] = true
		return nil
	}
	cleanupCursorCLIProviderSessions = func(context.Context) error {
		called["cursor"] = true
		return nil
	}
	cleanupPiCLIProviderSessions = func(context.Context) error {
		called["pi"] = true
		return nil
	}

	cleanupCodingAgentInteractiveSessions("test")

	for _, provider := range []string{"claude", "codex", "cursor", "pi"} {
		if !called[provider] {
			t.Fatalf("cleanup for %s was not called", provider)
		}
	}
}

// A Builder chat whose turn was lost has no query-ID mapping. Stop must still
// stop the tracker listing it as running: a run left "running" answers every
// new Builder chat on the workflow with 409 workflow_busy and is re-adopted by
// the workflow tab as the live chat.
func TestStopSessionWithoutQueryIDsClearsTrackedBuilderChat(t *testing.T) {
	api := &StreamingAPI{
		agentCancelFuncs:             map[string]context.CancelFunc{},
		workflowOrchestratorContexts: map[string]context.CancelFunc{},
		activeSessions:               map[string]*ActiveSessionInfo{"session-1": {SessionID: "session-1", Status: "running"}},
		stoppedSessions:              map[string]bool{},
		sessionQueryIDs:              map[string][]string{},
		sessionBusy:                  map[string]bool{},
		bgAgentRegistry:              NewBackgroundAgentRegistry(),
		pendingCompletions:           map[string][]string{},
		lastQueryRequests:            map[string]QueryRequest{},
		sessionWorkspaceFolders:      map[string]string{},
		sessionAgents:                map[string]*agent.LLMAgentWrapper{},
		completionLoopStarted:        map[string]bool{},
		workflowObjectives:           map[string]string{},
		activeWorkflowExecutions:     map[string]*ActiveWorkflowExecution{},
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			"exec-1": {SessionID: "session-1", PhaseID: "workflow-builder", Status: trackedExecutionStatusRunning},
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/session/stop?cancelAgents=true", nil)
	req.Header.Set("X-Session-ID", "session-1")
	rec := httptest.NewRecorder()

	api.handleStopSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("stop status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	exec := api.trackedWorkflowExecutions["exec-1"]
	if exec == nil {
		return // pruned: no longer listed as running either
	}
	if exec.Status == trackedExecutionStatusRunning {
		t.Fatalf("tracked execution still %q after stop; it would block new Builder chats", exec.Status)
	}
}

// A turn typed into a retained provider pane (live input) has no Go turn to cancel; Stop must still interrupt the pane
// (Stop on a Pi live turn logged "No active LLM turn to cancel" and the command ran to the end).
func TestHandleCancelCurrentTurnInterruptsALiveRetainedPaneWithoutAGoTurn(t *testing.T) {
	const sessionID = "pi-live-pane-stop"
	oldPane := deliverCLIPaneInterrupt
	t.Cleanup(func() { deliverCLIPaneInterrupt = oldPane })
	var got []string
	deliverCLIPaneInterrupt = func(_ context.Context, provider llmproviders.Provider, owner string) error {
		got = append(got, string(provider)+" "+owner)
		return nil
	}
	api := &StreamingAPI{
		agentCancelFuncs: map[string]context.CancelFunc{},
		activeSessions:   map[string]*ActiveSessionInfo{sessionID: {SessionID: sessionID, Status: "running"}},
		terminalStore:    terminals.NewStore(),
	}
	api.terminalStore.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(time.Now(), sessionID, "main:"+sessionID, "mlp-pi-cli-int-live"))
	store := internalevents.NewEventStore(10)
	defer store.Stop()
	store.EnableTurnPhase() // the server enables the turn-phase tracking Stop reads on its own store
	api.eventStore = store
	stop := func() int {
		req := httptest.NewRequest(http.MethodPost, "/api/session/cancel-turn", nil)
		req.Header.Set("X-Session-ID", sessionID)
		rec := httptest.NewRecorder()
		api.handleCancelCurrentTurn(rec, req)
		return rec.Code
	}
	// Idle: no turn is running, so no Escape (a second Esc at an idle prompt can open a menu in some CLIs).
	if code := stop(); code != http.StatusNoContent || len(got) != 0 {
		t.Fatalf("idle Stop response=%d, pane interrupts=%v; want none", code, got)
	}
	store.AddEvent(sessionID, internalevents.Event{Type: "user_message", Timestamp: time.Now(), SessionID: sessionID})
	if code := stop(); code != http.StatusNoContent || len(got) != 1 || got[0] != "pi-cli "+sessionID {
		t.Fatalf("running Stop response=%d, pane interrupts=%v; want one Escape to the pi-cli pane", code, got)
	}
}
