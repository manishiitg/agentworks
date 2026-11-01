package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
)

func TestBrowserConnectionsKeepAccountCodeOutOfProjectTokens(t *testing.T) {
	f := newExternalToolsFixture(t)
	previous := browserrelay.Default
	browserrelay.Default = browserrelay.New()
	defer func() { browserrelay.Default.Close(); browserrelay.Default = previous }()
	token, err := browserrelay.Default.PairAccount("owner", false)
	if err != nil {
		t.Fatal(err)
	}
	claims := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"builder:chat", "workflows:read", "files:read", "runs:execute"}, WorkflowIDs: []string{"invoices"}}}
	call := func(args map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		action := externalArg(args, "action")
		delete(args, "action")
		raw, _ := json.Marshal(map[string]any{"name": "browser_connections__" + action, "arguments": args})
		w := httptest.NewRecorder()
		f.api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(raw), claims, nil))
		return w
	}
	if w := call(map[string]any{"action": "list"}); w.Code != 200 || strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), "account-paired") {
		t.Fatalf("metadata leaked account control: %d %s", w.Code, w.Body)
	}
	if w := call(map[string]any{"action": "select", "workflow_id": "secret", "browser_id": "11111111-1111-4111-8111-111111111111"}); w.Code != 403 {
		t.Fatalf("project grant escaped: %d %s", w.Code, w.Body)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/browser/extension", strings.NewReader(`{"action":"pair"}`)).WithContext(context.WithValue(context.Background(), UserContextKey, claims))
	w := httptest.NewRecorder()
	f.api.handleBrowserExtension(w, r)
	if w.Code != 403 || strings.Contains(w.Body.String(), token) {
		t.Fatalf("project token copied general account credential: %d %s", w.Code, w.Body)
	}
}
