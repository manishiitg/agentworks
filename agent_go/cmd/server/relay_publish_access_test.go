package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func TestRelayPublishRequiresWriterAndTokenScope(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"},{"id":"reader","username":"reader","role":"viewer","products":["agentworks"]}]}`)
	m := NewWorkflowManifest("Protected Relay")
	m.Kind, m.RelayRuntime = "relay", "python"
	m.Access = &WorkflowAccess{Owners: []string{"owner"}, Readers: []string{"reader"}}
	raw, _ := json.Marshal(m)
	ws := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{manifestPath("Workflow/protected"): string(raw)}})
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	for _, tc := range []struct {
		name   string
		claims *UserClaims
		status int
	}{
		{"reader", &UserClaims{UserID: "reader"}, 403},
		{"reader with scope", &UserClaims{UserID: "reader", AccessToken: &accesstokens.Token{Scopes: []string{"relays:write"}, WorkflowIDs: []string{m.ID}}}, 403},
		{"owner without scope", &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"runs:execute"}, WorkflowIDs: []string{m.ID}}}, 404},
		{"owner wrong workflow", &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"relays:write"}, WorkflowIDs: []string{"another"}}}, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/relays/"+m.ID+"/releases", nil)
			req = mux.SetURLVars(req, map[string]string{"id": m.ID})
			req = req.WithContext(context.WithValue(req.Context(), UserContextKey, tc.claims))
			out := httptest.NewRecorder()
			(&StreamingAPI{}).handlePublishRelayRelease(out, req)
			if out.Code != tc.status {
				t.Fatalf("status %d: %s", out.Code, out.Body.String())
			}
		})
	}
}
