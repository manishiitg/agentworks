package retainedturn

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestFinalResponseUsesLastAssistantText(t *testing.T) {
	messages := []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "progress"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeTool, "tool result"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, " final answer "),
	}
	if got := finalResponse(messages); got != "final answer" {
		t.Fatalf("finalResponse()=%q, want final answer", got)
	}
}

// PLAT-179. A single assistant message can legitimately bundle intermediate
// commentary and the tool call it introduces in one message (confirmed live
// for pi-cli: {"role":"assistant","content":[{"type":"text","text":"<progress
// update>"},{"type":"toolCall",...}]}, and true by construction for
// claude-code/cursor-cli too, which group one LLM call's blocks the same
// way). Before this fix, that message's text alone was enough to be
// returned as final -- the pending tool call sitting right next to it in
// Parts was never even looked at.
func TestFinalResponseSkipsAnAIMessageThatAlsoHasAToolCall(t *testing.T) {
	commentaryWithPendingToolCall := llmtypes.MessageContent{
		Role: llmtypes.ChatMessageTypeAI,
		Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{Text: "progress update, not the final answer"},
			llmtypes.ToolCall{ID: "call_1", Type: "function", FunctionCall: &llmtypes.FunctionCall{Name: "execute_shell_command"}},
		},
	}
	messages := []llmtypes.MessageContent{
		commentaryWithPendingToolCall,
		llmtypes.TextPart(llmtypes.ChatMessageTypeTool, "tool result"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "the real final answer"),
	}
	if got := finalResponse(messages); got != "the real final answer" {
		t.Fatalf("finalResponse()=%q, want %q (must skip the commentary+toolCall message)", got, "the real final answer")
	}
}

// If the ONLY AI message so far still has a pending tool call, there is no
// genuine final answer yet -- must return empty, not the commentary text.
func TestFinalResponseReturnsEmptyWhenOnlyMessageHasAPendingToolCall(t *testing.T) {
	messages := []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeAI,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "progress update, not the final answer"},
				llmtypes.ToolCall{ID: "call_1", Type: "function", FunctionCall: &llmtypes.FunctionCall{Name: "execute_shell_command"}},
			},
		},
	}
	if got := finalResponse(messages); got != "" {
		t.Fatalf("finalResponse()=%q, want empty (turn not actually finished yet)", got)
	}
}

func TestFinalResponseReturnsEmptyWhenNewestAssistantMessageHasToolCall(t *testing.T) {
	messages := []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "older progress update"),
		{
			Role: llmtypes.ChatMessageTypeAI,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "checking now"},
				llmtypes.ToolCall{ID: "call-1", Type: "function", FunctionCall: &llmtypes.FunctionCall{Name: "execute_shell_command"}},
			},
		},
	}

	if got := finalResponse(messages); got != "" {
		t.Fatalf("finalResponse()=%q, want empty while newest assistant message has a pending tool call", got)
	}
}

func TestFinalResponseUsesTextAfterEarlierToolCallCompletes(t *testing.T) {
	messages := []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeAI,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "checking now"},
				llmtypes.ToolCall{ID: "call-1", Type: "function", FunctionCall: &llmtypes.FunctionCall{Name: "execute_shell_command"}},
			},
		},
		llmtypes.TextPart(llmtypes.ChatMessageTypeTool, "done"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "final answer"),
	}

	if got := finalResponse(messages); got != "final answer" {
		t.Fatalf("finalResponse()=%q, want final answer", got)
	}
}

func TestFinalResponseDoesNotCrossANewerUserQuery(t *testing.T) {
	messages := []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "old schedule reply"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "fix Slack readability"),
	}
	if got := finalResponse(messages); got != "" {
		t.Fatalf("stale final = %q", got)
	}
}
