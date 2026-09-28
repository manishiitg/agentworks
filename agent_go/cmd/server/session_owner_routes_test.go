package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

// Dismiss and reconnect act only on sessions the caller may see (PLAT-362 D5).
func TestSessionRoutesRefuseAnotherUsersSession(t *testing.T) {
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{
		"chat-alice": {SessionID: "chat-alice", UserID: "alice", Status: "running"},
	}}
	router := mux.NewRouter()
	router.HandleFunc("/api/sessions/{session_id}/dismiss", api.handleDismissSession)
	router.HandleFunc("/api/sessions/{session_id}/reconnect", api.handleReconnectSession)
	call := func(user, path string) int {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := call("bob", "/api/sessions/chat-alice/reconnect"); code == http.StatusOK {
		t.Error("bob must not reconnect to alice's session")
	}
	if code := call("alice", "/api/sessions/chat-alice/reconnect"); code != http.StatusOK {
		t.Errorf("alice reconnects to her own session, got %d", code)
	}
	if code := call("bob", "/api/sessions/chat-alice/dismiss"); code == http.StatusOK {
		t.Error("bob must not dismiss alice's session")
	}
	if api.activeSessions["chat-alice"].Status == "dismissed" {
		t.Fatal("a refused dismiss changed the session")
	}
	if code := call("alice", "/api/sessions/chat-alice/dismiss"); code != http.StatusOK || api.activeSessions["chat-alice"].Status != "dismissed" {
		t.Errorf("alice dismisses her own session, got %d", code)
	}
}
