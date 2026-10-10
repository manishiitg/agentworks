package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

const crewWorkflowTestCrewPath = "_users/owner/Chats/Work/projects/project-a"

func newCrewWorkflowRunTestEnv(t *testing.T) (map[string]recordedTool, *mockWorkspaceAPI, *StreamingAPI) {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"reader","username":"reader","can_create":false}]}`)

	registry := agentprofiles.NewRegistry()
	work := agentprofiles.Profile{
		ID: "work", Name: "Work", Version: 1, SystemPromptTemplate: "hi", BuiltIn: true, Product: "work",
		Runtime: agentprofiles.RuntimePolicy{
			Transport:    "auto",
			Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject},
			Workspace:    agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, Root: "Chats", ProjectsRoot: "Chats/Work/projects"},
		},
		Features: []agentprofiles.FeatureBinding{{ID: "triggers"}},
		UIPanels: agentprofiles.UIPanels{Schedules: true},
	}
	if err := registry.RegisterProfile(work); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	crews := NewProductScheduleService(nil, registry)
	crews.readFile = func(_ context.Context, path string) (string, bool, error) {
		c, ok := files[path]
		return c, ok, nil
	}
	crews.writeFile = func(_ context.Context, path, content string) error {
		files[path] = content
		return nil
	}

	crewProduct := `{"schema_version":1,"product":"work","id":"project-a","title":"project-a","session_id":"sess-1"}`
	files[crewWorkflowTestCrewPath+"/product.json"] = crewProduct
	crewRuntime := `{"schema_version":1,"product":"work","id":"project-a","workflow_context_paths":["Workflow/test"]}`

	manifest := NewWorkflowManifest("Hook target")
	manifest.CreatedBy = "owner"
	manifest.Access = &WorkflowAccess{Owners: []string{"owner"}}
	manifest.Schedules = []WorkflowSchedule{
		{ID: "trig-crew-on", Name: "Crew bound", ScheduleType: "webhook", Enabled: true, WorkshopMode: "run", Kind: triggerKindInternal, Caller: &triggerCaller{Type: triggerCallerCrew, ID: "project-a", ProfileID: "work"}},
		{ID: "trig-crew-off", Name: "Crew disabled", ScheduleType: "webhook", Enabled: false, WorkshopMode: "run", Kind: triggerKindInternal, Caller: &triggerCaller{Type: triggerCallerCrew, ID: "project-a", ProfileID: "work"}},
		{ID: "trig-other", Name: "Other crew", ScheduleType: "webhook", Enabled: true, WorkshopMode: "run", Kind: triggerKindInternal, Caller: &triggerCaller{Type: triggerCallerCrew, ID: "other", ProfileID: "work"}},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	other := NewWorkflowManifest("Unattached")
	other.CreatedBy = "owner"
	other.Access = &WorkflowAccess{Owners: []string{"owner"}}
	otherRaw, err := json.Marshal(other)
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{
		manifestPath("Workflow/test"):               string(raw),
		"Workflow/test/planning/plan.json":          `{"steps":[{"type":"regular","id":"work","title":"Work","description":"Work"}]}`,
		"Workflow/test/variables/variables.json":    `{"variables":[],"groups":[{"name":"default"}]}`,
		manifestPath("Workflow/other"):              string(otherRaw),
		"Workflow/other/planning/plan.json":         `{"steps":[{"type":"regular","id":"work","title":"Work","description":"Work"}]}`,
		"Workflow/other/variables/variables.json":   `{"variables":[],"groups":[{"name":"default"}]}`,
		crewWorkflowTestCrewPath + "/product.json":  crewProduct,
		crewWorkflowTestCrewPath + "/workflow.json": crewRuntime,
	}}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)

	api := &StreamingAPI{productSchedules: crews}
	api.scheduler = NewSchedulerService(api)
	reg := &recordingRegistrar{}
	if err := api.registerCrewWorkflowRunTools(reg, "owner", "sess-1", crewWorkflowTestCrewPath); err != nil {
		t.Fatal(err)
	}
	return reg.tools, mock, api
}

func TestListAttachedWorkflows(t *testing.T) {
	tools, _, _ := newCrewWorkflowRunTestEnv(t)
	out, err := tools["list_attached_workflows"].exec(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	var listed struct {
		Workflows []struct {
			WorkspacePath string `json:"workspace_path"`
			Label         string `json:"label"`
			WorkflowID    string `json:"workflow_id"`
			Available     bool   `json:"available"`
		} `json:"workflows"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Workflows) != 1 || listed.Workflows[0].WorkspacePath != "Workflow/test" {
		t.Fatalf("workflows = %+v, want the one attached workflow", listed.Workflows)
	}
	if listed.Workflows[0].Label != "Hook target" || listed.Workflows[0].WorkflowID == "" || !listed.Workflows[0].Available {
		t.Fatalf("workflow = %+v, want resolved manifest", listed.Workflows[0])
	}
}

