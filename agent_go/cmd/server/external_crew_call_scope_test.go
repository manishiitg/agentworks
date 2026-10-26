package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// PLAT-366: project_functions_get_call returns only the caller's calls to a Crew.
// A Crew-only token (AllCrews) must not read the same user's workflow calls,
// which belong to project_functions_get_call under workflow access.
func TestGetCrewFunctionCallRefusesWorkflowCalls(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	seed := func(id, kind, target string) {
		call := &crewFunctionCall{ID: id, UserID: "owner", CallerKind: triggerCallerUser, TargetKind: kind, TargetID: target, TargetPath: func() string {
			if kind == triggerCallerWorkflow {
				return "Workflow/reports"
			}
			return linkBetaPath
		}(),
			Status: "completed", Result: map[string]any{"answer": "secret"}, CreatedAt: time.Now(), UpdatedAt: time.Now(), done: make(chan struct{})}
		crewFunctionCalls.Lock()
		crewFunctionCalls.m[id] = call
		crewFunctionCalls.Unlock()
		t.Cleanup(func() {
			crewFunctionCalls.Lock()
			delete(crewFunctionCalls.m, id)
			crewFunctionCalls.Unlock()
		})
	}
	manifest, _, err := ReadWorkflowManifest(context.Background(), "Workflow/reports")
	if err != nil {
		t.Fatal(err)
	}
	seed("fn-plat366-workflow", triggerCallerWorkflow, manifest.ID)
	seed("fn-plat366-crew", triggerCallerCrew, "beta")
	crewToken := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}

	if code, out := externalCrewRequest(t, env, crewToken, "project_functions_get_call", map[string]any{"call_id": "fn-plat366-workflow"}); code != 404 {
		t.Fatalf("a Crew-only token read a workflow call: %d %v", code, out)
	}
	if code, out := externalCrewRequest(t, env, crewToken, "project_functions_get_call", map[string]any{"call_id": "fn-plat366-crew"}); code != 200 {
		t.Fatalf("the user's own Crew call must stay readable: %d %v", code, out)
	}
	other := &UserClaims{UserID: "someone", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	if code, out := externalCrewRequest(t, env, other, "project_functions_get_call", map[string]any{"call_id": "fn-plat366-crew"}); code != 200 || out["can_reply"] != false {
		t.Fatalf("a current shared member reads results but cannot reply: %d %v", code, out)
	}
	// PLAT-871: the common HTTP family infers the durable target from call_id,
	// then keeps the target's own scope. Both allowed paths must remain real.
	workflowToken := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"workflows:read"}, AllWorkflows: true}}
	for _, tc := range []struct {
		claims  *UserClaims
		id      string
		allowed bool
	}{
		{crewToken, "fn-plat366-crew", true}, {crewToken, "fn-plat366-workflow", false},
		{workflowToken, "fn-plat366-workflow", true}, {workflowToken, "fn-plat366-crew", false},
	} {
		raw, _ := json.Marshal(map[string]interface{}{"name": "functions", "arguments": map[string]interface{}{"action": "status", "call_id": tc.id}})
		rec := httptest.NewRecorder()
		env.api.handleExternalCall(rec, adminRequest("POST", "/api/external/v1/call", string(raw), tc.claims, nil))
		if tc.allowed && rec.Code != 200 {
			t.Fatalf("allowed call-id lookup: %d %s", rec.Code, rec.Body.String())
		}
		if !tc.allowed && (rec.Code != 403 || !strings.Contains(rec.Body.String(), "insufficient_scope")) {
			t.Fatalf("cross-product scope must refuse explicitly: %d %s", rec.Code, rec.Body.String())
		}
	}

}

