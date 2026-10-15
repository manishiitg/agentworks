package events

import (
	"strings"
	"testing"
	"time"

	mcpevents "github.com/manishiitg/mcpagent/events"
)

func snapshot(terminal, id, content string, replace bool) Event {
	chunk := &mcpevents.StreamingChunkEvent{
		BaseEventData: mcpevents.BaseEventData{Metadata: map[string]interface{}{"kind": "terminal", "replace": replace}},
		Content:       content,
		Source:        mcpevents.StreamingChunkSourceTerminal,
	}
	return Event{ID: id, Type: "streaming_chunk", Timestamp: time.Now(), TerminalID: terminal, Data: mcpevents.NewAgentEvent(chunk)}
}

// 2026-10-16: a long-running server held GBs, mostly terminal-screen snapshots, each of which REPLACES the one before it.
// Only the latest snapshot of a terminal is kept; appended chunks and other terminals' snapshots are untouched.
func TestReplacingTerminalSnapshotsKeepOnlyTheLatestPerTerminal(t *testing.T) {
	es := NewEventStore(100)
	defer es.Stop()
	const session = "snap-session"
	big := strings.Repeat("x", 20<<10)
	es.AddEvent(session, snapshot("t1", "a", big, true))
	es.AddEvent(session, snapshot("t2", "b", big, true))
	es.AddEvent(session, snapshot("t1", "c", big, true))
	es.AddEvent(session, snapshot("t1", "appended", "text", false))
	es.AddEvent(session, snapshot("t1", "d", big, true))

	var ids []string
	for _, event := range es.GetAllEventsRaw(session) {
		ids = append(ids, event.ID)
	}
	if strings.Join(ids, ",") != "b,appended,d" {
		t.Fatalf("kept events = %v, want [b appended d] (latest snapshot per terminal plus the appended chunk)", ids)
	}
	stats := es.Stats(5)
	if stats.Bytes <= 0 || stats.Bytes > 80<<10 {
		t.Fatalf("estimated bytes = %d, want about two snapshots", stats.Bytes)
	}
}

// A session cannot hold more than its byte budget, whatever its event count; the newest event always stays.
func TestSessionStaysInsideItsByteBudget(t *testing.T) {
	es := NewEventStore(1000)
	defer es.Stop()
	es.byteBudget = 300 << 10
	const session = "budget-session"
	huge := strings.Repeat("r", 100<<10)
	for i := 0; i < 10; i++ {
		es.AddEvent(session, Event{ID: string(rune('a' + i)), Type: "tool_call_end", Timestamp: time.Now(),
			Data: mcpevents.NewAgentEvent(&mcpevents.ToolCallEndEvent{Result: huge})})
	}
	stats := es.Stats(5)
	if stats.Bytes > es.byteBudget+(110<<10) {
		t.Fatalf("session holds %d bytes, budget %d", stats.Bytes, es.byteBudget)
	}
	events := es.GetAllEventsRaw(session)
	if len(events) == 0 || events[len(events)-1].ID != "j" {
		t.Fatalf("the newest event was dropped: %d events", len(events))
	}
}

// Earlier turns are pruned before the turn a live screen is still drawing.
func TestByteBudgetKeepsTheCurrentTurnAndPrunesEarlierTurnsFirst(t *testing.T) {
	es := NewEventStore(1000)
	defer es.Stop()
	es.byteBudget = 250 << 10
	const session = "turns-session"
	huge := strings.Repeat("r", 100<<10)
	add := func(id, typ string, data mcpevents.EventData) {
		es.AddEvent(session, Event{ID: id, Type: typ, Timestamp: time.Now(), Data: mcpevents.NewAgentEvent(data)})
	}
	add("old1", "tool_call_end", &mcpevents.ToolCallEndEvent{Result: huge})
	add("old2", "tool_call_end", &mcpevents.ToolCallEndEvent{Result: huge})
	add("user", "user_message", &mcpevents.UserMessageEvent{Content: "now"})
	add("cur1", "tool_call_end", &mcpevents.ToolCallEndEvent{Result: huge})
	add("cur2", "tool_call_end", &mcpevents.ToolCallEndEvent{Result: huge})
	var ids []string
	for _, event := range es.GetAllEventsRaw(session) {
		ids = append(ids, event.ID)
	}
	got := strings.Join(ids, ",")
	if !strings.Contains(got, "user") || !strings.Contains(got, "cur1") || !strings.Contains(got, "cur2") || strings.Contains(got, "old1") {
		t.Fatalf("kept %v: the current turn must stay and the earlier turn go first", ids)
	}
}
