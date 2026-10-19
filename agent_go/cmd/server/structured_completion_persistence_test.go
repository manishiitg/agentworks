package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
)

func retainedCompletionEvent(sessionID, turnID, eventID, final string) storeevents.Event {
	completedAt := time.Date(2026, 9, 21, 11, 0, 0, 0, time.UTC)
	completion := agentevents.NewUnifiedCompletionEvent("coding_agent", "retained", "", final, "completed", time.Second, 1)
	completion.SessionID = sessionID
	completion.Metadata["source"] = "mcpagent_session"
	completion.Metadata["turn_id"] = turnID
	return storeevents.Event{
		ID: eventID, Type: string(agentevents.EventTypeUnifiedCompletion), Timestamp: completedAt,
		SessionID: sessionID, ExecutionID: "main:" + sessionID, Sequence: 12,
		Data: &agentevents.AgentEvent{Type: agentevents.EventTypeUnifiedCompletion, Timestamp: completedAt, SessionID: sessionID, TurnID: turnID, Data: completion},
	}
}

func TestRetainedStructuredFinalRequiresCompletedReply(t *testing.T) {
	event := retainedCompletionEvent("session-1", "turn-1", "completion-1", "done")
	final, turnID, ok := retainedStructuredFinal(event)
	if !ok || final != "done" || turnID != "turn-1" {
		t.Fatalf("structured final = %q/%q/%v", final, turnID, ok)
	}
	event.Data.TurnID = ""
	if _, turnID, ok := retainedStructuredFinal(event); !ok || turnID != "turn-1" {
		t.Fatalf("completion metadata turn ID was not preferred over execution ownership: %q/%v", turnID, ok)
	}
	event.Data.Data.(*agentevents.UnifiedCompletionEvent).FinalResult = ""
	if _, _, ok := retainedStructuredFinal(event); ok {
		t.Fatal("completion without final response was accepted as durable reply")
	}
	event.Type = "streaming_end"
	event.Data.Data.(*agentevents.UnifiedCompletionEvent).FinalResult = "done"
	if _, _, ok := retainedStructuredFinal(event); ok {
		t.Fatal("streaming lifecycle event was accepted as durable reply")
	}
}

