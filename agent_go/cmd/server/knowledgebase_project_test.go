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
		return callRemoteTool(t, t.Context(), cli, externalMCPToolCall, map[string]any{"name": "manage_knowledgebase_access", "arguments": args})
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
	read := executors["read_knowledgebase"].(func(context.Context, map[string]interface{}) (string, error))
	update := executors["update_knowledgebase"].(func(context.Context, map[string]interface{}) (string, error))
	args := map[string]any{"action": "bind_project", "workspace_path": workspace, "folder_id": folderID, "alias": "kbtest", "access": "read", "expected_manifest_version": current["manifest_version"], "request_id": "mcp-bind-test"}
	bound := result(args)
	if result(args)["manifest_version"] != bound["manifest_version"] {
		t.Fatal("binding retry changed manifest")
	}
	entry, err := service.CallTool(t.Context(), admin, "update_knowledgebase", map[string]any{"action": "create", "folder_id": folderID, "filename": "smoke.md", "title": "Smoke", "type": "note", "content": "kb-marker-71831", "request_id": "marker-create"})
	if err != nil {
		t.Fatal(err)
	}
	entryID := knowledgeMap(entry)["entry_id"]
	readArgs := map[string]any{"action": "read", "entry_id": entryID, "binding_alias": "kbtest"}
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
		if _, err := update(ctx, map[string]any{"action": "create", "folder_id": folderID, "filename": "denied-" + access + ".md", "title": "Denied", "type": "note", "content": "denied", "binding_alias": "kbtest", "request_id": "denied-step-" + access}); err == nil {
			t.Fatalf("%s bypassed read binding", access)
		}
	}
	// Builder setup uses the same action, constrained to its authenticated root.
	rootCtx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "admin", Username: "admin"})
	rootCtx = executor.WithSessionID(rootCtx, parent)
	rootCtx = context.WithValue(rootCtx, knowledgeProjectBuilderKey{}, knowledgeProjectBuilderAuthority{"admin", workspace, parent})
	rootCtx = context.WithValue(rootCtx, common.ChatSessionIDKey, parent)
	version := result(inspect)["manifest_version"]
	writeArgs := map[string]any{"action": "bind_project", "workspace_path": workspace, "folder_id": folderID, "alias": "kbtest", "access": "write", "expected_manifest_version": version, "request_id": "builder-write-bind"}
	if _, err := knowledgeProjectBuilderExecute(context.WithValue(rootCtx, common.ChatSessionIDKey, "project-step-read"), "manage_knowledgebase_access", writeArgs); err == nil {
		t.Fatal("child inherited Builder setup")
	}
	if _, err := knowledgeProjectBuilderExecute(rootCtx, "manage_knowledgebase_access", writeArgs); err != nil {
		t.Fatal("builder attachment", err)
	}
	ctx := knowledgeTestCaller(context.WithValue(t.Context(), common.ChatSessionIDKey, "project-step-write"), "admin")
	if _, err := update(ctx, map[string]any{"action": "create", "folder_id": folderID, "filename": "step.md", "title": "Step", "type": "note", "content": "step-created", "binding_alias": "kbtest", "request_id": "write-step"}); err != nil {
		t.Fatal("write step", err)
	}
	ctx = knowledgeTestCaller(context.WithValue(t.Context(), common.ChatSessionIDKey, "project-step-read"), "admin")
	if _, err := update(ctx, map[string]any{"action": "create", "folder_id": folderID, "filename": "readonly.md", "title": "Denied", "type": "note", "content": "denied", "binding_alias": "kbtest", "request_id": "read-step-rejected"}); err == nil {
		t.Fatal("read step inherited parent write")
	}
	if _, err := read(ctx, map[string]any{"action": "read", "path": "Payments/Checkout/retries.md"}); err == nil {
		t.Fatal("step escaped bound folder")
	}
	if err := store.Revoke(t.Context(), token.ID, "admin", time.Now()); err != nil {
		t.Fatal(err)
	}
	requireRemoteError(t, call(cli, args), "revoked binding retry", "FORBIDDEN")
}

func TestKnowledgebaseProjectUIUsesOwnerAndAudienceChecks(t *testing.T) {
	service, admin, workspace, folderID := knowledgeIntegrationFixture(t)
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
	args := map[string]any{"action": "bind_project", "workspace_path": workspace, "folder_id": folderID, "alias": "ui", "access": "read", "expected_manifest_version": project.Version, "request_id": "ui-bind"}
	if w := request("priya", http.MethodPost, args); w.Code != 403 {
		t.Fatal("reader changed binding", w.Code, w.Body.String())
	}
	if w := request("admin", http.MethodPost, args); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	args["request_id"] = "stale-ui-bind"
	if w := request("admin", http.MethodPost, args); w.Code != 409 {
		t.Fatal("stale UI accepted", w.Code, w.Body.String())
	}
	project, _ = knowledgeProjectLoad(t.Context(), "admin", workspace, true)
	args["expected_manifest_version"] = project.Version
	args["request_id"] = "private-ui-bind"
	private, err := service.Call(t.Context(), admin, "create_knowledgebase_folder", map[string]any{"folder_path": "", "name": "Private", "request_id": "private-folder"})
	if err != nil {
		t.Fatal(err)
	}
	args["folder_id"] = knowledgeMap(private)["folder_id"]
	if w := request("admin", http.MethodPost, args); w.Code != 403 {
		t.Fatal("audience check bypassed", w.Code, w.Body.String())
	}
	// Existing selection survives a denied replacement and can be detached.
	fresh, _ := knowledgeProjectLoad(t.Context(), "admin", workspace, true)
	if fresh.Version != project.Version {
		t.Fatal("denied binding mutated manifest")
	}
	delete(args, "folder_id")
	delete(args, "access")
	args["action"] = "unbind_project"
	args["request_id"] = "ui-unbind"
	if w := request("admin", http.MethodPost, args); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestKnowledgebaseProjectBuilderAuthorityMatchesVaultIsolation(t *testing.T) {
	claims := &UserClaims{UserID: "admin"}
	req := QueryRequest{AgentMode: "workflow_phase", PhaseID: "workflow-builder", SelectedFolder: "Workflow/payments"}
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
	req := QueryRequest{AgentMode: "workflow_phase", PhaseID: "workflow-builder", SelectedFolder: workspace}
	root, err := api.bindToolExecutionContext(input, "kb-root", req, false)(executor.WithSessionID(t.Context(), "kb-root"), "manage_knowledgebase_access")
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := root.Value(knowledgeProjectBuilderKey{}).(knowledgeProjectBuilderAuthority); !ok || value.UserID != "admin" || value.Workspace != workspace {
		t.Fatal("missing root authority")
	}
	child, err := api.bindToolExecutionContextForSession(input, "kb-root", "kb-child", req, false)(root, "manage_knowledgebase_access")
	// The incoming root session is intentionally rejected for a child binding.
	if err == nil {
		t.Fatal("root caller admitted as child")
	}
	child, err = api.bindToolExecutionContextForSession(input, "kb-root", "kb-child", req, false)(context.WithValue(executor.WithSessionID(t.Context(), "kb-child"), knowledgeProjectBuilderKey{}, root.Value(knowledgeProjectBuilderKey{})), "manage_knowledgebase_access")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := child.Value(knowledgeProjectBuilderKey{}).(knowledgeProjectBuilderAuthority); ok {
		t.Fatal("child inherited setup")
	}
}
