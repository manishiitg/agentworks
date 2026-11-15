package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/workspace/chunks"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	"github.com/manishiitg/coding-agent-loop/workspace/teamonline"
)

// PLAT-746: discovering a shared dashboard must not expose another Crew or
// initialize a tracking database. Existing metric calls must never write it.
func TestCrewDashboardDiscoveryKeepsProjectAccessWithoutTrackingStore(t *testing.T) {
	svc, files, ctx := newCrewCreationTestEnv(t)
	docs, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Progress", Purpose: "Review progress", IdempotencyKey: "dashboard-access"})
	if err != nil {
		t.Fatal(err)
	}
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"},{"id":"runner","username":"runner","role":"creator"},{"id":"stranger","username":"stranger","role":"creator"}]}`)
	for _, binding := range []crewExecutableBinding{
		{ID: "report", CrewID: crew.CrewID, CrewRoot: crew.WorkspacePath, Root: "Workflow/report", Kind: "script"},
		{ID: "foreign", CrewID: "another", CrewRoot: "Crew/another", Root: "Workflow/foreign", Kind: "script"},
	} {
		if err := registerCrewExecutable(binding); err != nil {
			t.Fatal(err)
		}
		files.files[binding.Root+"/workflow.json"] = `{"id":"` + binding.ID + `","label":"Progress report"}`
		files.files[binding.Root+"/db/reports/index.html"] = "<h1>Progress</h1>"
	}
	api := &StreamingAPI{projectActions: svc}
	call := func(user string, want int) string {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/crew-dashboards?crew_id="+crew.CrewID, nil).WithContext(internalBotRequestContext(t.Context(), user))
		response := httptest.NewRecorder()
		api.handleCrewDashboards(response, request)
		if response.Code != want {
			t.Fatalf("%s: %d %s", user, response.Code, response.Body.String())
		}
		return response.Body.String()
	}
	body := call("owner", 200)
	if !strings.Contains(body, "Workflow/report") || strings.Contains(body, "foreign") {
		t.Fatal("incorrect admitted catalog", body)
	}
	call("stranger", 403)
	identity, _ := projectIdentityOf(crew.WorkspacePath)
	if err := defaultProjectOwners().modify(func(records map[string]projectOwnerRecord) (bool, error) {
		record := records[identity.Product+"/"+identity.Folder]
		record.Private = true
		record.AudienceConfigured = true
		record.RunPeople = []string{"runner"}
		records[identity.Product+"/"+identity.Folder] = record
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	resetCrewOwnerCache()
	call("runner", 200)
	call("stranger", 403)
	private, err := crewroot.PrivateRoot(docs, crew.CrewID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(private, "db/db.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("discovery created a tracking store: %v", err)
	}
}

func TestWorkflowMeasurementReadRefusesCrewRootsAndCreatesNothing(t *testing.T) {
	svc, files, ctx := newCrewCreationTestEnv(t)
	docs, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Data", Purpose: "Record progress", IdempotencyKey: "tracking-data"})
	if err != nil {
		t.Fatal(err)
	}
	root := "Workflow/report"
	binding := crewExecutableBinding{ID: "report", CrewID: crew.CrewID, CrewRoot: crew.WorkspacePath, Root: root, Kind: "script"}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	files.files[root+"/workflow.json"] = `{"id":"report","label":"Progress"}`
	ledger, err := step.LoadGoalMeasurements(ctx, root, 10)
	if err != nil || len(ledger.Observations) != 0 {
		t.Fatalf("workflow data: %+v %v", ledger, err)
	}
	if _, err := step.LoadGoalMeasurements(ctx, crew.WorkspacePath, 10); err == nil {
		t.Fatal("Crew-root read was accepted")
	}
	private, err := crewroot.PrivateRoot(docs, crew.CrewID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(private, "db/db.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("reader created Crew DB: %v", err)
	}
}

// PLAT-746-1023: namespaced asset rewriting must not corrupt longer paths.
func TestTeamOnlineAssetRewriteMatchesWholePathTokens(t *testing.T) {
	input := `<img src="db/assets/x.png"><img src="db/assets/x.png?v=1#preview"> url(db/assets/x.png) db/assets/x.png.bak other/db/assets/x.png`
	got := rewriteTeamOnlineAssetToken(input, "db/assets/x.png", "db/assets/item/x.png")
	want := `<img src="db/assets/item/x.png"><img src="db/assets/item/x.png?v=1#preview"> url(db/assets/item/x.png) db/assets/x.png.bak other/db/assets/x.png`
	if got != want {
		t.Fatalf("asset rewrite changed nonmatching paths: %s", got)
	}
	// The authenticated Mac exporter makes source-qualified paths relative;
	// the receiving server can then namespace them without any source root.
	qualified := `<img src="Crew/source/db/assets/x.png?v=1"> Crew/source/db/assets/x.png.bak`
	relative := rewriteTeamOnlineAssetToken(qualified, "Crew/source/db/assets/x.png", "db/assets/x.png")
	got = rewriteTeamOnlineAssetToken(relative, "db/assets/x.png", "db/assets/item/x.png")
	if got != `<img src="db/assets/item/x.png?v=1"> Crew/source/db/assets/x.png.bak` {
		t.Fatalf("Mac asset reference kept its unavailable source workspace: %s", got)
	}
}

// PLAT-746-1023: a validated Mac source needs no server-local source root,
// but an admission for another person, item, submission or root grants nothing.
func TestTeamOnlineReceiveRequiresBoundSourceAdmissionNotServerSourceRoot(t *testing.T) {
	folder := t.TempDir()
	root, err := os.OpenRoot(folder)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	otherRoot, err := os.OpenRoot(folder)
	if err != nil {
		t.Fatal(err)
	}
	defer otherRoot.Close()
	source := triggerTarget{CrewOwner: "owner", CrewID: "source", Path: "Crew/not-on-this-server"}
	ref := teamOnlineDashboardRef{ItemID: "report", Kind: "artifact", Revision: strings.Repeat("a", 64), SubmissionID: "submission", Package: root}
	item := teamonline.Item{ID: ref.ItemID, Revision: ref.Revision, Snapshot: dashboards.Snapshot{Title: "Report", Artifact: true, Files: map[string]string{"index.html": "<h1>Report</h1>"}}}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(folder, teamonline.Descriptor), data, 0600); err != nil {
		t.Fatal(err)
	}
	ref.SourceAdmission = &teamOnlineDashboardSourceAdmission{person: "owner", owner: "owner", sourceProjectID: source.CrewID, kind: ref.Kind, itemID: ref.ItemID, revision: ref.Revision, submissionID: ref.SubmissionID, packageRoot: root}
	api := &StreamingAPI{}
	for _, tc := range []struct {
		name   string
		change func(*teamOnlineDashboardRef)
	}{
		{"absent", func(r *teamOnlineDashboardRef) { r.SourceAdmission = nil }},
		{"person", func(r *teamOnlineDashboardRef) { r.SourceAdmission.person = "other" }},
		{"owner", func(r *teamOnlineDashboardRef) { r.SourceAdmission.owner = "other" }},
		{"source", func(r *teamOnlineDashboardRef) { r.SourceAdmission.sourceProjectID = "other" }},
		{"kind", func(r *teamOnlineDashboardRef) { r.SourceAdmission.kind = "dashboard" }},
		{"item", func(r *teamOnlineDashboardRef) { r.SourceAdmission.itemID = "other" }},
		{"revision", func(r *teamOnlineDashboardRef) { r.SourceAdmission.revision = strings.Repeat("b", 64) }},
		{"submission", func(r *teamOnlineDashboardRef) { r.SourceAdmission.submissionID = "other" }},
		{"package", func(r *teamOnlineDashboardRef) { r.Package = otherRoot }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempt, admission := ref, *ref.SourceAdmission
			attempt.SourceAdmission = &admission
			tc.change(&attempt)
			if _, err := api.receiveTeamOnlineDashboard(internalBotRequestContext(t.Context(), "owner"), source, triggerTarget{}, attempt, true); err == nil || !strings.Contains(err.Error(), "source item is unavailable") {
				t.Fatalf("mismatched source capability was not refused: %v", err)
			}
		})
	}
	out, err := api.receiveTeamOnlineDashboard(internalBotRequestContext(t.Context(), "owner"), source, triggerTarget{}, ref, true)
	if err != nil || out.DestinationItemID != teamOnlineDashboardItemID(source.CrewID, ref.Kind, ref.ItemID) {
		t.Fatalf("validated Mac source required a server source path: %+v %v", out, err)
	}
}

// PLAT-746-1023: encoded JSON strings must not bypass the common secret scan;
// credential paths are refused before staging or reading any source computer.
func TestTeamOnlinePackageRejectsCredentialPathsAndDecodedSecretValues(t *testing.T) {
	const value = "credential\nbytes"
	contains := func(data []byte) bool { return strings.Contains(string(data), value) }
	var decoded any
	if err := json.Unmarshal([]byte(`{"instructions":"credential\nbytes","nested":[{"safe":"reference by name"}]}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if !teamOnlineDecodedContainsSecret(decoded, contains) {
		t.Fatal("escaped credential was admitted")
	}
	if teamOnlineDecodedContainsSecret(map[string]any{"instructions": "use SECRET_NAME"}, contains) {
		t.Fatal("secret names must remain portable")
	}
	for _, name := range []string{"../private", ".env", "keys/token.json"} {
		_, err := receiveTeamOnlinePackage("never-staged", teamOnlinePackageManifest{Files: map[string]chunks.File{name: {Hash: strings.Repeat("a", 64), Chunks: []chunks.Chunk{}}}}, func([]string) ([]byte, error) { t.Fatal("refused path reached source transport"); return nil, nil })
		if err == nil {
			t.Fatalf("credential/escape path %q admitted", name)
		}
	}
}

// PLAT-746-1023: a standalone routine hand-off belongs to the current Team,
// not to an arbitrary other Crew the member owns. Dashboard dependencies are
// admitted separately from their actual source-owned dependency preview.
func TestTeamOnlineAutomationRefusesNonTeamSource(t *testing.T) {
	in := teamOnlineRequest{TeamProjectID: "team", SourceProjectID: "other-owned-crew", Kind: "automation", ItemID: "weekly", Revision: strings.Repeat("a", 64), SubmissionID: "move-weekly"}
	if err := in.validate(); err == nil {
		t.Fatal("unrelated Crew routine admitted")
	}
	in.SourceProjectID = in.TeamProjectID
	if err := in.validate(); err != nil {
		t.Fatalf("Team routine refused: %v", err)
	}
	in.Kind = "dashboard"
	in.SourceProjectID = "other-owned-crew"
	if err := in.validate(); err != nil {
		t.Fatalf("owned dashboard source refused: %v", err)
	}
}

// PLAT-746-1023: a Mac-source selector resolves only the accepted Team
// publication. Its short link opens the home workspace for the normal report
// runtime, never the source DB; losing membership defeats even a broad Share.
func TestTeamOnlineSourceOpenUsesAcceptedHomeAndRechecksMembership(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f := newExternalToolsFixture(t)
	withProjectOwnerRegistry(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"reader","username":"reader","can_create":true,"products":["work"]},{"id":"outsider","username":"outsider","can_create":true,"products":["work"]}]}`)
	stubCrewLookups(t, map[string]string{"Crew/dashboard-team": "owner", "Crew/source-team": "owner"}, nil)
	home := registerTestCrew(t, "dashboard-team", "owner", true, "dashboard-team")
	registerTestCrew(t, "source-team", "owner", false, "source-team")
	f.write(t, home+"/.agentworks/settings/product.json", `{"id":"dashboard-team","product":"work","title":"Team"}`)
	team := crewTeam{RegistryVersion: 1, ProjectID: "source-team", OwnerID: "owner", Name: "Example", On: true, OnlineHome: "dashboard-team", People: []crewTeamPerson{{UserID: "reader"}}}
	// Seed an already accepted home record; lifecycle provisioning is covered
	// separately. No virtual identity or private source root is needed here.
	teamDir, err := crewTeamDir(team.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(teamDir, 0700); err != nil {
		t.Fatal(err)
	}
	saveRoster := func() {
		t.Helper()
		raw, err := json.Marshal(team)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(teamDir, "team.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	saveRoster()
	item := teamOnlineDashboardItemID("mac-source", "dashboard", "sales")
	ctx := internalBotRequestContext(t.Context(), "reader")
	created, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: home, Action: "create", ID: item, Title: "Sales", Files: map[string]string{"index.html": "<html><title>Sales</title><body>Team data</body></html>"}, IncludeDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	published, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: home, Action: "publish", ID: item, ExpectedRevision: created.Dashboard.Revision, IncludeDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := saveTeamOnlineMappings(ctx, triggerTarget{CrewID: "dashboard-team"}, []teamOnlineItemMapping{{SourceProjectID: "mac-source", Kind: "dashboard", ItemID: "sales", DestinationProjectID: "dashboard-team", DestinationItemID: item, Revision: published.Dashboard.Published}}); err != nil {
		t.Fatal(err)
	}
	open := func(user string) (int, teamOnlineOpenResult) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/dashboards", strings.NewReader(`{"action":"online_location","source_project_id":"mac-source","document_path":"db/reports/managed/sales/index.html"}`)).WithContext(internalBotRequestContext(t.Context(), user))
		rec := httptest.NewRecorder()
		f.api.handleDashboards(rec, req)
		var out teamOnlineOpenResult
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	// Unconfigured Team-state directories must not break unrelated Open.
	unused, err := crewTeamDir("not-configured")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(unused, 0700); err != nil {
		t.Fatal(err)
	}
	status, out := open("reader")
	if status != http.StatusOK || !out.Found || out.Location == nil || out.Location.Workspace != home || out.Location.ItemID != item || !strings.HasPrefix(out.ShortPath, "/d/") {
		t.Fatalf("accepted source Open = %d %+v", status, out)
	}
	// The real short-link handler binds every report operation to the Team home.
	req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx), map[string]string{"code": strings.TrimPrefix(out.ShortPath, "/d/")})
	rec := httptest.NewRecorder()
	f.api.handleDashboardByCode(rec, req)
	var resolved map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resolved)
	if rec.Code != http.StatusOK || resolved["root"] != home || resolved["document_path"] != "db/reports/managed/"+item+"/index.html" {
		t.Fatalf("destination runtime = %d %s", rec.Code, rec.Body.String())
	}
	team.People = nil
	saveRoster()
	if status, out := open("reader"); status != http.StatusOK || out.Found {
		t.Fatalf("leaver retained source mapping: %d %+v", status, out)
	}
	if status, out := open("outsider"); status != http.StatusOK || out.Found {
		t.Fatalf("outsider discovered source mapping: %d %+v", status, out)
	}

	// A transport failure alone permits the person's existing local file
	// access; a real server refusal (including an unavailable service reply)
	// must remain a refusal. Exercise the actual remote HTTP boundary.
	for _, status := range []int{0, http.StatusForbidden, http.StatusServiceUnavailable} {
		remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if status == 0 {
				connection, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					connection.Close()
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"team_refused","message":"Team access ended"}}`))
		}))
		var value teamOnlineOpenResult
		err := crewTeamRemoteJSON(ctx, "reader", desktopConnection{Server: remote.URL}, http.MethodPost, "/api/dashboards", map[string]string{"action": "online_location"}, &value)
		remote.Close()
		if err == nil {
			t.Fatalf("expected failed remote read, status=%d", status)
		}
		local, failure := teamOnlineOpenFailure(ctx, "mac-source", err)
		if status == 0 {
			if failure != nil || local.Found || local.Notice != teamOnlineUnreachableNotice {
				t.Fatalf("offline local Open: %+v %v", local, failure)
			}
		} else if failure == nil || local.Notice != "" {
			t.Fatalf("server refusal became a local fallback: status=%d %+v %v", status, local, failure)
		}
	}
}

// PLAT-746-1023: only a finished Artifact without live inputs skips the agent
// agreement. Adding an Artifact label must not silently move feeding routines.
func TestAddToTeamStaticArtifactCannotSkipLiveDependencyAgreement(t *testing.T) {
	for _, tc := range []struct {
		name     string
		artifact bool
		files    map[string]string
		direct   bool
	}{
		{"finished", true, map[string]string{"index.html": "<h1>Proposal</h1>"}, true},
		{"dashboard", false, map[string]string{"index.html": "<h1>Orders</h1>"}, false},
		{"live data", true, map[string]string{"online.json": `{"dataset":{"tables":["orders"]}}`}, false},
		{"updates", true, map[string]string{"online.json": `{"automations":[{"id":"daily","name":"Daily"}]}`}, false},
		{"button", true, map[string]string{"online.json": `{"functions":["ask"]}`}, false},
		{"script", true, map[string]string{"scripts/read.py": "print('data')"}, false},
		{"malformed manifest", true, map[string]string{"online.json": "not JSON"}, false},
		{"unknown live dependency", true, map[string]string{"online.json": `{"dataset":{"table":"orders"}}`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			direct := teamDashboardStaticArtifact(dashboards.Snapshot{Artifact: tc.artifact, Files: tc.files})
			if direct != tc.direct {
				t.Fatalf("direct=%v", direct)
			}
		})
	}
}

// PLAT-746-1023: use the real dashboard request path. An owned live item
// prepares a source-agent task without copying anything; shared Run access
// must never be enough to arrange this owner's Team handoff.
func TestAddToTeamRequestKeepsSourceOwnerAndUsesPublishedItem(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f := newExternalToolsFixture(t)
	withProjectOwnerRegistry(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"reader","username":"reader","can_create":true,"products":["work"]}]}`)
	stubCrewLookups(t, map[string]string{"Crew/dashboard-team": "owner"}, nil)
	root := registerTestCrew(t, "dashboard-team", "owner", true, "add-team-source")
	f.write(t, root+"/.agentworks/settings/product.json", `{"id":"add-team-source","product":"work","title":"Source"}`)
	team := crewTeam{RegistryVersion: 1, ProjectID: "add-team-source", OwnerID: "owner", Name: "Example", On: true, People: []crewTeamPerson{{UserID: "reader"}}}
	dir, err := crewTeamDir(team.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(team)
	if err = os.WriteFile(filepath.Join(dir, "team.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	ctx := internalBotRequestContext(t.Context(), "owner")
	created, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "create", ID: "orders", Title: "Orders", Files: map[string]string{"index.html": "<h1>Orders</h1>"}, IncludeDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "publish", ID: "orders", ExpectedRevision: created.Dashboard.Revision, IncludeDraft: true}); err != nil {
		t.Fatal(err)
	}
	call := func(user string) (int, teamDashboardStart) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"action": "add_to_team", "workspace": root, "dashboard_id": "orders"})
		req := httptest.NewRequest(http.MethodPost, "/api/dashboards", strings.NewReader(string(body))).WithContext(internalBotRequestContext(t.Context(), user))
		rec := httptest.NewRecorder()
		f.api.handleDashboards(rec, req)
		if user == "owner" && rec.Code != http.StatusOK {
			t.Fatalf("dashboard request failed: %d %s", rec.Code, rec.Body.String())
		}
		var out teamDashboardStart
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	status, out := call("owner")
	if status != 200 || out.ProjectID != team.ProjectID || !strings.Contains(out.Prompt, "AgentBridge") || !strings.Contains(out.Prompt, "orders") || out.Message != "" {
		t.Fatalf("owned task = %d %+v", status, out)
	}
	if status, out = call("reader"); status == 200 || out.Prompt != "" {
		t.Fatalf("other member arranged owner's handoff: %d %+v", status, out)
	}
	for _, scoped := range []*UserClaims{
		{UserID: "owner", Scope: "report-preview"},
		{UserID: "owner", AccessToken: &accesstokens.Token{AllCrews: true, Scopes: []string{"crews:run", "dashboards:read"}}},
	} {
		if _, err := f.api.addDashboardToTeam(context.WithValue(t.Context(), UserContextKey, scoped), root, "orders"); err == nil {
			t.Fatal("Run or preview claim began an owner handoff")
		}
	}
	scopedWriter := crewWriter("owner")
	scopedWriter.Scope = "mcp"
	scopedWriter.AccessToken.Scopes = append(scopedWriter.AccessToken.Scopes, "dashboards:read")
	if prepared, err := f.api.addDashboardToTeam(context.WithValue(t.Context(), UserContextKey, scopedWriter), root, "orders"); err != nil || prepared.Prompt == "" {
		t.Fatalf("scoped Builder refused: %+v %v", prepared, err)
	}
	// Exercise the actual consolidated MCP catalog too, not merely the native
	// schema. A screen action must also reach this same guarded handler in chat.
	// This fixture exposes workspace handlers, not a profile-binding service.
	mcpContext, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	writer := crewWriter("owner")
	writer.AccessToken.Scopes = append(writer.AccessToken.Scopes, "dashboards:read")
	srv := serveExternalMCP(t, f.api, writer)
	cli := dialExternalMCP(t, mcpContext, srv.URL+externalMCPPath)
	initializeExternalMCP(t, mcpContext, cli)
	result := callRemoteTool(t, mcpContext, cli, "call_tool", map[string]any{"name": "dashboard", "arguments": map[string]any{"action": "add_to_team", "workspace": root, "dashboard_id": "orders"}})
	requireRemoteSuccess(t, result, "dashboard")
	if !strings.Contains(marshalStructured(t, result), "AgentBridge") {
		t.Fatal("MCP omitted the source-agent task")
	}
}
