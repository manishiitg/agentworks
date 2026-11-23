package claudecode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/testcontracts"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const (
	claudeTmuxIntegrationModelEnv        = "CLAUDE_CODE_TMUX_INTEGRATION_MODEL"
	claudeInteractiveIntegrationModelEnv = "CLAUDE_CODE_EXPERIMENTAL_INTEGRATION_MODEL"
	// Sonnet 5 by default (2026-09-23): Haiku refused ordinary test prompts as
	// "prompt injection" and sometimes wrote fake tool calls as text, which made
	// the live suite flaky for reasons that were not about the transport.
	defaultClaudeInteractiveTestModel = "claude-sonnet-5-5"
	// claudeHaikuRegressionModel keeps Haiku for the test that caught the
	// pasted-content refusal: a stronger model can hide that production bug.
	claudeHaikuRegressionModel = "claude-haiku-5-5"
)

func TestClaudeCodeTmuxIntegrationNoInternalTools(t *testing.T) {
	skipClaudeInteractiveIntegration(t)

	adapter := NewClaudeCodeInteractiveAdapter(claudeInteractiveIntegrationModel(), &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Try to list files or read README.md using any available tool. If no shell/file/listing/read tool is available, answer exactly NO_SHELL_OR_FILE_TOOL."},
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	got := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(got, "NO_SHELL_OR_FILE_TOOL") {
		t.Fatalf("content = %q, want sentinel NO_SHELL_OR_FILE_TOOL", got)
	}
	if containsFold(got, "I listed") || containsFold(got, "README") {
		t.Fatalf("content = %q, looks like Claude had shell/file access", got)
	}
	if sessionID := experimentalClaudeSessionID(resp); sessionID == "" {
		t.Fatalf("response did not include claude_code_session_id: %#v", resp.Choices[0].GenerationInfo.Additional)
	}
	assertClaudeInteractiveHaikuMetadata(t, resp)
}

func TestClaudeCodeTmuxIntegrationNativeSystemPrompt(t *testing.T) {
	skipClaudeInteractiveIntegration(t)

	adapter := NewClaudeCodeInteractiveAdapter(claudeInteractiveIntegrationModel(), &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	systemToken := "NATIVE_SYSTEM_PROMPT_OK_4821"
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeSystem,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "For this integration test, include this exact token in your next answer: " + systemToken},
			},
		},
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Reply with one short sentence confirming the system prompt token."},
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	got := strings.TrimSpace(resp.Choices[0].Content)
	if !containsFold(got, systemToken) {
		t.Fatalf("content = %q, want token %q from native system prompt", got, systemToken)
	}
	assertClaudeInteractiveHaikuMetadata(t, resp)
}

func TestClaudeCodeTmuxIntegrationFreshPromptCarriesUserText(t *testing.T) {
	skipClaudeInteractiveIntegration(t)

	adapter := NewClaudeCodeInteractiveAdapter(claudeInteractiveIntegrationModel(), &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	userToken := "USER_TEXT_PRESERVED_9173"
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeSystem,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "If the user message is empty, answer exactly EMPTY_INPUT. Otherwise answer with only the exact token from the user message."},
			},
		},
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Token: " + userToken},
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	got := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(got, userToken) || strings.Contains(got, "EMPTY_INPUT") {
		t.Fatalf("content = %q, want user token %q and no EMPTY_INPUT", got, userToken)
	}
	assertClaudeInteractiveHaikuMetadata(t, resp)
}

func TestClaudeCodeTmuxIntegrationLargePastedPromptSubmits(t *testing.T) {
	skipClaudeInteractiveIntegration(t)

	adapter := NewClaudeCodeInteractiveAdapter(claudeInteractiveIntegrationModel(), &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	token := "CLAUDE_LARGE_PASTE_OK_" + randomHex(4)
	var prompt strings.Builder
	prompt.WriteString("This is a Claude Code tmux large-paste transport test.\n")
	prompt.WriteString("Read the full pasted prompt. Do not use tools.\n\n")
	for i := 0; i < 72; i++ {
		fmt.Fprintf(&prompt, "line %02d: preserve pasted multiline input before submitting.\n", i+1)
	}
	fmt.Fprintf(&prompt, "\nReply exactly with this token and nothing else:\n%s", token)

	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeSystem,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Do not use tools. Reply exactly as instructed."},
			},
		},
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: prompt.String()},
			},
		},
	}, WithEffort("low"))
	if err != nil {
		t.Fatalf("GenerateContent large pasted prompt error = %v", err)
	}
	got := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(got, token) {
		t.Fatalf("content = %q, want token %s", got, token)
	}
	assertClaudeInteractiveHaikuMetadata(t, resp)
}

// A multi-line command with long lines (the shape of a user slash command)
// must reach Claude whole. With a separate newline keystroke before each
// pasted line, Claude Code dropped the pasted lines and kept only the last one
// (RTS 2026-09-25), which a last-line token alone never caught.
func TestClaudeCodeTmuxIntegrationMultilineCommandKeepsEveryLine(t *testing.T) {
	skipClaudeInteractiveIntegration(t)

	adapter := NewClaudeCodeInteractiveAdapter(claudeInteractiveIntegrationModel(), &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	first, middle, last := "ALPHA_"+randomHex(3), "BRAVO_"+randomHex(3), "CHARLIE_"+randomHex(3)
	prompt := "This is a transport test for a multi-line command; do not use tools. The first code word is " + first + ", and this opening line is deliberately long so that it wraps in the terminal like a real command would.\n" +
		"1. Read every numbered step before answering; each one matters and none of them may be skipped or summarised away.\n" +
		"2. The second code word is " + middle + ", written on a line that follows a newline key in the composer.\n" +
		"\n" +
		"3. Reply with the three code words in order, separated by single spaces, and nothing else.\n" +
		"\n" +
		"The third code word is " + last + "."
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{
		Role:  llmtypes.ChatMessageTypeHuman,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}},
	}}, WithEffort("low"))
	if err != nil {
		t.Fatalf("GenerateContent multi-line command error = %v", err)
	}
	got := strings.TrimSpace(resp.Choices[0].Content)
	for _, word := range []string{first, middle, last} {
		if !strings.Contains(got, word) {
			t.Fatalf("content = %q, missing %s: a line of the command never reached Claude", got, word)
		}
	}
}

