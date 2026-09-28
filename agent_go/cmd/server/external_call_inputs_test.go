package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

func externalCallWithClaims(t *testing.T, api *StreamingAPI, claims *UserClaims, name string, args map[string]any) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(body), claims, nil))
	out := map[string]any{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s: status %d, body %q: %v", name, w.Code, w.Body.String(), err)
	}
	return w.Code, out
}

func awaitCallQuestion(t *testing.T, poll func() (int, map[string]any)) (string, map[string]any) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		code, out := poll()
		last = out
		if code != 200 {
			t.Fatalf("poll = %d %v", code, out)
		}
		if pending, _ := out["pending_inputs"].([]any); len(pending) > 0 {
			requestID, _ := pending[0].(map[string]any)["request_id"].(string)
			if requestID == "" || out["needs_user_input"] != true {
				t.Fatalf("malformed pending question: %v", out)
			}
			return requestID, out
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("call's pending question never appeared; last poll: %v", last)
	return "", nil
}

func TestExternalCrewCallPendingQuestionReplyOverREST(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	requestID := fmt.Sprintf("plat369-crew-%d", time.Now().UnixNano())
	previous := crewOwnChatAskTurn
	crewOwnChatAskTurn = func(_ *StreamingAPI, ctx context.Context, _ map[string]interface{}, sessionID, _ string) (internalSessionTurnResult, error) {
		store := virtualtools.GetHumanFeedbackStore()
		if err := store.CreatePendingRequest(requestID, "Which region?", "Choose the deployment region", sessionID, []string{"EU", "US"}, false, time.Minute); err != nil {
			return internalSessionTurnResult{}, err
		}
		answer, err := store.WaitForResponseCtx(ctx, requestID, time.Minute)
		return internalSessionTurnResult{FinalResponse: "Selected " + answer}, err
	}
	t.Cleanup(func() { crewOwnChatAskTurn = previous })
	owner := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}, CrewIDs: []string{"beta"}}}
	code, started := externalCallWithClaims(t, env.api, owner, "ask_crew", map[string]any{"crew_id": "beta", "message": "Deploy this", "wait_seconds": 0})
	if code != 200 {
		t.Fatalf("ask_crew = %d %v", code, started)
	}
	callID, _ := started["call_id"].(string)
	if callID == "" {
		t.Fatalf("missing call_id: %v", started)
	}
	request, polled := awaitCallQuestion(t, func() (int, map[string]any) {
		return externalCallWithClaims(t, env.api, owner, "get_crew_function_call", map[string]any{"call_id": callID})
	})
	if request != requestID || !strings.Contains(fmt.Sprint(polled["pending_inputs"]), "Which region?") {
		t.Fatalf("wrong pending question: %v", polled)
	}
	if strings.Contains(fmt.Sprint(polled["pending_inputs"]), "session_id") {
		t.Fatalf("internal session leaked: %v", polled["pending_inputs"])
	}
	readOnly := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{"beta"}}}
	if code, out := externalCallWithClaims(t, env.api, readOnly, "get_crew_function_call", map[string]any{"call_id": callID}); code != 200 || out["needs_user_input"] != false {
		t.Fatalf("read-only poll exposed answerable input: %d %v", code, out)
	}
	answer := map[string]any{"call_id": callID, "request_id": requestID, "response": "US"}
	other := &UserClaims{UserID: "other", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}, AllCrews: true}}
	if code, _ := externalCallWithClaims(t, env.api, other, "reply_function_call_input", answer); code != 404 {
		t.Fatalf("other user's reply = %d, want 404", code)
	}
	wrongScope := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"runs:execute"}, AllWorkflows: true}}
	if code, _ := externalCallWithClaims(t, env.api, wrongScope, "reply_function_call_input", answer); code != 404 {
		t.Fatalf("workflow-only token replied to Crew: %d", code)
	}
	wrongCrew := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}, CrewIDs: []string{"gamma"}}}
	if code, _ := externalCallWithClaims(t, env.api, wrongCrew, "reply_function_call_input", answer); code != 404 {
		t.Fatalf("token without target Crew replied: %d", code)
	}
	answer["response"] = "Antarctica"
	if code, _ := externalCallWithClaims(t, env.api, owner, "reply_function_call_input", answer); code != 400 {
		t.Fatalf("invalid choice = %d, want 400", code)
	}
	answer["response"] = "US"
	if code, out := externalCallWithClaims(t, env.api, owner, "reply_function_call_input", answer); code != 200 || out["status"] != "submitted" {
		t.Fatalf("valid answer = %d %v", code, out)
	}
	if code, _ := externalCallWithClaims(t, env.api, owner, "reply_function_call_input", answer); code != 409 {
		t.Fatalf("duplicate answer = %d, want 409", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		code, out := externalCallWithClaims(t, env.api, owner, "get_crew_function_call", map[string]any{"call_id": callID})
		if code == 200 && out["status"] == "completed" {
			if !strings.Contains(fmt.Sprint(out["result"]), "Selected US") {
				t.Fatalf("answer missed waiting Crew: %v", out)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("Crew did not finish after reply")
}

func TestExternalWorkflowAskQuestionReplyOverREST(t *testing.T) {
	f := newExternalToolsFixture(t)
	requestID := fmt.Sprintf("plat369-workflow-%d", time.Now().UnixNano())
	previous := workflowAskTurn
	workflowAskTurn = func(_ *StreamingAPI, ctx context.Context, _ map[string]interface{}, sessionID, _ string) (internalSessionTurnResult, error) {
		store := virtualtools.GetHumanFeedbackStore()
		if err := store.CreatePendingRequest(requestID, "Approve invoice batch?", "", sessionID, []string{"yes", "no"}, false, time.Minute); err != nil {
			return internalSessionTurnResult{}, err
		}
		answer, err := store.WaitForResponseCtx(ctx, requestID, time.Minute)
		return internalSessionTurnResult{FinalResponse: "Answer was " + answer}, err
	}
	t.Cleanup(func() { workflowAskTurn = previous })
	args := map[string]any{"workflow_id": "invoices", "function": "ask", "args": map[string]any{"message": "Check the batch"}}
	started := externalTestBody(t, f.call(t, "owner", "call_workflow_function", args), 200)
	callID, _ := started["call_id"].(string)
	if callID == "" {
		t.Fatalf("missing call ID: %v", started)
	}
	request, out := awaitCallQuestion(t, func() (int, map[string]any) {
		w := f.call(t, "owner", "get_workflow_function_call", map[string]any{"workflow_id": "invoices", "call_id": callID})
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	})
	if request != requestID || !strings.Contains(fmt.Sprint(out["pending_inputs"]), "Approve invoice batch?") {
		t.Fatalf("wrong pending input: %v", out)
	}
	answer := map[string]any{"call_id": callID, "request_id": requestID, "response": "yes"}
	externalTestBody(t, f.call(t, "reader", "reply_function_call_input", answer), 404)
	externalTestBody(t, f.call(t, "owner", "reply_function_call_input", answer), 200)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		body := externalTestBody(t, f.call(t, "owner", "get_workflow_function_call", map[string]any{"workflow_id": "invoices", "call_id": callID}), 200)
		if body["status"] == "completed" {
			if !strings.Contains(fmt.Sprint(body["result"]), "Answer was yes") {
				t.Fatalf("answer missed waiting workflow: %v", body)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("workflow Ask did not finish after reply")
}

func TestExternalWorkflowAskCallsShareSessionWithoutSharingQuestions(t *testing.T) {
	f := newExternalToolsFixture(t)
	var turns atomic.Int32
	previous := workflowAskTurn
	workflowAskTurn = func(_ *StreamingAPI, ctx context.Context, _ map[string]interface{}, sessionID, _ string) (internalSessionTurnResult, error) {
		turn := turns.Add(1)
		requestID := fmt.Sprintf("plat369-sequential-%d-%d", time.Now().UnixNano(), turn)
		store := virtualtools.GetHumanFeedbackStore()
		if err := store.CreatePendingRequest(requestID, fmt.Sprintf("Question %d?", turn), "", sessionID, nil, true, time.Minute); err != nil {
			return internalSessionTurnResult{}, err
		}
		answer, err := store.WaitForResponseCtx(ctx, requestID, time.Minute)
		return internalSessionTurnResult{FinalResponse: answer}, err
	}
	t.Cleanup(func() { workflowAskTurn = previous })
	start := func(message string) string {
		t.Helper()
		body := externalTestBody(t, f.call(t, "owner", "call_workflow_function", map[string]any{"workflow_id": "invoices", "function": "ask", "args": map[string]any{"message": message}}), 200)
		id, _ := body["call_id"].(string)
		if id == "" {
			t.Fatalf("missing call ID: %v", body)
		}
		return id
	}
	poll := func(id string) (int, map[string]any) {
		w := f.call(t, "owner", "get_workflow_function_call", map[string]any{"workflow_id": "invoices", "call_id": id})
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	}
	first := start("first question")
	firstRequest, _ := awaitCallQuestion(t, func() (int, map[string]any) { return poll(first) })
	second := start("second question")
	if second == first {
		t.Fatal("distinct asks unexpectedly joined")
	}
	if code, out := poll(second); code != 200 || out["needs_user_input"] != false || turns.Load() != 1 {
		t.Fatalf("queued second ask inherited first question: %d %v; turns=%d", code, out, turns.Load())
	}
	externalTestBody(t, f.call(t, "owner", "reply_function_call_input", map[string]any{"call_id": first, "request_id": firstRequest, "response": "first answer"}), 200)
	secondRequest, _ := awaitCallQuestion(t, func() (int, map[string]any) { return poll(second) })
	if secondRequest == firstRequest || turns.Load() != 2 {
		t.Fatalf("second ask question was not distinct: first=%s second=%s turns=%d", firstRequest, secondRequest, turns.Load())
	}
	if code, _ := externalCallWithClaims(t, f.api, &UserClaims{UserID: "owner"}, "reply_function_call_input", map[string]any{"call_id": first, "request_id": secondRequest, "response": "wrong call"}); code != 409 {
		t.Fatalf("first call answered second question: %d", code)
	}
	externalTestBody(t, f.call(t, "owner", "reply_function_call_input", map[string]any{"call_id": second, "request_id": secondRequest, "response": "second answer"}), 200)
}

func TestExternalTypedCrewCallUsesTriggerRunSession(t *testing.T) {
	env := newCrewFunctionEnv(t)
	env.api.agentProfiles = env.svc.registry
	if _, err := env.alpha["define_function"].exec(context.Background(), loginFlowArgs); err != nil {
		t.Fatal(err)
	}
	owner := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}, CrewIDs: []string{"beta"}}}
	code, started := externalCallWithClaims(t, env.api, owner, "call_crew_function", map[string]any{"crew_id": "beta", "function": "run_login_flow", "args": map[string]any{"build": "7"}})
	if code != 200 {
		t.Fatalf("call_crew_function = %d %v", code, started)
	}
	callID, _ := started["call_id"].(string)
	runID, _ := started["run_id"].(string)
	if callID == "" || runID == "" {
		t.Fatalf("missing call or run ID: %v", started)
	}
	const session = "plat369-typed-crew-session"
	runs := agentProfileRuntimeWorkspace("owner", linkBetaPath)
	if err := UpdateScheduleRun(context.Background(), runs, runID, "running", "", nil, "", session); err != nil {
		t.Fatal(err)
	}
	requestID := fmt.Sprintf("plat369-typed-%d", time.Now().UnixNano())
	store := virtualtools.GetHumanFeedbackStore()
	if err := store.CreatePendingRequest(requestID, "Continue tests?", "", session, nil, true, time.Minute); err != nil {
		t.Fatal(err)
	}
	answered := make(chan string, 1)
	go func() {
		response, _ := store.WaitForResponse(requestID, time.Minute)
		answered <- response
	}()
	request, _ := awaitCallQuestion(t, func() (int, map[string]any) {
		return externalCallWithClaims(t, env.api, owner, "get_crew_function_call", map[string]any{"call_id": callID})
	})
	if request != requestID {
		t.Fatalf("question = %q, want %q", request, requestID)
	}
	if code, out := externalCallWithClaims(t, env.api, owner, "reply_function_call_input", map[string]any{"call_id": callID, "request_id": requestID, "response": "yes"}); code != 200 {
		t.Fatalf("typed crew reply = %d %v", code, out)
	}
	select {
	case response := <-answered:
		if response != "yes" {
			t.Fatalf("waiter got %q", response)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("typed Crew waiter did not receive the answer")
	}
}

func TestExternalTypedWorkflowCallUsesTriggerRunSession(t *testing.T) {
	f := newExternalToolsFixture(t)
	var manifest WorkflowManifest
	if err := json.Unmarshal([]byte(f.read(t, "Workflow/invoices/workflow.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	schedule := reviewPRTrigger()
	manifest.Schedules = append(manifest.Schedules, schedule)
	raw, _ := json.Marshal(manifest)
	f.write(t, "Workflow/invoices/workflow.json", string(raw))
	state, err := schedulerstate.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	f.api.scheduler = &SchedulerService{stateStore: state}
	scope, scopeID, _ := scheduleStateScope(buildScheduleContext("Workflow/invoices", &manifest, schedule))
	const session = "plat369-typed-workflow-session"
	const runID = "plat369-typed-workflow-run"
	if err := state.BeginRun(context.Background(), schedulerstate.Run{RunID: runID, LockKey: runID, ScheduleID: schedule.ID, ScopeType: scope, ScopeID: scopeID, TriggerSource: "webhook", State: schedulerstate.State("running"), ActiveSessionID: session}); err != nil {
		t.Fatal(err)
	}
	callID := fmt.Sprintf("fn-plat369-workflow-%d", time.Now().UnixNano())
	call := &crewFunctionCall{ID: callID, Function: schedule.Function.Name, UserID: "owner", CallerKind: triggerCallerUser, CallerID: "owner", TargetKind: triggerCallerWorkflow, TargetID: manifest.ID, TargetPath: "Workflow/invoices", TriggerID: schedule.ID, RunID: runID, RunIDs: []string{runID}, Status: "running", CreatedAt: time.Now().Add(-time.Second), done: make(chan struct{}), target: triggerTarget{Kind: triggerCallerWorkflow, Path: "Workflow/invoices", Manifest: &manifest}, caller: triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerUser, ID: "owner"}}}
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[callID] = call
	crewFunctionCalls.Unlock()
	t.Cleanup(func() {
		crewFunctionCalls.Lock()
		delete(crewFunctionCalls.m, callID)
		crewFunctionCalls.Unlock()
	})
	requestID := fmt.Sprintf("plat369-workflow-trigger-%d", time.Now().UnixNano())
	feedback := virtualtools.GetHumanFeedbackStore()
	if err := feedback.CreatePendingRequest(requestID, "Use production?", "", session, []string{"yes", "no"}, false, time.Minute); err != nil {
		t.Fatal(err)
	}
	answered := make(chan string, 1)
	go func() {
		response, _ := feedback.WaitForResponse(requestID, time.Minute)
		answered <- response
	}()
	body := externalTestBody(t, f.call(t, "owner", "get_workflow_function_call", map[string]any{"workflow_id": manifest.ID, "call_id": callID}), 200)
	if body["needs_user_input"] != true || !strings.Contains(fmt.Sprint(body["pending_inputs"]), requestID) {
		t.Fatalf("typed workflow question missing: %v", body)
	}
	externalTestBody(t, f.call(t, "owner", "reply_function_call_input", map[string]any{"call_id": callID, "request_id": requestID, "response": "yes"}), 200)
	select {
	case response := <-answered:
		if response != "yes" {
			t.Fatalf("waiter got %q", response)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("typed workflow waiter did not receive answer")
	}
}

func TestExternalMCPCallScopedQuestionReply(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	claims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}, CrewIDs: []string{"beta"}}}
	callID := fmt.Sprintf("fn-plat369-mcp-%d", time.Now().UnixNano())
	requestID := "question-" + callID
	const session = "plat369-mcp-session"
	call := &crewFunctionCall{ID: callID, Function: crewFunctionAskName, UserID: "owner", CallerKind: triggerCallerUser, CallerID: "owner", TargetKind: triggerCallerCrew, TargetID: "beta", RunID: session, RunIDs: []string{session}, FreeText: true, Status: "running", CreatedAt: time.Now().Add(-time.Second), done: make(chan struct{})}
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[callID] = call
	crewFunctionCalls.Unlock()
	t.Cleanup(func() {
		crewFunctionCalls.Lock()
		delete(crewFunctionCalls.m, callID)
		crewFunctionCalls.Unlock()
	})
	feedback := virtualtools.GetHumanFeedbackStore()
	if err := feedback.CreatePendingRequest(requestID, "Pick a branch", "", session, []string{"main", "release"}, false, time.Minute); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	srv := serveExternalMCP(t, env.api, claims)
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	poll := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "get_crew_function_call", "arguments": map[string]any{"call_id": callID}})
	requireRemoteSuccess(t, poll, "get_crew_function_call")
	if !strings.Contains(marshalStructured(t, poll), requestID) {
		t.Fatalf("MCP poll hid question: %s", marshalStructured(t, poll))
	}
	reply := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "reply_function_call_input", "arguments": map[string]any{"call_id": callID, "request_id": requestID, "response": "main"}})
	requireRemoteSuccess(t, reply, "reply_function_call_input")
	if !strings.Contains(marshalStructured(t, reply), "submitted") {
		t.Fatalf("MCP reply = %s", marshalStructured(t, reply))
	}
	if answer, err := feedback.WaitForResponse(requestID, time.Second); err != nil || answer != "main" {
		t.Fatalf("waiter got %q, %v", answer, err)
	}
}

