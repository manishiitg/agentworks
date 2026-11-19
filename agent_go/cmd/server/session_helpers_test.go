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
}