func TestClaudeCodeTmuxIntegrationNativeResume(t *testing.T) {
	skipClaudeInteractiveIntegration(t)

	adapter := NewClaudeCodeInteractiveAdapter(claudeInteractiveIntegrationModel(), &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	codeword := "green-lantern-4821"
	first, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "The reference phrase for this test is " + codeword + ". Reply exactly: noted " + codeword},
			},
		},
	})
	if err != nil {
		t.Fatalf("first GenerateContent error = %v", err)
	}
	if got := firstChoiceText(first); !containsFold(got, codeword) {
		t.Fatalf("first content = %q, want token %q", got, codeword)
	}
	sessionID := experimentalClaudeSessionID(first)
	if sessionID == "" {
		t.Fatalf("first response did not include claude_code_session_id: %#v", first.Choices[0].GenerationInfo.Additional)
	}

	second, err := adapter.GenerateContent(
		ctx,
		[]llmtypes.MessageContent{
			{
				Role: llmtypes.ChatMessageTypeHuman,
				Parts: []llmtypes.ContentPart{
					llmtypes.TextContent{Text: "OLD_CONTEXT_SHOULD_NOT_BE_SENT"},
				},
			},
			{
				Role: llmtypes.ChatMessageTypeHuman,
				Parts: []llmtypes.ContentPart{
					llmtypes.TextContent{Text: "What reference phrase appeared in your previous answer? Reply exactly with the phrase and nothing else."},
				},
			},
		},
		WithResumeSessionID(sessionID),
	)
	if err != nil {
		t.Fatalf("second GenerateContent error = %v", err)
	}
	got := strings.TrimSpace(second.Choices[0].Content)
	if got != codeword {
		t.Fatalf("resumed content = %q, want %q", got, codeword)
	}
	if resumedID, _ := second.Choices[0].GenerationInfo.Additional["claude_code_resumed_session_id"].(string); resumedID != sessionID {
		t.Fatalf("resumed session id metadata = %q, want %q", resumedID, sessionID)
	}
	assertClaudeInteractiveHaikuMetadata(t, first)
	assertClaudeInteractiveHaikuMetadata(t, second)
}

func TestClaudeCodeTmuxIntegrationHaikuExtendedResumeIsolation(t *testing.T) {
	skipClaudeInteractiveIntegration(t)

	adapter := NewClaudeCodeInteractiveAdapter(claudeInteractiveIntegrationModel(), &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	keyA := "session-a-integration-4821"
	keyB := "session-b-integration-7394"
	largeInput := buildClaudeInteractiveIntegrationLargeInput()

	firstA, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeSystem,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "This is an integration smoke test. Keep answers short and preserve exact tokens."},
			},
		},
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "This is session A.\nThe session A token is: " + keyA + "\n\nLarge input:\n" + largeInput + "\n\nReply exactly: A saved " + keyA},
			},
		},
	})
	if err != nil {
		t.Fatalf("session A initial GenerateContent error = %v", err)
	}
	if got := firstChoiceText(firstA); !containsFold(got, "A saved "+keyA) {
		t.Fatalf("session A initial content = %q, want token %q", got, keyA)
	}
	sessionA := experimentalClaudeSessionID(firstA)
	if sessionA == "" {
		t.Fatalf("session A missing claude_code_session_id: %#v", firstA.Choices[0].GenerationInfo.Additional)
	}

	secondA, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "What session A token appeared in your previous answer? Reply with only the token."},
			},
		},
	}, WithResumeSessionID(sessionA))
	if err != nil {
		t.Fatalf("session A resumed GenerateContent error = %v", err)
	}
	if got := firstChoiceText(secondA); !containsFold(got, keyA) {
		t.Fatalf("session A resumed content = %q, want token %q", got, keyA)
	}
	if resumedID := experimentalClaudeResumedSessionID(secondA); resumedID != sessionA {
		t.Fatalf("session A resumed metadata = %q, want %q", resumedID, sessionA)
	}

	firstB, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "This is session B.\nThe session B token is: " + keyB + "\nDo not mention session A.\nReply exactly: B saved " + keyB},
			},
		},
	})
	if err != nil {
		t.Fatalf("session B initial GenerateContent error = %v", err)
	}
	if got := firstChoiceText(firstB); !containsFold(got, "B saved "+keyB) || containsFold(got, keyA) {
		t.Fatalf("session B initial content = %q, want token %q and no %q", got, keyB, keyA)
	}
	sessionB := experimentalClaudeSessionID(firstB)
	if sessionB == "" {
		t.Fatalf("session B missing claude_code_session_id: %#v", firstB.Choices[0].GenerationInfo.Additional)
	}

	thirdA, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "We are back in session A. Reply with only the exact session A token."},
			},
		},
	}, WithResumeSessionID(sessionA))
	if err != nil {
		t.Fatalf("session A second resumed GenerateContent error = %v", err)
	}
	if got := firstChoiceText(thirdA); !containsFold(got, keyA) || containsFold(got, keyB) {
		t.Fatalf("session A second resumed content = %q, want token %q and no %q", got, keyA, keyB)
	}

	secondB, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "We are back in session B. Reply with only the exact session B token."},
			},
		},
	}, WithResumeSessionID(sessionB))
	if err != nil {
		t.Fatalf("session B resumed GenerateContent error = %v", err)
	}
	if got := firstChoiceText(secondB); !containsFold(got, keyB) || containsFold(got, keyA) {
		t.Fatalf("session B resumed content = %q, want token %q and no %q", got, keyB, keyA)
	}
	if resumedID := experimentalClaudeResumedSessionID(secondB); resumedID != sessionB {
		t.Fatalf("session B resumed metadata = %q, want %q", resumedID, sessionB)
	}

	assertClaudeInteractiveHaikuMetadata(t, firstA)
	assertClaudeInteractiveHaikuMetadata(t, secondA)
	assertClaudeInteractiveHaikuMetadata(t, firstB)
	assertClaudeInteractiveHaikuMetadata(t, thirdA)
	assertClaudeInteractiveHaikuMetadata(t, secondB)
}