func TestExternalCallReplyRefusesAmbiguousSharedSession(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	owner := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}, CrewIDs: []string{"beta"}}}
	const session = "plat369-shared-session"
	createdAt := time.Now().Add(-time.Second)
	firstID := fmt.Sprintf("fn-plat369-first-%d", time.Now().UnixNano())
	secondID := fmt.Sprintf("fn-plat369-second-%d", time.Now().UnixNano())
	makeCall := func(id string) *crewFunctionCall {
		return &crewFunctionCall{ID: id, Function: crewFunctionAskName, UserID: "owner", CallerKind: triggerCallerUser, CallerID: "owner", TargetKind: triggerCallerCrew, TargetID: "beta", RunID: session, RunIDs: []string{session}, FreeText: true, Status: "running", CreatedAt: createdAt, done: make(chan struct{})}
	}
	first, second := makeCall(firstID), makeCall(secondID)
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[firstID], crewFunctionCalls.m[secondID] = first, second
	crewFunctionCalls.Unlock()
	t.Cleanup(func() {
		crewFunctionCalls.Lock()
		delete(crewFunctionCalls.m, firstID)
		delete(crewFunctionCalls.m, secondID)
		crewFunctionCalls.Unlock()
	})
	requestID := fmt.Sprintf("plat369-shared-%d", time.Now().UnixNano())
	store := virtualtools.GetHumanFeedbackStore()
	if err := store.CreatePendingRequest(requestID, "Which one?", "", session, nil, true, time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{firstID, secondID} {
		code, out := externalCallWithClaims(t, env.api, owner, "get_crew_function_call", map[string]any{"call_id": id})
		if code != 200 || out["needs_user_input"] != false {
			t.Fatalf("ambiguous call %s exposed input: %d %v", id, code, out)
		}
		code, out = externalCallWithClaims(t, env.api, owner, "reply_function_call_input", map[string]any{"call_id": id, "request_id": requestID, "response": "wrong"})
		if code != 409 {
			t.Fatalf("ambiguous reply %s = %d %v", id, code, out)
		}
	}
}

