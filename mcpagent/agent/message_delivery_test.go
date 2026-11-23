package mcpagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestDeliverUserMessageQueuesForNonCodingProvider(t *testing.T) {
	agent := &Agent{provider: llm.ProviderOpenAI, modelID: "gpt-5"}
	agent.setTurnInFlight(true) // the running turn drains the queue at its next boundary

	result, err := agent.deliverUserMessage(context.Background(), UserMessageDeliveryRequest{
		SessionID: "session-1",
		Message:   "remember this",
		Intent:    UserMessageDeliveryIntentAuto,
	})
	if err != nil {
		t.Fatalf("DeliverUserMessage() error = %v", err)
	}
	if result.DeliveryStatus != UserMessageDeliveryStatusQueuedForInjection {
		t.Fatalf("status = %q, want %q", result.DeliveryStatus, UserMessageDeliveryStatusQueuedForInjection)
	}
	got := agent.drainSteerMessages()
	if len(got) != 1 || got[0].Text != "remember this" {
		t.Fatalf("queued messages = %#v", got)
	}
}

func TestDeliverUserMessageRejectsEmptyMessage(t *testing.T) {
	agent := &Agent{provider: llm.ProviderOpenAI, modelID: "gpt-5"}
	_, err := agent.deliverUserMessage(context.Background(), UserMessageDeliveryRequest{
		SessionID: "session-1",
		Message:   " ",
		Intent:    UserMessageDeliveryIntentAuto,
	})
	if err == nil {
		t.Fatal("expected empty message error")
	}
	var deliveryErr *CodingAgentDeliveryError
	if !errors.As(err, &deliveryErr) {
		t.Fatalf("expected CodingAgentDeliveryError, got %T: %v", err, err)
	}
	if deliveryErr.Kind != DeliveryErrorKindEmptyMessage {
		t.Fatalf("error kind = %q, want %q", deliveryErr.Kind, DeliveryErrorKindEmptyMessage)
	}
}

func TestDeliverUserMessageQueuesWhenTurnInFlightButNoInteractiveSession(t *testing.T) {
	// API-continuation turn (tmux contract, no pooled TUI): the interactive
	// send misses the adapter pool before any I/O. With a turn running its
	// conversation loop, the message must queue for the next LLM-call
	// boundary instead of failing a send no CLI ever saw.
	agent := &Agent{provider: llm.ProviderMuseCLI, modelID: "muse-spark-1.3-contributor"}
	agent.setTurnInFlight(true)
	result, err := agent.deliverUserMessage(context.Background(), UserMessageDeliveryRequest{
		SessionID: "muse-api-turn-no-tui",
		Message:   "steer while API turn runs",
		Intent:    UserMessageDeliveryIntentLiveInput,
	})
	if err != nil {
		t.Fatalf("deliverUserMessage() error = %v, want queue fallback", err)
	}
	if result.DeliveryStatus != UserMessageDeliveryStatusQueuedForInjection {
		t.Fatalf("status = %q, want %q", result.DeliveryStatus, UserMessageDeliveryStatusQueuedForInjection)
	}
	got := agent.drainSteerMessages()
	if len(got) != 1 || got[0].Text != "steer while API turn runs" {
		t.Fatalf("queued messages = %#v", got)
	}
}

func TestDeliverUserMessageBreaksStuckQueueToNewTurn(t *testing.T) {
	// A previous queue that never drained proves its turn is hung or died
	// without cleanup (pool miss already proved its CLI target is gone).
	// Parking another message would strand the chat with a silent accept,
	// so delivery resets the stale flag and reports no-target: the caller
	// starts a fresh turn instead.
	agent := &Agent{provider: llm.ProviderMuseCLI, modelID: "muse-spark-1.3-contributor"}
	agent.setTurnInFlight(true)
	agent.addSteerMessage("first, never drained")
	agent.steerMu.Lock()
	agent.pendingSteerEnqueuedAt[0] = time.Now().Add(-stuckSteerQueueAge - time.Minute)
	agent.steerMu.Unlock()
	_, err := agent.deliverUserMessage(context.Background(), UserMessageDeliveryRequest{
		SessionID: "muse-stuck-queue",
		Message:   "second message",
		Intent:    UserMessageDeliveryIntentLiveInput,
	})
	if err == nil {
		t.Fatal("expected no-target error when the steer queue is proven orphaned")
	}
	var deliveryErr *CodingAgentDeliveryError
	if !errors.As(err, &deliveryErr) || deliveryErr.Kind != DeliveryErrorKindNoSession || len(deliveryErr.UnsentMessages) != 1 || deliveryErr.UnsentMessages[0] != "first, never drained" {
		t.Fatalf("expected definite refusal with recovered queue, got %#v", err)
	}
	if agent.isTurnInFlight() {
		t.Fatal("turn flag still set after stuck-queue break")
	}
	if got := agent.drainSteerMessages(); len(got) != 0 {
		t.Fatalf("queued messages = %#v, want queue transferred to NoSession recovery", got)
	}
}

