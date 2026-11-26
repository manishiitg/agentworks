package codexcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"
)

func TestCodexCLIRealInteractiveTmuxFullContract(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	ownerSessionID := "codex-real-contract-" + codexRandomHex(4)
	token := "REAL_CODEX_TMUX_" + codexRandomHex(4)

	options := []llmtypes.CallOption{
		WithInteractiveSessionID(ownerSessionID),
		WithPersistentInteractiveSession(true),
		WithDisableShellTool(),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
	}

	largeSystemPrompt := strings.Repeat("You are testing the Codex CLI tmux transport. Do not use tools. Keep exact-token replies concise.\n", 80)
	firstPrompt := fmt.Sprintf(`This is a real Codex CLI tmux contract test.

Preserve input safely:

blank line above
JSON: {"token": %q, "items": ["alpha", "beta"]}
Shell-looking text that must not execute: echo SHOULD_NOT_RUN
Unicode: नमस्ते

Take note of the word %s. Do not save it to memory.

Reply exactly:
noted %s`, token, token, token)

	firstStream := make(chan llmtypes.StreamChunk, 64)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	firstOptions := append([]llmtypes.CallOption{}, options...)
	firstOptions = append(firstOptions, llmtypes.WithStreamingChan(firstStream))

	first, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: largeSystemPrompt}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: firstPrompt}}},
	}, firstOptions...)
	if err != nil {
		t.Fatalf("first GenerateContent error = %v", err)
	}
	firstContent := strings.TrimSpace(first.Choices[0].Content)
	if !strings.Contains(firstContent, token) {
		t.Fatalf("first content = %q, want token %s", firstContent, token)
	}
	assertCodexInteractiveTerminalOnlyStream(t, firstStream)

	tmuxSession, ok := activeCodexInteractiveSession(ownerSessionID)
	if !ok || tmuxSession == "" {
		t.Fatalf("expected active Codex tmux session for %s", ownerSessionID)
	}
	pane, err := captureCodexPane(ctx, tmuxSession)
	if err != nil {
		t.Fatalf("capture Codex pane: %v", err)
	}
	if !hasCodexReadyPrompt(pane) {
		t.Fatalf("real Codex TUI ready prompt not detected; pane:\n%s", pane)
	}
	if hasCodexActivity(pane) {
		t.Fatalf("real Codex TUI should be idle after first turn; pane:\n%s", pane)
	}

	secondStream := make(chan llmtypes.StreamChunk, 64)
	secondOptions := append([]llmtypes.CallOption{}, options...)
	secondOptions = append(secondOptions, llmtypes.WithStreamingChan(secondStream))
	second, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: largeSystemPrompt}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "What exact word did I ask you to take note of? Reply with only that word."}}},
	}, secondOptions...)
	if err != nil {
		t.Fatalf("second GenerateContent error = %v", err)
	}
	secondContent := strings.TrimSpace(second.Choices[0].Content)
	if !strings.Contains(secondContent, token) {
		t.Fatalf("second content = %q, want remembered token %s from native tmux session", secondContent, token)
	}
	if strings.Contains(secondContent, "saved "+token) {
		t.Fatalf("second content replayed first assistant response: %q", secondContent)
	}
	assertCodexInteractiveTerminalOnlyStream(t, secondStream)

	tmuxSessionAfter, ok := activeCodexInteractiveSession(ownerSessionID)
	if !ok || tmuxSessionAfter != tmuxSession {
		t.Fatalf("expected same tmux session reused, before=%q after=%q ok=%v", tmuxSession, tmuxSessionAfter, ok)
	}
}

func TestCodexCLIRealInteractiveMCPBridgeContract(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	ownerSessionID := "codex-real-mcp-" + codexRandomHex(4)
	bridgeToken := "BRIDGE_REAL_" + codexRandomHex(4)

	mcpServerPath := writeCodexContractMCPServer(t)
	mcpCommandOverride, err := codexStringConfigOverride("mcp_servers.api-bridge.command", mcpServerPath)
	if err != nil {
		t.Fatalf("build MCP command override: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	streamChan := make(chan llmtypes.StreamChunk, 128)
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Use only declared MCP tools. Keep the final answer concise."}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: fmt.Sprintf("Call the api-bridge echo_contract MCP tool with token %s. Then reply exactly with the tool result text.", bridgeToken)}}},
	},
		WithInteractiveSessionID(ownerSessionID),
		WithPersistentInteractiveSession(true),
		WithDisableShellTool(),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
		WithConfigOverrides([]string{mcpCommandOverride}),
		llmtypes.WithStreamingChan(streamChan),
	)
	if err != nil {
		t.Fatalf("GenerateContent with MCP bridge error = %v", err)
	}

	want := "BRIDGE_TOOL_OK_" + bridgeToken
	content := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(content, want) {
		t.Fatalf("content = %q, want bridge tool result %q", content, want)
	}
	assertCodexInteractiveTerminalOnlyStream(t, streamChan)
}

