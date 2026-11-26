package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// Project groups are gone (owner, 22 Nov): a saved file from before keeps its network, its group filter becomes
// All, and the old groups and agent groups are never written back.
func TestMenuFilterDropsRetiredGroups(t *testing.T) {
	var saved menuFilter
	if err := json.Unmarshal([]byte(`{"agent_groups":[{"id":"old"}],"groups":[{"id":"projects","name":"Projects","projects":["crew"]}],"filter":"group:projects","network":"local"}`), &saved); err != nil {
		t.Fatal(err)
	}
	saved = saved.normalized()
	data, _ := json.Marshal(saved)
	if strings.Contains(string(data), "groups") || saved.Filter != "" || saved.Network != "local" {
		t.Fatalf("old groups survived or network lost: %s", data)
	}
	if (menuFilter{Filter: "shared"}).normalized().Filter != "shared" {
		t.Fatal("shared filter not kept")
	}
}