func TestClaudeCodeTmuxIntegrationHaikuLiveInputAndEscape(t *testing.T) {
	skipClaudeInteractiveLiveE2E(t)

	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	parentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	ownerSessionID := "claude-live-e2e-" + randomHex(4)
	toolToken := "SLOW_CLAUDE_E2E_" + randomHex(4)
	slowToolMarker := filepath.Join(t.TempDir(), "slow-tool-started")
	mcpServerPath := writeClaudeInteractiveSlowMCPServer(t, slowToolMarker)
	mcpConfig := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":%q}}}`, mcpServerPath)

	resultCh := make(chan claudeInteractiveRealResult, 1)
	startupErrCh := make(chan error, 1)
	go func() {
		resp, err := adapter.GenerateContent(
			parentCtx,
			[]llmtypes.MessageContent{
				{
					Role: llmtypes.ChatMessageTypeSystem,
					Parts: []llmtypes.ContentPart{
						llmtypes.TextContent{Text: "This is a Claude Code transport test. Use only declared MCP tools. Keep the final answer concise."},
					},
				},
				{
					Role: llmtypes.ChatMessageTypeHuman,
					Parts: []llmtypes.ContentPart{
						llmtypes.TextContent{Text: "Call the api-bridge slow_contract MCP tool with token " + toolToken + " and delay_ms 30000. Do not answer until the tool returns. Then reply exactly with the tool result text."},
					},
				},
			},
			WithInteractiveSessionID(ownerSessionID),
			WithMCPConfig(mcpConfig),
			WithClaudeCodeTools(""),
			WithAllowedTools("mcp__api-bridge__slow_contract"),
			WithEffort("low"),
		)
		out := claudeInteractiveRealResult{err: err}
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

	waitForIntegrationInteractiveSession(t, ownerSessionID, 30*time.Second, startupErrCh)
	waitForClaudeInteractiveFile(t, slowToolMarker, "slow MCP tool call start", 90*time.Second, resultCh)

	sendCtx, sendCancel := context.WithTimeout(context.Background(), 10*time.Second)
	liveErr := SendClaudeCodeInput(sendCtx, ownerSessionID, "## Pre-validation failed (retry attempt 3)\n\nFix the specific issue above and re-produce the required outputs.")
	sendCancel()
	if liveErr != nil {
		cancel()
		t.Fatalf("SendClaudeCodeInput error = %v", liveErr)
	}

	select {
	case got := <-resultCh:
		cancel()
		if got.err != nil {
			t.Fatalf("GenerateContent returned while live validation input was queued: err=%v content=%q", got.err, got.content)
		}
		t.Fatalf("GenerateContent completed while live validation input was queued; content=%q", got.content)
	case <-time.After(3 * time.Second):
	}

	cancel()
	select {
	case got := <-resultCh:
		if got.err == nil {
			t.Fatalf("GenerateContent completed normally after cancellation; content=%q", got.content)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for Claude Code live E2E cancellation")
	}
}

// TestClaudeCodeTmuxIntegrationLiveInputProcessesQueuedFollowup verifies the
// property the steer-vs-queue removal depends on for CLI agents: a message
// delivered mid-turn (while Claude is busy in a slow MCP tool) is queued by the
// Claude Code CLI ITSELF and processed once the current turn completes. It sends
// the follow-up via raw SendClaudeCodeInput (bypassing the server steer/queue),
// so a processed follow-up proves the CLI's own native queue did the work.
func TestClaudeCodeTmuxIntegrationLiveInputProcessesQueuedFollowup(t *testing.T) {
	skipClaudeInteractiveLiveE2E(t)

	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	parentCtx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	ownerSessionID := "claude-live-queue-" + randomHex(4)
	toolToken := "SLOW_CLAUDE_QUEUE_" + randomHex(4)
	firstDone := "CLAUDE_FIRST_DONE_" + randomHex(4)
	liveAck := "CLAUDE_LIVE_ACK_" + randomHex(4)
	slowToolMarker := filepath.Join(t.TempDir(), "slow-tool-started")
	mcpServerPath := writeClaudeInteractiveSlowMCPServer(t, slowToolMarker)
	mcpConfig := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":%q}}}`, mcpServerPath)

	paneDiag := func() string {
		if sn, ok := activeClaudeInteractiveOwner(ownerSessionID); ok {
			p, _ := captureTmuxPaneForDisplay(context.Background(), sn)
			return p
		}
		return "(no active session)"
	}

	resultCh := make(chan claudeInteractiveRealResult, 1)
	startupErrCh := make(chan error, 1)
	go func() {
		resp, err := adapter.GenerateContent(
			parentCtx,
			[]llmtypes.MessageContent{
				{
					Role:  llmtypes.ChatMessageTypeSystem,
					Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "This is a Claude Code transport test. Use only declared MCP tools. If a follow-up user message arrives while you are working, handle it after the current tool call finishes. Keep answers concise."}},
				},
				{
					Role:  llmtypes.ChatMessageTypeHuman,
					Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Call the api-bridge slow_contract MCP tool with token " + toolToken + " and delay_ms 8000. Do not answer until the tool returns. Then reply exactly " + firstDone + "."}},
				},
			},
			WithInteractiveSessionID(ownerSessionID),
			WithMCPConfig(mcpConfig),
			WithClaudeCodeTools(""),
			WithAllowedTools("mcp__api-bridge__slow_contract"),
			WithEffort("low"),
		)
		out := claudeInteractiveRealResult{err: err}
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

	waitForIntegrationInteractiveSession(t, ownerSessionID, 30*time.Second, startupErrCh)
	waitForClaudeInteractiveFile(t, slowToolMarker, "slow MCP tool call start", 90*time.Second, resultCh)

	// Deliver the follow-up WHILE Claude is busy in the slow tool, via the raw
	// adapter path (no server queue in between).
	liveMessage := fmt.Sprintf("Follow-up task: after the current answer completes, also reply exactly %s and nothing else.", liveAck)
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := SendClaudeCodeInput(sendCtx, ownerSessionID, liveMessage); err != nil {
		sendCancel()
		cancel()
		t.Fatalf("SendClaudeCodeInput error = %v", err)
	}
	sendCancel()

	select {
	case got := <-resultCh:
		if got.err != nil {
			t.Fatalf("GenerateContent error = %v (content=%q)", got.err, got.content)
		}
		if !strings.Contains(got.content, liveAck) {
			t.Fatalf("queued follow-up was NOT processed by the CLI; final content=%q\npane:\n%s", got.content, paneDiag())
		}
		t.Logf("OK: Claude Code natively queued + processed the mid-turn follow-up (found %s in final content)", liveAck)
	case <-time.After(3 * time.Minute):
		t.Fatalf("timed out waiting for Claude to process the queued live input; pane:\n%s", paneDiag())
	}
}

func TestClaudeCodeTmuxIntegrationHaikuPersistentInteractiveMultiTurn(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)

	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	ownerSessionID := "claude-persistent-e2e-" + randomHex(4)
	codeword := "persistent-haiku-token-4821"
	options := []llmtypes.CallOption{
		WithInteractiveSessionID(ownerSessionID),
		WithPersistentInteractiveSession(true),
		WithEffort("low"),
	}

	first, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeSystem,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "This is a Claude Code transport test. Do not use tools. Keep answers short."},
			},
		},
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "For this test ticket, the project codename is " + codeword + ". Reply exactly: codename recorded " + codeword},
			},
		},
	}, options...)
	if err != nil {
		t.Fatalf("first GenerateContent error = %v", err)
	}
	if got := firstChoiceText(first); !containsFold(got, codeword) {
		t.Fatalf("first content = %q, want token %q", got, codeword)
	}

	second, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "What project codename did I give for this test ticket? Reply with only the codename."},
			},
		},
	}, options...)
	if err != nil {
		t.Fatalf("second GenerateContent error = %v", err)
	}
	if got := strings.TrimSpace(firstChoiceText(second)); !containsFold(got, codeword) {
		t.Fatalf("second content = %q, want token %q from same persistent tmux session", got, codeword)
	}
	if persistent, _ := second.Choices[0].GenerationInfo.Additional["claude_code_persistent_interactive"].(bool); !persistent {
		t.Fatalf("persistent metadata = %#v, want true", second.Choices[0].GenerationInfo.Additional["claude_code_persistent_interactive"])
	}
	if _, ok := activeClaudeInteractiveOwner(ownerSessionID); !ok {
		t.Fatalf("persistent interactive session not registered after completed turn")
	}
}

