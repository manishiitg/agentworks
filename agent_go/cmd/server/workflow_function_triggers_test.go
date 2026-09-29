package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

func reviewPRTrigger() WorkflowSchedule {
	return WorkflowSchedule{
		ID: "fn-review", Name: "Review PR", ScheduleType: "webhook", Enabled: true, Kind: triggerKindFunction,
		GroupNames: []string{"prod", "staging"}, WorkshopMode: "run", Webhook: &WorkflowWebhookConfig{},
		Function: &WorkflowFunctionSpec{Name: "review_pr", Description: "Review one pull request", Inputs: []WorkflowFunctionInput{
			{Name: "GITHUB_OWNER", Required: true},
			{Name: "GITHUB_REPO", Required: true},
			{Name: "PR_NUMBER", Type: "integer", Required: true, Description: "Pull request number"},
			{Name: "REVIEW_DEPTH", Enum: []string{"quick", "full"}},
		}},
	}
}

func TestWorkflowFunctionSpecValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		spec *WorkflowFunctionSpec
		want string
	}{
		{"missing", nil, "needs function.name"},
		{"bad name", &WorkflowFunctionSpec{Name: "Review PR"}, "snake_case"},
		{"reserved input", &WorkflowFunctionSpec{Name: "run", Inputs: []WorkflowFunctionInput{{Name: "group"}}}, "reserved"},
		{"duplicate input", &WorkflowFunctionSpec{Name: "run", Inputs: []WorkflowFunctionInput{{Name: "A"}, {Name: "A"}}}, "twice"},
		{"bad type", &WorkflowFunctionSpec{Name: "run", Inputs: []WorkflowFunctionInput{{Name: "A", Type: "object"}}}, "type must be"},
	} {
		if err := validateWorkflowFunctionSpec(tt.spec); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
	}
	sched := reviewPRTrigger()
	if err := validateWebhookSchedule(sched); err != nil {
		t.Fatalf("valid function trigger rejected: %v", err)
	}
	sched.Caller = &triggerCaller{Type: triggerCallerCrew, ID: "alpha", ProfileID: "work"}
	if err := validateWebhookSchedule(sched); err == nil {
		t.Fatal("a function trigger must not carry a caller stamp")
	}
	if !reviewPRTrigger().IsInternalTrigger() {
		t.Fatal("a function trigger has no public URL")
	}
}

// The #149 incident: a call without the PR number must fail before anything
// runs instead of falling back to a saved PR_NUMBER.
func TestWorkflowFunctionArgs(t *testing.T) {
	sched := reviewPRTrigger()
	if _, _, err := workflowFunctionArgs(sched, map[string]interface{}{"GITHUB_OWNER": "acme", "GITHUB_REPO": "app"}); err == nil || !strings.Contains(err.Error(), "missing required input PR_NUMBER") {
		t.Fatalf("missing PR number: err = %v", err)
	}
	if _, _, err := workflowFunctionArgs(sched, map[string]interface{}{"GITHUB_OWNER": "acme", "GITHUB_REPO": "app", "PR_NUMBER": float64(149), "task": "review it"}); err == nil || !strings.Contains(err.Error(), `unknown input "task"`) {
		t.Fatalf("free-text task: err = %v", err)
	}
	if _, _, err := workflowFunctionArgs(sched, map[string]interface{}{"GITHUB_OWNER": "acme", "GITHUB_REPO": "app", "PR_NUMBER": "abc"}); err == nil || !strings.Contains(err.Error(), "PR_NUMBER must be an integer") {
		t.Fatalf("bad type: err = %v", err)
	}
	if _, _, err := workflowFunctionArgs(sched, map[string]interface{}{"GITHUB_OWNER": "acme", "GITHUB_REPO": "app", "PR_NUMBER": float64(1), "REVIEW_DEPTH": "deep"}); err == nil || !strings.Contains(err.Error(), "must be one of quick, full") {
		t.Fatalf("enum: err = %v", err)
	}
	if _, _, err := workflowFunctionArgs(sched, map[string]interface{}{"GITHUB_OWNER": "acme", "GITHUB_REPO": "app", "PR_NUMBER": float64(1), "group": "dev"}); err == nil || !strings.Contains(err.Error(), "group must be one of prod, staging") {
		t.Fatalf("group: err = %v", err)
	}
	variables, group, err := workflowFunctionArgs(sched, map[string]interface{}{"GITHUB_OWNER": "acme", "GITHUB_REPO": "app", "PR_NUMBER": float64(149), "group": "staging"})
	if err != nil {
		t.Fatal(err)
	}
	if variables["PR_NUMBER"] != "149" || variables["GITHUB_OWNER"] != "acme" || group != "staging" {
		t.Fatalf("variables = %v group = %q", variables, group)
	}
	schema := workflowFunctionInputSchema(sched)
	required, _ := schema["required"].([]interface{})
	if len(required) != 3 {
		t.Fatalf("required = %v, want the three required inputs", required)
	}
	if _, ok := schema["properties"].(map[string]interface{})["group"]; !ok {
		t.Fatal("a multi-group function offers group")
	}
}

