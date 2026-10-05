package server

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/liveattach"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

// PLAT-178 stale-promotion queue jam: a promoted pending input must either
// produce CLI output of its own or be settled as answered, and a busy retained
// turn must never hold the chat lane forever.

func plat178Thresholds(t *testing.T, grace, failAfter, paneCheck time.Duration, inspect func(string) codingTmuxPaneState) {
	t.Helper()
	oldGrace, oldFail, oldPane := retainedMainTurnPromotionEvidenceGrace, retainedMainTurnStuckFailAfter, retainedMainTurnPaneCheckInterval
	oldWarn, oldQuiet, oldRecheck := retainedMainTurnStuckWarnAfter, retainedMainTurnDurableQuietWindow, retainedMainTurnDurableRecheckWindow
	oldInspect := inspectRetainedMainTurnTmuxState
	retainedMainTurnPromotionEvidenceGrace = grace
	retainedMainTurnStuckFailAfter = failAfter
	retainedMainTurnPaneCheckInterval = paneCheck
	retainedMainTurnStuckWarnAfter = time.Hour
	retainedMainTurnDurableQuietWindow = 50 * time.Millisecond
	retainedMainTurnDurableRecheckWindow = 50 * time.Millisecond
	inspectRetainedMainTurnTmuxState = inspect
	t.Cleanup(func() {
		retainedMainTurnPromotionEvidenceGrace, retainedMainTurnStuckFailAfter, retainedMainTurnPaneCheckInterval = oldGrace, oldFail, oldPane
		retainedMainTurnStuckWarnAfter, retainedMainTurnDurableQuietWindow, retainedMainTurnDurableRecheckWindow = oldWarn, oldQuiet, oldRecheck
		inspectRetainedMainTurnTmuxState = oldInspect
	})
}

func paneAlive(string) codingTmuxPaneState { return codingTmuxPaneAlive }

// newPlat178API builds a Muse retained session whose pane never reports idle
// (Muse has no pane-ready signal) and whose durable sidecar has no new final
// response, which is exactly the incident's shape.
func newPlat178API(t *testing.T, sessionID, tmuxSession string) (*StreamingAPI, *terminals.Store, string) {
	t.Helper()
	installFakeAttach(t, func(cmd string) liveattach.Reply {
		if strings.HasPrefix(cmd, "capture-pane ") {
			return liveattach.Reply{Lines: []string{"⠋ Working…", "› agent output"}}
		}
		return liveattach.Reply{}
	})
	terminalID := sessionID + ":main:" + sessionID
	terminalStore := terminals.NewStore()
	terminalStore.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(time.Now(), sessionID, "main:"+sessionID, tmuxSession))
	if _, ok := terminalStore.MarkTurnCompleted(terminalID); !ok {
		t.Fatal("could not prepare idle retained terminal")
	}
	eventStore := internalevents.NewEventStore(100)
	t.Cleanup(eventStore.Stop)
	api := &StreamingAPI{
		eventStore:                          eventStore,
		terminalStore:                       terminalStore,
		liveAttach:                          newLiveAttachManager(),
		activeSessions:                      map[string]*ActiveSessionInfo{sessionID: {SessionID: sessionID, Status: "completed"}},
		retainedMainTurns:                   make(map[string]time.Time),
		retainedMainTurnExecutionIDs:        make(map[string]string),
		retainedMainTurnPendingExecutionIDs: make(map[string][]string),
		retainedMainTurnWatchCancels:        make(map[string]context.CancelFunc),
		retainedMainTurnCompletionEmitted:   make(map[string]time.Time),
		nativeTranscriptSyncInFlight:        make(map[string]bool),
		trackedWorkflowExecutions:           make(map[string]*TrackedWorkflowExecution),
		internalRetainedTurnFinalResponseReader: func(llmproviders.Provider, string, time.Time) string {
			return ""
		},
	}
	eventStore.SetEventAddedCallback(func(ownerSessionID string, event internalevents.Event) {
		terminalStore.HandleEventWithChange(ownerSessionID, event)
		api.observeRetainedMainTurnEvent(ownerSessionID, event)
	})
	return api, terminalStore, terminalID
}