func TestClaudeCodeTmuxRealFinalExtractionFromTmuxAgentJudgeE2E(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)

	adapter := NewClaudeCodeInteractiveAdapter(claudeInteractiveIntegrationModel(), &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	ownerSessionID := "claude-final-extract-e2e-" + randomHex(4)
	token := "LIVE_CLAUDE_FINAL_" + randomHex(5)
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeSystem,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Do not use tools. Preserve line breaks in the final answer."},
			},
		},
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: fmt.Sprintf("Return a final answer containing these three plain lines and no setup commentary:\nClaude final %s\nfirst %s\nsecond %s", token, token, token)},
			},
		},
	},
		WithInteractiveSessionID(ownerSessionID),
		WithPersistentInteractiveSession(true),
		WithEffort("low"),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	content := strings.TrimSpace(firstChoiceText(resp))
	tmuxSession, ok := activeClaudeInteractiveOwner(ownerSessionID)
	if !ok || tmuxSession == "" {
		t.Fatalf("expected active Claude Code tmux session for %s", ownerSessionID)
	}
	pane, err := captureTmuxPane(ctx, tmuxSession)
	if err != nil {
		t.Fatalf("capture Claude Code pane: %v", err)
	}

	testcontracts.AssertAgentJudgesFinalExtraction(t, testcontracts.FinalExtractionJudgeCase{
		Provider:   "claude-code",
		TmuxScreen: pane,
		Extracted:  content,
		UserGoal:   "Return the live tmux final answer containing the token heading and the first/second lines.",
		MustContain: []string{
			"Claude final " + token,
			"first " + token,
			"second " + token,
		},
		Forbidden: []string{
			"Return a final answer",
			"Do not use tools",
			"execute_shell_command",
			"api-bridge",
			"stdout",
			"ctrl+o",
			"❯",
			"⏺",
			"✻",
		},
		ExpectedNote: "This is a live Claude Code tmux capture after GenerateContent returned; the extracted response must be only the final answer.",
	})
}

