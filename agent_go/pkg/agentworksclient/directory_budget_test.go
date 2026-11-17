package agentworksclient

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBridgeDirectoriesShareOneOutputBudget(t *testing.T) {
	rows := []map[string]string{}
	address := "network:connection:agent:" + strings.Repeat("long-name", 20)
	for i := 0; i < 20; i++ {
		rows = append(rows, map[string]string{"target": address, "purpose": strings.Repeat("x", 800)})
	}
	page, _ := json.Marshal(map[string]any{"agents": rows, "matched": 20})
	reply := LocalBridgeReply{Result: page, Directories: []LocalAgentDirectory{{Server: "https://example.com", Agents: page}, {Server: "https://other.example.com", Agents: page}}}
	boundBridgeDirectoryReply(&reply)
	raw, err := json.Marshal(reply)
	if err != nil || len(raw) >= 20_000 || reply.Warning == "" {
		t.Fatalf("unbounded combined listing: %d %v", len(raw), err)
	}
	var result struct {
		Agents []map[string]string `json:"agents"`
	}
	if json.Unmarshal(reply.Result, &result) != nil || len(result.Agents) == 0 {
		t.Fatal("lost primary contacts")
	}
	for _, row := range result.Agents {
		if row["target"] != address {
			t.Fatal("address was clipped")
		}
	}
}
