package vertex

import (
	"context"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The transport label is declared per-adapter in ObservabilityConfig's
// RequestMetaExtra and surfaces on every synthetic-terminal chunk as
// Metadata["transport"]. The frontend renders it as the transport-class chip,
// so a wrong (or absent) label shows a direct API call as something it is not.
//
// llmtypes/synthetic_terminal_test.go already covers the label mechanically,
// but it constructs the terminal by hand and passes the string itself -- it
// proves the plumbing, not that any real adapter declares the right value.
// This runs the actual adapter through the real WithObservability path against
// the live Gemini API, so it fails if the declaration is dropped in a refactor
// (exactly how the CLI adapters' own "structured_cli" label was silently
// orphaned when structured mode was split out of the tmux path).
//
// Same gate as the other real-API tests here: RUN_VERTEX_REAL_E2E=1 plus a key.
func TestVertexRealDeclaresAPITransportOnTerminalChunks(t *testing.T) {
	adapter, model := newRealVertexAdapter(t)

	streamCh := make(chan llmtypes.StreamChunk, 256)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	done := make(chan struct{})
	var terminalChunks []llmtypes.StreamChunk
	go func() {
		defer close(done)
		for chunk := range streamCh {
			if chunk.Type == llmtypes.StreamChunkTypeTerminal {
				terminalChunks = append(terminalChunks, chunk)
			}
		}
	}()

	if _, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with exactly: ok"}},
		}},
		llmtypes.WithStreamingChan(streamCh),
		llmtypes.WithModel(model),
	); err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	// WithObservability owns closing the stream channel, so the drain
	// goroutine ends on its own once the call returns.
	<-done

	if len(terminalChunks) == 0 {
		t.Fatal("no synthetic-terminal chunks emitted; cannot verify the transport label")
	}
	for i, chunk := range terminalChunks {
		got, _ := chunk.Metadata["transport"].(string)
		if got != "api" {
			t.Fatalf("terminal chunk %d transport = %q, want %q -- a direct API call must not fall back to the generic label", i, got, "api")
		}
	}
}
