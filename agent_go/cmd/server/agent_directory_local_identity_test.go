package server

import (
	"testing"
)

// #3087: one current local chat, not each registration/network, supplies live
// activity. A foreign same-name registration must remain a separate identity.
func TestOwnChatDirectoryAndAliasStatusUseLiveTurn(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	api := &StreamingAPI{}
	owner, device := t.Name(), "device"
	record := ProductConversationRecord{ProfileID: crewProfileID, ResourceID: "crew", ConversationKey: "chat", ConversationID: "conversation", SessionID: t.Name()}
	entries := map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, record.ConversationKey): record}
	_, err := agentInfoCached(t.Context(), agentInfoCacheKey{api, owner, "directory_source", ""}, func() (agentInfoDirectorySource, error) {
		return agentInfoDirectorySource{Document: productConversationRegistryDocument{Entries: entries}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Annotation hashes the real device just as registration does; the merger's
	// explicit device parameter also pins that the proof is device-specific.
	device = desktopMachineID()
	if device == "" {
		t.Fatal("test device identity unavailable")
	}
	app := desktopAppAgentName(device, record.ConversationID)
	local := "agent:owner/chat-one"
	first, second := "network:first:agent:"+app, "network:second:agent:"+app
	rows := []map[string]any{
		{"id": local, "target": local, "crew_id": "crew", "chat_id": "chat", "yours": true, "online": false, "activity": "Offline"},
		{"id": first, "target": first, "agent_name": app, "provider": "agentworks", "yours": true, "online": true, "activity": "Idle"},
		{"id": second, "target": second, "agent_name": app, "provider": "agentworks", "yours": true, "online": true, "activity": "Unknown"},
		{"id": "network:public:agent:" + app, "target": "network:public:agent:" + app, "agent_name": app, "yours": false, "activity": "Unknown"},
	}
	api.setSessionBusy(record.SessionID, true)
	t.Cleanup(func() { api.setSessionBusy(record.SessionID, false) })
	emptyReach := func() map[string]reachableAgentRuntime { return map[string]reachableAgentRuntime{} }
	api.annotateAgentRuntimeInfoFrom(t.Context(), owner, rows, emptyReach)
	for _, row := range rows[:3] {
		if row["activity"] != "Working" {
			t.Fatalf("live chat misreported: %v", row)
		}
	}
	merged := mergeOwnAppChatRows(rows, entries, device)
	if len(merged) != 2 || merged[1]["yours"] != false {
		t.Fatalf("not one own chat plus separate foreign row: %v", merged)
	}
	directory := map[string]any{"agents": merged}
	for _, address := range []string{local, first, second} {
		status, err := agentStatusRow(directory, address)
		if err != nil || status["activity"] != "Working" || status["activity_source"] != "local_chat" {
			t.Fatalf("alias status differs %s: %v %v", address, status, err)
		}
	}
	filterAgentDirectory(directory, app)
	if len(directory["agents"].([]map[string]any)) != 2 {
		t.Fatal("alias search lost canonical own chat")
	}
	api.setSessionBusy(record.SessionID, false)
	// Reuse cached registry metadata, but activity must immediately be fresh.
	api.annotateAgentRuntimeInfoFrom(t.Context(), owner, merged, emptyReach)
	if merged[0]["activity"] != "Idle" {
		t.Fatal("stale running status after turn ended", merged[0])
	}
}
