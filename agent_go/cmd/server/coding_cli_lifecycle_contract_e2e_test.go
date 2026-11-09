package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
)

// TestCodingCLILifecycleMatrixHermeticTmux is the credential-free P0 terminal
// ownership contract. It uses real tmux processes while keeping provider CLIs
// hermetic, so every push verifies identical lifecycle behavior for all active
// coding providers without consuming provider quota.
func TestCodingCLILifecycleMatrixHermeticTmux(t *testing.T) {
	// PLAT-746: Unix sockets need a short path even when TMPDIR is a long verifier path.
	// This is a private server; never reuse or change the host's tmux directory.
	socketDir, err := os.MkdirTemp("/tmp", "aw-tmux-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TMPDIR", socketDir)
	t.Cleanup(func() {
		_ = runTerminalTmuxCommand(context.Background(), "", "kill-server")
		_ = os.RemoveAll(socketDir)
	})
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is required for the coding CLI lifecycle contract")
	}

	providers := []struct {
		name   string
		prefix string
	}{
		{name: "claude-code", prefix: "mlp-claude-code-int"},
		{name: "codex-cli", prefix: "mlp-codex-cli-int"},
		{name: "cursor-cli", prefix: "mlp-cursor-cli-int"},
		{name: "pi-cli", prefix: "mlp-pi-cli-int"},
	}

	for _, provider := range providers {
		provider := provider
		t.Run(provider.name+"/child_exit_fails_exact_child_only", func(t *testing.T) {
			tmuxSession := startLifecycleContractTmux(t, provider.prefix)
			sessionID := "lifecycle-child-" + strings.ReplaceAll(provider.name, "-", "_")
			executionID := "child-" + provider.name
			unrelatedID := "unrelated-" + provider.name
			startedAt := time.Now().Add(-time.Second)

			store := terminals.NewStore()
			store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, executionID, tmuxSession, "provider working", 1))
			registry := NewBackgroundAgentRegistry()
			workerCtx, workerCancel := context.WithCancel(context.Background())
			registry.Register(sessionID, &BackgroundAgent{
				ID:        executionID,
				SessionID: sessionID,
				Status:    BGAgentRunning,
				CreatedAt: startedAt,
				cancel:    workerCancel,
			})
			api := &StreamingAPI{
				terminalStore: store,
				activeSessions: map[string]*ActiveSessionInfo{
					sessionID: {SessionID: sessionID, Status: "running", CreatedAt: startedAt},
				},
				stoppedSessions: make(map[string]bool),
				bgAgentRegistry: registry,
				trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
					executionID: {ExecutionID: executionID, SessionID: sessionID, Status: trackedExecutionStatusRunning, StartedAt: startedAt},
					unrelatedID: {ExecutionID: unrelatedID, SessionID: sessionID, Status: trackedExecutionStatusRunning, StartedAt: startedAt},
				},
			}

			killLifecycleContractTmux(t, tmuxSession)
			reapUntilMissingPaneConfirmed(api)

			snapshot := mustLifecycleTerminal(t, store, sessionID+":"+executionID)
			if snapshot.State != "failed" || snapshot.ProcessState != "closed" || snapshot.TmuxSession != "" || snapshot.CloseReason == "" {
				t.Fatalf("terminal after child exit = state=%q process=%q tmux=%q reason=%q", snapshot.State, snapshot.ProcessState, snapshot.TmuxSession, snapshot.CloseReason)
			}
			if got := api.trackedWorkflowExecutions[executionID]; got.Status != trackedExecutionStatusFailed || got.LastError == "" {
				t.Fatalf("owning execution = status=%q error=%q, want failed with reason", got.Status, got.LastError)
			}
			if got := api.trackedWorkflowExecutions[unrelatedID]; got.Status != trackedExecutionStatusRunning {
				t.Fatalf("unrelated execution status = %q, want running", got.Status)
			}
			if got := registry.Get(sessionID, executionID); got.GetStatus() != BGAgentFailed {
				t.Fatalf("background owner status = %q, want failed", got.GetStatus())
			}
			select {
			case <-workerCtx.Done():
			default:
				t.Fatal("background owner context was not canceled")
			}
			if got := api.activeSessions[sessionID].Status; got != "running" {
				t.Fatalf("parent session status = %q, want running", got)
			}
			if api.isSessionMarkedStopped(sessionID) {
				t.Fatal("child process exit stopped the parent session")
			}
		})

		t.Run(provider.name+"/main_exit_fails_session", func(t *testing.T) {
			oldCloseAll := closeAllCodingCLISessionsForRuntimeCancel
			oldCloseTmux := closeCodingAgentTmuxForRuntimeCancel
			closeAllCodingCLISessionsForRuntimeCancel = func(string, string) {}
			closeCodingAgentTmuxForRuntimeCancel = func(string, string) bool { return true }
			t.Cleanup(func() {
				closeAllCodingCLISessionsForRuntimeCancel = oldCloseAll
				closeCodingAgentTmuxForRuntimeCancel = oldCloseTmux
			})

			tmuxSession := startLifecycleContractTmux(t, provider.prefix)
			sessionID := "lifecycle-main-" + strings.ReplaceAll(provider.name, "-", "_")
			startedAt := time.Now().Add(-time.Second)
			store := terminals.NewStore()
			event := terminalRouteChunkEvent(sessionID, "main:"+sessionID, tmuxSession, "provider working", 1)
			event.ExecutionKind = "main_agent"
			event.Data.Data.(*agentevents.StreamingChunkEvent).Metadata["execution_kind"] = "main_agent"
			store.HandleEvent(sessionID, event)
			api := &StreamingAPI{
				terminalStore: store,
				activeSessions: map[string]*ActiveSessionInfo{
					sessionID: {SessionID: sessionID, Status: "running", CreatedAt: startedAt},
				},
				stoppedSessions: make(map[string]bool),
			}

			killLifecycleContractTmux(t, tmuxSession)
			reapUntilMissingPaneConfirmed(api)

			if got := api.activeSessions[sessionID].Status; got != "error" {
				t.Fatalf("main session status = %q, want error", got)
			}
			if !api.isSessionMarkedStopped(sessionID) {
				t.Fatal("main process exit did not stop session runtime")
			}
			snapshot := mustLifecycleTerminal(t, store, sessionID+":main:"+sessionID)
			if snapshot.State != "failed" || snapshot.ProcessState != "closed" || snapshot.CloseReason == "" {
				t.Fatalf("main terminal after exit = state=%q process=%q reason=%q", snapshot.State, snapshot.ProcessState, snapshot.CloseReason)
			}
		})
	}
}

