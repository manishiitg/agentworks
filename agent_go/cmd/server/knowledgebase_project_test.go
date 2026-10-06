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

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestKnowledgebaseProjectMCPBuilderAndStepPolicies(t *testing.T) {
	tokenTestSetup(t)
	service, admin, workspace, folderID := knowledgeIntegrationFixture(t)
	api := &StreamingAPI{}
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scopes := []string{"knowledgebase:read", "knowledgebase:write", "builder:chat", "workflows:read", "files:read", "runs:execute"}
	connect := func(workflowID string, author bool) (*client.Client, accesstokens.Token) {
		t.Helper()
		granted := scopes
		ids := []string{workflowID}
		if !author {
			granted = scopes[:2]
			ids = nil
		}
		token, _, err := store.Issue(t.Context(), accesstokens.Token{UserID: "admin", Username: "admin", Name: "project-test", Scopes: granted, WorkflowIDs: ids, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		server := serveExternalMCP(t, api, &UserClaims{UserID: "admin", Username: "admin", AccessToken: &token})
		cli := dialExternalMCP(t, t.Context(), server.URL+externalMCPPath)
		initializeExternalMCP(t, t.Context(), cli)
		return cli, token
	}
	cli, token := connect("payments-workflow", true)
	wrong, _ := connect("another-workflow", true)
	contentOnly, _ := connect("payments-workflow", false)
	call := func(cli *client.Client, args map[string]any) *mcp.CallToolResult {
		return callRemoteTool(t, t.Context(), cli, externalMCPToolCall, map[string]any{"name": "brain_access", "arguments": args})
	}
	result := func(args map[string]any) map[string]any {
		t.Helper()
		response := call(cli, args)
		requireRemoteSuccess(t, response, "project binding")
		var body struct {
			Result map[string]any `json:"result"`
		}
		if err := json.Unmarshal([]byte(marshalStructured(t, response)), &body); err != nil {
			t.Fatal(err)
		}
		return body.Result
	}
	inspect := map[string]any{"action": "inspect_project", "workspace_path": workspace}
	requireRemoteError(t, call(wrong, inspect), "outside authoring grant", "FORBIDDEN")
	requireRemoteError(t, call(contentOnly, inspect), "KB-only token", "insufficient_scope")
	current := result(inspect)
	// The pool exists before attachment so subsequent builder-run steps can use
	// newly selected bindings without an unrelated manifest edit.
	parent := "project-builder"
	common.SetSessionWorkflowPath(parent, workspace)
	defer common.ClearSessionShellConfig(parent)
	_, executors, _ := createKnowledgebaseTools("admin", parent, workspace)
	read := executors["brain_read"].(func(context.Context, map[string]interface{}) (string, error))
	update := executors["brain_update"].(func(context.Context, map[string]interface{}) (string, error))
	args := map[string]any{"action": "set_project_access", "workspace_path": workspace, "mode": "read", "expected_manifest_version": current["manifest_version"], "request_id": "mcp-bind-test"}
	bound := result(args)
	if result(args)["manifest_version"] != bound["manifest_version"] {
		t.Fatal("binding retry changed manifest")
	}
	entry, err := service.CallTool(t.Context(), admin, "brain_update", map[string]any{"action": "create", "folder_id": folderID, "filename": "smoke.md", "title": "Smoke", "type": "note", "content": "kb-marker-71831", "request_id": "marker-create"})
	if err != nil {
		t.Fatal(err)
	}
	entryID := knowledgeMap(entry)["entry_id"]
	readArgs := map[string]any{"action": "read", "entry_id": entryID}
	if out, err := read(knowledgeTestCaller(context.WithValue(t.Context(), common.ChatSessionIDKey, parent), "admin"), readArgs); err != nil || !strings.Contains(out, "kb-marker-71831") {
		t.Fatalf("builder read: %s %v", out, err)
	}
	// Child policy must win over the parent's registration session.
	for _, access := range []string{"read", "none", "write"} {
		child := "project-step-" + access
		common.SetSessionWorkflowPath(child, workspace)
		common.SetSessionShellEnv(child, map[string]string{"SHARED_KB_STEP_ACCESS": access})
		defer common.ClearSessionShellConfig(child)
		ctx := knowledgeTestCaller(context.WithValue(t.Context(), common.ChatSessionIDKey, child), "admin")
		out, err := read(ctx, readArgs)
		if (err == nil) != (access != "none") {
			t.Fatalf("%s read: %s %v", access, out, err)
		}
		if _, err := update(ctx, map[string]any{"action": "create", "folder_id": folderID, "filename": "denied-" + access + ".md", "title": "Denied", "type": "note", "content": "denied", "request_id": "denied-step-" + access}); err == nil {
			t.Fatalf("%s bypassed read binding", access)
		}
	}
	// Builder setup uses the same action, constrained to its authenticated root.
	rootCtx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "admin", Username: "admin"})
	rootCtx = executor.WithSessionID(rootCtx, parent)
	rootCtx = context.WithValue(rootCtx, knowledgeProjectBuilderKey{}, knowledgeProjectBuilderAuthority{"admin", workspace, parent})
	rootCtx = context.WithValue(rootCtx, common.ChatSessionIDKey, parent)
	version := result(inspect)["manifest_version"]
	writeArgs := map[string]any{"action": "set_project_access", "workspace_path": workspace, "mode": "write", "expected_manifest_version": version, "request_id": "builder-write-bind"}
	if _, err := knowledgeProjectBuilderExecute(context.WithValue(rootCtx, common.ChatSessionIDKey, "project-step-read"), "brain_access", writeArgs); err == nil {
		t.Fatal("child inherited Builder setup")
	}
	if _, err := knowledgeProjectBuilderExecute(rootCtx, "brain_access", writeArgs); err != nil {
		t.Fatal("builder attachment", err)
	}
	ctx := knowledgeTestCaller(context.WithValue(t.Context(), common.ChatSessionIDKey, "project-step-write"), "admin")
	if _, err := update(ctx, map[string]any{"action": "create", "folder_id": folderID, "filename": "step.md", "title": "Step", "type": "note", "content": "step-created", "request_id": "write-step"}); err != nil {
		t.Fatal("write step", err)
	}
	ctx = knowledgeTestCaller(context.WithValue(t.Context(), common.ChatSessionIDKey, "project-step-read"), "admin")
	if _, err := update(ctx, map[string]any{"action": "create", "folder_id": folderID, "filename": "readonly.md", "title": "Denied", "type": "note", "content": "denied", "request_id": "read-step-rejected"}); err == nil {
		t.Fatal("read step inherited parent write")
	}
	if err := store.Revoke(t.Context(), token.ID, "admin", time.Now()); err != nil {
		t.Fatal(err)
	}
	requireRemoteError(t, call(cli, args), "revoked binding retry", "FORBIDDEN")
}

