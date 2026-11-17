package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// #2952: old personal agent groups are ignored and never serialized back;
// independent project-group choices and their ordering survive.
func TestMenuPreferencesDropRetiredAgentGroups(t *testing.T) {
	var saved menuGroups
	if err := json.Unmarshal([]byte(`{"agent_groups":[{"id":"old","name":"Old agents","project":"crew","agents":["chat"]}],"groups":[{"id":"projects","name":"Projects","projects":["crew"]}],"filter":"group:projects","network":"local"}`), &saved); err != nil {
		t.Fatal(err)
	}
	saved = saved.normalized()
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "agent_groups") || len(saved.Groups) != 1 || saved.Groups[0].Projects[0] != "crew" || saved.Filter != "group:projects" || saved.Network != "local" {
		t.Fatalf("changed independent project choices: %s", data)
	}
}
