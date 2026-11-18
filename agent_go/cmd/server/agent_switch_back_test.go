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
// when the chat switches back, and the "since you left" point must still be
// found when only the chat's tail is loaded.
func TestSwitchBackResumesTheAgentsOwnSession(t *testing.T) {
	text := func(role llmtypes.ChatMessageType, s string) llmtypes.MessageContent {
		return llmtypes.MessageContent{Role: role, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: s}}}
	}
	claude := &ChatHistoryAgentRuntime{Kind: "coding_agent", Provider: "claude-code", ExternalSessionID: "claude-native", CapturedAt: "2026-11-18T10:00:00Z"}
	history := []llmtypes.MessageContent{text(llmtypes.ChatMessageTypeHuman, "fix login"), text(llmtypes.ChatMessageTypeAI, "claude fixed it")}
	claude.SeenMessages, claude.SeenLastKey = len(history), continuityMessageKey(history)

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

	// Codex worked; only the tail is loaded later. The recap covers just Codex's part.
	history = append(history, text(llmtypes.ChatMessageTypeHuman, "now add tests"), text(llmtypes.ChatMessageTypeAI, "codex added tests"))
	since, found := historySince(history[1:], picked.SeenMessages, picked.SeenLastKey)
	if !found || len(since) != 2 || !strings.Contains(continuityMessageKey(since), "codex added tests") {
		t.Fatalf("since-you-left point lost: found=%t since=%d", found, len(since))
	}
}
