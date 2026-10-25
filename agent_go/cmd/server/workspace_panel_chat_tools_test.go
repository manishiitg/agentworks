package server

import (
	"context"
	"github.com/gin-gonic/gin"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
)

// Drive real settings/project handlers. A hidden selector, a retained tool
// after another person's turn, or a Run token must not widen the Builder.
func TestWorkspacePanelChatToolsBindCurrentBuilder(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AUTH_SECRET", "panel-fixture-encryption-key-for-tests")
	history, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.api.chatStore = history
	router := gin.New()
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.GET("/api/documents/*filepath", workspacehandlers.GetDocument)
	router.PUT("/api/documents/*filepath", workspacehandlers.UpdateDocument)
	workspace := httptest.NewServer(router)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	const session, root = "builder", "Workflow/invoices"
	f.api.lastQueryRequests = map[string]QueryRequest{session: {userID: "owner", SelectedFolder: root}}
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	reg := &recordingRegistrar{}
	if err := f.api.registerWorkspacePanelChatTools(reg, nil, "owner", session, root, "agentworks"); err != nil {
		t.Fatal(err)
	}
	for name := range reg.tools {
		if !agentworksproduct.ChatAllowsTool("builder", name) || agentworksproduct.ChatAllowsTool("run", name) {
			t.Fatalf("%s is not Builder-only", name)
		}
		props := reg.tools[name].params["properties"].(map[string]any)
		if props["workflow_id"] != nil || props["crew_id"] != nil {
			t.Fatalf("%s exposes a project selector", name)
		}
	}
	out, err := reg.tools["settings"].exec(ctx, map[string]any{"action": "get"})
	if err != nil || !strings.Contains(out, "version") {
		t.Fatalf("native settings read: %s %v", out, err)
	}
	out, err = reg.tools["manage_project"].exec(ctx, map[string]any{"action": "rename", "label": "Reviewed invoices", "workflow_id": "secret", "crew_id": "foreign"})
	if err != nil {
		t.Fatalf("native rename: %s %v", out, err)
	}
	manifest, _, _ := ReadWorkflowManifest(ctx, root)
	other, _, _ := ReadWorkflowManifest(ctx, "Workflow/secret")
	if manifest.Label != "Reviewed invoices" || other.Label != "Private research" {
		t.Fatal("project binding did not hold")
	}
	out, err = reg.tools["workflow_team"].exec(ctx, map[string]any{"action": "get"})
	if err != nil || !strings.Contains(out, "can_manage") {
		t.Fatalf("native team read: %s %v", out, err)
	}
	out, err = reg.tools["workflow_team"].exec(ctx, map[string]any{"action": "update", "expected_revision": 0, "confirm": "invoices", "team": map[string]any{"leader_id": "owner", "contributors": []any{}, "routes": []any{}}})
	if err != nil {
		t.Fatalf("native team update: %s %v", out, err)
	}
	reads := map[string]map[string]any{
		"settings": {"action": "get"}, "workflow_team": {"action": "get"},
		"manage_project": {"action": "get_access"}, "browser_connections": {"action": "list"},
		"manage_messaging": {"action": "whatsapp_link"}, "manage_pulse": {"action": "status"},
	}
	for name, args := range reads {
		if out, err := reg.tools[name].exec(ctx, args); err != nil {
			t.Fatalf("website Builder %s: %s %v", name, out, err)
		}
		for _, claims := range []*UserClaims{
			{UserID: "reader", Username: "reader"},
			{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{ID: "run-only", Scopes: []string{"runs:execute"}, AllWorkflows: true}},
		} {
			if _, err := reg.tools[name].exec(context.WithValue(ctx, UserContextKey, claims), args); err == nil {
				t.Fatalf("%s accepted a foreign user or Run token", name)
			}
		}
	}
	f.api.lastQueryRequests[session] = QueryRequest{userID: "reader", SelectedFolder: root}
	if _, err := reg.tools["settings"].exec(ctx, map[string]any{"action": "get"}); err == nil {
		t.Fatal("retained callback accepted a different current turn user")
	}
}

func TestRelayPanelToolsKeepDashboardSharing(t *testing.T) {
	f := newExternalToolsFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["agentworks","relays"]}]}`)
	const root, session = "Workflow/flow", "flow-builder"
	f.write(t, root+"/workflow.json", `{"id":"flow","kind":"relay","label":"Flow","created_by":"owner","access":{"owners":["owner"]}}`)
	f.api.lastQueryRequests = map[string]QueryRequest{session: {userID: "owner", SelectedFolder: root}}
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	reg := &recordingRegistrar{}
	if err := f.api.registerWorkspacePanelChatTools(reg, nil, "owner", session, root, "relays"); err != nil {
		t.Fatal(err)
	}
	if reg.tools["settings"].exec == nil || reg.tools["manage_project"].exec == nil || reg.tools["chat_preferences"].exec == nil {
		t.Fatal("Relay lost settings, sharing or personal display controls")
	}
	if reg.tools["workflow_team"].exec != nil || reg.tools["manage_pulse"].exec != nil {
		t.Fatal("Relay exposed Goal-only controls")
	}
	for _, action := range []string{"get_access", "set_access"} {
		if _, err := reg.tools["manage_project"].exec(ctx, map[string]any{"action": action, "owners": []any{"owner", "another"}}); err == nil {
			t.Fatal("Flow exposed owner/editor member lists")
		}
	}
	f.api.lastQueryRequests[session] = QueryRequest{userID: "owner", SelectedFolder: "Workflow/invoices"}
	if _, err := reg.tools["settings"].exec(ctx, map[string]any{"action": "get"}); err == nil {
		t.Fatal("Relay callback moved to a Goal")
	}
}
