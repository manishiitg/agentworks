package server

import (
	"context"
	"encoding/json"
	"github.com/gorilla/mux"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
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
	// Reproduce opening the newly registered destination before seed completes.
	store.SetSessionOwner(dest.SessionID, "owner")
	if err := store.SetSessionPersistenceClass(dest.SessionID, storeevents.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	if rows := store.GetAllEventsRaw(dest.SessionID); len(rows) != 0 {
		t.Fatal("new fork must start empty")
	}
	// The SSE pane has already loaded the empty destination, just as in the
	// reported fork. No copied row may be emitted as a fresh turn activity.
	api.runtimeCoordinator = NewRuntimeCoordinator()
	api.activeSessions = map[string]*ActiveSessionInfo{dest.SessionID: {SessionID: dest.SessionID, Status: "completed", CreatedAt: time.Now()}}
	req := httptest.NewRequest("GET", "/api/sessions/"+dest.SessionID+"/events/stream?durable_chat=1&since=0", nil)
	req = mux.SetURLVars(req, map[string]string{"session_id": dest.SessionID})
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { api.handleSSEStream(w, req.WithContext(ctx)); close(done) }()
	time.Sleep(50 * time.Millisecond)
	var parsed struct {
		History []llmtypes.MessageContent `json:"conversation_history"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if err := api.seedForkChatJournal("owner", dest, "project:chat:source", "Design", parsed.History); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	body := w.Body.String()
	for _, id := range []string{"builder-history-fork-session-1", "builder-history-fork-session-3", "fork:fork-session"} {
		if strings.Count(body, `"id":"`+id+`"`) != 1 {
			t.Fatalf("early subscriber missing/duplicated fork history row %s", id)
		}
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
