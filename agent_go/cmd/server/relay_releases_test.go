package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path"
	"strings"
	"testing"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

func TestRelayPublishKeepsRunningVersionSeparateFromDraft(t *testing.T) {
	workspace := "Workflow/release-test"
	manifest := NewWorkflowManifest("Release test")
	manifest.Kind = "relay"
	manifest.RelayOutputStepID = "answer"
	manifest.Schedules = []WorkflowSchedule{{ID: "function-one", Name: "Hello", ScheduleType: "webhook", Kind: triggerKindFunction, Enabled: true, GroupNames: []string{"default"}, Function: &WorkflowFunctionSpec{Name: "hello", Inputs: []WorkflowFunctionInput{{Name: "INPUT", Type: "object", Required: true}}}}}
	manifestJSON, _ := json.Marshal(manifest)
	plan := &stepworkflow.PlanningResponse{Steps: []stepworkflow.PlanStepInterface{&stepworkflow.MessageSequencePlanStep{
		CommonStepFields: stepworkflow.CommonStepFields{ID: "answer", Title: "Answer", Description: "Return JSON"},
		AuthoredPrompt:   true, SystemPrompt: "Return JSON", NextStepID: "end",
		Items: []stepworkflow.MessageSequenceItem{{ID: "turn", Type: "user_message", Message: "{{input.name}}"}},
	}}}
	planJSON, _ := json.Marshal(plan)
	mock := &mockWorkspaceAPI{files: map[string]string{
		manifestPath(workspace):                             string(manifestJSON),
		workspace + "/planning/plan.json":                   string(planJSON),
		workspace + "/variables/variables.json":             `{"variables":[{"name":"INPUT","type":"object"}],"groups":[{"name":"default","enabled":true}]}`,
		workspace + "/code/answer/main.py":                  "print('draft one')\n",
		workspace + "/planning/step_config.json":            `{"steps":[{"id":"answer","agent_configs":{"enabled_custom_tools":["python_tools:lookup_customer"]}}]}`,
		workspace + "/code/tools/lookup_customer/tool.json": `{"description":"Customer lookup","parameters":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}}`,
		workspace + "/code/tools/lookup_customer/main.py":   "def run(input): return {\"version\": 1}\n",
		workspace + "/runs/iteration-0/output.json":         `{"ignore":true}`,
	}}
	server := httptest.NewServer(mock)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	ctx := context.Background()
	v1, err := publishRelayRelease(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if v1.Version != "v1" || v1.Hash == "" || v1.FileCount != 7 {
		t.Fatalf("v1 = %+v", v1)
	}
	republish, err := publishRelayRelease(ctx, workspace)
	if err != nil || republish.Version != "v1" {
		t.Fatalf("unchanged republish = %+v, %v", republish, err)
	}
	root := relayReleaseWorkspace(workspace, "v1")
	if identity, err := relayDraftWorkspaceForRelease(ctx, root); err != nil || identity != workspace {
		t.Fatalf("release credential identity = %q, %v", identity, err)
	}
	mock.mu.Lock()
	mock.files[root+"/.pi/APPEND_SYSTEM.md"] = "injected prompt"
	mock.mu.Unlock()
	if err := verifyRelayRelease(ctx, v1, root); err == nil {
		t.Fatal("hidden file added after publishing was accepted")
	}
	mock.mu.Lock()
	delete(mock.files, root+"/.pi/APPEND_SYSTEM.md")
	mock.mu.Unlock()
	if !mock.hasFolder(root) {
		t.Fatalf("release workspace %q was not created", root)
	}
	if _, exists, _ := readFileFromWorkspace(ctx, path.Join(root, "runs/iteration-0/output.json")); exists {
		t.Fatal("run history leaked into release")
	}
	mock.mu.Lock()
	mock.files[workspace+"/code/answer/main.py"] = "print('draft two')\n"
	mock.files[workspace+"/code/tools/lookup_customer/main.py"] = "def run(input): return {\"version\": 2}\n"
	mock.mu.Unlock()
	v2, err := publishRelayRelease(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Version != "v2" || v2.Hash == v1.Hash {
		t.Fatalf("v2 = %+v; v1 = %+v", v2, v1)
	}
	oldCode, _, _ := readFileFromWorkspace(ctx, path.Join(root, "code/answer/main.py"))
	newCode, _, _ := readFileFromWorkspace(ctx, path.Join(relayReleaseWorkspace(workspace, "v2"), "code/answer/main.py"))
	if !strings.Contains(oldCode, "draft one") || !strings.Contains(newCode, "draft two") {
		t.Fatalf("release code changed: v1=%q v2=%q", oldCode, newCode)
	}
	oldTool, _, _ := readFileFromWorkspace(ctx, path.Join(root, "code/tools/lookup_customer/main.py"))
	newTool, _, _ := readFileFromWorkspace(ctx, path.Join(relayReleaseWorkspace(workspace, "v2"), "code/tools/lookup_customer/main.py"))
	if !strings.Contains(oldTool, `"version": 1`) || !strings.Contains(newTool, `"version": 2`) {
		t.Fatalf("release tool source changed: v1=%q v2=%q", oldTool, newTool)
	}
	active, activeWorkspace, err := activeRelayRelease(ctx, workspace)
	if err != nil || active.Version != "v2" || activeWorkspace != relayReleaseWorkspace(workspace, "v2") {
		t.Fatalf("active = %+v %q %v", active, activeWorkspace, err)
	}
	selected, selectedWorkspace, err := resolveRelayRelease(ctx, workspace, "v1")
	if err != nil || selected.Version != "v1" || selectedWorkspace != root {
		t.Fatalf("selected = %+v %q %v", selected, selectedWorkspace, err)
	}
}

func TestRelayReleaseLogAccessFollowsLiveManifest(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","role":"creator"},{"id":"bob","username":"bob","role":"editor","products":["agentworks"]}]}`)
	draft := "Workflow/relay-access-test"
	release := relayReleaseWorkspace(draft, "v1")
	manifest := NewWorkflowManifest("Access test")
	manifest.Kind = "relay"
	manifest.Access = &WorkflowAccess{Owners: []string{"alice"}}
	raw, _ := json.Marshal(manifest)
	mock := &mockWorkspaceAPI{files: map[string]string{
		manifestPath(draft):   string(raw),
		manifestPath(release): string(raw),
	}}
	server := httptest.NewServer(mock)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	for _, tc := range []struct {
		user string
		want int
	}{{"bob", 403}, {"alice", 200}} {
		req := httptest.NewRequest("GET", "/api/workflow/logs?workspace_path="+release+"&run_folder=iteration-0/default", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: tc.user, Username: tc.user}))
		response := httptest.NewRecorder()
		(&StreamingAPI{}).handleGetExecutionLogs(response, req)
		if response.Code != tc.want {
			t.Errorf("%s release logs: got %d, want %d: %s", tc.user, response.Code, tc.want, response.Body.String())
		}
	}
}

func TestRelayBrokenReleaseRemainsVisible(t *testing.T) {
	draft := "Workflow/broken-release"
	mock := &mockWorkspaceAPI{files: map[string]string{relayReleaseWorkspace(draft, "v2") + "/release.json": "{broken", relayReleaseWorkspace(draft, "v1") + "/release.json": `{"version":"v1","hash":"test","files":["workflow.json"],"file_count":1}`}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	releases, err := listRelayReleases(context.Background(), draft)
	if err != nil || len(releases) != 2 || releases[0].Version != "v1" || releases[0].Error != "" || releases[1].Version != "v2" || releases[1].Error == "" {
		t.Fatalf("releases=%+v err=%v", releases, err)
	}
}