func TestExternalCallReplyRefusesExpiredAndCancelledRequests(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	owner := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}, CrewIDs: []string{"beta"}}}
	callID := fmt.Sprintf("fn-plat369-expiry-%d", time.Now().UnixNano())
	session := "plat369-expiry-" + callID
	call := &crewFunctionCall{ID: callID, Function: crewFunctionAskName, UserID: "owner", CallerKind: triggerCallerUser, CallerID: "owner", TargetKind: triggerCallerCrew, TargetID: "beta", RunID: session, RunIDs: []string{session}, FreeText: true, Status: "running", CreatedAt: time.Now().Add(-time.Second), done: make(chan struct{})}
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[callID] = call
	crewFunctionCalls.Unlock()
	t.Cleanup(func() {
		crewFunctionCalls.Lock()
		delete(crewFunctionCalls.m, callID)
		crewFunctionCalls.Unlock()
	})
	store := virtualtools.GetHumanFeedbackStore()
	for _, state := range []string{"expired", "cancelled"} {
		requestID := fmt.Sprintf("plat369-%s-%d", state, time.Now().UnixNano())
		timeout := time.Minute
		if state == "expired" {
			timeout = time.Millisecond
		}
		if err := store.CreatePendingRequest(requestID, "Continue?", "", session, nil, true, timeout); err != nil {
			t.Fatal(err)
		}
		if state == "cancelled" {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := store.WaitForResponseCtx(ctx, requestID, time.Minute); err != virtualtools.ErrFeedbackCancelled {
				t.Fatalf("cancelled wait = %v", err)
			}
		} else {
			time.Sleep(2 * time.Millisecond)
		}
		code, out := externalCallWithClaims(t, env.api, owner, "reply_function_call_input", map[string]any{"call_id": callID, "request_id": requestID, "response": "yes"})
		if code != 409 {
			t.Fatalf("%s reply = %d %v", state, code, out)
		}
	}
}