func TestCodexCLIRealMCPBridgeFileFinalExtractionContract(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	ownerSessionID := "codex-real-final-extract-" + codexRandomHex(4)
	sessionMarker := "LIVE_CODEX_FINAL_" + codexRandomHex(5)
	token := "BRIDGE_FILE_" + codexRandomHex(5)
	outputPath := filepath.Join(t.TempDir(), "bridge-file-proof.json")
	mcpServerPath := writeCodexIsolationMCPServer(t, sessionMarker, outputPath)
	mcpServersJSON := fmt.Sprintf(`{"api-bridge":{"command":%q}}`, mcpServerPath)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	streamChan := make(chan llmtypes.StreamChunk, 128)
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Use only declared MCP tools for file operations. The final answer must contain only the requested result text, never tool calls, arguments, or outputs."}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: fmt.Sprintf("Call the api-bridge write_contract MCP tool with token %s. This tool writes a proof file. After it returns, reply with exactly its result text and nothing else.", token)}}},
	},
		WithInteractiveSessionID(ownerSessionID),
		WithPersistentInteractiveSession(true),
		WithDisableShellTool(),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
		WithMCPServers(mcpServersJSON),
		llmtypes.WithStreamingChan(streamChan),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	content := strings.TrimSpace(resp.Choices[0].Content)
	want := "ISOLATED_OK_" + sessionMarker + "_" + token
	if content != want {
		t.Fatalf("clean final content = %q, want exactly %q", content, want)
	}
	proof, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("MCP bridge did not create file proof at %s: %v", outputPath, err)
	}
	for _, required := range []string{sessionMarker, token} {
		if !strings.Contains(string(proof), required) {
			t.Fatalf("MCP file proof missing %q: %s", required, proof)
		}
	}
	assertCodexDoesNotContainAny(t, "final completion result", content,
		"api-bridge", "write_contract", "Called ", "Calling ", "tool", "arguments", "call_id", "ctrl+o", "❯", "›")

	_ = drainCodexStream(streamChan)
}

func TestCodexCLIRealInteractiveWorkspaceTrustPromptContract(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	ownerSessionID := "codex-real-trust-" + codexRandomHex(4)
	token := "TRUST_REAL_" + codexRandomHex(4)
	streamChan := make(chan llmtypes.StreamChunk, 128)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Do not use tools. Keep the response exact and concise."}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply exactly: trusted " + token}}},
	},
		WithInteractiveSessionID(ownerSessionID),
		WithPersistentInteractiveSession(true),
		WithProjectDirID(t.TempDir()),
		WithDisableShellTool(),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
		llmtypes.WithStreamingChan(streamChan),
	)
	if err != nil {
		t.Fatalf("GenerateContent in fresh workspace error = %v", err)
	}
	content := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(content, token) {
		t.Fatalf("content = %q, want trust token %s", content, token)
	}
	assertCodexDoesNotContainAny(t, "trust response", content, "No, quit", "Press enter to continue", "Do you trust")
	assertCodexInteractiveTerminalOnlyStream(t, streamChan)
}

func TestCodexCLIRealInteractiveQueuedValidationDoesNotCompleteDuringMCPTool(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	ownerSessionID := "codex-real-queued-validation-" + codexRandomHex(4)
	bridgeToken := "SLOW_BRIDGE_REAL_" + codexRandomHex(4)

	slowToolMarker := filepath.Join(t.TempDir(), "slow-tool-started")
	mcpServerPath := writeCodexSlowContractMCPServer(t, slowToolMarker)
	mcpCommandOverride, err := codexStringConfigOverride("mcp_servers.api-bridge.command", mcpServerPath)
	if err != nil {
		t.Fatalf("build MCP command override: %v", err)
	}

	parentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultCh := make(chan codexRealResult, 1)
	startupErrCh := make(chan error, 1)
	streamChan := make(chan llmtypes.StreamChunk, 128)

	go func() {
		resp, err := adapter.GenerateContent(parentCtx, []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Use only declared MCP tools. Keep the final answer concise."}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: fmt.Sprintf("Call the api-bridge slow_contract MCP tool with token %s and delay_ms 30000. Do not answer until the tool returns. Then reply exactly with the tool result text.", bridgeToken)}}},
		},
			WithInteractiveSessionID(ownerSessionID),
			WithPersistentInteractiveSession(true),
			WithDisableShellTool(),
			WithApprovalPolicy("never"),
			WithReasoningEffort("low"),
			WithConfigOverrides([]string{mcpCommandOverride}),
			llmtypes.WithStreamingChan(streamChan),
		)
		out := codexRealResult{err: err}
		if err == nil && resp != nil && len(resp.Choices) > 0 && resp.Choices[0] != nil {
			out.content = resp.Choices[0].Content
		}
		if err != nil {
			select {
			case startupErrCh <- err:
			default:
			}
		}
		resultCh <- out
	}()

	tmuxSession := waitForCodexRealActiveSession(t, ownerSessionID, 45*time.Second, startupErrCh)
	waitForCodexRealFile(t, slowToolMarker, "slow MCP tool call start", 90*time.Second, resultCh)

	activePane := waitForCodexRealPaneCondition(t, tmuxSession, "active slow MCP tool", 15*time.Second, resultCh, func(pane string) bool {
		return hasCodexActivity(pane)
	})
	if hasCodexReadyPrompt(activePane) {
		cancel()
		t.Fatalf("Codex pane looked ready while slow MCP tool was still active:\n%s", activePane)
	}
	select {
	case got := <-resultCh:
		cancel()
		if got.err != nil {
			t.Fatalf("GenerateContent returned while slow MCP tool was active: err=%v content=%q", got.err, got.content)
		}
		t.Fatalf("GenerateContent completed while slow MCP tool was active; content=%q", got.content)
	case <-time.After(3 * time.Second):
	}

	validationPrompt := `## Pre-validation failed (retry attempt 3)

❌ PRE-VALIDATION FAILED

Missing required output file. Fix the specific issue above and re-produce the required outputs.`
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := SendCodexInteractiveInput(sendCtx, ownerSessionID, validationPrompt); err != nil {
		sendCancel()
		cancel()
		t.Fatalf("SendCodexInteractiveInput error = %v", err)
	}
	sendCancel()

	queuedPane := waitForCodexRealPaneCondition(t, tmuxSession, "queued validation input", 15*time.Second, resultCh, func(pane string) bool {
		return hasCodexQueuedInput(pane) || strings.Contains(pane, "Pre-validation failed")
	})
	if !hasCodexActivity(queuedPane) {
		cancel()
		t.Fatalf("Codex queued validation pane was not considered active:\n%s", queuedPane)
	}
	if hasCodexReadyPrompt(queuedPane) {
		cancel()
		t.Fatalf("Codex queued validation pane looked ready/completed:\n%s", queuedPane)
	}

	select {
	case got := <-resultCh:
		cancel()
		if got.err != nil {
			t.Fatalf("GenerateContent returned while validation input was queued: err=%v content=%q", got.err, got.content)
		}
		t.Fatalf("GenerateContent completed while validation input was queued; content=%q", got.content)
	case <-time.After(3 * time.Second):
		// The regression was an early false completion while the TUI still showed
		// queued input. Remaining active here proves the adapter did not parse the
		// queued validation prompt as the assistant's final response.
	}

	cancel()
	select {
	case got := <-resultCh:
		if got.err == nil {
			t.Fatalf("GenerateContent completed normally after cancellation; content=%q", got.content)
		}
	case <-time.After(45 * time.Second):
		t.Fatal("timed out waiting for GenerateContent to return after cancellation")
	}
	_ = drainCodexStream(streamChan)
}

