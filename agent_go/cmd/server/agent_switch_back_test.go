package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3140: switching a chat Claude -> Codex -> Claude started Claude blank,
// because the chat kept only the latest agent's session. Each earlier agent's
// session must survive saves (including a save/load round trip), be picked
// when the chat switches back, and the "since you left" point is the saved
// line it last saw (a text match lost it and fell back to a full recap).
func TestSwitchBackResumesTheAgentsOwnSession(t *testing.T) {
	text := func(role llmtypes.ChatMessageType, s string) llmtypes.MessageContent {
		return llmtypes.MessageContent{Role: role, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: s}}}
	}
	claude := &ChatHistoryAgentRuntime{Kind: "coding_agent", Provider: "claude-code", ExternalSessionID: "claude-native", CapturedAt: "2026-11-18T10:00:00Z"}
	history := []llmtypes.MessageContent{text(llmtypes.ChatMessageTypeSystem, "system prompt"), text(llmtypes.ChatMessageTypeHuman, "fix login"), text(llmtypes.ChatMessageTypeAI, "claude fixed it")}
	claude.SeenLine = continuitySavedLines(history) // 2: the system prompt is not a saved line

	codex := &ChatHistoryAgentRuntime{Kind: "coding_agent", Provider: "codex-cli", ExternalSessionID: "codex-native", CapturedAt: "2026-11-18T10:05:00Z"}
	carryOtherAgentSessions(claude, codex)
	// Saves and loads must keep the earlier session.
	raw, err := json.Marshal(codex)
	if err != nil {
		t.Fatal(err)
	}
	var loaded ChatHistoryAgentRuntime
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	next := &ChatHistoryAgentRuntime{Kind: "coding_agent", Provider: "codex-cli", ExternalSessionID: "codex-native"}
	carryOtherAgentSessions(&loaded, next) // a later Codex turn keeps it too

	picked := pickSavedRuntime(next, "claude-code")
	if picked.ExternalSessionID != "claude-native" || picked.switchedBackFrom != "codex-cli" || picked.OtherAgentSessions != nil {
		t.Fatalf("switch back did not pick Claude's own session: %+v", picked)
	}
	if same := pickSavedRuntime(next, "codex-cli"); same.ExternalSessionID != "codex-native" || same.switchedBackFrom != "" {
		t.Fatalf("staying on Codex must keep its session: %+v", same)
	}
	if none := pickSavedRuntime(next, "cursor-cli"); none.ExternalSessionID != "codex-native" || none.switchedBackFrom != "" {
		t.Fatalf("an agent never used has no session to resume: %+v", none)
	}

	// Codex worked, repeating Claude's last words. The recap covers just Codex's part, from line 3.
	history = append(history, text(llmtypes.ChatMessageTypeHuman, "now add tests"), text(llmtypes.ChatMessageTypeAI, "claude fixed it"))
	start, found := continuityAfterLine(history, picked.SeenLine)
	selected, _, _, _ := continuityHandoff(history, start, continuityHandoffBytes)
	if !found || start != 3 || len(selected) != 2 || selected[0].Line != 3 || !strings.Contains(selected[0].Text, "now add tests") {
		t.Fatalf("since-you-left point lost: found=%t start=%d selected=%+v", found, start, selected)
	}
	if _, ok := continuityAfterLine(history, 9); ok {
		t.Fatal("a line past the end must fall back to the full handoff")
	}
}
