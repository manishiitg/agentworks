package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

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
	if rec := request(f.Ctx(fixtureUserC)); rec.Code != http.StatusForbidden {
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
