package server

import (
	"testing"

	llmtypes "github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func histEntry(role string) map[string]interface{} { return map[string]interface{}{"role": role} }
func msg(role string) llmtypes.MessageContent {
	return llmtypes.MessageContent{Role: llmtypes.ChatMessageType(role)}
}

// TestUserTurnsAreTheInvariantWorthGuarding.
//
// Assistant and tool entries are legitimately rewritten, summarized and trimmed
// by normal persistence, so a drop in their count proves nothing. User turns
// only ever grow — a session cannot un-ask a question — which is what makes a
// shrink detectable as a partial rebuild rather than ordinary cleaning.
func TestUserTurnsAreTheInvariantWorthGuarding(t *testing.T) {
	onDisk := []map[string]interface{}{
		histEntry("system"), histEntry("human"), histEntry("ai"), histEntry("tool"),
		histEntry("ai"), histEntry("human"), histEntry("ai"), histEntry("tool"), histEntry("tool"),
	}
	if got := conversationUserTurnCount(onDisk); got != 2 {
		t.Fatalf("user turns on disk = %d, want 2", got)
	}
	// "user" and "human" are both used across the codebase's history shapes.
	if got := conversationUserTurnCount([]map[string]interface{}{histEntry("user"), histEntry("HUMAN")}); got != 2 {
		t.Errorf("role spellings not both counted: %d", got)
	}

	incoming := []llmtypes.MessageContent{msg("system"), msg("human"), msg("ai")}
	if got := messageUserTurnCount(incoming); got != 1 {
		t.Fatalf("user turns incoming = %d, want 1", got)
	}
}
