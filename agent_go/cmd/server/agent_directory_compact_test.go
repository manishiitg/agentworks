package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// #2980: a 110-agent listing exceeded the tool budget and led to miscopied
// addresses. Check the actual JSON, not just the row count; queries must still
// find a Team or exact address beyond the default first page.
func TestAgentDirectoryToolOutputIsBoundedAndSearchable(t *testing.T) {
	_ = newTriggerLinkEnv(t)
	rows := []map[string]any{}
	for i := 0; i < 110; i++ {
		rows = append(rows, map[string]any{"kind": "team_agent", "agent_id": fmt.Sprint(i), "name": fmt.Sprintf("Helper %d", i), "target": fmt.Sprintf("network:connection:agent:peer/helper-%d", i), "team_name": fmt.Sprintf("Team %d", i), "purpose": strings.Repeat("<long description>", 1000), "examples": []string{strings.Repeat("x", 5000)}, "chats": []string{strings.Repeat("nested", 5000)}})
	}
	original := map[string]any{"agents": rows, "chats_inside_agentworks": rows, "note": strings.Repeat("note", 10000)}
	for _, query := range []string{"", "Team 109", "network:connection:agent:peer/helper-109", "Helper"} {
		out := compactAgentDirectory(t.Context(), "owner", original, query)
		raw, err := json.Marshal(out)
		if err != nil || len(raw) >= agentDirectoryMaxBytes {
			t.Fatalf("query=%q bytes=%d err=%v", query, len(raw), err)
		}
		got := out["agents"].([]map[string]any)
		if len(got) > 20 || len(got) == 0 {
			t.Fatalf("query=%q rows=%d", query, len(got))
		}
		for _, row := range got {
			if row["examples"] != nil || row["chats"] != nil {
				t.Fatal("nested inventory leaked into tool output")
			}
		}
		if strings.Contains(query, "109") && (len(got) != 1 || got[0]["target"] != rows[109]["target"]) {
			t.Fatalf("lost exact target beyond default list: %v", got)
		}
	}
	// Oversized addresses are omitted intact, never turned into plausible but
	// wrong targets by substring truncation. Hostile labels cannot bust the cap.
	rows[0]["target"] = strings.Repeat("a", 30_000)
	out := compactAgentDirectory(t.Context(), "owner", original, "")
	raw, _ := json.Marshal(out)
	if len(raw) >= agentDirectoryMaxBytes {
		t.Fatalf("oversized response: %d", len(raw))
	}
	for _, row := range out["agents"].([]map[string]any) {
		if row["target"] == rows[0]["target"] {
			t.Fatal("oversized address included")
		}
	}
	if len(original["agents"].([]map[string]any)) != 110 {
		t.Fatal("compaction changed resolution inventory")
	}
}

func TestAgentDirectoryRanksOnlyCallersContacts(t *testing.T) {
	store := agentMessageStore{Conversations: []agentConversation{
		{Endpoints: [2]agentMessageEndpoint{{UserID: "owner"}, {UserID: "peer", NamedAgentID: "frequent"}}, Messages: []agentMessage{{From: 0}, {From: 0}}},
		{Endpoints: [2]agentMessageEndpoint{{UserID: "other"}, {UserID: "peer", NamedAgentID: "private"}}, Messages: []agentMessage{{From: 0}}},
	}}
	counts := agentDirectoryContactFrequency(store, "owner")
	if counts["frequent"] != 2 || counts["private"] != 0 {
		t.Fatalf("ranking used another person's traffic: %v", counts)
	}
}
