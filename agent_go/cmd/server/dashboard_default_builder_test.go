package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Drive app discovery and MCP sharing through the real workspace HTTP/disk store.
// Merely listing a Crew must not write; only explicit dashboard consent may do so.
func TestCrewDashboardCreationRequiresShareConfirmation(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	root := externalCommandCrewRoot
	ctx := context.Background()
	empty := func() {
		t.Helper()
		stored, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "list", IncludeDraft: true})
		if err != nil || len(stored.Dashboards) != 0 {
			t.Fatalf("unexpected dashboard mutation: %+v %v", stored, err)
		}
	}
	for _, claims := range []*UserClaims{
		{UserID: "owner", Username: "owner"}, {UserID: "other", Username: "other"},
		{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"dashboards:read", "crews:read"}, AllCrews: true}},
		{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"dashboards:read", "crews:read", "crews:write"}, AllCrews: true, FileGuard: &wf.FolderGuard{ReadPaths: []string{"db/reports"}}}},
	} {
		w := httptest.NewRecorder()
		f.api.handleDashboards(w, adminRequest(http.MethodPost, "/api/dashboards", `{"workspace":"`+root+`","action":"list"}`, claims, nil))
		externalTestBody(t, w, http.StatusOK)
		empty()
	}
	if err := defaultProjectOwners().SetPrivate("work", "test-team", true); err != nil {
		t.Fatal(err)
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	w := httptest.NewRecorder()
	f.api.handleDashboards(w, adminRequest(http.MethodPost, "/api/dashboards", `{"workspace":"`+root+`","action":"share_status"}`, owner, nil))
	if state := externalTestBody(t, w, 200); state["needs_dashboard"] != true {
		t.Fatalf("missing creation choice: %+v", state)
	}
	w = httptest.NewRecorder()
	f.api.handleDashboards(w, adminRequest(http.MethodPost, "/api/dashboards", `{"workspace":"`+root+`","action":"share","mode":"everyone"}`, owner, nil))
	if state := externalTestBody(t, w, 409); state["dashboard_required"] != true {
		t.Fatalf("missing creation refusal: %+v", state)
	}
	empty()
	if crewSharedWithOthers(root) {
		t.Fatal("sharing changed before consent")
	}
	w = f.call(t, "owner", "share", map[string]any{"action": "set", "crew_id": "team", "mode": "everyone", "create_dashboard": true})
	if state := externalTestBody(t, w, 200); state["mode"] != "everyone" {
		t.Fatalf("share not saved: %+v", state)
	}
	stored, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "get", ID: defaultDashboardID, IncludeDraft: true})
	if err != nil || stored.Dashboard == nil || stored.Dashboard.Published == "" {
		t.Fatalf("confirmed dashboard not published: %+v %v", stored, err)
	}
	revision := stored.Dashboard.Revision
	// A later ordinary share reuses the existing dashboard.
	externalTestBody(t, f.call(t, "owner", "share", map[string]any{"action": "set", "crew_id": "team", "mode": "everyone"}), 200)
	again, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "get", ID: defaultDashboardID, IncludeDraft: true})
	if err != nil || again.Dashboard.Revision != revision {
		t.Fatalf("share rewrote dashboard: %+v %v", again, err)
	}
	externalTestBody(t, f.call(t, "other", "share", map[string]any{"action": "set", "crew_id": "team", "mode": "everyone", "create_dashboard": true}), 403)
}

// A private Goal keeps its standard dashboard. Opening it
// never shares the Goal or overwrites a dashboard the Builder already authored.
func TestGoalBuilderCatalogCreatesDefaultDashboard(t *testing.T) {
	f := newExternalToolsFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","can_create":true,"products":["agentworks"]}]}`)
	manifest, _ := json.Marshal(WorkflowManifest{ID: "invoices", Label: "Invoices", CreatedBy: "owner", Access: &WorkflowAccess{Owners: []string{"owner"}}})
	f.write(t, "Workflow/invoices/workflow.json", string(manifest))
	list := func() {
		t.Helper()
		w := httptest.NewRecorder()
		f.api.handleDashboards(w, adminRequest(http.MethodPost, "/api/dashboards", `{"workspace":"Workflow/invoices","action":"list"}`, &UserClaims{UserID: "owner", Username: "owner"}, nil))
		externalTestBody(t, w, http.StatusOK)
	}
	list()
	stored, err := dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: "Workflow/invoices", Action: "get", ID: defaultDashboardID, IncludeDraft: true})
	if err != nil || stored.Dashboard == nil || stored.Dashboard.Published == "" || stored.Snapshot == nil || len(stored.Snapshot.Files) == 0 {
		t.Fatalf("Goal has no usable default: %+v %v", stored, err)
	}
	revision := stored.Dashboard.Revision
	list()
	again, err := dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: "Workflow/invoices", Action: "get", ID: defaultDashboardID, IncludeDraft: true})
	if err != nil || again.Dashboard.Revision != revision {
		t.Fatalf("catalog rewrote default: %+v %v", again, err)
	}
	goal, found, err := ReadWorkflowManifest(context.Background(), "Workflow/invoices")
	if err != nil || !found || goal.Access == nil || goal.Access.RunEveryone || len(goal.Access.Readers) != 0 {
		t.Fatalf("opening the dashboard changed sharing: %+v %v", goal, err)
	}
}
