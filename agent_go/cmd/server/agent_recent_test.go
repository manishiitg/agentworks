package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3532: another agent reads this chat's recent items. A tool call carries its own result, secret values in tool
// output are blanked, and the default is the last 5 items.
func TestAgentRecentItemsPairsToolResultsAndBlanksSecrets(t *testing.T) {
	history := []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "system prompt"}}},
	}
	for i := 0; i < 6; i++ {
		history = append(history, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "older ask"}}})
	}
	history = append(history,
		llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{Text: "Deploying now."},
			llmtypes.ToolCall{ID: "call-1", Type: "function", FunctionCall: &llmtypes.FunctionCall{Name: "bash", Arguments: `{"command":"./deploy.sh"}`}},
		}},
		llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{
			llmtypes.ToolCallResponse{ToolCallID: "call-1", Name: "bash", Content: "failed: API_KEY=fixture-key rejected", IsError: true},
		}},
	)
	items := agentRecentItems(history, 0)
	if len(items) != agentRecentDefault {
		t.Fatalf("items = %d, want the default %d", len(items), agentRecentDefault)
	}
	last := items[len(items)-1]
	if last.Role != "tool" || !strings.Contains(last.Text, "deploy.sh") || !last.Error || last.Result == "" {
		t.Fatalf("last item = %+v, want the bash call with its failed result", last)
	}
	if strings.Contains(last.Result, "fixture-key") {
		t.Fatalf("secret value reached another agent: %q", last.Result)
	}
	if got := agentRecentItems(history, 100); len(got) != 8 {
		t.Fatalf("asking for more returned %d items, want all 8 visible items", len(got))
	}
}
