package mcpagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// stuckSteerQueueAge bounds how long a queued steer message may wait for a
// drain. Past it, the queue is proven orphaned: no running turn will pick
// it up (a pooled miss already proved the CLI target is gone, and drains
// happen inside the turn loop). Delivery then breaks the stuck turn —
// resetting its flag and reporting no-target so the caller starts a fresh
// turn — instead of parking another message forever. Five minutes sits
// beyond the adapter durable-ack budgets while boot races resolve in
// seconds, so legitimate queueing never trips it.
const stuckSteerQueueAge = 5 * time.Minute

// CodingAgentDeliveryErrorKind classifies why a DeliverUserMessage call failed.
// This is distinct from CodingAgentContinuationError, which covers provider-level
// session resume failures.
type CodingAgentDeliveryErrorKind string

const (
	DeliveryErrorKindEmptyMessage  CodingAgentDeliveryErrorKind = "empty_message"
	DeliveryErrorKindNotSupported  CodingAgentDeliveryErrorKind = "not_supported"
	DeliveryErrorKindNoSession     CodingAgentDeliveryErrorKind = "no_active_session"
	DeliveryErrorKindTimeout       CodingAgentDeliveryErrorKind = "delivery_timed_out"
	DeliveryErrorKindQueueRequired CodingAgentDeliveryErrorKind = "host_queue_required"
)

// CodingAgentDeliveryError is returned when DeliverUserMessage cannot route a
// message to the running agent, independently of any continuation/resume logic.
type CodingAgentDeliveryError struct {
	// UnsentMessages were accepted into the queue but never delivered. A NoSession
	// caller carries them, in order, into the replacement turn (PLAT-872).
	UnsentMessages []string
	// UnsentIdentities parallels UnsentMessages (zero for internal callers), so the turn that takes the
	// messages over records each one as its own row and every bubble the person sent settles.
	UnsentIdentities []QueuedMessageIdentity
	Kind             CodingAgentDeliveryErrorKind
	Provider         llm.Provider
	Reason           string
}

func (e *CodingAgentDeliveryError) Error() string {
	return fmt.Sprintf("coding agent delivery error (%s, %s): %s", e.Provider, e.Kind, e.Reason)
}

// isInteractiveSessionNotRegistered reports whether err is a provider
// adapter's pre-I/O pool miss ("no active X interactive session registered
// for owner session ..."). Every tmux adapter emits that shape from its
// pooled-session lookup before touching tmux, so it proves nothing was sent
// — unlike mid-send failures, which keep their own messages and stay
// uncertain.
func isInteractiveSessionNotRegistered(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "interactive session registered")
}

type UserMessageDeliveryIntent string

const (
	UserMessageDeliveryIntentAuto      UserMessageDeliveryIntent = "auto"
	UserMessageDeliveryIntentLiveInput UserMessageDeliveryIntent = "live_input"
)

type UserMessageDeliveryStatus string

const (
	UserMessageDeliveryStatusSentToCLI          UserMessageDeliveryStatus = "sent_to_cli"
	UserMessageDeliveryStatusQueuedForInjection UserMessageDeliveryStatus = "queued_for_injection"
)

type UserMessageDeliveryRequest struct {
	SessionID string
	Message   string
	Intent    UserMessageDeliveryIntent
	// MessageID and ClientMessageID identify the person's message. A queued message keeps them and is
	// recorded once, where the agent takes it, so the chat matches it to the bubble the person sent.
	MessageID       string
	ClientMessageID string
}

type UserMessageDeliveryResult struct {
	Provider       llm.Provider
	DeliveryStatus UserMessageDeliveryStatus
	Transport      llm.CodingAgentTransport
}

// ControlKeyDeliveryRequest carries a tmux control key (e.g. "Escape") for
// injection into a currently running coding-agent session.
type ControlKeyDeliveryRequest struct {
	SessionID string
	Key       string
}

// ControlKeyDeliveryResult reports how a control key was routed.
type ControlKeyDeliveryResult struct {
	Provider  llm.Provider
	Transport llm.CodingAgentTransport
}