func TestStructuredCompletionPersistsExactlyOnceByTurn(t *testing.T) {
	const (
		userID        = "alice"
		sessionID     = "retained-chat"
		workspacePath = "Workflow/project"
		conversation  = "Workflow/project/builder/conversation/2026-09-21/session-retained-chat-conversation.json"
	)
	record := map[string]interface{}{
		"session_id": sessionID,
		"user_id":    userID,
		"agent_mode": "workflow_phase",
		"conversation_history": []map[string]interface{}{
			{"Role": "human", "Parts": []map[string]string{{"Text": "run it"}}},
		},
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	workspace := &mockWorkspaceAPI{files: map[string]string{conversation: string(raw)}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	event := retainedCompletionEvent(sessionID, "turn-1", "completion-1", "finished")
	api := &StreamingAPI{}
	if !api.appendStructuredAssistantReply(userID, sessionID, workspacePath, "turn-1", "finished", event) {
		t.Fatal("structured completion was not persisted")
	}
	if !api.appendStructuredAssistantReply(userID, sessionID, workspacePath, "turn-1", "finished", event) {
		t.Fatal("idempotent replay did not adopt the durable checkpoint")
	}

	var persisted struct {
		History     []builderConversationMessage `json:"conversation_history"`
		Checkpoints map[string]interface{}       `json:"structured_turn_checkpoints"`
		UIEvents    []storeevents.Event          `json:"ui_events"`
	}
	if err := json.Unmarshal([]byte(workspace.files[conversation]), &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.History) != 2 || persisted.History[1].Role != "ai" || persisted.History[1].Parts[0].Text != "finished" {
		t.Fatalf("history = %+v, want one structured assistant reply", persisted.History)
	}
	if _, ok := persisted.Checkpoints["turn-1"]; !ok {
		t.Fatalf("missing exact turn checkpoint: %+v", persisted.Checkpoints)
	}
	if len(persisted.UIEvents) != 1 || persisted.UIEvents[0].ID != "completion-1" || persisted.UIEvents[0].Sequence != 12 {
		t.Fatalf("durable structured events = %+v", persisted.UIEvents)
	}
}

func TestStructuredCompletionPreservesLegitimateRepeatedRepliesAcrossTurns(t *testing.T) {
	const (
		userID        = "alice"
		sessionID     = "repeat-chat"
		workspacePath = "Workflow/project"
		conversation  = "Workflow/project/builder/conversation/2026-09-21/session-repeat-chat-conversation.json"
	)
	record := map[string]interface{}{
		"session_id": sessionID,
		"user_id":    userID,
		"conversation_history": []map[string]interface{}{
			{"Role": "human", "Parts": []map[string]string{{"Text": "first"}}},
		},
	}
	raw, _ := json.Marshal(record)
	workspace := &mockWorkspaceAPI{files: map[string]string{conversation: string(raw)}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	api := &StreamingAPI{}
	if !api.appendStructuredAssistantReply(userID, sessionID, workspacePath, "turn-1", "same answer", retainedCompletionEvent(sessionID, "turn-1", "completion-1", "same answer")) {
		t.Fatal("first reply was not persisted")
	}

	var current map[string]interface{}
	if json.Unmarshal([]byte(workspace.files[conversation]), &current) != nil {
		t.Fatal("cannot decode first append")
	}
	history := current["conversation_history"].([]interface{})
	current["conversation_history"] = append(history, map[string]interface{}{"Role": "human", "Parts": []map[string]string{{"Text": "second"}}})
	updated, _ := json.Marshal(current)
	workspace.files[conversation] = string(updated)
	if !api.appendStructuredAssistantReply(userID, sessionID, workspacePath, "turn-2", "same answer", retainedCompletionEvent(sessionID, "turn-2", "completion-2", "same answer")) {
		t.Fatal("second reply was not persisted")
	}
	var persisted builderConversationLog
	if json.Unmarshal([]byte(workspace.files[conversation]), &persisted) != nil || len(persisted.ConversationHistory) != 4 {
		t.Fatalf("legitimate repeated reply was collapsed: %+v", persisted.ConversationHistory)
	}
}

func TestStructuredCompletionPreservesBothAnswersAfterRapidPrompts(t *testing.T) {
	const sessionID = "rapid-replies"
	const workspacePath = "Workflow/project"
	conversation := workspacePath + "/builder/conversation/2026-09-21/" + chatHistoryConversationFileName(sessionID)
	raw, _ := json.Marshal(map[string]interface{}{
		"session_id": sessionID, "user_id": "alice",
		"conversation_history": []map[string]interface{}{
			{"Role": "human", "Parts": []map[string]string{{"Text": "first"}}},
			{"Role": "human", "Parts": []map[string]string{{"Text": "second"}}},
		},
	})
	workspace := &mockWorkspaceAPI{files: map[string]string{conversation: string(raw)}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	api := &StreamingAPI{}
	for _, turnID := range []string{"turn-one", "turn-two", "turn-two"} {
		if !api.appendStructuredAssistantReply("alice", sessionID, workspacePath, turnID, "same answer", retainedCompletionEvent(sessionID, turnID, "completion-"+turnID, "same answer")) {
			t.Fatal("failed to persist rapid reply")
		}
	}
	var persisted builderConversationLog
	if json.Unmarshal([]byte(workspace.files[conversation]), &persisted) != nil || len(persisted.ConversationHistory) != 4 {
		t.Fatalf("distinct native replies were collapsed or replayed: %+v", persisted.ConversationHistory)
	}
	before := workspace.files[conversation]
	if api.appendStructuredAssistantReply("bob", sessionID, workspacePath, "wrong-user", "private", retainedCompletionEvent(sessionID, "wrong-user", "wrong-user", "private")) {
		t.Fatal("another user was allowed to append to the transcript")
	}
	if workspace.files[conversation] != before {
		t.Fatal("denied append changed private history")
	}
}

func TestStructuredCompletionDoesNotAdoptIdenticalUncheckpointedForegroundAnswer(t *testing.T) {
	const sessionID = "foreground-then-retained"
	const workspacePath = "Workflow/project"
	conversation := workspacePath + "/builder/conversation/2026-09-21/" + chatHistoryConversationFileName(sessionID)
	raw, _ := json.Marshal(map[string]interface{}{
		"session_id": sessionID, "user_id": "alice",
		"conversation_history": []map[string]interface{}{
			{"Role": "human", "Parts": []map[string]string{{"Text": "first"}}},
			{"Role": "human", "Parts": []map[string]string{{"Text": "second"}}},
			{"Role": "ai", "Parts": []map[string]string{{"Text": "same answer"}}},
		},
	})
	workspace := &mockWorkspaceAPI{files: map[string]string{conversation: string(raw)}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	api := &StreamingAPI{}
	for range 2 {
		if !api.appendStructuredAssistantReply("alice", sessionID, workspacePath, "new-native-turn", "same answer", retainedCompletionEvent(sessionID, "new-native-turn", "new-completion", "same answer")) {
			t.Fatal("failed to append the retained reply")
		}
	}
	var persisted builderConversationLog
	if json.Unmarshal([]byte(workspace.files[conversation]), &persisted) != nil || len(persisted.ConversationHistory) != 4 {
		t.Fatalf("native reply was mistaken for an old foreground answer: %+v", persisted.ConversationHistory)
	}
}

// A Code or Work project chat keeps its transcript inside the project and the server does not know its workspace
// folder (a restart empties every in-memory map); the reply to a live input must still be saved, found through the
// chat's active session.
func TestStructuredCompletionFindsProjectTranscriptWithoutWorkspace(t *testing.T) {
	const (
		userID    = "alice"
		sessionID = "code:project:abc"
		project   = "_users/alice/Chats/Code/projects/demo-abc"
	)
	conversation := project + "/builder/conversation/2026-10-14/" + chatHistoryConversationFileName(sessionID)
	raw, _ := json.Marshal(map[string]interface{}{
		"session_id": sessionID, "user_id": userID,
		"conversation_history": []map[string]interface{}{{"Role": "human", "Parts": []map[string]string{{"Text": "hi"}}}},
	})
	workspace := &mockWorkspaceAPI{files: map[string]string{conversation: string(raw)}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{}}
	event := retainedCompletionEvent(sessionID, "turn-1", "completion-1", "the reply")
	if api.appendStructuredAssistantReply(userID, sessionID, "", "turn-1", "the reply", event) {
		t.Fatal("saved without any way to find the transcript")
	}
	api.activeSessions[sessionID] = &ActiveSessionInfo{SessionID: sessionID, UserID: userID, WorkspacePath: project}
	if !api.appendStructuredAssistantReply(userID, sessionID, "", "turn-1", "the reply", event) {
		t.Fatal("reply not saved into the project transcript")
	}
}

// The tool calls of a live-input turn happen after the Run's save; they must reach the stored events with the reply.
func TestStructuredCompletionStoresTheTurnsToolEvents(t *testing.T) {
	const (
		userID       = "alice"
		sessionID    = "retained-tools"
		workspaceDir = "Workflow/project"
	)
	conversation := workspaceDir + "/builder/conversation/2026-09-21/" + chatHistoryConversationFileName(sessionID)
	raw, _ := json.Marshal(map[string]interface{}{
		"session_id": sessionID, "user_id": userID,
		"conversation_history": []map[string]interface{}{{"Role": "human", "Parts": []map[string]string{{"Text": "go"}}}},
	})
	workspace := &mockWorkspaceAPI{files: map[string]string{conversation: string(raw)}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	store := storeevents.NewEventStore(100)
	api := &StreamingAPI{eventStore: store}
	at := time.Date(2026, 9, 21, 10, 59, 0, 0, time.UTC)
	for _, id := range []string{"tool-start", "tool-end"} {
		store.AddEvent(sessionID, storeevents.Event{ID: id, Type: "tool_call_start", Timestamp: at, SessionID: sessionID})
	}
	event := retainedCompletionEvent(sessionID, "turn-1", "completion-1", "done")
	store.AddEvent(sessionID, event)
	if !api.appendStructuredAssistantReply(userID, sessionID, workspaceDir, "turn-1", "done", event) {
		t.Fatal("reply not saved")
	}
	var persisted struct {
		UIEvents []storeevents.Event `json:"ui_events"`
	}
	if err := json.Unmarshal([]byte(workspace.files[conversation]), &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.UIEvents) != 3 {
		t.Fatalf("stored events = %d, want the 2 tool events and the completion", len(persisted.UIEvents))
	}
	// A second live-input completion merges the whole store again; the first turn's events must not double.
	event2 := retainedCompletionEvent(sessionID, "turn-2", "completion-2", "done again")
	store.AddEvent(sessionID, event2)
	if !api.appendStructuredAssistantReply(userID, sessionID, workspaceDir, "turn-2", "done again", event2) {
		t.Fatal("second reply not saved")
	}
	if err := json.Unmarshal([]byte(workspace.files[conversation]), &persisted); err != nil {
		t.Fatal(err)
	}
	tools := 0
	for _, e := range persisted.UIEvents {
		if e.Type == "tool_call_start" {
			tools++
		}
	}
	if tools != 2 || len(persisted.UIEvents) != 4 {
		t.Fatalf("after a second turn: %d tool events of %d stored, want 2 of 4", tools, len(persisted.UIEvents))
	}
}
