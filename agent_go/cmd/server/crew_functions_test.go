package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func muxRouterForCrewFunctions(svc *ProjectActionDispatcher) *mux.Router {
	router := mux.NewRouter()
	ProductWebhookRoutes(router, svc)
	return router
}

func TestCrewFunctionSchemaValidation(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object", "required": []interface{}{"build", "env"},
		"properties": map[string]interface{}{
			"build":   map[string]interface{}{"type": "string"},
			"env":     map[string]interface{}{"type": "string", "enum": []interface{}{"staging", "prod"}},
			"retries": map[string]interface{}{"type": "integer"},
			"tags":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
		},
	}
	if err := checkCrewFunctionSchema(schema, "input_schema"); err != nil {
		t.Fatalf("valid schema rejected: %v", err)
	}
	for _, bad := range []map[string]interface{}{
		{"type": "date"},
		{"type": "object", "required": []interface{}{"missing"}, "properties": map[string]interface{}{}},
		{"type": "string", "enum": []interface{}{}},
		{"type": "array", "items": "string"},
	} {
		if err := checkCrewFunctionSchema(bad, "s"); err == nil {
			t.Fatalf("invalid schema accepted: %v", bad)
		}
	}
	if problems := validateCrewFunctionValue(schema, map[string]interface{}{"build": "812", "env": "staging", "retries": float64(2), "tags": []interface{}{"a"}}); len(problems) != 0 {
		t.Fatalf("valid value rejected: %v", problems)
	}
	problems := validateCrewFunctionValue(schema, map[string]interface{}{"build": float64(812), "env": "qa", "retries": 1.5, "tags": []interface{}{float64(1)}})
	joined := strings.Join(problems, " | ")
	for _, want := range []string{"$.build: expected string", "$.env: qa is not one of", "$.retries: expected integer", "$.tags[0]: expected string"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("problems %q missing %q", joined, want)
		}
	}
	if problems := validateCrewFunctionValue(schema, map[string]interface{}{"build": "812"}); len(problems) != 1 || !strings.Contains(problems[0], "$.env: required") {
		t.Fatalf("missing required = %v", problems)
	}
}

type crewFunctionEnv struct {
	triggerLinkEnv
	alpha map[string]recordedTool
	beta  map[string]recordedTool
}

func newCrewFunctionEnv(t *testing.T) crewFunctionEnv {
	t.Helper()
	previousPoll, previousWait := triggerTargetPollInterval, crewFunctionFastWait
	triggerTargetPollInterval = 10 * time.Millisecond
	crewFunctionFastWait = 3 * time.Second
	t.Cleanup(func() { triggerTargetPollInterval, crewFunctionFastWait = previousPoll, previousWait })
	// Calls are process-wide; each fixture starts with independent admission state.
	crewFunctionCalls.Lock()
	crewFunctionCalls.m = map[string]*crewFunctionCall{}
	crewFunctionCalls.Unlock()
	env := crewFunctionEnv{triggerLinkEnv: newTriggerLinkEnv(t)}
	env.alpha = env.functionTools(t, linkAlphaPath, "sess-caller", nil)
	env.beta = env.functionTools(t, linkBetaPath, "sess-beta-chat", nil)
	return env
}

func (env crewFunctionEnv) functionTools(t *testing.T, crewPath, sessionID string, contextPaths []string) map[string]recordedTool {
	t.Helper()
	reg := &recordingRegistrar{}
	if err := env.api.registerCrewFunctionTools(reg, "owner", sessionID, QueryRequest{SelectedFolder: crewPath, WorkflowContextPaths: contextPaths}, crewTriggerLinkCaller(crewPath), nil); err != nil {
		t.Fatal(err)
	}
	return reg.tools
}