func trackRunning(api *StreamingAPI, sessionID string, ids ...string) {
	api.trackedWorkflowExecutionsMux.Lock()
	defer api.trackedWorkflowExecutionsMux.Unlock()
	for _, id := range ids {
		api.trackedWorkflowExecutions[id] = &TrackedWorkflowExecution{ExecutionID: id, SessionID: sessionID, Status: trackedExecutionStatusRunning}
	}
}

func trackedStatus(api *StreamingAPI, id string) string {
	api.trackedWorkflowExecutionsMux.Lock()
	defer api.trackedWorkflowExecutionsMux.Unlock()
	if exec := api.trackedWorkflowExecutions[id]; exec != nil {
		return exec.Status
	}
	return ""
}

func sessionCompletion(sessionID, id string, at time.Time) internalevents.Event {
	completion := unifiedevents.NewUnifiedCompletionEvent("coding_agent", "retained", "", "done", "completed", time.Second, 1)
	completion.SessionID = sessionID
	completion.Metadata["source"] = "mcpagent_session"
	return internalevents.Event{
		ID: id, Type: "unified_completion", Timestamp: at, SessionID: sessionID, ExecutionID: id,
		Data: &unifiedevents.AgentEvent{Type: unifiedevents.EventType("unified_completion"), Timestamp: at, SessionID: sessionID, Data: completion},
	}
}

