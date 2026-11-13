package server

import (
	"encoding/json"
	"path/filepath"
	"testing"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

// PLAT-746: copying model context must also populate the screen's own durable
// journal, without importing source tools, system prompts or execution handles.
func TestForkSeedsIndependentDialogueAndSourceMarker(t *testing.T) {
	journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	store := storeevents.NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	api := &StreamingAPI{eventStore: store}
	raw := []byte(`{"session_id":"source-session","conversation_history":[
		{"Role":"system","Parts":[{"Text":"private runtime prompt"}]},
		{"Role":"human","Parts":[{"Text":"Question"}]},
		{"Role":"tool","Parts":[{"Text":"source tool handle"}]},
		{"Role":"ai","Parts":[{"Text":"Answer"}]}
	]}`)
	dest := ProductConversationRecord{SessionID: "fork-session"}
	if err := api.seedForkChatJournal("owner", dest, "project:chat:source", "Design", raw); err != nil {
		t.Fatal(err)
	}
	owner, err := store.DurableChatOwner(dest.SessionID)
	if err != nil || owner != "owner" {
		t.Fatalf("destination ownership: %q %v", owner, err)
	}
	check := func(rows []storeevents.Event) {
		t.Helper()
		if len(rows) != 3 {
			t.Fatalf("want copied question, answer, then source marker; got %d rows", len(rows))
		}
		wantTypes := []string{"user_message", "unified_completion", "chat_forked"}
		for i, row := range rows {
			if row.Type != wantTypes[i] || row.SessionID != dest.SessionID {
				t.Fatalf("copied source identity or wrong dialogue: %+v", row)
			}
			if i > 0 && rows[i-1].Sequence >= row.Sequence {
				t.Fatal("fork marker must follow the copied dialogue")
			}
		}
		if eventPayloadMap(rows[0])["content"] != "Question" || eventPayloadMap(rows[1])["final_result"] != "Answer" {
			t.Fatal("copied dialogue was missing or changed")
		}
		marker := eventPayloadMap(rows[2])
		if marker["source_name"] != "Design" || marker["source_project_id"] != "project" || marker["source_chat_id"] != "source" {
			t.Fatalf("wrong source marker: %+v", marker)
		}
		encoded, _ := json.Marshal(rows)
		var restored []storeevents.Event
		if err := json.Unmarshal(encoded, &restored); err != nil || len(restored) != 3 {
			t.Fatal("fork history did not serialize")
		}
	}
	check(store.GetAllEventsRaw(dest.SessionID))
	page, err := journal.ReadPage(dest.SessionID, storeevents.DurableEventPageOptions{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	check(page.Events)
	if source, err := journal.ReadPage("source-session", storeevents.DurableEventPageOptions{Limit: 100}); err != nil || len(source.Events) != 0 {
		t.Fatal("fork wrote source chat")
	}
}
