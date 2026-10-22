package server

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// Drive real settings/project handlers. A hidden selector, a retained tool
// after another person's turn, or a Run token must not widen the Builder.
func TestWorkspacePanelChatToolsBindCurrentBuilder(t *testing.T) {
	f := newExternalToolsFixture(t)
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
		"manage_messaging": {"action": "status"}, "manage_pulse": {"action": "status"},
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
