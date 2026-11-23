package claudecode

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestClaudeCodeTmuxRealMultilinePromptIsActedOn: Claude Code records a
// multi-line paste as <pasted_content> with no typed text, and Haiku refused
// this exact prompt as "a prompt injection attempt" (2026-09-23). With the
// typed authorization line it must simply follow the instructions.
func TestClaudeCodeTmuxRealMultilinePromptIsActedOn(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)
	adapter := NewClaudeCodeInteractiveAdapter(claudeHaikuRegressionModel, &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })
	token := "probe-b-" + randomHex(4)
	prompt := "This is session B.\nThe session B token is: " + token + "\nDo not mention session A.\nReply exactly: B saved " + token
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}})
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	got := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(got, "B saved "+token) {
		t.Fatalf("multi-line prompt not acted on: %q", got)
	}
	t.Logf("PASS: %q", got)
}
