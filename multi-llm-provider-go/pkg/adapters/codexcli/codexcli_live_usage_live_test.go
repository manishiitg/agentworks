package codexcli

import (
	"context"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Live: a real `codex exec --json` turn streams a status_line chunk with the
// context fill read from its own rollout (stdout has no usage until the end
// and no context window at all). RUN_CODEX_CLI_STREAM_JSON_E2E=1.
func TestCodexCLIStructuredLiveUsage(t *testing.T) {
	requireCodexCLIStructuredE2E(t)

	adapter := NewCodexCLIAdapter("", "codex-cli", &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	stream := make(chan llmtypes.StreamChunk, 256)
	errCh := make(chan error, 1)
	go func() {
		_, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Run `ls` in the current directory with your shell tool, then reply with the word DONE."}}},
		}, WithCodexStructuredTransport(true), WithProjectDirID(t.TempDir()), WithApprovalPolicy("never"), llmtypes.WithStreamingChan(stream))
		errCh <- err
	}()
	var usage *llmtypes.StatusLine
	sawContentAfterUsage := false
	for chunk := range stream {
		switch chunk.Type {
		case llmtypes.StreamChunkTypeStatusLine:
			usage = chunk.StatusLine
			t.Logf("status_line at %s: %v", time.Now().Format(time.TimeOnly), chunk.StatusLine.Metadata)
		case llmtypes.StreamChunkTypeContent:
			if usage != nil {
				sawContentAfterUsage = true
			}
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	if usage == nil {
		t.Fatal("no live status_line chunk from the rollout")
	}
	used, _ := usage.Metadata[llmtypes.ContextUsedTokensMetaKey].(int)
	window, _ := usage.Metadata[llmtypes.ContextWindowTokensMetaKey].(int)
	if used <= 0 || window <= 0 {
		t.Fatalf("context used/window = %d/%d", used, window)
	}
	t.Logf("context %d / %d, windows %v, content after first usage: %v", used, window, usage.RateLimitWindows(), sawContentAfterUsage)
}
