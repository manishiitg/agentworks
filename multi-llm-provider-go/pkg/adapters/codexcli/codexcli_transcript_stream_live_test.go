package codexcli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/agentreview"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type codexTranscriptStreamCapture struct {
	content       strings.Builder
	contentTexts  []string // each content chunk verbatim, so formatting/readability is reviewable
	contentChunks int
	toolStarts    int
	toolNames     []string
	order         []string
	terminalOnly  int
}

func collectCodexTranscriptStream(streamChan <-chan llmtypes.StreamChunk) <-chan codexTranscriptStreamCapture {
	done := make(chan codexTranscriptStreamCapture, 1)
	go func() {
		var cap codexTranscriptStreamCapture
		for chunk := range streamChan {
			fromTranscript := chunk.Metadata != nil && (chunk.Metadata["codex_cli_stream_source"] == "transcript" || chunk.Metadata["codex_stream_source"] == "app-server")
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

func codexBridgeStreamOpts(t *testing.T, streamChan chan<- llmtypes.StreamChunk) (ownerSessionID string, opts []llmtypes.CallOption) {
	t.Helper()
	ownerSessionID = "codex-transcript-stream-" + codexRandomHex(4)
	mcpCommandOverride, err := codexStringConfigOverride("mcp_servers.api-bridge.command", writeCodexContractMCPServer(t))
	if err != nil {
		t.Fatalf("build MCP command override: %v", err)
	}
	opts = []llmtypes.CallOption{
		WithInteractiveSessionID(ownerSessionID),
		WithPersistentInteractiveSession(true),
		WithProjectDirID(t.TempDir()),
		WithDisableShellTool(),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
		WithConfigOverrides([]string{mcpCommandOverride}),
		llmtypes.WithStreamingChan(streamChan),
	}
	return ownerSessionID, opts
}

// TestCodexCLITranscriptStreamingBridgeLive is the P0-grade proof for Codex: a
// real Codex tmux turn against a real MCP bridge (api-bridge / echo_contract)
// and a real interleaved task, with WithStreamTranscript(true). Asserts the
// rollout tailer streamed BOTH assistant text (Content) and MCP tool-call
// (ToolCallStart) chunks, and the tools actually executed end to end.
func TestCodexCLITranscriptStreamingBridgeLive(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	tokenA := "A_" + codexRandomHex(4)
	tokenB := "B_" + codexRandomHex(4)
	wantA := "BRIDGE_TOOL_OK_" + tokenA
	wantB := "BRIDGE_TOOL_OK_" + tokenB

	streamChan := make(chan llmtypes.StreamChunk, 1024)
	captureDone := collectCodexTranscriptStream(streamChan)
	_, opts := codexBridgeStreamOpts(t, streamChan)
	opts = append(opts, WithStreamTranscript(true))

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	task := "You are verifying an MCP bridge. Do these steps in order, writing one short sentence of narration BEFORE each tool call:\n" +
		"1. Narrate, then call the api-bridge echo_contract MCP tool with token " + tokenA + ".\n" +
		"2. Narrate, then call the api-bridge echo_contract MCP tool with token " + tokenB + ".\n" +
		"Finally, on one line, reply with both tool result strings exactly as returned."

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, task)},
		opts...,
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	capture := <-captureDone

	final := ""
	if len(resp.Choices) == 1 {
		final = strings.TrimSpace(resp.Choices[0].Content)
	}
	t.Logf("codex bridge transcript stream: %d content, %d tool start(s) %v, order=%v, %d terminal; streamed=%q final=%q",
		capture.contentChunks, capture.toolStarts, capture.toolNames, capture.order, capture.terminalOnly,
		strings.TrimSpace(capture.content.String()), final)

	if !strings.Contains(final, wantA) || !strings.Contains(final, wantB) {
		t.Fatalf("final response missing bridge tool results (%q and %q); the MCP tools did not both run. final=%q", wantA, wantB, final)
	}
	if capture.toolStarts == 0 {
		t.Fatalf("no transcript-sourced ToolCallStart streamed. order=%v", capture.order)
	}
	if capture.contentChunks == 0 {
		t.Fatalf("no transcript-sourced content streamed alongside the tool calls. order=%v", capture.order)
	}

	rec := agentreview.Write(t, "TestCodexCLITranscriptStreamingBridgeLive",
		"Codex bridge-only: narrate + 2 interleaved MCP echo_contract calls, streamed live",
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

// TestCodexCLITranscriptStreamingDisabledControl is the control: the SAME real
// bridge turn with the feature OFF emits no structured content/tool chunks.
func TestCodexCLITranscriptStreamingDisabledControl(t *testing.T) {
	requireRealCodexCLIE2E(t)
	// Feature OFF — do NOT pass WithStreamTranscript.
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	tokenA := "A_" + codexRandomHex(4)
	tokenB := "B_" + codexRandomHex(4)

	streamChan := make(chan llmtypes.StreamChunk, 1024)
	captureDone := collectCodexTranscriptStream(streamChan)
	_, opts := codexBridgeStreamOpts(t, streamChan)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	task := "Call the api-bridge echo_contract MCP tool with token " + tokenA + ", then again with token " + tokenB +
		", then reply with both returned tool result strings on one line."
	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, task)},
		opts...,
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	capture := <-captureDone
	final := ""
	if len(resp.Choices) == 1 {
		final = strings.TrimSpace(resp.Choices[0].Content)
	}
	t.Logf("CODEX CONTROL (feature OFF): %d transcript-content, %d transcript-tool, %d terminal; final=%q",
		capture.contentChunks, capture.toolStarts, capture.terminalOnly, final)

	if !strings.Contains(final, "BRIDGE_TOOL_OK_"+tokenA) || !strings.Contains(final, "BRIDGE_TOOL_OK_"+tokenB) {
		t.Fatalf("control turn did not actually run the tools; final=%q", final)
	}
	if capture.contentChunks != 0 || capture.toolStarts != 0 {
		t.Fatalf("feature OFF but transcript-sourced chunks appeared (%d content, %d tool) — stream not from this feature",
			capture.contentChunks, capture.toolStarts)
	}
}
