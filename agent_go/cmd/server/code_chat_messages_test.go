package server

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"
)

// Code messaging discovers only the owner's main and sibling chats, never
// isolated function/schedule conversations or another Code.
func TestCodeMessagingListsOnlySiblingChats(t *testing.T) {
	files := map[string]string{}
	store := productConversationRegistryStore{
		read: func(_ context.Context, path string) (string, bool, error) {
			raw, ok := files[path]
			return raw, ok, nil
		},
		write: func(_ context.Context, path, content string) error { files[path] = content; return nil },
		now:   time.Now, newID: func() string { return "x" },
	}
	entries := map[string]ProductConversationRecord{}
	for _, key := range []string{"p1", "p1:chat:aa", "p1:chat:bb", "p1:trigger:t1", "p1:chat:aa:x", "p2", "p2:chat:cc"} {
		entries["code/"+key] = ProductConversationRecord{ConversationKey: key, SessionID: "s-" + key}
	}
	entries["work/p1:chat:zz"] = ProductConversationRecord{ConversationKey: "p1:chat:zz", SessionID: "s-work"}
	raw, _ := json.Marshal(productConversationRegistryDocument{Version: 1, Entries: entries})
	files[productConversationRegistryPath("u1")] = string(raw)
	chats, err := store.projectChats(context.Background(), "u1", "code", "p1")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, chat := range chats {
		keys = append(keys, chat.ConversationKey)
	}
	sort.Strings(keys)
	if got := strings.Join(keys, ","); got != "p1,p1:chat:aa,p1:chat:bb" {
		t.Fatalf("siblings = %s", got)
	}

	env := newCrewFunctionEnv(t)
	const codePath = "_users/owner/Chats/Code/projects/private-alpha"
	env.mock.mu.Lock()
	env.mock.files[codePath+"/product.json"] = `{"schema_version":1,"product":"code","id":"alpha","title":"Private project","session_id":"code-alpha"}`
	env.mock.mu.Unlock()
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	_, err = crewTriggerLinkCaller(codePath)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	main := codeChat{Key: "alpha", ID: "main", Name: "Main chat", SessionID: "s-main"}
	side := codeChat{Key: "alpha:chat:aa", ID: "aa", Name: "Chat 2", SessionID: "s-aa"}
	project := func(self codeChat) codeChatProject {
		return codeChatProject{OwnerID: "owner", ProjectID: "alpha", Workspace: codePath, Chats: []codeChat{main, side}, Self: self}
	}
	if _, err := project(main).find("p2"); err == nil {
		t.Fatal("a chat outside this Code was found")
	}
	reg := &recordingRegistrar{}
	if err := env.api.registerCrewFunctionTools(reg, "owner", main.SessionID, QueryRequest{SelectedFolder: codePath}, crewTriggerLinkCaller(codePath), nil); err != nil {
		t.Fatal(err)
	}
	if _, old := reg.tools["return_function_result"]; old {
		t.Fatal("obsolete result tool registered")
	}
	if _, exists := reg.tools["send_message"]; !exists {
		t.Fatal("explicit messaging tool absent")
	}
}

// The tabs of a Crew are equal agents that find each other (PLAT-861-262),
// but only in the owner's own turns: another person's turn in that Crew
// never reaches the owner's tabs.
func TestCrewTabsFindEachOtherOnlyForTheOwner(t *testing.T) {
	env := newTriggerLinkEnv(t)
	crewOwnerTabsFixture(t, env)
	env.api.agentProfiles = env.svc.registry
	ctx := internalBotRequestContext(t.Context(), "owner")
	profile, err := env.svc.registry.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	binding, owned, err := resolveConversationBindingForUser(ctx, "owner", profile, "beta")
	if err != nil || !owned {
		t.Fatalf("owner binding = %v %v", owned, err)
	}
	conversation, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, "owner", profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}
	project, err := env.api.codeChatsFor(ctx, "owner", conversation.SessionID, binding.WorkspacePath)
	if err != nil || project.ProfileID != "work" || !project.Self.IsMain || project.Self.ID == codeChatMainName {
		t.Fatalf("owner's Crew chat = %+v, %v", project, err)
	}
	reg := &recordingRegistrar{}
	if err := env.api.registerCodeChatTools(reg, nil, "owner", conversation.SessionID, binding.WorkspacePath); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.tools["ask_project_chat"].exec(ctx, map[string]interface{}{"chat": "main", "message": "Self ask"}); err == nil || err.Error() != "a chat cannot ask itself" {
		t.Fatalf("main alias self-ask = %v", err)
	}
	if target := codeChatTarget(project, project.Self, binding.WorkspacePath); target.CrewProfile != "work" {
		t.Fatalf("a Crew tab is addressed as %q", target.CrewProfile)
	}
	other := internalBotRequestContext(t.Context(), "other")
	if _, err := env.api.codeChatsFor(other, "other", conversation.SessionID, binding.WorkspacePath); err == nil {
		t.Fatal("another person's turn reached the owner's Crew tabs")
	}
}
