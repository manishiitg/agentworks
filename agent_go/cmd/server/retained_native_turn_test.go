package server

import (
	"context"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

// Use the same acknowledgement recording path as both Session.Send handlers,
// rather than populating the identity map by hand.
func acceptNativeTurn(api *StreamingAPI, sessionID, executionID, nativeID string) {
	delivery := mcpagent.DeliveryResult{TurnID: nativeID}
	api.recordMCPAgentSessionLiveInput(sessionID, executionID, "muse-cli", "", delivery.TurnID, executionID)
}

func nativeCompletion(sessionID, eventID, nativeID string) storeevents.Event {
	event := sessionCompletion(sessionID, eventID, time.Now())
	event.Data.TurnID = nativeID
	return event
}

func TestRetainedCompletionMatchesAcceptedNativeTurn(t *testing.T) {
	const sessionID = "retained-native-group"
	api, _, terminalID := newPlat178API(t, sessionID, "mlp-muse-native-group")
	t.Cleanup(func() { api.releaseStoppedSessionTurnMarkers(sessionID) })
	acceptNativeTurn(api, sessionID, "first", "native-one")
	acceptNativeTurn(api, sessionID, "steer", "native-one")
	acceptNativeTurn(api, sessionID, "later", "native-two")
	for _, kind := range []string{"streaming_end", "agent_end", "conversation_end"} {
		api.observeRetainedMainTurnEvent(sessionID, storeevents.Event{ID: kind, Type: kind, Timestamp: time.Now(), TerminalID: terminalID})
	}
	if !api.isSessionBusy(sessionID) || trackedStatus(api, "first") != trackedExecutionStatusRunning {
		t.Fatal("transport ending consumed an accepted input")
	}
	first := nativeCompletion(sessionID, "completion-one", "native-one")
	api.observeRetainedMainTurnEvent(sessionID, first)
	for _, id := range []string{"first", "steer"} {
		if got := trackedStatus(api, id); got != trackedExecutionStatusCompleted {
			t.Fatalf("%s status=%s; one native reply must cover both accepted inputs", id, got)
		}
	}
	api.observeRetainedMainTurnEvent(sessionID, first)
	// A compatibility observer cannot claim the promoted SDK turn's reply.
	snapshot, _ := api.terminalStore.GetRaw(terminalID)
	api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, "muse-cli", "completed", "")
	api.handleRetainedMainTurnStreamClosed(context.Background(), sessionID, snapshot, "muse-cli")
	if !api.isSessionBusy(sessionID) || trackedStatus(api, "later") != trackedExecutionStatusRunning {
		t.Fatal("stale completion or sidecar consumed the newer native turn")
	}
	api.observeRetainedMainTurnEvent(sessionID, nativeCompletion(sessionID, "completion-two", "native-two"))
	if api.isSessionBusy(sessionID) || api.retainedMainTurnTracked(sessionID) || trackedStatus(api, "later") != trackedExecutionStatusCompleted {
		t.Fatal("matching native completion did not settle the later turn")
	}
	if len(api.retainedMainTurnCanonicalIDs) != 0 {
		t.Fatal("completed native identities leaked")
	}
}

func TestRetainedNativeFollowupRemapsAndRejectsPreviousCompletion(t *testing.T) {
	for _, answeredByPrevious := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-answer", true: "answered-previously"}[answeredByPrevious], func(t *testing.T) {
			const sessionID = "retained-native-followup"
			api, _, _ := newPlat178API(t, sessionID, "mlp-muse-native-followup")
			t.Cleanup(func() { api.releaseStoppedSessionTurnMarkers(sessionID) })
			acceptNativeTurn(api, sessionID, "steer", "run-one")
			run := nativeCompletion(sessionID, "run-ended", "run-one")
			run.Data.Data.(*unifiedevents.UnifiedCompletionEvent).Metadata[mcpagent.LiveInputFollowupMetadataKey] = true
			api.observeRetainedMainTurnEvent(sessionID, run)
			stale := nativeCompletion(sessionID, "run-replay", "run-one")
			api.observeRetainedMainTurnEvent(sessionID, stale)
			if !api.isSessionBusy(sessionID) || trackedStatus(api, "steer") != trackedExecutionStatusRunning {
				t.Fatal("Run's old completion claimed the follow-up")
			}
			followup := nativeCompletion(sessionID, "followup-ended", "followup-two")
			completion := followup.Data.Data.(*unifiedevents.UnifiedCompletionEvent)
			if answeredByPrevious {
				completion.FinalResult = ""
				completion.Metadata[mcpagent.AnsweredByPreviousResponseMetadataKey] = true
			}
			api.observeRetainedMainTurnEvent(sessionID, followup)
			if api.isSessionBusy(sessionID) || trackedStatus(api, "steer") != trackedExecutionStatusCompleted {
				t.Fatal("SDK follow-up did not settle its accepted input")
			}
		})
	}
}

