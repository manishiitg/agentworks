package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// Shared by the real Google/Slack persistence regressions: drive the actual
// external settings adapter with existing workflow Builder consent.
func workflowAccountMCPRequest(t *testing.T, api *StreamingAPI, user, provider string, account map[string]any, want int) map[string]any {
	t.Helper()
	claims := &UserClaims{UserID: user, Username: user, AccessToken: &accesstokens.Token{
		Scopes: []string{"builder:chat", "workflows:read", "files:read", "runs:execute"}, WorkflowIDs: []string{"orders"},
	}}
	body, _ := json.Marshal(map[string]any{"name": "settings", "arguments": map[string]any{
		"action": provider + "_account", "workflow_id": "orders", "account": account,
	}})
	w := httptest.NewRecorder()
	api.handleExternalCall(w, adminRequest("POST", "/api/external/v1/call", string(body), claims, nil))
	if w.Code != want {
		t.Fatalf("%s account MCP: status=%d want=%d body=%s", provider, w.Code, want, w.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