// Fixtures create a real declared entry before calling it; runtime calls no
// longer synthesize a saved caller-specific trigger as a side effect.
func savedFunctionForTest(t *testing.T, ctx context.Context, target triggerTarget, fn crewFunction) crewFunction {
	t.Helper()
	functions, err := readCrewFunctions(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	functions = append(functions, fn)
	if err := writeCrewFunctions(ctx, target, functions); err != nil {
		t.Fatal(err)
	}
	functions, err = readCrewFunctions(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	saved, found := findCrewFunction(functions, fn.Name)
	if !found {
		t.Fatal("declared function missing")
	}
	return saved
}

func TestInternalFunctionCallPendingInputAndReplyAcrossCallerKinds(t *testing.T) {
	env := newCrewFunctionEnv(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	workflowCaller, err := workflowTriggerLinkCaller("Workflow/reports")(ctx)
	if err != nil {
		t.Fatal(err)
	}
	workflowTarget, err := workflowTriggerLinkCaller("Workflow/pipeline")(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reg := &recordingRegistrar{}
	if err := env.api.registerCrewFunctionTools(reg, "owner", "workflow-caller", QueryRequest{SelectedFolder: "Workflow/reports"}, workflowTriggerLinkCaller("Workflow/reports"), nil); err != nil {
		t.Fatal(err)
	}
	store := virtualtools.GetHumanFeedbackStore()
	for _, tc := range []struct {
		name          string
		callID        string
		requestID     string
		callerKind    string
		callerID      string
		targetKind    string
		targetID      string
		targetPath    string
		targetProfile string
		tools         map[string]recordedTool
	}{
		{"crew-to-crew", "fn-internal-crew-crew", "request-internal-crew-crew", triggerCallerCrew, "alpha", triggerCallerCrew, "beta", linkBetaPath, "work", env.alpha},
		{"crew-to-workflow", "fn-internal-crew-workflow", "request-internal-crew-workflow", triggerCallerCrew, "alpha", triggerCallerWorkflow, workflowCaller.Stamp.ID, "Workflow/reports", "", env.alpha},
		{"workflow-to-crew", "fn-internal-workflow-crew", "request-internal-workflow-crew", triggerCallerWorkflow, workflowCaller.Stamp.ID, triggerCallerCrew, "beta", linkBetaPath, "work", reg.tools},
		{"workflow-to-workflow", "fn-internal-workflow-workflow", "request-internal-workflow-workflow", triggerCallerWorkflow, workflowCaller.Stamp.ID, triggerCallerWorkflow, workflowTarget.Stamp.ID, "Workflow/pipeline", "", reg.tools},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := &crewFunctionCall{ID: tc.callID, UserID: "owner", CallerKind: tc.callerKind, CallerID: tc.callerID, TargetKind: tc.targetKind, TargetID: tc.targetID, TargetPath: tc.targetPath, TargetProfileID: tc.targetProfile, Status: "running", CreatedAt: time.Now(), UpdatedAt: time.Now(), done: make(chan struct{})}
			crewFunctionCalls.Lock()
			crewFunctionCalls.m[call.ID] = call
			crewFunctionCalls.Unlock()
			t.Cleanup(func() {
				crewFunctionCalls.Lock()
				delete(crewFunctionCalls.m, call.ID)
				crewFunctionCalls.Unlock()
				store.WithdrawOperation(call.ID)
			})
			if err := store.CreatePendingRequest(tc.requestID, "Which branch?", "", "shared-chat", []string{"main", "release"}, false, time.Minute, tc.callID); err != nil {
				t.Fatal(err)
			}
			foreignRequestID := tc.requestID + "-foreign"
			if err := store.CreatePendingRequest(foreignRequestID, "Unrelated question", "", "shared-chat", nil, true, time.Minute, "fn-other-call"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.WithdrawOperation("fn-other-call") })
			polled, err := tc.tools["project_functions_get_call"].exec(ctx, map[string]interface{}{"call_id": call.ID})
			if err != nil || !strings.Contains(polled, tc.requestID) || strings.Contains(polled, foreignRequestID) {
				t.Fatalf("pending input missing: %s, %v", polled, err)
			}
			args := map[string]interface{}{"call_id": call.ID, "request_id": tc.requestID, "response": "main"}
			if _, err := env.beta["project_functions_reply"].exec(ctx, args); err == nil {
				t.Fatal("target Crew answered the caller's pending input")
			}
			args["request_id"] = foreignRequestID
			if _, err := tc.tools["project_functions_reply"].exec(ctx, args); err == nil {
				t.Fatal("another call's question was answered")
			}
			args["request_id"] = tc.requestID
			args["response"] = "wrong"
			if _, err := tc.tools["project_functions_reply"].exec(ctx, args); err == nil {
				t.Fatal("invalid choice accepted")
			}
			// A stray space or newline around an exact choice does not matter,
			// whichever tool answers.
			args["response"] = " main\n"
			if _, err := tc.tools["project_functions_reply"].exec(ctx, args); err != nil {
				t.Fatalf("valid answer refused: %v", err)
			}
			if _, err := tc.tools["project_functions_reply"].exec(ctx, args); err == nil {
				t.Fatal("duplicate answer accepted")
			}
		})
	}
}

func TestInternalFunctionReplyRechecksTargetAccess(t *testing.T) {
	env := newCrewFunctionEnv(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	reg := &recordingRegistrar{}
	if err := env.api.registerCrewFunctionTools(reg, "owner", "workflow-caller", QueryRequest{SelectedFolder: "Workflow/reports"}, workflowTriggerLinkCaller("Workflow/reports"), nil); err != nil {
		t.Fatal(err)
	}
	const targetPath = "Workflow/revocable"
	manifest := NewWorkflowManifest("Revocable")
	manifest.CreatedBy = "other"
	manifest.Access = &WorkflowAccess{Owners: []string{"other", "owner"}}
	writeManifest := func() {
		t.Helper()
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		env.mock.mu.Lock()
		env.mock.files[manifestPath(targetPath)] = string(raw)
		env.mock.mu.Unlock()
	}
	writeManifest()
	caller, err := workflowTriggerLinkCaller("Workflow/reports")(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveFunctionTarget(ctx, &UserClaims{UserID: "owner"}, caller, targetPath)
	if err != nil {
		t.Fatalf("initial editor access: %v", err)
	}
	call := &crewFunctionCall{ID: "fn-revoked-target", UserID: "owner", CallerKind: caller.Stamp.Type, CallerID: caller.Stamp.ID,
		TargetKind: target.Kind, TargetID: target.stampID(), TargetPath: targetPath, Status: "running", CreatedAt: time.Now(), UpdatedAt: time.Now(), done: make(chan struct{})}
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[call.ID] = call
	crewFunctionCalls.Unlock()
	store := virtualtools.GetHumanFeedbackStore()
	t.Cleanup(func() { store.WithdrawOperation(call.ID) })
	if err := store.CreatePendingRequest("revoked-target-request", "Proceed?", "", "shared-chat", []string{"yes", "no"}, false, time.Minute, call.ID); err != nil {
		t.Fatal(err)
	}
	manifest.Access.Owners = []string{"other"}
	writeManifest()
	if _, err := reg.tools["project_functions_reply"].exec(ctx, map[string]interface{}{
		"call_id": call.ID, "request_id": "revoked-target-request", "response": "yes",
	}); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("revoked editor answered target question: %v", err)
	}
}

func TestFunctionSubmissionSaveFailureSettlesConcurrentRetry(t *testing.T) {
	env := newCrewFunctionEnv(t)
	blocked := make(chan struct{}, 1)
	release := make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/function_submissions/") {
			select {
			case blocked <- struct{}{}:
			default:
			}
			<-release
			http.Error(w, "write failed", http.StatusServiceUnavailable)
			return
		}
		env.mock.ServeHTTP(w, r)
	}))
	t.Cleanup(proxy.Close)
	t.Setenv("WORKSPACE_API_URL", proxy.URL)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	caller, err := crewTriggerLinkCaller(linkAlphaPath)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveTriggerTarget(ctx, &UserClaims{UserID: "owner"}, "Beta")
	if err != nil {
		t.Fatal(err)
	}
	fn := savedFunctionForTest(t, ctx, target, crewFunction{Name: "status", Instructions: "Check status."})
	firstErr := make(chan error, 1)
	start := func() (*crewFunctionCall, error) {
		return env.api.startCrewFunctionCall(ctx, "owner", caller, target, fn, map[string]interface{}{}, time.Minute, "same-submission")
	}
	go func() { _, err := start(); firstErr <- err }()
	select {
	case <-blocked:
	case <-time.After(3 * time.Second):
		t.Fatal("first call never reached submission save")
	}
	retry, err := start()
	if err != nil || retry == nil {
		close(release)
		t.Fatalf("concurrent retry did not join: %+v, %v", retry, err)
	}
	close(release)
	if err := <-firstErr; err == nil {
		t.Fatal("failed submission save was reported as success")
	}
	select {
	case <-retry.done:
	case <-time.After(3 * time.Second):
		t.Fatal("joined retry never settled")
	}
	if status, _ := retry.snapshot()["status"].(string); status != "failed" {
		t.Fatalf("joined retry status = %q", status)
	}
	if lookupCrewFunctionCall(retry.ID) != retry {
		t.Fatal("joined call disappeared before retry could poll it")
	}
}