func TestCodexCLIRealInteractiveLiveInputAndEscapeContract(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	ownerSessionID := "codex-real-live-" + codexRandomHex(4)
	token := "LIVE_REAL_" + codexRandomHex(4)

	parentCtx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	streamChan := make(chan llmtypes.StreamChunk, 128)

	go func() {
		_, err := adapter.GenerateContent(parentCtx, []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Do not use tools. This is a live input and Escape transport test."}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: fmt.Sprintf("Draft a detailed 1200 word checklist about tmux transport testing. Include token %s near the end only.", token)}}},
		},
			WithInteractiveSessionID(ownerSessionID),
			WithPersistentInteractiveSession(true),
			WithDisableShellTool(),
			WithApprovalPolicy("never"),
			WithReasoningEffort("low"),
			llmtypes.WithStreamingChan(streamChan),
		)
		errCh <- err
	}()

	tmuxSession := waitForCodexRealActiveSession(t, ownerSessionID, 45*time.Second, errCh)
	// Registration precedes CLI startup readiness. Give the live-input gate
	// enough time to settle before treating this as a Stop/reuse failure.
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 60*time.Second)
	liveMessage := "LIVE_FOLLOWUP_" + token
	liveErr := SendCodexInteractiveInput(sendCtx, ownerSessionID, liveMessage)
	sendCancel()
	if liveErr != nil {
		cancel()
		t.Fatalf("SendCodexInteractiveInput error = %v", liveErr)
	}

	pane, err := captureCodexPane(context.Background(), tmuxSession)
	if err != nil {
		cancel()
		t.Fatalf("capture Codex pane after live input: %v", err)
	}
	if !strings.Contains(pane, liveMessage) {
		cancel()
		t.Fatalf("live input was not visible in tmux pane; pane:\n%s", pane)
	}

	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatalf("GenerateContent completed normally; want cancellation after Escape path")
		}
	case <-time.After(45 * time.Second):
		t.Fatalf("timed out waiting for GenerateContent to return after cancellation")
	}
	_ = drainCodexStream(streamChan)
	retryCtx, retryCancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer retryCancel()
	retryToken := "CODEX_AFTER_STOP_" + codexRandomHex(4)
	retry, err := adapter.GenerateContent(retryCtx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply with exactly "+retryToken+" and nothing else."),
	},
		WithInteractiveSessionID(ownerSessionID),
		WithPersistentInteractiveSession(true),
		WithDisableShellTool(),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
	)
	if err != nil {
		t.Fatalf("new message after Stop: %v", err)
	}
	if len(retry.Choices) == 0 || !strings.Contains(retry.Choices[0].Content, retryToken) {
		t.Fatalf("new message after Stop was not answered: %#v", retry.Choices)
	}
}

