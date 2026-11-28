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

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

// Exercise the app/MCP share path against the real workspace HTTP handler and
// disk store. A project with no dashboard used to fail with a workspace 403
// because platform-generated mutations omitted the draft/write flag.
func TestDashboardShareCreatesAndPublishesStandardDashboard(t *testing.T) {
	// PLAT-746: Relay sharing retired on 8 Nov; retain the existing workflow sharing contract.
	for _, mode := range []string{runSharePeople, runShareEveryone} {
		t.Run(mode, func(t *testing.T) {
			f := newExternalToolsFixture(t)
			withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["agentworks"]},{"id":"reader","username":"reader","email":"reader@example.com","products":["agentworks"]}]}`)
			raw, _ := json.Marshal(WorkflowManifest{ID: "invoices", Label: "Invoices", CreatedBy: "owner", Access: &WorkflowAccess{Owners: []string{"owner"}}})
			f.write(t, "Workflow/invoices/workflow.json", string(raw))
			args := map[string]any{"mode": mode, "emails": []string{"reader@example.com"}}
			claims := &UserClaims{UserID: "owner", Username: "owner"}
			var w *httptest.ResponseRecorder
			if mode == runSharePeople {
				// MCP speaks general access (#4545): restricted with the people added.
				delete(args, "mode")
				args["action"], args["access"] = "set", "restricted"
				args["workflow_id"] = "invoices"
				w = f.call(t, "owner", "share", args)
			} else {
				args["workspace"], args["action"] = "Workflow/invoices", "share"
				body, _ := json.Marshal(args)
				w = httptest.NewRecorder()
				f.api.handleDashboards(w, adminRequest(http.MethodPost, "/api/dashboards", string(body), claims, nil))
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

// A readable connection never acquires authoring consent through Share.
func TestShareMCPKeepsExistingPermissions(t *testing.T) {
	f := newExternalToolsFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["agentworks"]},{"id":"editor","username":"editor","can_create":true,"products":["agentworks"]},{"id":"reader","username":"reader","email":"reader@example.com","products":["agentworks"]}]}`)
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoices","created_by":"owner","access":{"owners":["owner","editor"],"readers":["reader"]}}`)
	readToken := &accesstokens.Token{ID: "share-read", Scopes: []string{"dashboards:read"}, WorkflowIDs: []string{"invoices"}}
	writerToken := &accesstokens.Token{ID: "share-author", Scopes: []string{"dashboards:read", "workflows:read", "files:read", "runs:execute", "builder:chat"}, WorkflowIDs: []string{"invoices"}}
	call := func(name, user string, token *accesstokens.Token, args map[string]any, status int) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
		w := httptest.NewRecorder()
		f.api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(body), &UserClaims{UserID: user, Username: user, AccessToken: token}, nil))
		return externalTestBody(t, w, status)
	}
	call("share", "reader", readToken, map[string]any{"action": "get", "workflow_id": "invoices"}, 200)
	share, ok := externalCatalogTool("share")
	if !ok {
		t.Fatal("canonical sharing tool missing")
	}
	narrowed, ok := externalMergedForClaims(&UserClaims{UserID: "reader", Username: "reader", AccessToken: readToken}, share)
	if !ok {
		t.Fatal("read connection lost sharing inspection")
	}
	actions := narrowed.InputSchema["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]any)
	if len(actions) != 1 || actions[0] != "get" {
		t.Fatalf("read connection acquired a sharing mutation: %+v", actions)
	}
	// A read connection cannot share, and nor can someone who only uses the Goal (one sharing model, PLAT-856-393).
	for _, user := range []string{"owner", "reader"} {
		token := writerToken
		if user == "owner" {
			token = readToken
		}
		call("share", user, token, map[string]any{"action": "set", "workflow_id": "invoices", "access": "restricted"}, 403)
	}
	call("share", "owner", writerToken, map[string]any{"action": "set", "workflow_id": "invoices", "flow_id": "invoices", "access": "restricted"}, 400)
	call("share", "owner", writerToken, map[string]any{"action": "set", "workflow_id": "invoices", "access": "restricted", "emails": []string{"reader@example.com"}}, 200)
	call("share", "owner", writerToken, map[string]any{"action": "set", "workflow_id": "invoices", "access": "restricted"}, 200)
	state := call("share", "owner", readToken, map[string]any{"action": "get", "workflow_id": "invoices"}, 200)
	if state["mode"] != "private" {
		t.Fatalf("Share did not save audience: %+v", state)
	}
	call("share", "reader", readToken, map[string]any{"action": "get", "workflow_id": "invoices"}, 404)
}