// TestRetainedSubmissionRecoveryAfterIdleReaperP0 covers the cross-component
// lifecycle that previously escaped the individual reaper, journal, and live
// input tests: an idle retained tmux is reaped, an already-durable uncertain
// receipt survives a server restart, reconciliation proves it was not
// delivered, and the same submission starts exactly one resumed turn.
func TestRetainedSubmissionRecoveryAfterIdleReaperP0(t *testing.T) {
	// Retained tmux delivery is the opt-in terminal mode; the SDK default reports another transport.
	t.Setenv("CLAUDE_CODE_TRANSPORT", "tmux")
	const (
		sessionID    = "p0-reaper-restart-recovery"
		tmuxSession  = "mlp-claude-code-p0-reaped"
		submissionID = "p0-durable-submission"
		project      = "projects/p0-recovery"
		message      = "resume after idle cleanup"
	)

	now := time.Now()
	terminalStore := terminals.NewStore()
	terminalStore.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(
		now.Add(-defaultCodingAgentTmuxOrphanIdleTimeout-time.Minute),
		sessionID,
		"main:"+sessionID,
		tmuxSession,
	))
	retainedSession := startRegisteredTestTmuxSession(t, sessionID, tmuxSession)
	journal := newTestChatSubmissionStore()

	// Model the durable receipt left by the historical failed delivery. It is
	// deliberately finalized as uncertain so only reconciliation may reopen it.
	initialRequest := httptest.NewRequest(http.MethodPost, "/api/query", nil)
	initialRequest.Header.Set("Idempotency-Key", submissionID)
	initialResponse := httptest.NewRecorder()
	initialAPI := &StreamingAPI{internalChatSubmissionStore: journal}
	w, _, finish, accepted := initialAPI.beginChatSubmission(initialResponse, initialRequest, sessionID, project, message)
	if !accepted {
		t.Fatalf("initial durable submission was rejected: status=%d body=%s", initialResponse.Code, initialResponse.Body.String())
	}
	writeSubmissionUncertain(w, submissionID)
	finish()

	reaperAPI := &StreamingAPI{
		terminalStore: terminalStore,
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "completed"},
		},
	}
	stubTerminalTmuxCommand(t)
	if closed := reaperAPI.cleanupStaleCodingAgentTmuxSessions(now); closed != 1 {
		t.Fatalf("reaper closed %d tmux sessions, want 1", closed)
	}
	if _, ok := mcpagent.LookupSession(sessionID); ok {
		t.Fatal("idle reaper left the durable provider session registered")
	}
	if retainedSession.ActiveTurnID() != "" {
		t.Fatal("reaped provider session retained an active turn")
	}

	// A fresh StreamingAPI represents a backend restart. The journal is the
	// only shared state; the provider session registry remains cleared.
	queryStarted := make(chan struct{}, 1)
	restartedAPI := &StreamingAPI{
		internalChatSubmissionStore: journal,
		internalUncertainSubmissionRetryChecker: func(_ context.Context, record chatSubmissionRecord) bool {
			return record.ID == submissionID && record.Session == sessionID && record.Project == project && record.Message == message
		},
		runningAgents:    map[string]*mcpagent.Agent{},
		runningAgentsMux: sync.RWMutex{},
		agentCancelFuncs: map[string]context.CancelFunc{},
		agentCancelMux:   sync.RWMutex{},
		lastQueryRequests: map[string]QueryRequest{
			sessionID: {SelectedFolder: project, AgentMode: "multi-agent", Provider: string(llm.ProviderClaudeCode), ModelID: "claude-sonnet-4-6"},
		},
		internalQueryHandler: func(w http.ResponseWriter, _ *http.Request) {
			queryStarted <- struct{}{}
			_ = json.NewEncoder(w).Encode(QueryResponse{QueryID: "p0-resumed-turn"})
		},
	}
	body := bytes.NewBufferString(fmt.Sprintf(`{"message":%q,"submission_id":%q}`, message, submissionID))
	retryRequest := mux.SetURLVars(
		httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", body),
		map[string]string{"session_id": sessionID},
	)
	retryResponse := httptest.NewRecorder()
	restartedAPI.handleLiveInputMessage(retryResponse, retryRequest)

	if retryResponse.Code != http.StatusOK {
		t.Fatalf("reconciled retry status=%d body=%s, want 200", retryResponse.Code, retryResponse.Body.String())
	}
	retryResponseBody := retryResponse.Body.String()
	var response LiveInputResponse
	if err := json.Unmarshal([]byte(retryResponseBody), &response); err != nil {
		t.Fatalf("decode reconciled retry: %v", err)
	}
	if !response.Success || response.DeliveryStatus != "next_turn_started" {
		t.Fatalf("reconciled retry response=%#v, want next_turn_started", response)
	}
	select {
	case <-queryStarted:
	case <-time.After(time.Second):
		t.Fatal("reconciled submission did not start the resumed turn")
	}
	select {
	case <-queryStarted:
		t.Fatal("reconciled submission dispatched more than one resumed turn")
	default:
	}

	// Once reconciliation succeeds, another retry of the same idempotency key
	// must replay the durable success instead of opening a second turn.
	replayBody := bytes.NewBufferString(fmt.Sprintf(`{"message":%q,"submission_id":%q}`, message, submissionID))
	replayRequest := mux.SetURLVars(
		httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", replayBody),
		map[string]string{"session_id": sessionID},
	)
	replayResponse := httptest.NewRecorder()
	restartedAPI.handleLiveInputMessage(replayResponse, replayRequest)
	if replayResponse.Code != http.StatusOK || replayResponse.Body.String() != retryResponseBody {
		t.Fatalf("completed submission was not replayed: status=%d first=%s replay=%s", replayResponse.Code, retryResponseBody, replayResponse.Body.String())
	}
	select {
	case <-queryStarted:
		t.Fatal("durable replay dispatched a duplicate resumed turn")
	default:
	}
}