func waitNotBusy(t *testing.T, api *StreamingAPI, sessionID string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for api.isSessionBusy(sessionID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if api.isSessionBusy(sessionID) {
		t.Fatal("retained turn still holds the chat lane")
	}
	api.retainedMainTurnsMu.Lock()
	_, tracked := api.retainedMainTurns[sessionID]
	pending := len(api.retainedMainTurnPendingExecutionIDs[sessionID])
	api.retainedMainTurnsMu.Unlock()
	if tracked || pending != 0 {
		t.Fatalf("retained lifecycle still tracked=%t pending=%d; conversationTurnOccupied would stay true", tracked, pending)
	}
}

func TestPLAT178FinishedExecutionIsNeverQueuedBehindActiveTurn(t *testing.T) {
	plat178Thresholds(t, time.Hour, time.Hour, time.Hour, paneAlive)
	const sessionID = "plat178-finished"
	api, _, _ := newPlat178API(t, sessionID, "mlp-muse-plat178-finished")
	trackRunning(api, sessionID, "live-turn:active")
	api.trackedWorkflowExecutionsMux.Lock()
	api.trackedWorkflowExecutions["query_old"] = &TrackedWorkflowExecution{ExecutionID: "query_old", SessionID: sessionID, Status: trackedExecutionStatusCompleted}
	api.trackedWorkflowExecutionsMux.Unlock()

	api.markMCPAgentSessionTurnRunning(sessionID, "live-turn:active")
	api.markMCPAgentSessionTurnRunning(sessionID, "query_old")

	api.retainedMainTurnsMu.Lock()
	pending := append([]string(nil), api.retainedMainTurnPendingExecutionIDs[sessionID]...)
	api.retainedMainTurnsMu.Unlock()
	if len(pending) != 0 {
		t.Fatalf("pending = %#v, want an already-finished execution never queued", pending)
	}
}

// Two rapid inputs answered by one response: the completion promotes the
// second input, which never produces output of its own. It must settle after
// the grace window (with the backlog behind it) and release the lane.
func TestPLAT178PromotedInputAnsweredByPreviousResponseSettlesAfterGrace(t *testing.T) {
	plat178Thresholds(t, 300*time.Millisecond, time.Hour, time.Hour, paneAlive)
	const sessionID = "plat178-answered-together"
	api, _, _ := newPlat178API(t, sessionID, "mlp-muse-plat178-together")
	trackRunning(api, sessionID, "live-turn:first", "live-turn:second", "live-turn:third")

	api.markMCPAgentSessionTurnRunning(sessionID, "live-turn:first")
	api.markMCPAgentSessionTurnRunning(sessionID, "live-turn:second")
	api.markMCPAgentSessionTurnRunning(sessionID, "live-turn:third")
	api.observeRetainedMainTurnEvent(sessionID, sessionCompletion(sessionID, "first-completion", time.Now().Add(time.Second)))

	api.retainedMainTurnsMu.Lock()
	promoted := api.retainedMainTurnExecutionIDs[sessionID]
	api.retainedMainTurnsMu.Unlock()
	if promoted != "live-turn:second" || !api.isSessionBusy(sessionID) {
		t.Fatalf("promoted=%q busy=%t, want second input promoted and lane busy", promoted, api.isSessionBusy(sessionID))
	}

	waitNotBusy(t, api, sessionID, 5*time.Second)
	for _, id := range []string{"live-turn:first", "live-turn:second", "live-turn:third"} {
		if got := trackedStatus(api, id); got != trackedExecutionStatusCompleted {
			t.Fatalf("%s status = %q, want completed", id, got)
		}
	}
}

// A promoted input that does start a response (new CLI output) is a live turn
// and must not be settled by the evidence gate.
func TestPLAT178PromotedInputWithNewOutputIsNotSettledEarly(t *testing.T) {
	plat178Thresholds(t, 300*time.Millisecond, time.Hour, time.Hour, paneAlive)
	const sessionID = "plat178-genuine"
	const tmuxSession = "mlp-muse-plat178-genuine"
	api, _, _ := newPlat178API(t, sessionID, tmuxSession)
	trackRunning(api, sessionID, "live-turn:first", "live-turn:second")

	api.markMCPAgentSessionTurnRunning(sessionID, "live-turn:first")
	api.markMCPAgentSessionTurnRunning(sessionID, "live-turn:second")
	api.observeRetainedMainTurnEvent(sessionID, sessionCompletion(sessionID, "first-completion", time.Now().Add(time.Second)))

	stop := make(chan struct{})
	broadcasterDone := make(chan struct{})
	defer func() {
		if stop != nil {
			close(stop)
			<-broadcasterDone
		}
	}()
	go func() {
		defer close(broadcasterDone)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				// Reuse the observer's stream; never start a new one here.
				api.liveAttach.mu.Lock()
				st := api.liveAttach.sessions[tmuxSession]
				api.liveAttach.mu.Unlock()
				if st != nil {
					st.broadcast([]byte("streaming answer to the second input"))
				}
			}
		}
	}()

	time.Sleep(1500 * time.Millisecond)
	if !api.isSessionBusy(sessionID) {
		t.Fatal("a promoted turn that produced new CLI output was settled by the evidence gate")
	}
	api.retainedMainTurnsMu.Lock()
	cancel := api.retainedMainTurnWatchCancels[sessionID]
	api.retainedMainTurnsMu.Unlock()
	if cancel != nil {
		cancel()
	}
	close(stop)
	<-broadcasterDone
	stop = nil
	// Stop the observer's attach stream and wait for it so the fake attach is
	// not restored while the stream goroutine still runs.
	api.liveAttach.mu.Lock()
	st := api.liveAttach.sessions[tmuxSession]
	api.liveAttach.mu.Unlock()
	if st != nil {
		st.stop()
		<-st.done
	}
}

// The backstop: a turn with neither pane-idle nor a durable final response
// fails after retainedMainTurnStuckFailAfter, fails its backlog and releases
// the lane so queued conversation turns run.
func TestPLAT178StuckRetainedTurnFailsAfterBackstopAndReleasesLane(t *testing.T) {
	plat178Thresholds(t, time.Hour, 400*time.Millisecond, time.Hour, paneAlive)
	const sessionID = "plat178-fail-after"
	api, _, _ := newPlat178API(t, sessionID, "mlp-muse-plat178-failafter")
	trackRunning(api, sessionID, "exec-active", "exec-pending")

	api.markRetainedMainCodingTurnRunning(sessionID, "exec-active")
	api.markRetainedMainCodingTurnRunning(sessionID, "exec-pending")
	if !api.isSessionBusy(sessionID) {
		t.Fatal("retained turn was not marked busy")
	}

	waitNotBusy(t, api, sessionID, 5*time.Second)
	for _, id := range []string{"exec-active", "exec-pending"} {
		if got := trackedStatus(api, id); got != trackedExecutionStatusFailed {
			t.Fatalf("%s status = %q, want failed", id, got)
		}
	}
}