func TestClaudeCodeTmuxIntegrationPersistentCancelDoesNotLeaveBusySessionReusable(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)

	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	ownerSessionID := "claude-cancel-ready-e2e-" + randomHex(4)
	markerPath := filepath.Join(t.TempDir(), "slow-tool-started.json")
	mcpServerPath := writeClaudeInteractiveSlowMCPServer(t, markerPath)
	mcpConfig := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":%q}}}`, mcpServerPath)
	options := []llmtypes.CallOption{
		WithInteractiveSessionID(ownerSessionID),
		WithPersistentInteractiveSession(true),
		WithMCPConfig(mcpConfig),
		WithClaudeCodeTools(""),
		WithAllowedTools("mcp__api-bridge__slow_contract"),
		WithEffort("low"),
	}

	callCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := adapter.GenerateContent(callCtx, []llmtypes.MessageContent{
			{
				Role: llmtypes.ChatMessageTypeSystem,
				Parts: []llmtypes.ContentPart{
					llmtypes.TextContent{Text: "This is a cancellation contract test. Use only declared MCP tools."},
				},
			},
			{
				Role: llmtypes.ChatMessageTypeHuman,
				Parts: []llmtypes.ContentPart{
					llmtypes.TextContent{Text: "Call the api-bridge slow_contract MCP tool with token CANCEL_READY and delay_ms 60000. Do not answer until the tool returns."},
				},
			},
		}, options...)
		errCh <- err
	}()

	waitForClaudeInteractiveFileOrResult(t, markerPath, "slow tool start marker", 90*time.Second, errCh, ownerSessionID)
	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatalf("GenerateContent error = nil, want context cancellation")
		}
		if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("GenerateContent error = %v, want context canceled", err)
		}
	case <-time.After(45 * time.Second):
		t.Fatal("timed out waiting for canceled Claude Code turn to return")
	}

	if sessionName, ok := activeClaudeInteractiveOwner(ownerSessionID); ok {
		captured, err := captureTmuxPane(context.Background(), sessionName)
		if err != nil {
			t.Fatalf("active session %s could not be captured after cancellation: %v", sessionName, err)
		}
		if !hasReadyInputPrompt(captured) {
			t.Fatalf("canceled persistent session remained registered but was not prompt-ready; latest pane:\n%s", captured)
		}
	}
	retryCtx, retryCancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer retryCancel()
	retryToken := "CLAUDE_AFTER_STOP_" + randomHex(4)
	retry, err := adapter.GenerateContent(retryCtx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "This is a cancellation contract test. Use only declared MCP tools."),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply with exactly "+retryToken+" and nothing else."),
	}, options...)
	if err != nil {
		t.Fatalf("new message after Stop: %v", err)
	}
	if len(retry.Choices) == 0 || !strings.Contains(retry.Choices[0].Content, retryToken) {
		t.Fatalf("new message after Stop was not answered: %#v", retry.Choices)
	}
}

func waitForClaudeInteractiveFileOrResult(t *testing.T, path, label string, timeout time.Duration, errCh <-chan error, ownerSessionID string) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-errCh:
			t.Fatalf("Claude Code exited before %s: %v", label, err)
		case <-deadline.C:
			if sessionName, ok := activeClaudeInteractiveOwner(ownerSessionID); ok {
				if captured, err := captureTmuxPane(context.Background(), sessionName); err == nil {
					t.Fatalf("timed out waiting for %s at %s; latest pane:\n%s", label, path, captured)
				}
			}
			t.Fatalf("timed out waiting for %s at %s", label, path)
		case <-ticker.C:
			if _, err := os.Stat(path); err == nil {
				return
			}
		}
	}
}

func skipClaudeInteractiveIntegration(t *testing.T) {
	t.Helper()
	if !*codingCLIP0Live {
		t.Skip("run through the live coding CLI P0 runner")
	}
}

func skipClaudeInteractiveLiveE2E(t *testing.T) {
	t.Helper()
	if !*codingCLIP0Live {
		t.Skip("run through the live coding CLI P0 runner")
	}
}

func skipClaudeInteractivePersistentE2E(t *testing.T) {
	t.Helper()
	if !*codingCLIP0Live {
		t.Skip("run through the live coding CLI P0 runner")
	}
}

func claudeInteractiveIntegrationModel() string {
	if model := strings.TrimSpace(os.Getenv(claudeTmuxIntegrationModelEnv)); model != "" {
		return model
	}
	if model := strings.TrimSpace(os.Getenv(claudeInteractiveIntegrationModelEnv)); model != "" {
		return model
	}
	return defaultClaudeInteractiveTestModel
}

