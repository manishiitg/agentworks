package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// The new skills edge must not turn a caller's supplied workspace into an
// account-wide or cross-user filesystem capability.
func TestWorkspaceSkillRouteRequiresAnAuthorizedScope(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[{"id":"bob","username":"bob","can_edit":true},{"id":"viewer","username":"viewer"}]}`)
	claims := &UserClaims{UserID: "bob", Username: "bob"}
	for _, tc := range []struct {
		scope string
		want  int
	}{
		{"", 400}, {"skills", 400}, {"../Workflow/private", 400},
		{"_users/alice/Chats/Code/projects/private", 403},
		{"Chats/Code/projects/mine", 200},
	} {
		req := httptest.NewRequest("GET", "/api/skills?workspace_path="+url.QueryEscape(tc.scope), nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
		rec := httptest.NewRecorder()
		workspaceSkillRoute(false, func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("workspace_path"); got != "_users/bob/Chats/Code/projects/mine" {
				t.Fatalf("uncanonicalized scope: %s", got)
			}
			w.WriteHeader(200)
		})(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("scope %q: %d %s", tc.scope, rec.Code, rec.Body.String())
		}
	}
	req := httptest.NewRequest("DELETE", "/api/skills/demo?workspace_path=Chats/Code/projects/mine", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "viewer", Username: "viewer"}))
	rec := httptest.NewRecorder()
	workspaceSkillRoute(true, func(http.ResponseWriter, *http.Request) { t.Fatal("viewer reached mutation handler") })(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer mutation: %d %s", rec.Code, rec.Body.String())
	}

}