// TestCodexCLIRealInteractiveLiveInputProcessesQueuedFollowupContract verifies
// the property the steer-vs-queue removal depends on for CLI agents: a message
// delivered mid-turn (while Codex is busy in a slow MCP tool) is queued by the
// Codex CLI ITSELF and processed once the current turn completes. It sends the
// follow-up via raw SendCodexInteractiveInput (bypassing the server steer/queue),
// so a processed follow-up proves the CLI's own native queue did the work.
func TestCodexCLIRealInteractiveLiveInputSteersBusyTurnContract(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	ownerSessionID := "codex-real-live-queue-" + codexRandomHex(4)
	toolToken := "SLOW_CODEX_QUEUE_" + codexRandomHex(4)
	firstDone := "CODEX_FIRST_DONE_" + codexRandomHex(4)
	liveAck := "CODEX_LIVE_ACK_" + codexRandomHex(4)

	slowToolMarker := filepath.Join(t.TempDir(), "slow-tool-started")
	mcpServerPath := writeCodexSlowContractMCPServer(t, slowToolMarker)
	mcpCommandOverride, err := codexStringConfigOverride("mcp_servers.api-bridge.command", mcpServerPath)
	if err != nil {
		t.Fatalf("build MCP command override: %v", err)
	}

	paneDiag := func() string {
		if sn, ok := activeCodexInteractiveSession(ownerSessionID); ok {
			p, _ := captureCodexPaneForDisplay(context.Background(), sn)
			return p
		}
		return "(no active session)"
	}

	parentCtx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	resultCh := make(chan codexRealResult, 1)
	startupErrCh := make(chan error, 1)
	streamChan := make(chan llmtypes.StreamChunk, 128)

	go func() {
		resp, err := adapter.GenerateContent(parentCtx, []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "This is a Codex CLI transport test. Use only declared MCP tools. If a follow-up user message arrives while you are working, handle it after the current tool call finishes. Keep answers concise."}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: fmt.Sprintf("Call the api-bridge slow_contract MCP tool with token %s and delay_ms 8000. Do not answer until the tool returns. Then reply exactly %s.", toolToken, firstDone)}}},
		},
			WithInteractiveSessionID(ownerSessionID),
			WithPersistentInteractiveSession(true),
			WithDisableShellTool(),
			WithApprovalPolicy("never"),
			WithReasoningEffort("low"),
			WithConfigOverrides([]string{mcpCommandOverride}),
			llmtypes.WithStreamingChan(streamChan),
		)
		out := codexRealResult{err: err}
		if err == nil && resp != nil && len(resp.Choices) > 0 && resp.Choices[0] != nil {
			out.content = resp.Choices[0].Content
		}
		if err != nil {
			select {
			case startupErrCh <- err:
			default:
			}
		}
		resultCh <- out
	}()

	tmuxSession := waitForCodexRealActiveSession(t, ownerSessionID, 45*time.Second, startupErrCh)
	ownerOutput, err := exec.CommandContext(parentCtx, "tmux", "show-options", "-v", "-t", tmuxSession, tmuxinput.OwnerSessionOption).CombinedOutput()
	if err != nil || strings.TrimSpace(string(ownerOutput)) != ownerSessionID {
		t.Fatalf("tmux owner metadata = %q err=%v, want %q", strings.TrimSpace(string(ownerOutput)), err, ownerSessionID)
	}
	waitForCodexRealFile(t, slowToolMarker, "slow MCP tool call start", 90*time.Second, resultCh)

	// Deliver the follow-up WHILE Codex is busy in the slow tool, via the raw
	// adapter path (no server queue in between).
	// Additive, not a competing "reply exactly": the first prompt already says
	// "reply exactly <firstDone>", and two exact-reply instructions made the model
	// pick one (it answered only firstDone about 1 run in 3 on codex 0.156.1 even
	// though the rollout showed the steer delivered inside the same turn). The
	// assertion below is unchanged: the final answer must contain liveAck.
	liveMessage := fmt.Sprintf("Additional instruction for this same task: after the current tool returns, also include %s on its own line in your final reply.", liveAck)
	steerSentAt := time.Now()
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := SendCodexInteractiveInput(sendCtx, ownerSessionID, liveMessage); err != nil {
		sendCancel()
		cancel()
		t.Fatalf("SendCodexInteractiveInput error = %v", err)
	}
	sendCancel()

	select {
	case got := <-resultCh:
		if got.err != nil {
			t.Fatalf("GenerateContent error = %v (content=%q)", got.err, got.content)
		}
		if !strings.Contains(got.content, liveAck) {
			t.Fatalf("live steer did not affect the busy Codex turn; final content=%q\npane:\n%s", got.content, paneDiag())
		}
		// P0 proof comes from the rollout, not the pane: a long steer wraps
		// across pane lines, so a pane substring match is presentation (P1).
		rolloutPath := ""
		if session, ok := codexPersistentRegistry.Get(ownerSessionID); ok && session != nil {
			rolloutPath, _ = codexRolloutIdentity(session)
		}
		if rolloutPath == "" {
			t.Fatalf("no rollout bound to the live session; pane:\n%s", paneDiag())
		}
		if _, ok := codexRolloutUserMessageSince(rolloutPath, liveMessage, steerSentAt, 0); !ok {
			t.Fatalf("live follow-up has no user row in the rollout %s; pane:\n%s", rolloutPath, paneDiag())
		}
		if codexRolloutHasTurnAbortedSince(t, rolloutPath, steerSentAt) {
			t.Fatalf("normal live input interrupted the active Codex conversation (turn_aborted in %s)", rolloutPath)
		}
		t.Log("OK: Codex CLI durably submitted and applied the live steer while the initial turn was busy")
	case <-time.After(3 * time.Minute):
		t.Fatalf("timed out waiting for Codex to complete after live input submission; pane:\n%s", paneDiag())
	}
	_ = drainCodexStream(streamChan)
}

