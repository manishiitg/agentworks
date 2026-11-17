package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

// PLAT-746-1025: deleting a Team home through the ordinary Crew handler must
// terminate at its derived roster, and remove only the two deleted Crew states.
func TestTeamHomeLifecycleProvisionsOnEnableAndDoesNotRecurseOnDelete(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	layout := legacyIdentityLayout()
	layout.CrewPhysical = func(_ string, folder string) string { return "Crew/" + folder }
	layout.CrewShort = func(folder string) string { return "Crew/" + folder }
	f := newMultiUserFixture(t, layout)
	registerTestCrew(t, fixtureCrewFolder, fixtureUserA, true, fixtureCrewID)
	profile := f.Crew
	profile.Version++
	profile.Runtime.Workspace.ProjectsRoot = "Crew"
	if err := f.API.agentProfiles.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	team := crewTeam{ProjectID: fixtureCrewID, OwnerID: fixtureUserA, Name: "Sales", People: []crewTeamPerson{{UserID: fixtureUserB}}}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	// Reads and asks never create the online home while the Team is off.
	if _, err := f.API.ensureCrewTeamOnlineHome(f.Ctx(fixtureUserA), fixtureCrewID); err == nil {
		t.Fatal("Team-off read provisioned a home")
	}
	const sourceAgent = "team-agent-11111111-2222-4333-8444-555555555555"
	team.Agents = map[string]crewTeamRegisteredAgent{sourceAgent: {ID: sourceAgent, OwnerID: fixtureUserA, Tab: "source", ChatKey: fixtureCrewID + ":chat:source", InTeam: true, Place: "mac"}}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	team, err := readCrewTeam(fixtureCrewID)
	if err != nil || team.OnlineHome != "" {
		t.Fatal("Team-off setup provisioned a home", err)
	}
	team.On = true
	if err := crewroot.WriteSync(fixtureCrewID, crewroot.SyncBinding{Mode: "relay", Side: "server", Editing: "server", PairID: "source-pair", PeerProjectID: "local-source"}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(team)
	enable := httptest.NewRequest(http.MethodPut, "/api/crew-teams/"+fixtureCrewID, bytes.NewReader(body)).WithContext(f.Ctx(fixtureUserA))
	enable = mux.SetURLVars(enable, map[string]string{"project": fixtureCrewID})
	enabled := httptest.NewRecorder()
	f.API.handleCrewTeam(enabled, enable)
	if enabled.Code >= 300 {
		t.Fatalf("Team enable: %d %s", enabled.Code, enabled.Body.String())
	}
	saved, err := readCrewTeam(fixtureCrewID)
	if err != nil || saved.OnlineHome == "" {
		t.Fatalf("Team on did not create its agent: %+v %v", saved, err)
	}
	f.API.repairCrewTeamHomeMembers(f.Ctx(fixtureUserA))
	homeID, err := f.API.ensureCrewTeamOnlineHome(f.Ctx(fixtureUserB), fixtureCrewID)
	if err != nil {
		t.Fatal(err)
	}
	home, err := readCrewTeam(homeID)
	if err != nil || home.RosterSource != fixtureCrewID || home.OnlineHome != "" {
		t.Fatal("home must terminate lifecycle recursion", err)
	}
	if homeID != saved.OnlineHome {
		t.Fatal("startup/repeated member request duplicated home")
	}
	if !crewTeamHomeOwnsSetup(saved, home.OwnerID) || crewTeamHomeOwnsSetup(saved, fixtureUserB) {
		t.Fatal("setup runtime must be this home's virtual user only")
	}
	for _, viewer := range []string{fixtureUserA, fixtureUserB} {
		found := false
		for _, row := range crewTeamReachableIn(f.Ctx(viewer), saved, viewer) {
			if row.Kind == "team_agent" && row.Online {
				if row.Name != "Team agent" {
					t.Fatalf("Team agent projected as %q", row.Name)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("member %s has no Team agent", viewer)
		}
	}
	// PLAT-746-1025: the online home must not appear as a second Crew or its execution account as a person.
	people := teamAgentsByPerson(f.Ctx(fixtureUserA), home, fixtureUserA)
	live := crewTeamLiveFor(f.Ctx(fixtureUserA), home, people, fixtureUserA)
	for _, person := range live.People {
		if person.UserID == home.OwnerID {
			t.Fatal("virtual execution owner appeared as a teammate")
		}
	}
	for _, person := range crewTeamPeopleFor(home, people, fixtureUserA, crewPersonShortName) {
		if person.Name == crewTeamPersonLabels(home)[home.OwnerID] {
			t.Fatal("virtual execution owner appeared in presence")
		}
	}
	for _, detail := range []string{"", "navigation"} {
		out := httptest.NewRecorder()
		f.API.listExternalWorkspaces(out, httptest.NewRequest("POST", "/api/workspaces", nil).WithContext(f.Ctx(fixtureUserA)), nil, detail == "navigation")
		if out.Code != 200 || strings.Contains(out.Body.String(), homeID) || !strings.Contains(out.Body.String(), fixtureCrewID) {
			t.Fatalf("home leaked into Crew list (%s): %d %s", detail, out.Code, out.Body.String())
		}
	}
	// #2796: the legacy/shared directory and invitation paths must agree with
	// navigation for both the maker and a joined member, not list the home.
	for _, viewer := range []string{fixtureUserA, fixtureUserB} {
		rows := listProjectsForOwner(f.Ctx(viewer), f.Claims(viewer), profile, home.OwnerID, true)
		if len(rows) != 0 {
			t.Fatal("home appeared in shared project directory")
		}
		rows = listProjectsForOwner(f.Ctx(viewer), f.Claims(viewer), profile, fixtureUserA, true)
		found := false
		for _, row := range rows {
			if row.ID == fixtureCrewID {
				found = true
			}
		}
		if !found {
			t.Fatal("source project missing from directory")
		}
		out := httptest.NewRecorder()
		f.API.listCrewTeams(out, httptest.NewRequest("GET", "/api/crew-teams", nil).WithContext(f.Ctx(viewer)))
		var listed struct {
			Teams []crewTeam `json:"teams"`
		}
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &listed) != nil {
			t.Fatal("team list unavailable")
		}
		found = false
		for _, row := range listed.Teams {
			if row.ProjectID == homeID {
				t.Fatal("home appeared as separate Team")
			}
			if row.ProjectID == fixtureCrewID {
				found = true
			}
		}
		if !found {
			t.Fatal("source Team missing from invites list")
		}
	}
	// A home read resolves the source Team, not a different permission path.
	for _, viewer := range []string{fixtureUserA, fixtureUserB, fixtureUserC} {
		out := httptest.NewRecorder()
		r := mux.SetURLVars(httptest.NewRequest("GET", "/online-items", nil).WithContext(f.Ctx(viewer)), map[string]string{"project": homeID})
		f.API.handleCrewTeamOnlineItems(out, r)
		if viewer == fixtureUserC {
			if out.Code != 403 {
				t.Fatalf("outsider read home mappings: %d", out.Code)
			}
		} else if out.Code != 200 {
			t.Fatalf("member home items: %d %s", out.Code, out.Body.String())
		}
	}
	for _, allowed := range []bool{false, true} {
		claims := f.Claims(fixtureUserB)
		claims.AccessToken = &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{homeID}}
		if allowed {
			claims.AccessToken.CrewIDs = append(claims.AccessToken.CrewIDs, fixtureCrewID)
		}
		out := httptest.NewRecorder()
		r := mux.SetURLVars(httptest.NewRequest("GET", "/online-items", nil).WithContext(context.WithValue(f.Ctx(fixtureUserB), UserContextKey, claims)), map[string]string{"project": homeID})
		f.API.handleCrewTeamOnlineItems(out, r)
		if allowed && out.Code != 200 || !allowed && out.Code != 403 {
			t.Fatalf("source consent allowed=%t: %d %s", allowed, out.Code, out.Body.String())
		}
	}
	// The saved name, including a rename, must survive registry/reach projection (f97a70da42).
	key := crewOwnerMainChatKey(homeID)
	name := "Operations helper"
	if _, err := defaultProductConversationRegistryStore().setAgentIdentity(f.Ctx(home.OwnerID), home.OwnerID, crewProfileID, key, &name, nil, home.OwnerID); err != nil {
		t.Fatal(err)
	}
	for _, row := range crewTeamReachableIn(f.Ctx(fixtureUserA), saved, fixtureUserA) {
		if row.Kind == "team_agent" && row.Name != name {
			t.Fatalf("Team rename lost: %+v", row)
		}
	}
	if rows := crewTeamReachableIn(f.Ctx("not-a-member"), saved, "not-a-member"); len(rows) != 0 {
		t.Fatal("outsider reached Team agent")
	}
	// Normal private Share plus the derived Team roster is not Everyone.
	homeRoot := crewProjectRootByID(homeID)
	if !crewSharedWith(homeRoot, fixtureUserB) || crewSharedWith(homeRoot, "not-a-member") {
		t.Fatal("home audience is not the current Team roster")
	}
	siblingDir, err := crewTeamDir("unrelated-crew")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(siblingDir, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(siblingDir, "keep")
	if err := os.WriteFile(marker, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/agent-profiles/work/projects/"+fixtureCrewID, nil).WithContext(f.Ctx(fixtureUserA))
	req = mux.SetURLVars(req, map[string]string{"id": "work", "project_id": fixtureCrewID})
	response := httptest.NewRecorder()
	f.API.handleDeleteAgentProfileProject(response, req)
	if response.Code >= 300 {
		t.Fatalf("ordinary delete failed: %d %s", response.Code, response.Body.String())
	}
	for _, id := range []string{fixtureCrewID, homeID} {
		dir, err := crewTeamDir(id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("deleted Crew %s retained Team state: %v", id, err)
		}
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "unrelated" {
		t.Fatal("unrelated Team state was removed", err)
	}
}

// A failed initial manifest is removed; failed optional setup keeps the new
// identity and returns it with a warning so retrying does not make duplicates.
func TestCrewCreationFailureKeepsOneUsableIdentity(t *testing.T) {
	f := newMultiUserFixture(t, legacyIdentityLayout())
	failSuffix := "/product.json"
	failedRoot := ""
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/documents/Crew/") && strings.HasSuffix(r.URL.Path, failSuffix) {
			failedRoot = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/documents/"), failSuffix)
			http.Error(w, "setup write unavailable", http.StatusServiceUnavailable)
			return
		}
		f.Mock.ServeHTTP(w, r)
	}))
	defer proxy.Close()
	t.Setenv("WORKSPACE_API_URL", proxy.URL)
	ctx := f.Ctx(fixtureUserA)
	if id, err := f.API.createWorkspaceFromSpec(ctx, GetUserFromContext(ctx), "work", crewSpec{Name: "Initial failure"}, false); err == nil || id != "" {
		t.Fatalf("initial manifest failure = %q %v", id, err)
	}
	if failedRoot == "" {
		t.Fatal("manifest failure was not reached")
	}
	if _, exists := defaultProjectOwners().Lookup("work", strings.TrimPrefix(failedRoot, "Crew/")); exists {
		t.Fatal("failed creation left an owner record")
	}
	for path := range f.Mock.files {
		if strings.HasPrefix(path, failedRoot+"/") {
			t.Fatalf("failed creation left %s", path)
		}
	}
	failSuffix = "/README.md"
	req := httptest.NewRequest(http.MethodPost, "/api/agent-profiles/work/projects", strings.NewReader(`{"name":"Partial setup","files":{"README.md":"Project notes"}}`)).WithContext(ctx)
	req = mux.SetURLVars(req, map[string]string{"id": "work"})
	rec := httptest.NewRecorder()
	f.API.handleCreateAgentProfileProject(rec, req)
	var out struct {
		ID       string   `json:"id"`
		Root     string   `json:"workspace_path"`
		Warnings []string `json:"warnings"`
	}
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.ID == "" || len(out.Warnings) != 1 {
		t.Fatalf("partial setup must return its created project: %d %s", rec.Code, rec.Body.String())
	}
	if projectStorageOwner(out.Root) != fixtureUserA || f.Mock.files[out.Root+"/product.json"] == "" || f.Mock.files[out.Root+"/workflow.json"] == "" {
		t.Fatal("partial setup lost its usable identity")
	}
}

