package events

import (
	"path/filepath"
	"testing"

	pkgevents "github.com/manishiitg/mcpagent/events"
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

func transcriptMessage(id, content string) Event {
	return Event{ID: id, Type: "streaming_chunk", Data: &pkgevents.AgentEvent{
		Type: pkgevents.EventType("streaming_chunk"),
		Data: &pkgevents.StreamingChunkEvent{Source: "transcript", Content: content},
	}}
}
