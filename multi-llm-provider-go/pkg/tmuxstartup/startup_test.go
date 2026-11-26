package tmuxstartup

import (
	"context"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestPublishEmitsImmediateTerminalIdentity(t *testing.T) {
	stream := make(chan llmtypes.StreamChunk, 1)
	if !Publish(context.Background(), stream, "claude-code", "opus-4.8", "tmux-123", "/workspace", map[string]interface{}{
		"claude_code_interactive_session": "tmux-123",
	}) {
		t.Fatal("Publish returned false")
	}

	chunk := <-stream
	if chunk.Type != llmtypes.StreamChunkTypeStatusLine || chunk.StatusLine == nil {
		t.Fatalf("chunk = %#v, want status line", chunk)
	}
	if got := chunk.StatusLine.Metadata["tmux_session"]; got != "tmux-123" {
		t.Fatalf("tmux_session = %v, want tmux-123", got)
	}
	if got := chunk.StatusLine.Metadata["step_transport"]; got != "tmux" {
		t.Fatalf("step_transport = %v, want tmux", got)
	}
	if got := chunk.StatusLine.Metadata["working_dir"]; got != "/workspace" {
		t.Fatalf("working_dir = %v, want /workspace", got)
	}
}

func TestPublishRejectsMissingSession(t *testing.T) {
	stream := make(chan llmtypes.StreamChunk, 1)
	if Publish(context.Background(), stream, "codex-cli", "gpt", "", "", nil) {
		t.Fatal("Publish returned true for empty tmux session")
	}
	if len(stream) != 0 {
		t.Fatalf("stream length = %d, want 0", len(stream))
	}
}
