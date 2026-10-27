package server

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
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

// liveInputDelivered remembers the exact text a retained CLI was sent when the
// server added its session notice in front of the user's message. The chat row
// shows the message as typed, but the CLI's own record holds the notice too, so
// the durability watch must look for what was really sent.
var liveInputDelivered sync.Map

func liveInputDeliveredKey(sessionID, typed string) string {
	return sessionID + "\x00" + strings.TrimSpace(typed)
}

// noteLiveInputDelivered records the text sent to the CLI for a typed message.
func noteLiveInputDelivered(sessionID, provider, typed, delivered string) {
	// Only a provider with a durability watch will ever take the entry back.
	if !liveInputHasDurableAck(provider) || sessionID == "" || delivered == "" || strings.TrimSpace(delivered) == strings.TrimSpace(typed) {
		return
	}
	liveInputDelivered.Store(liveInputDeliveredKey(sessionID, typed), delivered)
}

// takeLiveInputDelivered returns the text the CLI was sent for a typed message
// (the typed text itself when nothing was added), and forgets it.
func takeLiveInputDelivered(sessionID, typed string) string {
	if v, ok := liveInputDelivered.LoadAndDelete(liveInputDeliveredKey(sessionID, typed)); ok {
		if text, ok := v.(string); ok && text != "" {
			return text
		}
	}
	return typed
}

// liveInputDurableTimedOut reports that the CLI's record simply was not seen in
// time. That is "not confirmed yet", never proof the message was lost.
func liveInputDurableTimedOut(err error) bool {
	return err != nil && (errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "not durably acknowledged"))
}

// liveInputLateConfirmWindow is how much longer the server keeps looking for
// the CLI's record after the first budget ran out.
const liveInputLateConfirmWindow = 5 * time.Minute

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
		delivered := takeLiveInputDelivered(sessionID, message)
		ack, err := api.awaitLiveInputDurable(ctx, llmproviders.Provider(provider), sessionID, delivered)
		var outcome string
		proof := ""
		var latencyMs int64
		timedOut := false
		if err != nil {
			if liveInputDurableTimedOut(err) {
				// The CLI took the message at its prompt (the fast ack); its own record was
				// not seen within the budget. Say "held, awaiting record", not "failed":
				// a failed receipt invites a Resend and duplicate work.
				outcome, proof, timedOut = string(llmtypes.DurableAckUnflushed), "durable_wait_timeout", true
			} else {
				outcome = string(llmtypes.DurableAckFailed)
			}
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
		if !timedOut {
			return
		}
		// Keep looking for the CLI's own record for a while; when it shows up the
		// row is upgraded to confirmed (a later event, so it is not deduplicated).
		lateCtx, lateCancel := context.WithTimeout(context.Background(), liveInputLateConfirmWindow)
		defer lateCancel()
		for lateCtx.Err() == nil {
			lateAck, lateErr := api.awaitLiveInputDurable(lateCtx, llmproviders.Provider(provider), sessionID, delivered)
			if lateErr == nil && lateAck.Outcome == llmtypes.DurableAckConfirmed {
				api.recordLiveInputConfirmedLate(sessionID, messageID, string(lateAck.Outcome), lateAck.ProofSource, provider, lateAck.Latency.Milliseconds(), clientMessageID)
				return
			}
			if lateErr != nil && !liveInputDurableTimedOut(lateErr) {
				return
			}
		}
	}()
}

// durableAckAwaiters binds every SupportsDurableAck provider to its typed
// await entry point. A new durable-ack provider must add its entry here;
// TestDurableAckDispatchCoversAckContracts fails otherwise. The zero
// timeout selects each adapter's env-tuned budget.
var durableAckAwaiters = map[llmproviders.Provider]func(ctx context.Context, sessionID, message string) (llmtypes.DurableAck, error){
	llmproviders.ProviderCursorCLI: func(ctx context.Context, sessionID, message string) (llmtypes.DurableAck, error) {
		return llm.AwaitCursorInputDurable(ctx, sessionID, message, 0)
	},
	llmproviders.ProviderClaudeCode: func(ctx context.Context, sessionID, message string) (llmtypes.DurableAck, error) {
		return llm.AwaitClaudeInputDurable(ctx, sessionID, message, 0)
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
	api.recordLiveInputReceipt(sessionID, messageID, outcome, proofSource, provider, latencyMs, clientMessageID, "")
}

// recordLiveInputConfirmedLate records a later verdict for a message that
// already has one (an upgrade after the first budget ran out). Its event ID
// differs so the store does not drop it as a duplicate.
func (api *StreamingAPI) recordLiveInputConfirmedLate(sessionID, messageID, outcome, proofSource, provider string, latencyMs int64, clientMessageID string) {
	api.recordLiveInputReceipt(sessionID, messageID, outcome, proofSource, provider, latencyMs, clientMessageID, ":late")
}

// liveInputDecision maps a stored outcome to the word used in the [DELIVERY] log.
func liveInputDecision(outcome string) string {
	switch outcome {
	case string(llmtypes.DurableAckConfirmed):
		return "confirmed"
	case string(llmtypes.DurableAckUnflushed):
		return "unflushed"
	default:
		return "failed"
	}
}

func (api *StreamingAPI) recordLiveInputReceipt(sessionID, messageID, outcome, proofSource, provider string, latencyMs int64, clientMessageID, idSuffix string) {
	if api == nil || api.eventStore == nil || sessionID == "" || messageID == "" {
		return
	}
	source := "durable_ack"
	switch {
	case strings.HasPrefix(proofSource, "conversation_turn"):
		source = "turn_queue"
	case proofSource == "" && strings.HasPrefix(messageID, "late-delivery-"):
		source = "late_delivery"
	case idSuffix != "":
		source = "durable_ack_late"
	}
	// One line per receipt decision, keyed by submission and message id. No message text.
	log.Printf("[DELIVERY] submission=%s message_id=%s session=%s decision=%s source=%s reason=%s provider=%s latency_ms=%d",
		clientMessageID, messageID, sessionID, liveInputDecision(outcome), source, proofSource, provider, latencyMs)
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
		ID:        messageID + ":confirmed" + idSuffix,
		Type:      string(unifiedevents.LiveInputConfirmed),
		Timestamp: time.Now(),
		Data:      agentEvent,
		SessionID: sessionID,
	}
	api.eventStore.AddEvent(sessionID, event)
}
