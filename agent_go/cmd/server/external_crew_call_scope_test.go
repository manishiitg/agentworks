package server

import (
	"testing"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// PLAT-366: get_crew_function_call returns only the caller's calls to a Crew.
// A Crew-only token (AllCrews) must not read the same user's workflow calls,
// which belong to get_workflow_function_call under workflow access.
func TestGetCrewFunctionCallRefusesWorkflowCalls(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	seed := func(id, kind, target string) {
		call := &crewFunctionCall{ID: id, UserID: "owner", CallerKind: triggerCallerUser, TargetKind: kind, TargetID: target,
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
	seed("fn-plat366-workflow", triggerCallerWorkflow, "invoices")
	seed("fn-plat366-crew", triggerCallerCrew, "beta")
	crewToken := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}

	if code, out := externalCrewRequest(t, env, crewToken, "get_crew_function_call", map[string]any{"call_id": "fn-plat366-workflow"}); code != 404 {
		t.Fatalf("a Crew-only token read a workflow call: %d %v", code, out)
	}
	if code, out := externalCrewRequest(t, env, crewToken, "get_crew_function_call", map[string]any{"call_id": "fn-plat366-crew"}); code != 200 {
		t.Fatalf("the user's own Crew call must stay readable: %d %v", code, out)
	}
	other := &UserClaims{UserID: "someone", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	if code, _ := externalCrewRequest(t, env, other, "get_crew_function_call", map[string]any{"call_id": "fn-plat366-crew"}); code != 404 {
		t.Fatalf("another user read the call: %d", code)
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
	if code, out := externalCrewRequest(t, env, runner, "get_crew_function_call", map[string]any{"call_id": "fn-question-a"}); code != 200 || len(out["pending_inputs"].([]any)) != 1 {
		t.Fatalf("call A must see just its question: %d %v", code, out)
	}
	args := map[string]any{"call_id": "fn-question-a", "request_id": "request-b", "response": "yes"}
	if code, _ := externalCrewRequest(t, env, runner, "reply_crew_function_call", args); code != 409 {
		t.Fatalf("cross-call answer must be refused, got %d", code)
	}
	args["request_id"] = "request-a"
	args["response"] = "wrong"
	if code, _ := externalCrewRequest(t, env, runner, "reply_crew_function_call", args); code != 409 {
		t.Fatalf("invalid choice must be refused, got %d", code)
	}
	args["response"] = "main"
	if code, _ := externalCrewRequest(t, env, readonly, "reply_crew_function_call", args); code != 403 {
		t.Fatalf("read-only token answered a question: %d", code)
	}
	if code, out := externalCrewRequest(t, env, runner, "reply_crew_function_call", args); code != 200 || out["status"] != "submitted" {
		t.Fatalf("valid answer failed: %d %v", code, out)
	}
	if code, _ := externalCrewRequest(t, env, runner, "reply_crew_function_call", args); code != 409 {
		t.Fatalf("duplicate answer must be refused, got %d", code)
	}
}

// A Crew's owner sees every call to it (who, which function, how it ended) without other people's results; anyone else
// sees only their own calls, in full (PLAT-837).
func TestListCrewFunctionCallsOwnerSeesAllOthersSeeTheirOwn(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	seed := func(id, user string) {
		call := &crewFunctionCall{ID: id, UserID: user, CallerKind: triggerCallerUser, CallerLabel: user + " connection", Function: "ask", TargetKind: triggerCallerCrew, TargetID: "beta",
			Status: "completed", Result: map[string]any{"answer": "answer-for-" + user}, CreatedAt: time.Now(), UpdatedAt: time.Now(), done: make(chan struct{})}
		crewFunctionCalls.Lock()
		crewFunctionCalls.m[id] = call
		crewFunctionCalls.Unlock()
		t.Cleanup(func() {
			crewFunctionCalls.Lock()
			delete(crewFunctionCalls.m, id)
			crewFunctionCalls.Unlock()
		})
	}
	seed("fn-list-owner", "owner")
	seed("fn-list-someone", "someone")
	token := func(user string) *UserClaims {
		return &UserClaims{UserID: user, AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	}

	code, out := externalCrewRequest(t, env, token("owner"), "list_crew_function_calls", map[string]any{"crew_id": "beta"})
	calls, _ := out["calls"].([]any)
	if code != 200 || len(calls) != 2 {
		t.Fatalf("the owner sees every call: %d %v", code, out)
	}
	for _, raw := range calls {
		call := raw.(map[string]any)
		_, hasResult := call["result"]
		mine := call["caller"].(map[string]any)["you"] == true
		if hasResult != mine {
			t.Fatalf("the owner sees a result only on their own call, got %v", call)
		}
	}
	code, out = externalCrewRequest(t, env, token("someone"), "list_crew_function_calls", map[string]any{"crew_id": "beta"})
	calls, _ = out["calls"].([]any)
	if code != 200 || len(calls) != 1 || calls[0].(map[string]any)["call_id"] != "fn-list-someone" || calls[0].(map[string]any)["result"] == nil {
		t.Fatalf("another user sees only their own call, in full: %d %v", code, out)
	}
}
