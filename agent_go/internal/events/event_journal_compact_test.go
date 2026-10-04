package events

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	agentevents "github.com/manishiitg/mcpagent/events"
)

func compactTestJournal(t *testing.T) *SQLiteEventJournal {
	t.Helper()
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { journal.Close() })
	return journal
}

func compactAppend(t *testing.T, journal *SQLiteEventJournal, session string, events ...Event) {
	t.Helper()
	for _, event := range events {
		if _, _, err := journal.Append(session, event); err != nil {
			t.Fatal(err)
		}
	}
}

func cUser(id string) Event { return journalTestEvent(id, "question "+id) }

func cAnswer(id string) Event {
	chunk := &agentevents.StreamingChunkEvent{Content: "answer " + id, Source: "transcript"}
	return Event{ID: id, Type: "streaming_chunk", Timestamp: time.Now(), Data: agentevents.NewAgentEvent(chunk)}
}

func cPlain(id, kind string) Event { return Event{ID: id, Type: kind, Timestamp: time.Now()} }

// cTurn: user, `tools` tool calls (start+end), answer, completion.
func cTurn(n, tools int) []Event {
	out := []Event{cUser(fmt.Sprintf("u%d", n))}
	for i := 0; i < tools; i++ {
		out = append(out, cPlain(fmt.Sprintf("t%d-%d-s", n, i), "tool_call_start"), cPlain(fmt.Sprintf("t%d-%d-e", n, i), "tool_call_end"))
	}
	return append(out, cAnswer(fmt.Sprintf("a%d", n)), cPlain(fmt.Sprintf("c%d", n), "unified_completion"))
}

func hasToolRows(events []Event) bool {
	for _, event := range events {
		if event.Type == "tool_call_start" || event.Type == "tool_call_end" || event.Type == "tool_call_error" {
			return true
		}
	}
	return false
}

func TestCompactPageKeepsLatestTurnWholeAndOlderAsMessages(t *testing.T) {
	journal := compactTestJournal(t)
	for turn := 1; turn <= 3; turn++ {
		compactAppend(t, journal, "s", cTurn(turn, 5)...)
	}
	page, err := journal.ReadCompactPage("s", CompactPageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"u1", "a1", "c1", "u2", "a2", "c2", "u3"}
	for i := 0; i < 5; i++ {
		want = append(want, fmt.Sprintf("t3-%d-s", i), fmt.Sprintf("t3-%d-e", i))
	}
	want = append(want, "a3", "c3")
	if got := journalEventIDs(page.Events); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if page.HasOlder || page.OldestSequence != 1 || page.LatestSequence != page.JournalLatestSequence {
		t.Fatalf("page=%+v", page)
	}
}

