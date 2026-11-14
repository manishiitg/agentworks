package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
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

// PLAT-746-982: forks keep personal membership without grouping unrelated
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
		if err := ensureCrewOwnerDefaultTab(ctx, person, profile, "beta"); err != nil {
			t.Fatal(err)
		}
	}
	ctx := internalBotRequestContext(t.Context(), "owner")
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	chats := env.api.crewChats(req, "owner", "beta", root)
	var parent, foreign string
	for _, chat := range chats {
		if chat.OwnerID == "owner" {
			parent = chat.key
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
	own := map[string]interface{}{"chat_id": parent, "yours": true}
	peer := map[string]interface{}{"chat_id": foreign, "yours": false}
	remote := map[string]interface{}{"chat_id": parent, "yours": false}
	prefs := productConversationRegistryDocument{}
	annotateDirectoryAgentGroups([]map[string]interface{}{{"chats": []map[string]interface{}{own, peer}}, remote}, saved, prefs, "")
	if len(own["group_names"].([]string)) != 1 || peer["group_names"] != nil || remote["group_names"] != nil {
		t.Fatal("directory grouping crossed person boundary")
	}
}