func TestDashboardShareResumesStandardDraftButNeverPublishesAuthoredDraft(t *testing.T) {
	for _, authored := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "authored"}[authored], func(t *testing.T) {
			f := newExternalToolsFixture(t)
			page, pageErr := defaultDashboardHTML(defaultDashboardInfo{Kind: "workflow", Title: "Earlier title"})
			if pageErr != nil {
				t.Fatal(pageErr)
			}
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

// People added to a restricted project dashboard open its pages, never the project (#4545); nobody else opens either.
func TestPrivateCrewDashboardPeopleOpenOnlyItsPages(t *testing.T) {
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
	if !crewDashboardSharedWith(root, "ann") || crewSharedWith(root, "ann") {
		t.Fatal("a person added to the dashboard must open its pages and not the project")
	}
	if crewDashboardSharedWith(root, "bob") || crewSharedWith(root, "bob") {
		t.Fatal("anyone not added must stay out of a restricted dashboard")
	}
}

// PLAT-750-93: a server Crew's co-owners build it but cannot widen its audience or change who its co-owners are; only
// the main owner does both (owner, 21 Nov).
func TestDashboardShareRequiresMainOwnerNotCoOwner(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	if err := defaultProjectOwners().SetPrivate("work", "test-team", true); err != nil {
		t.Fatal(err)
	}
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work"]},{"id":"member","role":"editor","products":["work"]},{"id":"guest","email":"guest@example.com","role":"editor","products":["work"]}]}`)
	joinCrewTeam(t, "test-team", "member")
	member := &UserClaims{UserID: "member", Username: "member"}
	ref, ok := resolveCrewPath(context.Background(), "member", externalCommandCrewRoot)
	if !ok || crewAccessFor(member, ref) != crewAccessOwner {
		t.Fatal("fixture must admit the co-owner before testing sharing")
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	created, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: externalCommandCrewRoot, Action: "create", ID: "summary", Title: "Summary", IncludeDraft: true, Files: map[string]string{"index.html": "<p>Summary</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dashboardWorkspaceRequest(ctx, dashboards.Request{Root: externalCommandCrewRoot, Action: "publish", ID: "summary", IncludeDraft: true, ExpectedRevision: created.Dashboard.Revision}); err != nil {
		t.Fatal(err)
	}
	// Real dashboard dispatch, not the helper alone: the refusal must reach the
	// owner gate, not fail because the member lacks project authoring access.
	call := func(user string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		f.api.handleDashboards(w, adminRequest(http.MethodPost, "/api/dashboards", `{"action":"share","workspace":"`+externalCommandCrewRoot+`","mode":"everyone"}`, &UserClaims{UserID: user, Username: user}, nil))
		return w
	}
	w := call("member")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "Only the owner changes who can open these") {
		t.Fatalf("member share: %d %s", w.Code, w.Body.String())
	}
	if crewDashboardOpenToCompany(externalCommandCrewRoot) {
		t.Fatal("refused sharing changed the audience")
	}
	w = call("owner")
	if w.Code != http.StatusOK || !crewDashboardOpenToCompany(externalCommandCrewRoot) {
		t.Fatalf("owner share: %d %s", w.Code, w.Body.String())
	}
	owners := func(user, action string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		f.api.handleDashboards(w, adminRequest(http.MethodPost, "/api/dashboards", `{"action":"`+action+`","workspace":"`+externalCommandCrewRoot+`","emails":["guest@example.com"]}`, &UserClaims{UserID: user, Username: user}, nil))
		return w
	}
	if w := owners("member", "owner_add"); w.Code != http.StatusForbidden || crewIsCoOwner(externalCommandCrewRoot, "guest") {
		t.Fatalf("a co-owner added a co-owner: %d %s", w.Code, w.Body.String())
	}
	if w := owners("owner", "owner_add"); w.Code != http.StatusOK || !crewIsCoOwner(externalCommandCrewRoot, "guest") {
		t.Fatalf("owner add co-owner: %d %s", w.Code, w.Body.String())
	}
	if w := owners("owner", "owner_remove"); w.Code != http.StatusOK || crewIsCoOwner(externalCommandCrewRoot, "guest") {
		t.Fatalf("owner remove co-owner: %d %s", w.Code, w.Body.String())
	}
}
