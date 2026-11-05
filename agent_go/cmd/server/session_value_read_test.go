package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// `mcpbridge secret NAME` (decisions log, 5 Nov): only the chat's own secret-read token reads, and only while its reply
// runs; the bridge's tool token is refused; a Run turn gets nothing; a selected secret returns its current value, and an
// unselected name is refused.
func TestSessionSecretReadFollowsTheChatsOwnSelection(t *testing.T) {
	common.SetBridgeTokenSecret("test-bridge-secret")
	previous := sessionSecretsResolver
	t.Cleanup(func() { sessionSecretsResolver = previous })
	sessionSecretsResolver = func(_ *StreamingAPI, _ context.Context, source sessionSecretSource) []resolvedSecret {
		if source.RunMode {
			t.Fatal("a Run turn's secrets were resolved")
		}
		return []resolvedSecret{{Name: "GITHUB_TOKEN", Value: "current-value"}}
	}
	recordSessionSecretSource("chat-owner", sessionSecretSource{User: "owner"})
	recordSessionSecretSource("chat-run", sessionSecretSource{User: "reader", RunMode: true})
	api := &StreamingAPI{}
	api.setSessionBusy("chat-owner", true)
	api.setSessionBusy("chat-run", true)
	readToken := func(session string) string {
		return common.BridgeTokenForSession(session + mcpagent.SecretReadTokenSuffix)
	}
	read := func(token, name string) (int, string) {
		r := httptest.NewRequest(http.MethodPost, "/bridge/secret-read", strings.NewReader(`{"name":"`+name+`"}`))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		api.handleSessionSecretRead(w, r)
		return w.Code, w.Body.String()
	}
	if code, body := read(readToken("chat-owner"), "GITHUB_TOKEN"); code != 200 || !strings.Contains(body, "current-value") {
		t.Fatalf("selected secret: %d %s", code, body)
	}
	if code, body := read(common.BridgeTokenForSession("chat-owner"), "GITHUB_TOKEN"); code != 401 || strings.Contains(body, "current-value") {
		t.Fatalf("the bridge's tool token read a secret: %d %s", code, body)
	}
	if code, body := read(readToken("chat-owner"), "OTHER_TOKEN"); code != 404 || strings.Contains(body, "current-value") {
		t.Fatalf("an unselected name was answered: %d %s", code, body)
	}
	if code, body := read(readToken("chat-run"), "GITHUB_TOKEN"); code != 403 || strings.Contains(body, "current-value") {
		t.Fatalf("a Run turn got a secret: %d %s", code, body)
	}
	api.setSessionBusy("chat-owner", false)
	if code, body := read(readToken("chat-owner"), "GITHUB_TOKEN"); code != 403 || strings.Contains(body, "current-value") {
		t.Fatalf("a secret was read after the chat's reply ended: %d %s", code, body)
	}
	if code, _ := read("not-a-session-token", "GITHUB_TOKEN"); code != 401 {
		t.Fatalf("a token that is not a session token was accepted: %d", code)
	}
}
