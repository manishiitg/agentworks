package server

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestBuilderConversationMessagesFromLLMTypesKeepsTextTurnsOnly(t *testing.T) {
	got := builderConversationMessagesFromLLMTypes([]llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "system"}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "  hello  "}}},
		{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.ToolCall{ID: "t1"}}},
		{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "first"}, llmtypes.TextContent{Text: "second"}}},
		{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "tool output"}}},
	})
	want := []builderConversationMessage{
		{Role: "human", Parts: []builderConversationPart{{Text: "hello"}}},
		{Role: "ai", Parts: []builderConversationPart{{Text: "first\n\nsecond"}}},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if builderConversationMessageKey(got[i]) != builderConversationMessageKey(want[i]) {
			t.Fatalf("message %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestNativeTranscriptMessagesForRuntimeCursorNeedsWorkingDirAndSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, tc := range []struct{ session, dir string }{{"", "/tmp/ws"}, {"agent-1", ""}, {"agent-1", "/tmp/ws"}} {
		_, _, _, ok, err := nativeTranscriptMessagesForRuntime("cursor-cli", tc.session, tc.dir)
		if ok || err != nil {
			t.Fatalf("cursor session=%q dir=%q: ok=%v err=%v, want false/nil (no store present)", tc.session, tc.dir, ok, err)
		}
	}
}
