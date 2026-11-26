package server

import (
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
)

type fakeRetainedSession struct{ active string }

func (s fakeRetainedSession) ActiveTurnID() string { return s.active }

func fakeLookupRetainedSession(t *testing.T, sessionID string, found bool, active string) {
	t.Helper()
	old := lookupRetainedSession
	lookupRetainedSession = func(id string) (interface{ ActiveTurnID() string }, bool) {
		if id != sessionID || !found {
			return nil, false
		}
		return fakeRetainedSession{active: active}, true
	}
	t.Cleanup(func() { lookupRetainedSession = old })
}

func trackNativeSteeredInput(t *testing.T, api *StreamingAPI, sessionID, queryID string) {
	t.Helper()
	trackRunning(api, sessionID, queryID)
	api.retainedMainTurnsMu.Lock()
	api.retainedMainTurnCanonicalIDs = map[string]retainedTurnIdentity{queryID: {ID: "run-turn", Provider: "claude-code", Transport: "sdk", NativeSessionID: "native-session"}}
	api.retainedMainTurnsMu.Unlock()
	api.markMCPAgentSessionTurnRunning(sessionID, queryID)
	t.Cleanup(func() {
		api.retainedMainTurnsMu.Lock()
		cancel := api.retainedMainTurnWatchCancels[sessionID]
		api.retainedMainTurnsMu.Unlock()
		if cancel != nil {
			cancel()
		}
	})
	if !api.retainedMainTurnTracked(sessionID) || !api.retainedMainTurnIsNative(sessionID) {
		t.Fatal("steered SDK input was not tracked as a native retained turn")
	}
}

// Air-2 2026-11-23: a message steered into a running Claude SDK reply was
// answered inside that reply, so no completion of its own ever came and the
// chat stayed "running" with later messages queued until Stop. An idle native
// Session is proof nothing else will answer it; anything less is not.
func TestNativeSteeredInputReleasedOnlyWhenSessionIdle(t *testing.T) {
	for _, tc := range []struct {
		name          string
		found         bool
		active        string
		wantReleased  bool
		wantExecution string
	}{
		{name: "session idle", found: true, wantReleased: true, wantExecution: trackedExecutionStatusCompleted},
		{name: "session still answering", found: true, active: "run-turn", wantExecution: trackedExecutionStatusRunning},
		{name: "no session to ask", wantExecution: trackedExecutionStatusRunning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const sessionID, queryID = "native-steer-idle", "query-steered"
			api, _, _ := newPlat178API(t, sessionID, "unused-pane")
			api.terminalStore = terminals.NewStore()
			fakeLookupRetainedSession(t, sessionID, tc.found, tc.active)
			trackNativeSteeredInput(t, api, sessionID, queryID)

			if got := api.releaseStaleRetainedMainTurn(sessionID, "foreground_turn_answered_native_input"); got != tc.wantReleased {
				t.Fatalf("released=%v, want %v", got, tc.wantReleased)
			}
			if got := trackedStatus(api, queryID); got != tc.wantExecution {
				t.Fatalf("input status=%s, want %s", got, tc.wantExecution)
			}
			if tracked := api.retainedMainTurnTracked(sessionID); tracked == tc.wantReleased {
				t.Fatalf("retained mark tracked=%v after release=%v", tracked, tc.wantReleased)
			}
			if busy := api.isSessionBusy(sessionID); busy == tc.wantReleased {
				t.Fatalf("chat busy=%v after release=%v", busy, tc.wantReleased)
			}
		})
	}
}

// The session watchdog used to skip terminal-less native Sessions entirely,
// so its idle release never ran for Claude SDK chats.
func TestNativeSessionWatchdogReleasesIdleSteeredInput(t *testing.T) {
	plat178Thresholds(t, time.Hour, time.Hour, 20*time.Millisecond, paneAlive)
	const sessionID, queryID = "native-steer-watchdog", "query-steered"
	api, _, _ := newPlat178API(t, sessionID, "unused-pane")
	api.terminalStore = terminals.NewStore()
	fakeLookupRetainedSession(t, sessionID, true, "")
	trackNativeSteeredInput(t, api, sessionID, queryID)

	waitNotBusy(t, api, sessionID, 2*time.Second)
	if got := trackedStatus(api, queryID); got != trackedExecutionStatusCompleted {
		t.Fatalf("input status=%s, want completed", got)
	}
}

func TestNativeSessionWatchdogKeepsInputWhileSessionAnswers(t *testing.T) {
	plat178Thresholds(t, time.Hour, time.Hour, 20*time.Millisecond, paneAlive)
	const sessionID, queryID = "native-steer-active", "query-steered"
	api, _, _ := newPlat178API(t, sessionID, "unused-pane")
	api.terminalStore = terminals.NewStore()
	fakeLookupRetainedSession(t, sessionID, true, "run-turn")
	trackNativeSteeredInput(t, api, sessionID, queryID)

	time.Sleep(200 * time.Millisecond)
	if !api.retainedMainTurnTracked(sessionID) || trackedStatus(api, queryID) != trackedExecutionStatusRunning {
		t.Fatal("watchdog released an input the native Session is still answering")
	}
}