// Creating a Crew over a token needs crews:write on all Crews; a token bounded to listed Crews cannot create.
func TestWorkspaceCreateTokenNeedsAllCrewsWrite(t *testing.T) {
	claims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:write"}, AllCrews: true}}
	if !workspaceCreateTokenAllowed(claims) {
		t.Fatal("all-Crews write token refused")
	}
	claims.AccessToken.AllCrews, claims.AccessToken.CrewIDs = false, []string{"one-project"}
	if workspaceCreateTokenAllowed(claims) {
		t.Fatal("bounded machine token created outside its project list")
	}
}

// fec04534b1 pins flat function definitions; function writes are not project patches.
func TestWorkspaceFunctionAuthoringKeepsProjectIdentity(t *testing.T) {
	tool, found := externalCatalogTool("functions")
	if !found {
		t.Fatal("functions tool missing")
	}
	for _, action := range []string{"define", "delete"} {
		input := map[string]any{"action": action, "project": "work:example"}
		if action == "define" {
			input["definition"] = map[string]any{"name": "reconcile", "description": "Function description", "execution": "tab", "tab_id": "main"}
		} else {
			input["name"] = "reconcile"
		}
		member, args, err := externalResolveMerged(tool, input)
		if err != nil || member.Name != "project_functions_"+action || args["project"] != "work:example" {
			t.Fatalf("%s lost function/project identity: %s %v %v", action, member.Name, args, err)
		}
		if _, exists := args["functions"]; exists {
			t.Fatal("function authoring became a project patch")
		}
	}
}

