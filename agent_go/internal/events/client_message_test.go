package events

import (
	"path/filepath"
	"testing"
	"time"

	agentevents "github.com/manishiitg/mcpagent/events"
)

func openClientMessageTestStore(t *testing.T, sessionID string) *EventStore {
	t.Helper()
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewEventStore(100)
	t.Cleanup(store.Stop)
	store.SetDurableJournal(journal)
	classifyInteractiveTestSession(t, store, sessionID)
	return store
}

func durableRows(t *testing.T, store *EventStore, sessionID string) []Event {
	t.Helper()
	page, err := store.ReadDurableChatPage(sessionID, DurableEventPageOptions{Limit: 100, FromStart: true})
	if err != nil {
		t.Fatal(err)
	}
	return page.Events
}

func userMessageMetadata(t *testing.T, event Event) map[string]interface{} {
	t.Helper()
	metadata, _ := eventPayloadMap(&event)["metadata"].(map[string]interface{})
	return metadata
}

func TestFreshTurnUserMessageCarriesExpectedClientID(t *testing.T) {
	const session = "chat-fresh"
	store := openClientMessageTestStore(t, session)
	store.ExpectClientUserMessage(session, "sub-1", "hi")

	wrapped := "[AGENTWORKS CONVERSATION CONTINUITY]\nread the archive\n[/AGENTWORKS CONVERSATION CONTINUITY]\n\n[USER MESSAGE]\nhi"
	store.AddEvent(session, journalTestEvent("bridge-random-id", wrapped))
	store.AddEvent(session, journalTestEvent("bridge-other-id", "a later automated message"))

	rows := durableRows(t, store, session)
	if len(rows) != 2 || rows[0].ID != "user:sub-1" {
		t.Fatalf("rows = %v, want user:sub-1 first", journalEventIDs(rows))
	}
	metadata := userMessageMetadata(t, rows[0])
	if metadata["client_message_id"] != "sub-1" || metadata["display_content"] != "hi" {
		t.Fatalf("metadata = %#v, want client id and the typed text as display_content", metadata)
	}
	if rows[1].ID != "bridge-other-id" {
		t.Fatalf("expectation leaked onto a second user_message: %v", journalEventIDs(rows))
	}
}

func TestRetriedClientMessageKeepsOneRow(t *testing.T) {
	const session = "chat-retry"
	store := openClientMessageTestStore(t, session)
	for i := 0; i < 2; i++ {
		store.ExpectClientUserMessage(session, "sub-retry", "same text")
		store.AddEvent(session, journalTestEvent("attempt", "same text"))
	}
	if rows := durableRows(t, store, session); len(rows) != 1 || rows[0].ID != "user:sub-retry" {
		t.Fatalf("rows = %v, want one user:sub-retry", journalEventIDs(rows))
	}
}

func TestIdenticalTextSentTwiceKeepsTwoRows(t *testing.T) {
	const session = "chat-twice"
	store := openClientMessageTestStore(t, session)
	store.ExpectClientUserMessage(session, "sub-a", "ok")
	store.AddEvent(session, journalTestEvent("echo-a", "ok"))
	store.ExpectClientUserMessage(session, "sub-b", "ok")
	store.AddEvent(session, journalTestEvent("echo-b", "ok"))
	rows := durableRows(t, store, session)
	if len(rows) != 2 || rows[0].ID != "user:sub-a" || rows[1].ID != "user:sub-b" {
		t.Fatalf("rows = %v, want user:sub-a then user:sub-b", journalEventIDs(rows))
	}
}

func TestChildUserMessageDoesNotConsumeExpectation(t *testing.T) {
	const session = "chat-child"
	store := openClientMessageTestStore(t, session)
	store.ExpectClientUserMessage(session, "sub-main", "do it")
	child := journalTestEvent("child-echo", "delegated instruction")
	child.ExecutionID = "delegation:d1"
	child.ExecutionKind = "delegation"
	store.AddEvent(session, child)
	store.AddEvent(session, journalTestEvent("main-echo", "do it"))

	var mainID string
	for _, event := range store.GetAllEventsRaw(session) {
		if event.ExecutionKind == "delegation" && event.ID != "child-echo" {
			t.Fatalf("child user_message was stamped: %s", event.ID)
		}
		if event.ExecutionKind != "delegation" && event.Type == string(agentevents.UserMessage) {
			mainID = event.ID
		}
	}
	if mainID != "user:sub-main" {
		t.Fatalf("main user_message id = %q, want user:sub-main", mainID)
	}
}

func TestExpiredClientExpectationIsDropped(t *testing.T) {
	const session = "chat-expired"
	store := openClientMessageTestStore(t, session)
	store.ExpectClientUserMessage(session, "sub-old", "hi")
	store.mu.Lock()
	expected := store.expectedClientMessages[session]
	expected.expires = time.Now().Add(-time.Second)
	store.expectedClientMessages[session] = expected
	store.mu.Unlock()
	store.AddEvent(session, journalTestEvent("late-echo", "hi"))
	if rows := durableRows(t, store, session); len(rows) != 1 || rows[0].ID != "late-echo" {
		t.Fatalf("rows = %v, want the unstamped late echo", journalEventIDs(rows))
	}
}

// PLAT-746-331: live input carried its client id, but retained a turn-scoped
// observer id, duplicating the browser bubble and dropping the next steer.
func TestLiveInputObserverUsesClientIdentityWithoutFreshTurnExpectation(t *testing.T) {
	const session = "chat-live-identity"
	store := openClientMessageTestStore(t, session)
	store.ExpectClientUserMessage(session, "first", "same question")
	store.AddEvent(session, journalTestEvent("fresh-turn", "same question"))
	for _, id := range []string{"first", "second", "second"} {
		event := journalTestEvent("observer:chat-live-identity:turn:one:user_message", "same question")
		userMessageEventData(&event).Metadata = map[string]interface{}{
			"client_message_id": id, "source": "coding_agent_live_input",
		}
		store.AddEvent(session, event)
	}
	rows := durableRows(t, store, session)
	if len(rows) != 2 || rows[0].ID != "user:first" || rows[1].ID != "user:second" {
		t.Fatalf("rows = %v, want distinct submissions once each", journalEventIDs(rows))
	}
}
