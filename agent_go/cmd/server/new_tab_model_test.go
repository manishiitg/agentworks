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
	// An engine with no account saves the account its turns will run on. A tab saved with none was checked as the
	// company account while its turns filled in the person's own, so every message to it was refused.
	doc, err := defaultProductConversationRegistryStore().loadDocument(context.Background(), productConversationRegistryPath(owner.UserID))
	if err != nil {
		t.Fatal(err)
	}
	want := productChatDefaultAccount(context.Background(), owner.UserID, choices, choices.Runtime.ProviderOptions[0].Provider)
	saved := false
	for _, own := range doc.Agents {
		if own.Engine == engine {
			saved = true
			if own.ConnectionID == "" || own.ConnectionID != want {
				t.Fatalf("new tab saved account %q, its turns run on %q", own.ConnectionID, want)
			}
		}
	}
	if !saved {
		t.Fatal("the new tab's model choice was not saved")
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

// Fork takes the engine its caller names. It used to copy the source's model and drop the engine without a word, so
// a fork asked for on Cursor came out as Claude. A refused engine makes no fork; an offered one is saved on the fork.
func TestForkRunsOnTheEngineItWasAskedFor(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	call := func(args map[string]any) (int, map[string]any) {
		req := httptest.NewRequest("POST", "/api/external/call", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, owner))
		rec := httptest.NewRecorder()
		env.api.externalCrewChatsCall(rec, req, args)
		out := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, source := call(map[string]any{"crew_id": "beta", "action": "side_open", "name": "Source"})
	if code != 200 {
		t.Fatalf("open source tab: %d %v", code, source)
	}
	if code, out := call(map[string]any{"crew_id": "beta", "action": "fork", "chat_id": source["chat_id"], "engine": "no-such-engine"}); code != 422 || !strings.Contains(strings.ToLower(jsonText(out)), "no fork was made") {
		t.Fatalf("an engine the Crew does not offer was not refused before forking: %d %v", code, out)
	}
	choices, err := env.api.agentProfiles.Resolve("work", 0, owner.UserID)
	if err != nil || len(choices.Runtime.ProviderOptions) == 0 {
		t.Skip("the fixture offers no provider to choose")
	}
	var opened map[string]any
	code, opened = call(map[string]any{"crew_id": "beta", "action": "side_open", "name": "Fork target"})
	if code != 200 {
		t.Fatalf("open target tab: %d %v", code, opened)
	}
	chatID, _ := opened["chat_id"].(string)
	forkBody := []byte(`{"chat_id":"` + chatID + `","engine":"claude-code","provider":"claude-code","connection_id":"global:claude-code"}`)
	req := httptest.NewRequest("POST", "/", nil).WithContext(context.WithValue(context.Background(), UserContextKey, owner))
	if got := env.api.applyForkModel(req, "work", "beta", newTabModel{}, forkBody); string(got) != string(forkBody) {
		t.Fatalf("a fork with no engine changed its reply: %s", got)
	}
	engine := choices.Runtime.ProviderOptions[0].ID
	out := map[string]any{}
	if err := json.Unmarshal(env.api.applyForkModel(req, "work", "beta", newTabModel{engine: engine}, forkBody), &out); err != nil || out["engine"] != engine || out["model_not_applied"] != nil {
		t.Fatalf("fork reply does not run on the asked engine: %v %v", out, err)
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(context.Background(), productConversationRegistryPath(owner.UserID))
	if err != nil {
		t.Fatal(err)
	}
	if own := doc.Agents[productConversationRegistryEntryKey("work", "beta"+codeChatSideMarker+chatID)]; own.Engine != engine {
		t.Fatalf("fork saved engine %q, want %q", own.Engine, engine)
	}
}

func jsonText(v any) string { raw, _ := json.Marshal(v); return string(raw) }