func TestRetainedNativeInterruptSettlesAllCoveredInputs(t *testing.T) {
	const sessionID = "retained-native-interrupt"
	api, _, _ := newPlat178API(t, sessionID, "mlp-muse-native-interrupt")
	acceptNativeTurn(api, sessionID, "first", "native-one")
	acceptNativeTurn(api, sessionID, "second", "native-one")
	interrupted := nativeCompletion(sessionID, "interrupted", "native-one")
	completion := interrupted.Data.Data.(*unifiedevents.UnifiedCompletionEvent)
	completion.FinalResult = ""
	completion.Metadata[mcpagent.RetainedTurnOutcomeMetadataKey] = "interrupted"
	api.observeRetainedMainTurnEvent(sessionID, interrupted)
	if api.isSessionBusy(sessionID) || api.retainedMainTurnTracked(sessionID) || trackedStatus(api, "second") == trackedExecutionStatusRunning {
		t.Fatal("interrupted native turn left accepted input running")
	}
	if len(api.retainedMainTurnCanonicalIDs) != 0 {
		t.Fatal("interruption leaked native identities")
	}
}

func TestRetainedNativeMissingCompletionHasFailureBackstop(t *testing.T) {
	for _, paneGone := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "pane-gone"}[paneGone], func(t *testing.T) {
			inspect := paneAlive
			if paneGone {
				inspect = func(string) codingTmuxPaneState { return codingTmuxPaneMissing }
			}
			plat178Thresholds(t, time.Hour, 150*time.Millisecond, 10*time.Millisecond, inspect)
			const sessionID = "retained-native-lost"
			api, _, _ := newPlat178API(t, sessionID, "mlp-muse-native-lost")
			acceptNativeTurn(api, sessionID, "first", "native-one")
			acceptNativeTurn(api, sessionID, "later", "native-two")
			waitNotBusy(t, api, sessionID, 2*time.Second)
			for _, id := range []string{"first", "later"} {
				if got := trackedStatus(api, id); got != trackedExecutionStatusFailed {
					t.Fatalf("lost completion left %s status=%s", id, got)
				}
			}
			api.retainedMainTurnsMu.Lock()
			remaining := len(api.retainedMainTurnCanonicalIDs)
			api.retainedMainTurnsMu.Unlock()
			if remaining != 0 {
				t.Fatal("failure leaked native identities")
			}
		})
	}
}

func TestRetainedNativeStopClearsAcceptedIdentities(t *testing.T) {
	const sessionID = "retained-native-stop"
	api, _, _ := newPlat178API(t, sessionID, "mlp-muse-native-stop")
	acceptNativeTurn(api, sessionID, "first", "native-one")
	acceptNativeTurn(api, sessionID, "second", "native-two")
	api.releaseStoppedSessionTurnMarkers(sessionID)
	if api.retainedMainTurnTracked(sessionID) || len(api.retainedMainTurnCanonicalIDs) != 0 || len(api.retainedMainTurnWatchCancels) != 0 {
		t.Fatal("stopped native turn kept identities or a watchdog")
	}
}

func TestRetainedNativeCompletionBeforeAcknowledgementIsReconciled(t *testing.T) {
	const sessionID = "retained-native-fast"
	api, _, _ := newPlat178API(t, sessionID, "mlp-muse-native-fast")
	t.Cleanup(func() { api.releaseStoppedSessionTurnMarkers(sessionID) })
	api.eventStore.AddEvent(sessionID, nativeCompletion(sessionID, "older", "old-turn"))
	api.eventStore.AddEvent(sessionID, nativeCompletion(sessionID, "fast", "accepted-turn"))
	acceptNativeTurn(api, sessionID, "accepted", "accepted-turn")
	if api.isSessionBusy(sessionID) || api.retainedMainTurnTracked(sessionID) || trackedStatus(api, "accepted") != trackedExecutionStatusCompleted {
		t.Fatal("a completion preceding host acknowledgement was lost")
	}
}
