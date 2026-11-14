package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// A tab opened with its model takes the same choice check as models action=set scope=tab, and a refused choice opens
// nothing: the agent never needs a second call, and a failed call leaves no stray tab.
func TestSideOpenTakesTheTabModelInOneCall(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	chats := func(args map[string]any) (int, map[string]any) {
		req := httptest.NewRequest("POST", "/api/external/call", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, owner))
		rec := httptest.NewRecorder()
		env.api.externalCrewChatsCall(rec, req, args)
		out := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	tabNames := func() string {
		names := ""
		for _, row := range env.api.crewChats(httptest.NewRequest("GET", "/", nil), owner.UserID, "beta", linkBetaPath) {
			names += "|" + row.Name
		}
		return names
	}
	if code, _ := chats(map[string]any{"crew_id": "beta", "action": "side_open", "name": "Refused", "engine": "no-such-engine"}); code != 422 {
		t.Fatalf("an engine the Crew does not offer was accepted: %d", code)
	}
	if code, _ := chats(map[string]any{"crew_id": "beta", "action": "side_open", "name": "Refused", "model_id": "some-model"}); code != 400 {
		t.Fatalf("a model without an engine was accepted: %d", code)
	}
	if strings.Contains(tabNames(), "Refused") {
		t.Fatal("a refused model choice still opened a tab")
	}
	choices, err := env.api.agentProfiles.Resolve("work", 0, owner.UserID)
	if err != nil || len(choices.Runtime.ProviderOptions) == 0 {
		t.Skip("the fixture offers no provider to choose")
	}
	engine := choices.Runtime.ProviderOptions[0].ID
	if code, out := chats(map[string]any{"crew_id": "beta", "action": "side_open", "name": "On engine", "engine": engine}); code != 200 || out["engine"] != engine {
		t.Fatalf("a tab opened on an offered engine: %d %v", code, out)
	}
}

// The one choice check: an offered provider, model and reasoning level stand; anything else is refused with a status.
func TestChatModelChoiceRefusal(t *testing.T) {
	api := &StreamingAPI{}
	slot := profileAndBinding{profile: agentprofiles.Profile{Runtime: agentprofiles.RuntimePolicy{ProviderOptions: []agentprofiles.ProviderOption{
		{ID: "claude-code", Provider: "claude-code", Models: []string{"claude-sonnet-5-5"}, ReasoningEfforts: []string{"high"}},
	}}}}
	for name, tc := range map[string]struct {
		choice AgentProfileChatModel
		status int
	}{
		"no engine is the Crew default":  {AgentProfileChatModel{}, 0},
		"offered provider, model, level": {AgentProfileChatModel{Engine: "claude-code", ModelID: "claude-sonnet-5-5", ReasoningEffort: "high"}, 0},
		"provider not offered":           {AgentProfileChatModel{Engine: "other"}, 422},
		"model not offered":              {AgentProfileChatModel{Engine: "claude-code", ModelID: "claude-nope-1-1"}, 422},
		"reasoning level not offered":    {AgentProfileChatModel{Engine: "claude-code", ReasoningEffort: "extreme"}, 422},
	} {
		if status, reason := api.chatModelChoiceRefusal(context.Background(), "owner", slot, tc.choice); status != tc.status {
			t.Errorf("%s: status %d (%s), want %d", name, status, reason, tc.status)
		}
	}
}