// codexRolloutHasTurnAbortedSince reports a turn_aborted event recorded at or
// after since: the structured equivalent of the pane's "conversation
// interrupted" notice.
func codexRolloutHasTurnAbortedSince(t *testing.T, path string, since time.Time) bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rollout %s: %v", path, err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		var e struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Payload   struct {
				Type string `json:"type"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &e) != nil || e.Type != "event_msg" || e.Payload.Type != "turn_aborted" {
			continue
		}
		if ts, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil && !ts.Before(since) {
			return true
		}
	}
	return false
}

func requireRealCodexCLIE2E(t *testing.T) {
	t.Helper()
	if !*codingCLIP0Live {
		t.Skip("run through the live coding CLI P0 runner")
	}
	for _, bin := range []string{"codex", "node"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("real Codex CLI tests require %s in PATH: %v", bin, err)
		}
	}
}

func waitForCodexRealActiveSession(t *testing.T, ownerSessionID string, timeout time.Duration, errCh <-chan error) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("GenerateContent returned before active session was available: %v", err)
		default:
		}
		if sessionName, ok := activeCodexInteractiveSession(ownerSessionID); ok {
			return sessionName
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for real Codex interactive session %q", ownerSessionID)
	return ""
}

func waitForCodexRealPaneCondition(t *testing.T, tmuxSession, label string, timeout time.Duration, errCh <-chan codexRealResult, matches func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case got := <-errCh:
			t.Fatalf("GenerateContent returned before tmux pane matched %s: err=%v content=%q", label, got.err, got.content)
		default:
		}
		pane, err := captureCodexPane(context.Background(), tmuxSession)
		if err == nil && matches(pane) {
			return pane
		}
		time.Sleep(250 * time.Millisecond)
	}
	pane, _ := captureCodexPane(context.Background(), tmuxSession)
	t.Fatalf("timed out waiting for Codex tmux pane to match %s; latest pane:\n%s", label, pane)
	return ""
}

func waitForCodexRealFile(t *testing.T, path, label string, timeout time.Duration, errCh <-chan codexRealResult) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case got := <-errCh:
			t.Fatalf("GenerateContent returned before %s: err=%v content=%q", label, got.err, got.content)
		default:
		}
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s at %s", label, path)
}

type codexRealResult struct {
	content string
	err     error
}

func writeCodexContractMCPServer(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex-contract-mcp.js")
	script := `#!/usr/bin/env node
const readline = require("readline");
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });

function send(message) {
  process.stdout.write(JSON.stringify(message) + "\n");
}

rl.on("line", (line) => {
  if (!line.trim()) return;
  let msg;
  try {
    msg = JSON.parse(line);
  } catch (err) {
    return;
  }
  if (msg.method === "initialize") {
    send({
      jsonrpc: "2.0",
      id: msg.id,
      result: {
        protocolVersion: "2024-11-05",
        capabilities: { tools: {} },
        serverInfo: { name: "api-bridge", version: "1.0.0" }
      }
    });
    return;
  }
  if (msg.method === "notifications/initialized") {
    return;
  }
  if (msg.method === "tools/list") {
    send({
      jsonrpc: "2.0",
      id: msg.id,
      result: {
        tools: [
          {
            name: "echo_contract",
            description: "Return a deterministic contract token.",
            inputSchema: {
              type: "object",
              properties: { token: { type: "string" } },
              required: ["token"]
            }
          },
          {
            name: "slow_contract",
            description: "Return a deterministic contract token after a caller-provided delay.",
            inputSchema: {
              type: "object",
              properties: {
                token: { type: "string" },
                delay_ms: { type: "number" }
              },
              required: ["token"]
            }
          }
        ]
      }
    });
    return;
  }
  if (msg.method === "tools/call") {
    const name = String((msg.params && msg.params.name) || "");
    const args = (msg.params && msg.params.arguments) || {};
    const token = String(args.token || "");
    if (name === "slow_contract") {
      const delay = Math.max(0, Math.min(Number(args.delay_ms || 30000), 60000));
      setTimeout(() => {
        send({
          jsonrpc: "2.0",
          id: msg.id,
          result: {
            content: [{ type: "text", text: "SLOW_BRIDGE_TOOL_OK_" + token }],
            isError: false
          }
        });
      }, delay);
      return;
    }
    send({
      jsonrpc: "2.0",
      id: msg.id,
      result: {
        content: [{ type: "text", text: "BRIDGE_TOOL_OK_" + token }],
        isError: false
      }
    });
    return;
  }
  if (msg.id !== undefined) {
    send({ jsonrpc: "2.0", id: msg.id, result: {} });
  }
});
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write MCP server: %v", err)
	}
	return path
}

