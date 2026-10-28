package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func newRunHandoffAPI(t *testing.T, sessionID, tmuxSession string) *StreamingAPI {
	api, _, _ := newPlat178API(t, sessionID, tmuxSession)
	api.internalDurableAckHandler = func(context.Context, llmproviders.Provider, string, string) (llmtypes.DurableAck, error) {
		return llmtypes.DurableAck{Outcome: llmtypes.DurableAckConfirmed}, nil
	}
	return api
}

func acceptRunHandoffInput(t *testing.T, api *StreamingAPI, sessionID, executionID, nativeID string, acceptedAt ...time.Time) {
	t.Helper()
	boundary := time.Now()
	if len(acceptedAt) > 0 {
		boundary = acceptedAt[0]
	}
	id := api.recordMCPAgentSessionLiveInputAt(sessionID, executionID, "muse-cli", "", nativeID, boundary, executionID)
	// The real recorder defers answer events until the durable ack. Wait for
	// that mocked ack rather than asserting while the event-store hold is active.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, event := range api.eventStore.GetAllEventsRaw(sessionID) {
			if event.ID == id+":confirmed" {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("mocked durable acknowledgment did not flush accepted input")
}

// Session.Run's ordinary completion is canonical, but has no retained source
// and no terminal ID. The old nativeCompletion fixture concealed this shape.
func canonicalRunHandoff(sessionID, nativeID string) storeevents.Event {
	event := nativeCompletion(sessionID, "run-completed", nativeID)
	completion := event.Data.Data.(*unifiedevents.UnifiedCompletionEvent)
	delete(completion.Metadata, "source")
	completion.Metadata["canonical_turn_completion"] = true
	completion.Metadata[mcpagent.LiveInputFollowupMetadataKey] = true
	event.TerminalID = ""
	return event
}

func TestRetainedCanonicalRunHandoffBeforeAndAfterAcknowledgement(t *testing.T) {
	for _, lateAck := range []bool{false, true} {
		t.Run(fmt.Sprint("late-ack-", lateAck), func(t *testing.T) {
			const sessionID = "canonical-run-handoff"
			api := newRunHandoffAPI(t, sessionID, "mlp-muse-run-handoff")
			t.Cleanup(func() { api.releaseStoppedSessionTurnMarkers(sessionID) })
			started := time.Now()
			accept := func() {
				acceptRunHandoffInput(t, api, sessionID, "input", "run-one", started)
			}
			if !lateAck {
				accept()
			}
			api.eventStore.AddEvent(sessionID, canonicalRunHandoff(sessionID, "run-one"))
			followup := nativeCompletion(sessionID, "followup", "followup-one")
			if lateAck {
				// Both final events can beat Send's durable acknowledgment.
				api.eventStore.AddEvent(sessionID, followup)
				accept()
			} else {
				if !api.isSessionBusy(sessionID) || api.retainedMainTurnCanonicalIDs["input"].FollowupFrom != "run-one" {
					t.Fatal("normal Run did not hand off its accepted live input")
				}
				api.observeRetainedMainTurnEvent(sessionID, followup)
			}
			if api.isSessionBusy(sessionID) || trackedStatus(api, "input") != trackedExecutionStatusCompleted {
				t.Fatal("canonical follow-up left accepted input Working")
			}
		})
	}
}

func TestRetainedCanonicalRunFourLaterSteersPersistOwnReplies(t *testing.T) {
	const sessionID = "rts-four-steers"
	const workspacePath = "Workflow/project"
	conversation := workspacePath + "/builder/conversation/2026-10-19/" + chatHistoryConversationFileName(sessionID)
	raw, _ := json.Marshal(map[string]interface{}{
		"session_id": sessionID, "user_id": "alice",
		"conversation_history": []map[string]interface{}{{"Role": "human", "Parts": []map[string]string{{"Text": "start"}}}},
	})
	workspace := &mockWorkspaceAPI{files: map[string]string{conversation: string(raw)}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	api := newRunHandoffAPI(t, sessionID, "mlp-muse-four-steers")
	t.Cleanup(func() { api.releaseStoppedSessionTurnMarkers(sessionID) })
	api.activeSessions[sessionID].UserID = "alice"
	api.sessionWorkspaceFolders = map[string]string{sessionID: workspacePath}
	// Defensive replay case derived from RTS's stale expected Run ID. Its logs
	// do not include SDK delivery.TurnIDs, so stale acknowledgments are synthetic.
	// Every later input must ignore previous follow-up replies.
	api.eventStore.AddEvent(sessionID, canonicalRunHandoff(sessionID, "finished-run"))
	for i := 1; i <= 4; i++ {
		input, nativeID := fmt.Sprintf("input-%d", i), fmt.Sprintf("reply-%d", i)
		acceptRunHandoffInput(t, api, sessionID, input, "finished-run")
		if !api.isSessionBusy(sessionID) || trackedStatus(api, input) != trackedExecutionStatusRunning {
			t.Fatalf("old replay consumed fresh input %d", i)
		}
		event := nativeCompletion(sessionID, nativeID, nativeID)
		event.Data.Data.(*unifiedevents.UnifiedCompletionEvent).FinalResult = nativeID
		api.eventStore.AddEvent(sessionID, event)
		if api.isSessionBusy(sessionID) || trackedStatus(api, input) != trackedExecutionStatusCompleted {
			t.Fatalf("reply %d left the chat Working", i)
		}
		// Replaying a completion cannot append it again.
		api.observeRetainedMainTurnEvent(sessionID, event)
	}
	var saved struct {
		History     []builderConversationMessage `json:"conversation_history"`
		Checkpoints map[string]interface{}       `json:"structured_turn_checkpoints"`
	}
	if err := json.Unmarshal([]byte(workspace.files[conversation]), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.History) != 5 || len(saved.Checkpoints) != 4 {
		t.Fatalf("want four distinct durable replies, got history=%d checkpoints=%d", len(saved.History), len(saved.Checkpoints))
	}
	for i := 1; i <= 4; i++ {
		if saved.History[i].Role != "ai" || saved.History[i].Parts[0].Text != fmt.Sprintf("reply-%d", i) {
			t.Fatalf("reply %d missing or replaced: %+v", i, saved.History[i])
		}
	}
}

func TestRetainedCanonicalRunFollowupCompletionCoversLaterAcceptedInput(t *testing.T) {
	const sessionID = "run-handoff-known-pending"
	api := newRunHandoffAPI(t, sessionID, "mlp-muse-known-pending")
	t.Cleanup(func() { api.releaseStoppedSessionTurnMarkers(sessionID) })
	acceptRunHandoffInput(t, api, sessionID, "steer", "run-one")
	api.eventStore.AddEvent(sessionID, canonicalRunHandoff(sessionID, "run-one"))
	acceptRunHandoffInput(t, api, sessionID, "newer", "known-two")
	api.eventStore.AddEvent(sessionID, nativeCompletion(sessionID, "newer-reply", "known-two"))
	// The later Send adopts the active follow-up lifecycle. There is no second
	// followup-one completion: this single reply must settle both inputs.
	for _, id := range []string{"steer", "newer"} {
		if trackedStatus(api, id) != trackedExecutionStatusCompleted {
			t.Fatalf("lost %s completion during promotion", id)
		}
	}
	if api.isSessionBusy(sessionID) || api.retainedMainTurnTracked(sessionID) {
		t.Fatal("coalesced SDK follow-up left the chat Working")
	}
}

func TestRetainedCanonicalRunRequiresExactAcceptedHandoff(t *testing.T) {
	const sessionID = "canonical-run-no-fallback"
	api := newRunHandoffAPI(t, sessionID, "mlp-muse-no-fallback")
	t.Cleanup(func() { api.releaseStoppedSessionTurnMarkers(sessionID) })
	acceptRunHandoffInput(t, api, sessionID, "input", "run-one")
	wrong := canonicalRunHandoff(sessionID, "child-or-old-run")
	api.observeRetainedMainTurnEvent(sessionID, wrong)
	unmarked := canonicalRunHandoff(sessionID, "run-one")
	delete(unmarked.Data.Data.(*unifiedevents.UnifiedCompletionEvent).Metadata, "canonical_turn_completion")
	api.observeRetainedMainTurnEvent(sessionID, unmarked)
	if identity := api.retainedMainTurnCanonicalIDs["input"]; identity.ID != "run-one" || identity.FollowupFrom != "" {
		t.Fatalf("unrelated/unmarked completion changed ownership: %+v", identity)
	}
	api.observeRetainedMainTurnEvent(sessionID, nativeCompletion(sessionID, "unknown", "unrelated"))
	if !api.isSessionBusy(sessionID) || trackedStatus(api, "input") != trackedExecutionStatusRunning {
		t.Fatal("unknown completion settled an exact accepted ID")
	}
}

func TestRetainedNativeCompletionWithoutTimestampStillMatchesExactOwner(t *testing.T) {
	const sessionID = "native-unknown-timestamp"
	api := newRunHandoffAPI(t, sessionID, "mlp-muse-unknown-timestamp")
	t.Cleanup(func() { api.releaseStoppedSessionTurnMarkers(sessionID) })
	acceptRunHandoffInput(t, api, sessionID, "input", "native-one")
	event := nativeCompletion(sessionID, "reply", "native-one")
	event.Timestamp = time.Time{}
	api.observeRetainedMainTurnEvent(sessionID, event)
	if api.isSessionBusy(sessionID) || trackedStatus(api, "input") != trackedExecutionStatusCompleted {
		t.Fatal("unknown event timestamp was treated as evidence the completion is stale")
	}
}

func TestRetainedNativeOldExactCompletionCannotAnswerFreshInput(t *testing.T) {
	const sessionID = "native-old-exact-replay"
	api := newRunHandoffAPI(t, sessionID, "mlp-muse-old-exact-replay")
	t.Cleanup(func() { api.releaseStoppedSessionTurnMarkers(sessionID) })
	old := nativeCompletion(sessionID, "old-reply", "native-one")
	api.eventStore.AddEvent(sessionID, old)
	acceptRunHandoffInput(t, api, sessionID, "fresh", "native-one")
	if !api.isSessionBusy(sessionID) || trackedStatus(api, "fresh") != trackedExecutionStatusRunning {
		t.Fatal("old exact-ID replay consumed a fresh accepted input")
	}
}

// Excellence 2026-10-28: a retained turn nothing watched kept the chat "working" and 15 Stops only held. Stop must
// release a retained turn that is still open after the provider had its chance to end it.
func TestStopReleasesRetainedTurnNothingEnds(t *testing.T) {
	const sessionID = "stop-retained-turn"
	api := newRunHandoffAPI(t, sessionID, "mlp-muse-stop-retained")
	t.Cleanup(func() { api.releaseStoppedSessionTurnMarkers(sessionID) })
	acceptRunHandoffInput(t, api, sessionID, "input-1", "finished-run")
	if !api.isSessionBusy(sessionID) || !api.retainedMainTurnTracked(sessionID) {
		t.Fatal("setup: the retained turn should be open")
	}
	old := stoppedTurnSettleWait
	stoppedTurnSettleWait = 0
	defer func() { stoppedTurnSettleWait = old }()
	api.settleStoppedTurnWithNoOwner(sessionID)
	if api.isSessionBusy(sessionID) || api.retainedMainTurnTracked(sessionID) {
		t.Fatal("Stop left the retained turn open")
	}
}
