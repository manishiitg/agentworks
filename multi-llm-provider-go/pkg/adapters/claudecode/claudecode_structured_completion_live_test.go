package claudecode

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestClaudeCodeTmuxRealStructuredCompletionP0 certifies done_detection and
// slow_tool_false_idle from the JSONL transcript (PLAT-354) on one retained
// tmux session:
//
//   - turn 1 runs a slow shell tool; the turn must not end before the tool and
//     its final end_turn answer, and must report completion from the transcript;
//   - turn 2, on the same retained session, must also complete from the
//     transcript with its own answer, proving the pane was left usable.
func TestClaudeCodeTmuxRealStructuredCompletionP0(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)
	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })
	mcpServerPath := writeClaudeInteractiveSlowMCPServer(t, filepath.Join(t.TempDir(), "slow-tool-started"))
	options := []llmtypes.CallOption{
		WithInteractiveSessionID("claude-structured-done-" + randomHex(4)),
		WithPersistentInteractiveSession(true),
		WithMCPConfig(fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":%q}}}`, mcpServerPath)),
		WithClaudeCodeTools(""),
		WithAllowedTools("mcp__api-bridge__slow_contract"),
		WithEffort("low"),
	}
	turn := func(prompt string) (string, map[string]interface{}, time.Duration) {
		t.Helper()
		started := time.Now()
		resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, options...)
		if err != nil {
			t.Fatalf("turn %q: %v", prompt, err)
		}
		var additional map[string]interface{}
		if gi := resp.Choices[0].GenerationInfo; gi != nil {
			additional = gi.Additional
		}
		return strings.TrimSpace(resp.Choices[0].Content), additional, time.Since(started)
	}

	token1 := "SLOWTOOL-" + randomHex(4)
	final1, meta1, took1 := turn("Call the api-bridge slow_contract MCP tool with token " + token1 + " and delay_ms 12000. Do not answer until the tool returns. Then reply with exactly " + token1 + " and nothing else.")
	if !strings.Contains(final1, token1) {
		t.Fatalf("slow-tool turn final = %q, want %s", final1, token1)
	}
	if took1 < 12*time.Second {
		t.Fatalf("slow-tool turn ended after %s, before its 12s tool could finish (false idle)", took1)
	}
	if got := meta1["claude_code_completion_source"]; got != "jsonl_end_turn" {
		t.Fatalf("slow-tool turn completion_source = %v, want jsonl_end_turn (meta=%v)", got, meta1)
	}

	token2 := "RETAINED-" + randomHex(4)
	final2, meta2, _ := turn("Reply with exactly " + token2 + " and nothing else. Do not use tools.")
	if !strings.Contains(final2, token2) || strings.Contains(final2, token1) {
		t.Fatalf("retained turn final = %q, want only %s", final2, token2)
	}
	if got := meta2["claude_code_completion_source"]; got != "jsonl_end_turn" {
		t.Fatalf("retained turn completion_source = %v, want jsonl_end_turn", got)
	}
	t.Logf("PASS: slow tool turn %s and retained follow-up both completed from transcript end_turn", took1.Round(time.Second))
}
