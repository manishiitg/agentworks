package claudecode

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestClaudeCodeRealDurableAckContract is the CertDurableAck P0 proof: a
// follow-up steered into a busy Claude turn must be confirmable against
// the session transcript (not just the pane), and the model must obey it.
// A second, idle follow-up must be durably acked fast. Uses the raw
// adapter path with no server queue in between.
func TestClaudeCodeRealDurableAckContract(t *testing.T) {
	skipClaudeInteractiveLiveE2E(t)
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
	ownerSessionID := "claude-real-durable-ack-" + randomHex(4)
	toolToken := "SLOW_DURABLE_" + randomHex(4)
	liveAck := "CLAUDE_DURABLE_ACK_" + randomHex(4)
	idleToken := "CLAUDE_DURABLE_IDLE_" + randomHex(4)

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

	parentCtx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	resultCh := make(chan claudeInteractiveRealResult, 1)
	startupErrCh := make(chan error, 1)

	go func() {
		resp, err := adapter.GenerateContent(parentCtx, []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "This is a Claude Code transport test. Use only declared MCP tools. If a follow-up user message arrives while you are working, handle it after the current tool call finishes. Keep answers concise."}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: fmt.Sprintf("Call the api-bridge slow_contract MCP tool with token %s and delay_ms 8000. Do not answer until the tool returns.", toolToken)}}},
		},
			WithInteractiveSessionID(ownerSessionID),
			WithPersistentInteractiveSession(true),
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

	waitForIntegrationInteractiveSession(t, ownerSessionID, 45*time.Second, startupErrCh)
	waitForClaudeInteractiveFile(t, slowToolMarker, "slow MCP tool call start", 90*time.Second, resultCh)

	// Deliver the follow-up WHILE Claude is busy in the slow tool.
	// Phrased as a benign follow-up task (proven to survive the
	// system-reminder framing tool-time steers arrive in — an
	// instruction-styled steer was rejected by the model as
	// injection-like even though delivery was confirmed).
	liveMessage := fmt.Sprintf("Follow-up task: after the current answer completes, also reply exactly %s and nothing else.", liveAck)
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 30*time.Second)
	sendErr := SendClaudeCodeInput(sendCtx, ownerSessionID, liveMessage)
	sendCancel()
	if sendErr != nil {
		cancel()
		t.Fatalf("SendClaudeCodeInput error = %v", sendErr)
	}

	// Durability half of the receipt: the transcript must record this exact
	// send. The pane already accepted it (fast ack above); the file proves
	// the CLI durably holds it even across a pane misread.
	awaitCtx, awaitCancel := context.WithTimeout(context.Background(), 90*time.Second)
	ack, awaitErr := AwaitClaudeInputDurable(awaitCtx, ownerSessionID, liveMessage, 60*time.Second)
	awaitCancel()
	if awaitErr != nil {
		cancel()
		t.Fatalf("AwaitClaudeInputDurable error = %v\npane:\n%s", awaitErr, paneDiag())
	}
	if ack.Outcome != ClaudeDurableAckConfirmed {
		cancel()
		t.Fatalf("durable ack outcome = %q, want confirmed (latency %s)\n pane:\n%s", ack.Outcome, ack.Latency, paneDiag())
	}
	t.Logf("busy steer durably acked in %s via %s", ack.Latency.Round(100*time.Millisecond), ack.ProofPath)

	select {
	case got := <-resultCh:
		if got.err != nil {
			t.Fatalf("GenerateContent error = %v (content=%q)", got.err, got.content)
		}
		if !strings.Contains(got.content, liveAck) {
			t.Fatalf("live steer did not affect the busy Claude turn; final content=%q\npane:\n%s", got.content, paneDiag())
		}
	case <-parentCtx.Done():
		t.Fatalf("timed out waiting for the busy turn: %v", parentCtx.Err())
	}

	// Idle follow-up: paste into the settled composer and prove the
	// durable record confirms it fast — this is the single-tick to
	// double-tick path with no queue in between.
	idleMessage := fmt.Sprintf("Do not use tools. Reply exactly %s.", idleToken)
	idleSendCtx, idleSendCancel := context.WithTimeout(context.Background(), 30*time.Second)
	idleSendErr := SendClaudeCodeInput(idleSendCtx, ownerSessionID, idleMessage)
	idleSendCancel()
	if idleSendErr != nil {
		t.Fatalf("idle SendClaudeCodeInput error = %v", idleSendErr)
	}
	idleAwaitCtx, idleAwaitCancel := context.WithTimeout(context.Background(), 90*time.Second)
	idleAck, idleAwaitErr := AwaitClaudeInputDurable(idleAwaitCtx, ownerSessionID, idleMessage, 60*time.Second)
	idleAwaitCancel()
	if idleAwaitErr != nil {
		t.Fatalf("idle AwaitClaudeInputDurable error = %v\npane:\n%s", idleAwaitErr, paneDiag())
	}
	if idleAck.Outcome != ClaudeDurableAckConfirmed {
		t.Fatalf("idle durable ack outcome = %q, want confirmed\npane:\n%s", idleAck.Outcome, paneDiag())
	}
	if idleAck.Latency > 45*time.Second {
		t.Fatalf("idle durable ack latency = %s, want well under the budget\npane:\n%s", idleAck.Latency, paneDiag())
	}
	t.Logf("idle follow-up durably acked in %s", idleAck.Latency.Round(100*time.Millisecond))
}