func TestDeliverUserMessageStillErrorsWithoutTurnInFlight(t *testing.T) {
	// No running turn means nothing drains the steer queue, so a pool miss
	// must keep erroring (the backend starts a new turn instead of stalling
	// an accepted message). This also pins the fixture onto the interactive
	// branch: only it can produce the pool-miss error.
	agent := &Agent{provider: llm.ProviderMuseCLI, modelID: "muse-spark-1.3-contributor"}
	_, err := agent.deliverUserMessage(context.Background(), UserMessageDeliveryRequest{
		SessionID: "muse-idle-no-tui",
		Message:   "nobody drains",
		Intent:    UserMessageDeliveryIntentLiveInput,
	})
	if err == nil {
		t.Fatal("expected not-registered error when no turn drains the steer queue")
	}
	if !strings.Contains(err.Error(), "interactive session registered") {
		t.Fatalf("error = %v, want pool-miss error", err)
	}
	if got := agent.drainSteerMessages(); len(got) != 0 {
		t.Fatalf("queued messages = %#v, want none", got)
	}
}

func TestDeliverUserMessageReportsActualStructuredTransport(t *testing.T) {
	agent := &Agent{
		provider:             llm.ProviderCodexCLI,
		modelID:              "gpt-5.6-sol",
		codingAgentTransport: llm.CodingAgentTransportStructured,
	}
	agent.setTurnInFlight(true)
	result, err := agent.deliverUserMessage(context.Background(), UserMessageDeliveryRequest{
		SessionID: "structured-session",
		Message:   "continue",
	})
	if err != nil {
		t.Fatalf("deliverUserMessage() error = %v", err)
	}
	if result.Transport != llm.CodingAgentTransportStructured {
		t.Fatalf("transport = %q, want structured", result.Transport)
	}
	if result.DeliveryStatus != UserMessageDeliveryStatusQueuedForInjection {
		t.Fatalf("status = %q, want queued_for_injection", result.DeliveryStatus)
	}
}

// With no turn running nothing drains the steer queue, so a structured or API
// agent refuses the message and the caller starts a new turn with it, instead
// of accepting it into a queue no turn will read (RTS 2026-09-25).
func TestDeliverUserMessageRefusesQueueWhenNoTurnIsRunning(t *testing.T) {
	for _, agent := range []*Agent{
		{provider: llm.ProviderOpenAI, modelID: "gpt-5"},
		{provider: llm.ProviderCodexCLI, modelID: "gpt-5.6-sol"},
		{provider: llm.ProviderPiCLI, modelID: "google/gemini-3.8-flash"},
		{provider: llm.ProviderCursorCLI, modelID: "composer-2.5"},
		{provider: llm.ProviderClaudeCode, modelID: "claude-sonnet-5", codingAgentTransport: llm.CodingAgentTransportStructured},
	} {
		_, err := agent.deliverUserMessage(context.Background(), UserMessageDeliveryRequest{SessionID: "idle", Message: "see the new ticket"})
		var deliveryErr *CodingAgentDeliveryError
		if !errors.As(err, &deliveryErr) || deliveryErr.Kind != DeliveryErrorKindNoSession {
			t.Fatalf("%s: error = %v, want %s", agent.provider, err, DeliveryErrorKindNoSession)
		}
		if got := agent.drainSteerMessages(); len(got) != 0 {
			t.Fatalf("%s: message stranded in the steer queue: %#v", agent.provider, got)
		}
	}
}

// PLAT-872-24: a native turn can finish before its outer SDK turn. Its
// pre-write rejection must queue once at the outer boundary, never become 409.
func TestNativeTurnNotStartedQueuesAtActiveOuterBoundary(t *testing.T) {
	t.Setenv("CLAUDE_CODE_TRANSPORT", "sdk")
	defer func(wait time.Duration) { nativeSteerStartWait = wait }(nativeSteerStartWait)
	nativeSteerStartWait = 0 // the provider turn already ended; no start window to wait out
	for _, provider := range []llm.Provider{llm.ProviderCodexCLI, llm.ProviderPiCLI, llm.ProviderClaudeCode} {
		a := &Agent{provider: provider, modelID: "test"}
		a.setTurnInFlight(true)
		result, err := a.deliverUserMessage(context.Background(), UserMessageDeliveryRequest{SessionID: "native-no-process", Message: "follow up", Intent: UserMessageDeliveryIntentLiveInput})
		if err != nil || result.DeliveryStatus != UserMessageDeliveryStatusQueuedForInjection {
			t.Fatalf("%s: result=%+v err=%v", provider, result, err)
		}
		got := a.drainSteerMessages()
		if len(got) != 1 || got[0].Text != "follow up" {
			t.Fatalf("%s: queued=%v", provider, got)
		}
	}
}