// The validated inputs become the run's variables directly.
func TestDeliverFunctionSetsRunVariables(t *testing.T) {
	var got *WorkflowWebhookDelivery
	receiver := webhookReceiver{
		existing: func(context.Context, string) (schedulerstate.Run, error) {
			return schedulerstate.Run{}, schedulerstate.ErrRunNotFound
		},
		start: func(_ string, _ string, _ string, input *WorkflowWebhookDelivery) (string, error) {
			got = input
			return input.RunID, nil
		},
	}
	sched := reviewPRTrigger()
	result, err := receiver.deliverFunction(context.Background(), "gate", "Workflow/gate", sched, "call-1", []byte(`{"function":"review_pr"}`), map[string]string{"PR_NUMBER": "149"}, "staging")
	if err != nil || result.Status != "accepted" {
		t.Fatalf("deliver = %+v err=%v", result, err)
	}
	if got == nil || got.Variables["PR_NUMBER"] != "149" || got.Group != "staging" || got.Event != "agentworks.function.review_pr" {
		t.Fatalf("delivery = %+v", got)
	}
}

func TestWorkflowFunctionCallerAllowList(t *testing.T) {
	spec := &WorkflowFunctionSpec{Name: "review_pr"}
	sde := triggerCaller{Type: triggerCallerCrew, ID: "sde", ProfileID: "work"}
	if !workflowFunctionCallerAllowed(spec, sde) {
		t.Fatal("no allow-list means anyone who can run the workflow")
	}
	spec.AllowedCallers = []triggerCaller{{Type: triggerCallerCrew, ID: "sde", ProfileID: "work"}}
	if !workflowFunctionCallerAllowed(spec, sde) {
		t.Fatal("listed caller refused")
	}
	if workflowFunctionCallerAllowed(spec, triggerCaller{Type: triggerCallerCrew, ID: "other", ProfileID: "work"}) {
		t.Fatal("unlisted caller allowed")
	}
}

