package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

// Exercise the app/MCP share path against the real workspace HTTP handler and
// disk store. A project with no dashboard used to fail with a workspace 403
// because platform-generated mutations omitted the draft/write flag.
func TestDashboardShareCreatesAndPublishesStandardDashboard(t *testing.T) {
	for _, mode := range []string{runSharePeople, runShareEveryone} {
		t.Run(mode, func(t *testing.T) {
			f := newExternalToolsFixture(t)
			withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["agentworks"]},{"id":"reader","username":"reader","email":"reader@example.com","products":["agentworks"]}]}`)
			f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoices","created_by":"owner","access":{"owners":["owner"]}}`)
			args := map[string]any{"mode": mode, "emails": []string{"reader@example.com"}}
			claims := &UserClaims{UserID: "owner", Username: "owner"}
			w := httptest.NewRecorder()
			if mode == runSharePeople {
				args["workflow_id"] = "invoices"
				f.api.externalDashboardCall(w, adminRequest(http.MethodPost, "/api/external/call", "", claims, nil), "share_dashboard", args)
			} else {
				args["workspace"], args["action"] = "Workflow/invoices", "share"
				raw, _ := json.Marshal(args)
				f.api.handleDashboards(w, adminRequest(http.MethodPost, "/api/dashboards", string(raw), claims, nil))
			}
			state := externalTestBody(t, w, 200)
			if state["mode"] != mode || state["note"] == "" {
				t.Fatalf("share should explain the generated dashboard: %v", state)
			}
			listed, err := dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: "Workflow/invoices", Action: "list"})
			if err != nil || len(listed.Dashboards) != 1 || listed.Dashboards[0].ID != defaultDashboardID || listed.Dashboards[0].Published == "" {
				t.Fatalf("share did not publish the standard dashboard: %+v, %v", listed, err)
			}
			manifest, found, err := ReadWorkflowManifest(context.Background(), "Workflow/invoices")
			if err != nil || !found || manifest == nil || workflowAccessForManifest(&UserClaims{UserID: "reader", Username: "reader"}, manifest) != WorkflowAccessRead {
				t.Fatalf("the shared person cannot use the project: %+v, %v", manifest, err)
			}
		})
	}
}

func TestDashboardShareResumesStandardDraftButNeverPublishesAuthoredDraft(t *testing.T) {
	for _, authored := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "authored"}[authored], func(t *testing.T) {
			f := newExternalToolsFixture(t)
			page := defaultDashboardHTML(defaultDashboardInfo{Kind: "workflow", Title: "Earlier title"})
			if authored {
				// Editors commonly keep the marker. The changed body must be
				// treated as authored, not overwritten just because it remains.
				page = strings.Replace(page, "Earlier title", "My unfinished dashboard", 1)
			}
			created, err := dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: "Workflow/invoices", Action: "create", ID: defaultDashboardID, Title: "Earlier title", Files: map[string]string{"index.html": page}, IncludeDraft: true})
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(previous) })
			w := httptest.NewRecorder()
			f.api.handleDashboards(w, adminRequest(http.MethodPost, "/api/dashboards", `{"action":"share","workspace":"Workflow/invoices","mode":"everyone"}`, &UserClaims{UserID: "owner", Username: "owner"}, nil))
			want := http.StatusOK
			if authored {
				want = http.StatusConflict
			}
			externalTestBody(t, w, want)
			current, err := dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: "Workflow/invoices", Action: "get", ID: defaultDashboardID, IncludeDraft: true})
			if err != nil {
				t.Fatal(err)
			}
			if authored {
				if current.Dashboard.Published != "" || current.Dashboard.Revision != created.Dashboard.Revision || current.Snapshot.Files["index.html"] != page {
					t.Fatal("sharing changed or published the owner's unfinished dashboard")
				}
				for _, part := range []string{`[RUN_SHARE] refused`, `target="invoices"`, `mode="everyone"`, `stage="dashboard"`, `status=409`, `reason="dashboard is not published"`} {
					if !strings.Contains(logs.String(), part) {
						t.Fatalf("refusal missing %s: %s", part, logs.String())
					}
				}
			} else if current.Dashboard.Published == "" || !defaultDashboardMarker.MatchString(current.Snapshot.Files["index.html"]) {
				t.Fatal("the platform draft was not resumed and published")
			}
		})
	}
}