func writeCodexSlowContractMCPServer(t *testing.T, markerPath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex-slow-contract-mcp.js")
	script := fmt.Sprintf(`#!/usr/bin/env node
const fs = require("fs");
const readline = require("readline");
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
const markerPath = %q;

function send(message) {
  process.stdout.write(JSON.stringify(message) + "\n");
}

rl.on("line", (line) => {
  if (!line.trim()) return;
  let msg;
  try {
    msg = JSON.parse(line);
  } catch (err) {
    return;
  }
  if (msg.method === "initialize") {
    send({
      jsonrpc: "2.0",
      id: msg.id,
      result: {
        protocolVersion: "2024-11-05",
        capabilities: { tools: {} },
        serverInfo: { name: "api-bridge", version: "1.0.0" }
      }
    });
    return;
  }
  if (msg.method === "notifications/initialized") {
    return;
  }
  if (msg.method === "tools/list") {
    send({
      jsonrpc: "2.0",
      id: msg.id,
      result: {
        tools: [{
          name: "slow_contract",
          description: "Return a deterministic contract token after a caller-provided delay.",
          inputSchema: {
            type: "object",
            properties: {
              token: { type: "string" },
              delay_ms: { type: "number" }
            },
            required: ["token"]
          }
        }]
      }
    });
    return;
  }
  if (msg.method === "tools/call") {
    const args = (msg.params && msg.params.arguments) || {};
    const token = String(args.token || "");
    const delay = Math.max(0, Math.min(Number(args.delay_ms || 30000), 60000));
    fs.writeFileSync(markerPath, JSON.stringify({ token, delay, started_at: new Date().toISOString() }));
    setTimeout(() => {
      send({
        jsonrpc: "2.0",
        id: msg.id,
        result: {
          content: [{ type: "text", text: "SLOW_BRIDGE_TOOL_OK_" + token }],
          isError: false
        }
      });
    }, delay);
    return;
  }
  if (msg.id !== undefined) {
    send({ jsonrpc: "2.0", id: msg.id, result: {} });
  }
});
`, markerPath)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write slow MCP server: %v", err)
	}
	return path
}

// ---------------------------------------------------------------------------
// E2E: Working directory — Codex CLI launches with the requested cwd and the
// MCP bridge processes inherit that workspace so they can be located by path.
// ---------------------------------------------------------------------------

func TestCodexCLIRealInteractiveWorkingDirectoryContract(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	ownerSessionID := "codex-real-workdir-" + codexRandomHex(4)
	workDir := t.TempDir()

	mcpServerPath := writeCodexCwdReportMCPServer(t)
	mcpCommandOverride, err := codexStringConfigOverride("mcp_servers.api-bridge.command", mcpServerPath)
	if err != nil {
		t.Fatalf("build MCP command override: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	streamChan := make(chan llmtypes.StreamChunk, 128)
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Use only declared MCP tools. Keep the final answer concise."}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Call the api-bridge report_cwd MCP tool. Then reply exactly with the tool result text."}}},
	},
		WithInteractiveSessionID(ownerSessionID),
		WithPersistentInteractiveSession(true),
		WithProjectDirID(workDir),
		WithDisableShellTool(),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
		WithConfigOverrides([]string{mcpCommandOverride}),
		llmtypes.WithStreamingChan(streamChan),
	)
	if err != nil {
		t.Fatalf("GenerateContent with working dir error = %v", err)
	}
	content := strings.TrimSpace(resp.Choices[0].Content)

	// The MCP server reports its process cwd. macOS symlinks /var/folders to
	// /private/var/folders, so the cwd Codex spawns the MCP server under may
	// be either form.
	wantPaths := []string{workDir}
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil && resolved != workDir {
		wantPaths = append(wantPaths, resolved)
	}
	// codex's TUI hard-wraps a long single-line answer across terminal rows,
	// inserting a newline + continuation indent mid-path (it breaks at "/"
	// path-separator boundaries, NOT at terminal width, so it cannot be
	// reconstructed by line length). codex uses the SAME continuation indent for
	// genuine multi-line answers, so the adapter cannot safely auto-unwrap it
	// without corrupting multi-line replies. The reported cwd is a single
	// whitespace-free path, so rejoin the TUI-wrapped segments by removing
	// interior whitespace before asserting the exact path is present. This only
	// undoes the display wrap; the full path substring is still required.
	stripWhitespace := func(s string) string { return strings.Join(strings.Fields(s), "") }
	normalizedContent := stripWhitespace(content)
	matched := false
	for _, want := range wantPaths {
		if strings.Contains(normalizedContent, stripWhitespace("CWD_REPORTED_"+want)) {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("content = %q, want CWD_REPORTED_<workDir> with workDir in %v", content, wantPaths)
	}
	assertCodexInteractiveTerminalOnlyStream(t, streamChan)
}

// ---------------------------------------------------------------------------
// E2E: Parallel session isolation — two concurrent Codex sessions in distinct
// working dirs return their own tokens without leaking each other.
// ---------------------------------------------------------------------------