// DeliverControlKey injects a tmux control key into the agent's currently
// running coding-agent session. Returns DeliveryErrorKindNotSupported for
// non-tmux providers so callers can fall back to context cancellation.
func (a *Agent) deliverControlKey(ctx context.Context, req ControlKeyDeliveryRequest) (ControlKeyDeliveryResult, error) {
	provider := a.getProvider()
	result := ControlKeyDeliveryResult{Provider: provider}
	key := strings.TrimSpace(req.Key)
	if key == "" {
		return result, &CodingAgentDeliveryError{
			Kind:     DeliveryErrorKindEmptyMessage,
			Provider: provider,
			Reason:   "control key is empty",
		}
	}
	if !llm.IsAllowedCodingAgentControlKey(key) {
		return result, &CodingAgentDeliveryError{
			Kind:     DeliveryErrorKindNotSupported,
			Provider: provider,
			Reason:   fmt.Sprintf("control key %q is not allowed", key),
		}
	}

	contract, isCodingAgent := llm.GetCodingAgentProviderContract(provider, a.modelID)
	if !isCodingAgent || !contract.SupportsInterrupt {
		return result, &CodingAgentDeliveryError{
			Kind:     DeliveryErrorKindNotSupported,
			Provider: provider,
			Reason:   "provider transport does not support interruption",
		}
	}
	result.Transport = contract.Transport

	if err := llm.SendCodingAgentControlKey(ctx, provider, a.modelID, req.SessionID, key); err != nil {
		return result, err
	}
	return result, nil
}