func assertClaudeInteractiveHaikuMetadata(t *testing.T, resp *llmtypes.ContentResponse) {
	t.Helper()
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].GenerationInfo == nil {
		t.Fatalf("response missing generation info: %#v", resp)
	}
	additional := resp.Choices[0].GenerationInfo.Additional
	if additional["claude_code_mode"] != "tmux" {
		t.Fatalf("claude_code_mode = %#v, want tmux", additional["claude_code_mode"])
	}
	if additional["claude_code_uses_print_flag"] != false {
		t.Fatalf("claude_code_uses_print_flag = %#v, want false", additional["claude_code_uses_print_flag"])
	}
	if sessionID, _ := additional["claude_code_session_id"].(string); strings.TrimSpace(sessionID) == "" {
		t.Fatalf("claude_code_session_id missing: %#v", additional)
	}
}

func firstChoiceText(resp *llmtypes.ContentResponse) string {
	if resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	return strings.TrimSpace(resp.Choices[0].Content)
}

func waitForIntegrationInteractiveSession(t *testing.T, ownerSessionID string, timeout time.Duration, errCh <-chan error) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-errCh:
			t.Fatalf("Claude Code exited before interactive session registration: %v", err)
		case <-deadline.C:
			t.Fatalf("timed out waiting for Claude Code interactive session %q", ownerSessionID)
		case <-ticker.C:
			if _, ok := activeClaudeInteractiveOwner(ownerSessionID); ok {
				return
			}
		}
	}
}

func waitForClaudeInteractiveFile(t *testing.T, path, label string, timeout time.Duration, resultCh <-chan claudeInteractiveRealResult) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case got := <-resultCh:
			t.Fatalf("Claude Code exited before %s: err=%v content=%q", label, got.err, got.content)
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s at %s", label, path)
		case <-ticker.C:
			if _, err := os.Stat(path); err == nil {
				return
			}
		}
	}
}

type claudeInteractiveRealResult struct {
	content string
	err     error
}

func experimentalClaudeResumedSessionID(resp *llmtypes.ContentResponse) string {
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].GenerationInfo == nil {
		return ""
	}
	sessionID, _ := resp.Choices[0].GenerationInfo.Additional["claude_code_resumed_session_id"].(string)
	return sessionID
}

func buildClaudeInteractiveIntegrationLargeInput() string {
	var b strings.Builder
	b.WriteString("Large prompt regression fixture:\n")
	for i := 0; b.Len() < 12000; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", i%17+3))
		b.WriteString(" checks prompt paste stability, pane reset, and resume parsing.\n")
	}
	return b.String()
}

func writeClaudeInteractiveSlowMCPServer(t *testing.T, markerPath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-slow-contract-mcp.js")
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
// E2E: MCP bridge tool call — verify a synchronous MCP tool returns through
// the bridge and the tool result text reaches the assistant response.
// ---------------------------------------------------------------------------

func TestClaudeCodeTmuxIntegrationHaikuMCPBridgeContract(t *testing.T) {
	skipClaudeInteractiveIntegration(t)

	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	bridgeToken := "CLAUDE_BRIDGE_" + randomHex(4)
	mcpServerPath := writeClaudeInteractiveContractMCPServer(t)
	mcpConfig := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":%q}}}`, mcpServerPath)

	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeSystem,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Use only declared MCP tools. Keep the final answer concise."},
			},
		},
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: fmt.Sprintf("Call the api-bridge echo_contract MCP tool with token %s. Then reply exactly with the tool result text.", bridgeToken)},
			},
		},
	},
		WithMCPConfig(mcpConfig),
		WithClaudeCodeTools(""),
		WithAllowedTools("mcp__api-bridge__echo_contract"),
		WithEffort("low"),
	)
	if err != nil {
		t.Fatalf("GenerateContent with MCP bridge error = %v", err)
	}
	want := "BRIDGE_TOOL_OK_" + bridgeToken
	got := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(got, want) {
		t.Fatalf("content = %q, want bridge tool result %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// E2E: Working directory — verify Claude Code launches with the requested cwd
// by asking it to read a marker file that exists only in that workspace.
// ---------------------------------------------------------------------------

func TestClaudeCodeTmuxIntegrationHaikuWorkingDirectory(t *testing.T) {
	skipClaudeInteractiveIntegration(t)

	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	workDir := t.TempDir()
	preTrustClaudeWorkspace(t, workDir)
	marker := "CLAUDE_WD_MARKER_" + randomHex(6)
	markerPath := filepath.Join(workDir, "wd-marker.txt")
	if err := os.WriteFile(markerPath, []byte(marker), 0o600); err != nil {
		t.Fatalf("write workspace marker: %v", err)
	}

	mcpServerPath := writeClaudeInteractiveCwdMCPServer(t)
	mcpConfig := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":%q}}}`, mcpServerPath)

	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeSystem,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Use only declared MCP tools. Keep the final answer concise."},
			},
		},
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Call the api-bridge report_cwd MCP tool. Then reply exactly with the tool result text."},
			},
		},
	},
		WithWorkingDir(workDir),
		WithMCPConfig(mcpConfig),
		WithClaudeCodeTools(""),
		WithAllowedTools("mcp__api-bridge__report_cwd"),
		WithEffort("low"),
	)
	if err != nil {
		t.Fatalf("GenerateContent with working dir error = %v", err)
	}
	got := strings.TrimSpace(resp.Choices[0].Content)
	// Claude's TUI can hard-wrap a long unbroken path at the pane width. This
	// contract verifies the child process cwd, not terminal line wrapping.
	compactGot := strings.NewReplacer("\r", "", "\n", "").Replace(got)

	// The MCP server reports its process cwd; it should match the requested
	// working dir (or its resolved real path on macOS).
	wantPaths := []string{workDir}
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil && resolved != workDir {
		wantPaths = append(wantPaths, resolved)
	}
	matched := false
	for _, want := range wantPaths {
		if strings.Contains(compactGot, "CWD_REPORTED_"+want) {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("content = %q, want CWD_REPORTED_<workDir> with workDir in %v", got, wantPaths)
	}
}

