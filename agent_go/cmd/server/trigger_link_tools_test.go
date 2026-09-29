package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

const (
	linkAlphaPath = "_users/owner/Chats/Work/projects/alpha"
	linkBetaPath  = "_users/owner/Chats/Work/projects/beta"
	linkGammaPath = "_users/other/Chats/Work/projects/gamma"
)

type triggerLinkEnv struct {
	api  *StreamingAPI
	svc  *ProductScheduleService
	mock *mockWorkspaceAPI
}

func newTriggerLinkEnv(t *testing.T) triggerLinkEnv {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"other","username":"other","can_create":true}]}`)

	registry := agentprofiles.NewRegistry()
	if err := registry.RegisterProfile(agentprofiles.Profile{
		ID: "work", Name: "Work", Version: 1, SystemPromptTemplate: "hi", BuiltIn: true, Product: "work",
		Runtime: agentprofiles.RuntimePolicy{
			Transport:    "auto",
			Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject},
			Workspace:    agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, Root: "Chats", ProjectsRoot: "Chats/Work/projects"},
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
		"Workflow/reports/planning/plan.json":        `{"steps":[{"type":"regular","id":"work","title":"Work","description":"Work"}]}`,
		"Workflow/reports/variables/variables.json":  `{"variables":[],"groups":[{"name":"default"}]}`,
		manifestPath("Workflow/pipeline"):            workflow("Pipeline", &WorkflowAccess{Owners: []string{"owner"}}),
		"Workflow/pipeline/planning/plan.json":       `{"steps":[{"type":"regular","id":"work","title":"Work","description":"Work"}]}`,
		"Workflow/pipeline/variables/variables.json": `{"variables":[],"groups":[{"name":"default"}]}`,
		manifestPath("Workflow/shared"):              workflow("Shared", &WorkflowAccess{Owners: []string{"other"}, Readers: []string{"owner"}}),
	}}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)

	svc := NewProductScheduleService(nil, registry)
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
	api := &StreamingAPI{productSchedules: svc, bgAgentRegistry: NewBackgroundAgentRegistry(),
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

// connect binds the caller at callerPath (a Crew, or a workflow when
// workflow is true) to target the way a first function call does.
func (env triggerLinkEnv) connect(t *testing.T, callerPath string, workflow bool, target string) (string, bool, error) {
	t.Helper()
	claims := &UserClaims{UserID: "owner"}
	ctx := context.WithValue(context.Background(), UserContextKey, claims)
	resolveCaller := crewTriggerLinkCaller(callerPath)
	if workflow {
		resolveCaller = workflowTriggerLinkCaller(callerPath)
	}
	caller, err := resolveCaller(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveTriggerTarget(ctx, claims, target)
	if err != nil {
		return "", false, err
	}
	return env.api.connectTriggerTarget(ctx, "owner", caller, resolved)
}

func decodeToolJSON(t *testing.T, out string) map[string]interface{} {
	t.Helper()
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("tool output is not JSON: %v\n%s", err, out)
	}
	return decoded
}

// Crew A's first call to Crew B creates an ordinary internal trigger in B's
// own list, named for and bound to A, that always runs in A's own
// conversation with B; a second call reuses it.
func TestConnectCrewToCrewCreatesVisibleBoundTrigger(t *testing.T) {
	env := newTriggerLinkEnv(t)
	ctx := context.Background()

	firstID, created, err := env.connect(t, linkAlphaPath, false, "#crew:Beta")
	if err != nil || !created || firstID == "" {
		t.Fatalf("connect = %q created=%v err=%v", firstID, created, err)
	}
	triggers, err := env.svc.projectWebhookConfigs(ctx, "owner", "work", "beta")
	if err != nil || len(triggers) != 1 {
		t.Fatalf("beta triggers = %+v err=%v", triggers, err)
	}
	bound := triggers[0]
	if !bound.IsInternal() || !bound.Enabled || bound.Caller == nil || bound.Caller.Type != triggerCallerCrew || bound.Caller.ID != "alpha" {
		t.Fatalf("created trigger = %+v, want an enabled internal trigger bound to crew alpha", bound)
	}
	if bound.Name != "Called by Alpha Bot" || !strings.Contains(bound.Message, "Alpha Bot") {
		t.Fatalf("trigger does not record its caller: name=%q message=%q", bound.Name, bound.Message)
	}
	if !bound.ownConversation() || bound.RunDestination != runDestinationIsolated {
		t.Fatalf("caller binding must run in its own conversation: %+v", bound)
	}
	if bound.Webhook != nil {
		t.Fatalf("internal trigger carries secret material: %+v", bound.Webhook)
	}
	againID, created, err := env.connect(t, linkAlphaPath, false, "Beta")
	if err != nil || created || againID != firstID {
		t.Fatalf("second connect = %q created=%v err=%v, want reuse of %q", againID, created, err, firstID)
	}
}

// An existing caller binding saved with crew_chat still runs in its own
// conversation: the main chat is for people.
func TestCallerBindingNeverRunsInMainChat(t *testing.T) {
	trigger := productWebhookTrigger{ID: "t", Kind: triggerKindInternal, RunDestination: runDestinationCrewChat}
	if !trigger.ownConversation() {
		t.Fatal("internal trigger with crew_chat must still run in its own conversation")
	}
	if webhook := (productWebhookTrigger{ID: "w", RunDestination: runDestinationCrewChat}); webhook.ownConversation() {
		t.Fatal("an external webhook keeps its main-chat destination")
	}
	if webhook := (productWebhookTrigger{ID: "w", RunDestination: runDestinationIsolated}); !webhook.ownConversation() {
		t.Fatal("an external webhook may choose its own conversation")
	}
}

func TestTriggerLinkPermissions(t *testing.T) {
	env := newTriggerLinkEnv(t)
	for _, tt := range []struct {
		name, target, want string
	}{
		{"read-only workflow", "workflow:Shared", "only have read access"},
		{"itself", "Alpha Bot", "cannot connect to itself"},
		{"unknown", "nope", "no Crew or workflow you can edit"},
	} {
		_, _, err := env.connect(t, linkAlphaPath, false, tt.target)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
	}
}

// Crews are shared server-wide: a Crew calls another user's Crew, by name or
// physical path, and the binding lands in that Crew's own list under its
// owner.
func TestTriggerLinkReachesAnotherUsersCrew(t *testing.T) {
	env := newTriggerLinkEnv(t)
	ctx := context.Background()
	for _, target := range []string{"Gamma", linkGammaPath} {
		if triggerID, _, err := env.connect(t, linkAlphaPath, false, target); err != nil || triggerID == "" {
			t.Fatalf("connect %q to another user's crew = %q err=%v", target, triggerID, err)
		}
	}
	triggers, err := env.svc.projectWebhookConfigs(ctx, "other", "work", "gamma")
	if err != nil || len(triggers) != 1 || !triggers[0].IsInternal() {
		t.Fatalf("expected one reused binding on gamma, got %+v err=%v", triggers, err)
	}
	// A Crew caller from another owner may bind to a Crew trigger.
	if _, _, err := env.svc.saveProductWebhookConfig(ctx, "owner", productWebhookRequest{
		ProfileID: "work", ProjectID: "beta", Name: "From gamma", Message: "x", Enabled: true,
		Kind: triggerKindInternal, Caller: &triggerCaller{Type: triggerCallerCrew, ID: "gamma", ProfileID: "work"},
	}, ""); err != nil {
		t.Fatalf("binding another user's crew as caller: %v", err)
	}
	crews, err := listAccessibleCrewProjects(ctx, "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	var sawGamma bool
	for _, crew := range crews {
		if crew["workspace_path"] == linkGammaPath && crew["access"] == "write" && crew["owner"] == "other" {
			sawGamma = true
		}
	}
	if !sawGamma {
		t.Fatalf("list must include another user's crew with write access: %+v", crews)
	}
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
	for _, name := range []string{"list_functions", "call_function", "get_function_call", "reply_function_call", "ask_function_update"} {
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
	env := newTriggerLinkEnv(t)
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
	triggerID, _, err := env.api.connectTriggerTarget(ctx, "owner", caller, target)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := env.api.dispatchTargetTrigger(ctx, "owner", caller, target, triggerID, "d-1", crewFunctionEvent, map[string]interface{}{"task": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.api.sendToCrewTriggerRun(ctx, "owner", caller, target, triggerID, delivery.RunID, "more"); err == nil || !strings.Contains(err.Error(), "has not started yet") {
		t.Fatalf("follow-up before start: err = %v", err)
	}
	if err := UpdateScheduleRun(ctx, agentProfileRuntimeWorkspace("owner", linkBetaPath), delivery.RunID, "success", "", nil, "", "s"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.api.sendToCrewTriggerRun(ctx, "owner", caller, target, triggerID, delivery.RunID, "late"); err == nil || !strings.Contains(err.Error(), "already finished") {
		t.Fatalf("follow-up after finish: err = %v", err)
	}
}

// A workflow (Builder chat) calls a Crew (workflow→crew) and another
// workflow (workflow→workflow); a workflow cannot call itself.
func TestWorkflowCallerConnectsToCrewAndWorkflow(t *testing.T) {
	env := newTriggerLinkEnv(t)
	ctx := context.Background()

	if _, created, err := env.connect(t, "Workflow/pipeline", true, "#crew:Beta"); err != nil || !created {
		t.Fatalf("workflow→crew connect created=%v err=%v", created, err)
	}
	triggers, _ := env.svc.projectWebhookConfigs(ctx, "owner", "work", "beta")
	if len(triggers) != 1 || triggers[0].Caller.Type != triggerCallerWorkflow || triggers[0].Name != "Called by Pipeline" || !triggers[0].ownConversation() {
		t.Fatalf("crew trigger for workflow caller = %+v", triggers)
	}

	triggerID, _, err := env.connect(t, "Workflow/pipeline", true, "#workflow:Reports")
	if err != nil {
		t.Fatalf("workflow→workflow connect: %v", err)
	}
	manifest, _, err := ReadWorkflowManifest(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "owner"}), "Workflow/reports")
	if err != nil {
		t.Fatal(err)
	}
	var bound *WorkflowSchedule
	for i := range manifest.Schedules {
		if manifest.Schedules[i].ID == triggerID {
			bound = &manifest.Schedules[i]
		}
	}
	if bound == nil || !bound.IsInternalTrigger() || bound.Caller == nil || bound.Caller.Type != triggerCallerWorkflow || bound.Name != "Called by Pipeline" {
		t.Fatalf("workflow binding = %+v", bound)
	}
	if _, _, err := env.connect(t, "Workflow/pipeline", true, "workflow:Pipeline"); err == nil || !strings.Contains(err.Error(), "cannot connect to itself") {
		t.Fatalf("self connect err = %v", err)
	}
}

// Workflow triggers accept a workflow caller on dispatch and on poll.
func TestInternalWorkflowTriggerAcceptsWorkflowCaller(t *testing.T) {
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
		[]string{"_users/owner/Chats/Work/projects/beta", "Workflow/reports"},
		workflowContextLabels([]workflowContextRef{
			{Path: "Chats/Work/projects/beta", Label: "Beta `Bot`\n", Kind: "workflow"},
			{Path: "Workflow/reports/", Label: "Weekly Reports"},
		}),
	)
	for _, want := range []string{
		"**#crew:Beta Bot** (Crew) `_users/owner/Chats/Work/projects/beta/`",
		"**#workflow:Weekly Reports** (workflow) `Workflow/reports/`",
		"never a Slack channel",
		"call_function",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