var loginFlowArgs = map[string]interface{}{
	"project": "Beta", "definition": map[string]interface{}{"name": "run_login_flow", "description": "Run the login flow against a build.",
		"action": map[string]interface{}{"type": "message"},
		"input":  map[string]interface{}{"messages": []string{"Run tests/login.py with the build and report the failing step."}},
		"input_schema": map[string]interface{}{"type": "object", "required": []interface{}{"build"}, "properties": map[string]interface{}{
			"build": map[string]interface{}{"type": "string"}, "env": map[string]interface{}{"type": "string", "enum": []interface{}{"staging", "prod"}},
		}},
		"result_schema": map[string]interface{}{"type": "object", "required": []interface{}{"passed"}, "properties": map[string]interface{}{
			"passed": map[string]interface{}{"type": "boolean"}, "failed_step": map[string]interface{}{"type": "integer"},
		}},
	}}

// Self-calls, cycles and deep chains are refused; mid-run questions are for
// Crew targets only.
func TestCrewFunctionGuards(t *testing.T) {
	env := newCrewFunctionEnv(t)
	ctx := context.Background()
	if _, err := env.alpha["project_functions_define"].exec(ctx, loginFlowArgs); err != nil {
		t.Fatal(err)
	}
	own := map[string]interface{}{"definition": map[string]interface{}{"name": "own_fn", "description": "d", "action": map[string]interface{}{"type": "message"}, "input": map[string]interface{}{"messages": []string{"i"}}}}
	if _, err := env.alpha["project_functions_define"].exec(ctx, own); err != nil {
		t.Fatalf("define own: %v", err)
	}
	if _, err := env.alpha["project_functions_call"].exec(ctx, map[string]interface{}{"project": "Alpha", "function": "own_fn"}); err == nil || !strings.Contains(err.Error(), "cannot call itself") && !strings.Contains(err.Error(), "own function") {
		t.Fatalf("self call: err = %v", err)
	}

	// Alpha is currently running a call that Beta made: Alpha calling Beta
	// back would loop.
	inflight := &crewFunctionCall{ID: "fn-inflight", Function: "x", TargetKind: triggerCallerCrew, TargetID: "alpha", CallerKind: triggerCallerCrew, CallerID: "beta",
		Chain: []string{"crew:work:beta", "crew:work:alpha"}, Root: "fn-inflight", Status: "running", done: make(chan struct{})}
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[inflight.ID] = inflight
	crewFunctionCalls.Unlock()
	t.Cleanup(func() {
		crewFunctionCalls.Lock()
		delete(crewFunctionCalls.m, inflight.ID)
		crewFunctionCalls.Unlock()
	})
	if _, err := env.alpha["project_functions_call"].exec(ctx, map[string]interface{}{"project": "Beta", "function": "run_login_flow", "input": map[string]interface{}{"build": "1"}}); err == nil || !strings.Contains(err.Error(), "already in this call chain") {
		t.Fatalf("cycle: err = %v", err)
	}
	inflight.mu.Lock()
	inflight.Chain = []string{"crew:work:x1", "crew:work:x2", "crew:work:x3", "crew:work:alpha"}
	inflight.mu.Unlock()
	if _, err := env.alpha["project_functions_call"].exec(ctx, map[string]interface{}{"project": "Beta", "function": "run_login_flow", "input": map[string]interface{}{"build": "1"}}); err == nil || !strings.Contains(err.Error(), "depth limit") {
		t.Fatalf("depth: err = %v", err)
	}
	inflight.finish("completed", nil, "")

	// Workflow targets take no mid-run questions.
	wfCall := &crewFunctionCall{ID: "fn-wf", Function: "report", TargetKind: triggerCallerWorkflow, TargetID: "wf", CallerKind: triggerCallerCrew, CallerID: "alpha", Status: "running", done: make(chan struct{})}
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[wfCall.ID] = wfCall
	crewFunctionCalls.Unlock()
	if _, err := env.alpha["ask_function_update"].exec(ctx, map[string]interface{}{"call_id": "fn-wf", "question": "status?"}); err == nil || !strings.Contains(err.Error(), "workflow runs do not accept") {
		t.Fatalf("workflow update: err = %v", err)
	}
	if _, err := env.beta["ask_function_update"].exec(ctx, map[string]interface{}{"call_id": "fn-wf", "question": "status?"}); err == nil || !strings.Contains(err.Error(), "only the caller") {
		t.Fatalf("non-caller update: err = %v", err)
	}
}

