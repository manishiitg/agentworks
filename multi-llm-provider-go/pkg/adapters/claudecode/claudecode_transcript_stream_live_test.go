package claudecode

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/agentreview"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// transcriptStreamCapture collects only the chunks emitted by the JSONL
// transcript tailer (Metadata.claude_code_stream_source == "transcript"), so
// assertions prove THIS feature's path — not the terminal-snapshot stream — and
// preserves their arrival ORDER so we can prove interleaving.
type transcriptStreamCapture struct {
	content       strings.Builder
	contentTexts  []string // each content chunk verbatim, so formatting/readability is reviewable
	contentChunks int
	toolStarts    int
	toolNames     []string
	order         []string // ordered "text"/"tool" sequence, transcript-sourced only
	terminalOnly  int
}

func collectTranscriptStream(streamChan <-chan llmtypes.StreamChunk) <-chan transcriptStreamCapture {
	done := make(chan transcriptStreamCapture, 1)
	go func() {
		var cap transcriptStreamCapture
		for chunk := range streamChan {
			fromTranscript := chunk.Metadata != nil && chunk.Metadata["claude_code_stream_source"] == "transcript"
			switch chunk.Type {
			case llmtypes.StreamChunkTypeContent:
				if fromTranscript {
					cap.content.WriteString(chunk.Content)
					cap.contentTexts = append(cap.contentTexts, chunk.Content)
					cap.contentChunks++
					cap.order = append(cap.order, "text")
				}
			case llmtypes.StreamChunkTypeToolCallStart:
				if fromTranscript {
					cap.toolStarts++
					cap.toolNames = append(cap.toolNames, chunk.ToolName)
					cap.order = append(cap.order, "tool")
				}
			case llmtypes.StreamChunkTypeTerminal:
				cap.terminalOnly++
			}
		}
		done <- cap
	}()
	return done
}

// TestClaudeCodeTranscriptStreamingBridgeLive is the REAL P0-grade proof: it
// runs an authenticated Claude Code tmux turn IN BRIDGE-ONLY MODE against a real
// MCP server (api-bridge / echo_contract) and a real multi-step task that forces
// narration interleaved with two MCP tool calls — the text → tool → text → tool
// → final-text shape a live turn actually produces. With
// WithStreamTranscript(true) it asserts the transcript tailer streamed BOTH
// assistant-text (Content) and MCP tool-call (ToolCallStart) chunks from the
// live JSONL transcript, and that the tools actually executed end to end.
//
// This meets the P0 bar (real MCP bridge + real task), unlike a trivial
// no-tools reply. Gated behind -coding-cli-p0-live; requires a real `claude`
// CLI, `node`, and tmux.
func TestClaudeCodeTranscriptStreamingBridgeLive(t *testing.T) {
	skipClaudeInteractiveIntegration(t)
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	workDir := t.TempDir()
	tokenA := "A_" + randomHex(4)
	tokenB := "B_" + randomHex(4)
	wantA := "BRIDGE_TOOL_OK_" + tokenA
	wantB := "BRIDGE_TOOL_OK_" + tokenB

	mcpConfig := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":%q}}}`, writeClaudeInteractiveContractMCPServer(t))

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	streamChan := make(chan llmtypes.StreamChunk, 1024)
	captureDone := collectTranscriptStream(streamChan)

	task := "You are verifying an MCP bridge. Do these steps in order, writing one short sentence of narration BEFORE each tool call:\n" +
		"1. Narrate, then call the api-bridge echo_contract MCP tool with token " + tokenA + ".\n" +
		"2. Narrate, then call the api-bridge echo_contract MCP tool with token " + tokenB + ".\n" +
		"Finally, on one line, reply with both tool result strings exactly as returned."

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, task),
		},
		WithWorkingDir(workDir),
		WithMCPConfig(mcpConfig),
		WithClaudeCodeTools(""),
		WithAllowedTools("mcp__api-bridge__echo_contract"),
		WithEffort("low"),
		WithStreamTranscript(true),
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
	t.Logf("bridge transcript stream: %d content, %d tool start(s) %v, order=%v, %d terminal; streamed=%q final=%q",
		capture.contentChunks, capture.toolStarts, capture.toolNames, capture.order, capture.terminalOnly,
		strings.TrimSpace(capture.content.String()), final)

	// End-to-end: the real MCP tools executed (both tokens came back in the reply).
	if !strings.Contains(final, wantA) || !strings.Contains(final, wantB) {
		t.Fatalf("final response missing bridge tool results (%q and %q); the real MCP tools did not both run. final=%q", wantA, wantB, final)
	}
	// Streaming: the transcript tailer emitted the real interleaved shape.
	if capture.toolStarts == 0 {
		t.Fatalf("no transcript-sourced ToolCallStart streamed — the MCP tool_use blocks did not stream. order=%v", capture.order)
	}
	if capture.contentChunks == 0 {
		t.Fatalf("no transcript-sourced content streamed alongside the tool calls. order=%v", capture.order)
	}
	if !hasFold(capture.toolNames, "echo_contract") {
		t.Fatalf("streamed tool names %v do not include the MCP tool echo_contract", capture.toolNames)
	}
	// Interleaving: both a text and a tool chunk streamed (order captured above
	// shows the actual text↔tool sequence for the record).
	if !containsStr(capture.order, "text") || !containsStr(capture.order, "tool") {
		t.Fatalf("expected interleaved text and tool chunks; got order=%v", capture.order)
	}

	rec := agentreview.Write(t, "TestClaudeCodeTranscriptStreamingBridgeLive",
		"Claude bridge-only: narrate + 2 interleaved MCP echo_contract calls, streamed live",
		map[string]any{
			"content_chunks":   capture.contentTexts, // discrete chunks — review formatting/readability
			"streamed_content": strings.TrimSpace(capture.content.String()),
			"stream_order":     capture.order,
			"tool_names":       capture.toolNames,
			"final":            final,
		},
		map[string]any{"distinct_tools": sortedKeys(distinctToolNames(capture.toolNames))},
	)
	agentreview.RequireReviewed(t, rec)
}

