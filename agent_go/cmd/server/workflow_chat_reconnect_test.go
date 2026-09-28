package server

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func reconnectMessage(role llmtypes.ChatMessageType, text string) llmtypes.MessageContent {
	return llmtypes.MessageContent{Role: role, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}}}
}

func TestPolicyReconnectCarriesRetryContextWithoutOldTools(t *testing.T) {
	history := []llmtypes.MessageContent{
		reconnectMessage(llmtypes.ChatMessageTypeSystem, "obsolete system permissions"),
		reconnectMessage(llmtypes.ChatMessageTypeHuman, "Test Notion search"),
		{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.ToolCall{ID: "old-call", FunctionCall: &llmtypes.FunctionCall{Name: "obsolete_tool"}}}},
		reconnectMessage(llmtypes.ChatMessageTypeTool, "large old tool output"),
		reconnectMessage(llmtypes.ChatMessageTypeAI, "Notion is connected, but this chat only has Jam in scope."),
	}
	handoff := buildModeChangeConversationContext("workshop", "workshop", "Workflow/test/conversation.json", history)
	for _, expected := range []string{"Test Notion search", "Notion is connected", "current tool permissions", "Parts[].Text"} {
		if !strings.Contains(handoff, expected) {
			t.Fatalf("handoff is missing %q", expected)
		}
	}
	for _, excluded := range []string{"obsolete system permissions", "obsolete_tool", "large old tool output"} {
		if strings.Contains(handoff, excluded) {
			t.Fatalf("old runtime content leaked: %q", excluded)
		}
	}
	if strings.Index(handoff, "Test Notion search") > strings.Index(handoff, "Notion is connected") {
		t.Fatal("dialogue order reversed")
	}
	current := []llmtypes.MessageContent{
		reconnectMessage(llmtypes.ChatMessageTypeHuman, handoff),
		reconnectMessage(llmtypes.ChatMessageTypeHuman, "try it again once"),
		reconnectMessage(llmtypes.ChatMessageTypeAI, "Notion search succeeded"),
	}
	merged := mergeModeChangedChatHistory(history, current)
	if len(merged) != len(history)+2 {
		t.Fatal("reconnect must keep all earlier turns and append only the new exchange")
	}
	// A second policy refresh in the same process must still see the original
	// request, without nesting the previous synthetic handoff.
	next := buildModeChangeConversationContext("workshop", "run", "", merged)
	if !strings.Contains(next, "Test Notion search") || strings.Count(next, "[WORKFLOW CHAT HANDOFF]") != 1 {
		t.Fatal("repeated reconnect lost context or nested transport context")
	}
}

func TestPolicyReconnectBoundsDialogueAndSkipsToolNoise(t *testing.T) {
	var history []llmtypes.MessageContent
	for i := 0; i < 200; i++ {
		history = append(history, reconnectMessage(llmtypes.ChatMessageTypeAI, strings.Repeat("界", 10000)))
	}
	history = append(history, reconnectMessage(llmtypes.ChatMessageTypeHuman, "retry the Notion check"))
	for i := 0; i < 100; i++ {
		history = append(history, reconnectMessage(llmtypes.ChatMessageTypeTool, strings.Repeat("tool output", 1000)))
	}
	got := buildModeChangeConversationContext("run", "workshop", "", history)
	if !strings.Contains(got, "retry the Notion check") || !utf8.ValidString(got) || len(got) > maxCodingAgentFallbackBytes {
		t.Fatalf("invalid bounded handoff: bytes=%d validUTF8=%v", len(got), utf8.ValidString(got))
	}
}

func TestPolicyReconnectKeepsContextWithHeavilyEscapedLatestReply(t *testing.T) {
	history := []llmtypes.MessageContent{
		reconnectMessage(llmtypes.ChatMessageTypeHuman, "Test Notion search"),
		reconnectMessage(llmtypes.ChatMessageTypeAI, strings.Repeat("<>&\x01", 10000)+"Please retry Notion"),
	}
	got := buildModeChangeConversationContext("workshop", "workshop", "", history)
	if !strings.Contains(got, "Test Notion search") || !strings.Contains(got, "Please retry Notion") || len(got) > maxCodingAgentFallbackBytes {
		t.Fatal("large encoded reply displaced the request or exceeded the context budget")
	}
}

func TestBuildCodingAgentContinuityNoticePointsAtProjectArchive(t *testing.T) {
	got := buildCodingAgentContinuityNotice(
		"_users/u/Chats/Work/projects/demo/builder/conversation/2026-09-17/session-chat-conversation.json",
		"_users/u/Chats/Work/projects/demo",
	)
	if !strings.Contains(got, "read its last 10 dialogue turns") || !strings.Contains(got, "Before answering that message") {
		t.Fatalf("notice does not require reading the recent turns: %s", got)
	}
	if !strings.Contains(got, "builder/conversation/2026-09-17/session-chat-conversation.json") {
		t.Fatalf("notice does not expose the project-relative archive: %s", got)
	}
	if strings.Contains(got, "_users/u/Chats/Work/projects/demo/") {
		t.Fatalf("notice leaked docs-root path instead of project-relative path: %s", got)
	}
}

func TestBuildCodingAgentContinuityNoticeNormalizesUserPrefixedProjectPath(t *testing.T) {
	got := buildCodingAgentContinuityNotice(
		"_users/u/Chats/Work/projects/demo/builder/conversation/session.json",
		"Chats/Work/projects/demo",
	)
	if !strings.Contains(got, "at builder/conversation/session.json (relative to the project workspace)") {
		t.Fatalf("notice path is not relative to the provider cwd: %s", got)
	}
	if strings.Contains(got, "_users/u/") {
		t.Fatalf("notice retained the docs-root user prefix: %s", got)
	}
}

func TestPrependCodingAgentContinuityNoticeUsesSameVisibleUserTurn(t *testing.T) {
	got := prependCodingAgentContinuityNotice(
		"when will it get picked up?",
		"_users/u/Chats/Work/projects/demo/builder/conversation/session.json",
		"_users/u/Chats/Work/projects/demo",
	)
	if !strings.HasPrefix(got, "[AGENTWORKS CONVERSATION CONTINUITY]") {
		t.Fatalf("combined user turn does not begin with continuity notice: %s", got)
	}
	if !strings.HasSuffix(got, "[USER MESSAGE]\nwhen will it get picked up?") {
		t.Fatalf("combined user turn does not retain the user's message: %s", got)
	}
	if strings.Count(got, "[AGENTWORKS CONVERSATION CONTINUITY]") != 1 {
		t.Fatalf("combined user turn contains duplicate continuity notices: %s", got)
	}
}

// The notice carries only the archive path and how to read it, never a
// pasted copy of the dialogue (that made the typed prompt tens of KB).
func TestCodingAgentContinuityNoticeIsPathOnly(t *testing.T) {
	got := prependCodingAgentContinuityNotice("what did we work on yesterday", "builder/conversation/c.json", "")
	for _, want := range []string{"builder/conversation/c.json", ".[-10:][]", "Read further back yourself", "Do not rely on chat-index.json", "[USER MESSAGE]\nwhat did we work on yesterday"} {
		if !strings.Contains(got, want) {
			t.Fatalf("notice missing %q:\n%s", want, got)
		}
	}
	if len(got) > 1500 {
		t.Fatalf("notice is %d bytes; it must stay a short path reference", len(got))
	}
}
