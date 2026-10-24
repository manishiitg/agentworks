package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestWorkspaceMemoryDefaultsAndSwitch(t *testing.T) {
	_, profile := newCodePrivacyFixture(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	if workspaceMemoryEnabled(ctx, "code", codePrivacyOwnerRoot) || !workspaceMemoryEnabled(ctx, "work", "Crew/existing") {
		t.Fatal("origin memory defaults changed")
	}
	for _, enabled := range []bool{true, false} {
		if err := setWorkspaceMemory(ctx, "code", codePrivacyOwnerRoot, enabled); err != nil {
			t.Fatal(err)
		}
		if err := applyWorkspaceMemoryFeature(ctx, &profile, codePrivacyOwnerRoot); err != nil {
			t.Fatal(err)
		}
		if agentprofiles.HasFeature(profile, "memory") != enabled {
			t.Fatal("runtime ignored saved memory setting")
		}
		if sectionByName(t, "project-memory").Applies(promptContext{HasProfile: true, ProjectMemoryDisabled: !agentprofiles.HasFeature(profile, "memory")}) != enabled {
			t.Fatal("Memory instruction gate disagrees with saved setting")
		}
		raw, _, err := readFileFromWorkspace(ctx, codePrivacyOwnerRoot+"/workflow.json")
		var manifest productProjectManifest
		if err != nil || json.Unmarshal([]byte(raw), &manifest) != nil || manifest.Capabilities.MemoryEnabled == nil || *manifest.Capabilities.MemoryEnabled != enabled {
			t.Fatal("typed manifest lost memory setting")
		}
	}
}

// Creation changes only the new project's explicit value; paths with absent
// legacy settings retain their old behavior.
func TestNewCrewMemoryDefaultsPreserveExistingProjects(t *testing.T) {
	f := newMultiUserFixture(t, legacyIdentityLayout())
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: fixtureUserA})
	for _, tc := range []struct {
		origin, mode string
		enabled      bool
	}{
		{"work", "dev", true}, {"code", "dev", true}, {"code", "cowork", true},
	} {
		t.Run(tc.origin+"/"+tc.mode, func(t *testing.T) {
			body := `{"name":"Memory default","purpose":"Keep project facts","icon":"M","role":"Assistant","mode":"` + tc.mode + `","llm_config":{"schema_version":2,"mode":"explicit","builder_llm":{"provider":"codex-cli","model_id":"gpt-6-luna","connection_id":"company-account","options":{"reasoning_effort":"high"}}}}`
			req := httptest.NewRequest(http.MethodPost, "/api/agent-profiles/"+tc.origin+"/projects", strings.NewReader(body)).WithContext(ctx)
			req = mux.SetURLVars(req, map[string]string{"id": tc.origin})
			rec := httptest.NewRecorder()
			f.API.handleCreateAgentProfileProject(rec, req)
			var result struct {
				ID       string `json:"id"`
				Root     string `json:"workspace_path"`
				Manifest struct {
					Product string `json:"product"`
					Mode    string `json:"mode"`
					Session string `json:"session_id"`
				} `json:"product_manifest"`
				Runtime struct {
					Capabilities struct {
						Memory *bool `json:"memory_enabled"`
						LLM    *struct {
							Builder *struct {
								Model      string `json:"model_id"`
								Connection string `json:"connection_id"`
							} `json:"builder_llm"`
						} `json:"llm_config"`
					} `json:"capabilities"`
				} `json:"runtime_manifest"`
			}
			if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.Runtime.Capabilities.Memory == nil || *result.Runtime.Capabilities.Memory != tc.enabled {
				t.Fatalf("creation policy: %d %s", rec.Code, rec.Body.String())
			}
			if !strings.HasPrefix(result.Root, "Crew/") || result.Manifest.Product != tc.origin || result.Manifest.Mode != tc.mode || result.Manifest.Session != tc.origin+":project:"+result.ID {
				t.Fatalf("creation lost origin/mode/session: %s", rec.Body.String())
			}
			saved, ok := defaultProjectOwners().Lookup(tc.origin, strings.TrimPrefix(result.Root, "Crew/"))
			if !ok || saved.OwnerID != fixtureUserA || saved.CanonicalRoot != result.Root || !saved.Private || !saved.AudienceConfigured {
				t.Fatalf("untrusted creation identity: %+v", saved)
			}
			if result.Runtime.Capabilities.LLM == nil || result.Runtime.Capabilities.LLM.Builder == nil || result.Runtime.Capabilities.LLM.Builder.Model != "gpt-6-luna" || result.Runtime.Capabilities.LLM.Builder.Connection != "company-account" {
				t.Fatal("creation lost the selected model/account")
			}
			if enabled := workspaceMemoryEnabled(ctx, tc.origin, result.Root); enabled != tc.enabled {
				t.Fatalf("saved memory=%v want %v", enabled, tc.enabled)
			}

		})
	}
	// MCP chooses the supported origin for a Cowork request without changing
	// the caller's existing product/create authority.
	mcp := httptest.NewRecorder()
	f.API.externalWorkspaceCall(mcp, httptest.NewRequest(http.MethodPost, "/mcp", nil).WithContext(ctx), map[string]any{"action": "create", "spec": map[string]any{"name": "MCP Cowork", "mode": "cowork"}})
	var mcpCreated struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if mcp.Code != http.StatusOK || json.Unmarshal(mcp.Body.Bytes(), &mcpCreated) != nil || !strings.HasPrefix(mcpCreated.WorkspaceID, "code:") {
		t.Fatalf("MCP creation: %d %s", mcp.Code, mcp.Body.String())
	}
	// A co-owner creating a sibling is the creator; the current Crew's main
	// owner/provider account never lends its create authority or identity.
	if _, err := defaultProjectOwners().AddCoOwner("code", fixtureCodeFolder, fixtureUserB); err != nil {
		t.Fatal(err)
	}
	created, err := f.API.projectCreationTool("code", fixtureUserB, "creation-test", agentprofiles.ToolBinding{})(f.Ctx(fixtureUserB), map[string]any{"name": "Sibling"})
	if err != nil {
		t.Fatal(err)
	}
	var sibling struct {
		Root string `json:"workspace_path"`
	}
	if json.Unmarshal([]byte(created), &sibling) != nil || projectStorageOwner(sibling.Root) != fixtureUserB {
		t.Fatalf("sibling borrowed its owner's identity: %s", created)
	}
	// Native tool execution keeps the authenticated turn's restricted token,
	// even when the tool context has no request or has broader account claims.
	if _, err := f.API.projectCreationTool("code", fixtureUserB, "missing-claims", agentprofiles.ToolBinding{})(context.Background(), map[string]any{"name": "Refused"}); err == nil {
		t.Fatal("a native creation tool invented full account authority")
	}
	bounded := &UserClaims{UserID: fixtureUserB, AccessToken: &accesstokens.Token{Scopes: []string{"code:run"}, CrewIDs: []string{fixtureCodeFolder}}}
	if _, err := f.API.projectCreationTool("code", fixtureUserB, "bounded-claims", agentprofiles.ToolBinding{}, bounded)(f.Ctx(fixtureUserB), map[string]any{"name": "Refused"}); err == nil {
		t.Fatal("native creation dropped the current token's project limit")
	}
	for _, profile := range []agentprofiles.Profile{f.Crew, f.Code} {
		root := "Crew/new-" + profile.ID + "-default"
		if profile.ID == "code" {
			root = "_users/" + fixtureUserA + "/Chats/Code/projects/new-code-default"
		}
		if _, err := writeCrewCreationManifests(ctx, fixtureUserA, profile, profile.ID, "", "Memory default", "", "", "", "new-"+profile.ID, root, nil); err != nil {
			t.Fatal(err)
		}
		if !workspaceMemoryEnabled(ctx, profile.ID, root) {
			t.Fatalf("native %s creation ignored its explicit default", profile.ID)
		}
	}
	// Exercise the real Code factory registration, including saved native calls.
	if err := workproduct.RegisterAgentProfileRuntime(f.API.agentProfiles, "http://127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	profile := f.Code
	profile.Tools = []agentprofiles.ToolBinding{{ID: "code.create-project"}}
	resolved := &resolvedAgentProfile{Definition: profile, ToolCallerClaims: GetUserFromContext(ctx)}
	registrar := &recordingRegistrar{}
	if err := f.API.registerAgentProfileTools(registrar, newProductToolGate(resolved), resolved, fixtureUserA, "native-create", "", false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"create_crew", "create_code_workspace"} {
		tool, ok := registrar.tools[name]
		if !ok || !strings.Contains(tool.desc, "${MCP_CUSTOM}/create_crew") {
			t.Fatalf("missing native creation route %s", name)
		}
		result, err := tool.exec(context.Background(), map[string]interface{}{"name": "Native sibling"})
		if err != nil || !strings.Contains(result, `"workspace_path":"Crew/`) {
			t.Fatalf("%s: %s %v", name, result, err)
		}
	}
	reader := &recordingRegistrar{}
	if err := f.API.registerAgentProfileTools(reader, newProductToolGate(resolved), resolved, fixtureUserA, "native-run", "", true); err != nil {
		t.Fatal(err)
	}
	if _, found := reader.tools["create_crew"]; found {
		t.Fatal("Run mode received Crew creation")
	}
	if workspaceMemoryEnabled(ctx, "code", f.Layout.CodePhysical(fixtureUserA, fixtureCodeFolder)) || !workspaceMemoryEnabled(ctx, "work", f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)) {
		t.Fatal("new-project default changed existing projects")
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

func TestWorkspaceCodeOnlyCreateScope(t *testing.T) {
	claims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"code:run"}}}
	if !workspaceCreateTokenAllowed(claims, "code") || workspaceCreateTokenAllowed(claims, "work") {
		t.Fatal("creation scope crossed origin")
	}
	claims.AccessToken.CrewIDs = []string{"one-project"}
	if workspaceCreateTokenAllowed(claims, "code") {
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