func TestKnowledgebaseProjectUIUsesOwnerAndAudienceChecks(t *testing.T) {
	_, _, workspace, _ := knowledgeIntegrationFixture(t)
	api := &StreamingAPI{}
	request := func(user, method string, args map[string]any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(args)
		req := httptest.NewRequest(method, "/api/knowledgebase/project?workspace_path="+workspace, strings.NewReader(string(data)))
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		w := httptest.NewRecorder()
		api.handleKnowledgebaseProject(w, req)
		return w
	}
	if w := request("outsider", http.MethodGet, nil); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("priya", http.MethodGet, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"can_manage":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
	project, _ := knowledgeProjectLoad(t.Context(), "admin", workspace, true)
	args := map[string]any{"action": "set_project_access", "workspace_path": workspace, "mode": "read", "expected_manifest_version": project.Version, "request_id": "ui-bind"}
	if w := request("priya", http.MethodPost, args); w.Code != 403 {
		t.Fatal("reader changed Brain access", w.Code, w.Body.String())
	}
	if w := request("admin", http.MethodPost, args); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	args["request_id"] = "stale-ui-bind"
	if w := request("admin", http.MethodPost, args); w.Code != 409 {
		t.Fatal("stale UI accepted", w.Code, w.Body.String())
	}
	args["action"], args["request_id"] = "bind_project", "removed-bind"
	if w := request("admin", http.MethodPost, args); w.Code != 400 {
		t.Fatal("folder bindings were removed (PLAT-628)", w.Code, w.Body.String())
	}
}

func TestKnowledgebaseProjectBuilderAuthorityMatchesVaultIsolation(t *testing.T) {
	claims := &UserClaims{UserID: "admin"}
	// The shape handleQuery binds tools with: the mode is already rewritten, the admission flag is set (PLAT-600/608).
	req := QueryRequest{AgentMode: "multi-agent", PhaseID: "workflow-builder", SelectedFolder: "Workflow/payments", admittedWorkflowPhase: true}
	if !knowledgeProjectBuilderQuery(req, claims, "root", "root", false) {
		t.Fatal("interactive builder missing")
	}
	if knowledgeProjectBuilderQuery(req, claims, "root", "child", false) || knowledgeProjectBuilderQuery(req, claims, "root", "root", true) {
		t.Fatal("delegated or reader setup admitted")
	}
	claims.ExecutionPrincipal = &ExecutionPrincipal{Kind: "scheduled"}
	if knowledgeProjectBuilderQuery(req, claims, "root", "root", false) {
		t.Fatal("scheduled setup admitted")
	}
}

func TestKnowledgebaseProjectSetupUsesAuthenticatedToolBinding(t *testing.T) {
	_, _, workspace, _ := knowledgeIntegrationFixture(t)
	workspaceServer, docs := newFakeWorkspaceServer(t)
	defer workspaceServer.Close()
	data, err := os.ReadFile(filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), workspace, "workflow.json"))
	if err != nil {
		t.Fatal(err)
	}
	docs.files[workspace+"/workflow.json"] = string(data)
	t.Setenv("WORKSPACE_API_URL", workspaceServer.URL)
	api := &StreamingAPI{}
	claims := &UserClaims{UserID: "admin", Username: "admin"}
	input := context.WithValue(t.Context(), UserContextKey, claims)
	req := QueryRequest{AgentMode: "multi-agent", PhaseID: "workflow-builder", SelectedFolder: workspace, admittedWorkflowPhase: true}
	root, err := api.bindToolExecutionContext(input, "kb-root", req, false)(executor.WithSessionID(t.Context(), "kb-root"), "brain_access")
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := root.Value(knowledgeProjectBuilderKey{}).(knowledgeProjectBuilderAuthority); !ok || value.UserID != "admin" || value.Workspace != workspace {
		t.Fatal("missing root authority")
	}
	child, err := api.bindToolExecutionContextForSession(input, "kb-root", "kb-child", req, false)(root, "brain_access")
	// The incoming root session is intentionally rejected for a child binding.
	if err == nil {
		t.Fatal("root caller admitted as child")
	}
	child, err = api.bindToolExecutionContextForSession(input, "kb-root", "kb-child", req, false)(context.WithValue(executor.WithSessionID(t.Context(), "kb-child"), knowledgeProjectBuilderKey{}, root.Value(knowledgeProjectBuilderKey{})), "brain_access")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := child.Value(knowledgeProjectBuilderKey{}).(knowledgeProjectBuilderAuthority); ok {
		t.Fatal("child inherited setup")
	}
}

