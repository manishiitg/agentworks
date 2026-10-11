package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuilderWebhookToolCreatesAndTestsSignedPing(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"reader","username":"reader","can_create":false}]}`)
	manifest := NewWorkflowManifest("API test")
	manifest.CreatedBy = "owner"
	manifest.Access = &WorkflowAccess{Owners: []string{"owner"}, Readers: []string{"reader"}}
	raw, _ := json.Marshal(manifest)
	mock := &mockWorkspaceAPI{files: map[string]string{
		manifestPath("Workflow/test"):            string(raw),
		"Workflow/test/planning/plan.json":       `{"steps":[{"type":"routing","id":"route","title":"Choose","routing_question":"Which?","routes":[{"route_id":"issue","route_name":"Issue","next_step_id":"component-branch"},{"route_id":"other","route_name":"Other","next_step_id":"work"}]},{"type":"branch","id":"component-branch","title":"Choose component","routes":[{"route_id":"review","route_name":"Review","next_step_id":"work"},{"route_id":"other-component","route_name":"Other","next_step_id":"work"}]},{"type":"scripted","id":"work","title":"Work","description":"Work"}]}`,
		"Workflow/test/variables/variables.json": `{"variables":[{"name":"base_url"}],"groups":[{"name":"default"}]}`,
	}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	svc := NewSchedulerService(&StreamingAPI{})

	api := &StreamingAPI{scheduler: svc}
	policy := workflowChatPolicy{Mode: "builder", Origin: "interactive", Capabilities: map[string]bool{"plan_authoring": true}}
	reg := &recordingRegistrar{}
	if err := api.registerWebhookTools(reg, "owner", "Workflow/test", policy); err != nil {
		t.Fatal(err)
	}
	tool := reg.tools["manage_workflow_webhook"]
	args := map[string]interface{}{"action": "create", "name": "PR smoke", "enabled": true, "auth_mode": "github", "route_selections": map[string]string{"route": "issue"}, "group_names": []string{"default"}, "input_mode": "envelope", "allowed_variables": []string{"base_url"}}
	output, err := tool.exec(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	var created workflowWebhookResponse
	if err := json.Unmarshal([]byte(output), &created); err != nil || len(created.Secret) != 64 {
		t.Fatal("missing generated secret")
	}

	if created.InputMode != "envelope" || len(created.AllowedVariables) != 1 {
		t.Fatal("builder configuration lost")
	}
	if created.MaxConcurrency != maxWebhookConcurrency {
		t.Fatalf("max concurrency = %d, want %d", created.MaxConcurrency, maxWebhookConcurrency)
	}
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc.stateStore = store
	if err := store.BeginRun(context.Background(), schedulerstate.Run{RunID: "test-run", ScopeType: "workflow", ScopeID: "Workflow/test", LockKey: "test", ScheduleID: created.ID, TriggerSource: "webhook"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Transition(context.Background(), schedulerstate.Transition{RunID: "test-run", To: schedulerstate.StateFailed}); err != nil {
		t.Fatal(err)
	}
	status, err := tool.exec(context.Background(), map[string]interface{}{"action": "status", "id": created.ID, "run_id": "test-run"})
	if err != nil || !strings.Contains(status, `"terminal":true`) || strings.Contains(status, created.Secret) {
		t.Fatalf("builder status: %s %v", status, err)
	}
	output, err = tool.exec(context.Background(), map[string]interface{}{"action": "list"})
	if err != nil || strings.Contains(output, created.Secret) || !strings.Contains(output, `"max_concurrency":4`) {
		t.Fatal("list exposed secret or failed")
	}
	mappedOutput, mappedErr := tool.exec(context.Background(), map[string]interface{}{
		"action": "create", "name": "Mapped raw delivery", "enabled": true, "auth_mode": "bearer", "input_mode": "raw",
		"group_names": []string{"default"}, "route_selections": map[string]string{"route": "issue"},
		"payload_mappings": map[string]interface{}{
			"group":  map[string]interface{}{"source": "env", "values": map[string]string{"prod": "default"}},
			"routes": map[string]interface{}{"component-branch": map[string]interface{}{"source": "component", "values": map[string]string{"service/review": "review"}}},
		},
	})
	if mappedErr != nil {
		t.Fatal(mappedErr)
	}
	var mapped workflowWebhookResponse
	if json.Unmarshal([]byte(mappedOutput), &mapped) != nil || mapped.PayloadMappings == nil || mapped.PayloadMappings.Group == nil || mapped.PayloadMappings.Routes["component-branch"].Values["service/review"] != "review" {
		t.Fatalf("payload mappings were not saved: %s", mappedOutput)
	}
	output, err = tool.exec(context.Background(), map[string]interface{}{"action": "test", "id": created.ID, "event": "ping", "payload": map[string]interface{}{}})
	if err != nil || !strings.Contains(output, "pong") || strings.Contains(output, created.Secret) {
		t.Fatalf("signed receiver ping failed: %v", err)
	}
	denied := &recordingRegistrar{}
	api.registerWebhookTools(denied, "reader", "Workflow/test", policy)
	if _, err := denied.tools["manage_workflow_webhook"].exec(context.Background(), args); err == nil {
		t.Fatal("reader created webhook")
	}
	if _, err := denied.tools["manage_workflow_webhook"].exec(context.Background(), map[string]interface{}{"action": "test", "id": created.ID, "payload": map[string]interface{}{}}); err == nil {
		t.Fatal("reader tested webhook")
	}
	run := &recordingRegistrar{}
	policy.Mode = "run"
	api.registerWebhookTools(run, "owner", "Workflow/test", policy)
	if len(run.tools) != 0 {
		t.Fatal("Run received management tool")
	}
	// Single-step targets survive updates that omit the target.
	args["action"] = "create"
	args["step_id"] = "work"
	args["route_selections"] = map[string]string{}
	stepOutput, stepErr := tool.exec(context.Background(), args)
	if stepErr != nil {
		t.Fatal(stepErr)
	}
	var stepTrigger workflowWebhookResponse
	if err := json.Unmarshal([]byte(stepOutput), &stepTrigger); err != nil || stepTrigger.StepID != "work" {
		t.Fatal("step target lost")
	}
	args["action"] = "update"
	args["id"] = stepTrigger.ID
	delete(args, "step_id")
	stepOutput, stepErr = tool.exec(context.Background(), args)
	if stepErr != nil {
		t.Fatal(stepErr)
	}
	if err := json.Unmarshal([]byte(stepOutput), &stepTrigger); err != nil || stepTrigger.StepID != "work" {
		t.Fatal("update cleared step target")
	}
	args["step_id"] = "missing"
	if _, err := tool.exec(context.Background(), args); err == nil {
		t.Fatal("missing step accepted")
	}
	args["step_id"] = "route"
	if _, err := tool.exec(context.Background(), args); err == nil {
		t.Fatal("routing node accepted as standalone step")
	}

}
