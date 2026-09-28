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
		manifestPath(workspace):                     string(manifestJSON),
		workspace + "/planning/plan.json":           string(planJSON),
		workspace + "/variables/variables.json":     `{"variables":[{"name":"INPUT","type":"object"}],"groups":[{"name":"default","enabled":true}]}`,
		workspace + "/code/answer/main.py":          "print('draft one')\n",
		workspace + "/runs/iteration-0/output.json": `{"ignore":true}`,
	}}
	server := httptest.NewServer(mock)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	ctx := context.Background()
	v1, err := publishRelayRelease(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if v1.Version != "v1" || v1.Hash == "" || v1.FileCount != 4 {
		t.Fatalf("v1 = %+v", v1)
	}
	republish, err := publishRelayRelease(ctx, workspace)
	if err != nil || republish.Version != "v1" {
		t.Fatalf("unchanged republish = %+v, %v", republish, err)
	}
	root := relayReleaseWorkspace(workspace, "v1")
	if !mock.hasFolder(root) {
		t.Fatalf("release workspace %q was not created", root)
	}
	if _, exists, _ := readFileFromWorkspace(ctx, path.Join(root, "runs/iteration-0/output.json")); exists {
		t.Fatal("run history leaked into release")
	}
	mock.mu.Lock()
	mock.files[workspace+"/code/answer/main.py"] = "print('draft two')\n"
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
	active, activeWorkspace, err := activeRelayRelease(ctx, workspace)
	if err != nil || active.Version != "v2" || activeWorkspace != relayReleaseWorkspace(workspace, "v2") {
		t.Fatalf("active = %+v %q %v", active, activeWorkspace, err)
	}
	selected, selectedWorkspace, err := resolveRelayRelease(ctx, workspace, "v1")
	if err != nil || selected.Version != "v1" || selectedWorkspace != root {
		t.Fatalf("selected = %+v %q %v", selected, selectedWorkspace, err)
	}
}