func TestCodexCLIRealInteractiveParallelIsolation(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})

	type parallelSpec struct {
		name         string
		ownerSession string
		token        string
		workDir      string
	}
	specs := []parallelSpec{
		{
			name:         "left",
			ownerSession: "codex-real-parallel-left-" + codexRandomHex(4),
			token:        "PAR_LEFT_" + codexRandomHex(4),
			workDir:      t.TempDir(),
		},
		{
			name:         "right",
			ownerSession: "codex-real-parallel-right-" + codexRandomHex(4),
			token:        "PAR_RIGHT_" + codexRandomHex(4),
			workDir:      t.TempDir(),
		},
	}

	type parallelResult struct {
		spec    parallelSpec
		content string
		err     error
	}
	resultCh := make(chan parallelResult, len(specs))
	for _, spec := range specs {
		spec := spec
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
				{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Do not use tools. Keep the reply exact and concise."}}},
				{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: fmt.Sprintf("Reply exactly: %s", spec.token)}}},
			},
				WithInteractiveSessionID(spec.ownerSession),
				WithPersistentInteractiveSession(true),
				WithProjectDirID(spec.workDir),
				WithDisableShellTool(),
				WithApprovalPolicy("never"),
				WithReasoningEffort("low"),
			)
			result := parallelResult{spec: spec, err: err}
			if err == nil && resp != nil && len(resp.Choices) > 0 && resp.Choices[0] != nil {
				result.content = resp.Choices[0].Content
			}
			resultCh <- result
		}()
	}

	results := make(map[string]parallelResult, len(specs))
	for range specs {
		got := <-resultCh
		if got.err != nil {
			t.Fatalf("%s GenerateContent error = %v", got.spec.name, got.err)
		}
		results[got.spec.name] = got
	}

	for _, spec := range specs {
		got := results[spec.name]
		if !strings.Contains(got.content, spec.token) {
			t.Fatalf("%s content = %q, want token %s", spec.name, got.content, spec.token)
		}
		otherToken := ""
		for _, other := range specs {
			if other.name != spec.name {
				otherToken = other.token
				break
			}
		}
		if otherToken != "" && strings.Contains(got.content, otherToken) {
			t.Fatalf("%s content leaked other session's token %s: %q", spec.name, otherToken, got.content)
		}
	}
}

// ---------------------------------------------------------------------------
// E2E: Shared-workdir MCP isolation — two concurrent Codex sessions that
// share the same working directory still see their own MCP server replies.
// ---------------------------------------------------------------------------

func TestCodexCLIRealInteractiveSharedWorkingDirMCPIsolation(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	sharedWorkDir := filepath.Join(t.TempDir(), "shared-workspace")
	if err := os.MkdirAll(sharedWorkDir, 0o755); err != nil {
		t.Fatalf("create shared workdir: %v", err)
	}

	type runSpec struct {
		name          string
		ownerSession  string
		sessionMarker string
		token         string
		outputPath    string
		mcpServerPath string
		mcpOverride   string
	}
	specs := []runSpec{
		{
			name:          "alpha",
			ownerSession:  "codex-real-shared-alpha-" + codexRandomHex(4),
			sessionMarker: "SESSION_ALPHA_" + codexRandomHex(4),
			token:         "TOKEN_ALPHA_" + codexRandomHex(4),
			outputPath:    filepath.Join(t.TempDir(), "alpha-output.json"),
		},
		{
			name:          "beta",
			ownerSession:  "codex-real-shared-beta-" + codexRandomHex(4),
			sessionMarker: "SESSION_BETA_" + codexRandomHex(4),
			token:         "TOKEN_BETA_" + codexRandomHex(4),
			outputPath:    filepath.Join(t.TempDir(), "beta-output.json"),
		},
	}
	for i := range specs {
		specs[i].mcpServerPath = writeCodexIsolationMCPServer(t, specs[i].sessionMarker, specs[i].outputPath)
		ov, err := json.Marshal(map[string]any{"api-bridge": map[string]any{"command": specs[i].mcpServerPath}})
		if err != nil {
			t.Fatalf("build MCP command override for %s: %v", specs[i].name, err)
		}
		specs[i].mcpOverride = string(ov)
	}

	type runResult struct {
		spec    runSpec
		content string
		err     error
	}
	resultCh := make(chan runResult, len(specs))
	for _, spec := range specs {
		spec := spec
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
				{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Use only declared MCP tools. Keep the final answer concise."}}},
				{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: fmt.Sprintf("Call the api-bridge write_contract MCP tool with token %s. Then reply exactly with the tool result text.", spec.token)}}},
			},
				WithInteractiveSessionID(spec.ownerSession),
				WithPersistentInteractiveSession(true),
				WithProjectDirID(sharedWorkDir),
				WithDisableShellTool(),
				WithApprovalPolicy("never"),
				WithReasoningEffort("low"),
				WithMCPServers(spec.mcpOverride),
			)
			result := runResult{spec: spec, err: err}
			if err == nil && resp != nil && len(resp.Choices) > 0 && resp.Choices[0] != nil {
				result.content = resp.Choices[0].Content
			}
			resultCh <- result
		}()
	}

	results := make(map[string]runResult, len(specs))
	for range specs {
		got := <-resultCh
		if got.err != nil {
			t.Fatalf("%s GenerateContent error = %v", got.spec.name, got.err)
		}
		results[got.spec.name] = got
	}

	for _, spec := range specs {
		got := results[spec.name]
		want := "ISOLATED_OK_" + spec.sessionMarker + "_" + spec.token
		if !strings.Contains(got.content, want) {
			t.Fatalf("%s content = %q, want isolated tool result %q", spec.name, got.content, want)
		}
		data, err := os.ReadFile(spec.outputPath)
		if err != nil {
			t.Fatalf("%s output file missing at %s: %v", spec.name, spec.outputPath, err)
		}
		forbiddenMarker := ""
		for _, other := range specs {
			if other.name != spec.name {
				forbiddenMarker = other.sessionMarker
				break
			}
		}
		text := string(data)
		if !strings.Contains(text, spec.sessionMarker) || !strings.Contains(text, spec.token) {
			t.Fatalf("%s output = %s, want session %s and token %s", spec.name, text, spec.sessionMarker, spec.token)
		}
		if forbiddenMarker != "" && strings.Contains(text, forbiddenMarker) {
			t.Fatalf("%s output crossed sessions: %s", spec.name, text)
		}
	}
}

