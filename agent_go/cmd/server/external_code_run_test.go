package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// Running your own Code needs code:run, and a project that is not the caller's own is one not-found.
func TestCodeRunNeedsTheScopeAndOwnProjects(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true}]}`)
	api := &StreamingAPI{}
	call := func(scopes []string, name string, args map[string]any) (int, string) {
		claims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: scopes, AllCrews: true}}
		raw, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
		w := httptest.NewRecorder()
		api.handleExternalCall(w, adminRequest("POST", "/api/external/v1/call", string(raw), claims, nil))
		return w.Code, w.Body.String()
	}
	if code, body := call([]string{"crews:read", "crews:run"}, "ask_my_code", map[string]any{"project_id": "p1", "message": "hi"}); code != 403 {
		t.Fatalf("without code:run: %d %s", code, body)
	}
	code, body := call([]string{"code:run"}, "ask_my_code", map[string]any{"project_id": "someone-elses", "message": "hi"})
	if code != 404 || !strings.Contains(body, "No Code project of yours") {
		t.Fatalf("not your project: %d %s", code, body)
	}
	if code, body := call([]string{"code:run"}, "get_my_code_state", map[string]any{"project_id": "a:b"}); code != 404 {
		t.Fatalf("a malformed project id must be a not-found: %d %s", code, body)
	}
}