// Pins the owner decisions for a project's Brain access: Off refuses everything, Read can only read, Read & write
// lets the agents organize folders wherever the owner may write, and both stop at a folder that one of the project's
// output readers cannot read. A step with no Brain access gets none in any mode.
func TestBrainProjectAccessModes(t *testing.T) {
	service, admin, workspace, folderID := knowledgeIntegrationFixture(t)
	create := func(folder map[string]any, name string) string {
		t.Helper()
		args := map[string]any{"action": "create", "filename": name + ".md", "title": name, "type": "note", "content": name + "-marker", "request_id": "seed-" + name}
		for key, value := range folder {
			args[key] = value
		}
		entry, err := service.CallTool(t.Context(), admin, "brain_update", args)
		if err != nil {
			t.Fatal(err)
		}
		return knowledgeMap(entry)["entry_id"].(string)
	}
	shared := create(map[string]any{"folder_id": folderID}, "shared")
	// Only the administrator can read this folder; the project's reader (priya) cannot.
	if _, err := service.Call(t.Context(), admin, "create_knowledgebase_folder", map[string]any{"folder_path": "", "name": "Private", "request_id": "private-folder"}); err != nil {
		t.Fatal(err)
	}
	private := create(map[string]any{"folder_path": "Private"}, "private")

	parent := "brain-modes"
	common.SetSessionWorkflowPath(parent, workspace)
	defer common.ClearSessionShellConfig(parent)
	_, executors, _ := createKnowledgebaseTools("admin", parent, workspace)
	read := executors["brain_read"].(func(context.Context, map[string]interface{}) (string, error))
	update := executors["brain_update"].(func(context.Context, map[string]interface{}) (string, error))
	run := knowledgeTestCaller(context.WithValue(t.Context(), common.ChatSessionIDKey, parent), "admin")

	rootCtx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "admin", Username: "admin"})
	rootCtx = executor.WithSessionID(rootCtx, parent)
	rootCtx = context.WithValue(rootCtx, knowledgeProjectBuilderKey{}, knowledgeProjectBuilderAuthority{"admin", workspace, parent})
	rootCtx = context.WithValue(rootCtx, common.ChatSessionIDKey, parent)
	setMode := func(mode string) {
		t.Helper()
		out, err := knowledgeProjectBuilderExecute(rootCtx, "brain_access", map[string]any{"action": "inspect_project", "workspace_path": workspace})
		if err != nil {
			t.Fatal(err)
		}
		var inspected map[string]any
		if err := json.Unmarshal([]byte(out), &inspected); err != nil {
			t.Fatal(err)
		}
		if _, err := knowledgeProjectBuilderExecute(rootCtx, "brain_access", map[string]any{"action": "set_project_access", "workspace_path": workspace, "mode": mode, "expected_manifest_version": inspected["manifest_version"], "request_id": "mode-" + mode}); err != nil {
			t.Fatal("set", mode, err)
		}
	}

	// Open by default: a project that never chose a mode can read (and write, below) within its owner's roles.
	if out, err := read(run, map[string]any{"action": "read", "entry_id": shared}); err != nil || !strings.Contains(out, "shared-marker") {
		t.Fatalf("the open default could not read: %s %v", out, err)
	}
	setMode("read")
	if out, err := read(run, map[string]any{"action": "read", "entry_id": shared}); err != nil || !strings.Contains(out, "shared-marker") {
		t.Fatalf("read access could not read: %s %v", out, err)
	}
	if _, err := update(run, map[string]any{"action": "create", "folder_id": folderID, "filename": "denied.md", "title": "Denied", "type": "note", "content": "denied", "request_id": "read-mode-write"}); err == nil {
		t.Fatal("read access wrote to the Brain")
	}
	if _, err := read(run, map[string]any{"action": "read", "entry_id": private}); err == nil {
		t.Fatal("read access reached a folder an output reader cannot read")
	}
	// A step with no Brain access gets none, even while the project is on Read.
	step := "brain-modes-step-none"
	common.SetSessionWorkflowPath(step, workspace)
	common.SetSessionShellEnv(step, map[string]string{"SHARED_KB_STEP_ACCESS": "none"})
	defer common.ClearSessionShellConfig(step)
	if _, err := read(knowledgeTestCaller(context.WithValue(t.Context(), common.ChatSessionIDKey, step), "admin"), map[string]any{"action": "read", "entry_id": shared}); err == nil {
		t.Fatal("a no-Brain step read the Brain in Read mode")
	}
	// Read & write: the agents organize folders themselves wherever the owner may write, still bounded by the audience.
	setMode("write")
	if _, err := update(run, map[string]any{"action": "create_folder", "folder_id": folderID, "name": "Agent notes", "request_id": "write-mode-folder"}); err != nil {
		t.Fatal("read & write could not create a folder:", err)
	}
	if _, err := update(run, map[string]any{"action": "create", "folder_path": "Imported/Agent notes", "filename": "found.md", "title": "Found", "type": "note", "content": "written by the agent", "request_id": "write-mode-entry"}); err != nil {
		t.Fatal("read & write could not write:", err)
	}
	if _, err := read(run, map[string]any{"action": "read", "entry_id": private}); err == nil {
		t.Fatal("read & write reached a folder an output reader cannot read")
	}
	setMode("off")
	if _, err := read(run, map[string]any{"action": "read", "entry_id": shared}); err == nil {
		t.Fatal("turning Brain off did not stop reads")
	}
}

