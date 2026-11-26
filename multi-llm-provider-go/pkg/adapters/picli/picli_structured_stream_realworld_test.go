package picli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/agentreview"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type piStreamCapture struct {
	content       strings.Builder
	contentTexts  []string
	contentChunks int
	contentRaw    []llmtypes.StreamChunk // raw Content chunks (w/ metadata) for reassembly checks
	toolStarts    int
	toolNames     []string
	order         []string
	terminalOnly  int
}

// collectPiStructuredStream captures Pi's structured stream: unlike the
// transcript-tailing adapters, Pi emits Content + ToolCallStart chunks live from
// its injected MARKER stream (piChunkMetadata tags them provider=pi-cli), so
// there is no stream_source filter — every non-terminal structured chunk here is
// the streaming path under test. Terminal-snapshot chunks are counted separately.
func collectPiStructuredStream(streamChan <-chan llmtypes.StreamChunk) <-chan piStreamCapture {
	done := make(chan piStreamCapture, 1)
	go func() {
		var cap piStreamCapture
		for chunk := range streamChan {
			switch chunk.Type {
			case llmtypes.StreamChunkTypeContent:
				// Keep EVERY content chunk (including whitespace-only message
				// boundaries) for reassembly — StreamAssistantText needs them to
				// separate messages. Only skip whitespace for the display counts.
				cap.contentRaw = append(cap.contentRaw, chunk)
				cap.content.WriteString(chunk.Content)
				if strings.TrimSpace(chunk.Content) == "" {
					continue
				}
				cap.contentTexts = append(cap.contentTexts, chunk.Content)
				cap.contentChunks++
				cap.order = append(cap.order, "text")
			case llmtypes.StreamChunkTypeToolCallStart:
				cap.toolStarts++
				cap.toolNames = append(cap.toolNames, chunk.ToolName)
				cap.order = append(cap.order, "tool")
			case llmtypes.StreamChunkTypeTerminal:
				cap.terminalOnly++
			}
		}
		done <- cap
	}()
	return done
}

func piDistinctToolNames(names []string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		if strings.TrimSpace(n) != "" {
			m[n] = true
		}
	}
	return m
}
func piSortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestPiCLIStructuredStreamingRealWorldLive is Pi's structured-streaming P0 proof,
// the pi-side counterpart to the Claude/Codex/Cursor streaming tests. A real Pi
// tmux turn, MCP bridge-only, drives narration interleaved with two api-bridge
// echo_contract calls. Pi streams structured chunks natively via its injected
// marker stream, so the test asserts Content + ToolCallStart chunks arrived live
// (a no-terminal UI can render them) AND that the tools actually executed (both
// tokens returned), then records the streamed output for agent review.
//
// Gated behind -coding-cli-p0-live; requires a real pi CLI, node, tmux, and a
// GEMINI_API_KEY / GOOGLE_API_KEY / PI_API_KEY.
func TestPiCLIStructuredStreamingRealWorldLive(t *testing.T) {
	requireRealPiCLIContractE2E(t)
	t.Cleanup(func() { _ = CleanupPiCLIInteractiveSessions(context.Background()) })

	adapter := newRealPiCLIAdapter(t)
	workDir := piLiveWorkDir(t)
	tokenA := "A_" + piRandomHex(4)
	tokenB := "B_" + piRandomHex(4)
	wantA := "PI_STREAM_" + tokenA
	wantB := "PI_STREAM_" + tokenB
	mcpConfig := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":"node","args":[%q]}}}`, writePiEchoMCPServer(t, "PI_STREAM"))

	streamChan := make(chan llmtypes.StreamChunk, 2048)
	captureDone := collectPiStructuredStream(streamChan)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	task := "You are verifying an MCP bridge. The api-bridge MCP server exposes echo_contract. " +
		"Do these steps in order, writing one short sentence of narration BEFORE each tool call:\n" +
		"1. Narrate, then call echo_contract with token " + tokenA + ".\n" +
		"2. Narrate, then call echo_contract with token " + tokenB + ".\n" +
		"Finally, on one line, reply with both tool result strings exactly as returned."

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "Use declared MCP tools when asked. Reply exactly with tool results."),
			llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, task),
		},
		WithInteractiveSessionID("pi-realworld-stream-"+piRandomHex(4)),
		WithPersistentInteractiveSession(true),
		WithWorkingDir(workDir),
		WithMCPConfig(mcpConfig),
		llmtypes.WithStreamingChan(streamChan),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	capture := <-captureDone

	final := ""
	if len(resp.Choices) == 1 {
		final = strings.TrimSpace(resp.Choices[0].Content)
	}
	t.Logf("pi realworld structured stream: %d content, %d tool start(s) %v, order=%v, %d terminal; streamed=%q final=%q",
		capture.contentChunks, capture.toolStarts, capture.toolNames, capture.order, capture.terminalOnly,
		strings.TrimSpace(capture.content.String()), final)

	// End-to-end: the real MCP tool executed (both tokens came back in the reply).
	if !strings.Contains(final, wantA) || !strings.Contains(final, wantB) {
		t.Fatalf("final response missing bridge tool results (%q and %q); the real MCP tool did not both run. final=%q", wantA, wantB, final)
	}
	// Streaming: Pi's marker stream emitted structured chunks (content and/or a
	// tool start) that a no-terminal UI could render.
	if capture.contentChunks == 0 && capture.toolStarts == 0 {
		t.Fatalf("no structured chunks streamed at all (only terminal=%d); Pi's marker stream produced nothing renderable. order=%v", capture.terminalOnly, capture.order)
	}

	// Reassembly on REAL output: pi streams token-level deltas that split
	// mid-token, so the message-modes reassembler (StreamAssistantText) must
	// concatenate them without splitting tokens. Verify the user-facing message
	// keeps both result tokens INTACT (this is the regression guard for the
	// delta-garbling bug, exercised on live pi chunks rather than synthetic ones).
	reassembled := llmtypes.StreamAssistantText(capture.contentRaw)
	for _, want := range []string{wantA, wantB} {
		if !strings.Contains(reassembled, want) {
			t.Fatalf("reassembled message lost/garbled token %q (mid-token delta not concatenated cleanly): %q", want, reassembled)
		}
	}
	if capture.contentChunks > len(capture.contentTexts) { // sanity
		t.Fatalf("content accounting mismatch")
	}

	rec := agentreview.Write(t, "TestPiCLIStructuredStreamingRealWorldLive",
		"Pi bridge-only: narrate + 2 interleaved MCP echo_contract calls, streamed live from pi markers",
		map[string]any{
			"content_chunks":      capture.contentTexts,
			"streamed_content":    strings.TrimSpace(capture.content.String()),
			"reassembled_message": reassembled, // what a streaming UI shows after granularity-aware reassembly
			"stream_order":        capture.order,
			"tool_names":          capture.toolNames,
			"final":               final,
		},
		map[string]any{"distinct_tools": piSortedKeys(piDistinctToolNames(capture.toolNames))},
	)
	agentreview.RequireReviewed(t, rec)
}
