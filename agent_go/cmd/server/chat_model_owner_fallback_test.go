package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// One place to choose a model (owner, 26 Nov): the chat. A project has no model setting of its own.
type chatModelFixture struct {
	t    *testing.T
	f    *externalToolsFixture
	root string
}

func newChatModelFixture(t *testing.T) chatModelFixture {
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AUTH_SECRET", "chat-model-test-secret")
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "claude-code,codex-cli")
	t.Setenv("ALLOW_PERSONAL_PROVIDER_CONNECTIONS", "")
	t.Setenv("LLM_CONFIG_LOCKED", "")
	t.Setenv("AGENTWORKS_PROVIDER_POLICY", "")
	var err error
	if f.api.chatStore, err = chathistory.NewFilesystemStore(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	joinCrewTeam(t, "test-team", "other")
	return chatModelFixture{t: t, f: f}
}

// openTab makes a person's side chat in the project, as the "New agent" button does (optionally opened from a chat).
func (c *chatModelFixture) openTab(person, key, openedFrom string) {
	c.t.Helper()
	body := AgentProfileConversationRequest{ConversationKey: key, Name: "Helper", OpenedFrom: openedFrom}
	req := mux.SetURLVars(sharedSecretsRequest(http.MethodPost, "/api/agent-profiles/work/conversation", person, body), map[string]string{"id": "work"})
	w := httptest.NewRecorder()
	c.f.api.handleResolveAgentProfileConversation(w, req)
	if w.Code != http.StatusOK {
		c.t.Fatalf("open %s as %s: %d %s", key, person, w.Code, w.Body.String())
	}
	if c.root == "" {
		profile, _ := c.f.api.agentProfiles.Resolve(crewProfileID, 0, person)
		binding, _, err := resolveConversationBindingForUser(context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: person}), person, profile, key)
		if err != nil {
			c.t.Fatal(err)
		}
		c.root = binding.WorkspacePath
	}
}

func (c *chatModelFixture) pick(person, key, engine, model string) {
	c.t.Helper()
	body := map[string]any{"project_id": "team", "conversation_key": key, "engine": engine, "model_id": model}
	req := mux.SetURLVars(sharedSecretsRequest(http.MethodPost, "/api/agent-profiles/work/chat-models", person, body), map[string]string{"id": "work"})
	w := httptest.NewRecorder()
	c.f.api.handleAgentProfileChatModels(w, req)
	if w.Code != http.StatusOK {
		c.t.Fatalf("pick as %s: %d %s", person, w.Code, w.Body.String())
	}
}

func (c *chatModelFixture) fallback() *workflowtypes.AgentLLMConfig {
	c.t.Helper()
	raw, _, err := readProjectRuntimeManifest(context.Background(), crewProfileID, c.root)
	if err != nil {
		c.t.Fatal(err)
	}
	var manifest struct {
		Capabilities struct {
			LLMConfig *workflowtypes.PresetLLMConfig `json:"llm_config"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		c.t.Fatal(err)
	}
	return presetPrimaryLLMForChat(manifest.Capabilities.LLMConfig)
}

// Chats with no model of their own (schedules, agent messages, Users) run on the main owner's last-used model; a
// teammate's pick changes only their own chat, and returning a chat to the fallback does not clear it.
func TestOwnerChatModelPickBecomesProjectFallback(t *testing.T) {
	c := newChatModelFixture(t)
	c.openTab("other", "team:chat:teammate", "")
	c.openTab("owner", "team:chat:owner", "")
	c.pick("other", "team:chat:teammate", "codex-cli", "gpt-6-astra")
	if got := c.fallback(); got != nil {
		t.Fatalf("a teammate's pick became the project fallback: %+v", got)
	}
	c.pick("owner", "team:chat:owner", "codex-cli", "gpt-6-astra")
	if got := c.fallback(); got == nil || got.Provider != "codex-cli" || got.ModelID != "gpt-6-astra" {
		t.Fatalf("the owner's pick is not the project fallback: %+v", got)
	}
	c.pick("owner", "team:chat:owner", "", "")
	if got := c.fallback(); got == nil || got.Provider != "codex-cli" {
		t.Fatalf("returning a chat to the fallback cleared it: %+v", got)
	}
	// The empty project's start page shows the model its first chat will use; it was missing because the
	// not-yet-existing chat had no session or folder (#4472).
	req := mux.SetURLVars(sharedSecretsRequest(http.MethodGet, "/api/agent-profiles/work/chat-models?project_id=team", "owner", nil), map[string]string{"id": "work"})
	w := httptest.NewRecorder()
	c.f.api.handleAgentProfileChatModels(w, req)
	var state struct {
		NewChatModel map[string]string `json:"new_chat_model"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &state) != nil || state.NewChatModel["display_label"] == "" {
		t.Fatalf("start page has no model for the first chat: %d %s", w.Code, w.Body.String())
	}
}

// A new agent starts on the model of the chat it was opened from, pinned as its own model.
func TestNewChatStartsOnOpenerModel(t *testing.T) {
	c := newChatModelFixture(t)
	c.openTab("owner", "team:chat:opener", "")
	c.pick("owner", "team:chat:opener", "codex-cli", "gpt-6-astra")
	c.openTab("owner", "team:chat:helper", "team:chat:opener")
	own, err := defaultProductConversationRegistryStore().agentIdentity(context.Background(), "owner", crewProfileID, "team:chat:helper")
	if err != nil || own.Engine != "codex-cli" || own.ModelID != "gpt-6-astra" {
		t.Fatalf("new chat did not start on its opener's model: %+v %v", own, err)
	}
}
