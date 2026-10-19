package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

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
			api, _, _ := newPlat178API(t, sessionID, "mlp-muse-run-handoff")
			t.Cleanup(func() { api.releaseStoppedSessionTurnMarkers(sessionID) })
			started := time.Now()
			accept := func() {
				api.recordMCPAgentSessionLiveInputAt(sessionID, "input", "muse-cli", "", "run-one", started, "input")
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
	api, _, _ := newPlat178API(t, sessionID, "mlp-muse-four-steers")
	t.Cleanup(func() { api.releaseStoppedSessionTurnMarkers(sessionID) })
	api.activeSessions[sessionID].UserID = "alice"
	api.sessionWorkspaceFolders = map[string]string{sessionID: workspacePath}
	// RTS: the normal Run already ended, but later delivery acknowledgments
	// still identify it. Every later input must ignore previous follow-up replies.
	api.eventStore.AddEvent(sessionID, canonicalRunHandoff(sessionID, "finished-run"))
	for i := 1; i <= 4; i++ {
		input, nativeID := fmt.Sprintf("input-%d", i), fmt.Sprintf("reply-%d", i)
		api.recordMCPAgentSessionLiveInputAt(sessionID, input, "claude-code", "", "finished-run", time.Now(), input)
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

func TestRetainedCanonicalRunHandoffCannotClaimKnownPendingTurn(t *testing.T) {
	const sessionID = "run-handoff-known-pending"
	api, _, _ := newPlat178API(t, sessionID, "mlp-muse-known-pending")
	t.Cleanup(func() { api.releaseStoppedSessionTurnMarkers(sessionID) })
	acceptNativeTurn(api, sessionID, "steer", "run-one")
	api.eventStore.AddEvent(sessionID, canonicalRunHandoff(sessionID, "run-one"))
	acceptNativeTurn(api, sessionID, "newer", "known-two")
	api.eventStore.AddEvent(sessionID, nativeCompletion(sessionID, "newer-reply", "known-two"))
	if trackedStatus(api, "steer") != trackedExecutionStatusRunning || trackedStatus(api, "newer") != trackedExecutionStatusRunning {
		t.Fatal("unresolved handoff stole a pending input's known completion")
	}
	api.eventStore.AddEvent(sessionID, nativeCompletion(sessionID, "steer-reply", "followup-one"))
	for _, id := range []string{"steer", "newer"} {
		if trackedStatus(api, id) != trackedExecutionStatusCompleted {
			t.Fatalf("lost %s completion during promotion", id)
		}
	}
}

func TestRetainedCanonicalRunRequiresExactAcceptedHandoff(t *testing.T) {
	const sessionID = "canonical-run-no-fallback"
	api, _, _ := newPlat178API(t, sessionID, "mlp-muse-no-fallback")
	t.Cleanup(func() { api.releaseStoppedSessionTurnMarkers(sessionID) })
	acceptNativeTurn(api, sessionID, "input", "run-one")
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
