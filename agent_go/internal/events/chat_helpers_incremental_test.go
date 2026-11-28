package events

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #4576: ChatHelperEventsRaw scans incrementally when maxSeq advances, keeps
// oldest-first order, trims to limit, and falls back to a full scan if maxSeq
// decreases (e.g. after row deletion).
func TestChatHelperEventsRawIncrementalScan(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "helpers-inc.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	store := NewEventStore(5)
	defer store.Stop()
	store.SetDurableJournal(journal)
	store.SetSessionOwner("chat-inc", "owner")
	if err = store.SetSessionPersistenceClass("chat-inc", SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}

	add := func(event Event) {
		t.Helper()
		event.Timestamp = time.Now()
		if err := store.AddEventChecked("chat-inc", event); err != nil {
			t.Fatal(err)
		}
	}
	helper := func(id string) Event {
		record := llmtypes.NativeRecord{ID: id, Provider: "claude-code", Kind: "helper", Timestamp: time.Now()}
		return Event{ID: id, Type: string(agentevents.CodingAgentNativeActivity), Data: agentevents.NewAgentEvent(agentevents.NewCodingAgentNativeActivity(record))}
	}
	readIDs := func(limit int) ([]string, int64) {
		t.Helper()
		raw, seq, err := journal.ChatHelperEventsRaw(context.Background(), "chat-inc", limit)
		if err != nil {
			t.Fatal(err)
		}
		var evs []Event
		if err := json.Unmarshal(raw, &evs); err != nil {
			t.Fatalf("invalid json %s: %v", string(raw), err)
		}
		ids := make([]string, 0, len(evs))
		for _, e := range evs {
			ids = append(ids, e.ID)
		}
		return ids, seq
	}

	// 1. Cold read populates cache.
	add(helper("h-1"))
	add(journalTestEvent("u-1", "user_message"))
	add(helper("h-2"))
	ids, seq1 := readIDs(3)
	if !reflect.DeepEqual(ids, []string{"h-1", "h-2"}) {
		t.Fatalf("cold ids = %v, want [h-1 h-2]", ids)
	}

	// 2. Non-helper event advances maxSeq without changing helper list.
	add(journalTestEvent("u-2", "user_message"))
	ids, seq2 := readIDs(3)
	if seq2 <= seq1 {
		t.Fatalf("expected seq2 (%d) > seq1 (%d)", seq2, seq1)
	}
	if !reflect.DeepEqual(ids, []string{"h-1", "h-2"}) {
		t.Fatalf("after non-helper ids = %v, want [h-1 h-2]", ids)
	}

	// 3. New helper events append incrementally and trim to limit=3.
	add(helper("h-3"))
	add(helper("h-4"))
	ids, seq3 := readIDs(3)
	if seq3 <= seq2 {
		t.Fatalf("expected seq3 (%d) > seq2 (%d)", seq3, seq2)
	}
	if !reflect.DeepEqual(ids, []string{"h-2", "h-3", "h-4"}) {
		t.Fatalf("after trim ids = %v, want [h-2 h-3 h-4]", ids)
	}

	// 4. Sequence reset (delete newer rows) falls back to full scan.
	if _, err := journal.db.Exec(`DELETE FROM structured_chat_events WHERE session_id=? AND sequence>?`, "chat-inc", seq1); err != nil {
		t.Fatal(err)
	}
	ids, seq4 := readIDs(3)
	if seq4 != seq1 {
		t.Fatalf("after delete seq4 = %d, want %d", seq4, seq1)
	}
	if !reflect.DeepEqual(ids, []string{"h-1", "h-2"}) {
		t.Fatalf("after delete fallback ids = %v, want [h-1 h-2]", ids)
	}
}
