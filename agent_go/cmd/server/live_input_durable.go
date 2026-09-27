package server

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// liveInputDurableWatchTimeout caps the server-side durability watch.
// The adapter's own env-tuned budget (codex default 180s, max 300s)
// bounds the actual wait; this only guards against a pathological
// hang beneath it.
const liveInputDurableWatchTimeout = 6 * time.Minute

// watchLiveInputDurable starts the durability half of the two-stage
// delivery receipt after a fast pane ack. It returns at once — the HTTP
// response must not wait behind it — and emits a live_input_confirmed
// event (matched by message_id) when the CLI's own durable record
// confirms the send. Providers without SupportsDurableAck are ignored.
func (api *StreamingAPI) watchLiveInputDurable(sessionID, provider, messageID, message string) {
	api.watchLiveInputDurableRecording(sessionID, provider, messageID, message, "", nil)
}

// liveInputHasDurableAck reports whether the provider can tell us when its CLI
// actually took a steered message (as opposed to it waiting in the pane).
func liveInputHasDurableAck(provider string) bool {
	contract, ok := llmproviders.GetCodingAgentProviderContract(llmproviders.Provider(strings.TrimSpace(provider)), "")
	return ok && contract.SupportsDurableAck
}

// watchLiveInputDurableRecording is watchLiveInputDurable that, when deferred
// is set, records that user_message only once the CLI has taken it. A message
// steered into a busy CLI waits in its input until the current answer ends, so
// recording it at submit time placed the in-flight answer under it.
func (api *StreamingAPI) watchLiveInputDurableRecording(sessionID, provider, messageID, message, clientMessageID string, deferred *events.Event) {
	provider = strings.TrimSpace(provider)
	if api == nil || api.eventStore == nil || sessionID == "" || messageID == "" || strings.TrimSpace(message) == "" {
		if deferred != nil && api != nil && api.eventStore != nil && sessionID != "" {
			api.eventStore.AddEvent(sessionID, *deferred)
		}
		return
	}
	if !liveInputHasDurableAck(provider) {
		if deferred != nil {
			api.eventStore.AddEvent(sessionID, *deferred)
		}
		return
	}
	if deferred != nil {
		// Answer rows written before the ack must not land above this message.
		api.eventStore.BeginDeferredSteer(sessionID, *deferred)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), liveInputDurableWatchTimeout)
		defer cancel()
		ack, err := api.awaitLiveInputDurable(ctx, llmproviders.Provider(provider), sessionID, message)
		var outcome string
		proof := ""
		var latencyMs int64
		if err != nil {
			outcome = string(llmtypes.DurableAckFailed)
		} else {
			outcome = string(ack.Outcome)
			proof = ack.ProofSource
			latencyMs = ack.Latency.Milliseconds()
		}
		// Background audit: the fast ack already told the user this
		// send succeeded, so anything but confirmed is provider
		// drift worth logging loud — unflushed is held-not-failed,
		// failed-after-fast-ack is a silent drop.
		if outcome != string(llmtypes.DurableAckConfirmed) {
			log.Printf("[LIVE-INPUT-DURABLE] session=%s provider=%s message=%s outcome=%s err=%v",
				sessionID, provider, messageID, outcome, err)
		}
		// Whatever the verdict, the message was sent: record it now, at the
		// point the CLI took it (or gave up), then its receipt.
		if deferred != nil {
			api.eventStore.CompleteDeferredSteer(sessionID, events.UserMessageAt(*deferred, time.Now()))
		}
		api.recordLiveInputConfirmed(sessionID, messageID, outcome, proof, provider, latencyMs, clientMessageID)
	}()
}

// durableAckAwaiters binds every SupportsDurableAck provider to its typed
// await entry point. A new durable-ack provider must add its entry here;
// TestDurableAckDispatchCoversAckContracts fails otherwise. The zero
// timeout selects each adapter's env-tuned budget.
var durableAckAwaiters = map[llmproviders.Provider]func(ctx context.Context, sessionID, message string) (llmtypes.DurableAck, error){
	llmproviders.ProviderClaudeCode: func(ctx context.Context, sessionID, message string) (llmtypes.DurableAck, error) {
		return llm.AwaitClaudeInputDurable(ctx, sessionID, message, 0)
	},
	llmproviders.ProviderCursorCLI: func(ctx context.Context, sessionID, message string) (llmtypes.DurableAck, error) {
		return llm.AwaitCursorInputDurable(ctx, sessionID, message, 0)
	},
	llmproviders.ProviderCodexCLI: func(ctx context.Context, sessionID, message string) (llmtypes.DurableAck, error) {
		return llm.AwaitCodexInputDurable(ctx, sessionID, message, 0)
	},
	llmproviders.ProviderPiCLI: func(ctx context.Context, sessionID, message string) (llmtypes.DurableAck, error) {
		return llm.AwaitPiInputDurable(ctx, sessionID, message, 0)
	},
	llmproviders.ProviderMuseCLI: func(ctx context.Context, sessionID, message string) (llmtypes.DurableAck, error) {
		return llm.AwaitMuseInputDurable(ctx, sessionID, message, 0)
	},
	llmproviders.ProviderAgyCLI: func(ctx context.Context, sessionID, message string) (llmtypes.DurableAck, error) {
		return llm.AwaitAgyInputDurable(ctx, sessionID, message, 0)
	},
}

// awaitLiveInputDurable dispatches to the provider's durable-ack entry
// point. internalDurableAckHandler lets routing tests observe the watch
// without a real CLI; production falls through to the typed adapter
// await, which exists for every provider with the contract flag.
func (api *StreamingAPI) awaitLiveInputDurable(ctx context.Context, provider llmproviders.Provider, sessionID, message string) (llmtypes.DurableAck, error) {
	if api != nil && api.internalDurableAckHandler != nil {
		return api.internalDurableAckHandler(ctx, provider, sessionID, message)
	}
	if await, ok := durableAckAwaiters[provider]; ok {
		return await(ctx, sessionID, message)
	}
	return llmtypes.DurableAck{Outcome: llmtypes.DurableAckFailed}, nil
}

// recordLiveInputConfirmed persists the durability verdict as a session
// event so it reaches chat over SSE and survives reconnect replay like
// the user_message row it upgrades. The event ID derives
// deterministically from the message so redelivery is idempotent.
func (api *StreamingAPI) recordLiveInputConfirmed(sessionID, messageID, outcome, proofSource, provider string, latencyMs int64, clientMessageID string) {
	if api == nil || api.eventStore == nil || sessionID == "" || messageID == "" {
		return
	}
	eventData := unifiedevents.NewLiveInputConfirmedEvent(messageID, outcome, proofSource, provider, latencyMs)
	if clientMessageID != "" {
		if eventData.Metadata == nil {
			eventData.Metadata = map[string]interface{}{}
		}
		eventData.Metadata["client_message_id"] = clientMessageID
	}
	agentEvent := unifiedevents.NewAgentEvent(eventData)
	agentEvent.SessionID = sessionID
	agentEvent.Component = "coding_agent_live_input"
	event := events.Event{
		ID:        messageID + ":confirmed",
		Type:      string(unifiedevents.LiveInputConfirmed),
		Timestamp: time.Now(),
		Data:      agentEvent,
		SessionID: sessionID,
	}
	api.eventStore.AddEvent(sessionID, event)
}
