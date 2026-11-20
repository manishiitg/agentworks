package server

import "testing"

// #2977: Team projections arrive both directly and through the named directory.
func TestAgentDirectoryListsEachAgentOnceBeforeLabelling(t *testing.T) {
	rows := []map[string]interface{}{
		{"kind": "team_agent", "agent_id": "team-one", "agent_name": "agent:peer/one", "target": "agent:peer/one", "name": "Main", "label": "Main", "team_name": "Design"},
		{"kind": "team_agent", "agent_id": "team-one", "agent_name": "agent:peer/one", "target": "agent:peer/one", "name": "Main", "label": "Main (agent:peer/one)", "team_name": "Design"},
		{"kind": "team_agent", "agent_id": "team-two", "agent_name": "agent:peer/two", "target": "agent:peer/two", "name": "Main", "label": "Main", "team_name": "Research"},
		{"agent_id": "chat-one", "agent_name": "main-2", "target": "agent:main-2", "label": "main-2"},
		{"agent_id": "chat-two", "agent_name": "main-3", "target": "agent:main-3", "label": "main-3"},
	}
	// Exercise the two-pass merge shape, including a local alias of one ID.
	nested := uniqueAgentDirectoryItems(rows)
	projectAgentLabels(nested)
	rows = append(nested, rows[1], map[string]interface{}{"agent_id": "chat-one", "agent_name": "peer/main-2", "target": "agent:peer/main-2"})
	rows = uniqueAgentDirectoryItems(rows)
	projectAgentLabels(rows)
	if len(rows) != 4 {
		t.Fatalf("duplicate rows or distinct chats lost: %v", rows)
	}
	if rows[0]["label"] != "Main · Design" || rows[1]["label"] != "Main · Research" {
		t.Fatalf("unhelpful collision labels: %v", rows)
	}
	// Identical registered IDs on different networks are different destinations.
	networks := uniqueAgentDirectoryItems([]map[string]interface{}{
		{"agent_id": "same", "target": "network:first:agent:peer"},
		{"agent_id": "same", "target": "network:second:agent:peer"},
	})
	if len(networks) != 2 {
		t.Fatal("collapsed independent network destinations")
	}
	// #3364: the same chat on two networks once read "ai-work (app-a) (app-a)".
	twice := []map[string]interface{}{
		{"agent_id": "app-a", "agent_name": "app-a", "target": "network:first:agent:app-a", "label": "ai-work (app-a)"},
		{"agent_id": "app-a", "agent_name": "app-a", "target": "network:second:agent:app-a", "label": "ai-work (app-a)"},
	}
	projectAgentLabels(twice)
	if twice[0]["label"] != "ai-work (app-a)" {
		t.Fatalf("address added to a label twice: %v", twice[0]["label"])
	}
}
