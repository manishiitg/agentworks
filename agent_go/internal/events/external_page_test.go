package events

import (
	"fmt"
	"strings"
	"testing"

	pkgevents "github.com/manishiitg/mcpagent/events"
)

func TestForwardEventPageBoundsInitialPageAndMaintainsFilteredCursor(t *testing.T) {
	store := NewEventStore(100)
	defer store.Stop()
	store.InitializeSession("s", 0)
	store.events["s"] = []Event{
		{ID: "a", Type: "user_message"},
		{ID: "hidden", Type: "streaming_chunk"},
		{ID: "b", Type: "conversation_end"},
		{ID: "hidden2", Type: "streaming_chunk"},
		{ID: "c", Type: "user_message"},
	}
	first := store.GetForwardEventPage("s", -1, 2)
	if len(first.Events) != 2 || first.Events[0].ID != "a" || first.Events[1].ID != "b" || !first.HasMore || first.LastProcessedIndex != 3 {
		t.Fatalf("wrong first page: %+v", first)
	}
	next := store.GetForwardEventPage("s", first.LastProcessedIndex, 2)
	if len(next.Events) != 1 || next.Events[0].ID != "c" || next.HasMore || next.LastProcessedIndex != 4 {
		t.Fatalf("wrong continuation: %+v", next)
	}
	end := store.GetForwardEventPage("s", next.LastProcessedIndex, 2)
	if len(end.Events) != 0 || end.CursorReset || end.LastProcessedIndex != 4 {
		t.Fatalf("wrong terminal page: %+v", end)
	}
}

func TestForwardEventPageExplicitCursorReset(t *testing.T) {
	store := NewEventStore(100)
	defer store.Stop()
	store.InitializeSession("s", 20)
	store.events["s"] = []Event{{ID: "a", Type: "user_message"}, {ID: "b", Type: "user_message"}}
	old := store.GetForwardEventPage("s", 4, 1)
	if !old.CursorReset || old.FirstAvailableIndex != 20 || old.LastProcessedIndex != 20 || len(old.Events) != 1 || !old.HasMore {
		t.Fatalf("old cursor page: %+v", old)
	}
	future := store.GetForwardEventPage("s", 99, 1)
	if !future.CursorReset || len(future.Events) != 0 || future.LastProcessedIndex != 21 {
		t.Fatalf("future cursor page: %+v", future)
	}
	missing := store.GetForwardEventPage("missing", 99, 1)
	if missing.Exists || !missing.CursorReset || len(missing.Events) != 0 || missing.LastProcessedIndex != -1 {
		t.Fatalf("missing session page: %+v", missing)
	}
}

func TestForwardEventPageCapsStructuralEvents(t *testing.T) {
	store := NewEventStore(500)
	defer store.Stop()
	store.InitializeSession("s", 0)
	for i := 0; i < 300; i++ {
		store.events["s"] = append(store.events["s"], Event{ID: fmt.Sprint(i), Type: "user_message"})
	}
	page := store.GetForwardEventPage("s", 0, 5000)
	if len(page.Events) != 200 || page.Events[0].ID != "1" || !page.HasMore || page.LastProcessedIndex != 200 {
		t.Fatalf("page not bounded: %+v", page)
	}
}

// MCP feedback 2026-10-08: run_status pages were 400-600 KB because every event
// carries its full tool output and the limit counted events. A page must stop
// at its size budget and continue from the last event it returned.
func TestForwardEventPageStopsAtItsSizeBudgetAndContinues(t *testing.T) {
	store := NewEventStore(100)
	defer store.Stop()
	store.InitializeSession("s", 0)
	big := strings.Repeat("x", 3000)
	rows := []Event{}
	for i := 0; i < 6; i++ {
		rows = append(rows, Event{ID: fmt.Sprint(i), Type: "user_message", Data: &pkgevents.AgentEvent{Data: &pkgevents.UserMessageEvent{Content: big}}})
	}
	store.events["s"] = rows
	first := store.GetForwardEventPageBudget("s", -1, 50, 7000)
	if len(first.Events) != 2 || !first.HasMore || first.LastProcessedIndex != 1 {
		t.Fatalf("first page = %d events, more=%v, last=%d; want 2 events within the budget, more to come, last index 1", len(first.Events), first.HasMore, first.LastProcessedIndex)
	}
	rest := store.GetForwardEventPageBudget("s", first.LastProcessedIndex, 50, 0)
	if len(rest.Events) != 4 || rest.Events[0].ID != "2" {
		t.Fatalf("continuing returned %d events starting at %q, want the remaining 4 from id 2", len(rest.Events), rest.Events[0].ID)
	}
}

func TestLatestAnswerPrefersTheMainReplyAndIgnoresOlderTurns(t *testing.T) {
	store := NewEventStore(100)
	defer store.Stop()
	store.InitializeSession("s", 0)
	reply := func(typ, text string) Event {
		return Event{Type: typ, Data: &pkgevents.AgentEvent{Data: &pkgevents.UserMessageEvent{Content: text}}}
	}
	store.events["s"] = []Event{reply("unified_completion", "first answer")}
	after := store.LastIndex("s")
	if _, ok := store.LatestAnswer("s", after); ok {
		t.Fatal("an answer from before the turn started was returned")
	}
	store.events["s"] = append(store.events["s"], reply("llm_generation_end", "second answer"), reply("background_agent_completed", "a background agent finished"))
	if got, _ := store.LatestAnswer("s", after); got != "second answer" {
		t.Fatalf("LatestAnswer = %q, want the main reply, not the background agent's", got)
	}
}
