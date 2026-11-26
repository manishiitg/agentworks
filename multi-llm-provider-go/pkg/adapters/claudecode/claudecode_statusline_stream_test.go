package claudecode

import (
	"context"
	"os"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/internal/testcontracts"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Compile-time conformance: the claude-code adapter must satisfy the statusline
// contract interface so the cross-provider e2e harness can drive it.
var _ llmtypes.StatusLineProvider = (*ClaudeCodeInteractiveAdapter)(nil)

// TestStreamClaudeStatusLineEmitsChunk is a producer-side e2e: it writes a real
// Claude Code statusLine JSON (native model/cost shape) to the path the streamer
// reads, runs streamClaudeStatusLine, and asserts the emitted chunk carries the
// real model (display_name), cost, tokens, and the owning tmux session — with no
// fabricated "claude-3-5-sonnet" default.
func TestStreamClaudeStatusLineEmitsChunk(t *testing.T) {
	sessionName := "claude-statusline-e2e-pane"
	path := claudeStatuslinePath(sessionName)

	payload := `{
		"model": {"id": "claude-opus-4-8", "display_name": "Opus 4.8"},
		"cost": {"total_cost_usd": 0.0421},
		"input_tokens": 15000,
		"output_tokens": 273,
		"cache_read_input_tokens": 48000
	}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write statusline file: %v", err)
	}
	defer os.Remove(path)

	// Reset the per-session dedup so the streamer emits for this content.
	claudeStatusLineStreamMu.Lock()
	delete(claudeStatusLineStreamed, sessionName)
	claudeStatusLineStreamMu.Unlock()

	streamChan := make(chan llmtypes.StreamChunk, 1)
	if ok := streamClaudeStatusLine(context.Background(), sessionName, streamChan); !ok {
		t.Fatal("streamClaudeStatusLine returned false; expected it to emit a chunk")
	}

	select {
	case chunk := <-streamChan:
		if chunk.Type != llmtypes.StreamChunkTypeStatusLine {
			t.Fatalf("chunk.Type = %q, want status_line", chunk.Type)
		}
		sl := chunk.StatusLine
		if sl == nil {
			t.Fatal("chunk.StatusLine is nil")
		}
		// Shared cross-provider contract: canonical provider name, no placeholder
		// model duplication, real tokens, valid tmux tag.
		testcontracts.AssertStatusLineContract(t, sl, "claudecode", true)
		if sl.Model != "Opus 4.8" {
			t.Errorf("Model = %q, want 'Opus 4.8' (real model, not a hardcoded default)", sl.Model)
		}
		if sl.CostUSD != 0.0421 {
			t.Errorf("CostUSD = %v, want 0.0421", sl.CostUSD)
		}
		if sl.InputTokens != 15000 || sl.OutputTokens != 273 || sl.CacheReadInputTokens != 48000 {
			t.Errorf("tokens not surfaced: %+v", sl)
		}
		if got, _ := sl.Metadata["tmux_session"].(string); got != sessionName {
			t.Errorf("Metadata[tmux_session] = %q, want %q", got, sessionName)
		}
	default:
		t.Fatal("no chunk emitted on streamChan")
	}
}