// TUI repaint bytes are not proof of active model work. They must neither
// starve the provider's completion check nor disable the existing backstop.
func TestRetainedCompletionChecksSurviveContinuousPaneOutput(t *testing.T) {
	for _, completed := range []bool{true, false} {
		name := "completed-sidecar"
		if !completed {
			name = "stuck-backstop"
		}
		t.Run(name, func(t *testing.T) {
			plat178Thresholds(t, time.Hour, 600*time.Millisecond, time.Hour, paneAlive)
			sessionID, tmuxSession := "repaint-"+name, "mlp-muse-repaint-"+name
			api, _, _ := newPlat178API(t, sessionID, tmuxSession)
			if completed {
				api.internalRetainedTurnFinalResponseReader = func(llmproviders.Provider, string, time.Time) string {
					return "Provider-confirmed final response."
				}
			}
			trackRunning(api, sessionID, "repaint-execution")
			stop, done := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(done)
				tick := time.NewTicker(25 * time.Millisecond)
				defer tick.Stop()
				for {
					select {
					case <-stop:
						return
					case <-tick.C:
						api.liveAttach.mu.Lock()
						stream := api.liveAttach.sessions[tmuxSession]
						api.liveAttach.mu.Unlock()
						if stream != nil {
							stream.broadcast([]byte("\x1b[?25h"))
						}
					}
				}
			}()
			t.Cleanup(func() {
				close(stop)
				<-done
				api.retainedMainTurnsMu.Lock()
				cancel := api.retainedMainTurnWatchCancels[sessionID]
				api.retainedMainTurnsMu.Unlock()
				if cancel != nil {
					cancel()
				}
				api.liveAttach.mu.Lock()
				stream := api.liveAttach.sessions[tmuxSession]
				api.liveAttach.mu.Unlock()
				if stream != nil {
					stream.stop()
					<-stream.done
				}
			})
			api.markRetainedMainCodingTurnRunning(sessionID, "repaint-execution")
			waitNotBusy(t, api, sessionID, 5*time.Second)
			want := trackedExecutionStatusCompleted
			if !completed {
				want = trackedExecutionStatusFailed
			}
			if got := trackedStatus(api, "repaint-execution"); got != want {
				t.Fatalf("execution status = %q, want %q", got, want)
			}
		})
	}
}

// Dead pane: three consecutive missing checks fail the turn; one transient
// miss does not.
func TestPLAT178DeadPaneFailsAfterConsecutiveMissesOnly(t *testing.T) {
	var checks, failAt atomic.Int64
	failAt.Store(1 << 30)
	inspect := func(string) codingTmuxPaneState {
		n := checks.Add(1)
		if n == 1 || n >= failAt.Load() {
			return codingTmuxPaneMissing
		}
		return codingTmuxPaneAlive
	}
	plat178Thresholds(t, time.Hour, time.Hour, 20*time.Millisecond, inspect)
	const sessionID = "plat178-dead-pane"
	api, terminalStore, terminalID := newPlat178API(t, sessionID, "mlp-muse-plat178-deadpane")
	trackRunning(api, sessionID, "exec-active", "exec-pending")

	api.markRetainedMainCodingTurnRunning(sessionID, "exec-active")
	api.markRetainedMainCodingTurnRunning(sessionID, "exec-pending")

	// One transient miss followed by live checks must keep the turn open.
	time.Sleep(600 * time.Millisecond)
	if !api.isSessionBusy(sessionID) {
		t.Fatal("a single transient pane miss failed the retained turn")
	}
	failAt.Store(checks.Load() + 1)

	waitNotBusy(t, api, sessionID, 5*time.Second)
	if snap, ok := terminalStore.GetRaw(terminalID); !ok || snap.State != "failed" {
		t.Fatalf("terminal = %+v, want failed after the pane disappeared", snap)
	}
	for _, id := range []string{"exec-active", "exec-pending"} {
		if got := trackedStatus(api, id); got != trackedExecutionStatusFailed {
			t.Fatalf("%s status = %q, want failed", id, got)
		}
	}
}

