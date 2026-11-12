package events

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	pkgevents "github.com/manishiitg/mcpagent/events"
)

// PLAT-746-928: a steer must not cut the current reply or move its completed
// carrier below the steer on history reload. The journal keeps both positions:
// append sequence for cursors, generation start sequence for presentation.
func TestGenerationOrderSurvivesJournalReloadAndRejectsForeignOwner(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	store := NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	classifyInteractiveTestSession(t, store, "chat")
	add := func(id, kind, owner string) Event {
		event := Event{ID: id, Type: kind, Timestamp: time.Now(), SessionID: "chat", TerminalOwnerID: owner, ExecutionID: "main:chat"}
		if kind == "streaming_start" {
			event.Data = pkgevents.NewAgentEvent(&pkgevents.StreamingStartEvent{})
		}
		if kind == "agent_end" {
			event.Data = pkgevents.NewAgentEvent(NewGenericEventData(kind, map[string]interface{}{"result": "Intact completed reply"}))
		}
		if err := store.AddEventChecked("chat", event); err != nil {
			t.Fatal(err)
		}
		rows := store.GetAllEventsRaw("chat")
		return rows[len(rows)-1]
	}
	start := add("start", "streaming_start", "main:chat")
	add("child-start", "streaming_start", "child:chat")
	steer := add("steer", "user_message", "main:chat")
	finished := add("finish", "agent_end", "main:chat")
	if finished.GenerationID != start.ID || finished.GenerationStartSequence != start.Sequence || finished.Sequence <= steer.Sequence {
		t.Fatalf("wrong generation positions: start=%+v steer=%+v finish=%+v", start, steer, finished)
	}
	child := add("child-finish", "agent_end", "child:chat")
	if child.GenerationID != "" {
		t.Fatal("child acquired main generation identity")
	}
	page, err := journal.ReadPage("chat", DurableEventPageOptions{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var restored Event
	for _, row := range page.Events {
		if row.ID == finished.ID {
			restored = row
		}
	}
	encoded, err := json.Marshal(restored)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Event
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.GenerationID != start.ID || decoded.GenerationStartSequence != start.Sequence {
		t.Fatalf("history lost generation position: %s", encoded)
	}
	next := add("next-start", "streaming_start", "main:chat")
	nextFinish := add("next-finish", "agent_end", "main:chat")
	if nextFinish.GenerationID != next.ID || nextFinish.GenerationStartSequence <= steer.Sequence {
		t.Fatal("next generation reused the preceding row")
	}
}