func TestTerminalOwnerReconciliationRejectsStaleGeneration(t *testing.T) {
	store := terminals.NewStore()
	sessionID := "reused-main-session"
	tmuxSession := "mlp-codex-cli-int-historical"
	event := terminalRouteChunkEvent(sessionID, "main:"+sessionID, tmuxSession, "historical pane", 1)
	event.ExecutionKind = "main_agent"
	event.Data.Data.(*agentevents.StreamingChunkEvent).Metadata["execution_kind"] = "main_agent"
	store.HandleEvent(sessionID, event)
	snapshot := mustLifecycleTerminal(t, store, sessionID+":main:"+sessionID)

	api := &StreamingAPI{
		terminalStore: store,
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {
				SessionID: sessionID,
				Status:    "running",
				CreatedAt: snapshot.CreatedAt.Add(time.Second),
			},
		},
		stoppedSessions: make(map[string]bool),
	}
	if api.reconcileUnexpectedTerminalExit(snapshot, "historical pane exited") {
		t.Fatal("stale terminal reconciled into a newer main session")
	}
	if got := api.activeSessions[sessionID].Status; got != "running" {
		t.Fatalf("newer session status = %q, want running", got)
	}
	if api.isSessionMarkedStopped(sessionID) {
		t.Fatal("stale terminal stopped the newer main session")
	}
}