func TestWorkspaceExternalResultsKeepRunPresentationAndCallBoundaries(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	registry := withProjectOwnerRegistry(t)
	withProjectSharing(t, true)
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "app-c0de0001", OwnerID: "owner", ProjectID: "c0de0001-0000", Shared: true, Private: true, AudienceConfigured: true}); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetRunPeople("work", "app-c0de0001", []string{"other"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/external/v1/call", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "other"}))
	recorder := httptest.NewRecorder()
	api.externalWorkspaceCall(recorder, req, map[string]any{"action": "get", "workspace_id": "work:c0de0001-0000"})
	var out map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &out); err != nil || recorder.Code != 200 {
		t.Fatalf("shared get: %d %s", recorder.Code, recorder.Body.String())
	}
	panels, _ := out["ui_panels"].([]any)
	if out["mode"] != "run" || len(panels) != 1 || panels[0] != "dashboard" {
		t.Fatalf("Run exposed Builder panels: %v", out)
	}
	call := &crewFunctionCall{ID: "call", TargetKind: triggerCallerCrew, TargetLabel: "Project", TargetPath: codePrivacyOwnerRoot, Status: "completed", Result: map[string]any{"marker": "ok"}}
	result := externalCrewCallResponse(context.Background(), call, 0)
	target := result["target"].(map[string]any)
	if _, exposed := target["workspace_path"]; exposed || target["name"] != "Project" || result["result"] == nil {
		t.Fatalf("function result exposed its physical root or lost its result: %v", result)
	}
}