// TestClaudeCodeTranscriptStreamingDisabledControl is the control for the test
// above: the SAME real bridge turn with the feature OFF (env flag unset). It
// proves the structured content/tool stream is produced by THIS feature and was
// not already there — with streaming disabled the tailer emits nothing, so the
// only stream signal is terminal pane snapshots (which a no-terminal UI can't
// use). The tools still run (both tokens come back), they just don't stream as
// structured chunks. Run alongside the enabled test to see the contrast.
func TestClaudeCodeTranscriptStreamingDisabledControl(t *testing.T) {
	skipClaudeInteractiveIntegration(t)
	// Deliberately DO NOT pass WithStreamTranscript — feature OFF by default.
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	workDir := t.TempDir()
	tokenA := "A_" + randomHex(4)
	tokenB := "B_" + randomHex(4)
	mcpConfig := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":%q}}}`, writeClaudeInteractiveContractMCPServer(t))

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	streamChan := make(chan llmtypes.StreamChunk, 1024)
	captureDone := collectTranscriptStream(streamChan)

	task := "Call the api-bridge echo_contract MCP tool with token " + tokenA + ", then again with token " + tokenB +
		", then reply with both returned tool result strings on one line."
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, task)},
		WithWorkingDir(workDir),
		WithMCPConfig(mcpConfig),
		WithClaudeCodeTools(""),
		WithAllowedTools("mcp__api-bridge__echo_contract"),
		WithEffort("low"),
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
	t.Logf("CONTROL (feature OFF): %d transcript-content, %d transcript-tool, %d terminal; final=%q",
		capture.contentChunks, capture.toolStarts, capture.terminalOnly, final)

	// The tools still ran (proves it was a real, comparable turn)...
	if !strings.Contains(final, "BRIDGE_TOOL_OK_"+tokenA) || !strings.Contains(final, "BRIDGE_TOOL_OK_"+tokenB) {
		t.Fatalf("control turn did not actually run the tools; final=%q", final)
	}
	// ...but with the feature OFF, NO structured content/tool chunks are emitted.
	if capture.contentChunks != 0 || capture.toolStarts != 0 {
		t.Fatalf("feature OFF but transcript-sourced chunks appeared (%d content, %d tool) — the stream was NOT coming from this feature",
			capture.contentChunks, capture.toolStarts)
	}
}

func hasFold(names []string, sub string) bool {
	for _, n := range names {
		if strings.Contains(strings.ToLower(n), strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

func containsStr(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
