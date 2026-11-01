package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestWorkspaceMCPAliasesUseNativeServices(t *testing.T) {
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range []string{"files", "share", "settings", "dashboard", "functions"} {
		for _, tool := range catalog {
			if tool.Name != family {
				continue
			}
			action := "get"
			if family == "files" || family == "functions" {
				action = "list"
			}
			member, args, err := externalResolveMerged(tool, map[string]any{"action": action, "workspace_id": "code:example"})
			if err != nil || args["crew_id"] != "code:example" {
				t.Fatalf("%s lost native Code target: %s %v %v", family, member.Name, args, err)
			}
		}
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

func TestWorkspaceFunctionAuthoringKeepsProjectIdentity(t *testing.T) {
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range catalog {
		if tool.Name != "functions" {
			continue
		}
		for _, action := range []string{"define", "update", "delete"} {
			member, args, err := externalResolveMerged(tool, map[string]any{"action": action, "workspace_id": "code:example", "name": "reconcile", "description": "Function description", "instructions": "Function instructions", "input_schema": map[string]any{"type": "object"}, "result_schema": map[string]any{"type": "object"}})
			if err != nil || member.Name != "update_crew" || args["crew_id"] != "code:example" {
				t.Fatalf("%s: %s %v", action, member.Name, err)
			}
			for _, key := range []string{"name", "description", "instructions", "input_schema", "result_schema"} {
				if _, exists := args[key]; exists {
					t.Fatalf("%s can change project field %s: %v", action, key, args)
				}
			}
			raw, err := json.Marshal(args["functions"])
			if err != nil || !strings.Contains(string(raw), "reconcile") {
				t.Fatalf("%s lost the function name: %s %v", action, raw, err)
			}
		}
		return
	}
	t.Fatal("functions tool missing")
}

func TestWorkspaceExternalResultsKeepRunPresentationAndCallBoundaries(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	registry := withProjectOwnerRegistry(t)
	withProjectSharing(t, true)
	if err := registry.Register(projectOwnerRecord{Product: "code", Folder: "app-c0de0001", OwnerID: "owner", ProjectID: "c0de0001-0000"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetRunPeople("code", "app-c0de0001", []string{"other"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/external/v1/call", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "other"}))
	recorder := httptest.NewRecorder()
	api.externalWorkspaceCall(recorder, req, map[string]any{"action": "get", "workspace_id": "code:c0de0001-0000"})
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

func TestWorkspaceFailedTurnDoesNotHideOrReplaceNewerTurns(t *testing.T) {
	now := time.Now()
	api := &StreamingAPI{trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
		"failed": {ExecutionID: "failed", SessionID: "chat", StartedAt: now, Status: trackedExecutionStatusFailed},
		"other":  {ExecutionID: "other", SessionID: "another-chat", StartedAt: now.Add(time.Second), Status: trackedExecutionStatusFailed},
	}}
	if failure := api.externalWorkspaceTurnExecution("chat", ""); failure == nil || failure.ExecutionID != "failed" || failure.Status != trackedExecutionStatusFailed {
		t.Fatal("launch failure with no transcript disappeared")
	}
	if failure := api.externalWorkspaceTurnExecution("chat", "new"); failure != nil {
		t.Fatal("a new ask inherited an old failure")
	}
	api.trackedWorkflowExecutions["new"] = &TrackedWorkflowExecution{ExecutionID: "new", SessionID: "chat", StartedAt: now.Add(time.Second), Status: trackedExecutionStatusCompleted}
	if execution := api.externalWorkspaceTurnExecution("chat", ""); execution == nil || execution.ExecutionID != "new" || execution.Status != trackedExecutionStatusCompleted {
		t.Fatal("an old failure replaced the latest completed turn")
	}
	api.trackedWorkflowExecutions["new"].Status = trackedExecutionStatusRunning
	if execution := api.externalWorkspaceTurnExecution("chat", "new"); execution == nil || execution.Status != trackedExecutionStatusRunning {
		t.Fatal("a newly accepted turn looked idle before its runner acquired the lane")
	}
}

func TestWorkspaceCodeDeletionUsesItsNativeOrigin(t *testing.T) {
	for _, coOwner := range []bool{false, true} {
		t.Run(fmt.Sprint(coOwner), func(t *testing.T) {
			f := newMultiUserFixture(t, legacyIdentityLayout())
			caller := fixtureUserA
			if coOwner {
				caller = fixtureUserB
				if _, err := defaultProjectOwners().AddCoOwner("code", fixtureCodeFolder, caller); err != nil {
					t.Fatal(err)
				}
			}
			key := "code:" + fixtureCodeID
			req := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", nil).WithContext(f.Ctx(caller))
			rec := httptest.NewRecorder()
			f.API.externalCrewProjectCall(rec, req, map[string]any{"action": "delete", "confirm": key}, key)
			if rec.Code != http.StatusOK {
				t.Fatalf("delete code as co-owner=%v: %d %s", coOwner, rec.Code, rec.Body.String())
			}
			f.Mock.mu.Lock()
			defer f.Mock.mu.Unlock()
			if _, present := f.Mock.files[f.Layout.CodePhysical(fixtureUserA, fixtureCodeFolder)+"/product.json"]; present {
				t.Fatal("Code source still present after deletion")
			}
			if _, present := f.Mock.files[f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)+"/product.json"]; !present {
				t.Fatal("another origin was deleted")
			}
		})
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
		t.Setenv("LOCAL_MODE", fmt.Sprint(local))
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
	t.Setenv("LOCAL_MODE", "false")
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
