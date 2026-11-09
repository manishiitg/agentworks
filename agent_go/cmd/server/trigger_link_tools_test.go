package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

const (
	linkAlphaPath = "Crew/alpha"
	linkBetaPath  = "Crew/beta"
	linkGammaPath = "Crew/gamma"
)

type triggerLinkEnv struct {
	api  *StreamingAPI
	svc  *ProjectActionDispatcher
	mock *mockWorkspaceAPI
}

func newTriggerLinkEnv(t *testing.T) triggerLinkEnv {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"other","username":"other","can_create":true}]}`)
	registerTestCrew(t, "alpha", "owner", true, "alpha")
	registerTestCrew(t, "beta", "owner", true, "beta")
	registerTestCrew(t, "gamma", "other", true, "gamma")

	registry := agentprofiles.NewRegistry()
	if err := registry.RegisterProfile(agentprofiles.Profile{
		ID: "work", Name: "Work", Version: 1, SystemPromptTemplate: "hi", BuiltIn: true, Product: "work",
		Runtime: agentprofiles.RuntimePolicy{
			Transport:    "auto",
			Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject},
			Workspace:    agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, Root: "Chats", ProjectsRoot: "Crew"},
		},
		Features: []agentprofiles.FeatureBinding{{ID: "triggers"}},
		UIPanels: agentprofiles.UIPanels{Schedules: true},
	}); err != nil {
		t.Fatal(err)
	}

	workflow := func(label string, access *WorkflowAccess) string {
		manifest := NewWorkflowManifest(label)
		manifest.CreatedBy = access.Owners[0]
		manifest.Access = access
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{
		linkAlphaPath + "/product.json": `{"schema_version":1,"product":"work","id":"alpha","title":"Alpha","identity":{"name":"Alpha Bot"},"session_id":"sess-alpha","triggers":[]}`,
		linkBetaPath + "/product.json":  `{"schema_version":1,"product":"work","id":"beta","title":"Beta","session_id":"sess-beta","triggers":[]}`,
		linkGammaPath + "/product.json": `{"schema_version":1,"product":"work","id":"gamma","title":"Gamma","session_id":"sess-gamma","triggers":[]}`,

		manifestPath("Workflow/reports"):             workflow("Reports", &WorkflowAccess{Owners: []string{"owner"}}),
		"Workflow/reports/planning/plan.json":        `{"steps":[{"type":"scripted","id":"work","title":"Work","description":"Work"}]}`,
		"Workflow/reports/variables/variables.json":  `{"variables":[],"groups":[{"name":"default"}]}`,
		manifestPath("Workflow/pipeline"):            workflow("Pipeline", &WorkflowAccess{Owners: []string{"owner"}}),
		"Workflow/pipeline/planning/plan.json":       `{"steps":[{"type":"scripted","id":"work","title":"Work","description":"Work"}]}`,
		"Workflow/pipeline/variables/variables.json": `{"variables":[],"groups":[{"name":"default"}]}`,
		manifestPath("Workflow/shared"):              workflow("Shared", &WorkflowAccess{Owners: []string{"other"}, Readers: []string{"owner"}}),
	}}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)

	svc := NewProjectActionDispatcher(nil, registry)
	svc.readFile = func(_ context.Context, path string) (string, bool, error) {
		mock.mu.Lock()
		defer mock.mu.Unlock()
		content, ok := mock.files[path]
		return content, ok, nil
	}
	svc.writeFile = func(_ context.Context, path, content string) error {
		mock.mu.Lock()
		defer mock.mu.Unlock()
		mock.files[path] = content
		return nil
	}
	// Mark the completion loops started: the test reads the watch's terminal
	// state from the registry instead of resuming a live chat.
	api := &StreamingAPI{projectActions: svc, bgAgentRegistry: NewBackgroundAgentRegistry(),
		completionLoopStarted: map[string]bool{"sess-caller": true, "sess-builder": true}}
	api.scheduler = NewSchedulerService(api)
	svc.api = api
	// Hold every Crew conversation (main chats and each caller's own) so
	// deliveries queue instead of starting live agent turns: the claim,
	// binding and poll wiring is under test.
	svc.conversations = map[string]bool{}
	svc.heldConversation = func(string) bool { return true }
	return triggerLinkEnv{api: api, svc: svc, mock: mock}
}

func decodeToolJSON(t *testing.T, out string) map[string]interface{} {
	t.Helper()
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("tool output is not JSON: %v\n%s", err, out)
	}
	return decoded
}

// Internal bindings default to isolation until dispatch resolves project owners.
func TestCallerBindingDefaultsToIsolationBeforeOwnerRouting(t *testing.T) {
	trigger := productWebhookTrigger{ID: "t", Kind: triggerKindInternal, RunDestination: runDestinationChat}
	if !trigger.ownConversation() {
		t.Fatal("internal trigger with chat must still run in its own conversation")
	}
	if webhook := (productWebhookTrigger{ID: "w", RunDestination: runDestinationChat}); webhook.ownConversation() {
		t.Fatal("an external webhook keeps its main-chat destination")
	}
	if webhook := (productWebhookTrigger{ID: "w", RunDestination: runDestinationIsolated}); !webhook.ownConversation() {
		t.Fatal("an external webhook may choose its own conversation")
	}
}

// Shared workflow functions are Run entry points; missing projects still refuse.
func TestTriggerTargetResolutionUsesCurrentRunAccess(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	newTriggerLinkEnv(t)
	claims := &UserClaims{UserID: "owner"}
	ctx := context.WithValue(context.Background(), UserContextKey, claims)
	if target, err := resolveTriggerTarget(ctx, claims, "workflow:Shared"); err != nil || target.Kind != triggerCallerWorkflow {
		t.Fatalf("shared workflow Run entry = %+v, err=%v", target, err)
	}
	if _, err := resolveTriggerTarget(ctx, claims, "nope"); err == nil || !strings.Contains(err.Error(), "no Crew or workflow you can use") {
		t.Fatalf("unknown target: %v", err)
	}
}

// A shared Crew is resolved by its name or canonical path. Calls use the
// owner's declared entry and do not synthesize a caller-specific binding.
func TestSharedCrewFunctionUsesDeclaredEntry(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newCrewFunctionEnv(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	ownerCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "other"})
	target, err := resolveTriggerTarget(ownerCtx, &UserClaims{UserID: "other"}, "Gamma")
	if err != nil {
		t.Fatal(err)
	}
	fn := savedFunctionForTest(t, ownerCtx, target, crewFunction{Name: "shared_task", Description: "Shared task", Instructions: "Complete the task."})
	caller, err := crewTriggerLinkCaller(linkAlphaPath)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Gamma", linkGammaPath} {
		resolved, err := resolveTriggerTarget(ctx, &UserClaims{UserID: "owner"}, name)
		if err != nil || resolved.CrewOwner != "other" || resolved.CrewID != target.CrewID {
			t.Fatalf("shared target %s: %+v, %v", name, resolved, err)
		}
		call, err := env.api.startCrewFunctionCall(ctx, "owner", caller, resolved, fn, nil, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if call.TriggerID != fn.TriggerID || call.RunID == "" {
			t.Fatalf("call lost declared entry/run identity: %+v", call.snapshot())
		}
		call.finish("completed", "fixture", "")
	}
	functions, err := readCrewFunctions(ownerCtx, target)
	if err != nil || len(functions) != 1 || functions[0].TriggerID != fn.TriggerID {
		t.Fatalf("calling changed declared entries: %+v, %v", functions, err)
	}
	crews, err := listAccessibleCrewProjects(ctx, "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, crew := range crews {
		if crew["workspace_path"] == linkGammaPath && crew["access"] == "run" && crew["owner"] == "other" {
			return
		}
	}
	t.Fatalf("shared Crew missing from accessible projects: %+v", crews)
}

// The Work product gate admits the function tools through the
// workflow-references feature (readers are excluded at registration); the
// retired *_target tools are gone.
func TestTriggerLinkToolsDeclaredByFeature(t *testing.T) {
	profile := agentprofiles.Profile{ID: "p", Features: []agentprofiles.FeatureBinding{{ID: "workflow-references"}}}
	if err := agentprofiles.ResolveFeatures(&profile); err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	for _, feature := range profile.ResolvedFeatures {
		for _, tool := range feature.Tools {
			declared[tool] = true
		}
	}
	for _, name := range []string{"project_functions_list", "project_functions_call", "project_functions_get_call", "project_functions_reply", "ask_function_update"} {
		if !declared[name] {
			t.Fatalf("%s is not declared by the workflow-references feature", name)
		}
	}
	for _, name := range []string{"connect_to_target", "call_target", "get_target_run", "send_to_target_run"} {
		if declared[name] {
			t.Fatalf("retired tool %s is still declared", name)
		}
	}
}

// A follow-up into a call's run is refused before the run starts and after
// it ends.
func TestFollowUpNeedsARunningCall(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newCrewFunctionEnv(t)
	claims := &UserClaims{UserID: "owner"}
	ctx := context.WithValue(context.Background(), UserContextKey, claims)
	caller, err := crewTriggerLinkCaller(linkAlphaPath)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveTriggerTarget(ctx, claims, "Beta")
	if err != nil {
		t.Fatal(err)
	}
	fn := savedFunctionForTest(t, ctx, target, crewFunction{Name: "follow_up", Description: "Follow up", Instructions: "Complete the task."})
	call, err := env.api.startCrewFunctionCall(ctx, "owner", caller, target, fn, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer call.finish("completed", "fixture", "")
	triggerID := fn.TriggerID

	if _, err := env.api.sendToCrewTriggerRun(ctx, "owner", caller, target, triggerID, call.RunID, "more"); err == nil || !strings.Contains(err.Error(), "has not started yet") {
		t.Fatalf("follow-up before start: err = %v", err)
	}
	if err := UpdateScheduleRun(ctx, agentProfileRuntimeWorkspace("owner", linkBetaPath), call.RunID, "success", "", nil, "", "s"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.api.sendToCrewTriggerRun(ctx, "owner", caller, target, triggerID, call.RunID, "late"); err == nil || !strings.Contains(err.Error(), "already finished") {
		t.Fatalf("follow-up after finish: err = %v", err)
	}
}

// Workflow triggers accept a workflow caller on dispatch and on poll.
func TestInternalWorkflowTriggerAcceptsWorkflowCaller(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{"Workflow/reports/variables/variables.json": `{"variables":[]}`}})
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	ctx := context.Background()
	manifest := &WorkflowManifest{ID: "reports", Schedules: []WorkflowSchedule{
		{ID: "trig-wf", ScheduleType: "webhook", Enabled: true, Kind: "internal", Caller: &triggerCaller{Type: "workflow", ID: "pipeline"}},
	}}
	receiver := webhookReceiver{
		existing: func(context.Context, string) (schedulerstate.Run, error) {
			return schedulerstate.Run{}, schedulerstate.ErrRunNotFound
		},
		start: func(_ string, _ string, _ string, input *WorkflowWebhookDelivery) (string, error) {
			return input.RunID, nil
		},
	}
	call := internalWorkflowTriggerCall{WorkflowID: "reports", TriggerID: "trig-wf", Caller: triggerCaller{Type: "workflow", ID: "pipeline"}, DeliveryID: "d1", Payload: []byte(`{"task":"x"}`)}
	if result, err := receiver.dispatchInternal(ctx, "Workflow/reports", manifest, "trig-wf", call); err != nil || result.Status != "accepted" {
		t.Fatalf("workflow→workflow dispatch = %+v err=%v", result, err)
	}
	call.Caller = triggerCaller{Type: "crew", ID: "pipeline", ProfileID: "work"}
	if _, err := receiver.dispatchInternal(ctx, "Workflow/reports", manifest, "trig-wf", call); !errors.Is(err, ErrInternalCallerMismatch) {
		t.Fatalf("crew presenting a workflow binding: err = %v", err)
	}
}

func TestWorkflowContextPromptListsTypedTags(t *testing.T) {
	prompt := buildWorkflowContextPromptWithLabels(
		[]string{"Crew/beta", "Workflow/reports"},
		workflowContextLabels([]workflowContextRef{
			{Path: "Crew/beta", Label: "Beta `Bot`\n", Kind: "workflow"},
			{Path: "Workflow/reports/", Label: "Weekly Reports"},
		}),
	)
	for _, want := range []string{
		"**#crew:Beta Bot** (Crew) `Crew/beta/`",
		"**#workflow:Weekly Reports** (workflow) `Workflow/reports/`",
		"work-workflow-files",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
