package server

import "testing"

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
