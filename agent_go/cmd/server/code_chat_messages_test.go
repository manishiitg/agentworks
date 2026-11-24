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
	bridgeRouteTestMain(t, env.api, "owner", "beta")
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
	if err := env.api.registerCodeChatTools(reg, nil, "owner", conversation.SessionID, binding.WorkspacePath, nil); err != nil {
		t.Fatal(err)
	}
	if _, exists := reg.tools["ask_project_chat"]; exists {
		t.Fatal("retired peer-message tool bypasses AgentBridge")
	}
	// Owner, 22 Nov: a fork made with a task starts on it; with no way to send it, no fork is made.
	fork := reg.tools["fork_project_chat"]
	if _, offered := fork.params["properties"].(map[string]interface{})["task"]; !offered {
		t.Fatal("fork_project_chat does not offer a task")
	}
	if _, err := fork.exec(ctx, map[string]interface{}{"chat": "main", "task": "Review the tools list"}); err == nil || !strings.Contains(err.Error(), "cannot send a fork its task") {
		t.Fatalf("a fork with a task and no send path = %v", err)
	}
	// Owner, 22 Nov: a fresh agent can start with a task too, and either kind can sit beside its maker.
	open := reg.tools["open_project_chat"]
	for name, tool := range map[string]recordedTool{"fork_project_chat": fork, "open_project_chat": open} {
		properties := tool.params["properties"].(map[string]interface{})
		if _, offered := properties["place"]; !offered {
			t.Fatalf("%s does not offer place", name)
		}
		if _, offered := properties["task"]; !offered {
			t.Fatalf("%s does not offer a task", name)
		}
	}
	if _, err := open.exec(ctx, map[string]interface{}{"name": "Critic", "task": "Review the diff"}); err == nil || !strings.Contains(err.Error(), "cannot send a new agent its task") {
		t.Fatalf("a fresh agent with a task and no send path = %v", err)
	}
	if _, err := fork.exec(ctx, map[string]interface{}{"chat": "main", "place": "sideways"}); err == nil || !strings.Contains(err.Error(), "place is under or beside") {
		t.Fatalf("an unknown place = %v", err)
	}
	if place, _ := spawnPlace(map[string]interface{}{}); place != spawnUnder {
		t.Fatalf("default place = %q", place)
	}
	// #3655: making an agent also sets it up, with the same limits and owner rule as sessions action=identity.
	for name, tool := range map[string]recordedTool{"fork_project_chat": fork, "open_project_chat": open} {
		properties := tool.params["properties"].(map[string]interface{})
		for _, field := range []string{"purpose", "instructions"} {
			if _, offered := properties[field]; !offered {
				t.Fatalf("%s does not offer %s", name, field)
			}
		}
	}
	if _, err := open.exec(ctx, map[string]interface{}{"name": "Issue lead", "instructions": strings.Repeat("x", productAgentInstructionsMaxRunes+1)}); err == nil || !strings.Contains(err.Error(), "nothing was made") {
		t.Fatalf("over-long instructions = %v", err)
	}
	purpose, rules := "Leads the issues row", "Fixer done -> Reviewer checks it.\nAnyone stuck -> ask me."
	setUp, err := env.api.spawnIdentityArgs(ctx, conversation.SessionID, "owner", map[string]interface{}{"purpose": purpose, "instructions": rules})
	if err != nil || setUp.empty() {
		t.Fatalf("owner's own setup = %+v, %v", setUp, err)
	}
	saved := map[string]interface{}{}
	saveSpawnIdentity(ctx, "owner", project, project.Self.Key, setUp, saved)
	if saved["purpose"] != purpose || saved["instructions_saved"] != true {
		t.Fatalf("setup not saved: %v", saved)
	}
	env.api.lastQueryMu.Lock()
	if env.api.lastQueryRequests == nil {
		env.api.lastQueryRequests = map[string]QueryRequest{}
	}
	env.api.lastQueryRequests[conversation.SessionID] = QueryRequest{AskedBy: "teammate"}
	env.api.lastQueryMu.Unlock()
	if _, err := env.api.spawnIdentityArgs(ctx, conversation.SessionID, "owner", map[string]interface{}{"instructions": rules}); err == nil || !strings.Contains(err.Error(), "own person") {
		t.Fatalf("a teammate's turn set a new agent's instructions: %v", err)
	}
	env.api.lastQueryMu.Lock()
	delete(env.api.lastQueryRequests, conversation.SessionID)
	env.api.lastQueryMu.Unlock()
	holder := &agentSendHolder{}
	capture := agentSendCaptureRegistrar{&recordingRegistrar{}, holder}
	_ = capture.RegisterCustomTool("read_agent_messages", "", nil, func(context.Context, map[string]interface{}) (string, error) { return "", nil }, "")
	if holder.send != nil {
		t.Fatal("captured a tool other than send_message")
	}
	_ = capture.RegisterCustomTool("send_message", "", nil, func(context.Context, map[string]interface{}) (string, error) { return "sent", nil }, "")
	if out, _ := holder.send(ctx, nil); out != "sent" {
		t.Fatal("send_message was not kept for a fork's task")
	}
	if target := codeChatTarget(project, project.Self, binding.WorkspacePath); target.CrewProfile != "work" {
		t.Fatalf("a Crew tab is addressed as %q", target.CrewProfile)
	}
	other := internalBotRequestContext(t.Context(), "other")
	if _, err := env.api.codeChatsFor(other, "other", conversation.SessionID, binding.WorkspacePath); err == nil {
		t.Fatal("another person's turn reached the owner's Crew tabs")
	}
}