// Pins the owner decision that Code projects get Brain like workflows and Crews: open by default, acting as their
// owner. A Code project is private, so its owner is its whole audience.
func TestBrainCodeProjectUsesBrainAsItsOwner(t *testing.T) {
	service, admin, _, folderID := knowledgeIntegrationFixture(t)
	root := os.Getenv("WORKSPACE_DOCS_PATH")
	code := "_users/admin/" + workspaceref.CodeProjectsRoot + "/payments-code"
	if err := os.MkdirAll(filepath.Join(root, code), 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"workflow.json", "product.json"} {
		if err := os.WriteFile(filepath.Join(root, code, file), []byte(`{"id":"code-payments","owner_id":"admin","product":"code"}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	project, err := knowledgeProjectLoad(t.Context(), "admin", code, true)
	if err != nil {
		t.Fatal("Code project refused:", err)
	}
	if project.BrainMode() != "write" || len(project.Audience) != 1 || project.Audience[0] != "admin" {
		t.Fatalf("Code project: mode %q audience %v", project.BrainMode(), project.Audience)
	}
	seed, err := service.CallTool(t.Context(), admin, "brain_update", map[string]any{"action": "create", "folder_id": folderID, "filename": "code.md", "title": "Code", "type": "note", "content": "code-marker", "request_id": "code-seed"})
	if err != nil {
		t.Fatal(err)
	}
	session := "brain-code"
	common.SetSessionWorkingDir(session, code)
	defer common.ClearSessionShellConfig(session)
	_, executors, _ := createKnowledgebaseTools("admin", session, code)
	read, ok := executors["brain_read"].(func(context.Context, map[string]interface{}) (string, error))
	update, writable := executors["brain_update"].(func(context.Context, map[string]interface{}) (string, error))
	if !ok || !writable {
		t.Fatal("a Code session got no Brain tools")
	}
	run := knowledgeTestCaller(context.WithValue(t.Context(), common.ChatSessionIDKey, session), "admin")
	if out, err := read(run, map[string]any{"action": "read", "entry_id": knowledgeMap(seed)["entry_id"]}); err != nil || !strings.Contains(out, "code-marker") {
		t.Fatalf("Code could not read Brain: %s %v", out, err)
	}
	if _, err := update(run, map[string]any{"action": "create", "folder_id": folderID, "filename": "from-code.md", "title": "From Code", "type": "note", "content": "written by Code", "request_id": "code-write"}); err != nil {
		t.Fatal("Code could not write to Brain:", err)
	}
}
