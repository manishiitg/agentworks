package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/mcpoauth"
)

func externalCrewRequest(t *testing.T, env triggerLinkEnv, claims *UserClaims, name string, args map[string]any) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/external/call", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
	rec := httptest.NewRecorder()
	env.api.externalCrewCall(rec, req, name, args)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestExternalCrewToolsRespectTokenCrewBounds(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	env.mock.mu.Lock()
	env.mock.files[linkBetaPath+"/notes/plan.md"] = "beta plan"
	env.mock.files[linkBetaPath+"/builder/conversation/2026-09-24/session.json"] = "{}"
	env.mock.mu.Unlock()

	bounded := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{"beta"}}}
	code, out := externalCrewRequest(t, env, bounded, "list_crews", map[string]any{})
	crews, _ := out["crews"].([]any)
	if code != 200 || len(crews) != 1 || crews[0].(map[string]any)["crew_id"] != "beta" {
		t.Fatalf("bounded list_crews = %d %v", code, out)
	}
	if code, _ := externalCrewRequest(t, env, bounded, "get_crew", map[string]any{"crew_id": "alpha"}); code != 404 {
		t.Fatalf("crew outside the token bound must be not-found, got %d", code)
	}
	code, out = externalCrewRequest(t, env, bounded, "get_crew", map[string]any{"crew_id": "beta"})
	if code != 200 || out["crew_id"] != "beta" {
		t.Fatalf("get_crew beta = %d %v", code, out)
	}
	if functions, _ := out["functions"].([]any); len(functions) == 0 || functions[0].(map[string]any)["name"] != "ask" {
		t.Fatalf("get_crew must list the built-in ask: %v", out["functions"])
	}
	code, out = externalCrewRequest(t, env, bounded, "read_crew_file", map[string]any{"crew_id": "beta", "path": "notes/plan.md"})
	if code != 200 || out["content"] != "beta plan" {
		t.Fatalf("read_crew_file = %d %v", code, out)
	}
	for _, private := range []string{"builder/conversation/2026-09-24/session.json", "product.json", "../alpha/product.json"} {
		if code, _ := externalCrewRequest(t, env, bounded, "read_crew_file", map[string]any{"crew_id": "beta", "path": private}); code != 404 {
			t.Fatalf("private path %q must be refused, got %d", private, code)
		}
	}

	all := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	if _, out := externalCrewRequest(t, env, all, "list_crews", map[string]any{}); len(out["crews"].([]any)) != 3 {
		t.Fatalf("all_crews token must see every accessible Crew (incl. other users'): %v", out)
	}
}

func TestExternalTokenScopeForCrewTools(t *testing.T) {
	tool := externalTool{Name: "list_crews"}
	if externalTokenAllows(&UserClaims{AccessToken: &accesstokens.Token{Scopes: []string{"workflows:read"}, AllWorkflows: true}}, tool) {
		t.Fatal("a workflow-only token must not reach Crew tools")
	}
	if !externalTokenAllows(&UserClaims{AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}, tool) {
		t.Fatal("crews:read must allow list_crews")
	}
}

func TestExternalAskCrewRunsInCrewChatAndIsPollable(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	runner := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Name: "laptop", Scopes: []string{"crews:run"}, CrewIDs: []string{"beta"}}}

	code, out := externalCrewRequest(t, env, runner, "ask_crew", map[string]any{"crew_id": "beta", "message": "what changed today?", "wait_seconds": float64(0)})
	if code != 200 {
		t.Fatalf("ask_crew = %d %v", code, out)
	}
	callID, _ := out["call_id"].(string)
	if callID == "" || out["status"] == "failed" || out["next"] == nil {
		t.Fatalf("ask_crew must return a pollable running call: %v", out)
	}
	// The call is a normal internal trigger on the Crew, bound to this user's
	// external connection and visible in the Crew's own trigger list.
	triggers, err := env.svc.projectWebhookConfigs(context.Background(), "owner", "work", "beta")
	if err != nil || len(triggers) != 1 || triggers[0].Caller == nil || triggers[0].Caller.Type != triggerCallerUser || triggers[0].Caller.ID != "owner" {
		t.Fatalf("expected one trigger bound to the external caller, got %+v err=%v", triggers, err)
	}
	if code, out := externalCrewRequest(t, env, runner, "get_crew_function_call", map[string]any{"call_id": callID}); code != 200 || out["call_id"] != callID {
		t.Fatalf("poll = %d %v", code, out)
	}
	other := &UserClaims{UserID: "other", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}, AllCrews: true}}
	if code, _ := externalCrewRequest(t, env, other, "get_crew_function_call", map[string]any{"call_id": callID}); code != 404 {
		t.Fatalf("another user's poll must be not-found, got %d", code)
	}
	if code, _ := externalCrewRequest(t, env, runner, "call_crew_function", map[string]any{"crew_id": "beta", "function": "nope"}); code != 404 {
		t.Fatalf("unknown function must be not-found, got %d", code)
	}
	if code, _ := externalCrewRequest(t, env, runner, "ask_crew", map[string]any{"crew_id": "alpha", "message": "hi"}); code != 404 {
		t.Fatalf("crew outside the token bound must be not-found, got %d", code)
	}
	readOnly := &UserClaims{AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	if externalTokenAllows(readOnly, externalTool{Name: "ask_crew"}) || externalTokenAllows(readOnly, externalTool{Name: "call_crew_function"}) {
		t.Fatal("crews:read must not allow running a Crew")
	}
	if !externalTokenAllows(readOnly, externalTool{Name: "get_crew_function_call"}) {
		t.Fatal("crews:read may poll")
	}
}

func TestOAuthGrantCrewAccessFollowsApprovedScopes(t *testing.T) {
	withCrews := mcpOAuthTokenForGrant(mcpoauth.Grant{UserID: "u", Scopes: []string{"workflows:read", "crews:run"}})
	if !withCrews.AllCrews || !withCrews.AllowsCrew("any") {
		t.Fatal("an OAuth grant approving a Crew permission must reach the user's Crews")
	}
	legacy := mcpOAuthTokenForGrant(mcpoauth.Grant{UserID: "u", Scopes: []string{"workflows:read", "files:read", "runs:execute"}})
	if legacy.AllCrews || legacy.AllowsCrew("any") {
		t.Fatal("an OAuth grant without Crew permissions must not reach Crews")
	}
}
