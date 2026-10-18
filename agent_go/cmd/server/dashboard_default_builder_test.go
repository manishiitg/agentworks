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
	for _, origin := range []string{"work", "code"} {
		t.Run(origin, func(t *testing.T) {
			_, codeProfile := newCodePrivacyFixture(t)
			f, _ := newExternalCrewCommandFixture(t)
			if err := f.api.agentProfiles.RegisterProfile(codeProfile); err != nil {
				t.Fatal(err)
			}
			withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work","code"]},{"id":"other","role":"editor","products":["work","code"]}]}`)
			root := externalCommandCrewRoot
			if origin == "code" {
				root = codePrivacyOwnerRoot
				f.write(t, root+"/product.json", `{"product":"code","id":"c0de0001-0000","title":"Private Crew","session_id":"code:project:c0de0001-0000","mode":"dev"}`)
				f.write(t, root+"/workflow.json", `{"id":"c0de0001-0000","label":"Private Crew","created_by":"owner"}`)
			}
			list := func(claims *UserClaims) map[string]any {
				t.Helper()
				body, _ := json.Marshal(map[string]string{"workspace": root, "action": "list"})
				w := httptest.NewRecorder()
				f.api.handleDashboards(w, adminRequest(http.MethodPost, "/api/dashboards", string(body), claims, nil))
				return externalTestBody(t, w, http.StatusOK)
			}
			if origin == "work" {
				// A Run person and an owner's read connection can discover the
				// empty catalog without creating files in somebody's project.
				for _, claims := range []*UserClaims{
					{UserID: "other", Username: "other"},
					{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"dashboards:read"}, AllCrews: true}},
					{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"dashboards:read", "crews:write"}, AllCrews: true, FileGuard: &wf.FolderGuard{ReadPaths: []string{"db/reports"}}}},
				} {
					list(claims)
					stored, err := dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: root, Action: "list", IncludeDraft: true})
					if err != nil || len(stored.Dashboards) != 0 {
						t.Fatalf("read request wrote a default: %+v %v", stored, err)
					}
				}
			}
			owner := &UserClaims{UserID: "owner", Username: "owner"}
			if origin == "code" {
				f.write(t, root+"/product.json", `{"product":"code","id":"c0de0001-0000","title":"Private Crew","session_id":"code:project:c0de0001-0000","mode":"local"}`)
				list(owner)
				stored, err := dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: root, Action: "list", IncludeDraft: true})
				if err != nil || len(stored.Dashboards) != 0 {
					t.Fatalf("Local discovery wrote a server dashboard: %+v %v", stored, err)
				}
				f.write(t, root+"/product.json", `{"product":"code","id":"c0de0001-0000","title":"Private Crew","session_id":"code:project:c0de0001-0000","mode":"dev"}`)
				// A connection with existing Builder consent follows the same
				// service as the website, rather than a browser-only default.
				owner.AccessToken = &accesstokens.Token{Scopes: []string{"dashboards:read", "code:run"}}
			}
			got := list(owner)
			raw, _ := json.Marshal(got)
			if !strings.Contains(string(raw), "db/reports/managed/overview/index.html") {
				t.Fatalf("new Crew has no default dashboard: %v", got)
			}
			stored, err := dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: root, Action: "get", ID: defaultDashboardID, IncludeDraft: true})
			if err != nil || stored.Dashboard == nil || stored.Dashboard.Published == "" || stored.Snapshot == nil || !strings.Contains(stored.Snapshot.Files["index.html"], "data-report-chat-panel") {
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
