package server

import (
	"encoding/json"
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
	if err != nil { t.Fatal(err) }
	var restored menuGroups
	if err := json.Unmarshal(raw, &restored); err != nil { t.Fatal(err) }
	if !restored.AgentGroups[0].Collapsed || restored.AgentGroups[0].ID != "first" || len(restored.Groups) != 1 || restored.Filter != saved.Filter || restored.Network != saved.Network {
		t.Fatal("agent grouping lost existing menu preferences or saved state")
	}
}