// PLAT-746-419: a successful live Crew delete left its workflow/script IDs
// behind, which can make a later lookup ambiguous. Other Crews stay intact.
func TestCrewDeletionRemovesOnlyItsExecutableBindings(t *testing.T) {
	// Code routing and per-user Crew roots were retired by 8106ccb9de /
	// f732afe300. Use the current shared-root Crew identity at this API.
	layout := legacyIdentityLayout()
	layout.CrewPhysical = func(_ string, folder string) string { return "Crew/" + folder }
	layout.CrewShort = func(folder string) string { return "Crew/" + folder }
	f := newMultiUserFixture(t, layout)
	// Real creation also materializes the private root required by workspace mutations.
	registerTestCrew(t, fixtureCrewFolder, fixtureUserA, true, fixtureCrewID)
	profile := f.Crew
	profile.Version++
	profile.Runtime.Workspace.ProjectsRoot = "Crew"
	if err := f.API.agentProfiles.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	f.put("Crew/other/product.json", `{"schema_version":1,"product":"work","id":"other-crew","title":"Other","session_id":"work:project:other-crew"}`)
	f.put("Crew/other/workflow.json", `{"schema_version":1,"id":"other-crew","label":"Other","capabilities":{}}`)
	registerTestCrew(t, "other", fixtureUserB, true, "other-crew")
	withProjectSharing(t, true)
	if err := defaultProjectOwners().SetRunPeople("work", fixtureCrewFolder, []string{fixtureUserB}); err != nil {
		t.Fatal(err)
	}
	for _, binding := range []crewExecutableBinding{
		{ID: "workflow-delete", CrewID: fixtureCrewID, CrewRoot: f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder), Root: "Workflow/delete-workflow", Kind: "script"},
		{ID: "script-shared-id", CrewID: fixtureCrewID, CrewRoot: f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder), Root: "Workflow/delete-script", Kind: "script"},
		{ID: "script-shared-id", CrewID: "other-crew", CrewRoot: "Crew/other", Root: "Workflow/keep-script", Kind: "script"},
	} {
		seedSavedCrewExecutableForTest(t, binding)
	}
	call := func(user string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", nil).WithContext(f.Ctx(user))
		rec := httptest.NewRecorder()
		f.API.externalCrewProjectCall(rec, req, map[string]any{"action": "delete", "confirm": fixtureCrewID}, fixtureCrewID)
		return rec
	}
	if rec := call(fixtureUserB); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "Only the Crew's owner") {
		t.Fatalf("Run deletion must refuse at owner check: %d %s", rec.Code, rec.Body.String())
	}
	if bindings, err := readCrewExecutables(); err != nil || len(bindings) != 3 {
		t.Fatalf("refused delete changed bindings: %v %v", bindings, err)
	}
	if rec := call(fixtureUserA); rec.Code != http.StatusOK {
		t.Fatalf("owner deletion: %d %s", rec.Code, rec.Body.String())
	}
	f.Mock.mu.Lock()
	_, ownPresent := f.Mock.files[layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)+"/product.json"]
	_, otherPresent := f.Mock.files["Crew/other/product.json"]
	f.Mock.mu.Unlock()
	if ownPresent || !otherPresent {
		t.Fatal("Crew delete removed the wrong project's source")
	}
	bindings, err := readCrewExecutables()
	if err != nil || len(bindings) != 1 || bindings["Workflow/keep-script"].CrewID != "other-crew" {
		t.Fatalf("wrong executable cleanup: %v %v", bindings, err)
	}
	if binding, err := findCrewExecutableByID("script-shared-id"); err != nil || binding == nil || binding.CrewID != "other-crew" {
		t.Fatalf("orphan still shadows another identity: %v %v", binding, err)
	}
}