func TestCrewFunctionQuestionIsBoundToCallAndRunScope(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	store := virtualtools.GetHumanFeedbackStore()
	for _, id := range []string{"fn-question-a", "fn-question-b"} {
		call := &crewFunctionCall{ID: id, UserID: "owner", CallerKind: triggerCallerUser, TargetKind: triggerCallerCrew, TargetID: "beta", Status: "running", CreatedAt: time.Now(), UpdatedAt: time.Now(), done: make(chan struct{})}
		crewFunctionCalls.Lock()
		crewFunctionCalls.m[id] = call
		crewFunctionCalls.Unlock()
		t.Cleanup(func() {
			crewFunctionCalls.Lock()
			delete(crewFunctionCalls.m, id)
			crewFunctionCalls.Unlock()
			store.WithdrawOperation(id)
		})
	}
	if err := store.CreatePendingRequest("request-a", "Which branch?", "", "chat-shared", []string{"main", "release"}, false, time.Minute, "fn-question-a"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreatePendingRequest("request-b", "Ship it?", "", "chat-shared", nil, true, time.Minute, "fn-question-b"); err != nil {
		t.Fatal(err)
	}
	runner := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}, AllCrews: true}}
	readonly := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	if code, out := externalCrewRequest(t, env, runner, "project_functions_get_call", map[string]any{"call_id": "fn-question-a"}); code != 200 || len(out["pending_inputs"].([]any)) != 1 {
		t.Fatalf("call A must see just its question: %d %v", code, out)
	}
	args := map[string]any{"call_id": "fn-question-a", "request_id": "request-b", "response": "yes"}
	if code, _ := externalCrewRequest(t, env, runner, "project_functions_reply", args); code != 409 {
		t.Fatalf("cross-call answer must be refused, got %d", code)
	}
	args["request_id"] = "request-a"
	args["response"] = "wrong"
	if code, _ := externalCrewRequest(t, env, runner, "project_functions_reply", args); code != 409 {
		t.Fatalf("invalid choice must be refused, got %d", code)
	}
	args["response"] = "main"
	if code, _ := externalCrewRequest(t, env, readonly, "project_functions_reply", args); code != 403 {
		t.Fatalf("read-only token answered a question: %d", code)
	}
	if code, out := externalCrewRequest(t, env, runner, "project_functions_reply", args); code != 200 || out["status"] != "submitted" {
		t.Fatalf("valid answer failed: %d %v", code, out)
	}
	if code, _ := externalCrewRequest(t, env, runner, "project_functions_reply", args); code != 409 {
		t.Fatalf("duplicate answer must be refused, got %d", code)
	}
}

