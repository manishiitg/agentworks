package server

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// newSyntheticCollisionFixture models the server A incident: a user message was
// delivered as live input into the retained Cursor CLI (a turn owns the
// session and its main tmux terminal) when a background step's completion
// turn started on the same session.
func newSyntheticCollisionFixture(t *testing.T, sessionID, tmuxSession string) (*StreamingAPI, string) {
	t.Helper()
	store := terminals.NewStore()
	store.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(time.Now(), sessionID, "main:"+sessionID, tmuxSession))
	api := &StreamingAPI{
		terminalStore: store,
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "running"},
		},
	}
	return api, sessionID + ":main:" + sessionID
}

func sessionStatusForTest(api *StreamingAPI, sessionID string) string {
	api.activeSessionsMux.RLock()
	defer api.activeSessionsMux.RUnlock()
	return api.activeSessions[sessionID].Status
}

// An "already in flight" refusal of a synthetic turn means nothing ran: the
// user's turn still owns the CLI. It must not mark the session "error" (the
// reaper closes the owner's terminal on that status), must not fail the main
// terminal, and must leave the session reachable so the completion is
// re-queued for retry.
func TestSyntheticTurnInFlightRefusalIsNotASessionError(t *testing.T) {
	const sessionID = "synthetic-collision-session"
	const tmuxSession = "mlp-cursor-cli-int-collision"
	api, mainTerminalID := newSyntheticCollisionFixture(t, sessionID, tmuxSession)

	// Session.Run's refusal, possibly wrapped by a caller.
	refusal := fmt.Errorf("synthetic turn: %w", mcpagent.ErrTurnAlreadyInFlight)
	api.settleFailedSyntheticTurn(sessionID, mainTerminalID, refusal)

	if got := sessionStatusForTest(api, sessionID); got != "running" {
		t.Fatalf("session status after in-flight refusal = %q, want running", got)
	}
	if snapshot, ok := api.terminalStore.GetRaw(mainTerminalID); !ok || snapshot.State == "failed" {
		t.Fatalf("main terminal after in-flight refusal = %+v (ok=%v), want not failed", snapshot, ok)
	}
	if api.autoNotificationSessionUnreachable(sessionID) {
		t.Fatal("session became unreachable after an in-flight refusal; the completion would be dropped instead of retried")
	}

	gotArgs := stubTerminalTmuxCommand(t)
	if closed := api.cleanupStaleCodingAgentTmuxSessions(time.Now()); closed != 0 {
		t.Fatalf("reaper closed %d terminal(s) after an in-flight refusal, want 0", closed)
	}
	if len(*gotArgs) != 0 {
		t.Fatalf("reaper ran tmux %v after an in-flight refusal; the user's turn lost its terminal", *gotArgs)
	}
}

// Contrast: a genuine synthetic-turn failure still marks the session error and
// the reaper still closes that terminal, so the fix narrows only the refusal.
func TestSyntheticTurnRealFailureStillMarksSessionError(t *testing.T) {
	const sessionID = "synthetic-real-failure-session"
	const tmuxSession = "mlp-cursor-cli-int-failure"
	api, mainTerminalID := newSyntheticCollisionFixture(t, sessionID, tmuxSession)

	api.settleFailedSyntheticTurn(sessionID, mainTerminalID, errors.New("provider crashed"))

	if got := sessionStatusForTest(api, sessionID); got != "error" {
		t.Fatalf("session status after real failure = %q, want error", got)
	}
	gotArgs := stubTerminalTmuxCommand(t)
	if closed := api.cleanupStaleCodingAgentTmuxSessions(time.Now()); closed != 1 {
		t.Fatalf("reaper closed %d terminal(s) after a real failure, want 1", closed)
	}
	if got := strings.Join(*gotArgs, " "); got != "kill-session -t "+tmuxSession {
		t.Fatalf("tmux args = %q, want kill-session", got)
	}
}

// Both synthetic failure sites (stream setup and the asynchronous outcome)
// must go through settleFailedSyntheticTurn, and the asynchronous one must
// still hand the refusal to onComplete (terminalErr) so it is queued for retry.
func TestSyntheticTurnFailureSitesUseInFlightAwareSettle(t *testing.T) {
	raw, err := os.ReadFile("background_agents.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	start := strings.Index(body, "func (api *StreamingAPI) executeSyntheticTurnWithOutcome(")
	if start < 0 {
		t.Fatal("executeSyntheticTurnWithOutcome not found; update this test with the new name")
	}
	fn := body[start:]
	if end := strings.Index(fn[1:], "\nfunc "); end >= 0 {
		fn = fn[:end+1]
	}
	if strings.Contains(fn, `updateSessionStatus(sessionID, "error")`) {
		t.Fatal(`executeSyntheticTurnWithOutcome sets status "error" directly; route failures through settleFailedSyntheticTurn so in-flight refusals are not session errors`)
	}
	if n := strings.Count(fn, "api.settleFailedSyntheticTurn(sessionID, mainTerminalID,"); n != 2 {
		t.Fatalf("settleFailedSyntheticTurn call sites = %d, want 2 (setup error and async outcome)", n)
	}
	asyncFailure := strings.Index(fn, "if turnErr != nil {")
	if asyncFailure < 0 {
		t.Fatal("async turnErr branch not found")
	}
	branch := fn[asyncFailure:]
	if i, j := strings.Index(branch, "terminalErr = turnErr"), strings.Index(branch, "api.settleFailedSyntheticTurn("); i < 0 || j < 0 || i > j {
		t.Fatal("async failure branch must set terminalErr (drives onComplete retry) before settling")
	}
}
