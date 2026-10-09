package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
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

// A person asking a Crew over MCP talks in their own chat of it, exactly the
// chat a 1:1 Slack DM or WhatsApp message continues: for the owner, the
// Crew's own chat. No per-caller trigger conversation is created.
func TestExternalAskCrewRunsInCallersOwnChatAndIsPollable(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	type turn struct {
		req       map[string]interface{}
		sessionID string
		userID    string
	}
	turns := make(chan turn, 4)
	crewOwnChatAskTurn = func(_ *StreamingAPI, _ context.Context, reqMap map[string]interface{}, sessionID, userID string) (internalSessionTurnResult, error) {
		turns <- turn{reqMap, sessionID, userID}
		return internalSessionTurnResult{FinalResponse: "Nothing changed today."}, nil
	}
	t.Cleanup(func() { crewOwnChatAskTurn = nil })
	runner := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Name: "laptop", Scopes: []string{"crews:run"}, CrewIDs: []string{"beta"}}}

	code, out := externalCrewRequest(t, env, runner, "ask_crew", map[string]any{"crew_id": "beta", "message": "what changed today?", "wait_seconds": float64(0)})
	if code != 200 {
		t.Fatalf("ask_crew = %d %v", code, out)
	}
	callID, _ := out["call_id"].(string)
	if callID == "" || out["status"] == "failed" {
		t.Fatalf("ask_crew must return a pollable call: %v", out)
	}
	var got turn
	select {
	case got = <-turns:
	case <-time.After(5 * time.Second):
		t.Fatal("the ask never reached the Crew")
	}
	if got.userID != "owner" || got.req["query"] != "what changed today?" || got.req["triggered_by"] != "external" {
		t.Fatalf("the ask must be the owner's own message in their chat: user=%q req=%v", got.userID, got.req)
	}
	profile, err := env.svc.registry.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	_, dmSession, _, err := env.api.senderProfileTurn(context.Background(), "owner", profile, "beta", services.BotIncomingMessage{Platform: "slack", DirectMessage: true, Text: "hi"}, services.ThreadID{})
	if err != nil || dmSession == "" || got.sessionID != dmSession {
		t.Fatalf("MCP ask session %q must be the chat a Slack DM continues (%q, err=%v)", got.sessionID, dmSession, err)
	}
	if triggers, err := env.svc.projectWebhookConfigs(context.Background(), "owner", "work", "beta"); err != nil || len(triggers) != 0 {
		t.Fatalf("a person's ask must not create a per-caller trigger conversation, got %+v err=%v", triggers, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		code, out = externalCrewRequest(t, env, runner, "get_crew_function_call", map[string]any{"call_id": callID})
		if code != 200 || out["call_id"] != callID {
			t.Fatalf("poll = %d %v", code, out)
		}
		if out["status"] == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("call never completed: %v", out)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if result, _ := out["result"].(map[string]interface{}); result["answer"] != "Nothing changed today." {
		t.Fatalf("result = %v", out["result"])
	}

	other := &UserClaims{UserID: "other", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}, AllCrews: true}}
	// Someone else's ask continues their own reader chat, never the owner's.
	if code, out := externalCrewRequest(t, env, other, "ask_crew", map[string]any{"crew_id": "beta", "message": "can you help?"}); code == 200 {
		select {
		case reader := <-turns:
			if reader.userID != "other" || reader.sessionID == "" || reader.sessionID == dmSession {
				t.Fatalf("a non-owner's ask must run in their own chat: user=%q session=%q owner=%q", reader.userID, reader.sessionID, dmSession)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the non-owner's ask never ran")
		}
	} else {
		t.Logf("non-owner cannot reach this Crew in the fixture (%d %v)", code, out)
	}
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
	withCrews := mcpOAuthTokenForGrant(mcpOAuthGrant{UserID: "u", Scopes: []string{"workflows:read", "crews:run"}})
	if !withCrews.AllCrews || !withCrews.AllowsCrew("any") {
		t.Fatal("an OAuth grant approving a Crew permission must reach the user's Crews")
	}
	legacy := mcpOAuthTokenForGrant(mcpOAuthGrant{UserID: "u", Scopes: []string{"workflows:read", "files:read", "runs:execute"}})
	if legacy.AllCrews || legacy.AllowsCrew("any") {
		t.Fatal("an OAuth grant without Crew permissions must not reach Crews")
	}
}

// manage_crew_chats lists only the caller's own chats, ask_crew's chat_id
// must name one of them, and a Crew-bounded connection stays bounded.
func TestExternalCrewChatsAreYourOwn(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	chats := func(claims *UserClaims, args map[string]any) (int, map[string]any) {
		req := httptest.NewRequest("POST", "/api/external/call", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
		rec := httptest.NewRecorder()
		env.api.externalCrewChatsCall(rec, req, args)
		out := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	if code, out := chats(owner, map[string]any{"crew_id": "beta", "action": "list"}); code != 200 || out["chats"] == nil {
		t.Fatalf("list own chats = %d %v", code, out)
	}
	if code, _ := externalCrewRequest(t, env, owner, "ask_crew", map[string]any{"crew_id": "beta", "message": "hi", "chat_id": "not-a-chat"}); code != 404 {
		t.Fatalf("ask_crew into an unknown chat = %d", code)
	}
	bounded := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Name: "laptop", Scopes: []string{"crews:read", "crews:run"}, CrewIDs: []string{"beta"}}}
	if code, _ := chats(bounded, map[string]any{"crew_id": "alpha", "action": "list"}); code != 403 {
		t.Fatalf("Crew-bounded connection listed another Crew's chats: %d", code)
	}
}