func TestListWorkflowTriggersAttachedOnly(t *testing.T) {
	tools, _, _ := newCrewWorkflowRunTestEnv(t)
	tool := tools["list_workflow_triggers"]
	out, err := tool.exec(context.Background(), map[string]interface{}{"workspace_path": "Workflow/test"})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if strings.Contains(strings.ToLower(out), "secret") {
		t.Fatalf("trigger list leaked secret material: %s", out)
	}
	var listed struct {
		Triggers []workflowWebhookResponse `json:"triggers"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Triggers) != 3 {
		t.Fatalf("triggers = %d, want 3", len(listed.Triggers))
	}
	for _, trigger := range listed.Triggers {
		if trigger.Kind != triggerKindInternal || trigger.Path != "" {
			t.Fatalf("internal trigger exposed endpoint material: %+v", trigger)
		}
	}
	if _, err := tool.exec(context.Background(), map[string]interface{}{"workspace_path": "Workflow/other"}); err == nil || !strings.Contains(err.Error(), "not attached") {
		t.Fatalf("unattached list err = %v, want attachment failure", err)
	}
}

func TestRunWorkflowTriggerRejectsBeforeStart(t *testing.T) {
	tools, _, _ := newCrewWorkflowRunTestEnv(t)
	tool := tools["run_workflow_trigger"]
	ctx := context.Background()
	for _, tt := range []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"unattached", map[string]interface{}{"workspace_path": "Workflow/other", "trigger_id": "trig-crew-on"}, "not attached"},
		{"unknown trigger", map[string]interface{}{"workspace_path": "Workflow/test", "trigger_id": "missing"}, "trigger not found"},
		{"foreign binding", map[string]interface{}{"workspace_path": "Workflow/test", "trigger_id": "trig-other"}, "no longer names this caller"},
		{"disabled binding", map[string]interface{}{"workspace_path": "Workflow/test", "trigger_id": "trig-crew-off"}, "disabled"},
		{"bad payload", map[string]interface{}{"workspace_path": "Workflow/test", "trigger_id": "trig-crew-on", "payload": "nope"}, "payload must be a JSON object"},
	} {
		if _, err := tool.exec(ctx, tt.args); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
	}
	oversize := map[string]interface{}{
		"workspace_path": "Workflow/test", "trigger_id": "trig-crew-on",
		"payload": map[string]interface{}{"blob": strings.Repeat("x", (1<<20)+1)},
	}
	if _, err := tool.exec(ctx, oversize); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("oversize payload err = %v, want 1 MiB failure", err)
	}
}

func TestCrewWorkflowTriggerBindingReuseAndCreate(t *testing.T) {
	tools, mock, api := newCrewWorkflowRunTestEnv(t)
	_ = tools
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	manifest, err := crewAttachedWorkflowManifest(ctx, crewWorkflowTestCrewPath, "Workflow/test")
	if err != nil {
		t.Fatal(err)
	}
	crew := triggerCaller{Type: triggerCallerCrew, ID: "project-a", ProfileID: "work"}
	reused, err := crewWorkflowTriggerBinding(ctx, api, "Workflow/test", manifest, crew)
	if err != nil {
		t.Fatalf("reuse failed: %v", err)
	}
	if reused != "trig-crew-on" {
		t.Fatalf("reused binding = %q, want trig-crew-on", reused)
	}

	plain := NewWorkflowManifest("Binding target")
	plain.CreatedBy = "stranger"
	plain.Access = &WorkflowAccess{Owners: []string{"stranger"}, Readers: []string{"owner"}}
	plainRaw, _ := json.Marshal(plain)
	mock.files[manifestPath("Workflow/plain")] = string(plainRaw)
	mock.files["Workflow/plain/planning/plan.json"] = `{"steps":[{"type":"regular","id":"work","title":"Work","description":"Work"}]}`
	mock.files["Workflow/plain/variables/variables.json"] = `{"variables":[],"groups":[{"name":"default"}]}`
	mock.files[crewWorkflowTestCrewPath+"/workflow.json"] = `{"schema_version":1,"product":"work","id":"project-a","workflow_context_paths":["Workflow/test","Workflow/plain"]}`
	createdManifest, err := crewAttachedWorkflowManifest(ctx, crewWorkflowTestCrewPath, "Workflow/plain")
	if err != nil {
		t.Fatal(err)
	}
	if access := workflowAccessForManifest(&UserClaims{UserID: "owner"}, createdManifest); access != WorkflowAccessRead {
		t.Fatalf("test setup access = %q, want read-only", access)
	}
	created, err := crewWorkflowTriggerBinding(ctx, api, "Workflow/plain", createdManifest, crew)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	var saved WorkflowManifest
	if err := json.Unmarshal([]byte(mock.files[manifestPath("Workflow/plain")]), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Schedules) != 1 || saved.Schedules[0].ID != created {
		t.Fatalf("binding not persisted: %+v", saved.Schedules)
	}
	binding := saved.Schedules[0]
	if !isInternalTriggerKind(binding.Kind) || binding.Caller == nil || binding.Caller.ID != "project-a" || binding.Webhook == nil || binding.Webhook.EncryptedSecret != "" {
		t.Fatalf("binding is not secretless internal: %+v", binding)
	}
	deniedCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "reader"})
	if _, err := crewWorkflowTriggerBinding(deniedCtx, api, "Workflow/plain", createdManifest, crew); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("unshared workflow binding err = %v, want access denial", err)
	}
}

func TestGetWorkflowTriggerRunPollsStore(t *testing.T) {
	tools, _, api := newCrewWorkflowRunTestEnv(t)
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	api.scheduler.stateStore = store
	ctx := context.Background()
	manifest, err := crewAttachedWorkflowManifest(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "owner"}), crewWorkflowTestCrewPath, "Workflow/test")
	if err != nil {
		t.Fatal(err)
	}
	var sched WorkflowSchedule
	for _, candidate := range manifest.Schedules {
		if candidate.ID == "trig-crew-on" {
			sched = candidate
		}
	}
	scope, scopeID, _ := scheduleStateScope(buildScheduleContext("Workflow/test", manifest, sched))
	if err := store.BeginRun(ctx, schedulerstate.Run{RunID: "run-1", LockKey: "k1", ScheduleID: "trig-crew-on", ScopeType: scope, ScopeID: scopeID, TriggerSource: "webhook", State: schedulerstate.State("running")}); err != nil {
		t.Fatal(err)
	}
	tool := tools["get_workflow_trigger_run"]
	out, err := tool.exec(ctx, map[string]interface{}{
		"workspace_path": "Workflow/test", "trigger_id": "trig-crew-on", "run_id": "run-1",
	})
	if err != nil {
		t.Fatalf("poll failed: %v", err)
	}
	var result webhookRunResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.RunID != "run-1" || result.Terminal {
		t.Fatalf("run status = %+v, want running run-1", result)
	}
	if _, err := tool.exec(ctx, map[string]interface{}{
		"workspace_path": "Workflow/test", "trigger_id": "trig-crew-on", "run_id": "missing",
	}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("missing run err = %v, want expiry failure", err)
	}
	if _, err := tool.exec(ctx, map[string]interface{}{
		"workspace_path": "Workflow/test", "trigger_id": "trig-other", "run_id": "run-1",
	}); err == nil || !strings.Contains(err.Error(), "no longer names this caller") {
		t.Fatalf("foreign binding poll err = %v, want caller failure", err)
	}
}

// A call already running keeps the binding it started under: only the supervision of a started call may read its run
// after the trigger's caller list changed; every other read still needs the caller on the list (PLAT-839).
func TestInFlightCallKeepsItsBindingWhenTheTriggerIsEdited(t *testing.T) {
	plain := context.Background()
	if internalRunReadAllowed(plain, false) {
		t.Fatal("a caller no longer on the list must not read a run")
	}
	if !internalRunReadAllowed(plain, true) {
		t.Fatal("a caller on the list may read a run")
	}
	supervising := context.WithValue(plain, inFlightCallRunReadKey{}, true)
	if !internalRunReadAllowed(supervising, false) {
		t.Fatal("the supervision of a call that already started must keep reading its run")
	}
}
