package codexcli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestCodexCLIRealDurableAckContract is the CertDurableAck P0 proof: a
// follow-up steered into a busy Codex turn must be confirmable against
// the session rollout (not just the pane), and the model must obey it.
// A second, idle follow-up must be durably acked fast. Uses the raw
// adapter path with no server queue in between.
func TestCodexCLIRealDurableAckContract(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	ownerSessionID := "codex-real-durable-ack-" + codexRandomHex(4)
	toolToken := "SLOW_DURABLE_" + codexRandomHex(4)
	liveAck := "CODEX_DURABLE_ACK_" + codexRandomHex(4)
	idleToken := "CODEX_DURABLE_IDLE_" + codexRandomHex(4)

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

	parentCtx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	resultCh := make(chan codexRealResult, 1)
	startupErrCh := make(chan error, 1)
	streamChan := make(chan llmtypes.StreamChunk, 128)

	go func() {
		resp, err := adapter.GenerateContent(parentCtx, []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "This is a Codex CLI transport test. Use only declared MCP tools. If a follow-up user message arrives while you are working, handle it after the current tool call finishes. Keep answers concise."}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: fmt.Sprintf("Call the api-bridge slow_contract MCP tool with token %s and delay_ms 8000. Do not answer until the tool returns.", toolToken)}}},
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

	waitForCodexRealActiveSession(t, ownerSessionID, 45*time.Second, startupErrCh)
	waitForCodexRealFile(t, slowToolMarker, "slow MCP tool call start", 90*time.Second, resultCh)

	// Deliver the follow-up WHILE Codex is busy in the slow tool.
	liveMessage := fmt.Sprintf("New highest-priority instruction: after the current tool returns, reply exactly %s.", liveAck)
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 30*time.Second)
	sendErr := SendCodexInteractiveInput(sendCtx, ownerSessionID, liveMessage)
	sendCancel()
	if sendErr != nil {
		cancel()
		t.Fatalf("SendCodexInteractiveInput error = %v", sendErr)
	}

	// Durability half of the receipt: the rollout must record this exact
	// send. The pane already accepted it (fast ack above); the file
	// proves the CLI durably holds it even across a pane misread.
	awaitCtx, awaitCancel := context.WithTimeout(context.Background(), 90*time.Second)
	ack, awaitErr := AwaitCodexInputDurable(awaitCtx, ownerSessionID, liveMessage, 60*time.Second)
	awaitCancel()
	if awaitErr != nil {
		cancel()
		t.Fatalf("AwaitCodexInputDurable error = %v\npane:\n%s", awaitErr, paneDiag())
	}
	if ack.Outcome != CodexDurableAckConfirmed {
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
			t.Fatalf("live steer did not affect the busy Codex turn; final content=%q\npane:\n%s", got.content, paneDiag())
		}
	case <-parentCtx.Done():
		t.Fatalf("timed out waiting for the busy turn: %v", parentCtx.Err())
	}

	// Idle follow-up: paste into the settled composer and prove the
	// durable record confirms it fast — this is the single-tick to
	// double-tick path with no queue in between.
	idleMessage := fmt.Sprintf("Do not use tools. Reply exactly %s.", idleToken)
	idleSendCtx, idleSendCancel := context.WithTimeout(context.Background(), 30*time.Second)
	idleSendErr := SendCodexInteractiveInput(idleSendCtx, ownerSessionID, idleMessage)
	idleSendCancel()
	if idleSendErr != nil {
		t.Fatalf("idle SendCodexInteractiveInput error = %v", idleSendErr)
	}
	idleAwaitCtx, idleAwaitCancel := context.WithTimeout(context.Background(), 90*time.Second)
	idleAck, idleAwaitErr := AwaitCodexInputDurable(idleAwaitCtx, ownerSessionID, idleMessage, 60*time.Second)
	idleAwaitCancel()
	if idleAwaitErr != nil {
		t.Fatalf("idle AwaitCodexInputDurable error = %v\npane:\n%s", idleAwaitErr, paneDiag())
	}
	if idleAck.Outcome != CodexDurableAckConfirmed {
		t.Fatalf("idle durable ack outcome = %q, want confirmed\npane:\n%s", idleAck.Outcome, paneDiag())
	}
	if idleAck.Latency > 45*time.Second {
		t.Fatalf("idle durable ack latency = %s, want well under the budget\npane:\n%s", idleAck.Latency, paneDiag())
	}
	t.Logf("idle follow-up durably acked in %s", idleAck.Latency.Round(100*time.Millisecond))
}