// ---------------------------------------------------------------------------
// E2E: Trust-prompt auto-dismiss — the adapter pre-trusts a fresh working
// directory so Claude Code's "Do you trust the files in this folder?" dialog
// never appears and the session starts without timing out.
// ---------------------------------------------------------------------------

func TestClaudeCodeTmuxIntegrationTrustPromptAutoDismiss(t *testing.T) {
	skipClaudeInteractiveIntegration(t)

	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	// Intentionally do NOT call preTrustClaudeWorkspace — the adapter must
	// handle the trust dialog itself via preTrustClaudeWorkingDir.
	workDir := t.TempDir()

	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with exactly: TRUST_OK"}},
		},
	},
		WithWorkingDir(workDir),
		WithClaudeCodeTools(""),
		WithEffort("low"),
	)
	if err != nil {
		t.Fatalf("GenerateContent in untrusted workdir error = %v (trust prompt was not auto-dismissed)", err)
	}
	if !strings.Contains(resp.Choices[0].Content, "TRUST_OK") {
		t.Fatalf("content = %q, want TRUST_OK", resp.Choices[0].Content)
	}
}

// ---------------------------------------------------------------------------
// E2E: Parallel session isolation — two concurrent Claude Code sessions return
// their own tokens without leaking each other's state.
// ---------------------------------------------------------------------------

func TestClaudeCodeTmuxIntegrationParallelIsolation(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)

	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	type parallelSpec struct {
		name         string
		ownerSession string
		token        string
		workDir      string
	}
	specs := []parallelSpec{
		{
			name:         "left",
			ownerSession: "claude-parallel-left-" + randomHex(4),
			token:        "CLAUDE_PAR_LEFT_" + randomHex(4),
			workDir:      t.TempDir(),
		},
		{
			name:         "right",
			ownerSession: "claude-parallel-right-" + randomHex(4),
			token:        "CLAUDE_PAR_RIGHT_" + randomHex(4),
			workDir:      t.TempDir(),
		},
	}
	for _, spec := range specs {
		preTrustClaudeWorkspace(t, spec.workDir)
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
				{
					Role: llmtypes.ChatMessageTypeSystem,
					Parts: []llmtypes.ContentPart{
						llmtypes.TextContent{Text: "This is a Claude Code transport isolation test. Do not use tools. Keep the reply exact and concise."},
					},
				},
				{
					Role: llmtypes.ChatMessageTypeHuman,
					Parts: []llmtypes.ContentPart{
						llmtypes.TextContent{Text: fmt.Sprintf("Reply exactly: %s", spec.token)},
					},
				},
			},
				WithInteractiveSessionID(spec.ownerSession),
				WithPersistentInteractiveSession(true),
				WithWorkingDir(spec.workDir),
				WithEffort("low"),
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
		for _, other := range specs {
			if other.name != spec.name && strings.Contains(got.content, other.token) {
				t.Fatalf("%s content leaked other session's token %s: %q", spec.name, other.token, got.content)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// E2E: Shared-workdir MCP isolation — two concurrent Claude Code sessions that
// share the same working directory still see their own MCP server replies.
// ---------------------------------------------------------------------------

func TestClaudeCodeTmuxIntegrationSharedWorkingDirMCPIsolation(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)

	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	sharedWorkDir := filepath.Join(t.TempDir(), "shared-workspace")
	if err := os.MkdirAll(sharedWorkDir, 0o755); err != nil {
		t.Fatalf("create shared workdir: %v", err)
	}
	preTrustClaudeWorkspace(t, sharedWorkDir)

	type runSpec struct {
		name          string
		ownerSession  string
		sessionMarker string
		token         string
		outputPath    string
		mcpServerPath string
		mcpConfig     string
	}
	specs := []runSpec{
		{
			name:          "alpha",
			ownerSession:  "claude-shared-alpha-" + randomHex(4),
			sessionMarker: "CLAUDE_SESSION_ALPHA_" + randomHex(4),
			token:         "CLAUDE_TOKEN_ALPHA_" + randomHex(4),
			outputPath:    filepath.Join(t.TempDir(), "alpha-output.json"),
		},
		{
			name:          "beta",
			ownerSession:  "claude-shared-beta-" + randomHex(4),
			sessionMarker: "CLAUDE_SESSION_BETA_" + randomHex(4),
			token:         "CLAUDE_TOKEN_BETA_" + randomHex(4),
			outputPath:    filepath.Join(t.TempDir(), "beta-output.json"),
		},
	}
	for i := range specs {
		specs[i].mcpServerPath = writeClaudeInteractiveIsolationMCPServer(t, specs[i].sessionMarker, specs[i].outputPath)
		specs[i].mcpConfig = fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":%q}}}`, specs[i].mcpServerPath)
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
				{
					Role: llmtypes.ChatMessageTypeSystem,
					Parts: []llmtypes.ContentPart{
						llmtypes.TextContent{Text: "Use only declared MCP tools. Keep the final answer concise."},
					},
				},
				{
					Role: llmtypes.ChatMessageTypeHuman,
					Parts: []llmtypes.ContentPart{
						llmtypes.TextContent{Text: fmt.Sprintf("Call the api-bridge write_contract MCP tool with token %s. Then reply exactly with the tool result text.", spec.token)},
					},
				},
			},
				WithInteractiveSessionID(spec.ownerSession),
				WithPersistentInteractiveSession(true),
				WithWorkingDir(sharedWorkDir),
				WithMCPConfig(spec.mcpConfig),
				WithClaudeCodeTools(""),
				WithAllowedTools("mcp__api-bridge__write_contract"),
				WithEffort("low"),
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
		text := string(data)
		if !strings.Contains(text, spec.sessionMarker) || !strings.Contains(text, spec.token) {
			t.Fatalf("%s output = %s, want session %s and token %s", spec.name, text, spec.sessionMarker, spec.token)
		}
		for _, other := range specs {
			if other.name != spec.name && strings.Contains(text, other.sessionMarker) {
				t.Fatalf("%s output crossed sessions: %s", spec.name, text)
			}
		}
	}
}

