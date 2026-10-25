package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// The website and MCP catalog use the real workspace HTTP store. A fresh
// private Crew used to have no starting dashboard until it was shared.
func TestCrewBuilderCatalogCreatesDefaultDashboard(t *testing.T) {
	for _, origin := range []string{"work"} {
		t.Run(origin, func(t *testing.T) {
			f, _ := newExternalCrewCommandFixture(t)
			withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work","code"]},{"id":"other","role":"editor","products":["work","code"]}]}`)
			root := externalCommandCrewRoot
			list := func(claims *UserClaims) map[string]any {
				t.Helper()
				body, _ := json.Marshal(map[string]string{"workspace": root, "action": "list"})
				w := httptest.NewRecorder()
				f.api.handleDashboards(w, adminRequest(http.MethodPost, "/api/dashboards", string(body), claims, nil))
				return externalTestBody(t, w, http.StatusOK)
			}
			if origin == "work" {
				// External connections cannot create defaults through discovery.
				for _, claims := range []*UserClaims{
					{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"dashboards:read", "crews:read"}, AllCrews: true}},
					{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"dashboards:read", "crews:read", "crews:write"}, AllCrews: true, FileGuard: &wf.FolderGuard{ReadPaths: []string{"db/reports"}}}},
				} {
					list(claims)
					stored, err := dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: root, Action: "list", IncludeDraft: true})
					if err != nil || len(stored.Dashboards) != 0 {
						t.Fatalf("read request wrote a default: %+v %v", stored, err)
					}
				}
				// The app provisions its default on the server for a shared Run
				// person, without giving that person dashboard authoring access.
				got := list(&UserClaims{UserID: "other", Username: "other"})
				raw, _ := json.Marshal(got)
				if !strings.Contains(string(raw), "db/reports/managed/overview/index.html") {
					t.Fatalf("shared app has no server-provided default: %v", got)
				}
			}
			owner := &UserClaims{UserID: "owner", Username: "owner"}
			got := list(owner)
			raw, _ := json.Marshal(got)
			if !strings.Contains(string(raw), "db/reports/managed/overview/index.html") {
				t.Fatalf("new Crew has no default dashboard: %v", got)
			}
			stored, err := dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: root, Action: "get", ID: defaultDashboardID, IncludeDraft: true})
			if err != nil || stored.Dashboard == nil || stored.Dashboard.Published == "" || stored.Snapshot == nil || len(stored.Snapshot.Files) == 0 {
				t.Fatalf("default is not usable: %+v %v", stored, err)
			}
			revision := stored.Dashboard.Revision
			list(owner)
			again, err := dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: root, Action: "get", ID: defaultDashboardID, IncludeDraft: true})
			if err != nil || again.Dashboard.Revision != revision {
				t.Fatalf("catalog rewrote an existing dashboard: %+v %v", again, err)
			}
			// A later unfinished owner edit remains a draft, with no generated
			// replacement or publication caused by opening the catalog.
			draft, err := dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: root, Action: "update", ID: defaultDashboardID, ExpectedRevision: revision, Files: map[string]string{"index.html": "<html><title>Unfinished owner dashboard</title></html>"}, IncludeDraft: true})
			if err != nil {
				t.Fatal(err)
			}
			list(owner)
			again, err = dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: root, Action: "get", ID: defaultDashboardID, IncludeDraft: true})
			if err != nil || again.Dashboard.Revision != draft.Dashboard.Revision || again.Dashboard.Published != revision {
				t.Fatalf("catalog changed the owner's draft: %+v %v", again, err)
			}
		})
	}
}

// A private Goal opens with the same standard dashboard as a Crew. Opening it
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
