package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// #4439: a chat opened without a name is named by the server from its first message, and a name a person
// gives is never overwritten by the later AI title.
func TestUnnamedChatIsNamedFromFirstMessageAndRenameWins(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	crewOwnerTabsFixture(t, env)
	ctx := internalBotRequestContext(t.Context(), "owner")
	profile, err := env.api.agentProfiles.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	store := defaultProductConversationRegistryStore()
	open := func(key string) ProductConversationRecord {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/agent-profiles/work/conversation", strings.NewReader(`{"conversation_key":"`+key+`"}`))
		req = mux.SetURLVars(req.WithContext(ctx), map[string]string{"id": "work"})
		rec := httptest.NewRecorder()
		env.api.handleResolveAgentProfileConversation(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("open %s without a name: %d %s", key, rec.Code, rec.Body.String())
		}
		var out AgentProfileConversationResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		identity, _ := store.agentIdentity(ctx, "owner", "work", key)
		if identity.Name != autoChatPlaceholderName || identity.AutoName != autoNameWaitingForMessage {
			t.Fatalf("new unnamed chat identity = %+v", identity)
		}
		return ProductConversationRecord{ConversationKey: key, SessionID: out.SessionID}
	}

	// First words, without the greeting. With no AI account recorded the title step gives up and the first words stay.
	first := open("beta:chat:auto1")
	env.api.autoNameChatOnSend(ctx, "owner", profile, first, "Hi, can you help me fix the login page redirect after sign out", QueryRequest{})
	deadline := time.Now().Add(5 * time.Second)
	for {
		identity, _ := store.agentIdentity(ctx, "owner", "work", first.ConversationKey)
		if identity.AutoName == "" {
			if identity.Name != "Fix the login page redirect after" {
				t.Fatalf("first words name = %q", identity.Name)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("auto name never settled: %+v", identity)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// A person renames while the AI title is on its way: the title must not replace their name.
	second := open("beta:chat:auto2")
	if ok, err := store.applyAutoName(ctx, "owner", "work", second.ConversationKey, autoNameWaitingForMessage, "Draft words", autoNameWaitingForTitle); err != nil || !ok {
		t.Fatalf("first words step: %v %v", ok, err)
	}
	mine := "Release notes"
	if _, err := store.setAgentIdentity(ctx, "owner", "work", second.ConversationKey, &mine, nil, "owner"); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.applyAutoName(ctx, "owner", "work", second.ConversationKey, autoNameWaitingForTitle, "AI title", ""); err != nil || ok {
		t.Fatalf("AI title replaced a person's name: %v %v", ok, err)
	}
	if identity, _ := store.agentIdentity(ctx, "owner", "work", second.ConversationKey); identity.Name != mine {
		t.Fatalf("name after rename = %q", identity.Name)
	}

	for message, want := range map[string]string{"hello": "", "Please: \"summarise this PDF.\"": "Summarise this PDF"} {
		if got := autoChatFirstWordsName(message); got != want {
			t.Fatalf("first words of %q = %q, want %q", message, got, want)
		}
	}
	// A project started from the start page: a typed name wins, then an existing folder's own name, then the message.
	for _, c := range []struct{ name, root, message, want string }{
		{"Mine", "/work/shop", "fix it", "Mine"},
		{"", "/work/shop/", "fix the cart", "shop"},
		{"", "", "Hi, fix the cart total", "Fix the cart total"},
		{"", "", "hello", "New project"},
	} {
		if got := startProjectName(c.name, c.root, c.message); got != c.want {
			t.Fatalf("startProjectName(%q, %q, %q) = %q, want %q", c.name, c.root, c.message, got, c.want)
		}
	}
}
