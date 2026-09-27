package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func bridgeAuthRouter(t *testing.T) (*mux.Router, *string, *string) {
	t.Helper()
	common.SetBridgeTokenSecret("server-secret")
	t.Cleanup(func() { common.SetBridgeTokenSecret("") })
	seen, seenCtx := new(string), new(string)
	handler := func(w http.ResponseWriter, r *http.Request) {
		*seen = r.Header.Get("X-Session-ID")
		*seenCtx, _ = r.Context().Value(common.ChatSessionIDKey).(string)
		w.WriteHeader(http.StatusOK)
	}
	router := mux.NewRouter()
	tools := router.PathPrefix("/tools").Subrouter()
	tools.Use(bridgeAuthMiddleware("server-secret"))
	tools.HandleFunc("/custom/{tool}", handler)
	scoped := router.PathPrefix("/s/{session_id}/tools").Subrouter()
	scoped.Use(bridgeAuthMiddleware("server-secret"))
	scoped.HandleFunc("/custom/{tool}", handler)
	return router, seen, seenCtx
}

func bridgeCall(router *mux.Router, path, token, header string) int {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if header != "" {
		req.Header.Set("X-Session-ID", header)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec.Code
}

// A session token acts only as its own session, whichever way the request
// names one.
func TestBridgeAuthPinsTheCallToTheTokensSession(t *testing.T) {
	router, seen, seenCtx := bridgeAuthRouter(t)
	alice := common.BridgeTokenForSession("chat-alice")

	if code := bridgeCall(router, "/s/chat-alice/tools/custom/search_platform", alice, ""); code != http.StatusOK || *seen != "chat-alice" {
		t.Fatalf("own scoped route: %d session=%q", code, *seen)
	}
	if code := bridgeCall(router, "/tools/custom/search_platform", alice, ""); code != http.StatusOK || *seen != "chat-alice" || *seenCtx != "chat-alice" {
		t.Fatalf("global route must be pinned to the token's session: %d header=%q ctx=%q", code, *seen, *seenCtx)
	}
	if code := bridgeCall(router, "/tools/custom/search_platform", alice, "chat-alice"); code != http.StatusOK {
		t.Fatalf("own session header: %d", code)
	}
	// The reported attack: name someone else's session.
	if code := bridgeCall(router, "/s/chat-bob/tools/custom/search_platform", alice, ""); code != http.StatusForbidden {
		t.Errorf("another session's path must be refused, got %d", code)
	}
	if code := bridgeCall(router, "/tools/custom/search_platform", alice, "chat-bob"); code != http.StatusForbidden {
		t.Errorf("another session's header must be refused, got %d", code)
	}
}

// The process-wide token no longer works on agent routes; a rollback switch
// re-admits it.
func TestBridgeAuthRefusesTheGlobalToken(t *testing.T) {
	router, _, _ := bridgeAuthRouter(t)
	for _, token := range []string{"server-secret", "garbage", ""} {
		if code := bridgeCall(router, "/s/chat-bob/tools/custom/search_platform", token, ""); code != http.StatusUnauthorized {
			t.Errorf("token %q must be refused, got %d", token, code)
		}
	}
	t.Setenv(envBridgeAllowGlobalToken, "1")
	router, _, _ = bridgeAuthRouter(t)
	if code := bridgeCall(router, "/tools/custom/search_platform", "server-secret", "chat-bob"); code != http.StatusOK {
		t.Fatalf("the rollback switch must re-admit the global token, got %d", code)
	}
}