// PLAT-861-342: admitted Crew members see every published function call and result
// under the existing one-company sharing rule, regardless of who made the call.
func TestListCrewFunctionCallsMembersSeeAllResults(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	seed := func(id, user string, isolated bool) {
		call := &crewFunctionCall{ID: id, UserID: user, CallerKind: triggerCallerUser, CallerLabel: user + " connection", Function: "review", TargetKind: triggerCallerCrew, TargetID: "beta",
			Status: "completed", IsolatedExecution: isolated, Answer: "answer-for-" + user, Result: map[string]any{"answer": "answer-for-" + user}, Files: []structuredFunctionFile{{Name: "report.pdf", Size: 32, MIMEType: "application/pdf"}}, CreatedAt: time.Now(), UpdatedAt: time.Now(), done: make(chan struct{})}
		crewFunctionCalls.Lock()
		crewFunctionCalls.m[id] = call
		crewFunctionCalls.Unlock()
		t.Cleanup(func() {
			crewFunctionCalls.Lock()
			delete(crewFunctionCalls.m, id)
			crewFunctionCalls.Unlock()
		})
	}
	seed("fn-list-owner", "owner", false)
	seed("fn-list-someone", "someone", true)
	token := func(user string) *UserClaims {
		return &UserClaims{UserID: user, AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	}

	code, out := externalCrewRequest(t, env, token("owner"), "project_functions_calls", map[string]any{"crew_id": "beta"})
	calls, _ := out["calls"].([]any)
	if code != 200 || len(calls) != 2 {
		t.Fatalf("the owner sees every call: %d %v", code, out)
	}
	for _, raw := range calls {
		call := raw.(map[string]any)
		for _, field := range []string{"answer", "result", "files"} {
			if _, present := call[field]; !present {
				t.Fatalf("shared function history is missing %s: %v", field, call)
			}
		}
	}
	code, detail := externalCrewRequest(t, env, token("owner"), "project_functions_get_call", map[string]any{"call_id": "fn-list-someone"})
	if code != 200 || detail["answer"] != "answer-for-someone" || detail["result"] == nil {
		t.Fatalf("the owner can directly read another caller's result (PLAT-841): %d %v", code, detail)
	}
	code, detail = externalCrewRequest(t, env, token("owner"), "project_functions_get_call", map[string]any{"call_id": "fn-list-owner"})
	if code != 200 || detail["result"] == nil || detail["answer"] != nil {
		t.Fatalf("a legacy call retains its original result envelope: %d %v", code, detail)
	}
	code, out = externalCrewRequest(t, env, token("someone"), "project_functions_calls", map[string]any{"crew_id": "beta"})
	calls, _ = out["calls"].([]any)
	if code != 200 || len(calls) != 2 {
		t.Fatalf("an admitted person sees every shared call: %d %v", code, out)
	}
	for _, raw := range calls {
		if raw.(map[string]any)["result"] == nil {
			t.Fatalf("an admitted person sees every shared result: %v", raw)
		}
	}
}

// Only the Crew's owner gets the commands the Crew's agent ran for a call, with secret shapes masked (PLAT-837).
func TestOnlyTheOwnerSeesTheCommandsACallRan(t *testing.T) {
	store := storeevents.NewEventStore(50)
	store.AddEvent("sess-cmd", terminalRouteToolStartEvent("sess-cmd", "exec", "t1", "execute_shell_command", `{"command":"ls -la && echo API_KEY=supersecretvalue123"}`, nil))
	store.AddEvent("sess-cmd", terminalRouteToolStartEvent("sess-cmd", "exec", "t2", "read_file", `{"path":"notes.md"}`, nil))
	api := &StreamingAPI{eventStore: store}
	call := &crewFunctionCall{ID: "fn-cmd", TargetChatSession: "sess-cmd"}

	notOwner := map[string]interface{}{}
	api.addCrewCallCommands(context.Background(), false, call, notOwner)
	if _, leaked := notOwner["commands_run"]; leaked {
		t.Fatalf("a caller who is not the owner must not see the commands: %v", notOwner)
	}
	owner := map[string]interface{}{}
	api.addCrewCallCommands(context.Background(), true, call, owner)
	commands, _ := owner["commands_run"].([]map[string]interface{})
	if len(commands) != 2 || !strings.Contains(commands[0]["command"].(string), "ls -la") {
		t.Fatalf("the owner sees the shell command and the other tool: %v", owner)
	}
	if strings.Contains(commands[0]["command"].(string), "supersecretvalue123") {
		t.Fatalf("a secret in a command must be masked: %v", commands[0])
	}
	if _, hasCommand := commands[1]["command"]; hasCommand || commands[1]["tool"] != "read_file" {
		t.Fatalf("another tool lists only its name: %v", commands[1])
	}
}

// A poll with wait_seconds blocks until the call finishes and then returns at once; without it the poll answers
// immediately. A workflow function's status poll waits the same way (PLAT-837).
func TestFunctionCallPollWaitsUntilTheCallFinishes(t *testing.T) {
	call := &crewFunctionCall{ID: "fn-wait", UserID: "owner", TargetKind: triggerCallerWorkflow, Status: "running", CreatedAt: time.Now(), UpdatedAt: time.Now(), done: make(chan struct{})}
	started := time.Now()
	out := externalWorkflowCallResponse(context.Background(), call, 0)
	if time.Since(started) > time.Second || out["status"] != "running" || out["next"] == nil {
		t.Fatalf("a poll without wait answers at once: %v", out)
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		call.settle("completed", map[string]any{"answer": "done"}, "")
	}()
	started = time.Now()
	out = externalWorkflowCallResponse(context.Background(), call, 5*time.Second)
	if elapsed := time.Since(started); elapsed < 100*time.Millisecond || elapsed > 3*time.Second || out["status"] != "completed" {
		t.Fatalf("a waiting poll returns when the call finishes (after %v): %v", elapsed, out)
	}
}
