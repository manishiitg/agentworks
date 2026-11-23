package cursorcli

import (
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func has(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestBuildCursorStructuredPromptResumeSendsOnlyLatestUserMessage(t *testing.T) {
	messages := []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "old user context"}}},
		{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "old assistant context"}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "latest request"}}},
	}

	got := buildCursorStructuredPrompt("large repeated system instructions", messages, "cursor-native-session")
	if got != "latest request" {
		t.Fatalf("resumed structured prompt = %q, want only newest user message", got)
	}
}

func TestBuildCursorStructuredPromptFreshTurnBootstrapsContext(t *testing.T) {
	messages := []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "first"}}},
		{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "answer"}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "next"}}},
	}

	got := buildCursorStructuredPrompt("system", messages, "")
	for _, want := range []string{"[System Instructions]\nsystem", "User: first", "Assistant: answer", "User: next"} {
		if !strings.Contains(got, want) {
			t.Fatalf("fresh structured prompt missing %q: %q", want, got)
		}
	}
}