func TestCompactPageRunningTurnKeptWholeAndSteeringDoesNotSplitIt(t *testing.T) {
	journal := compactTestJournal(t)
	compactAppend(t, journal, "s", cTurn(1, 3)...)
	compactAppend(t, journal, "s", cUser("u2"), cPlain("t-a", "tool_call_start"), cPlain("t-b", "tool_call_end"),
		cUser("steer"), cPlain("t-c", "tool_call_start"))
	page, err := journal.ReadCompactPage("s", CompactPageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := journalEventIDs(page.Events)
	want := []string{"u1", "a1", "c1", "u2", "t-a", "t-b", "steer", "t-c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestCompactPagingOverFilteredViewNeverSplitsATurn(t *testing.T) {
	journal := compactTestJournal(t)
	for turn := 1; turn <= 30; turn++ {
		compactAppend(t, journal, "s", cTurn(turn, 4)...)
	}
	first, err := journal.ReadCompactPage("s", CompactPageOptions{Messages: 10})
	if err != nil {
		t.Fatal(err)
	}
	if first.Events[0].Type != "user_message" || !first.HasOlder {
		t.Fatalf("first page must start at a turn and have older: %+v", journalEventIDs(first.Events))
	}
	seen := map[string]bool{}
	for _, event := range first.Events {
		seen[event.ID] = true
	}
	page := first
	for guard := 0; page.HasOlder && guard < 20; guard++ {
		older, err := journal.ReadCompactPage("s", CompactPageOptions{BeforeSequence: page.OldestSequence, Messages: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(older.Events) == 0 {
			t.Fatal("has_older advertised an empty page")
		}
		if older.Events[0].Type != "user_message" {
			t.Fatalf("page starts mid-turn: %s", older.Events[0].ID)
		}
		if hasToolRows(older.Events) {
			t.Fatal("older page carries tool rows")
		}
		for _, event := range older.Events {
			if seen[event.ID] || event.Sequence >= page.OldestSequence {
				t.Fatalf("overlap at %s", event.ID)
			}
			seen[event.ID] = true
		}
		page = older
	}
	if page.HasOlder || page.Events[0].ID != "u1" {
		t.Fatalf("did not reach the start: %s hasOlder=%v", page.Events[0].ID, page.HasOlder)
	}
	for turn := 1; turn <= 30; turn++ {
		for _, id := range []string{fmt.Sprintf("u%d", turn), fmt.Sprintf("a%d", turn)} {
			if !seen[id] {
				t.Fatalf("missing %s", id)
			}
		}
	}
}

func TestCompactKeepsQuestionsApprovalsAndErrorsOfOlderTurns(t *testing.T) {
	journal := compactTestJournal(t)
	compactAppend(t, journal, "s", cUser("u1"), cPlain("q", "coding_agent_question"), cPlain("ap", "plan_approval"),
		cPlain("tc", "tool_call_start"), cPlain("r", "human_feedback_resolved"), cPlain("err", "agent_error"),
		cPlain("live", "live_input_confirmed"), cPlain("bg", "background_agent_started"))
	compactAppend(t, journal, "s", cTurn(2, 1)...)
	page, err := journal.ReadCompactPage("s", CompactPageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := journalEventIDs(page.Events)[:6]
	want := []string{"u1", "q", "ap", "r", "err", "u2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestCompactChatWithOnlyToolEventsAndEmptyChat(t *testing.T) {
	journal := compactTestJournal(t)
	empty, err := journal.ReadCompactPage("none", CompactPageOptions{})
	if err != nil || empty.Exists || len(empty.Events) != 0 || empty.HasOlder {
		t.Fatalf("empty: %+v %v", empty, err)
	}
	compactAppend(t, journal, "tools", cPlain("a", "tool_call_start"), cPlain("b", "tool_call_end"))
	page, err := journal.ReadCompactPage("tools", CompactPageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(journalEventIDs(page.Events), []string{"a", "b"}) || page.HasOlder {
		t.Fatalf("tools-only chat: %v", journalEventIDs(page.Events))
	}
}

func TestCompactVeryLongLatestTurnIsCappedAndPagesOlderMessagesOnly(t *testing.T) {
	journal := compactTestJournal(t)
	compactAppend(t, journal, "s", cTurn(1, 2)...)
	compactAppend(t, journal, "s", cUser("u2"))
	for i := 0; i < 300; i++ {
		compactAppend(t, journal, "s", cPlain(fmt.Sprintf("x%d-s", i), "tool_call_start"), cPlain(fmt.Sprintf("x%d-e", i), "tool_call_end"))
	}
	whole, err := journal.ReadCompactPage("s", CompactPageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(whole.Events) != 604 || whole.Events[3].ID != "u2" || whole.HasOlder || hasToolRows(whole.Events[:3]) {
		t.Fatalf("long turn must arrive whole: %d", len(whole.Events))
	}
	capped, err := journal.ReadCompactPage("s", CompactPageOptions{LatestTurnCap: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(capped.Events) != 104 || capped.HasOlder || capped.LatestSequence != capped.JournalLatestSequence {
		t.Fatalf("capped: %d hasOlder=%v", len(capped.Events), capped.HasOlder)
	}
	older, err := journal.ReadCompactPage("s", CompactPageOptions{BeforeSequence: capped.Events[3].Sequence})
	if err != nil {
		t.Fatal(err)
	}
	if hasToolRows(older.Events) || older.Events[len(older.Events)-1].ID != "c1" || older.HasOlder {
		t.Fatalf("older: %v hasOlder=%v", journalEventIDs(older.Events), older.HasOlder)
	}
}

func TestCompactPageDoesNotChangeDefaultReadPage(t *testing.T) {
	journal := compactTestJournal(t)
	for turn := 1; turn <= 2; turn++ {
		compactAppend(t, journal, "s", cTurn(turn, 3)...)
	}
	page, err := journal.ReadPage("s", DurableEventPageOptions{Limit: 300})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2*(1+6+2) || !hasToolRows(page.Events) || page.HasOlder {
		t.Fatalf("default page changed: %d", len(page.Events))
	}
}

func TestCompactDefaultWindowReportsHasOlderAcrossManyTurns(t *testing.T) {
	journal := compactTestJournal(t)
	for turn := 1; turn <= 60; turn++ {
		compactAppend(t, journal, "s", cTurn(turn, 2)...)
	}
	page, err := journal.ReadCompactPage("s", CompactPageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	messages := 0
	for _, event := range page.Events {
		if isCompactMessageType(event.Type) {
			messages++
		}
	}
	if messages < CompactMessageWindow || messages > CompactMessageWindow+6 || !page.HasOlder || page.Events[0].Type != "user_message" {
		t.Fatalf("messages=%d hasOlder=%v", messages, page.HasOlder)
	}
}