// ---------------------------------------------------------------------------
// E2E: Cleanup — tmux session is removed after explicit cleanup.
// ---------------------------------------------------------------------------

func TestCodexCLIRealInteractiveCleanup(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	ownerSessionID := "codex-real-cleanup-" + codexRandomHex(4)
	workDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	stream := make(chan llmtypes.StreamChunk, 64)
	_, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Keep replies concise. Do not use tools."}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply exactly: cleanup test OK"}}},
	},
		WithInteractiveSessionID(ownerSessionID),
		WithPersistentInteractiveSession(true),
		WithProjectDirID(workDir),
		WithDisableShellTool(),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
		llmtypes.WithStreamingChan(stream),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	_ = drainCodexStream(stream)

	tmuxSession, ok := activeCodexInteractiveSession(ownerSessionID)
	if !ok || tmuxSession == "" {
		t.Fatalf("expected active tmux session for %s before cleanup", ownerSessionID)
	}

	if err := CleanupCodexCLIInteractiveSessions(context.Background()); err != nil {
		t.Fatalf("cleanup error = %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	if _, stillActive := activeCodexInteractiveSession(ownerSessionID); stillActive {
		t.Fatalf("tmux session still registered after cleanup for %s", ownerSessionID)
	}

	out, err := exec.CommandContext(ctx, "tmux", "has-session", "-t", tmuxSession).CombinedOutput()
	if err == nil {
		t.Fatalf("tmux session %s still exists after cleanup; output=%s", tmuxSession, string(out))
	}
}

// ---------------------------------------------------------------------------
// Helpers for working-dir + isolation contracts
// ---------------------------------------------------------------------------

func writeCodexCwdReportMCPServer(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex-cwd-report-mcp.js")
	script := `#!/usr/bin/env node
const readline = require("readline");
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
function send(message) { process.stdout.write(JSON.stringify(message) + "\n"); }
rl.on("line", (line) => {
  if (!line.trim()) return;
  let msg;
  try { msg = JSON.parse(line); } catch (err) { return; }
  if (msg.method === "initialize") {
    send({ jsonrpc: "2.0", id: msg.id, result: { protocolVersion: "2024-11-05", capabilities: { tools: {} }, serverInfo: { name: "api-bridge", version: "1.0.0" } } });
    return;
  }
  if (msg.method === "notifications/initialized") return;
  if (msg.method === "tools/list") {
    send({ jsonrpc: "2.0", id: msg.id, result: { tools: [{ name: "report_cwd", description: "Return the MCP server process cwd, which the Codex CLI inherits from the launched workspace.", inputSchema: { type: "object", properties: {}, required: [] } }] } });
    return;
  }
  if (msg.method === "tools/call") {
    send({ jsonrpc: "2.0", id: msg.id, result: { content: [{ type: "text", text: "CWD_REPORTED_" + process.cwd() }], isError: false } });
    return;
  }
  if (msg.id !== undefined) send({ jsonrpc: "2.0", id: msg.id, result: {} });
});
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write cwd MCP server: %v", err)
	}
	return path
}

func writeCodexIsolationMCPServer(t *testing.T, sessionMarker, outputPath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex-isolation-contract-mcp.js")
	script := fmt.Sprintf(`#!/usr/bin/env node
const fs = require("fs");
const readline = require("readline");
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
const sessionMarker = %q;
const outputPath = %q;

function send(message) { process.stdout.write(JSON.stringify(message) + "\n"); }

rl.on("line", (line) => {
  if (!line.trim()) return;
  let msg;
  try { msg = JSON.parse(line); } catch (err) { return; }
  if (msg.method === "initialize") {
    send({ jsonrpc: "2.0", id: msg.id, result: { protocolVersion: "2024-11-05", capabilities: { tools: {} }, serverInfo: { name: "api-bridge", version: "1.0.0" } } });
    return;
  }
  if (msg.method === "notifications/initialized") return;
  if (msg.method === "tools/list") {
    send({ jsonrpc: "2.0", id: msg.id, result: { tools: [{ name: "write_contract", description: "Write a deterministic marker proving this MCP server/session was used.", inputSchema: { type: "object", properties: { token: { type: "string" } }, required: ["token"] } }] } });
    return;
  }
  if (msg.method === "tools/call") {
    const args = (msg.params && msg.params.arguments) || {};
    const token = String(args.token || "");
    const payload = { session_marker: sessionMarker, token, cwd: process.cwd(), timestamp: new Date().toISOString() };
    fs.writeFileSync(outputPath, JSON.stringify(payload, null, 2));
    send({ jsonrpc: "2.0", id: msg.id, result: { content: [{ type: "text", text: "ISOLATED_OK_" + sessionMarker + "_" + token }], isError: false } });
    return;
  }
  if (msg.id !== undefined) send({ jsonrpc: "2.0", id: msg.id, result: {} });
});
`, sessionMarker, outputPath)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write isolation MCP server: %v", err)
	}
	return path
}

func assertCodexDoesNotContainAny(t *testing.T, label, got string, forbidden ...string) {
	t.Helper()
	for _, item := range forbidden {
		if strings.Contains(got, item) {
			t.Fatalf("%s leaked %q in %q", label, item, got)
		}
	}
}
