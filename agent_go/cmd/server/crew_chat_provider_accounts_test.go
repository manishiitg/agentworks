package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// Exercise the same saved account over website HTTP, native Builder chat and
// external MCP, then the actual turn account scope and credential admission.
func TestCrewChatProviderAccountsFollowTabOwner(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AUTH_SECRET", "crew-provider-test-secret")
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "claude-code,codex-cli,muse-cli")
	t.Setenv("ALLOW_PERSONAL_PROVIDER_CONNECTIONS", "")
	t.Setenv("LLM_CONFIG_LOCKED", "")
	t.Setenv("AGENTWORKS_PROVIDER_POLICY", "")
	var err error
	f.api.chatStore, err = chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := defaultProjectOwners().AddCoOwner("work", "test-team", "other"); err != nil {
		t.Fatal(err)
	}
	env := &providerAccountsEnv{api: f.api}
	personal := env.addAccount(t, "other", map[string]any{"provider": "codex-cli", "display_name": "Personal coding account", "credential": "test-personal-credential"})
	private := env.addAccount(t, "owner", map[string]any{"provider": "codex-cli", "display_name": "Another person's private account", "credential": "test-private-credential"})
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "other"})
	profile, err := f.api.agentProfiles.Resolve(crewProfileID, 0, "other")
	if err != nil {
		t.Fatal(err)
	}
	binding, canBuild, err := resolveConversationBindingForUser(ctx, "other", profile, "team")
	if err != nil || !canBuild {
		t.Fatalf("binding: %t %v", canBuild, err)
	}
	store := defaultProductConversationRegistryStore()
	chat, err := store.resolveOrCreate(ctx, "other", profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}
	resolved := &resolvedAgentProfile{Definition: profile}
	reg := &recordingRegistrar{}
	if err := f.api.registerCrewSettingsChatTools(reg, newProductToolGate(resolved), resolved, GetUserFromContext(ctx), false, chat.SessionID, binding.WorkspacePath, "https://example.com"); err != nil {
		t.Fatal(err)
	}
	save := func(person, id string, want int) string {
		t.Helper()
		body := map[string]any{"project_id": "team", "conversation_key": chat.ConversationKey, "engine": "codex-cli", "model_id": "gpt-6-astra", "connection_id": id}
		req := mux.SetURLVars(sharedSecretsRequest(http.MethodPost, "/api/agent-profiles/work/chat-models", person, body), map[string]string{"id": "work"})
		w := httptest.NewRecorder()
		f.api.handleAgentProfileChatModels(w, req)
		if w.Code != want {
			t.Fatalf("save as %s: %d %s", person, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	save("other", private.ID, 403)
	saved := save("other", personal.ID, 200)
	if !strings.Contains(saved, personal.ID) {
		t.Fatal("own account was not returned")
	}
	save("owner", private.ID, 403) // Even the main owner cannot change this tab.
	list, err := reg.tools["manage_crew_model"].exec(ctx, map[string]any{"action": "list"})
	if err != nil || !strings.Contains(list, personal.ID) || strings.Contains(list, private.ID) || strings.Contains(list, "test-personal-credential") {
		t.Fatalf("native account discovery: %v %s", err, list)
	}
	native, err := reg.tools["manage_crew_model"].exec(ctx, map[string]any{"action": "set", "engine": "codex-cli", "model_id": "gpt-6-astra", "connection_id": personal.ID})
	if err != nil || !strings.Contains(native, personal.ID) {
		t.Fatalf("native selection: %v %s", err, native)
	}
	w := httptest.NewRecorder()
	f.api.externalCrewChatsCall(w, sharedSecretsRequest(http.MethodGet, "/", "owner", nil), map[string]any{"crew_id": "team", "action": "list"})
	if w.Code != 200 || strings.Contains(w.Body.String(), personal.ID) {
		t.Fatalf("another owner saw a private account ID: %d %s", w.Code, w.Body.String())
	}
	run := func(account string) QueryRequest {
		t.Helper()
		current, err := store.resolveOrCreate(ctx, "other", profile, binding, "")
		if err != nil {
			t.Fatal(err)
		}
		query, err := prepareProductConversationTurn(ctx, "other", profile, AgentProfileChatRequest{Message: "Continue", Engine: "codex-cli", ConnectionID: private.ID}, current)
		if err != nil || query.ConnectionID != account {
			t.Fatalf("saved choice lost in turn: %v %+v", err, query)
		}
		own, err := crewChatUsesPersonalAccountScope(ctx, "other", current.SessionID, query, resolved, query.Provider, query.ConnectionID)
		if err != nil || !own {
			t.Fatalf("explicit account scope: %t %v", own, err)
		}
		if _, err := f.api.admitProviderAccount(ctx, queryProviderAccountScope(query, "other", resolved, false, ""), query.Provider, query.ConnectionID); err != nil {
			t.Fatal(err)
		}
		stale := query
		stale.ConnectionID = private.ID
		if _, err := crewChatUsesPersonalAccountScope(ctx, "other", current.SessionID, stale, resolved, stale.Provider, stale.ConnectionID); err == nil {
			t.Fatal("mismatched account accepted as a saved tab choice")
		}
		return query
	}
	run(personal.ID)
	// Company account selection uses the same MCP handler and actual person.
	req := sharedSecretsRequest(http.MethodPost, "/", "other", nil)
	w = httptest.NewRecorder()
	f.api.externalCrewChatsCall(w, req, map[string]any{"crew_id": "team", "action": "set_model", "chat_id": chat.SessionID, "engine": "codex-cli", "model_id": "gpt-6-astra", "connection_id": "global:codex-cli"})
	if w.Code != 200 {
		t.Fatalf("MCP company choice: %d %s", w.Code, w.Body.String())
	}
	run("global:codex-cli")
	// Reset through chat restores the project's account, including on API callers
	// that send stale fields. Its old private CLI session must not resume.
	if _, err := reg.tools["manage_crew_model"].exec(ctx, map[string]any{"action": "set", "engine": ""}); err != nil {
		t.Fatal(err)
	}
	current, err := store.resolveOrCreate(ctx, "other", profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}
	current.ProjectLLMConfig = &workflowtypes.PresetLLMConfig{}
	if err := json.Unmarshal([]byte(`{"schema_version":2,"mode":"explicit","builder_llm":{"provider":"codex-cli","model_id":"gpt-6-astra","connection_id":"global:codex-cli"}}`), current.ProjectLLMConfig); err != nil {
		t.Fatal(err)
	}
	query, err := prepareProductConversationTurn(ctx, "other", profile, AgentProfileChatRequest{Message: "Continue", Engine: "codex-cli", ConnectionID: personal.ID}, current)
	if err != nil || query.ConnectionID != "global:codex-cli" {
		t.Fatalf("reset kept stale account: %v %+v", err, query)
	}
	if own, err := crewChatUsesPersonalAccountScope(ctx, "other", current.SessionID, query, resolved, query.Provider, query.ConnectionID); err != nil || own {
		t.Fatalf("inherited choice did not use Crew identity: %t %v", own, err)
	}
	// Availability is rechecked at runtime, rather than trusting a saved ID.
	save("other", personal.ID, 200)
	providerConnectionsMu.Lock()
	records, err := loadProviderConnections(ctx)
	if err == nil {
		filtered := records[:0]
		for _, record := range records {
			if record.ID != personal.ID {
				filtered = append(filtered, record)
			}
		}
		err = saveProviderConnections(ctx, filtered)
	}
	providerConnectionsMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.admitProviderAccount(ctx, providerAccountScope{Principal: "other", WorkspacePath: binding.WorkspacePath, Product: "work"}, "codex-cli", personal.ID); err == nil {
		t.Fatal("removed account remained usable")
	}
}
