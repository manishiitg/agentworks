package server

import (
	"strings"
	"testing"
)

// Status selection cannot discover an unlisted target, choose an ambiguous
// display name or copy private runtime payloads into a public status reply.
func TestAgentStatusSelectionKeepsDirectoryBoundaryAndPrivatePayloads(t *testing.T) {
	directory := map[string]interface{}{"agents": []map[string]interface{}{
		{"id": "agent:one", "label": "Same name", "activity": "Working", "owner_status": map[string]interface{}{"last_messages": []string{"private text"}}},
		{"id": "agent:two", "label": "Same name", "activity": "Idle"},
	}}
	for _, target := range []string{"agent:missing", "Same name", ""} {
		if _, err := agentStatusRow(directory, target); err == nil {
			t.Fatalf("unavailable/ambiguous status admitted: %q", target)
		}
	}
	row, err := agentStatusRow(directory, "agent:one")
	if err != nil {
		t.Fatal(err)
	}
	if row["activity"] != "Working" {
		t.Fatal(row)
	}
	for _, field := range []string{"owner_status", "last_messages", "current_task"} {
		if _, present := row[field]; present {
			t.Fatalf("private payload leaked: %s", field)
		}
	}
	public := map[string]interface{}{"agents": []map[string]interface{}{{"id": "agent:one", "published": true, "group_names": []string{"internal"}, "crew_name": "internal", "last_messages": []string{"private"}, "activity": "Working"}}}
	publicAgentDirectoryOnly(public)
	kept := public["agents"].([]map[string]interface{})
	if len(kept) != 1 || kept[0]["activity"] != "Working" {
		t.Fatal(public)
	}
	for _, field := range []string{"group_names", "crew_name", "last_messages"} {
		if _, present := kept[0][field]; present {
			t.Fatalf("restricted turn leaked %s", field)
		}
	}
}

// #2881: name convenience must select only an already-reachable Bridge row,
// not restore product/name routing or choose between identically named agents.
func TestBridgeMessageNameSelectionUsesReachableAgentAddresses(t *testing.T) {
	directory := map[string]interface{}{"agents": []map[string]interface{}{
		{"id": "workspace:work:one", "name": "Crew"},
		{"target": "agent:one", "agent_id": "named-one", "name": "Worker"},
		{"target": "agent:owner/one", "agent_id": "named-one", "label": "Worker"},
		{"target": "agent:two", "label": "Review"},
		{"target": "network:peer:agent:review", "label": "Review"},
	}}
	if got, err := bridgeMessageAddress(directory, "worker"); err != nil || got != "agent:owner/one" {
		t.Fatal(got, err)
	}
	for _, name := range []string{"Crew", "private", "Review"} {
		if _, err := bridgeMessageAddress(directory, name); err == nil {
			t.Fatalf("unavailable/ambiguous agent selected: %s", name)
		}
	}
	if _, err := bridgeMessageAddress(directory, "Review"); err == nil || !strings.Contains(err.Error(), "agent:two") || !strings.Contains(err.Error(), "network:peer:agent:review") {
		t.Fatal("missing ambiguity choices", err)
	}
}