// Sharing opens a product only when its owner says so: RunEveryone gives every
// account that may use the product reader access, never owner or editor, and a
// disabled account gets nothing. Without it a stranger has none (no change).
func TestWorkflowRunEveryoneGivesReaderOnlyWhenTheOwnerSharedIt(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"stranger","username":"stranger","products":["agentworks"]},{"id":"nolicense","username":"nolicense"},{"id":"gone","username":"gone","disabled":true}]}`)
	m := NewWorkflowManifest("Shared run")
	m.CreatedBy = "owner"
	m.Access = &WorkflowAccess{Owners: []string{"owner"}}
	if got := workflowAccessForManifest(&UserClaims{UserID: "stranger", Username: "stranger"}, m); got != WorkflowAccessNone {
		t.Fatalf("an unshared workflow must stay closed to a stranger, got %v", got)
	}
	m.Access.RunEveryone = true
	if got := workflowAccessForManifest(&UserClaims{UserID: "stranger", Username: "stranger"}, m); got != WorkflowAccessRead {
		t.Fatalf("a workflow shared with everyone is a reader for any account, got %v", got)
	}
	if got := workflowAccessForManifest(&UserClaims{UserID: "owner", Username: "owner"}, m); got != WorkflowAccessOwner {
		t.Fatalf("sharing must not demote the owner, got %v", got)
	}
	if got := workflowAccessForManifest(&UserClaims{UserID: "nolicense", Username: "nolicense"}, m); got != WorkflowAccessNone {
		t.Fatalf("an account without the product gets nothing, got %v", got)
	}
	if got := workflowAccessForManifest(&UserClaims{UserID: "gone", Username: "gone"}, m); got != WorkflowAccessNone {
		t.Fatalf("a disabled account gets nothing, got %v", got)
	}
}

// 'people' mode reports an email that matches no active account instead of dropping it.
func TestResolveShareEmailsReportsUnknownEmails(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[{"id":"u1","username":"ann","email":"ann@example.com"},{"id":"u2","username":"bob","email":"bob@example.com","disabled":true}]}`)
	dir, err := readUserDirectoryFile()
	if err != nil {
		t.Fatal(err)
	}
	people, unknown := resolveShareEmails(dir, []string{" Ann@Example.com ", "ann@example.com", "bob@example.com", "new@example.com"})
	if len(people) != 1 || people[0].ID != "u1" {
		t.Fatalf("only the active account matches, once: %+v", people)
	}
	if len(unknown) != 2 || unknown[0] != "bob@example.com" || unknown[1] != "new@example.com" {
		t.Fatalf("a disabled or missing account must be reported back: %v", unknown)
	}
}

// A private Crew shared with named people opens for them and for nobody else.
func TestPrivateCrewSharedWithNamedPeopleOpensOnlyForThem(t *testing.T) {
	registry := newTestRegistry(t)
	prev := defaultProjectOwnersOverride
	defaultProjectOwnersOverride = registry
	t.Cleanup(func() { defaultProjectOwnersOverride = prev })
	root := "Crew/demo-12345678"
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "demo-12345678", OwnerID: "owner", Shared: true}); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPrivate("work", "demo-12345678", true); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetRunPeople("work", "demo-12345678", []string{"ann"}); err != nil {
		t.Fatal(err)
	}
	if !crewSharedWith(root, "ann") {
		t.Fatal("a named person must open a private Crew shared with them")
	}
	if crewSharedWith(root, "bob") {
		t.Fatal("anyone not named must stay out of a private Crew")
	}
}
