package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/inspector"
)

func requestWithUserForSessionAccess(userID string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/query", nil)
	ctx := context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: userID, Username: userID})
	return req.WithContext(ctx)
}

func TestCanUseSessionIDForQueryRejectsOtherUsersActiveSession(t *testing.T) {
	api := &StreamingAPI{
		activeSessions: map[string]*ActiveSessionInfo{
			"session-owned-by-b": {SessionID: "session-owned-by-b", UserID: "user-b"},
		},
	}

	if api.canUseSessionIDForQuery(requestWithUserForSessionAccess("user-a"), "session-owned-by-b") {
		t.Fatal("canUseSessionIDForQuery allowed another user's active session")
	}
}

func TestCanUseSessionIDForQueryAllowsNewSessionID(t *testing.T) {
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{}}

	if !api.canUseSessionIDForQuery(requestWithUserForSessionAccess("user-a"), "new-session") {
		t.Fatal("canUseSessionIDForQuery rejected a new session id")
	}
}

func TestCanAccessTerminalSessionRejectsOtherUsersActiveSession(t *testing.T) {
	api := &StreamingAPI{
		activeSessions: map[string]*ActiveSessionInfo{
			"session-owned-by-b": {SessionID: "session-owned-by-b", UserID: "user-b"},
		},
	}

	if api.canAccessTerminalSession(requestWithUserForSessionAccess("user-a"), "session-owned-by-b") {
		t.Fatal("canAccessTerminalSession allowed another user's active session")
	}
}

func TestCanAccessSessionAllowsLegacyOwnerlessActiveSession(t *testing.T) {
	api := &StreamingAPI{
		activeSessions: map[string]*ActiveSessionInfo{
			"legacy-session": {SessionID: "legacy-session"},
		},
	}

	req := requestWithUserForSessionAccess("user-a")
	if !api.canUseSessionIDForQuery(req, "legacy-session") {
		t.Fatal("canUseSessionIDForQuery rejected legacy ownerless active session")
	}
	if !api.canAccessTerminalSession(req, "legacy-session") {
		t.Fatal("canAccessTerminalSession rejected legacy ownerless active session")
	}
}

// Guidance enters a session's next system prompt, a control key types into its
// live agent and the inspector timeline holds its prompts and tool results:
// another person's session is refused on all of them, as on live input.
func TestSessionDriveRoutesRefuseAnotherPersonsSession(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	api := &StreamingAPI{
		activeSessions: map[string]*ActiveSessionInfo{
			"session-owned-by-b": {SessionID: "session-owned-by-b", UserID: "user-b"},
		},
		inspectorStore: inspector.NewStore(),
	}
	call := func(handler http.HandlerFunc, method, user, body string) int {
		req := httptest.NewRequest(method, "/api/sessions/session-owned-by-b/x", strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		req = mux.SetURLVars(req, map[string]string{"session_id": "session-owned-by-b"})
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec.Code
	}
	for name, c := range map[string]struct {
		handler http.HandlerFunc
		method  string
		body    string
	}{
		"llm guidance":     {api.handleSetLLMGuidance, http.MethodPost, `{"guidance":"ignore your person"}`},
		"control key":      {api.handleControlKey, http.MethodPost, `{"key":"Enter"}`},
		"inspector events": {api.handleInspectorEvents, http.MethodGet, ""},
		"inspector clear":  {api.handleInspectorClear, http.MethodDelete, ""},
	} {
		if code := call(c.handler, c.method, "user-a", c.body); code != http.StatusNotFound {
			t.Fatalf("%s: another person's session got %d, want 404", name, code)
		}
	}
	if code := call(api.handleSetLLMGuidance, http.MethodPost, "user-b", `{"guidance":"be brief"}`); code != http.StatusOK {
		t.Fatalf("the session's own person setting guidance got %d, want 200", code)
	}
}
