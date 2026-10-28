package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"

	"github.com/gorilla/mux"
	pkgevents "github.com/manishiitg/mcpagent/events"
)

func TestWriteSSEEventFormatsCorrectly(t *testing.T) {
	w := httptest.NewRecorder()

	msg := sseEventMessage{
		Events: []events.Event{
			{
				ID:        "evt-1",
				Type:      "user_message",
				Timestamp: time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC),
				SessionID: "sess-1",
			},
		},
		SessionStatus:      "running",
		LastProcessedIndex: 42,
	}

	err := writeSSEEvent(w, "event", 42, msg)
	if err != nil {
		t.Fatalf("writeSSEEvent error: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "id: 42\n") {
		t.Fatalf("missing id line in SSE output:\n%s", body)
	}
	if !strings.Contains(body, "event: event\n") {
		t.Fatalf("missing event line in SSE output:\n%s", body)
	}
	if !strings.Contains(body, "data: ") {
		t.Fatalf("missing data line in SSE output:\n%s", body)
	}
	if !strings.HasSuffix(body, "\n\n") {
		t.Fatalf("SSE message should end with double newline:\n%q", body)
	}

	// Verify data is valid JSON
	dataLine := ""
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimPrefix(line, "data: ")
		}
	}
	if dataLine == "" {
		t.Fatal("no data line found")
	}
	var parsed sseEventMessage
	if err := json.Unmarshal([]byte(dataLine), &parsed); err != nil {
		t.Fatalf("data line not valid JSON: %v\ndata: %s", err, dataLine)
	}
	if parsed.SessionStatus != "running" {
		t.Fatalf("parsed SessionStatus = %q, want running", parsed.SessionStatus)
	}
	if parsed.LastProcessedIndex != 42 {
		t.Fatalf("parsed LastProcessedIndex = %d, want 42", parsed.LastProcessedIndex)
	}
	if len(parsed.Events) != 1 {
		t.Fatalf("parsed events count = %d, want 1", len(parsed.Events))
	}
}

func TestWriteSSEEventNoIDWhenNegative(t *testing.T) {
	w := httptest.NewRecorder()

	msg := sseStatusMessage{SessionStatus: "idle"}
	err := writeSSEEvent(w, "status", -1, msg)
	if err != nil {
		t.Fatalf("writeSSEEvent error: %v", err)
	}

	body := w.Body.String()
	if strings.Contains(body, "id:") {
		t.Fatalf("negative id should not produce id line:\n%s", body)
	}
	if !strings.Contains(body, "event: status\n") {
		t.Fatalf("missing event line:\n%s", body)
	}
}

func TestWriteSSEEventRecoversPanic(t *testing.T) {
	w := httptest.NewRecorder()

	// A type that panics during JSON marshal (concurrent map read)
	// Simulate by using a channel which json.Marshal can't handle
	type unserializable struct {
		Ch chan int
	}

	err := writeSSEEvent(w, "event", 1, unserializable{Ch: make(chan int)})
	if err == nil {
		t.Fatal("expected error for unserializable data")
	}
}

func TestSSEEventMessageStructure(t *testing.T) {
	now := time.Now()
	msg := sseEventMessage{
		Events: []events.Event{
			{
				ID:        "evt-error-1",
				Type:      "conversation_error",
				Timestamp: now,
				SessionID: "sess-1",
				Data: &pkgevents.AgentEvent{
					Type:      pkgevents.EventType("conversation_error"),
					Timestamp: now,
					Data: &pkgevents.GenericEventData{
						Data: map[string]interface{}{
							"error": "CLI binary not found",
						},
					},
				},
			},
		},
		SessionStatus:      "error",
		LastProcessedIndex: 10,
	}

	jsonBytes, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if parsed["session_status"] != "error" {
		t.Fatalf("session_status = %v, want error", parsed["session_status"])
	}
	evts, ok := parsed["events"].([]interface{})
	if !ok || len(evts) != 1 {
		t.Fatalf("events not properly serialized: %v", parsed["events"])
	}
}