func TestExternalCallKeepsMultiplePendingQuestionsDistinct(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	owner := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}, CrewIDs: []string{"beta"}}}
	callID := fmt.Sprintf("fn-plat369-multiple-%d", time.Now().UnixNano())
	session := "plat369-multiple-" + callID
	call := &crewFunctionCall{ID: callID, Function: crewFunctionAskName, UserID: "owner", CallerKind: triggerCallerUser, CallerID: "owner", TargetKind: triggerCallerCrew, TargetID: "beta", RunID: session, RunIDs: []string{session}, FreeText: true, Status: "running", CreatedAt: time.Now().Add(-time.Second), done: make(chan struct{})}
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[callID] = call
	crewFunctionCalls.Unlock()
	t.Cleanup(func() {
		crewFunctionCalls.Lock()
		delete(crewFunctionCalls.m, callID)
		crewFunctionCalls.Unlock()
	})
	store := virtualtools.GetHumanFeedbackStore()
	first := fmt.Sprintf("plat369-first-question-%d", time.Now().UnixNano())
	second := fmt.Sprintf("plat369-second-question-%d", time.Now().UnixNano())
	for _, id := range []string{first, second} {
		if err := store.CreatePendingRequest(id, "Answer "+id, "", session, nil, true, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	code, out := externalCallWithClaims(t, env.api, owner, "get_crew_function_call", map[string]any{"call_id": callID})
	pending, _ := out["pending_inputs"].([]any)
	if code != 200 || len(pending) != 2 {
		t.Fatalf("multiple pending questions = %d %v", code, out)
	}
	if code, out := externalCallWithClaims(t, env.api, owner, "reply_function_call_input", map[string]any{"call_id": callID, "request_id": first, "response": "one"}); code != 200 {
		t.Fatalf("first reply = %d %v", code, out)
	}
	code, out = externalCallWithClaims(t, env.api, owner, "get_crew_function_call", map[string]any{"call_id": callID})
	pending, _ = out["pending_inputs"].([]any)
	if code != 200 || len(pending) != 1 || pending[0].(map[string]any)["request_id"] != second {
		t.Fatalf("second question lost after first answer: %d %v", code, out)
	}
	if code, out := externalCallWithClaims(t, env.api, owner, "reply_function_call_input", map[string]any{"call_id": callID, "request_id": second, "response": "two"}); code != 200 {
		t.Fatalf("second reply = %d %v", code, out)
	}
	for id, want := range map[string]string{first: "one", second: "two"} {
		if got, err := store.WaitForResponse(id, time.Second); err != nil || got != want {
			t.Fatalf("question %s got %q, %v; want %q", id, got, err, want)
		}
	}
}
