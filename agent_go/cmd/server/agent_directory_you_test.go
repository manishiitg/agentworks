package server

import (
	"strings"
	"testing"
)

// #3666: your own agents had no owner, so an agent counted only the named owners as people and missed its own
// person's agents. Your rows now say "you", the reply names who "you" is, and teammates' owners are unchanged.
func TestAgentDirectoryNamesYourOwnAgents(t *testing.T) {
	_ = newTriggerLinkEnv(t)
	rows := []map[string]any{
		{"kind": "connected_agent", "name": "Issue lead", "target": "agent:user/chat-1", "yours": true},
		{"kind": "team_agent", "name": "perf", "target": "network:c:agent:Owner/team-agent-1", "owner": "Owner P."},
		{"kind": "crew", "name": "ai-work", "id": "workspace:work:1", "owner": "you"},
	}
	out := compactAgentDirectory(t.Context(), "owner", map[string]any{"agents": rows}, "", 0)
	you, _ := out["you"].(string)
	if !strings.HasPrefix(you, "you") {
		t.Fatalf("you=%q", you)
	}
	if hint, _ := out["hint"].(string); !strings.Contains(hint, "yours=true belong to your own person") {
		t.Fatalf("hint=%q", hint)
	}
	owners := map[string]string{}
	for _, row := range out["agents"].([]map[string]any) {
		owners[stringField(row["name"])] = stringField(row["owner"])
	}
	if owners["Issue lead"] != you || owners["perf"] != "Owner P." || owners["ai-work"] != "you" {
		t.Fatalf("owners=%v", owners)
	}
	if rows[0]["owner"] != nil {
		t.Fatal("compaction changed the resolution inventory")
	}
}
