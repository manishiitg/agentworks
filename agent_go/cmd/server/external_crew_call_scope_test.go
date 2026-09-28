package server

import (
	"testing"
	"time"

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