// Real tmux: killing the provider pane mid-turn must fail the retained turn and
// its backlog and release the lane (queued conversation turns then run).
func TestPLAT178RealTmuxKilledPaneReleasesLane(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), terminalTmuxActionTimeout)
	ok, _ := liveAttachTmuxSupported(ctx)
	cancel()
	if !ok {
		t.Skip("tmux unavailable or too old")
	}
	plat178Thresholds(t, time.Hour, time.Hour, 200*time.Millisecond, inspectCodingTmuxPaneState)

	tmuxSession := "mlp-muse-plat178-e2e-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	// A busy Muse-like pane: it never shows an idle composer.
	script := `while true; do echo "⠋ Working…"; sleep 0.2; done`
	if err := exec.Command("tmux", "new-session", "-d", "-s", tmuxSession, "-x", "100", "-y", "30", "sh", "-c", script).Run(); err != nil {
		t.Skipf("cannot create tmux session: %v", err)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", tmuxSession).Run() })

	const sessionID = "plat178-real-tmux"
	terminalID := sessionID + ":main:" + sessionID
	terminalStore := terminals.NewStore()
	terminalStore.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(time.Now(), sessionID, "main:"+sessionID, tmuxSession))
	if _, ok := terminalStore.MarkTurnCompleted(terminalID); !ok {
		t.Fatal("could not prepare idle retained terminal")
	}
	eventStore := internalevents.NewEventStore(100)
	t.Cleanup(eventStore.Stop)
	api := &StreamingAPI{
		eventStore:                          eventStore,
		terminalStore:                       terminalStore,
		liveAttach:                          newLiveAttachManager(),
		activeSessions:                      map[string]*ActiveSessionInfo{sessionID: {SessionID: sessionID, Status: "completed"}},
		retainedMainTurns:                   make(map[string]time.Time),
		retainedMainTurnExecutionIDs:        make(map[string]string),
		retainedMainTurnPendingExecutionIDs: make(map[string][]string),
		retainedMainTurnWatchCancels:        make(map[string]context.CancelFunc),
		retainedMainTurnCompletionEmitted:   make(map[string]time.Time),
		nativeTranscriptSyncInFlight:        make(map[string]bool),
		trackedWorkflowExecutions:           make(map[string]*TrackedWorkflowExecution),
		internalRetainedTurnFinalResponseReader: func(llmproviders.Provider, string, time.Time) string {
			return ""
		},
	}
	eventStore.SetEventAddedCallback(func(ownerSessionID string, event internalevents.Event) {
		terminalStore.HandleEventWithChange(ownerSessionID, event)
		api.observeRetainedMainTurnEvent(ownerSessionID, event)
	})
	trackRunning(api, sessionID, "exec-active", "exec-pending")
	api.markRetainedMainCodingTurnRunning(sessionID, "exec-active")
	api.markRetainedMainCodingTurnRunning(sessionID, "exec-pending")

	time.Sleep(1500 * time.Millisecond)
	if !api.isSessionBusy(sessionID) {
		t.Fatal("busy live pane settled the retained turn")
	}
	if err := exec.Command("tmux", "kill-session", "-t", tmuxSession).Run(); err != nil {
		t.Fatalf("kill tmux session: %v", err)
	}
	waitNotBusy(t, api, sessionID, 15*time.Second)
	for _, id := range []string{"exec-active", "exec-pending"} {
		if got := trackedStatus(api, id); got != trackedExecutionStatusFailed {
			t.Fatalf("%s status = %q, want failed", id, got)
		}
	}
}