// TestSSEStreamSendsResyncSignalWhenEventStoreHasNoRecordOfSession covers a
// reconnect for a session the in-memory EventStore has never seen -- the
// exact shape of a restart wiping the store while a browser tab was still
// open with a stale, large since= cursor from before the restart. Before the
// fix, the empty-backfill branch sent nothing at all: no correction, and the
// client never learns its cursor is stale. Confirmed live on the Dominion
// deployment 2026-08-31: a chat turn completed and was persisted correctly,
// but never appeared in an already-open tab until a manual page refresh.
func TestSSEStreamSendsResyncSignalWhenEventStoreHasNoRecordOfSession(t *testing.T) {
	api := &StreamingAPI{eventStore: events.NewEventStore(100)}
	sessionID := "sess-wiped-by-restart"

	req := httptest.NewRequest("GET", "/api/sessions/"+sessionID+"/events/stream?since=500", nil)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	ctx, cancel := context.WithTimeout(req.Context(), 150*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	api.handleSSEStream(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `"last_processed_index":-1`) {
		t.Fatalf("expected an explicit last_processed_index:-1 resync signal when the event store has no record of the session, got:\n%s", body)
	}
	if !strings.Contains(body, "event: event\n") {
		t.Fatalf("the resync signal must be an 'event' SSE frame (the frontend's real event handler), not only a 'status' tick, got:\n%s", body)
	}
}

// TestSSEStreamDeliversNewEventsAfterResyncSignal covers the second half of
// the same bug: even if a resync signal were sent, the handler's own
// `lastIndex` tracking must also be reset, or every subsequent genuinely-new
// event -- freshly numbered from a low index in the recreated store -- keeps
// getting silently discarded by the "already covered by backfill" guard,
// forever, for the life of the connection. Without resetting sinceIndex in
// the fix, this test fails because the post-restart event (index 1) never
// clears the stale since=500 cursor.
func TestSSEStreamDeliversNewEventsAfterResyncSignal(t *testing.T) {
	api := &StreamingAPI{eventStore: events.NewEventStore(100)}
	sessionID := "sess-wiped-by-restart-2"

	req := httptest.NewRequest("GET", "/api/sessions/"+sessionID+"/events/stream?since=500", nil)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		api.handleSSEStream(w, req)
		close(done)
	}()

	// Let the handler subscribe and send its (empty) backfill/resync signal
	// before publishing a "new" post-restart event.
	time.Sleep(50 * time.Millisecond)

	api.eventStore.AddEvent(sessionID, events.Event{
		ID:        "evt-post-restart-1",
		Type:      "user_message",
		Timestamp: time.Now(),
		SessionID: sessionID,
		Data:      &pkgevents.AgentEvent{EventIndex: 1},
	})

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := w.Body.String()
	if !strings.Contains(body, "evt-post-restart-1") {
		t.Fatalf("a genuinely new event published after the resync signal must reach the client, not be silently dropped as already-seen:\n%s", body)
	}
}

