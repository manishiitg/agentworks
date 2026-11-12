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

// PLAT-746-928 follow-up: a native block in flight at delivery is never cut.
// Its completed row stays above the steer, a later block stays below on reload.
func TestNativeBlocksStayWholeAcrossDeliveredSteerAndReload(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	store := NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	classifyInteractiveTestSession(t, store, "native-chat")
	appendEvent := func(id, kind, block, phase, text string) Event {
		metadata := map[string]interface{}{"assistant_block_id": block, "assistant_block_phase": phase}
		var data pkgevents.EventData
		switch kind {
		case "streaming_start":
			data = &pkgevents.StreamingStartEvent{BaseEventData: pkgevents.BaseEventData{Metadata: metadata}}
		case "streaming_chunk":
			source := "content"
			if phase == "end" {
				source = "transcript"
			}
			data = &pkgevents.StreamingChunkEvent{BaseEventData: pkgevents.BaseEventData{Metadata: metadata}, Content: text, Source: source, IsDelta: phase != "end"}
		case "user_message":
			user := pkgevents.NewUserMessageEvent(0, "Change direction", "user")
			user.Metadata = map[string]interface{}{"delivery_status": "sent_to_cli"}
			data = user
		case "unified_completion":
			data = pkgevents.NewUnifiedCompletionEvent("coding_agent", "simple", "", "before finishes\nafter steer", "completed", time.Second, 1)
		}
		event := Event{ID: id, Type: kind, Timestamp: time.Now(), SessionID: "native-chat", ExecutionID: "main:native-chat", TerminalOwnerID: "main:native-chat", Data: pkgevents.NewAgentEvent(data)}
		if err := store.AddEventChecked("native-chat", event); err != nil {
			t.Fatal(err)
		}
		rows := store.GetAllEventsRaw("native-chat")
		return rows[len(rows)-1]
	}
	appendEvent("generation", "streaming_start", "", "", "")
	a := appendEvent("a-start", "streaming_start", "a", "start", "")
	appendEvent("a-delta1", "streaming_chunk", "a", "", "before ")
	steer := appendEvent("steer", "user_message", "", "", "")
	appendEvent("a-delta2", "streaming_chunk", "a", "", "finishes")
	aEnd := appendEvent("a-end", "streaming_chunk", "a", "end", "before finishes")
	b := appendEvent("b-start", "streaming_start", "b", "start", "")
	appendEvent("b-delta", "streaming_chunk", "b", "", "after steer")
	bEnd := appendEvent("b-end", "streaming_chunk", "b", "end", "after steer")
	end := appendEvent("done", "unified_completion", "", "", "")
	if aEnd.GenerationStartSequence != a.Sequence || aEnd.GenerationStartSequence >= steer.Sequence || bEnd.GenerationStartSequence != b.Sequence || bEnd.GenerationStartSequence <= steer.Sequence || !end.ReplyBlocksComplete {
		t.Fatalf("wrong live positions: a=%+v steer=%+v b=%+v done=%+v", aEnd, steer, bEnd, end)
	}
	page, err := journal.ReadPage("native-chat", DurableEventPageOptions{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]Event{}
	for _, row := range page.Events {
		raw, _ := json.Marshal(row)
		var restored Event
		if err := json.Unmarshal(raw, &restored); err != nil {
			t.Fatal(err)
		}
		rows[row.ID] = restored
	}
	restoredA := rows["a-end"]
	if rows["a-end"].GenerationStartSequence != a.Sequence || rows["b-end"].GenerationStartSequence != b.Sequence || !rows["done"].ReplyBlocksComplete || eventPayloadMap(&restoredA)["content"] != "before finishes" {
		t.Fatal("reload lost a whole native block or its server position")
	}
	// A malformed/mixed transport may emit content without identity after known
	// blocks. Render only that unframed remainder, not the aggregate twice.
	appendEvent("mixed-delta", "streaming_chunk", "", "", "unframed remainder")
	mixed := appendEvent("mixed-done", "unified_completion", "", "", "")
	if mixed.ReplyBlocksComplete || mixed.ReplyRemainder == nil || *mixed.ReplyRemainder != "unframed remainder" {
		t.Fatal("mixed content repeats native blocks or disappears")
	}
	// Unknown transports remain whole-generation, with no invented framing.
	legacy := appendEvent("legacy-start", "streaming_start", "", "", "")
	appendEvent("legacy-chunk", "streaming_chunk", "", "", "legacy reply")
	legacyEnd := appendEvent("legacy-end", "unified_completion", "", "", "")
	if legacyEnd.GenerationStartSequence != legacy.Sequence || legacyEnd.ReplyBlocksComplete {
		t.Fatal("unframed transport opted into native block mode")
	}
}