// A Crew sees a workflow's function triggers, no free-text ask, and cannot
// define functions on it; a call with a missing input fails before any run.
func TestCrewSeesWorkflowFunctions(t *testing.T) {
	env := newCrewFunctionEnv(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	manifest, _, err := ReadWorkflowManifest(ctx, "Workflow/reports")
	if err != nil {
		t.Fatal(err)
	}
	manifest.Schedules = append(manifest.Schedules, reviewPRTrigger())
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	env.mock.mu.Lock()
	env.mock.files[manifestPath("Workflow/reports")] = string(raw)
	env.mock.mu.Unlock()

	out, err := env.alpha["list_functions"].exec(context.Background(), map[string]interface{}{"target": "#workflow:Reports"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "review_pr") || !strings.Contains(out, `"ask"`) {
		t.Fatalf("workflow functions = %s, want review_pr and the assistant ask", out)
	}
	if _, err := env.alpha["call_function"].exec(context.Background(), map[string]interface{}{"target": "#workflow:Reports", "function": "review_pr", "args": map[string]interface{}{"GITHUB_OWNER": "acme", "GITHUB_REPO": "app"}}); err == nil || !strings.Contains(err.Error(), "PR_NUMBER") {
		t.Fatalf("missing input must fail before running: err = %v", err)
	}
	if _, err := env.alpha["define_function"].exec(context.Background(), map[string]interface{}{"target": "#workflow:Reports", "name": "x", "description": "d", "instructions": "y", "input_schema": map[string]interface{}{"type": "object"}, "result_schema": map[string]interface{}{"type": "object"}}); err == nil || !strings.Contains(err.Error(), "function triggers") {
		t.Fatalf("define on a workflow: err = %v", err)
	}
	if _, err := env.alpha["call_function"].exec(context.Background(), map[string]interface{}{"target": "#workflow:Pipeline", "function": "run"}); err == nil || !strings.Contains(err.Error(), "has no function") {
		t.Fatalf("workflow without that function: err = %v", err)
	}
}

// MCP/CLI: list shows the function; a call missing a required input is
// refused before anything runs; readers cannot call.
func TestExternalWorkflowFunctions(t *testing.T) {
	env := newTriggerLinkEnv(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	manifest, _, err := ReadWorkflowManifest(ctx, "Workflow/reports")
	if err != nil {
		t.Fatal(err)
	}
	manifest.Schedules = append(manifest.Schedules, reviewPRTrigger())
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	env.mock.mu.Lock()
	env.mock.files[manifestPath("Workflow/reports")] = string(raw)
	env.mock.mu.Unlock()
	selected := DiscoveredWorkflow{WorkspacePath: "Workflow/reports", Manifest: manifest}
	claims := &UserClaims{UserID: "owner", Username: "owner"}
	request := func(name string, args map[string]any, access WorkflowAccessLevel) (int, map[string]any) {
		req := httptest.NewRequest("POST", "/api/external/call", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
		rec := httptest.NewRecorder()
		env.api.externalWorkflowFunctionCall(rec, req, name, args, selected, access)
		out := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, out := request("list_workflow_functions", map[string]any{}, WorkflowAccessOwner)
	functions, _ := out["functions"].([]any)
	if code != 200 || len(functions) != 2 || functions[0].(map[string]any)["name"] != "review_pr" || functions[1].(map[string]any)["name"] != "ask" {
		t.Fatalf("list = %d %v", code, out)
	}
	code, out = request("call_workflow_function", map[string]any{"function": "review_pr", "args": map[string]any{"GITHUB_OWNER": "acme", "GITHUB_REPO": "app"}}, WorkflowAccessOwner)
	if code != 400 || !strings.Contains(fmt.Sprint(out["error"]), "missing required input PR_NUMBER") {
		t.Fatalf("missing input = %d %v", code, out)
	}
	if code, _ := request("call_workflow_function", map[string]any{"function": "review_pr"}, WorkflowAccessRead); code != 403 {
		t.Fatalf("reader call = %d, want 403", code)
	}
	if code, _ := request("call_workflow_function", map[string]any{"function": "nope"}, WorkflowAccessOwner); code != 404 {
		t.Fatalf("unknown function = %d, want 404", code)
	}
}

// ask on a workflow goes to its Run-mode assistant in one continuing thread
// per caller; it never starts a run by itself.
func TestWorkflowAskUsesCallersAssistantThread(t *testing.T) {
	env := newCrewFunctionEnv(t)
	var mu sync.Mutex
	var turns []map[string]interface{}
	var sessions []string
	previous := workflowAskTurn
	workflowAskTurn = func(_ *StreamingAPI, _ context.Context, reqMap map[string]interface{}, sessionID, _ string) (internalSessionTurnResult, error) {
		mu.Lock()
		defer mu.Unlock()
		turns = append(turns, reqMap)
		sessions = append(sessions, sessionID)
		return internalSessionTurnResult{FinalResponse: "It reviews pull requests: review_pr(owner, repo, pr)."}, nil
	}
	t.Cleanup(func() { workflowAskTurn = previous })

	out, err := env.alpha["list_functions"].exec(context.Background(), map[string]interface{}{"target": "#workflow:Reports"})
	if err != nil || !strings.Contains(out, `"ask"`) || !strings.Contains(out, "assistant") {
		t.Fatalf("workflow functions = %s err=%v, want the assistant ask", out, err)
	}
	for i := 0; i < 2; i++ {
		out, err = env.alpha["call_function"].exec(context.Background(), map[string]interface{}{"target": "#workflow:Reports", "function": "ask", "args": map[string]interface{}{"message": "what can you do?"}, "wait_seconds": 3})
		if err != nil || !strings.Contains(out, "It reviews pull requests") || !strings.Contains(out, `"completed"`) {
			t.Fatalf("ask = %s err=%v", out, err)
		}
	}
	if _, err := env.alpha["call_function"].exec(context.Background(), map[string]interface{}{"target": "#workflow:Reports", "function": "ask", "args": map[string]interface{}{"message": " "}}); err == nil {
		t.Fatal("an empty ask must be refused")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sessions) != 2 || sessions[0] != sessions[1] || !strings.HasPrefix(sessions[0], "wfask-") {
		t.Fatalf("sessions = %v, want one continuing thread", sessions)
	}
	req := turns[0]
	if req["pin_run_mode"] != true || req["agent_mode"] != "workflow_phase" || !strings.Contains(fmt.Sprint(req["query"]), "Alpha Bot") || !strings.Contains(fmt.Sprint(req["query"]), "submit_workflow_suggestion") {
		t.Fatalf("assistant request = %v", req)
	}
	if other := workflowAskSessionID("reports", triggerCaller{Type: triggerCallerCrew, ID: "beta", ProfileID: "work"}); other == sessions[0] {
		t.Fatal("another caller must get its own thread")
	}
}

// The run-start check must accept a function's own inputs (RTS 2026-09-24:
// review_pr failed with "GITHUB_OWNER is no longer allowed" because function
// triggers have no allowed_variables list).
func TestFunctionTriggerVariablesPassRunStartCheck(t *testing.T) {
	sched := reviewPRTrigger()
	input := &WorkflowWebhookDelivery{Variables: map[string]string{"GITHUB_OWNER": "acme", "GITHUB_REPO": "app", "PR_NUMBER": "149"}, Group: "staging"}
	if err := webhookDeliveryStartError(sched, input); err != nil {
		t.Fatalf("function inputs rejected at run start: %v", err)
	}
	input.Variables["OTHER"] = "x"
	if err := webhookDeliveryStartError(sched, input); err == nil || !strings.Contains(err.Error(), `"OTHER"`) {
		t.Fatalf("undeclared variable: err = %v", err)
	}
	hook := WorkflowSchedule{ScheduleType: "webhook", Webhook: &WorkflowWebhookConfig{AllowedVariables: []string{"PR_NUMBER"}}}
	if err := webhookDeliveryStartError(hook, &WorkflowWebhookDelivery{Variables: map[string]string{"PR_NUMBER": "1"}}); err != nil {
		t.Fatalf("webhook allow-list: %v", err)
	}
	if err := webhookDeliveryStartError(hook, &WorkflowWebhookDelivery{Variables: map[string]string{"GITHUB_OWNER": "a"}}); err == nil {
		t.Fatal("webhook must still reject variables outside allowed_variables")
	}
}

// A workflow's "Native agent tools" applies to its interactive Builder and
// Run-mode chats of editors only; steps, automations, bots and read-only
// users keep AgentWorks tools.
func TestWorkflowChatNativeAgentToolsScope(t *testing.T) {
	env := newTriggerLinkEnv(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	chat := QueryRequest{AgentMode: "workflow_phase", PhaseID: "workflow-builder", SelectedFolder: "Workflow/reports"}
	// On by default: a workflow that never set the switch uses native tools.
	if !env.api.workflowChatNativeAgentTools(ctx, chat, "sess-1", false) {
		t.Fatal("interactive Builder chat must use native tools by default")
	}
	manifest, _, err := ReadWorkflowManifest(ctx, "Workflow/reports")
	if err != nil {
		t.Fatal(err)
	}
	setSwitch := func(enabled bool) {
		manifest.Capabilities.NativeAgentTools = &enabled
		raw, _ := json.Marshal(manifest)
		env.mock.mu.Lock()
		env.mock.files[manifestPath("Workflow/reports")] = string(raw)
		env.mock.mu.Unlock()
	}
	setSwitch(false)
	if env.api.workflowChatNativeAgentTools(ctx, chat, "sess-1", false) {
		t.Fatal("switch explicitly off: native tools must stay off")
	}
	setSwitch(true)
	if !env.api.workflowChatNativeAgentTools(ctx, chat, "sess-1", false) {
		t.Fatal("interactive Builder chat with the switch on must use native tools")
	}
	run := chat
	run.PinRunMode = true
	if !env.api.workflowChatNativeAgentTools(ctx, run, "sess-2", false) {
		t.Fatal("interactive Run-mode chat must use native tools")
	}
	for name, tc := range map[string]struct {
		req      QueryRequest
		readOnly bool
	}{
		"read-only user":    {chat, true},
		"step agent":        {func() QueryRequest { r := chat; r.ParentSessionID = "parent"; return r }(), false},
		"headless workflow": {func() QueryRequest { r := chat; r.AgentMode = "workflow"; return r }(), false},
	} {
		if env.api.workflowChatNativeAgentTools(ctx, tc.req, "sess-"+name, tc.readOnly) {
			t.Fatalf("%s must keep AgentWorks-only tools", name)
		}
	}
	// Owner decision 2026-09-29: every other turn type uses native tools.
	for name, req := range map[string]QueryRequest{
		"scheduled run": func() QueryRequest { r := chat; r.TriggeredBy = "cron"; return r }(),
		"bot":           func() QueryRequest { r := chat; r.BotPlatform = "slack"; return r }(),
		"notification":  func() QueryRequest { r := chat; r.IsAutoNotification = true; return r }(),
		"Pulse reviewer child": func() QueryRequest {
			r := chat
			r.ParentSessionID = "parent"
			r.SessionKind = "pulse_reviewer"
			return r
		}(),
	} {
		if !env.api.workflowChatNativeAgentTools(ctx, req, "sess-"+name, false) {
			t.Fatalf("%s must use native tools", name)
		}
	}
}
