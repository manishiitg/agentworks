package server

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestExternalBuilderPolicyIsOperationScopedAndNeverNative(t *testing.T) {
	req := QueryRequest{AgentMode: "workflow_phase", PhaseID: "workflow-builder", SelectedFolder: "Workflow/test", ExternalBuilderOperationID: "op-1"}
	p := resolveWorkflowChatPolicy("shared-browser-chat", req, nil, false)
	if p.Origin != "external_builder" || p.Mode != "builder" || !p.allows("plan_authoring") {
		t.Fatalf("missing external builder policy: %+v", p)
	}
	for _, cap := range []string{"user_management", "mcp_management", "secret_management", "bot_management"} {
		if p.allows(cap) {
			t.Fatalf("external policy granted %s", cap)
		}
	}
	if (&StreamingAPI{}).workflowChatNativeAgentTools(context.Background(), req, "shared-browser-chat", false) {
		t.Fatal("external turn admitted native tools")
	}
	req.ExternalBuilderOperationID = "op-2"
	other := resolveWorkflowChatPolicy("shared-browser-chat", req, nil, false)
	if p.sessionKey() == other.sessionKey() {
		t.Fatal("different operation reused runtime authority")
	}
	if !chatPolicyRoleRequiresReconnect(true, p.sessionKey(), other.sessionKey(), true, nil) {
		t.Fatal("operation change retained native process")
	}
	req.ExternalBuilderOperationID = ""
	web := resolveWorkflowChatPolicy("shared-browser-chat", req, nil, false)
	if web.Origin != "interactive" || web.sessionKey() == p.sessionKey() {
		t.Fatal("browser turn inherited MCP authority")
	}
	req.ExternalBuilderOperationID = "op-1"
	req.PinRunMode = true
	run := resolveWorkflowChatPolicy("shared-browser-chat", req, nil, readOnlyForRequest(WorkflowAccessOwner, req))
	if run.Mode != "run" || run.allows("plan_authoring") {
		t.Fatal("external provenance overrode Run pin")
	}
}

func TestExternalBuilderProvenanceCannotComeFromJSON(t *testing.T) {
	var req QueryRequest
	if err := json.Unmarshal([]byte(`{"external_builder_operation_id":"forged","ExternalBuilderOperationID":"forged"}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.ExternalBuilderOperationID != "" {
		t.Fatal("untrusted JSON supplied authority")
	}
	req.ExternalBuilderOperationID = "trusted"
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["ExternalBuilderOperationID"]; ok {
		t.Fatal("internal authority serialized to caller")
	}
}

func TestExternalBuilderFoldersExcludeAmbientGrants(t *testing.T) {
	req := QueryRequest{ExternalBuilderOperationID: "op", SelectedFolder: "Workflow/test", WorkflowContextPaths: []string{"Workflow/other"}, authorizedWorkflowContextReadPaths: []string{"Workflow/other"}}
	if err := admitWorkflowBuilderContextPaths(context.Background(), &req); err != nil {
		t.Fatal(err)
	}
	if len(req.WorkflowContextPaths) != 0 || len(req.authorizedWorkflowContextReadPaths) != 0 {
		t.Fatal("external turn inherited context roots")
	}
	read, write := externalBuilderFolderPaths(req.SelectedFolder)
	want := []string{"Workflow/test/"}
	if !reflect.DeepEqual(read, want) || !reflect.DeepEqual(write, want) {
		t.Fatalf("unexpected roots: %v %v", read, write)
	}
	for _, folder := range []string{"", "Workflow", "/tmp", "Workflow/../../private"} {
		read, write = externalBuilderFolderPaths(folder)
		if len(read) != 0 || len(write) != 0 {
			t.Fatalf("invalid root admitted: %q", folder)
		}
	}
	if externalBuilderHostDownloads(req, "test") != "" {
		t.Fatal("external turn received host downloads")
	}
}

func TestExternalBuilderFilePatchGuardProtectsManifestAndOtherRoots(t *testing.T) {
	called := false
	executors := map[string]func(context.Context, map[string]interface{}) (string, error){
		"diff_patch_workspace_file": func(context.Context, map[string]interface{}) (string, error) { called = true; return "ok", nil },
	}
	_, write := externalBuilderFolderPaths("Workflow/test")
	guarded := wrapExecutorsWithFolderGuard(executors, "test", "test", folderGuardContextWorkflow, nil, []string{"Workflow/test/workflow.json", "Workflow/test/planning/"}, write)
	for _, file := range []string{"Workflow/test/workflow.json", "Workflow/test/planning/plan.json", "Workflow/other/code.js", "Workflow/test/../other/code.js", "Downloads/out.txt", "_users/alice/chat_history/private.json"} {
		called = false
		_, err := guarded["diff_patch_workspace_file"](context.Background(), map[string]interface{}{"filepath": file, "patch": "change"})
		if err == nil || called {
			t.Fatalf("unsafe file edit reached executor: %s", file)
		}
	}
	if _, err := guarded["diff_patch_workspace_file"](context.Background(), map[string]interface{}{"filepath": "Workflow/test/code/app.py", "patch": "change"}); err != nil || !called {
		t.Fatal("workflow code edit rejected", err)
	}
}

func TestExternalBuilderPhaseRegistrarOmitsToolsOutsideCapability(t *testing.T) {
	base := &chatPolicyTestDefinition{}
	registrar := externalBuilderRegistrar(base, &UserClaims{ExternalBuilderOperationID: "operation"})
	run := func(context.Context, map[string]interface{}) (string, error) { return "", nil }
	for _, name := range []string{"read_file", "execute_shell_command", "set_workflow_secret", "set_workflow_access"} {
		if err := registrar.RegisterCustomTool(name, "", nil, run, "test"); err != nil {
			t.Fatal(err)
		}
	}
	if len(base.tools) != 1 {
		t.Fatalf("unexpected registered tools: %+v", base.tools)
	}
	if _, ok := base.tools["read_file"]; !ok {
		t.Fatal("allowed tool missing")
	}
}
