package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// Drive the website and native/MCP operation against the real encrypted bot
// registry and persisted manifests. No Slack network or message is needed.
func TestSlackVaultSelectionPermissionsAcrossSurfaces(t *testing.T) {
	api, workspace := setupSlackConnectionTest(t)
	workspace.files["Workflow/flow/workflow.json"] = `{"schema_version":1,"id":"flow","kind":"relay","access":{"owners":["alice"]}}`
	svc, err := ensureSlackService()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := svc.CreateSlackConnection(context.Background(), services.SlackConnectionInput{DisplayName: "Saved bot", BotToken: "xoxb-fixture-private", AppToken: "xapp-fixture-private", Enabled: false, WorkspacePath: "Workflow/alpha"})
	if err != nil {
		t.Fatal(err)
	}
	call := func(user, action, path, id string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"action": action, "workspace_path": path, "connection_id": id})
		rec := httptest.NewRecorder()
		api.handleSlackVaultBots(rec, slackConnectionRequest(t, http.MethodPost, user, string(body)))
		return rec
	}
	saved := call("alice", "list_saved", "", "")
	if saved.Code != 200 || !strings.Contains(saved.Body.String(), "Saved bot") {
		t.Fatalf("saved bot not listed: %d %s", saved.Code, saved.Body)
	}
	for _, private := range []string{"bot_token", "app_token", "fixture-private", "xoxb-", "xapp-"} {
		if strings.Contains(saved.Body.String(), private) {
			t.Fatalf("credential field/value returned: %s", private)
		}
	}
	if other := call("bob", "list_saved", "", ""); other.Code != 200 || strings.Contains(other.Body.String(), "Saved bot") {
		t.Fatal("another owner's saved bot was exposed")
	}
	if denied := call("bob", "select", "Workflow/beta", conn.ID); denied.Code != 403 {
		t.Fatal("another owner's bot was selected")
	}
	selected := call("alice", "select", "Workflow/alpha", conn.ID)
	if selected.Code != 200 {
		t.Fatalf("website selection failed: %s", selected.Body)
	}
	reg := &recordingRegistrar{}
	if err := api.registerSlackVaultBotTool(reg, "Workflow/alpha"); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), UserContextKey, slackConnectionClaims("alice"))
	if _, err := reg.tools["manage_slack_bot"].exec(ctx, map[string]any{"action": "select", "connection_id": conn.ID}); err != nil {
		t.Fatal(err)
	}
	// Exercise the public merged MCP action too, including catalog admission.
	for _, action := range []string{"list_saved", "select"} {
		body, _ := json.Marshal(map[string]any{"name": "settings", "arguments": map[string]any{
			"workflow_id": "wf_alpha", "action": "slack_bot", "bot": map[string]any{"action": action, "connection_id": conn.ID},
		}})
		claims := slackConnectionClaims("alice")
		rec := httptest.NewRecorder()
		api.handleExternalCall(rec, adminRequest(http.MethodPost, "/api/external/call", string(body), claims, nil))
		if rec.Code != http.StatusForbidden {
			t.Fatal("external MCP allowed bot setup without Builder consent")
		}
		claims.AccessToken = &accesstokens.Token{AllWorkflows: true, Scopes: []string{"builder:chat", "workflows:read", "files:read", "runs:execute"}}
		rec = httptest.NewRecorder()
		api.handleExternalCall(rec, adminRequest(http.MethodPost, "/api/external/call", string(body), claims, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("external MCP %s: %d %s", action, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "fixture-private") || strings.Contains(rec.Body.String(), "bot_token") {
			t.Fatal("external MCP exposed bot credentials")
		}
	}
	manifest, found, err := ReadWorkflowManifest(ctx, "Workflow/alpha")
	if err != nil || !found || manifest.Capabilities.SlackConnectionID != conn.ID {
		t.Fatal("website/MCP did not share the saved connection reference")
	}
	if len(svc.ListConnections()) != 1 {
		t.Fatal("selecting a bot copied its credentials into a duplicate connection")
	}
	flow := call("alice", "list_saved", "Workflow/flow", "")
	if flow.Code != 200 || !strings.Contains(flow.Body.String(), `"allow_bots":false`) {
		t.Fatalf("Flow policy not projected: %s", flow.Body)
	}
	if denied := call("alice", "select", "Workflow/flow", conn.ID); denied.Code != 403 {
		t.Fatal("Flow accepted an incoming bot")
	}
	if denied := call("bob", "save", "", conn.ID); denied.Code != 403 {
		t.Fatal("non-owner changed saved bot credentials")
	}
	if _, err := reg.tools["manage_slack_bot"].exec(ctx, map[string]any{"action": "save", "bot_token": "never-store"}); err == nil {
		t.Fatal("product bot-choice tool accepted credential mutation")
	}
}
