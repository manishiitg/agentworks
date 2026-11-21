package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

// #3188: when an agent ignores Stop, Force stop must end the turn so the chat is not left "working",
// while the conversation stays usable (it is not a full session stop).
func TestForceStopEndsTheTurnTheAgentIgnored(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]}]}`)
	sid := "force-stop-chat"
	api, store := newDurableChatAPI(t, sid, "owner")
	store.EnableTurnPhase()
	store.AddEvent(sid, events.Event{ID: "msg-2", Type: "user_message", Timestamp: time.Now()})
	if !store.TurnInProgress(sid) {
		t.Fatal("setup: no running turn")
	}
	api.activeSessions = map[string]*ActiveSessionInfo{sid: {SessionID: sid, UserID: "owner"}}
	canceled := false
	api.agentCancelFuncs = map[string]context.CancelFunc{sid: func() { canceled = true }}
	r := sharedSecretsRequest(http.MethodPost, "/api/session/cancel-turn?force=true", "owner", nil)
	r.Header.Set("X-Session-ID", sid)
	w := httptest.NewRecorder()
	api.handleCancelCurrentTurn(w, r)
	if w.Code != http.StatusNoContent || !canceled {
		t.Fatal("force stop did not cancel the turn", w.Code)
	}
	if store.TurnInProgress(sid) {
		t.Fatal("force stop left the chat working")
	}
	api.stoppedSessionsMu.Lock()
	stopped := api.stoppedSessions[sid]
	api.stoppedSessionsMu.Unlock()
	if stopped {
		t.Fatal("force stop ended the whole conversation")
	}
}
