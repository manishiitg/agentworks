package events

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentevents "github.com/manishiitg/mcpagent/events"
)

// A finished thinking block survives a chat rebuild; a streamed fragment and an empty block do not (PLAT-832).
func TestOnlyFinishedThinkingIsDurable(t *testing.T) {
	thinking := func(text string, delta bool) Event {
		return Event{ID: "t", Type: "conversation_thinking", Timestamp: time.Now(),
			Data: agentevents.NewAgentEvent(&agentevents.ConversationThinkingEvent{Thinking: text, IsDelta: delta})}
	}
	if !IsDurableChatEvent(thinking("Checking the skill files", false)) {
		t.Fatal("a finished thinking block must be kept in chat history")
	}
	if IsDurableChatEvent(thinking("Check", true)) {
		t.Fatal("a streamed thinking fragment must not be stored")
	}
	if IsDurableChatEvent(thinking("  ", false)) {
		t.Fatal("an empty thinking block must not be stored")
	}
	child := thinking("sub-agent reasoning", false)
	child.ExecutionKind = "sub_agent"
	if IsDurableChatEvent(child) {
		t.Fatal("a child execution's thinking stays out of the chat history")
	}
}

// Streamed thinking (how Pi sends it) is stored as one joined block just before the next stored event, in order, and the
// fragments themselves never reach the journal (PLAT-832).
func TestStreamedThinkingIsStoredJoinedBeforeTheNextStoredEvent(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	classifyInteractiveTestSession(t, store, "chat-1")
	fragment := func(id, text string) Event {
		return Event{ID: id, Type: "conversation_thinking", Timestamp: time.Now(),
			Data: agentevents.NewAgentEvent(&agentevents.ConversationThinkingEvent{Thinking: text, IsDelta: true})}
	}
	store.AddEvent("chat-1", journalTestEvent("q1", "question"))
	store.AddEvent("chat-1", fragment("t1", "Check"))
	store.AddEvent("chat-1", fragment("t2", "ing the "))
	store.AddEvent("chat-1", fragment("t3", "files"))
	store.AddEvent("chat-1", journalTestEvent("q2", "next message"))

	page, err := store.ReadDurableChatPage("chat-1", DurableEventPageOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	ids := journalEventIDs(page.Events)
	if len(ids) != 3 || ids[0] != "q1" || ids[1] != "t1:joined" || ids[2] != "q2" {
		t.Fatalf("durable page = %v, want [q1 t1:joined q2]", ids)
	}
	text, delta, _, ok := thinkingText(page.Events[1])
	if !ok || delta || text != "Checking the files" {
		t.Fatalf("joined block = %q (delta=%v, thinking event=%v), want \"Checking the files\" as one finished block", text, delta, ok)
	}
	if page.Events[1].Sequence <= page.Events[0].Sequence || page.Events[2].Sequence <= page.Events[1].Sequence {
		t.Fatalf("sequences out of order: %d %d %d", page.Events[0].Sequence, page.Events[1].Sequence, page.Events[2].Sequence)
	}
	// The live buffer still holds the fragments for connected chats; they are not repeated by the stored block.
	live := 0
	for _, event := range store.GetEvents("chat-1", GetEventsOptions{}).Events {
		if event.Type == "conversation_thinking" && strings.Contains(event.ID, ":joined") {
			t.Fatal("the joined block was published to live subscribers")
		}
		if event.Type == "conversation_thinking" {
			live++
		}
	}
	if live != 3 {
		t.Fatalf("live fragments = %d, want 3", live)
	}
}
