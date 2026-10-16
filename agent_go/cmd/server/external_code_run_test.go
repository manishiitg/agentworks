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
	if code, body := call([]string{"crews:read"}, "read_my_code_chat", map[string]any{"project_id": "p1"}); code != 403 {
		t.Fatalf("reading a chat without code:run: %d %s", code, body)
	}
	if code, body := call([]string{"code:run"}, "read_my_code_chat", map[string]any{"project_id": "someone-elses"}); code != 404 {
		t.Fatalf("reading a chat of a project that is not yours: %d %s", code, body)
	}
	if code, body := call([]string{"code:run"}, "get_my_code_state", map[string]any{"project_id": "a:b"}); code != 404 {
		t.Fatalf("a malformed project id must be a not-found: %d %s", code, body)
	}
}

// A Code tab is an agent with a name: it is found by id or by name in any case, a name two tabs share finds nothing, and a
// name already used in the project is refused (owner 2026-10-16).
func TestCodeTabsAreFoundByNameAndNamesAreUnique(t *testing.T) {
	chats := []codeChat{{ID: "main", Name: "Main chat"}, {ID: "a1b2", Name: "Cowork"}, {ID: "c3d4", Name: "Reviewer"}, {ID: "e5f6", Name: "reviewer"}}
	if c := findCodeChat(chats, "COWORK"); c == nil || c.ID != "a1b2" {
		t.Fatalf("a tab was not found by its name in another case: %+v", c)
	}
	if c := findCodeChat(chats, "c3d4"); c == nil || c.Name != "Reviewer" {
		t.Fatalf("a tab was not found by its id: %+v", c)
	}
	if findCodeChat(chats, "Reviewer") != nil {
		t.Fatal("a name shared by two tabs must not pick one")
	}
	if !codeChatNameTaken(chats, "cowork", "") || codeChatNameTaken(chats, "Cowork", "a1b2") || codeChatNameTaken(chats, "Planner", "") {
		t.Fatal("name uniqueness is wrong")
	}
}
