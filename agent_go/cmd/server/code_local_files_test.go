package server

import "testing"

func TestCodeLocalFilesRequireInteractiveCodeAndReplaceRetainedToolBinding(t *testing.T) {
	profile := routeTestProfile("code", true, "")
	target := &codeLocalFileTarget{DeviceID: "laptop", ResourceID: "project"}
	conversation := ProductConversationRecord{SessionID: "session", WorkspacePath: "Chats/Code/projects/project"}
	query, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "read my code", CodeLocalFiles: target}, conversation)
	if err != nil || query.CodeLocalFiles != target {
		t.Fatalf("Code selection lost: %+v %v", query, err)
	}
	resolved := &resolvedAgentProfile{Definition: profile}
	if !codeLocalFileTurn(query, resolved) {
		t.Fatal("interactive Code denied")
	}
	serverKey := agentProfileSessionKey(resolved)
	resolved.CodeLocalFiles = target
	localKey := agentProfileSessionKey(resolved)
	if serverKey == localKey {
		t.Fatal("retained server tools reused for local folder")
	}
	resolved.CodeLocalFiles = &codeLocalFileTarget{DeviceID: "laptop", ResourceID: "different-project"}
	if localKey == agentProfileSessionKey(resolved) {
		t.Fatal("retained folder binding was reused")
	}
	beforePolicy := agentProfileSessionKey(resolved)
	resolved.CodeLocalFilePolicy = "read-only grant"
	if beforePolicy == agentProfileSessionKey(resolved) {
		t.Fatal("changed local permissions reused retained tools")
	}
	for _, kind := range []string{"cron", "webhook", "chat_tool"} {
		query.TriggeredBy = kind
		if codeLocalFileTurn(query, resolved) {
			t.Fatalf("automated %s acquired local tools", kind)
		}
	}
	query.TriggeredBy = "interactive"
	if !codeLocalFileTurn(query, resolved) {
		t.Fatal("website continuation denied")
	}
	query.BotPlatform = "slack"
	if codeLocalFileTurn(query, resolved) {
		t.Fatal("connector acquired local tools")
	}
	for _, id := range []string{"work", "knowledgebase", "agentworks"} {
		profile.ID = id
		if _, err = queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "read", CodeLocalFiles: target}, conversation); err == nil {
			t.Fatalf("%s accepted local Code hint", id)
		}
	}
	profile.ID = "code"
	if _, err = queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "read", CodeLocalFiles: &codeLocalFileTarget{DeviceID: "../escape", ResourceID: "project"}}, conversation); err == nil {
		t.Fatal("invalid device accepted")
	}
}
