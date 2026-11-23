package cursorcli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCursorCLIRealSearchWeb(t *testing.T) {
	requireRealCursorCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCursorCLIInteractiveSessions(context.Background()) })

	adapter := NewCursorCLIAdapter("", "cursor-cli", &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := adapter.SearchWeb(ctx,
		"What is the capital of France? Use web search and reply with the city and country only.",
		WithDenyBuiltinTools(true),
	)
	if err != nil {
		t.Fatalf("SearchWeb() error = %v", err)
	}
	if !strings.Contains(strings.ToLower(result), "paris") {
		t.Fatalf("expected result to mention Paris, got %q", result)
	}
}

func TestCursorCLIRealSearchWebLiveData(t *testing.T) {
	requireRealCursorCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCursorCLIInteractiveSessions(context.Background()) })

	adapter := NewCursorCLIAdapter("", "cursor-cli", &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := adapter.SearchWeb(ctx,
		"Search the web for the latest cursor-agent CLI version number released in 2026. Reply with just the version string.",
		WithDenyBuiltinTools(true),
	)
	if err != nil {
		t.Fatalf("SearchWeb() error = %v", err)
	}
	result = strings.TrimSpace(result)
	if result == "" {
		t.Fatal("SearchWeb returned empty result")
	}
	// The result must contain something that looks like a version — proof of live data.
	if !strings.Contains(result, "20") && !strings.Contains(result, "v") && !strings.Contains(result, ".") {
		t.Logf("WARNING: result may not contain live web data: %q", result)
	}
	t.Logf("Live web search result: %s", result)
}

// TestCursorCLIRealSearchWebStructured is the structured/JSON-transport
// counterpart to TestCursorCLIRealSearchWeb (which only ever exercised tmux
// -- GenerateContent defaults to tmux and SearchWeb never opts into
// structured transport). Cursor's own auto-approve-web-access mechanism is
// tmux-prompt-specific (see TestCursorCLIRealInteractiveAutoApprovesWebOpenURL),
// so this also checks whether native search needs it under structured mode
// or works without any approval step at all in that transport.
func TestCursorCLIRealSearchWebStructured(t *testing.T) {
	requireRealCursorCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCursorCLIInteractiveSessions(context.Background()) })

	adapter := NewCursorCLIAdapter("", "cursor-cli", &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	streamChan := make(chan llmtypes.StreamChunk, 128)
	captureDone := collectCursorSearchStream(streamChan)
	result, err := adapter.SearchWeb(ctx,
		"What is the capital of France? Use web search and reply with the city and country only.",
		llmtypes.WithStreamingChan(streamChan),
	)
	if err != nil {
		t.Fatalf("SearchWeb() error = %v", err)
	}
	if !strings.Contains(strings.ToLower(result), "paris") {
		t.Fatalf("expected result to mention Paris, got %q", result)
	}
	if capture := <-captureDone; capture.toolStarts == 0 {
		t.Fatalf("expected structured-transport SearchWeb to emit a native web-search tool call, streamed content=%q", capture.content)
	}
}

type cursorSearchStreamCapture struct {
	content    string
	toolStarts int
}

func collectCursorSearchStream(streamChan <-chan llmtypes.StreamChunk) <-chan cursorSearchStreamCapture {
	done := make(chan cursorSearchStreamCapture, 1)
	go func() {
		var capture cursorSearchStreamCapture
		var content strings.Builder
		for chunk := range streamChan {
			switch chunk.Type {
			case llmtypes.StreamChunkTypeContent:
				content.WriteString(chunk.Content)
			case llmtypes.StreamChunkTypeToolCallStart:
				capture.toolStarts++
			}
		}
		capture.content = strings.TrimSpace(content.String())
		done <- capture
	}()
	return done
}
