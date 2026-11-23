// Package retainedturn recovers structured output for messages delivered
// directly to an already-running coding CLI.
package retainedturn

import (
	"strings"
	"time"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// FinalResponse returns the authoritative final assistant text for a retained
// coding-CLI turn. It reads only the provider's structured sidecar transcript
// and never starts, resumes, configures, or sends input to an agent session.
func FinalResponse(provider llmproviders.Provider, ownerSessionID string, turnStart time.Time) string {
	messages := llmproviders.ReadCodingAgentRetainedTurnMessages(provider, ownerSessionID, turnStart)
	return finalResponse(messages)
}

func FinalResponseWithMetadata(provider llmproviders.Provider, ownerSessionID string, turnStart time.Time) (string, string, []string) {
	response := llmproviders.ReadCodingAgentRetainedTurnResponse(provider, ownerSessionID, turnStart)
	return finalResponse(response.Messages), response.NativeSessionID, response.NativeContinuationRoots
}

// finalResponse picks the last AI-role message that is PURELY text -- no
// ToolCall part anywhere in the same message -- and returns its text.
//
// PLAT-179. Every provider's transcript can legitimately bundle intermediate
// commentary and the tool call it introduces into ONE assistant message
// (confirmed live for pi-cli -- {"role":"assistant","content":[{"type":"text",
// "text":"<progress update>"},{"type":"toolCall",...}]} -- and true by
// construction for claude-code and cursor-cli too, which group a single LLM
// call's blocks into one MessageContent the same way). The previous version
// here treated ANY non-empty text on the last AI message as final, so a
// message that also carried a pending tool call was indistinguishable from a
// genuinely finished reply -- confirmed live: a prompt asking for a progress
// update before a tool call had that progress update declared the final
// result, 2.2s in, before the tool's own `sleep 2` could finish.
//
// A first attempt at fixing this gated on the coding CLI's own tmux pane
// text instead (piPaneReadyForInput-style, picli/cursorcli side). That
// approach failed live for a different reason: the pane genuinely had no
// "idle" text in its status line for the pi CLI build under test, so the gate
// never fired and the retained turn hung until the caller's own timeout --
// worse than the original bug, which at least returned promptly (with the
// wrong text). This transcript-level check needs no pane inspection at all
// and is exactly the signal claude-code's (unwired) completedAssistantResponseFromTranscript
// and codex-cli's phase=="final_answer" filter already use, just applied
// once here instead of duplicated per provider.
func finalResponse(messages []llmtypes.MessageContent) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llmtypes.ChatMessageTypeHuman {
			return "" // A newer user query has not received an assistant reply yet.
		}
		if messages[i].Role != llmtypes.ChatMessageTypeAI {
			continue
		}
		// Only the newest assistant message can describe the current terminal
		// state. If it contains a tool call, the turn is still in progress; do
		// not scan backward and misclassify an older progress update as final.
		if messageHasToolCall(messages[i]) {
			return ""
		}
		parts := make([]string, 0, len(messages[i].Parts))
		for _, part := range messages[i].Parts {
			switch text := part.(type) {
			case llmtypes.TextContent:
				if value := strings.TrimSpace(text.Text); value != "" {
					parts = append(parts, value)
				}
			case *llmtypes.TextContent:
				if text != nil {
					if value := strings.TrimSpace(text.Text); value != "" {
						parts = append(parts, value)
					}
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

func messageHasToolCall(message llmtypes.MessageContent) bool {
	for _, part := range message.Parts {
		switch part.(type) {
		case llmtypes.ToolCall, *llmtypes.ToolCall:
			return true
		}
	}
	return false
}