func TestTerminalOwnerReconciliationPreservesSettledTurnError(t *testing.T) {
	store := terminals.NewStore()
	sessionID := "settled-main-session"
	event := terminalRouteChunkEvent(sessionID, "main:"+sessionID, "mlp-claude-code-settled", "pane", 1)
	event.ExecutionKind = "main_agent"
	event.Data.Data.(*agentevents.StreamingChunkEvent).Metadata["execution_kind"] = "main_agent"
	store.HandleEvent(sessionID, event)
	snapshot := mustLifecycleTerminal(t, store, sessionID+":main:"+sessionID)

	coordinator := NewRuntimeCoordinator()
	coordinator.MarkTerminalBoundary(sessionID, runtimePhaseFailed, "execute request: context canceled")
	api := &StreamingAPI{
		terminalStore:      store,
		runtimeCoordinator: coordinator,
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "running"},
		},
		stoppedSessions: make(map[string]bool),
	}

	if api.reconcileUnexpectedTerminalExit(snapshot, "tmux pane disappeared unexpectedly") {
		t.Fatal("secondary tmux cleanup replaced an already-settled turn failure")
	}
	runtime, ok := api.authoritativeRuntimeSnapshot(sessionID)
	if !ok || runtime.Phase != runtimePhaseFailed || runtime.Reason != "execute request: context canceled" {
		t.Fatalf("runtime = %#v, want original settled failure", runtime)
	}
	if api.isSessionMarkedStopped(sessionID) {
		t.Fatal("secondary tmux cleanup stopped an already-settled session")
	}
}

func TestTerminalSnapshotExpiryMatrix(t *testing.T) {
	for _, providerPrefix := range []string{"mlp-claude-code-int", "mlp-codex-cli-int", "mlp-cursor-cli-int", "mlp-pi-cli-int"} {
		t.Run(providerPrefix, func(t *testing.T) {
			store := terminals.NewStore()
			sessionID := "expiry-" + providerPrefix
			executionID := "workflow-step:expiry"
			tmuxSession := providerPrefix + "-expiry"
			completedAt := time.Now().Add(-2 * time.Second)
			store.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(completedAt, sessionID, executionID, tmuxSession))
			// terminalRouteEndEvent uses time.Now by default; replace it with an
			// authoritative historical completion timestamp so expiry is deterministic.
			end := terminalRouteEndEvent(sessionID, executionID, tmuxSession, 1)
			end.Timestamp = completedAt
			store.HandleEvent(sessionID, end)
			if got := store.List(sessionID); len(got) != 0 {
				t.Fatalf("expired snapshot count = %d, want 0", len(got))
			}
		})
	}
}

// The server reaches tmux with a minimal environment (no inherited TMUX), so the test creates and kills its
// panes through the same runner; a plain exec could land on an enclosing tmux server the watchdog never sees.
func startLifecycleContractTmux(t *testing.T, prefix string) string {
	t.Helper()
	name := fmt.Sprintf("%s-e2e-%d", prefix, time.Now().UnixNano())
	if err := runTerminalTmuxCommand(context.Background(), "", "new-session", "-d", "-s", name, "sleep", "60"); err != nil {
		t.Fatalf("start tmux %s: %v", name, err)
	}
	t.Cleanup(func() { _ = runTerminalTmuxCommand(context.Background(), "", "kill-session", "-t", name) })
	if got := inspectCodingTmuxPaneState(name); got != codingTmuxPaneAlive {
		list, listErr := runTerminalTmuxOutputCommand(context.Background(), "list-sessions")
		shown, showErr := runTerminalTmuxOutputCommand(context.Background(), "display-message", "-p", "-t", name, "#{pane_dead}")
		t.Fatalf("tmux %s state = %v, want alive; list=%q err=%v display=%q err=%v TMUX_TMPDIR=%q TMPDIR=%q", name, got, list, listErr, shown, showErr, os.Getenv("TMUX_TMPDIR"), os.Getenv("TMPDIR"))
	}
	return name
}

func killLifecycleContractTmux(t *testing.T, name string) {
	t.Helper()
	if err := runTerminalTmuxCommand(context.Background(), "", "kill-session", "-t", name); err != nil {
		t.Fatalf("kill tmux %s: %v", name, err)
	}
	if got := inspectCodingTmuxPaneState(name); got != codingTmuxPaneMissing {
		t.Fatalf("tmux %s state after kill = %v, want missing", name, got)
	}
}

func mustLifecycleTerminal(t *testing.T, store *terminals.Store, terminalID string) terminals.Snapshot {
	t.Helper()
	snapshot, ok := store.Get(terminalID)
	if !ok {
		t.Fatalf("terminal %s not found", terminalID)
	}
	return snapshot
}

// reapUntilMissingPaneConfirmed runs the watchdog as many passes as it takes
// to confirm a vanished pane (codingWatchdogMissingConfirmChecks), sharing
// its streak like the real ticker, so cleanup that is still settling is not
// mistaken for a crash.
func reapUntilMissingPaneConfirmed(api *StreamingAPI) {
	streak := map[string]codingWatchdogObservation{}
	for i := 0; i < codingWatchdogMissingConfirmChecks; i++ {
		api.reapRateLimitedCodingSessionsOnce(streak)
	}
}
