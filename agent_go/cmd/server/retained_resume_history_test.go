package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-876: successive parsed resume snapshots must not be held by the
// last-turn or idle-compaction request in addition to the chat's loaded copy.
func TestRetainedTurnRequestsDoNotPinParsedHistory(t *testing.T) {
	api := &StreamingAPI{lastQueryRequests: map[string]QueryRequest{}, conversationHistory: map[string][]llmtypes.MessageContent{}}
	tracker := &idleCompactTracker{chats: map[string]*idleCompactChat{}}
	for chat := 0; chat < 8; chat++ {
		session := fmt.Sprintf("chat-%d", chat)
		for turn := 0; turn < 5; turn++ {
			history := make([]llmtypes.MessageContent, 100)
			for i := range history {
				history[i] = llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: strings.Repeat("message ", 512)}}}
			}
			target := &resolvedResumeTarget{SessionID: session, ConversationPath: "conversation.json", History: history, Runtime: &ChatHistoryAgentRuntime{Provider: "claude-code"}}
			req := QueryRequest{Provider: "claude-code", ModelID: "example-model", resolvedResumeTarget: target}
			api.restoreResolvedResumeTarget(session, target, "")
			api.rememberLastQueryRequest(session, req)
			tracker.notePersonTurn(session, "owner", req, time.Now())
			for _, stored := range []QueryRequest{api.lastQueryRequests[session], tracker.chats[session].personReq} {
				if stored.resolvedResumeTarget == nil || stored.resolvedResumeTarget.History != nil {
					t.Fatal("retained config still pins parsed history")
				}
				if stored.Provider != req.Provider || stored.ModelID != req.ModelID || stored.resolvedResumeTarget.provider() != target.provider() || !stored.resolvedResumeTarget.canPersist("") {
					t.Fatal("resume configuration lost")
				}
				// Follow-up hydration with config-only metadata keeps the canonical copy.
				api.restoreResolvedResumeTarget(session, stored.resolvedResumeTarget, "")
			}
			if len(req.resolvedResumeTarget.History) != 100 {
				t.Fatal("active turn history was mutated")
			}
			if len(api.conversationHistory[session]) != 100 {
				t.Fatal("canonical conversation copy lost")
			}
		}
	}
}
