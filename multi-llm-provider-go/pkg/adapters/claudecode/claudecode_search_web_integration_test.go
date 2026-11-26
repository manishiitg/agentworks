package claudecode

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestClaudeCodeRealSearchWeb(t *testing.T) {
	requireRealClaudeCodeSearchWebE2E(t)

	adapter := NewClaudeCodeAdapter("", "claude-haiku-5-5", quietClaudeSearchLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	streamChan := make(chan llmtypes.StreamChunk, 128)
	captureDone := collectClaudeSearchStream(streamChan)
	result, err := adapter.SearchWeb(ctx,
		"What is the capital of France? Use WebSearch and reply with the city and country only.",
		llmtypes.WithStreamingChan(streamChan),
	)
	if err != nil {
		t.Fatalf("SearchWeb() error = %v", err)
	}
	if !strings.Contains(strings.ToLower(result), "paris") {
		t.Fatalf("expected result to mention Paris, got %q", result)
	}
	if capture := <-captureDone; capture.toolStarts == 0 {
		t.Fatalf("expected SearchWeb to emit a native WebSearch tool call, streamed content=%q", capture.content)
	}
}

// TestClaudeCodeRealSearchWebStructured is the structured/JSON-transport
// counterpart to TestClaudeCodeRealSearchWeb (which only ever exercised tmux
// -- the public adapter's GenerateContent always delegates to
// ClaudeCodeInteractiveAdapter, and no prior test opted into
// WithClaudeStructuredTransport for native web search).
func TestClaudeCodeRealSearchWebStructured(t *testing.T) {
	requireRealClaudeCodeSearchWebE2E(t)

	adapter := NewClaudeCodeAdapter("", "claude-haiku-5-5", quietClaudeSearchLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	streamChan := make(chan llmtypes.StreamChunk, 128)
	captureDone := collectClaudeSearchStream(streamChan)
	result, err := adapter.SearchWeb(ctx,
		"What is the capital of France? Use WebSearch and reply with the city and country only.",
		WithClaudeStructuredTransport(true),
		llmtypes.WithStreamingChan(streamChan),
	)
	if err != nil {
		t.Fatalf("SearchWeb() error = %v", err)
	}
	if !strings.Contains(strings.ToLower(result), "paris") {
		t.Fatalf("expected result to mention Paris, got %q", result)
	}
	if capture := <-captureDone; capture.toolStarts == 0 {
		t.Fatalf("expected structured-transport SearchWeb to emit a native WebSearch tool call, streamed content=%q", capture.content)
	}
}

func requireRealClaudeCodeSearchWebE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("RUN_CLAUDE_CODE_SEARCH_WEB_E2E") == "" {
		t.Skip("set RUN_CLAUDE_CODE_SEARCH_WEB_E2E=1 to run legacy Claude Code -p web search test")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Fatalf("real Claude Code web search test requires claude in PATH: %v", err)
	}
}

func TestClaudeCodeRealSearchWebLiveData(t *testing.T) {
	requireRealClaudeCodeSearchWebE2E(t)

	adapter := NewClaudeCodeAdapter("", "claude-haiku-5-5", quietClaudeSearchLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := adapter.SearchWeb(ctx,
		"Search the web for the latest Claude Code CLI version number released in 2026. Reply with just the version string.",
	)
	if err != nil {
		t.Fatalf("SearchWeb() error = %v", err)
	}
	result = strings.TrimSpace(result)
	if result == "" {
		t.Fatal("SearchWeb returned empty result")
	}
	t.Logf("Live web search result: %s", result)
}

type quietClaudeSearchLogger struct{}

func (quietClaudeSearchLogger) Debugf(string, ...interface{}) {}
func (quietClaudeSearchLogger) Infof(string, ...any)          {}
func (quietClaudeSearchLogger) Errorf(string, ...any)         {}

type claudeSearchStreamCapture struct {
	content    string
	toolStarts int
}

func collectClaudeSearchStream(streamChan <-chan llmtypes.StreamChunk) <-chan claudeSearchStreamCapture {
	done := make(chan claudeSearchStreamCapture, 1)
	go func() {
		var capture claudeSearchStreamCapture
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
