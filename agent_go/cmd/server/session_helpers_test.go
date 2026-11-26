package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	pkgevents "github.com/manishiitg/mcpagent/events"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

// #3245: only the chat's owner reads its helper list, and it holds only
// helper steps.
func TestSessionHelperEventsEndpoint(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	api, store := newDurableChatAPI(t, "chat-a", "alice")
	for id, name := range map[string]string{"spawn-1": "spawnAgent", "read-1": "read_file"} {
		store.AddEvent("chat-a", events.Event{ID: id, Type: "tool_call_start", Timestamp: time.Now(), Data: &pkgevents.AgentEvent{
			Type: pkgevents.EventType("tool_call_start"),
			Data: events.NewGenericEventData("tool_call_start", map[string]interface{}{"tool_name": name, "tool_call_id": id}),
		}})
	}
	read := func(user string) (int, string) {
		req := httptest.NewRequest("GET", "/api/sessions/chat-a/helper-events", nil)
		req = mux.SetURLVars(req, map[string]string{"session_id": "chat-a"})
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user}))
		w := httptest.NewRecorder()
		api.handleGetSessionHelperEvents(w, req)
		return w.Code, w.Body.String()
	}
	if code, body := read("alice"); code != 200 || !strings.Contains(body, "spawn-1") || strings.Contains(body, "read-1") {
		t.Fatalf("owner read = %d %s", code, body)
	}
	if code, _ := read("bob"); code != 404 {
		t.Fatalf("other user read = %d, want 404", code)
	}

	// #4295: ETag + If-None-Match returns 304 when no new events arrived,
	// and invalidates as soon as a new helper event is appended.
	req := httptest.NewRequest("GET", "/api/sessions/chat-a/helper-events", nil)
	req = mux.SetURLVars(req, map[string]string{"session_id": "chat-a"})
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "alice"}))
	w := httptest.NewRecorder()
	api.handleGetSessionHelperEvents(w, req)
	etag := w.Header().Get("ETag")
	if w.Code != 200 || etag == "" {
		t.Fatalf("expected 200 with ETag, got %d etag=%q", w.Code, etag)
	}

	req304 := httptest.NewRequest("GET", "/api/sessions/chat-a/helper-events", nil)
	req304.Header.Set("If-None-Match", etag)
	req304 = mux.SetURLVars(req304, map[string]string{"session_id": "chat-a"})
	req304 = req304.WithContext(context.WithValue(req304.Context(), UserContextKey, &UserClaims{UserID: "alice"}))
	w304 := httptest.NewRecorder()
	api.handleGetSessionHelperEvents(w304, req304)
	if w304.Code != 304 || w304.Body.Len() != 0 {
		t.Fatalf("expected 304 empty body, got %d len=%d", w304.Code, w304.Body.Len())
	}

	store.AddEvent("chat-a", events.Event{ID: "spawn-2", Type: "tool_call_start", Timestamp: time.Now(), Data: &pkgevents.AgentEvent{
		Type: pkgevents.EventType("tool_call_start"),
		Data: events.NewGenericEventData("tool_call_start", map[string]interface{}{"tool_name": "spawnAgent", "tool_call_id": "spawn-2"}),
	}})
	reqAfter := httptest.NewRequest("GET", "/api/sessions/chat-a/helper-events", nil)
	reqAfter.Header.Set("If-None-Match", etag)
	reqAfter = mux.SetURLVars(reqAfter, map[string]string{"session_id": "chat-a"})
	reqAfter = reqAfter.WithContext(context.WithValue(reqAfter.Context(), UserContextKey, &UserClaims{UserID: "alice"}))
	wAfter := httptest.NewRecorder()
	api.handleGetSessionHelperEvents(wAfter, reqAfter)
	if wAfter.Code != 200 || !strings.Contains(wAfter.Body.String(), "spawn-2") || wAfter.Header().Get("ETag") == etag {
		t.Fatalf("expected refreshed 200 with spawn-2 and new ETag, got %d etag=%q body=%s", wAfter.Code, wAfter.Header().Get("ETag"), wAfter.Body.String())
	}
}