// The Automation panel's Functions tab lists declared functions plus the
// declared functions and recent calls; owners can remove their declarations.
func TestCrewFunctionsHTTPListAndDelete(t *testing.T) {
	env := newCrewFunctionEnv(t)
	ctx := context.Background()
	if _, err := env.alpha["project_functions_define"].exec(ctx, loginFlowArgs); err != nil {
		t.Fatal(err)
	}
	// PLAT-871: deleting a callable function must not erase a disabled sibling.
	if _, err := env.alpha["project_functions_define"].exec(ctx, map[string]interface{}{
		"project": "Beta", "definition": map[string]interface{}{
			"name": "paused_flow", "description": "A paused function", "enabled": false,
			"action": map[string]interface{}{"type": "message"},
			"input":  map[string]interface{}{"messages": []string{"Keep these saved instructions."}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	ownerCtx := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "owner"})
	target := triggerTarget{Kind: triggerCallerCrew, Path: linkBetaPath, CrewOwner: "owner"}
	root := crewFunctionRoot(ownerCtx, target)
	before, err := readProjectFunctionDefinitions(ownerCtx, root)
	if err != nil {
		t.Fatal(err)
	}
	var pausedBefore []byte
	for _, fn := range before {
		if fn.Name == "paused_flow" {
			pausedBefore, _ = json.Marshal(fn)
		}
	}
	if len(pausedBefore) == 0 {
		t.Fatal("disabled sibling was not saved by setup")
	}
	router := muxRouterForCrewFunctions(env.svc)
	get := httptest.NewRequest(http.MethodGet, "/api/crew-functions?profile_id=work&project_id=beta", nil)
	get = get.WithContext(context.WithValue(get.Context(), UserContextKey, &UserClaims{UserID: "owner"}))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, get)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", recorder.Code, recorder.Body.String())
	}
	var listed struct {
		Functions []struct {
			Name     string `json:"name"`
			Implicit bool   `json:"implicit"`
		} `json:"functions"`
		Calls []interface{} `json:"calls"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, fn := range listed.Functions {
		names[fn.Name] = fn.Implicit
	}
	if _, ok := names["ask"]; ok {
		t.Fatalf("functions = %+v", listed.Functions)
	}
	if implicit, ok := names["run_login_flow"]; !ok || implicit {
		t.Fatalf("functions = %+v", listed.Functions)
	}
	for _, tc := range []struct {
		name string
		code int
	}{{"ask", http.StatusNotFound}, {"run_login_flow", http.StatusNoContent}, {"run_login_flow", http.StatusNotFound}} {
		del := httptest.NewRequest(http.MethodDelete, "/api/crew-functions/"+tc.name+"?profile_id=work&project_id=beta", nil)
		del = del.WithContext(context.WithValue(del.Context(), UserContextKey, &UserClaims{UserID: "owner"}))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, del)
		if recorder.Code != tc.code {
			t.Fatalf("delete %s = %d, want %d: %s", tc.name, recorder.Code, tc.code, recorder.Body.String())
		}
	}
	after, err := readProjectFunctionDefinitions(ownerCtx, root)
	if err != nil || len(after) != 1 {
		t.Fatalf("disabled sibling lost after HTTP deletion: records=%v err=%v", after, err)
	}
	pausedAfter, _ := json.Marshal(after[0])
	if string(pausedAfter) != string(pausedBefore) {
		t.Fatalf("disabled sibling changed: before=%s after=%s", pausedBefore, pausedAfter)
	}

}

func TestCrewFunctionFailureDetailKeepsPartialWork(t *testing.T) {
	detail := crewFunctionFailureDetail(map[string]interface{}{"partial_result": map[string]interface{}{"video": "https://x/v.mp4"}, "final_reply": "1764 PASS"})
	if !strings.Contains(detail, "https://x/v.mp4") || !strings.Contains(detail, "1764 PASS") {
		t.Fatalf("failure detail dropped the target's work: %q", detail)
	}
}

// All project callers use the shared owner rule, including workflow and Code.
func TestCrewCallConversationRouting(t *testing.T) {
	crew := &productWebhookMatch{Profile: agentprofiles.Profile{ID: "work"}, Trigger: productWebhookTrigger{Kind: triggerKindInternal, Caller: &triggerCaller{Type: triggerCallerCrew, ID: "alpha", ProfileID: "work"}}}
	if !projectCallConversation(crew) {
		t.Fatal("a Crew calling a Crew must be routed by owner")
	}
	workflow := &productWebhookMatch{Profile: agentprofiles.Profile{ID: "work"}, Trigger: productWebhookTrigger{Kind: triggerKindInternal, Caller: &triggerCaller{Type: triggerCallerWorkflow, ID: "wf"}}}
	if !projectCallConversation(workflow) {
		t.Fatal("a workflow caller must use project ownership")
	}
	codeTarget := &productWebhookMatch{Profile: agentprofiles.Profile{ID: workproduct.ProfileID}, Trigger: productWebhookTrigger{Kind: triggerKindInternal, Caller: &triggerCaller{Type: triggerCallerCrew, ID: "x", ProfileID: workproduct.ProfileID}}}
	if !projectCallConversation(codeTarget) {
		t.Fatal("Code peer calls must use project ownership")
	}
	if got := crewCallIsolatedKey("trig-1", ""); got != "trig-1" {
		t.Fatalf("same-owner key = %q", got)
	}
	if a, b := crewCallIsolatedKey("trig-1", "bob"), crewCallIsolatedKey("trig-1", "carol"); a == b || a == "trig-1" {
		t.Fatalf("each calling person needs their own conversation: %q %q", a, b)
	}
}

func TestCrewCallMessageMatchesWhereItRuns(t *testing.T) {
	stored := crewTargetMessage(triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ID: "a"}, Label: "Alpha"})
	if got := crewCallMessage(stored, true); !strings.Contains(got, "runs in your main chat") || strings.Contains(got, "does not see it") {
		t.Fatalf("same-owner message: %s", got)
	}
	if got := crewCallMessage(stored, false); !strings.Contains(got, "only for this caller's calls") {
		t.Fatalf("cross-owner message: %s", got)
	}
}

// A Crew function runs in its own named tab by default; "fresh" keeps a new chat per call, and functions naming
// one agent share its tab (owner, 2026-10-19).
func TestCrewFunctionRunsInItsNamedTabByDefault(t *testing.T) {
	if got := (crewFunction{Name: "weekly_report"}).tabName(); got != "weekly_report" || crewAgentChatID(got) != "weekly-report" {
		t.Fatalf("default tab = %q", got)
	}
	if got := (crewFunction{Name: "triage", Agent: "Support desk"}).tabName(); got != "Support desk" {
		t.Fatalf("named tab = %q", got)
	}
	if got := (crewFunction{Name: "private_check", Execution: "fresh", Agent: "x"}).tabName(); got != "" {
		t.Fatalf("fresh function ran in tab %q", got)
	}
	spec := crewFunctionSpec{Name: "triage", Description: "d", Instructions: "i", Execution: "sometimes"}.normalized()
	if spec.validate() == nil {
		t.Fatal("an unknown execution was accepted")
	}
}

// PLAT-871-15: a normal function's stable ID is not an internal-trigger
// shortcut around typed input validation and published owner authority.
func TestDeclaredFunctionCannotBeFiredThroughTheRawTriggerEndpoint(t *testing.T) {
	env := newCrewFunctionEnv(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	target, err := resolveTriggerTarget(ctx, &UserClaims{UserID: "owner"}, "Beta")
	if err != nil {
		t.Fatal(err)
	}
	fn := savedFunctionForTest(t, ctx, target, crewFunction{Name: "typed", Description: "Typed", Instructions: "Check typed input.", InputSchema: map[string]interface{}{"type": "object", "required": []interface{}{"query"}, "properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}}}})
	_, _, _, view, err := env.svc.findInternalProductTrigger(ctx, "owner", "work", "beta", fn.TriggerID)
	if err != nil || !view.WorkspaceFunction {
		t.Fatalf("declared entry setup: %v %v", view, err)
	}
	_, err = env.svc.dispatchInternalProductTrigger(ctx, internalCrewTriggerCall{UserID: "owner", ProfileID: "work", ProjectID: "beta", TriggerID: fn.TriggerID, DeliveryID: "forged", Caller: triggerCaller{Type: triggerCallerUser, ID: "owner"}, Event: crewFunctionEvent, Payload: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrInternalTriggerNotBound) {
		t.Fatalf("raw trigger bypassed typed function admission: %v", err)
	}
	publication := &publishedProject{OwnerID: "owner", WorkspaceID: "workspace", AgentID: "checks", Revision: "revision"}
	record, err := crewFunctionDefinition(publication.sourceRoot(), crewFunction{Name: "published_check", Instructions: "Check the published source."})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(projectFunctionsDocument{Functions: []projectFunctionDefinition{record}})
	if err != nil {
		t.Fatal(err)
	}
	// Publication upload installs immutable source. Ordinary authoring must not
	// write it; seed the upload boundary directly in this dispatch fixture.
	env.mock.mu.Lock()
	env.mock.files[publication.sourceRoot()+"/functions.json"] = string(encoded)
	env.mock.mu.Unlock()
	if _, _, _, _, err = env.svc.findInternalProductTrigger(ctx, "owner", "work", "beta", record.ID); !errors.Is(err, ErrInternalTriggerNotFound) {
		t.Fatalf("unadmitted published entry: %v", err)
	}
	admission := &crewFunctionCall{ID: "admitted", UserID: "owner", TargetID: "beta", TriggerID: record.ID, Function: record.Name, Publication: publication}
	admitted := context.WithValue(ctx, declaredFunctionDispatchKey{}, admission)
	_, _, _, view, err = env.svc.findInternalProductTrigger(admitted, "owner", "work", "beta", record.ID)
	if err != nil || !view.WorkspaceFunction {
		t.Fatalf("typed published entry cannot dispatch: %v %v", view, err)
	}

}
