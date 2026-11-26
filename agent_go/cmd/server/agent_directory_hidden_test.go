package server

import "testing"

// PLAT-746: hidden is a personal preference, not a removal or a shared-owner policy.
func TestDirectoryHiddenAgentsStayReachableAndPersonal(t *testing.T) {
	key := "example:chat:one"
	preferences := productConversationRegistryDocument{
		Agents:  map[string]ProductAgentIdentity{"work/" + key: {Unpinned: true}},
		Entries: map[string]ProductConversationRecord{"work/" + key: {ConversationID: "conversation"}},
	}
	hidden := map[string]interface{}{"chat_id": key, "can_write": true}
	visible := map[string]interface{}{"chat_id": "example:chat:two"}
	own := map[string]interface{}{"agent_name": desktopAppAgentName("device", "conversation"), "yours": true, "target": "same-target"}
	other := map[string]interface{}{"agent_name": desktopAppAgentName("device", "conversation"), "yours": false}
	rows := []map[string]interface{}{{"chats": []map[string]interface{}{hidden, visible}}, own, other}
	annotateDirectoryHiddenAgents(rows, preferences, "device")
	if hidden["hidden"] != true || own["hidden"] != true || hidden["can_write"] != true || own["target"] != "same-target" {
		t.Fatal("hidden agent lost its label or reach")
	}
	if visible["hidden"] != nil || other["hidden"] != nil {
		t.Fatal("hidden preference leaked to another agent/person")
	}
}
