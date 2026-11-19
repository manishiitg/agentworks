package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
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
	var trace bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&trace)
	defer log.SetOutput(previous)
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
	if !strings.Contains(trace.String(), "step=source_admission item=report person=owner source=source destination= outcome=failed") || !strings.Contains(trace.String(), "step=dataset_preflight item=report person=owner source=source destination= outcome=ok") {
		t.Fatalf("missing per-stage refusal/success diagnostics: %s", trace.String())
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
	t.Setenv("PUBLIC_URL", "https://example.com")
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
	// Serve both real Crew manifests in this fixture, not just its historical
	// hard-coded dashboard-team path. The actual dashboard handlers stay real.
	f.write(t, "Crew/source-team/.agentworks/settings/product.json", `{"id":"source-team","product":"work","title":"Source"}`)
	upstream, err := url.Parse(os.Getenv("WORKSPACE_API_URL"))
	if err != nil {
		t.Fatal(err)
	}
	forward := httputil.NewSingleHostReverseProxy(upstream)
	documents := gin.New()
	documents.GET("/api/documents/*filepath", workspacehandlers.GetDocument)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/documents/") {
			documents.ServeHTTP(w, r)
			return
		}
		forward.ServeHTTP(w, r)
	}))
	defer proxy.Close()
	t.Setenv("WORKSPACE_API_URL", proxy.URL)
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
	// Real reported failure: the source-root list was empty after acceptance
	// into the online home. Include direct publications and accepted home items
	// once each, but never an unrelated private page in that home.
	publishExtra := func(root, id, title string, artifact bool) dashboards.Result {
		t.Helper()
		draft, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "create", ID: id, Title: title, Artifact: &artifact, Files: map[string]string{"index.html": "<h1>" + title + "</h1>"}, IncludeDraft: true})
		if err != nil {
			t.Fatal(err)
		}
		pub, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "publish", ID: id, ExpectedRevision: draft.Dashboard.Revision, IncludeDraft: true})
		if err != nil {
			t.Fatal(err)
		}
		return pub
	}
	publishExtra("Crew/source-team", "overview", "Overview", false)
	proposalID := teamOnlineDashboardItemID("mac-source", "artifact", "proposal")
	proposal := publishExtra(home, proposalID, "Proposal", true)
	publishExtra(home, "private-helper", "Private helper", true)
	mappings, _ := readTeamOnlineMappings(ctx, triggerTarget{CrewID: "dashboard-team"})
	mappings = append(mappings, teamOnlineItemMapping{SourceProjectID: "mac-source", Kind: "artifact", ItemID: "proposal", DestinationProjectID: "dashboard-team", DestinationItemID: proposalID, Revision: proposal.Dashboard.Published})
	if err := saveTeamOnlineMappings(ctx, triggerTarget{CrewID: "dashboard-team"}, mappings); err != nil {
		t.Fatal(err)
	}
	readTab := func(mcp bool) crewDashboardsTab {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		if mcp {
			f.api.externalCrewTeamSyncCall(rec, req, map[string]any{"crew_id": team.ProjectID, "action": "dashboards"})
		} else {
			f.api.handleCrewTeamDashboardsTab(rec, mux.SetURLVars(req, map[string]string{"project": team.ProjectID}))
		}
		var tab crewDashboardsTab
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &tab) != nil {
			t.Fatalf("Team list: %d %s", rec.Code, rec.Body.String())
		}
		return tab
	}
	for _, mcp := range []bool{false, true} {
		tab := readTab(mcp)
		if len(tab.Items) != 3 || tab.Filters[0].Count != 3 {
			t.Fatalf("union/counts: %+v", tab)
		}
		seen := map[string]bool{}
		for _, row := range tab.Items {
			if seen[row.ID] || row.Title == "Private helper" {
				t.Fatalf("duplicate/private row: %+v", row)
			}
			seen[row.ID] = true
			if row.ID == proposalID && (row.Kind != "artifact" || row.Workspace != home || row.Online == nil || !strings.Contains(row.Line, "On the Team")) {
				t.Fatalf("mapped Artifact: %+v", row)
			}
		}
	}
	// A deleted destination must not hide the source or other accepted items.
	mappings = append(mappings, teamOnlineItemMapping{SourceProjectID: "mac-source", Kind: "dashboard", ItemID: "deleted", DestinationProjectID: "dashboard-team", DestinationItemID: teamOnlineDashboardItemID("mac-source", "dashboard", "deleted"), Revision: published.Dashboard.Published})
	if err := saveTeamOnlineMappings(ctx, triggerTarget{CrewID: "dashboard-team"}, mappings); err != nil {
		t.Fatal(err)
	}
	if tab := readTab(false); len(tab.Items) != 3 || tab.OnlineUnavailable {
		t.Fatalf("stale item blanked list: %+v", tab)
	}
	mappingPath, err := teamOnlineMappingsPath(triggerTarget{CrewID: "dashboard-team"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(mappingPath, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mcp := range []bool{false, true} {
		tab := readTab(mcp)
		if !tab.OnlineUnavailable || len(tab.Items) != 1 || tab.Items[0].Title != "Overview" {
			t.Fatalf("mapping failure hid source or claimed complete list: %+v", tab)
		}
	}
	if err := saveTeamOnlineMappings(ctx, triggerTarget{CrewID: "dashboard-team"}, mappings); err != nil {
		t.Fatal(err)
	}
	// A mapped-row click selects this Team explicitly and reuses normal Open.
	chosen, err := f.api.teamOnlineMappedOpen(ctx, team.ProjectID, "mac-source", "artifact", "proposal")
	if err != nil || !chosen.Found || chosen.Location.Workspace != home {
		t.Fatalf("mapped row Open: %+v %v", chosen, err)
	}
	// Live regression after the list fix: a real MCP get/Open must consume the
	// returned online selector, not look up the destination ID in the source.
	readerClaims := &UserClaims{UserID: "reader", Username: "reader", AccessToken: &accesstokens.Token{ID: "mapped-read", Scopes: []string{"dashboards:read", "crews:read"}, CrewIDs: []string{team.ProjectID, "dashboard-team"}}}
	mcpCtx, cancelMCP := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancelMCP()
	mcpServer := serveExternalMCP(t, f.api, readerClaims)
	mcpClient := dialExternalMCP(t, mcpCtx, mcpServer.URL+externalMCPPath)
	initializeExternalMCP(t, mcpCtx, mcpClient)
	selector := map[string]any{"team_project_id": team.ProjectID, "source_project_id": "mac-source", "item_kind": "artifact", "dashboard_id": "proposal"}
	mappedCall := func(action string, change map[string]any) *mcp.CallToolResult {
		t.Helper()
		args := map[string]any{"action": action}
		for k, v := range selector {
			args[k] = v
		}
		for k, v := range change {
			args[k] = v
		}
		return callRemoteTool(t, mcpCtx, mcpClient, "call_tool", map[string]any{"name": "dashboard", "arguments": args})
	}
	got := mappedCall("get", nil)
	requireRemoteSuccess(t, got, "mapped publication get")
	var read struct {
		dashboards.Result
		ResourceID string `json:"resource_id"`
	}
	if err := json.Unmarshal([]byte(marshalStructured(t, got)), &read); err != nil || read.Dashboard == nil || read.Dashboard.ID != proposalID || read.Snapshot == nil || !strings.Contains(read.Snapshot.Files["index.html"], "Proposal") || read.ResourceID == "" {
		t.Fatalf("MCP read did not reach Team publication: %s %v", marshalStructured(t, got), err)
	}
	location := mappedCall("online_location", nil)
	requireRemoteSuccess(t, location, "mapped online_location")
	var mcpLocation teamOnlineOpenResult
	if err := json.Unmarshal([]byte(marshalStructured(t, location)), &mcpLocation); err != nil || !mcpLocation.Found || mcpLocation.Location == nil || mcpLocation.Location.Workspace != home || mcpLocation.ResourceID == "" {
		t.Fatalf("MCP location: %s %v", marshalStructured(t, location), err)
	}
	opened := callRemoteTool(t, mcpCtx, mcpClient, "open_dashboard", selector)
	requireRemoteSuccess(t, opened, "mapped inline Open")
	// An existing source page with no accepted mapping is not a fallback.
	if bad := mappedCall("get", map[string]any{"dashboard_id": "overview"}); !bad.IsError {
		t.Fatal("missing mapping fell back to source")
	}
	if bad := mappedCall("get", map[string]any{"workspace": "Crew/source-team"}); !bad.IsError {
		t.Fatal("mixed project and online selectors accepted")
	}
	if bad := mappedCall("get", map[string]any{"revision": strings.Repeat("a", 64)}); !bad.IsError {
		t.Fatal("online selector read a non-publication revision")
	}
	for _, scope := range []string{"mcp", "report-preview"} {
		copy := *readerClaims
		copy.Scope = scope
		_, err := f.api.teamOnlineMappedOpen(context.WithValue(ctx, UserContextKey, &copy), team.ProjectID, "mac-source", "artifact", "proposal")
		if (scope == "mcp") != (err == nil) {
			t.Fatalf("mapped purpose boundary %s: %v", scope, err)
		}
		app := f.api.openDashboardApp(httptest.NewRequest(http.MethodPost, "/", nil).WithContext(context.WithValue(ctx, UserContextKey, &copy)), selector)
		if (scope == "mcp") == app.IsError {
			t.Fatalf("full inline purpose boundary %s: %+v", scope, app)
		}
	}
	if _, err := f.api.teamOnlineMappedOpen(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "reader", Scope: "mcp"}), team.ProjectID, "mac-source", "artifact", "proposal"); err == nil {
		t.Fatal("MCP purpose without validated token read Team item")
	}
	limited := *readerClaims
	token := *readerClaims.AccessToken
	token.CrewIDs = []string{team.ProjectID}
	limited.AccessToken = &token
	if _, err := f.api.teamOnlineMappedOpen(context.WithValue(ctx, UserContextKey, &limited), team.ProjectID, "mac-source", "artifact", "proposal"); err == nil {
		t.Fatal("source-only token read destination outside its Crew consent")
	}
	// Failed handoffs need a useful error, not just a generic reason code.
	var recorded bytes.Buffer
	before := log.Writer()
	log.SetOutput(&recorded)
	_, refused := f.api.receiveTeamOnline(internalBotRequestContext(t.Context(), "outsider"), teamOnlineRequest{TeamProjectID: team.ProjectID, SourceProjectID: "mac-source", Kind: "artifact", ItemID: "proposal", Revision: proposal.Dashboard.Published, SubmissionID: "refused-log", Preview: true})
	log.SetOutput(before)
	if refused == nil || !strings.Contains(recorded.String(), "error=") || !strings.Contains(recorded.String(), refused.Error()) {
		t.Fatalf("missing real failure reason: %v %s", refused, recorded.String())
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
	if revoked := mappedCall("get", nil); !revoked.IsError {
		t.Fatal("revoked member retained mapped MCP get")
	}
	if revoked := callRemoteTool(t, mcpCtx, mcpClient, "open_dashboard", selector); !revoked.IsError {
		t.Fatal("revoked member retained mapped MCP Open")
	}

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

// PLAT-746-1023: real preview/apply moves clocks and Watchers into the virtual
// home, but a webhook receives fresh Team authentication and never stops its source.
func TestTeamOnlineAutomationHandoffKeepsWatcherTargetsBound(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AUTH_SECRET", "automation-handoff-fixture")
	t.Setenv("PUBLIC_URL", "https://example.com")
	f.api.projectActions = NewProjectActionDispatcher(f.api, f.api.agentProfiles)
	ctx := internalBotRequestContext(t.Context(), "owner")
	if err := writeCrewTeam(crewTeam{ProjectID: "team", OwnerID: "owner", Name: "Example", On: true, People: []crewTeamPerson{{UserID: "other"}}}); err != nil {
		t.Fatal(err)
	}
	home, err := f.api.ensureCrewTeamOnlineHome(ctx, "team")
	if err != nil {
		t.Fatal(err)
	}
	source, err := resolveTriggerTarget(ctx, GetUserFromContext(ctx), "workspace:work:team")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := resolveTriggerTarget(ctx, GetUserFromContext(ctx), "workspace:work:"+home)
	if err != nil {
		t.Fatal(err)
	}
	script := "print('ready')\n"
	cfg := conditiontrigger.Config{Script: "code/triggers/check.py", SHA256: conditiontrigger.Hash(script), Target: conditiontrigger.Target{Kind: "agent", Message: "Summarize the change", ChatID: "main"}}
	input, _ := json.Marshal(scheduleExecutionInput{Messages: []string{"Daily summary"}})
	watchInput, _ := json.Marshal(scheduleExecutionInput{Condition: &cfg})
	schedules := []projectactions.Schedule{
		{ID: "daily", Name: "Daily", Project: projectactions.Project{Kind: "crew", ID: "team"}, RunAs: "owner", Enabled: true, When: projectactions.When{Cron: "0 9 * * *", Timezone: "UTC"}, Action: projectactions.Action{Type: "message", Tab: "team:chat:main"}, Input: input},
		{ID: "watcher", Name: "Watcher", Project: projectactions.Project{Kind: "crew", ID: "team"}, RunAs: "owner", Enabled: true, When: projectactions.When{Cron: "*/5 * * * *", Timezone: "UTC"}, Action: projectactions.Action{Type: "message", Tab: "team:chat:main"}, Input: watchInput},
	}
	if err = writeProjectSchedules(ctx, source.Path, schedules); err != nil {
		t.Fatal(err)
	}
	if err = writeFileToWorkspace(ctx, source.Path+"/"+cfg.Script, script); err != nil {
		t.Fatal(err)
	}
	call := func(request teamOnlineRequest) (int, teamOnlineResult, string) {
		t.Helper()
		raw, _ := json.Marshal(request)
		req := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/api/crew-teams/team/receive-online", bytes.NewReader(raw)).WithContext(ctx), map[string]string{"project": "team"})
		w := httptest.NewRecorder()
		f.api.handleCrewTeamReceiveOnline(w, req)
		var result teamOnlineResult
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return w.Code, result, w.Body.String()
	}
	for _, id := range []string{"daily", "watcher"} {
		request := teamOnlineRequest{TeamProjectID: "team", SourceProjectID: "team", Kind: "automation", ItemID: id, SubmissionID: "move-" + id, Preview: true}
		code, preview, body := call(request)
		if code != 200 || len(preview.Revision) != 64 {
			t.Fatalf("preview %s: %d %s", id, code, body)
		}
		request.Preview = false
		request.Revision = preview.Revision
		code, result, body := call(request)
		if code != 200 || len(result.Items) != 1 {
			t.Fatalf("apply %s: %d %s", id, code, body)
		}
		current, _ := readProjectSchedules(ctx, source.Path)
		for _, s := range current {
			if s.ID == id && s.Enabled {
				t.Fatal("source clock still enabled")
			}
		}
		copied, _ := readProjectSchedules(ctx, destination.Path)
		found := false
		for _, s := range copied {
			if s.ID != result.Items[0].DestinationItemID {
				continue
			}
			found = true
			if !s.Enabled || s.RunAs != destination.CrewOwner || s.Action.Tab != home+codeChatSideMarker+"main" {
				t.Fatal("routine is not on the Team Main")
			}
			if id == "watcher" {
				var in scheduleExecutionInput
				_ = json.Unmarshal(s.Input, &in)
				if in.Condition == nil || in.Condition.Target.ChatID != "main" || in.Condition.Validate() != nil {
					t.Fatal("moved Watcher cannot dispatch to its Team chat")
				}
			}
		}
		if !found {
			t.Fatal("destination clock missing")
		}
	}
}
