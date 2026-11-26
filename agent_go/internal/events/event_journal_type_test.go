package events

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSQLiteEventJournalReadEventsByTypeUsesPartialIndex(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatalf("OpenSQLiteEventJournal() error = %v", err)
	}
	defer journal.Close()

	store := NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	store.SetSessionOwner("chat-1", "user-1")
	if err := store.SetSessionPersistenceClass("chat-1", SessionPersistenceInteractiveChat); err != nil {
		t.Fatalf("SetSessionPersistenceClass() error = %v", err)
	}

	now := time.Now().UTC()
	for i := 0; i < 20; i++ {
		store.AddEvent("chat-1", Event{
			ID:        "noise-" + string(rune('a'+i)),
			Type:      "user_message",
			Timestamp: now.Add(time.Duration(i) * time.Millisecond),
			SessionID: "chat-1",
		})
	}
	store.AddEvent("chat-1", Event{
		ID:        "q-1",
		Type:      "coding_agent_question",
		Timestamp: now.Add(100 * time.Millisecond),
		SessionID: "chat-1",
	})
	store.AddEvent("chat-1", Event{
		ID:        "q-2",
		Type:      "coding_agent_question",
		Timestamp: now.Add(200 * time.Millisecond),
		SessionID: "chat-1",
	})
	// Events from another session must never leak into chat-1.
	store.SetSessionOwner("chat-2", "user-1")
	if err := store.SetSessionPersistenceClass("chat-2", SessionPersistenceInteractiveChat); err != nil {
		t.Fatalf("SetSessionPersistenceClass(chat-2) error = %v", err)
	}
	store.AddEvent("chat-2", Event{
		ID:        "q-other",
		Type:      "coding_agent_question",
		Timestamp: now.Add(300 * time.Millisecond),
		SessionID: "chat-2",
	})

	// Verify SQLite chooses the partial index for coding_agent_question lookups.
	rows, err := journal.reader().Query(
		`EXPLAIN QUERY PLAN SELECT payload FROM structured_chat_events
		 WHERE session_id = ? AND json_extract(payload,'$.type')='coding_agent_question'
		 ORDER BY sequence ASC`,
		"chat-1",
	)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
	}
	defer rows.Close()
	usedPartialIndex := false
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan error = %v", err)
		}
		if strings.Contains(detail, "structured_chat_coding_agent_question") {
			usedPartialIndex = true
		}
	}
	if !usedPartialIndex {
		t.Fatalf("expected EXPLAIN QUERY PLAN to use structured_chat_coding_agent_question partial index")
	}

	got, supported, err := store.ReadDurableChatEventsByType("chat-1", "coding_agent_question")
	if err != nil {
		t.Fatalf("ReadDurableChatEventsByType() error = %v", err)
	}
	if !supported {
		t.Fatalf("expected SQLiteEventJournal to support ReadDurableChatEventsByType")
	}
	if len(got) != 2 || got[0].ID != "q-1" || got[1].ID != "q-2" {
		t.Fatalf("ReadDurableChatEventsByType(chat-1) = %+v, want [q-1, q-2]", got)
	}
	if got[0].Sequence >= got[1].Sequence {
		t.Fatalf("expected ascending sequence order, got %d >= %d", got[0].Sequence, got[1].Sequence)
	}
}
