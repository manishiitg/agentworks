package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// Brain-chat backup setup runs when the person asks (no approval card), and a Git token never passes through the chat:
// it is a platform secret the person adds under Secrets, and setup names it (owner, 2026-10-06).
func TestKnowledgebaseChatBackupSetupIsDirectAndTokenIsASecret(t *testing.T) {
	tokenTestSetup(t)
	api, _ := knowledgebaseServerTest(t)
	claims := &UserClaims{UserID: "admin", Username: "admin"}
	ctx := context.WithValue(t.Context(), UserContextKey, claims)
	configured := func() bool {
		t.Helper()
		w := httptest.NewRecorder()
		api.handleKnowledgebaseViewer(w, httptest.NewRequest(http.MethodGet, "/api/knowledgebase/bootstrap", nil).WithContext(ctx))
		var response struct {
			Configured bool `json:"backup_configured"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil {
			t.Fatal(w.Code, w.Body)
		}
		return response.Configured
	}
	runtime := agentprofiles.ToolRuntimeContext{Product: "knowledgebase", UserID: "admin"}
	args := map[string]any{"action": "configure_backup", "username": "git", "remote_url": "https://github.com/org/knowledge.git", "branch": "main", "request_id": "direct-setup"}
	if configured() {
		t.Fatal("backup configured before setup")
	}
	if _, err := knowledgebaseAccessExecutor(ctx, runtime, args); err != nil || !configured() {
		t.Fatalf("setup should run directly: %v configured=%v", err, configured())
	}
	// A token pasted into chat is refused and never echoed.
	const token = "chat-token-should-never-be-accepted"
	if _, err := knowledgebaseAccessExecutor(ctx, runtime, map[string]any{"action": "configure_backup", "username": "git", "remote_url": "https://github.com/org/knowledge.git", "branch": "main", "request_id": "chat-pat", "pat": token}); err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("a token sent through chat must be refused without echoing it: %v", err)
	}
	// Naming a secret that does not exist fails; naming one that does works and the token is never in the result.
	named := map[string]any{"action": "configure_backup", "username": "git", "remote_url": "https://github.com/org/knowledge.git", "branch": "main", "request_id": "secret-setup", "pat_secret": "BRAIN_TEST_PAT"}
	if _, err := knowledgebaseAccessExecutor(ctx, runtime, named); err == nil {
		t.Fatal("a missing secret must be refused")
	}
	managedGlobalsMu.Lock()
	if managedGlobals == nil {
		managedGlobals = map[string]string{}
	}
	managedGlobals["BRAIN_TEST_PAT"] = "secret-value-for-test"
	managedGlobalsMu.Unlock()
	t.Cleanup(func() { managedGlobalsMu.Lock(); delete(managedGlobals, "BRAIN_TEST_PAT"); managedGlobalsMu.Unlock() })
	named["request_id"] = "secret-setup-2"
	result, err := knowledgebaseAccessExecutor(ctx, runtime, named)
	if err != nil || !strings.Contains(result, `"pat_secret":"BRAIN_TEST_PAT"`) || strings.Contains(result, "secret-value-for-test") {
		t.Fatalf("secret setup failed or leaked: %v %s", err, result)
	}
}
