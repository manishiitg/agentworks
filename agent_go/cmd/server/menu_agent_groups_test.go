package server

import (
	"context"
	"encoding/json"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Personal agent grouping is single-level and single-membership per project;
// similarly named groups and conversation keys in other projects are independent.
func TestAgentGroupsKeepOneMembershipPerProjectAndPreserveMenu(t *testing.T) {
	saved := menuGroups{
		Groups: []menuGroup{{ID: "crews", Name: "Projects", Projects: []string{"crew"}}},
		Filter: "group:crews", Network: "network",
		AgentGroups: []menuAgentGroup{
			{ID: "first", Project: "one", Name: " Work ", Agents: []string{"a", "a"}, Collapsed: true},
			{ID: "second", Project: "one", Name: "Other", Agents: []string{"a", "b"}},
			{ID: "third", Project: "two", Name: "Work", Agents: []string{"a"}},
			{ID: "duplicate", Project: "one", Name: "work"},
		},
	}
	got := saved.normalized()
	if len(got.AgentGroups) != 3 || len(got.AgentGroups[0].Agents) != 1 || len(got.AgentGroups[1].Agents) != 1 || got.AgentGroups[1].Agents[0] != "b" || got.AgentGroups[2].Agents[0] != "a" {
		t.Fatalf("wrong membership normalization: %+v", got.AgentGroups)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var restored menuGroups
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if !restored.AgentGroups[0].Collapsed || restored.AgentGroups[0].ID != "first" || len(restored.Groups) != 1 || restored.Filter != saved.Filter || restored.Network != saved.Network {
		t.Fatal("agent grouping lost existing menu preferences or saved state")
	}
}

// PLAT-746-983: forks keep personal membership without grouping unrelated
// projects/people; both the sessions list and directory show the saved names.
func TestForkGroupInheritanceAndToolListsStayPersonal(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	root := crewOwnerTabsFixture(t, env, "other")
	profile, err := env.svc.registry.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, person := range []string{"owner", "other"} {
		ctx := internalBotRequestContext(t.Context(), person)
		binding, _, err := resolveConversationBindingForUser(ctx, person, profile, "beta:chat:"+person)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, person, profile, binding, "Agent"); err != nil {
			t.Fatal(err)
		}
	}
	ctx := internalBotRequestContext(t.Context(), "owner")
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	chats := env.api.crewChats(req, "owner", "beta", root)
	var parent, foreign, session string
	for _, chat := range chats {
		if chat.OwnerID == "owner" {
			parent = chat.key
			session = chat.SessionID
		} else {
			foreign = chat.key
		}
	}
	if parent == "" || foreign == "" {
		t.Fatal("missing real owner/foreign chat fixture")
	}
	project, _ := json.Marshal([]string{"local", "beta"})
	otherProject, _ := json.Marshal([]string{"local", "elsewhere"})
	saved, err := updateMenuGroups(ctx, func(saved *menuGroups) error {
		saved.Filter = "mine"
		saved.AgentGroups = []menuAgentGroup{
			{ID: "own", Name: "Design", Project: string(project), Agents: []string{parent}, Collapsed: true},
			{ID: "wrong", Name: "Other project", Project: string(otherProject), Agents: []string{parent}},
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fork := "beta:chat:fork"
	saved, err = updateMenuGroups(ctx, func(m *menuGroups) error { m.inheritAgentGroup("beta", parent, fork); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.agentGroupNames("beta", fork); len(got) != 1 || got[0] != "Design" {
		t.Fatalf("fork groups %v", got)
	}
	if len(saved.AgentGroups[1].Agents) != 1 || !saved.AgentGroups[0].Collapsed || saved.Filter != "mine" {
		t.Fatal("changed unrelated preferences")
	}
	saved.inheritAgentGroup("beta", "not-grouped", "beta:chat:plain")
	if len(saved.agentGroupNames("beta", "beta:chat:plain")) != 0 {
		t.Fatal("invented a group")
	}
	// The real sessions/tool list reads the current caller's preferences only.
	for _, person := range []string{"owner", "other"} {
		request := req.WithContext(internalBotRequestContext(t.Context(), person))
		for _, chat := range env.api.crewChats(request, person, "beta", root) {
			want := person == "owner" && chat.key == parent
			if (len(chat.GroupNames) > 0) != want {
				t.Fatalf("group leak/missing group for %s: %+v", person, chat)
			}
		}
	}
	native, err := env.api.codeChatsFor(ctx, "owner", session, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, chat := range native.Chats {
		if (len(chat.GroupNames) > 0) != (chat.Key == parent && chat.OwnerID == "owner") {
			t.Fatalf("native list group boundary: %+v", chat)
		}
	}
	own := map[string]interface{}{"chat_id": parent, "yours": true}
	peer := map[string]interface{}{"chat_id": foreign, "yours": false}
	remote := map[string]interface{}{"chat_id": parent, "yours": false}
	prefs := productConversationRegistryDocument{}
	annotateDirectoryAgentGroups([]map[string]interface{}{{"chats": []map[string]interface{}{own, peer}}, remote}, saved, prefs, "")
	if len(own["group_names"].([]string)) != 1 || peer["group_names"] != nil || remote["group_names"] != nil {
		t.Fatal("directory grouping crossed person boundary")
	}
}

// Real workspace handlers (not the in-memory workspace seam) prove a
// project-bound fork can update the caller's preferences without widening guards.
func TestProjectBoundForkInheritsGroupThroughWorkspaceGuard(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "claude-code")
	f, _ := newExternalCrewCommandFixture(t)
	journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	f.api.eventStore = storeevents.NewEventStore(100)
	defer f.api.eventStore.Stop()
	f.api.eventStore.SetDurableJournal(journal)
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner"})
	profile, err := f.api.agentProfiles.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	binding, _, err := resolveConversationBindingForUser(ctx, "owner", profile, "team:chat:source")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, "owner", profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}

	// The source transcript is in the Crew's private runtime storage, not the
	// public docs root used by f.write.
	private, ok := resolveLocalWorkflowDir(parent.WorkspacePath)
	if !ok {
		t.Fatal("missing private history root")
	}
	historyFile := filepath.Join(private, "builder", "conversation", time.Now().Format("2006-01-02"), chatHistoryConversationFileName(parent.SessionID))
	if err := os.MkdirAll(filepath.Dir(historyFile), 0700); err != nil {
		t.Fatal(err)
	}
	history := `{"session_id":"` + parent.SessionID + `","conversation_history":[{"Role":"human","Parts":[{"Text":"Review this design"}]},{"Role":"ai","Parts":[{"Text":"Reviewed"}]}]}`
	if err := os.WriteFile(historyFile, []byte(history), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadChatHistoryConversation("owner", parent.SessionID, parent.WorkspacePath); err != nil {
		t.Fatalf("source fixture unreadable: %v", err)
	}

	project, _ := json.Marshal([]string{"local", "team"})
	if _, err := updateMenuGroups(ctx, func(saved *menuGroups) error {
		saved.AgentGroups = []menuAgentGroup{{ID: "design", Name: "Design", Project: string(project), Agents: []string{parent.ConversationKey}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Same HTTP handler as UI and sessions fork; the token is bound to this Crew.
	claims := crewWriter("owner")
	claims.AccessToken.CrewIDs = []string{"team"}
	claims.AccessToken.AllCrews = false
	tokenCtx := context.WithValue(t.Context(), UserContextKey, claims)
	request := httptest.NewRequest("POST", "/", nil).WithContext(tokenCtx)
	status, body := scheduleHandler(request, f.api.handleForkCrewChat, "POST", "/api/agent-profiles/work/conversation/fork", map[string]string{"id": "work"}, nil, map[string]any{"conversation_key": parent.ConversationKey})
	if status != 200 {
		t.Fatalf("fork: %d %s", status, body)
	}
	var fork struct {
		ConversationKey string   `json:"conversation_key"`
		GroupNames      []string `json:"group_names"`
	}
	if err := json.Unmarshal(body, &fork); err != nil {
		t.Fatal(err)
	}
	if len(fork.GroupNames) != 1 || fork.GroupNames[0] != "Design" {
		t.Fatalf("fork membership %+v", fork)
	}
	saved, err := updateMenuGroups(ctx, nil)
	if err != nil || len(saved.agentGroupNames("team", fork.ConversationKey)) != 1 {
		t.Fatalf("saved fork membership: %v %+v", err, saved)
	}
	if strings.Contains(string(body), "menu_groups") {
		t.Fatal("scoped fork disclosed unrelated personal menu")
	}
}