// DeliverUserMessage routes a user message through the correct running-turn
// mechanism for this agent. Tmux coding agents get provider-native live input;
// a failed tmux submission is returned to the caller instead of being hidden in
// the internal steer queue. API/structured/non-coding agents still use that
// queue between tool calls and the next LLM call.
func (a *Agent) deliverUserMessage(ctx context.Context, req UserMessageDeliveryRequest) (UserMessageDeliveryResult, error) {
	provider := a.getProvider()
	result := UserMessageDeliveryResult{Provider: provider}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		return result, &CodingAgentDeliveryError{
			Kind:     DeliveryErrorKindEmptyMessage,
			Provider: provider,
			Reason:   "message is empty",
		}
	}

	contract, isCodingAgent := llm.GetCodingAgentProviderContract(provider, a.modelID)
	if isCodingAgent {
		result.Transport = contract.Transport
		if a.usesStructuredTransport() {
			result.Transport = llm.CodingAgentTransportStructured
		}
	}

	// PLAT-872-24: native protocols steer only an active turn. A retained
	// process between turns must resume through the normal generation path.
	if isCodingAgent && contract.Transport != llm.CodingAgentTransportTmux && !a.isTurnInFlight() {
		return result, &CodingAgentDeliveryError{Kind: DeliveryErrorKindNoSession, Provider: provider, Reason: "no turn is running to take the message; start a new turn"}
	}

	// Transport-aware, not just contract-aware: the same provider is steerable on
	// tmux but query-only on the structured/JSON transport, which has no live pane
	// to send-keys into. usesStructuredTransport() gates the tmux live-input path
	// off for a structured run so it correctly falls through to the steer queue —
	// mirrors SupportsSteering() (coding_session.go). Without this a structured
	// coding-agent turn would try to tmux-inject into a one-shot process.
	if isCodingAgent && contract.SupportsLiveInput && !a.usesStructuredTransport() {
		if steersNatively(contract.Transport) {
			// Every caller (Session.Send and direct DeliverAgentInput) passes here, so a second message waits
			// behind one that is waiting out the turn start and they reach the agent in the order sent.
			a.nativeSteerMu.Lock()
			defer a.nativeSteerMu.Unlock()
		}
		send := func() error {
			if req.Intent == UserMessageDeliveryIntentLiveInput {
				return sendCodingAgentLiveInput(ctx, provider, a.modelID, req.SessionID, message)
			}
			return llm.SendCodingAgentRetainedInput(ctx, provider, a.modelID, req.SessionID, message)
		}
		err := send()
		if err != nil && inputNotTaken(err) && len(llmtypes.NativeImageInputsForSession(ctx, req.SessionID)) > 0 {
			return result, &CodingAgentDeliveryError{Kind: DeliveryErrorKindQueueRequired, Provider: provider, Reason: "native image input not taken; use host attachment queue"}
		}
		if err != nil && steersNatively(contract.Transport) && inputNotTaken(err) && a.isTurnInFlight() {
			// A native protocol takes a message mid-turn. "Not taken" while our turn is open usually means its
			// own turn is still starting (server B 2026-10-27: a message 2s into a Cursor SDK turn was parked
			// until the first answer ended). Keep offering it for a short while; if our turn closes meanwhile,
			// the caller starts the next turn with it. A provider turn that already ended keeps the queue below:
			// this turn's last step takes the message (PLAT-872-24).
			deadline := time.Now().Add(nativeSteerStartWait)
			for err != nil && inputNotTaken(err) && a.turnTakesInput() && ctx.Err() == nil && time.Now().Before(deadline) {
				select {
				case <-ctx.Done():
				case <-time.After(nativeSteerRetryInterval):
				}
				if ctx.Err() == nil {
					err = send()
				}
			}
			if err != nil && inputNotTaken(err) && ctx.Err() == nil && !a.isTurnInFlight() {
				return result, &CodingAgentDeliveryError{Kind: DeliveryErrorKindNoSession, Provider: provider, Reason: "the turn finished before taking the message; start a new turn"}
			}
			if err == nil {
				result.DeliveryStatus = UserMessageDeliveryStatusSentToCLI
				return result, nil
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return result, fmt.Errorf("failed to submit live input to %s: %w", provider, ctxErr)
			}
		}
		if err != nil {
			if (isInteractiveSessionNotRegistered(err) || errors.Is(err, llmtypes.ErrCodingAgentInputNotSent)) && len(llmtypes.NativeImageInputsForSession(ctx, req.SessionID)) > 0 {
				return result, &CodingAgentDeliveryError{Kind: DeliveryErrorKindQueueRequired, Provider: provider, Reason: "native image input was not taken; retain admitted attachments in the host queue"}
			}
			if (isInteractiveSessionNotRegistered(err) || errors.Is(err, llmtypes.ErrCodingAgentInputNotSent)) && a.isTurnInFlight() {
				// No pooled TUI while a turn runs: an API-continuation turn,
				// or a TUI turn whose pane is still booting. Either way the
				// outer conversation loop is running, so queue for its next
				// boundary (AskWithHistory drains after tool execution and
				// after the final response) instead of failing a send no CLI
				// ever saw. Without a running turn nothing drains the queue,
				// so idle sessions keep the error and the caller starts a
				// new turn.
				if age, orphaned := a.oldestQueuedSteerAge(); orphaned && age >= stuckSteerQueueAge {
					// The previous queue never drained: its turn is hung or
					// died without cleanup, and this send's pool miss proves
					// its CLI target is gone too. Parking another message
					// would strand the chat with a silent accept, so break
					// the stuck turn — reset the stale flag, recover the
					// orphaned queue — and report no-target: the caller
					// starts a fresh turn that re-resolves the current
					// provider instead.
					pending := a.resetStuckTurnState()
					if a.logger != nil {
						a.logger.Warn(fmt.Sprintf("stuck steer queue: session=%s provider=%s orphaned=%s recovered=%d; turn flag reset, caller starts a new turn", req.SessionID, provider, age.Round(time.Second), len(pending)))
					}
					return result, &CodingAgentDeliveryError{Kind: DeliveryErrorKindNoSession, Provider: provider, Reason: "native target unavailable; queued input must start a new turn", UnsentMessages: steerTexts(pending), UnsentIdentities: steerIdentities(pending)}
				}
				if !a.addSteerMessageWithID(message, QueuedMessageIdentity{ID: req.MessageID, ClientID: req.ClientMessageID}) {
					return result, &CodingAgentDeliveryError{Kind: DeliveryErrorKindNoSession, Provider: provider, Reason: "the turn has finished accepting input; start a new turn"}
				}
				if a.logger != nil {
					a.logger.Warn(fmt.Sprintf("steer message queued for injection: session=%s provider=%s", req.SessionID, provider))
				}
				result.DeliveryStatus = UserMessageDeliveryStatusQueuedForInjection
				return result, nil
			}
			if errors.Is(err, llmtypes.ErrCodingAgentInputNotSent) {
				return result, &CodingAgentDeliveryError{Kind: DeliveryErrorKindNoSession, Provider: provider, Reason: err.Error()}
			}
			return result, fmt.Errorf("failed to submit live input to %s: %w", provider, err)
		}
		result.DeliveryStatus = UserMessageDeliveryStatusSentToCLI
		return result, nil
	}

	if len(llmtypes.NativeImageInputsForSession(ctx, req.SessionID)) > 0 {
		return result, &CodingAgentDeliveryError{Kind: DeliveryErrorKindQueueRequired, Provider: provider, Reason: "text-only steer queue cannot retain native image input"}
	}
	// The steer queue is drained only inside a running turn. Accepting a
	// message into it while idle strands it: the caller reports success and no
	// turn ever reads it (server A 2026-09-25, a structured agent left behind by a
	// scheduled turn swallowed the user's next messages). Refuse instead so
	// the caller starts a new turn with the message.
	if !a.isTurnInFlight() {
		return result, &CodingAgentDeliveryError{
			Kind:     DeliveryErrorKindNoSession,
			Provider: provider,
			Reason:   "no turn is running to take the message; start a new turn",
		}
	}
	if !a.addSteerMessageWithID(message, QueuedMessageIdentity{ID: req.MessageID, ClientID: req.ClientMessageID}) {
		return result, &CodingAgentDeliveryError{Kind: DeliveryErrorKindNoSession, Provider: provider, Reason: "the turn has finished accepting input; start a new turn"}
	}
	result.DeliveryStatus = UserMessageDeliveryStatusQueuedForInjection
	return result, nil
}

