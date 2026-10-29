package server

import (
	"context"
	"github.com/gorilla/mux"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUsageMessagesKeepPrivateChatAccess(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	api, store := newDurableChatAPI(t, "usage-chat", "alice")
	store.AddEvent("usage-chat", storeevents.Event{ID: "user", Type: "user_message", Timestamp: time.Now(), UsageExecutionID: "turn", Data: agentevents.NewAgentEvent(storeevents.NewGenericEventData("user_message", map[string]interface{}{"content": "Check the report"}))})
	call := func(user string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/?execution_id=turn", nil)
		req = mux.SetURLVars(req, map[string]string{"session_id": "usage-chat"})
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user}))
		w := httptest.NewRecorder()
		api.handleUsageTurnMessages(w, req)
		return w
	}
	if w := call("bob"); w.Code == 200 || strings.Contains(w.Body.String(), "Check the report") {
		t.Fatalf("private message leaked: %d", w.Code)
	}
	if w := call("alice"); w.Code != 200 || !strings.Contains(w.Body.String(), "Check the report") || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("owner read: %d %s", w.Code, w.Body.String())
	}
}