// The location preview is gated like creation, shows virtual server paths,
// and must not create or inspect a caller-chosen folder.
func TestProjectCreationLocationKeepsAccessAndDoesNotCreate(t *testing.T) {
	f := newMultiUserFixture(t, legacyIdentityLayout())
	filesBefore := len(f.Mock.files)
	request := func(ctx context.Context) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/agent-profiles/work/projects/location?name=Release+notes&root_path=/unrelated", nil).WithContext(ctx)
		req = mux.SetURLVars(req, map[string]string{"id": "work"})
		rec := httptest.NewRecorder()
		f.API.handleProjectCreationLocation(rec, req)
		return rec
	}
	for _, local := range []bool{false, true} {
		t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", map[bool]string{true: "local"}[local])
		rec := request(f.Ctx(fixtureUserA))
		var out struct {
			Folder    string `json:"folder"`
			CanChoose bool   `json:"can_choose_folder"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("location: %d %s", rec.Code, rec.Body.String())
		}
		want := "Crew/release-notes-…"
		if local {
			want = filepath.Join(f.Docs, want)
		}
		if out.Folder != want || out.CanChoose != local {
			t.Fatalf("location %q %v; want %q %v", out.Folder, out.CanChoose, want, local)
		}
	}
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	if rec := request(context.Background()); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned caller: %d", rec.Code)
	}
	if rec := request(f.Ctx("not-a-member")); rec.Code != http.StatusForbidden {
		t.Fatalf("unadmitted caller: %d", rec.Code)
	}
	ctx := f.Ctx(fixtureUserA)
	claims := *GetUserFromContext(ctx)
	claims.AccessToken = &accesstokens.Token{Scopes: []string{"crews:write"}, CrewIDs: []string{"existing"}}
	if rec := request(context.WithValue(ctx, UserContextKey, &claims)); rec.Code != http.StatusForbidden {
		t.Fatalf("bounded token: %d", rec.Code)
	}
	if len(f.Mock.files) != filesBefore {
		t.Fatal("location lookup wrote workspace files")
	}
}

// PLAT-746-1023: only an explicit, source-validated begin provisions a Team
// agent. Repeated begin returns the same ordinary Main, without applying data.
func TestDashboardHandoffBeginValidatesSourceBeforeIdempotentProvision(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	layout := legacyIdentityLayout()
	layout.CrewPhysical = func(_ string, folder string) string { return "Crew/" + folder }
	layout.CrewShort = func(folder string) string { return "Crew/" + folder }
	f := newMultiUserFixture(t, layout)
	registerTestCrew(t, fixtureCrewFolder, fixtureUserA, true, fixtureCrewID)
	profile := f.Crew
	profile.Version++
	profile.Runtime.Workspace.ProjectsRoot = "Crew"
	if err := f.API.agentProfiles.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	if err := writeCrewTeam(crewTeam{ProjectID: fixtureCrewID, OwnerID: fixtureUserA, Name: "Sales", On: true, People: []crewTeamPerson{{UserID: fixtureUserB}}}); err != nil {
		t.Fatal(err)
	}
	oldDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", f.Docs)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
	router := gin.New()
	router.POST("/api/dashboards", workspacehandlers.DashboardOperation)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/dashboards" {
			router.ServeHTTP(w, r)
			return
		}
		f.Mock.ServeHTTP(w, r)
	}))
	defer proxy.Close()
	t.Setenv("WORKSPACE_API_URL", proxy.URL)
	root := "Crew/" + fixtureCrewFolder
	ctx := f.Ctx(fixtureUserA)
	created, err := dashboards.Execute(ctx, f.Docs, dashboards.Request{Root: root, ID: "orders", Action: "create", Title: "Orders", Files: map[string]string{"index.html": "<h1>Orders</h1>", "online.json": `{"dataset":{"tables":[]}}`}, IncludeDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dashboards.Execute(ctx, f.Docs, dashboards.Request{Root: root, ID: "orders", Action: "publish", ExpectedRevision: created.Dashboard.Revision, IncludeDraft: true}); err != nil {
		t.Fatal(err)
	}
	input := teamOnlineRequest{TeamProjectID: fixtureCrewID, SourceProjectID: fixtureCrewID, Kind: "dashboard", ItemID: "orders", Revision: strings.Repeat("a", 64), SubmissionID: "begin-orders", Preview: true}
	if _, err = f.API.beginTeamDashboardHandoff(ctx, input); err == nil {
		t.Fatal("stale source revision provisioned a home")
	}
	team, err := readCrewTeam(fixtureCrewID)
	if err != nil || team.OnlineHome != "" {
		t.Fatalf("refused begin created home: %+v %v", team, err)
	}
	input.Revision = created.Dashboard.Revision
	if _, err = f.API.beginTeamDashboardHandoff(f.Ctx(fixtureUserB), input); err == nil {
		t.Fatal("another member used the owner's source item")
	}
	first, err := f.API.beginTeamDashboardHandoff(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.API.beginTeamDashboardHandoff(ctx, input)
	if err != nil || first.HomeProjectID == "" || first.Address == "" || first.Address != second.Address || first.HomeProjectID != second.HomeProjectID {
		t.Fatalf("idempotent ordinary Main: %+v %+v %v", first, second, err)
	}
	// PLAT-746-1023: Team/home consent must not become consent for another
	// owned paired Mac Crew. Refuse before the remote export or provisioning.
	scoped := crewWriter(fixtureUserA)
	scoped.Scope = "mcp"
	scoped.AccessToken.AllCrews = false
	scoped.AccessToken.CrewIDs = []string{fixtureCrewID, first.HomeProjectID}
	scoped.AccessToken.Scopes = append(scoped.AccessToken.Scopes, "dashboards:read")
	scopedCtx := context.WithValue(ctx, UserContextKey, scoped)
	outside := input
	outside.SourceProjectID = "outside-connection-consent"
	if _, err = f.API.beginTeamDashboardHandoff(scopedCtx, outside); err == nil || !strings.Contains(err.Error(), "allow the source Crew") {
		t.Fatalf("connection without source consent reached export: %v", err)
	}
	allowed, err := f.API.beginTeamDashboardHandoff(scopedCtx, input)
	if err != nil || allowed.HomeProjectID != first.HomeProjectID || allowed.Address != first.Address {
		t.Fatalf("admitted scoped Builder refused: %+v %v", allowed, err)
	}
	rows, err := readTeamOnlineMappings(ctx, triggerTarget{CrewID: first.HomeProjectID})
	if err != nil || len(rows) != 0 {
		t.Fatalf("begin applied an item: %+v %v", rows, err)
	}
}