// PLAT-872 review: sends between the final drain and Run cleanup used to be
// acknowledged into a queue with nobody left to consume it.
func TestDeliverRejectsQueueAfterFinalDrain(t *testing.T) {
	a := &Agent{provider: llm.ProviderCursorCLI, modelID: "composer-2.5"}
	a.setTurnInFlight(true)
	if pending := a.drainFinalSteerMessages(); len(pending) != 0 {
		t.Fatal(pending)
	}
	_, err := a.deliverUserMessage(context.Background(), UserMessageDeliveryRequest{SessionID: "owner", Message: "late input"})
	var deliveryErr *CodingAgentDeliveryError
	if !errors.As(err, &deliveryErr) || deliveryErr.Kind != DeliveryErrorKindNoSession {
		t.Fatalf("late input was accepted: %v", err)
	}
	a.setTurnInFlight(true)
	if !a.addSteerMessage("unfinished input") {
		t.Fatal("new turn queue stayed closed")
	}
	if pending := a.closeSteerQueue(); len(pending) != 1 || pending[0].Text != "unfinished input" {
		t.Fatalf("stopped turn lost its undelivered input: %#v", pending)
	}
	a.setTurnInFlight(true)
	if pending := a.drainSteerMessages(); len(pending) != 0 {
		t.Fatal("cancelled input leaked to a later turn")
	}
}

// Server B 2026-10-27: a message sent 2s into a Cursor SDK turn was refused while the SDK turn was starting and
// parked in the steer queue, so the agent saw it only after its first answer. A native transport keeps offering
// the message during that window and steers it into the running turn; nothing waits in the queue.
func TestNativeSteerDuringTurnStartIsDeliveredNotQueued(t *testing.T) {
	t.Setenv("CURSOR_CLI_TRANSPORT", "sdk")
	defer func(send func(context.Context, llm.Provider, string, string, string) error, interval time.Duration) {
		sendCodingAgentLiveInput, nativeSteerRetryInterval = send, interval
	}(sendCodingAgentLiveInput, nativeSteerRetryInterval)
	nativeSteerRetryInterval = time.Millisecond
	attempts := 0
	sendCodingAgentLiveInput = func(context.Context, llm.Provider, string, string, string) error {
		if attempts++; attempts < 3 {
			return fmt.Errorf("%w: no active Cursor SDK turn", llmtypes.ErrCodingAgentInputNotSent)
		}
		return nil
	}
	a := &Agent{provider: llm.ProviderCursorCLI, modelID: "composer-2.5"}
	a.setTurnInFlight(true)
	result, err := a.deliverUserMessage(context.Background(), UserMessageDeliveryRequest{SessionID: "starting", Message: "is pr ready?", Intent: UserMessageDeliveryIntentLiveInput})
	if err != nil || result.DeliveryStatus != UserMessageDeliveryStatusSentToCLI || attempts != 3 {
		t.Fatalf("result=%+v err=%v attempts=%d, want steered on the third offer", result, err, attempts)
	}
	if got := a.drainSteerMessages(); len(got) != 0 {
		t.Fatalf("message parked in the steer queue: %#v", got)
	}
}

// PLAT-746-1000: an image whose native steer was not taken must remain in the
// host's durable attachment lane, never be silently accepted as text-only.
func TestNativeImageDeliveryCannotFallIntoTextOnlySteerQueue(t *testing.T) {
	agent := &Agent{provider: llm.ProviderMuseCLI, modelID: "muse-spark-1.3-contributor"}
	agent.setTurnInFlight(true)
	ctx := llmtypes.WithNativeImageInputs(context.Background(), "image-chat", []llmtypes.NativeImageInput{{ID: "admitted"}})
	_, err := agent.deliverUserMessage(ctx, UserMessageDeliveryRequest{SessionID: "image-chat", Message: "Inspect attached image", Intent: UserMessageDeliveryIntentLiveInput})
	var deliveryErr *CodingAgentDeliveryError
	if !errors.As(err, &deliveryErr) || deliveryErr.Kind != DeliveryErrorKindQueueRequired {
		t.Fatalf("expected host queue, got %v", err)
	}
	if len(agent.drainSteerMessages()) != 0 {
		t.Fatal("attachment was accepted as text only")
	}
}
