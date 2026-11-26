package codexcli

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCodexCLIRealSearchWeb(t *testing.T) {
	requireRealCodexCLISearchWebE2E(t)

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, quietCodexStreamLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	streamChan := make(chan llmtypes.StreamChunk, 128)
	captureDone := collectCodexStream(streamChan)
	result, err := adapter.SearchWeb(ctx,
		"What is the capital of France? Use web search and reply with the city and country only.",
		WithReasoningEffort("low"),
		llmtypes.WithStreamingChan(streamChan),
	)
	if err != nil {
		t.Fatalf("SearchWeb() error = %v", err)
	}
	if !strings.Contains(strings.ToLower(result), "paris") {
		t.Fatalf("expected result to mention Paris, got %q", result)
	}
	if capture := <-captureDone; capture.toolStarts == 0 {
		t.Fatalf("expected SearchWeb to emit a native web-search tool call, streamed content=%q", capture.content)
	}
}

// TestCodexCLIRealSearchWebStructured is the structured/JSON-transport
// counterpart to TestCodexCLIRealSearchWeb (which only ever exercised tmux --
// SearchWeb's own GenerateContent call defaults to the tmux transport, and no
// prior test covered `codex exec --json` for native web search at all).
func TestCodexCLIRealSearchWebStructured(t *testing.T) {
	requireRealCodexCLISearchWebE2E(t)

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, quietCodexStreamLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	streamChan := make(chan llmtypes.StreamChunk, 128)
	captureDone := collectCodexStream(streamChan)
	result, err := adapter.SearchWeb(ctx,
		"What is the capital of France? Use web search and reply with the city and country only.",
		WithReasoningEffort("low"),
		WithCodexStructuredTransport(true),
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

func TestCodexCLIRealSearchWebLiveData(t *testing.T) {
	requireRealCodexCLISearchWebE2E(t)

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, quietCodexStreamLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := adapter.SearchWeb(ctx,
		"Search the web for the latest OpenAI Codex CLI version number released in 2026. Reply with just the version string.",
		WithReasoningEffort("low"),
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

func requireRealCodexCLISearchWebE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("RUN_CODEX_CLI_REAL_E2E") == "" && os.Getenv("RUN_CODEX_CLI_SEARCH_WEB_E2E") == "" {
		t.Skip("set RUN_CODEX_CLI_SEARCH_WEB_E2E=1 to run real Codex CLI web search test")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Fatalf("real Codex CLI web search test requires codex in PATH: %v", err)
	}
}

type quietCodexStreamLogger struct{}

func (quietCodexStreamLogger) Debugf(string, ...interface{}) {}
func (quietCodexStreamLogger) Infof(string, ...any)          {}
func (quietCodexStreamLogger) Errorf(string, ...any)         {}

type codexStreamCapture struct {
	content    string
	toolStarts int
	toolEnds   int
}

func collectCodexStream(streamChan <-chan llmtypes.StreamChunk) <-chan codexStreamCapture {
	done := make(chan codexStreamCapture, 1)
	go func() {
		var capture codexStreamCapture
		var content strings.Builder
		for chunk := range streamChan {
			switch chunk.Type {
			case llmtypes.StreamChunkTypeContent:
				content.WriteString(chunk.Content)
			case llmtypes.StreamChunkTypeToolCallStart:
				capture.toolStarts++
			case llmtypes.StreamChunkTypeToolCallEnd:
				capture.toolEnds++
			}
		}
		capture.content = strings.TrimSpace(content.String())
		done <- capture
	}()
	return done
}
