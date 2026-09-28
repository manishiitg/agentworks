package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/wsauth"
)

// The /api/wp proxy reaches the workspace service with the server's token,
// never one a browser supplied.
func TestWorkspaceProxyCarriesServerTokenOnly(t *testing.T) {
	var seen string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get(wsauth.HeaderName)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("WORKSPACE_API_URL", upstream.URL)
	t.Setenv("WORKSPACE_API_TOKEN", "server-token")
	handler := workspaceProxyHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/wp/api/documents/Chats/x.md", nil)
	req.Header.Set(wsauth.HeaderName, "browser-guess")
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "alice"}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || seen != "server-token" {
		t.Fatalf("status=%d upstream token=%q", rec.Code, seen)
	}
}