func TestDurableSSEBackfillStreamsEveryPageWithoutSkipping(t *testing.T) {
	journal, err := events.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := events.NewEventStore(2)
	defer store.Stop()
	store.SetDurableJournal(journal)
	const sessionID = "paged-chat"
	if err := store.SetSessionPersistenceClass(sessionID, events.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= events.MaxPollingLimit+5; i++ {
		if _, _, err := journal.Append(sessionID, events.Event{
			ID: fmt.Sprintf("message-%d", i), Type: "user_message", Timestamp: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	api := &StreamingAPI{
		eventStore: store, runtimeCoordinator: NewRuntimeCoordinator(),
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "completed", CreatedAt: time.Now()},
		},
	}
	req := httptest.NewRequest("GET", "/api/sessions/"+sessionID+"/events/stream?durable_chat=1&since=0", nil)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	ctx, cancel := context.WithTimeout(req.Context(), 100*time.Millisecond)
	defer cancel()
	w := httptest.NewRecorder()
	api.handleSSEStream(w, req.WithContext(ctx))
	body := w.Body.String()
	if !strings.Contains(body, `"id":"message-1"`) || !strings.Contains(body, `"id":"message-1005"`) {
		t.Fatalf("durable backfill skipped the first or last page: first=%t last=%t", strings.Contains(body, `"id":"message-1"`), strings.Contains(body, `"id":"message-1005"`))
	}
	if !strings.Contains(body, "id: 1000\n") || !strings.Contains(body, "id: 1005\n") {
		t.Fatal("durable backfill did not advance one page at a time")
	}
}

func TestDurableSSELiveOnlyUpdatesDoNotAdvanceJournalCursor(t *testing.T) {
	journal, err := events.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := events.NewEventStore(10)
	defer store.Stop()
	store.SetDurableJournal(journal)
	const sessionID = "live-chat"
	if err := store.SetSessionPersistenceClass(sessionID, events.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{
		eventStore: store, runtimeCoordinator: NewRuntimeCoordinator(),
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "running", CreatedAt: time.Now()},
		},
	}
	req := httptest.NewRequest("GET", "/api/sessions/"+sessionID+"/events/stream?durable_chat=1&since=0", nil)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	ctx, cancel := context.WithCancel(req.Context())
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		api.handleSSEStream(w, req.WithContext(ctx))
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	store.AddEvent(sessionID, events.Event{
		ID: "live-chunk", Type: "streaming_chunk", Timestamp: time.Now(),
		Data: pkgevents.NewAgentEvent(&pkgevents.StreamingChunkEvent{Content: "working", Source: "llm"}),
	})
	store.AddEvent(sessionID, events.Event{ID: "durable-message", Type: "user_message", Timestamp: time.Now()})
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	body := w.Body.String()
	if !strings.Contains(body, `"id":"live-chunk"`) || !strings.Contains(body, `"id":"durable-message"`) {
		t.Fatalf("stream omitted live or durable event: %s", body)
	}
	if strings.Count(body, "id: ") != 1 || !strings.Contains(body, "id: 2\n") {
		t.Fatalf("live-only chunk advanced the journal cursor: %s", body)
	}
}

// A retained turn's completion callback appends reply_file_changes while the
// completion itself is still being published, so the newer event reaches the
// stream first. The older completion must still be sent; a test server
// (2026-10-28) lost it and the Crew chat spinner ran until the tab was reopened
// (PLAT-746-158).
func TestDurableSSEDeliversCompletionPublishedAfterANewerEvent(t *testing.T) {
	journal, err := events.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := events.NewEventStore(10)
	defer store.Stop()
	store.SetDurableJournal(journal)
	const sessionID = "retained-chat"
	if err := store.SetSessionPersistenceClass(sessionID, events.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	store.SetEventAddedCallback(func(session string, event events.Event) {
		if event.Type == "unified_completion" {
			store.AddEvent(session, events.Event{ID: "file-snapshot:1", Type: "reply_file_changes", Timestamp: time.Now()})
		}
	})
	api := &StreamingAPI{
		eventStore: store, runtimeCoordinator: NewRuntimeCoordinator(),
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "running", CreatedAt: time.Now()},
		},
	}
	req := httptest.NewRequest("GET", "/api/sessions/"+sessionID+"/events/stream?durable_chat=1&since=0", nil)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	ctx, cancel := context.WithCancel(req.Context())
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		api.handleSSEStream(w, req.WithContext(ctx))
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	store.AddEvent(sessionID, events.Event{
		ID: "turn-completion", Type: "unified_completion", Timestamp: time.Now(),
		Data: pkgevents.NewAgentEvent(&pkgevents.UnifiedCompletionEvent{Status: "completed", FinalResult: "done"}),
	})
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	body := w.Body.String()
	if !strings.Contains(body, `"id":"file-snapshot:1"`) {
		t.Fatalf("setup: the nested event was not streamed: %s", body)
	}
	if !strings.Contains(body, `"id":"turn-completion"`) {
		t.Fatalf("the completion published after a newer event was dropped: %s", body)
	}
}