// nativeSteerRetryInterval is how often a message is offered again to a native protocol whose turn is starting;
// nativeSteerStartWait bounds how long (a native turn starts in a few seconds).
var (
	nativeSteerRetryInterval = 200 * time.Millisecond
	nativeSteerStartWait     = 15 * time.Second
	sendCodingAgentLiveInput = llm.SendCodingAgentLiveInput
)

// steersNatively reports whether the transport takes a message in the middle of a turn (the Claude and Cursor
// SDKs, the Codex app server). Only the others (tmux, ACP, structured) use the steer queue.
func steersNatively(transport llm.CodingAgentTransport) bool {
	// ACP: Antigravity takes a prompt sent mid-turn into the running turn (#3920).
	return transport == llm.CodingAgentTransportSDK || transport == llm.CodingAgentTransportAppServer || transport == llm.CodingAgentTransportACP
}

// turnTakesInput: a turn is open and has not taken its last step (after the final drain it only closes).
func (a *Agent) turnTakesInput() bool {
	if !a.isTurnInFlight() {
		return false
	}
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	return !a.steerQueueClosed
}

func inputNotTaken(err error) bool {
	return isInteractiveSessionNotRegistered(err) || errors.Is(err, llmtypes.ErrCodingAgentInputNotSent)
}

// liveInputUsesTmux reports whether live input is typed into a tmux CLI pane
// (as opposed to the structured transport's steer queue).
func (a *Agent) liveInputUsesTmux() bool {
	if a == nil {
		return false
	}
	contract, isCodingAgent := llm.GetCodingAgentProviderContract(a.getProvider(), a.modelID)
	return isCodingAgent && contract.Transport == llm.CodingAgentTransportTmux && contract.SupportsLiveInput && !a.usesStructuredTransport()
}