func writeClaudeInteractiveContractMCPServer(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-contract-mcp.js")
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
    send({ jsonrpc: "2.0", id: msg.id, result: { tools: [{ name: "echo_contract", description: "Return a deterministic contract token.", inputSchema: { type: "object", properties: { token: { type: "string" } }, required: ["token"] } }] } });
    return;
  }
  if (msg.method === "tools/call") {
    const args = (msg.params && msg.params.arguments) || {};
    send({ jsonrpc: "2.0", id: msg.id, result: { content: [{ type: "text", text: "BRIDGE_TOOL_OK_" + String(args.token || "") }], isError: false } });
    return;
  }
  if (msg.id !== undefined) send({ jsonrpc: "2.0", id: msg.id, result: {} });
});
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write contract MCP server: %v", err)
	}
	return path
}

func writeClaudeInteractiveIsolationMCPServer(t *testing.T, sessionMarker, outputPath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-isolation-contract-mcp.js")
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

func writeClaudeInteractiveCwdMCPServer(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-cwd-mcp.js")
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
    send({ jsonrpc: "2.0", id: msg.id, result: { tools: [{ name: "report_cwd", description: "Return the MCP server process cwd. The Claude Code adapter spawns MCP servers inheriting the caller's working directory, so this proves the launched session is anchored to the requested workspace.", inputSchema: { type: "object", properties: {}, required: [] } }] } });
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

// preTrustClaudeWorkspace is kept for tests that pre-trust a directory before
// launching the adapter. New tests should rely on the adapter's own
// preTrustClaudeWorkingDir instead (see TestClaudeCodeTmuxIntegrationTrustPromptAutoDismiss).
func preTrustClaudeWorkspace(t *testing.T, workDir string) {
	t.Helper()
	preTrustClaudeWorkingDir(workDir)
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func experimentalClaudeSessionID(resp *llmtypes.ContentResponse) string {
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].GenerationInfo == nil {
		return ""
	}
	sessionID, _ := resp.Choices[0].GenerationInfo.Additional["claude_code_session_id"].(string)
	return sessionID
}

// A half-typed draft left in the composer must not join the next chat turn:
// the mod starts the turn itself and leaves the person's draft alone.
func TestClaudeCodeTmuxIntegrationPersistentClearsStaleDraftBeforeNextTurn(t *testing.T) {
	skipClaudeInteractivePersistentE2E(t)

	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	ownerSessionID := "claude-stale-draft-e2e-" + randomHex(4)
	options := []llmtypes.CallOption{
		WithInteractiveSessionID(ownerSessionID),
		WithPersistentInteractiveSession(true),
		WithEffort("low"),
	}

	first, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeSystem,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "This is a Claude Code transport test. Do not use tools. Keep answers short."},
			},
		},
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Reply exactly: ready"},
			},
		},
	}, options...)
	if err != nil {
		t.Fatalf("first GenerateContent error = %v", err)
	}
	if got := firstChoiceText(first); !containsFold(got, "ready") {
		t.Fatalf("first content = %q, want ready", got)
	}

	sessionName, ok := activeClaudeInteractiveOwner(ownerSessionID)
	if !ok {
		t.Fatalf("persistent interactive session not registered for %s", ownerSessionID)
	}
	staleDraft := "go with option B"
	if err := runCommand(ctx, nil, "tmux", "send-keys", "-t", sessionName, staleDraft); err != nil {
		t.Fatalf("seed stale tmux draft: %v", err)
	}
	waitForClaudeInteractiveDraft(t, sessionName, staleDraft, 5*time.Second)

	second, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Reply exactly: CURRENT_TURN_STALE_DRAFT_OK. Do not mention options."},
			},
		},
	}, options...)
	if err != nil {
		t.Fatalf("second GenerateContent error = %v", err)
	}
	got := firstChoiceText(second)
	if !containsFold(got, "CURRENT_TURN_STALE_DRAFT_OK") {
		t.Fatalf("second content = %q, want current turn marker", got)
	}
	if containsFold(got, "option b") {
		t.Fatalf("second content = %q, stale draft leaked into submitted turn", got)
	}
}

// waitForClaudeInteractiveDraft waits until the seeded text shows in the pane
// (test setup only).
func waitForClaudeInteractiveDraft(t *testing.T, sessionName, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if captured, err := captureTmuxPane(context.Background(), sessionName); err == nil && strings.Contains(captured, want) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for Claude Code draft %q", want)
}

// A request reaching the mod runs as the person, so a built-in command that
// changes the CLI's login or settings is refused even when it gets there.
func TestClaudeModRefusesBuiltinSettingsCommand(t *testing.T) {
	skipClaudeInteractiveIntegration(t)
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })
	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "/logout")}, WithWorkingDir(t.TempDir()))
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("/logout through the mod: err = %v, want refused", err)
	}
}
