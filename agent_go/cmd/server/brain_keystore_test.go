package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
)

// Brain lists only its own secrets, never Vault's platform secrets (owner, 2026-10-06), to people who own the whole
// Brain; a backup token that so far lived only among the platform secrets is copied into Brain's store on first use.
func TestBrainSecretsAreBrainsOwn(t *testing.T) {
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	knowledgebaseServerTest(t)
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{chatStore: store}
	previous := brainSecretsAPI
	brainSecretsAPI = api
	t.Cleanup(func() { brainSecretsAPI = previous })
	managedGlobalsMu.Lock()
	if managedGlobals == nil {
		managedGlobals = map[string]string{}
	}
	managedGlobals["PLATFORM_ONLY_KEY"] = "platform"
	managedGlobals["OLD_BRAIN_PAT"] = "old-token"
	managedGlobalsMu.Unlock()
	t.Cleanup(func() {
		managedGlobalsMu.Lock()
		delete(managedGlobals, "PLATFORM_ONLY_KEY")
		delete(managedGlobals, "OLD_BRAIN_PAT")
		managedGlobalsMu.Unlock()
	})
	call := func(user, method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/knowledgebase/secrets", strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		w := httptest.NewRecorder()
		api.handleBrainSecrets(w, r)
		return w
	}
	if w := call("admin", http.MethodPut, `{"name":"BRAIN_GITHUB_PAT","value":"token"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if value, ok := knowledgebaseBackupSecret("OLD_BRAIN_PAT"); !ok || value != "old-token" {
		t.Fatal("a token that lived among the platform secrets must keep working")
	}
	w := call("admin", http.MethodGet, "")
	var got struct{ Secrets []string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || strings.Join(got.Secrets, ",") != "BRAIN_GITHUB_PAT,OLD_BRAIN_PAT" {
		t.Fatalf("Brain must list only its own secrets: %d %s", w.Code, w.Body.String())
	}
	if w := call("priya", http.MethodGet, ""); w.Code != http.StatusForbidden {
		t.Fatal("only people who own the whole Brain see its secrets", w.Code)
	}
}
