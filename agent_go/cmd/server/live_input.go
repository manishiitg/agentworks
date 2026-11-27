package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// handleSetLLMGuidance sets LLM guidance for a session
func (api *StreamingAPI) handleSetLLMGuidance(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	vars := mux.Vars(r)
	sessionID := vars["session_id"]
	if sessionID == "" {
		http.Error(w, "Session ID is required", http.StatusBadRequest)
		return
	}
	// Same admission as live input: guidance enters the session's next system
	// prompt and a control key types into its live agent, so only someone who
	// may drive this session can use them.
	if !api.canAccessTerminalSession(r, sessionID) {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}
	if refusal := codeSessionTurnRefusal(sessionID, GetUserFromContext(r.Context())); refusal != "" {
		http.Error(w, refusal, http.StatusForbidden)
		return
	}

	var req LLMGuidanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Validate and update guidance under a single write lock so we never mutate a stale pointer.
	api.activeSessionsMux.Lock()
	session, exists := api.activeSessions[sessionID]
	if !exists {
		api.activeSessionsMux.Unlock()
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}
	session.LLMGuidance = req.Guidance
	session.LastActivity = time.Now()
	api.activeSessionsMux.Unlock()

	log.Printf("[LLM_GUIDANCE] Set guidance for session %s: %s", sessionID, req.Guidance)

	response := LLMGuidanceResponse{
		SessionID: sessionID,
		Status:    "success",
		Message:   "LLM guidance updated successfully",
		Guidance:  req.Guidance,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Compare current product setup to the admission of the actually launched
// agent. Saved runtime metadata may still belong to the preceding turn while
// the current turn streams; it is only a fallback after process restart.
func (api *StreamingAPI) agentProfileAllowsRetainedLiveInput(userID, sessionID, workspacePath string, hasProfile bool) bool {
	if api == nil || !hasProfile {
		return true
	}
	api.conversationMux.RLock()
	currentProfileKey, known := api.lastAgentProfileKeyBySession[sessionID]
	api.conversationMux.RUnlock()
	if !known || currentProfileKey == "" {
		log.Printf("[CHAT_HISTORY] Retained live input disabled for product session %s: no current agent profile key is recorded for it", sessionID)
		return false
	}
	api.conversationMux.RLock()
	launched, launchKnown := api.launchedAgentProfileKeyBySession[sessionID]
	api.conversationMux.RUnlock()
	if launchKnown {
		if launched != currentProfileKey {
			log.Printf("[CHAT_HISTORY] Retained live input disabled for product session %s: the launched agent profile differs from the current one (changed: %s)", sessionID, agentProfileKeyChange(launched, currentProfileKey))
		}
		return launched == currentProfileKey
	}

	runtime, ok, err := ReadChatHistoryRuntimeForSession(userID, sessionID, workspacePath)
	if err != nil || !ok || runtime == nil {
		log.Printf("[CHAT_HISTORY] Retained live input disabled for product session %s: current runtime fingerprint is unavailable (launched_key_known=false saved_runtime_found=%t read_error=%v)", sessionID, ok, err)
		return false
	}
	if strings.TrimSpace(runtime.AgentProfileKey) != currentProfileKey {
		log.Printf("[CHAT_HISTORY] Retained live input disabled for product session %s: agent profile definition changed (changed: %s)", sessionID, agentProfileKeyPartsChange(runtime.AgentProfileKeyParts, currentProfileKey))
		return false
	}
	return true
}

// agentSupportsLiveInputDelivery reports whether the retained agent is a coding
// agent whose provider contract supports live input (tmux-transport CLIs). Only
// these short-circuit a /api/query into CLI delivery; API/LLM agents fall through
// to the normal new-turn path so their behavior is unchanged.
func agentSupportsLiveInputDelivery(agent *mcpagent.Agent) bool {
	if agent == nil {
		return false
	}
	runtime := mcpagent.ReadAgentRuntimeInfo(agent)
	contract, isCodingAgent := llm.GetCodingAgentProviderContract(mcpagent.ReadAgentRuntimeInfo(agent).Provider, runtime.LLMConfig.ModelID)
	return isCodingAgent && contract.SupportsLiveInput
}

// liveInputErrorProvesNoTarget reports whether a live-input Send error proves
// no CLI received the message: the retained target is gone (a pooled-session
// miss after a restart closed it, or a closed turn session). The caller falls
// through to a normal new turn instead of surfacing delivery_uncertain. Every
// tmux adapter emits the "no active X session registered for owner session"
// shape from its pooled-session lookup before touching tmux; note this is
// wider than mcpagent's unexported isInteractiveSessionNotRegistered, which
// misses claude-code's "tmux session" phrasing.
func liveInputErrorProvesNoTarget(err error) bool {
	if err == nil {
		return false
	}
	// A running agent with no turn in flight cannot take the message (its
	// steer queue drains only inside a turn), so start a new turn with it.
	var deliveryErr *mcpagent.CodingAgentDeliveryError
	if errors.As(err, &deliveryErr) && deliveryErr.Kind == mcpagent.DeliveryErrorKindNoSession {
		return true
	}
	return strings.Contains(err.Error(), "session registered for owner") ||
		strings.Contains(err.Error(), "session is closed")
}

// recoverUnsentLiveInput carries acknowledged-but-unwritten queue entries into
// the replacement turn; a typed NoSession must never silently discard them.

// recoverUnsentLiveInput hands queued messages to the new turn that replaces a gone session. Each message a
// person sent is recorded as its own row first, so its bubble settles instead of waiting forever (PLAT-872).
func (api *StreamingAPI) recoverUnsentLiveInput(sessionID string, err error, recovered *[]string) {
	if recovered == nil {
		return
	}
	var deliveryErr *mcpagent.CodingAgentDeliveryError
	if !errors.As(err, &deliveryErr) || deliveryErr.Kind != mcpagent.DeliveryErrorKindNoSession {
		return
	}
	for i, text := range deliveryErr.UnsentMessages {
		if i < len(deliveryErr.UnsentIdentities) {
			if id := deliveryErr.UnsentIdentities[i]; id.ID != "" {
				api.recordLiveCodingAgentUserMessage(sessionID, text, string(deliveryErr.Provider), id.ID, "next_turn_started", id.ClientID)
			}
		}
	}
	*recovered = append(*recovered, deliveryErr.UnsentMessages...)
}

// tryDeliverQueryAsLiveInput is the single-entry retained-CLI path for
// tmux-transport coding-agent input (see handleQuery). When the session still
// has a retained coding-agent object that supports native live input, it sends
// the incoming /api/query message to that CLI instead of requiring a separate
// foreground-turn/busy proof. Returns true if it handled and responded to the
// request; false lets handleQuery start a normal new turn.
//
// Delivery into a busy CLI can take minutes (pane retries, durable-ack
// budgets), but the gateway/CloudFront cut a request at 30-60s and show the
// user a hard failure for a message that may still land. So the send runs on
// a request-detached context and the HTTP reply waits at most
// liveInputFastResponseWindow: a send that finishes in time answers exactly as
// before; a slower one answers "accepted" (delivery_status "delivering") at
// once and records its real outcome later (journal + chat receipt).
func (api *StreamingAPI) tryDeliverQueryAsLiveInput(w http.ResponseWriter, r *http.Request, sessionID, message, queryID string, recovered *[]string, receivedAt ...time.Time) bool {
	if api == nil || strings.TrimSpace(message) == "" {
		return false
	}
	if isQueuedMessageSteer(r.Context()) {
		// The owner action already returned 202 and this is its background
		// dispatch. Keep the durable claim until an actual delivery result;
		// the browser's early-accept timeout must not discard this queue row.
		return api.deliverQueryAsLiveInputNow(w, r, sessionID, message, queryID, recovered, receivedAt...)
	}
	// Text typed in the CLI's own composer does not block a chat send: only the person who owns the
	// chat can reach that terminal, and the refusal (423) was a false alarm for real users.
	detached := r.WithContext(context.WithoutCancel(r.Context()))
	detached.Header = r.Header.Clone()
	recorded := &internalResponseCapture{header: http.Header{}}
	done := make(chan bool, 1)
	var pending []string
	go func() {
		handled := false
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("[QUERY->LIVE] Delivery panicked for session %s: %v", sessionID, recovered)
				recorded = &internalResponseCapture{header: http.Header{}}
				writeSubmissionUncertain(recorded, detached.Header.Get("Idempotency-Key"))
				handled = true
			}
			done <- handled
		}()
		if api.internalLiveInputDeliver != nil {
			handled = api.internalLiveInputDeliver(recorded, detached, sessionID, message, queryID)
			return
		}
		handled = api.deliverQueryAsLiveInputNow(recorded, detached, sessionID, message, queryID, &pending, receivedAt...)
	}()
	timer := time.NewTimer(liveInputFastResponseWindow)
	defer timer.Stop()
	select {
	case handled := <-done:
		if recovered != nil {
			*recovered = append(*recovered, pending...)
		}
		if handled {
			copyInternalResponse(w, recorded)
		}
		return handled
	case <-timer.C:
	}

	submission, _ := r.Context().Value(chatSubmissionContextKey{}).(chatSubmissionContext)
	clientMessageID := clientMessageIDFromContext(r.Context())
	log.Printf("[QUERY->LIVE] Delivery for session %s still running after %s; answering accepted and finishing in the background", sessionID, liveInputFastResponseWindow)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true, "status": "accepted", "session_id": sessionID, "query_id": queryID,
		"delivery_status": liveInputDeliveryInProgress,
		"message":         "Still delivering to the coding agent; the chat updates when it lands",
	})
	go func() {
		// A message the coding agent has not taken is logged while it waits, so a stalled delivery is visible in the log
		// (no message text): one line each at these waits.
		handled := waitForLiveInputDelivery(done, sessionID, queryID, liveInputFastResponseWindow)
		if !handled {
			// This late path starts its turn under another ID; the snapshot prepared for queryID has no turn.
			api.discardUnstartedCrewSnapshot(queryID)
		}
		if !handled {
			// No live target took the message (e.g. no_active_session after a restart). The synchronous path falls
			// through to a new turn here; this late path must do the same instead of marking the message failed.
			log.Printf("[QUERY->LIVE] Background delivery for session %s found no live target; starting a new turn (recovered=%d)", sessionID, len(pending))
			handled = api.startTurnAfterLateNoTarget(recorded, detached, sessionID, strings.Join(append(pending, message), "\n\n"))
		}
		status := recorded.status
		if status == 0 {
			status = http.StatusOK
		}
		body := recorded.body.Bytes()
		failed := !handled || status >= 400 || submissionStateForResponse(status, body) == "delivery_uncertain"
		if failed {
			log.Printf("[QUERY->LIVE] Background delivery for session %s did not complete (handled=%t status=%d); marking uncertain", sessionID, handled, status)
			if !handled || status < 400 {
				recorded = &internalResponseCapture{header: http.Header{}}
				writeSubmissionUncertain(recorded, submission.ID)
				status, body = recorded.status, recorded.body.Bytes()
			}
			submission.handle.resolveLate(status, body)
			receiptID := clientMessageID
			if receiptID == "" {
				receiptID = queryID
			}
			api.recordLiveInputConfirmed(sessionID, "late-delivery-"+receiptID, string(llmtypes.DurableAckFailed), "", "", 0, clientMessageID)
			api.endLateDeliveryWorkingState(sessionID, queryID, submission.ID, clientMessageID)
			return
		}
		submission.handle.resolveLate(status, body)
		log.Printf("[QUERY->LIVE] Background delivery for session %s completed (status=%d)", sessionID, status)
	}()
	return true
}

// startTurnAfterLateNoTarget starts the normal new turn for a /query whose live-input delivery, already answered
// 202, found no live target (handled=false: nothing was sent). It replays the request handleQuery validated
// (retainedQueueInputKey), or the session's last query, with live input off: never a second provider send, and it
// cannot come back here. The chat submission in the context is reused when the message is unchanged.
func (api *StreamingAPI) startTurnAfterLateNoTarget(w http.ResponseWriter, r *http.Request, sessionID, message string) bool {
	input, known := r.Context().Value(retainedQueueInputKey{}).(retainedQueueInput)
	req := input.Request
	if !known {
		api.lastQueryMu.RLock()
		req, known = api.lastQueryRequests[sessionID]
		api.lastQueryMu.RUnlock()
		// The session's last request may have come from a schedule, a bot or another agent; this message is the person's.
		req.TriggeredBy, req.BotPlatform, req.AgentMessageInbox, req.SessionKind = "", "", "", ""
	}
	if !known {
		// No base request to start the turn from: the message stays uncertain.
		log.Printf("[QUERY->LIVE] Late no-target delivery for session %s has no base request; leaving it uncertain", sessionID)
		return false
	}
	req.Query, req.Message = message, message
	req.DisableLiveInputDelivery = true
	req.IsAutoNotification = false
	payload, err := json.Marshal(req)
	if err != nil {
		return false
	}
	next, err := http.NewRequestWithContext(context.WithoutCancel(r.Context()), http.MethodPost, "/api/query", bytes.NewReader(payload))
	if err != nil {
		return false
	}
	next.Header = r.Header.Clone()
	next.Header.Set("Content-Type", "application/json")
	next.Header.Set("X-Session-ID", sessionID)
	if submission, ok := r.Context().Value(chatSubmissionContextKey{}).(chatSubmissionContext); ok && submission.Message != message {
		// Recovered queue text was joined in: this is a different message from the original submission.
		next.Header.Del("Idempotency-Key")
	}
	if api.internalQueryHandler != nil {
		api.internalQueryHandler(w, next)
	} else {
		api.handleQuery(w, next)
	}
	return true
}

// endLateDeliveryWorkingState ends the browser's working state that this failed submission's 202 started. It is
// scoped to that submission: it runs only when no turn of any kind is running in the chat (a live turn there
// keeps its own state), and the terminal event names this query and message.
func (api *StreamingAPI) endLateDeliveryWorkingState(sessionID, queryID, submissionID, clientMessageID string) {
	if api == nil || api.eventStore == nil {
		return
	}
	if api.hasActiveTurnCancel(sessionID) || api.isSessionBusy(sessionID) || api.retainedMainTurnTracked(sessionID) {
		log.Printf("[QUERY->LIVE] Late delivery failed for session %s query_id=%s; a turn is running, its working state stays", sessionID, queryID)
		return
	}
	now := time.Now()
	failure := unifiedevents.NewUnifiedCompletionEventWithError("server", "chat", "", "Your message was not delivered. Please send it again.", 0, 0)
	failure.Metadata["source"] = "late_delivery_failed"
	failure.Metadata["query_id"] = queryID
	failure.Metadata["submission_id"] = submissionID
	failure.Metadata["client_message_id"] = clientMessageID
	agentEvent := unifiedevents.NewAgentEvent(failure)
	agentEvent.SessionID = sessionID
	api.eventStore.AddEvent(sessionID, events.Event{
		ID: "late-delivery-failed-" + queryID, Type: string(unifiedevents.EventTypeUnifiedCompletion), Timestamp: now,
		SessionID: sessionID, ExecutionID: queryID, ExecutionKind: "main_agent", TerminalOwnerID: "main:" + sessionID, Data: agentEvent,
	})
	log.Printf("[QUERY->LIVE] Late delivery failed for idle session %s query_id=%s submission=%s; ended its working state", sessionID, queryID, submissionID)
}

// liveInputFastResponseWindow bounds how long a /query waits on live-input
// delivery before answering "accepted"; well under CloudFront's 30s origin
// timeout. A var so tests can shorten it.
var liveInputFastResponseWindow = 12 * time.Second

// liveInputDeliveryInProgress is the delivery_status of an early "accepted"
// reply whose send is still running in the background.
const liveInputDeliveryInProgress = "delivering"

// copyInternalResponse replays a buffered response onto the real writer.
func copyInternalResponse(w http.ResponseWriter, recorded *internalResponseCapture) {
	for key, values := range recorded.header {
		w.Header()[key] = values
	}
	status := recorded.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(recorded.body.Bytes())
}

// deliverQueryAsLiveInputNow performs the delivery synchronously; see
// tryDeliverQueryAsLiveInput for the response-time bound around it.
func (api *StreamingAPI) deliverQueryAsLiveInputNow(w http.ResponseWriter, r *http.Request, sessionID, message, queryID string, recovered *[]string, receivedAt ...time.Time) (handled bool) {
	if refusal := chatTerminalTurnRefusal(sessionID); refusal != "" {
		externalError(w, 409, "continued_in_terminal", refusal)
		return true
	}
	attribution, _ := r.Context().Value(deliveredAgentMessageKey{}).(map[string]interface{})
	// Not delivered: the caller starts a new turn under the same query ID, and that turn keeps the before-snapshot
	// prepared here. Abandoning it marked every such turn's snapshot unavailable (server B 2026-10-28, PLAT-746-158).
	defer func() {
		if handled {
			api.discardUnstartedCrewSnapshot(queryID)
		}
	}()
	if isQueuedMessageSteer(r.Context()) {
		// The asynchronous fast-response path can outlive the route's earlier
		// eligibility check. Refuse before attempting any provider delivery.
		if _, canSteer := api.queuedMessageDeliveryState(sessionID, QueryRequest{}); !canSteer {
			rejectUnavailableQueuedSteer(w)
			return true
		}
	}
	requestReceivedAt := time.Now()
	if len(receivedAt) > 0 && !receivedAt[0].IsZero() {
		requestReceivedAt = receivedAt[0]
	}
	// The mcpagent Session, not this host's short-lived running-Agent map or a
	// provider-specific terminal branch, owns warm delivery. It survives the
	// wrapped turn and reports the transport that actually accepted the input.
	tryColdRetainedFallback := true
	if sessionSecretsChanged(sessionID) {
		// The CLI still holds the secrets it launched with; typing into it would run on the old values (server B
		// 2026-10-07: rotated Unipile keys "not picked up"). A new turn relaunches it with the current ones.
		log.Printf("[QUERY->LIVE] Secrets changed since session %s launched; starting a new turn instead of steering", sessionID)
		return false
	}
	if retainedSession, ok := mcpagent.LookupSession(sessionID); ok {
		if api.closeIdleRetainedTmuxSessionWithoutLiveTerminal(sessionID, retainedSession) {
			// Continue through cold-terminal compatibility and then the normal
			// new-turn path. No send was attempted, so delivery is not uncertain.
		} else {
			deliveryStartedAt := time.Now()
			if api.queueIdleRetainedInput(w, r, sessionID, retainedSession.ActiveTurnID() != "") {
				return true
			}
			if retainedSession.ActiveTurnID() == "" {
				api.prepareCrewTurnSnapshot(queryID, sessionID, QueryRequest{})
			}
			// The capture is bounded on its own; it must not use up the delivery time.
			sessionInputCtx, sessionInputCancel := context.WithTimeout(r.Context(), liveCodingAgentDeliveryTimeout)
			// The message's identity goes with it: if it has to wait for the turn's next step, the turn records it
			// once under this identity, matching the person's bubble (PLAT-872-59).
			delivery, err := retainedSession.SendIdentified(sessionInputCtx, withSessionMode(sessionModeFromContext(r.Context()), message),
				mcpagent.QueuedMessageIdentity{ID: newSteerMessageID(), ClientID: clientMessageIDFromContext(r.Context())})
			sessionInputCancel()
			if err != nil {
				if api.queueDeferredChatImage(w, r, sessionID, err) {
					return true
				}
				if liveInputErrorProvesNoTarget(err) {
					api.recoverUnsentLiveInput(sessionID, err, recovered)
					// The CLI has no turn, so a retained mark left by earlier live
					// input can never settle; it would queue this message forever.
					api.releaseStaleRetainedMainTurn(sessionID, "no_active_session")
					log.Printf("[QUERY->LIVE] No live target for session %s; starting a new turn: %v", sessionID, err)
					return false
				}
				log.Printf("[QUERY->LIVE] Delivery uncertain for session %s: %v", sessionID, err)
				writeLiveInputUncertain(w, r.Header.Get("Idempotency-Key"), err)
				return true
			} else if delivery.Status == mcpagent.UserMessageDeliveryStatusSentToCLI {
				provider := string(delivery.Provider)
				noteLiveInputDelivered(sessionID, string(delivery.Provider), message, withSessionMode(sessionModeFromContext(r.Context()), message))
				messageID := api.recordAttributedRetainedLiveInput(attribution, sessionID, message, provider, clientMessageIDFromContext(r.Context()), true, delivery.TurnID, deliveryStartedAt, queryID)
				api.persistLiveInputUserMessage(r.Context(), sessionID, message)
				w.Header().Set("Content-Type", "application/json")
				response := QueryResponse{
					QueryID: queryID, SessionID: sessionID,
					Status: queryStatusLiveInputDelivered, Message: "Delivered to retained coding-agent session",
					DeliveryStatus: string(delivery.Status), Provider: provider,
					DeliveryTransport: string(delivery.Transport), DeliverySource: queryDeliverySourceMCPAgentSession,
					MessageID: messageID,
				}
				api.stampCodingAgentDeliveryTiming(r, &response, requestReceivedAt)
				_ = json.NewEncoder(w).Encode(response)
				log.Printf("[QUERY->LIVE] session=%s query=%s provider=%s transport=%s action=steered reason=durable_native_input", sessionID, queryID, provider, delivery.Transport)
				return true
			} else {
				// Queueing is an accepted non-tmux submission. It is not evidence that
				// the durable session is broken, so do not bypass mcpagent via tmux.
				// Send accepted a non-CLI queue entry. Do not inject it again.
				log.Printf("[QUERY->LIVE] session=%s query=%s provider=%s transport=%s action=queued reason=%s", sessionID, queryID, delivery.Provider, delivery.Transport, delivery.Status)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "status": "accepted", "session_id": sessionID, "delivery_status": string(delivery.Status), "provider": string(delivery.Provider)})
				return true
			}
		}
	}
	if tryColdRetainedFallback {
		// Transitional cold-restart compatibility: a provider tmux from the
		// previous server process can outlive the in-memory Session registry. It
		// also recovers a stale/closed warm Session without paying for full Agent
		// reconstruction, preserving PLAT-102's latency guarantee.
		if api.conversationTurnHasBacklog(GetUserIDFromContext(r.Context()), sessionID) && api.queueIdleRetainedInput(w, r, sessionID, !retainedCLIAtPrompt(api, sessionID)) {
			return true
		}
		if !api.isSessionBusy(sessionID) {
			api.prepareCrewTurnSnapshot(queryID, sessionID, QueryRequest{})
		}
		fallbackCtx, fallbackCancel := context.WithTimeout(r.Context(), liveCodingAgentDeliveryTimeout)
		retainedProvider, handled, err := api.deliverRetainedMainTerminalInput(fallbackCtx, sessionID, withSessionMode(sessionModeFromContext(r.Context()), message))
		fallbackCancel()
		if handled {
			if err != nil {
				// The same proof the warm-session branch above accepts: the retained terminal is gone (Stop, a died CLI or a provider switch ended it), so nothing
				// was sent and a fresh turn is safe. Answering 409 delivery_uncertain here left every send, and each of its 30 s retries, rejected until the
				// stale record cleared (Code on server B, 2026-10-04: three 409s after a provider switch).
				if liveInputErrorProvesNoTarget(err) {
					api.recoverUnsentLiveInput(sessionID, err, recovered)
					log.Printf("[QUERY->LIVE] Retained terminal for session %s has no live target; starting a new turn: %v", sessionID, err)
					return false
				}
				log.Printf("[QUERY->LIVE] Retained-terminal fallback uncertain for session %s: %v", sessionID, err)
				writeLiveInputUncertain(w, r.Header.Get("Idempotency-Key"), err)
				return true
			}
			noteLiveInputDelivered(sessionID, retainedProvider, message, withSessionMode(sessionModeFromContext(r.Context()), message))
			messageID := api.recordAttributedRetainedLiveInput(attribution, sessionID, message, retainedProvider, clientMessageIDFromContext(r.Context()), false, "", time.Time{}, queryID)
			api.persistLiveInputUserMessage(r.Context(), sessionID, message)
			w.Header().Set("Content-Type", "application/json")
			response := QueryResponse{QueryID: queryID, SessionID: sessionID, Status: queryStatusLiveInputDelivered, Message: "Delivered to retained coding-agent CLI", DeliveryStatus: "sent_to_cli", Provider: retainedProvider, DeliveryTransport: "tmux", DeliverySource: queryDeliverySourceRetainedCompatibility, MessageID: messageID}
			api.stampCodingAgentDeliveryTiming(r, &response, requestReceivedAt)
			_ = json.NewEncoder(w).Encode(response)
			log.Printf("[QUERY->LIVE] session=%s query=%s provider=%s transport=tmux action=steered reason=retained_terminal", sessionID, queryID, retainedProvider)
			return true
		}
	}

	api.runningAgentsMux.RLock()
	runningAgent := api.runningAgents[sessionID]
	api.runningAgentsMux.RUnlock()
	if !agentSupportsLiveInputDelivery(runningAgent) {
		return false
	}
	hasActiveForegroundTurn := api.hasActiveTurnCancel(sessionID)
	if !hasActiveForegroundTurn && !api.sessionHasLiveMainCodingTmux(sessionID) {
		log.Printf("[QUERY→LIVE] Retained CLI agent for session %s has no active turn and no live tmux; starting a new turn", sessionID)
		return false
	}

	if err := r.Context().Err(); err != nil {
		log.Printf("[QUERY→LIVE] Request canceled before delivery for session %s: %v", sessionID, err)
		return false
	}

	// The message's identity exists before delivery: a queued message keeps it and is recorded once, where
	// the agent takes it, so the chat matches the person's bubble instead of adding a second (PLAT-872).
	messageID := newSteerMessageID()
	if !hasActiveForegroundTurn {
		api.prepareCrewTurnSnapshot(queryID, sessionID, QueryRequest{})
	}
	inputCtx, cancel := context.WithTimeout(r.Context(), liveCodingAgentDeliveryTimeout)
	defer cancel()
	delivery, err := api.deliverRunningAgentUserMessage(inputCtx, runningAgent, mcpagent.UserMessageDeliveryRequest{
		SessionID:       sessionID,
		Message:         message,
		Intent:          mcpagent.UserMessageDeliveryIntentLiveInput,
		MessageID:       messageID,
		ClientMessageID: clientMessageIDFromContext(r.Context()),
	})
	if err != nil {
		if api.queueDeferredChatImage(w, r, sessionID, err) {
			return true
		}
		if liveInputErrorProvesNoTarget(err) {
			api.recoverUnsentLiveInput(sessionID, err, recovered)
			log.Printf("[QUERY→LIVE] No live target for session %s; starting a new turn: %v", sessionID, err)
			return false
		}
		// Transport errors do not prove the CLI did not receive the input.
		// Keep the accepted submission uncertain instead of dispatching twice.
		log.Printf("[QUERY→LIVE] Delivery uncertain for session %s: %v", sessionID, err)
		writeLiveInputUncertain(w, r.Header.Get("Idempotency-Key"), err)
		return true
	}
	if delivery.DeliveryStatus == mcpagent.UserMessageDeliveryStatusQueuedForInjection {
		// Same accepted shape as the retained-session queue branch above:
		// the running turn owns this message at its next boundary and records it there under its identity.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "status": "accepted", "session_id": sessionID, "delivery_status": string(delivery.DeliveryStatus), "provider": string(delivery.Provider)})
		return true
	}
	if delivery.DeliveryStatus != mcpagent.UserMessageDeliveryStatusSentToCLI {
		log.Printf("[QUERY→LIVE] Provider did not confirm CLI submission for session %s status=%s", sessionID, delivery.DeliveryStatus)
		writeLiveInputUncertain(w, r.Header.Get("Idempotency-Key"), err)
		return true
	}

	provider := string(delivery.Provider)
	if provider == "" {
		provider = string(mcpagent.ReadAgentRuntimeInfo(runningAgent).Provider)
	}
	deliveryStatus := string(delivery.DeliveryStatus)
	if deliveryStatus == "" {
		deliveryStatus = "queued_for_injection"
	}
	// Record the steered user message so it persists in the conversation timeline.
	// The chat UI dedups this against its optimistic bubble by exact content, so
	// there is no double message.
	if !hasActiveForegroundTurn {
		api.trackConversationTurnStart(queryID, sessionID, QueryRequest{Query: message, TriggeredBy: "interactive"})
		api.watchCrewTurnSnapshot(queryID)
		api.setSessionBusy(sessionID, false)
		api.markRetainedMainCodingTurnRunning(sessionID, queryID)
	}
	api.recordAttributedCodingAgentUserMessage(sessionID, message, provider, messageID, deliveryStatus, clientMessageIDFromContext(r.Context()), attribution)
	api.persistLiveInputUserMessage(r.Context(), sessionID, message)
	log.Printf("[QUERY→LIVE] Delivered /api/query message to retained CLI for session %s status=%s: %.80s", sessionID, deliveryStatus, message)

	w.Header().Set("Content-Type", "application/json")
	response := QueryResponse{
		QueryID:           queryID,
		SessionID:         sessionID,
		Status:            queryStatusLiveInputDelivered,
		Message:           "Delivered to retained coding-agent CLI",
		DeliveryStatus:    deliveryStatus,
		Provider:          provider,
		DeliveryTransport: "tmux",
		DeliverySource:    queryDeliverySourceRunningAgent,
		MessageID:         messageID,
	}
	api.stampCodingAgentDeliveryTiming(r, &response, requestReceivedAt)
	_ = json.NewEncoder(w).Encode(response)
	return true
}

// handleLiveInputMessage delivers a user message to a session. Retained
// tmux-transport coding CLIs can accept the message directly; API/LLM agents
// still require a foreground turn or a normal next-turn fallback.
func (api *StreamingAPI) handleLiveInputMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	vars := mux.Vars(r)
	sessionID := vars["session_id"]
	if sessionID == "" {
		http.Error(w, "Session ID is required", http.StatusBadRequest)
		return
	}
	if !api.canAccessTerminalSession(r, sessionID) {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}
	if refusal := codeSessionTurnRefusal(sessionID, GetUserFromContext(r.Context())); refusal != "" {
		http.Error(w, refusal, http.StatusForbidden)
		return
	}

	var req LiveInputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Message == "" {
		http.Error(w, "Message is required", http.StatusBadRequest)
		return
	}
	api.waitForIdleCompaction(r.Context(), sessionID, "live_input")

	if req.SubmissionID != "" && r.Header.Get("Idempotency-Key") == "" {
		r.Header.Set("Idempotency-Key", req.SubmissionID)
	}
	api.sessionWorkspaceMu.RLock()
	submissionProject, projectKnown := api.sessionWorkspaceFolders[sessionID]
	api.sessionWorkspaceMu.RUnlock()
	if !projectKnown {
		api.lastQueryMu.RLock()
		previousQuery, known := api.lastQueryRequests[sessionID]
		api.lastQueryMu.RUnlock()
		if known {
			submissionProject = previousQuery.SelectedFolder
			projectKnown = true
		}
	}
	if !projectKnown {
		r = r.WithContext(context.WithValue(r.Context(), chatSubmissionUnknownProjectKey{}, true))
	}
	var finishSubmission func()
	var accepted bool
	w, r, finishSubmission, accepted = api.beginChatSubmission(w, r, sessionID, submissionProject, req.Message)
	if !accepted {
		return
	}
	defer finishSubmission()

	api.lastQueryMu.RLock()
	policyRequest, policyKnown := api.lastQueryRequests[sessionID]
	api.lastQueryMu.RUnlock()
	// Live input is a turn too: re-check the session's account for the
	// person sending it (sharing changes apply from the next turn).
	if policyKnown {
		if provider, connectionID := queryTurnConnection(policyRequest); provider != "" && connectionID != "" {
			sender := GetUserIDFromContext(r.Context())
			scope := providerAccountScope{Principal: sender, WorkspacePath: policyRequest.SelectedFolder, Product: policyRequest.AgentProfileID}
			if _, accountErr := api.admitProviderAccount(r.Context(), scope, provider, connectionID); accountErr != nil {
				http.Error(w, accountErr.Error(), http.StatusForbidden)
				return
			}
		}
	}
	if !policyKnown && strings.HasPrefix(submissionProject, "Workflow/") {
		http.Error(w, "Workflow session configuration is unavailable; reopen the workflow chat", http.StatusConflict)
		return
	}
	if policyKnown {
		compatible, runtimeChanged, policyErr := api.workflowRetainedCompatibility(r.Context(), sessionID, policyRequest)
		if policyErr != nil {
			http.Error(w, policyErr.Error(), http.StatusForbidden)
			return
		}
		if !compatible {
			if runtimeChanged {
				queuedRequest := policyRequest
				queuedRequest.Query = req.Message
				queuedRequest.Message = ""
				queuedRequest.TriggeredBy = "interactive"
				queuedRequest.IsAutoNotification = false
				if api.queueOccupiedConversationTurn(w, r, GetUserIDFromContext(r.Context()), sessionID, queuedRequest) {
					return
				}
			}
			api.interruptWorkflowPolicySession(sessionID, policyRequest.Provider)
			r = r.WithContext(context.WithValue(r.Context(), workflowPolicyRefreshKey{}, true))
			if api.startNextTurnFromLiveInput(w, r, sessionID, req.Message, nil) {
				return
			}
			http.Error(w, "Unable to refresh workflow session", http.StatusConflict)
			return
		}
	}
	// /live-input is only a transport optimization; it must not bypass the
	// conversation dispatcher. Policy compatibility is checked first because a
	// changed Workflow authority must cancel/relaunch the stale runtime now,
	// rather than waiting behind it.
	if policyKnown && shouldUseDurableConversationTurnQueue(policyRequest) &&
		(api.conversationTurnOccupied(sessionID) || api.conversationTurnHasBacklog(GetUserIDFromContext(r.Context()), sessionID)) {
		queuedRequest := policyRequest
		queuedRequest.Query = req.Message
		queuedRequest.Message = ""
		queuedRequest.TriggeredBy = "interactive"
		queuedRequest.IsAutoNotification = false
		api.queueLiveInputTurnResponse(w, r, GetUserIDFromContext(r.Context()), sessionID, queuedRequest)
		return
	}

	if policyKnown {
		queuedRequest := policyRequest
		queuedRequest.Query, queuedRequest.Message = req.Message, ""
		queuedRequest.TriggeredBy, queuedRequest.IsAutoNotification = "interactive", false
		r = r.WithContext(context.WithValue(r.Context(), retainedQueueInputKey{}, retainedQueueInput{Request: queuedRequest, LiveInput: true}))
	}

	snapshotExecutionID := "live-turn:" + newSteerMessageID()
	defer api.discardUnstartedCrewSnapshot(snapshotExecutionID)
	// One durable mcpagent Session owns warm delivery across transports and
	// remains addressable after the wrapped Go turn has completed.
	tryColdRetainedFallback := true
	if retainedSession, ok := mcpagent.LookupSession(sessionID); ok {
		if api.closeIdleRetainedTmuxSessionWithoutLiveTerminal(sessionID, retainedSession) {
			// Continue through cold-terminal compatibility and then start the
			// resumed turn. No provider delivery was attempted.
		} else {
			deliveryStartedAt := time.Now()
			if api.queueIdleRetainedInput(w, r, sessionID, retainedSession.ActiveTurnID() != "") {
				return
			}
			if retainedSession.ActiveTurnID() == "" {
				api.prepareCrewTurnSnapshot(snapshotExecutionID, sessionID, policyRequest)
			}
			// The capture is bounded on its own; it must not use up the delivery time.
			sessionInputCtx, sessionInputCancel := context.WithTimeout(r.Context(), liveCodingAgentDeliveryTimeout)
			delivery, err := retainedSession.SendIdentified(sessionInputCtx, req.Message,
				mcpagent.QueuedMessageIdentity{ID: newSteerMessageID(), ClientID: clientMessageIDFromContext(r.Context())})
			sessionInputCancel()
			if err != nil {
				if api.queueDeferredChatImage(w, r, sessionID, err) {
					return
				}
				if liveInputErrorProvesNoTarget(err) {
					var pending []string
					api.recoverUnsentLiveInput(sessionID, err, &pending)
					if api.startNextTurnFromLiveInput(w, r, sessionID, strings.Join(append(pending, req.Message), "\n\n"), nil) {
						return
					}
					http.Error(w, "No active turn; please send your message again", http.StatusConflict)
					return
				}
				log.Printf("[LIVE INPUT] Delivery uncertain for session %s: %v", sessionID, err)
				writeSubmissionUncertain(w, r.Header.Get("Idempotency-Key"))
				return
			} else if delivery.Status == mcpagent.UserMessageDeliveryStatusSentToCLI {
				provider := string(delivery.Provider)
				messageID := api.recordMCPAgentSessionLiveInputAt(sessionID, req.Message, provider, clientMessageIDFromContext(r.Context()), delivery.TurnID, deliveryStartedAt, snapshotExecutionID)
				writeRetainedTerminalLiveInputResponseWithMessageID(w, sessionID, provider, messageID)
				log.Printf("[LIVE INPUT] Delivered through durable mcpagent session=%s provider=%s transport=%s: %.80s", sessionID, provider, delivery.Transport, req.Message)
				api.persistLiveInputUserMessage(r.Context(), sessionID, req.Message)
				return
			} else {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(LiveInputResponse{Success: true, Message: "User message accepted by provider session", DeliveryStatus: string(delivery.Status), Provider: string(delivery.Provider)})
				return
			}
		}
	}
	if tryColdRetainedFallback {
		// Same cold-restart/stale-session compatibility as /api/query above.
		if api.conversationTurnHasBacklog(GetUserIDFromContext(r.Context()), sessionID) && api.queueIdleRetainedInput(w, r, sessionID, !retainedCLIAtPrompt(api, sessionID)) {
			return
		}
		if !api.isSessionBusy(sessionID) {
			api.prepareCrewTurnSnapshot(snapshotExecutionID, sessionID, policyRequest)
		}
		fallbackCtx, fallbackCancel := context.WithTimeout(r.Context(), liveCodingAgentDeliveryTimeout)
		retainedProvider, handled, err := api.deliverRetainedMainTerminalInput(fallbackCtx, sessionID, req.Message)
		fallbackCancel()
		if handled {
			if err != nil {
				writeSubmissionUncertain(w, r.Header.Get("Idempotency-Key"))
				return
			}
			messageID := api.recordRetainedTerminalLiveInput(sessionID, req.Message, retainedProvider, clientMessageIDFromContext(r.Context()), snapshotExecutionID)
			writeRetainedTerminalLiveInputResponseWithMessageID(w, sessionID, retainedProvider, messageID)
			api.persistLiveInputUserMessage(r.Context(), sessionID, req.Message)
			return
		}
	}

	// Look up the running agent for this session
	api.runningAgentsMux.RLock()
	runningAgent, exists := api.runningAgents[sessionID]
	api.runningAgentsMux.RUnlock()

	if !exists || runningAgent == nil {
		// There is no live, provider-addressable retained terminal. A normal new
		// turn is still appropriate when the previous pane truly exited.
		if api.startNextTurnFromLiveInput(w, r, sessionID, req.Message, nil) {
			return
		}
		http.Error(w, "No running agent for this session", http.StatusNotFound)
		return
	}
	hasActiveForegroundTurn := api.hasActiveTurnCancel(sessionID)
	if !hasActiveForegroundTurn && agentSupportsLiveInputDelivery(runningAgent) && !api.sessionHasLiveMainCodingTmux(sessionID) {
		log.Printf("[LIVE INPUT] Retained CLI agent for session %s has no active turn and no live tmux; starting next turn", sessionID)
		api.setSessionBusy(sessionID, false)
		if api.startNextTurnFromLiveInput(w, r, sessionID, req.Message, runningAgent) {
			return
		}
		http.Error(w, "No active foreground turn or live terminal for this session", http.StatusConflict)
		return
	}
	// Do not wait on the new-turn lane here. Live coding-agent input is ordered by
	// provider startup readiness and the per-tmux transaction broker; waiting on
	// request construction would turn a slow CLI launch into a blocked chat send.
	if !hasActiveForegroundTurn {
		// API/LLM agents still need a server-owned foreground turn. Retained
		// tmux-transport CLIs can accept the next message directly, whether their
		// pane currently looks busy or idle.
		if !agentSupportsLiveInputDelivery(runningAgent) {
			log.Printf("[LIVE INPUT] Rejecting stale live input for session %s: stored API/LLM agent has no foreground turn", sessionID)
			api.setSessionBusy(sessionID, false)
			hasRunningBackgroundAgents := api.bgAgentRegistry != nil && api.bgAgentRegistry.HasRunningAgents(sessionID)
			if !hasRunningBackgroundAgents && !api.isSyntheticTurn(sessionID) {
				api.updateSessionStatus(sessionID, "completed")
			}
			if api.startNextTurnFromLiveInput(w, r, sessionID, req.Message, runningAgent) {
				return
			}
			http.Error(w, "No active foreground turn for this session", http.StatusConflict)
			return
		}
		log.Printf("[LIVE INPUT] No foreground turn for session %s — delivering message to retained CLI", sessionID)
		api.setSessionBusy(sessionID, false)
	}

	if err := r.Context().Err(); err != nil {
		log.Printf("[LIVE INPUT] Request canceled before delivery for session %s: %v", sessionID, err)
		return
	}

	messageID := newSteerMessageID()
	if !hasActiveForegroundTurn {
		api.prepareCrewTurnSnapshot(snapshotExecutionID, sessionID, policyRequest)
	}
	inputCtx, cancel := context.WithTimeout(r.Context(), liveCodingAgentDeliveryTimeout)
	defer cancel()
	delivery, err := api.deliverRunningAgentUserMessage(inputCtx, runningAgent, mcpagent.UserMessageDeliveryRequest{
		SessionID:       sessionID,
		Message:         req.Message,
		Intent:          mcpagent.UserMessageDeliveryIntentLiveInput,
		MessageID:       messageID,
		ClientMessageID: clientMessageIDFromContext(r.Context()),
	})
	if err != nil {
		if api.queueDeferredChatImage(w, r, sessionID, err) {
			return
		}
		log.Printf("[LIVE INPUT] Live input unavailable for provider %s session %s: %v", mcpagent.ReadAgentRuntimeInfo(runningAgent).Provider, sessionID, err)
		// The agent refused because no turn is running to take the message:
		// nothing was delivered, so this is definite, not uncertain. Start
		// the next turn with it, as /api/query does (liveInputErrorProvesNoTarget).
		if liveInputErrorProvesNoTarget(err) {
			var pending []string
			api.recoverUnsentLiveInput(sessionID, err, &pending)
			req.Message = strings.Join(append(pending, req.Message), "\n\n")
			if api.startNextTurnFromLiveInput(w, r, sessionID, req.Message, runningAgent) {
				return
			}
			http.Error(w, "No active foreground turn for this session", http.StatusConflict)
			return
		}
		writeSubmissionUncertain(w, r.Header.Get("Idempotency-Key"))
		return
	}
	if agentSupportsLiveInputDelivery(runningAgent) && delivery.DeliveryStatus != mcpagent.UserMessageDeliveryStatusSentToCLI {
		log.Printf("[LIVE INPUT] Provider did not confirm CLI submission for session %s status=%s", sessionID, delivery.DeliveryStatus)
		writeSubmissionUncertain(w, r.Header.Get("Idempotency-Key"))
		return
	}

	provider := string(delivery.Provider)
	if provider == "" {
		provider = string(mcpagent.ReadAgentRuntimeInfo(runningAgent).Provider)
	}
	deliveryStatus := string(delivery.DeliveryStatus)
	if deliveryStatus == "" {
		deliveryStatus = "queued_for_injection"
	}
	// A queued message is recorded by the turn that takes it, under this identity; recording it here too
	// showed it twice (PLAT-872).
	if delivery.DeliveryStatus != mcpagent.UserMessageDeliveryStatusQueuedForInjection {
		api.recordLiveCodingAgentUserMessage(sessionID, req.Message, provider, messageID, deliveryStatus, clientMessageIDFromContext(r.Context()))
		api.persistLiveInputUserMessage(r.Context(), sessionID, req.Message)
	}
	if !hasActiveForegroundTurn {
		executionID := snapshotExecutionID
		api.trackConversationTurnStart(executionID, sessionID, QueryRequest{Query: req.Message, TriggeredBy: "interactive"})
		api.watchCrewTurnSnapshot(executionID)
		api.markRetainedMainCodingTurnRunning(sessionID, executionID)
	}
	log.Printf("[LIVE INPUT] Delivered user message to provider %s session %s status=%s: %.80s", provider, sessionID, deliveryStatus, req.Message)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(LiveInputResponse{
		Success:        true,
		Message:        "User message delivered",
		DeliveryStatus: deliveryStatus,
		Provider:       provider,
		MessageID:      messageID,
	})
}

func newSteerMessageID() string {
	return fmt.Sprintf("steer-message-%d", time.Now().UnixNano())
}

func (api *StreamingAPI) deliverRunningAgentUserMessage(ctx context.Context, runningAgent *mcpagent.Agent, req mcpagent.UserMessageDeliveryRequest) (mcpagent.UserMessageDeliveryResult, error) {
	if api.internalUserMessageDeliveryHandler != nil {
		return api.internalUserMessageDeliveryHandler(ctx, runningAgent, req)
	}
	return mcpagent.DeliverAgentInput(ctx, runningAgent, req)
}

type internalResponseCapture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

// queryRequestWithEffectiveRuntime records the provider/model that actually
// launched the retained coding CLI. The incoming request may carry tab state
// from before Update Automation changed workflow.json, while finalProvider and
// finalModelID have already been resolved from the current manifest.
func queryRequestWithEffectiveRuntime(req QueryRequest, provider, modelID string) QueryRequest {
	provider = strings.TrimSpace(provider)
	modelID = strings.TrimSpace(modelID)
	if provider == "" && modelID == "" {
		return req
	}
	req.Provider = provider
	req.ModelID = modelID
	if req.LLMConfig != nil {
		configCopy := *req.LLMConfig
		configCopy.Primary = req.LLMConfig.Primary
		configCopy.Primary.Provider = provider
		configCopy.Primary.ModelID = modelID
		req.LLMConfig = &configCopy
	}
	return req
}

func (c *internalResponseCapture) Header() http.Header {
	if c.header == nil {
		c.header = make(http.Header)
	}
	return c.header
}

func (c *internalResponseCapture) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}

func (c *internalResponseCapture) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.body.Write(p)
}

func (api *StreamingAPI) startNextTurnFromLiveInput(w http.ResponseWriter, r *http.Request, sessionID, message string, runningAgent *mcpagent.Agent) bool {
	api.lastQueryMu.RLock()
	baseReq, ok := api.lastQueryRequests[sessionID]
	api.lastQueryMu.RUnlock()
	if !ok {
		return false
	}

	if refresh, _ := r.Context().Value(workflowPolicyRefreshKey{}).(bool); refresh {
		baseReq.DisableLiveInputDelivery = true
	}
	baseReq.Query = message
	baseReq.Message = message
	baseReq.IsAutoNotification = false
	baseReq.userID = GetUserIDFromContext(r.Context())

	payload, err := json.Marshal(baseReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to prepare next turn: %v", err), http.StatusInternalServerError)
		return true
	}

	// The live-input request must be free to return as soon as the next turn is
	// accepted. The queued turn can legitimately outlive that request while it
	// waits for the current per-session input lane, so preserve request values
	// without inheriting its cancellation or deadline.
	nextTurnCtx := context.WithoutCancel(r.Context())
	nextReq, err := http.NewRequestWithContext(nextTurnCtx, http.MethodPost, "/api/query", bytes.NewReader(payload))
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to prepare next turn request: %v", err), http.StatusInternalServerError)
		return true
	}
	nextReq.Header = r.Header.Clone()
	nextReq.Header.Set("Content-Type", "application/json")
	nextReq.Header.Set("X-Session-ID", sessionID)

	provider := ""
	if runningAgent != nil {
		provider = string(mcpagent.ReadAgentRuntimeInfo(runningAgent).Provider)
	}
	if provider == "" {
		provider = baseReq.Provider
	}
	messageID := newSteerMessageID()

	// handleQuery takes ownership of the session input lane for the lifetime of
	// a turn. Calling it inline here made POST /live-input wait for the previous
	// turn to finish (73 seconds in the observed regression), leaving the draft
	// stuck on "Sending" and allowing duplicate submissions. Dispatch the
	// server-owned continuation independently and acknowledge it immediately.
	go func() {
		recorder := &internalResponseCapture{header: make(http.Header)}
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("[LIVE INPUT] Queued next turn panicked for session %s message_id=%s: %v", sessionID, messageID, recovered)
			}
		}()
		if api.internalQueryHandler != nil {
			api.internalQueryHandler(recorder, nextReq)
		} else {
			api.handleQuery(recorder, nextReq)
		}
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		if status >= http.StatusBadRequest {
			body := strings.TrimSpace(recorder.body.String())
			if body == "" {
				body = http.StatusText(status)
			}
			log.Printf("[LIVE INPUT] Queued next turn failed for session %s message_id=%s status=%d: %s", sessionID, messageID, status, body)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(LiveInputResponse{
		Success:        true,
		Message:        "Started next turn",
		DeliveryStatus: "next_turn_started",
		Provider:       provider,
		MessageID:      messageID,
	})
	return true
}

// handleControlKey injects a tmux control key (e.g. "Escape", "Enter", "Up",
// "Down") into a coding-agent session. The foreground Agent is preferred while
// a turn is running. Between turns, the interactive tmux process can remain
// alive after runningAgents has been cleared, so fall back to the terminal store.
func (api *StreamingAPI) handleControlKey(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	vars := mux.Vars(r)
	sessionID := vars["session_id"]
	if sessionID == "" {
		http.Error(w, "Session ID is required", http.StatusBadRequest)
		return
	}
	// Same admission as live input: guidance enters the session's next system
	// prompt and a control key types into its live agent, so only someone who
	// may drive this session can use them.
	if !api.canAccessTerminalSession(r, sessionID) {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}
	if refusal := codeSessionTurnRefusal(sessionID, GetUserFromContext(r.Context())); refusal != "" {
		http.Error(w, refusal, http.StatusForbidden)
		return
	}

	var req ControlKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(req.Key)
	if key == "" {
		http.Error(w, "Key is required", http.StatusBadRequest)
		return
	}
	if !llm.IsAllowedCodingAgentControlKey(key) {
		http.Error(w, fmt.Sprintf("Key %q is not allowed", key), http.StatusBadRequest)
		return
	}

	api.runningAgentsMux.RLock()
	runningAgent, exists := api.runningAgents[sessionID]
	api.runningAgentsMux.RUnlock()

	if err := r.Context().Err(); err != nil {
		log.Printf("[CONTROL] Request canceled before delivery for session %s: %v", sessionID, err)
		return
	}

	ctlCtx, cancel := context.WithTimeout(r.Context(), liveCodingAgentInputTimeout)
	defer cancel()
	if !exists || runningAgent == nil {
		provider, tmuxSession, err := api.deliverControlKeyToLiveMainTerminal(ctlCtx, sessionID, key)
		if err != nil {
			log.Printf("[CONTROL] Terminal fallback key %q unavailable for session %s tmux=%s: %v", key, sessionID, tmuxSession, err)
			http.Error(w, fmt.Sprintf("Control key unavailable: %v", err), http.StatusConflict)
			return
		}
		if tmuxSession == "" {
			http.Error(w, "No live coding-agent terminal for this session", http.StatusNotFound)
			return
		}
		log.Printf("[CONTROL] Delivered control key %q to completed live terminal session %s tmux=%s", key, sessionID, tmuxSession)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ControlKeyResponse{
			Success:  true,
			Message:  "Control key delivered to live terminal",
			Provider: provider,
			Key:      key,
		})
		return
	}

	result, err := mcpagent.DeliverAgentControlKey(ctlCtx, runningAgent, mcpagent.ControlKeyDeliveryRequest{
		SessionID: sessionID,
		Key:       key,
	})
	if err != nil {
		log.Printf("[CONTROL] Control key %q unavailable for provider %s session %s: %v", key, mcpagent.ReadAgentRuntimeInfo(runningAgent).Provider, sessionID, err)
		http.Error(w, fmt.Sprintf("Control key unavailable: %v", err), http.StatusConflict)
		return
	}

	provider := string(result.Provider)
	if provider == "" {
		provider = string(mcpagent.ReadAgentRuntimeInfo(runningAgent).Provider)
	}
	log.Printf("[CONTROL] Delivered control key %q to provider %s session %s", key, provider, sessionID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ControlKeyResponse{
		Success:  true,
		Message:  "Control key delivered",
		Provider: provider,
		Key:      key,
	})
}

// deliverControlKeyToLiveMainTerminal bridges the between-turn gap where the
// agent object is no longer registered but its interactive CLI pane is still
// alive. ListRaw retains process ownership independently of UI visibility.
func (api *StreamingAPI) deliverControlKeyToLiveMainTerminal(ctx context.Context, sessionID, key string) (provider, tmuxSession string, err error) {
	if api == nil || api.terminalStore == nil {
		return "", "", nil
	}
	for _, snapshot := range api.terminalStore.ListRaw(sessionID) {
		if !codingAgentSnapshotIsMainAgent(snapshot) {
			continue
		}
		tmuxSession = strings.TrimSpace(snapshot.TmuxSession)
		if tmuxSession == "" {
			continue
		}
		processState := strings.ToLower(strings.TrimSpace(snapshot.ProcessState))
		if processState == "closed" || processState == "stale" {
			continue
		}
		if err := sendTerminalKey(ctx, tmuxSession, key); err != nil {
			return "", tmuxSession, err
		}
		return strings.TrimSpace(snapshot.Status.ProviderLabel), tmuxSession, nil
	}
	return "", "", nil
}

func (api *StreamingAPI) recordLiveCodingAgentUserMessage(sessionID, message, provider, messageID, deliveryStatus, clientMessageID string) {
	api.recordAttributedCodingAgentUserMessage(sessionID, message, provider, messageID, deliveryStatus, clientMessageID, nil)
}

func (api *StreamingAPI) recordAttributedCodingAgentUserMessage(sessionID, message, provider, messageID, deliveryStatus, clientMessageID string, attribution map[string]interface{}) {
	api.recordAttributedCodingAgentUserMessageAt(time.Now(), sessionID, message, provider, messageID, deliveryStatus, clientMessageID, attribution)
}

// recordAttributedCodingAgentUserMessageAt is the same row stamped with the time the person sent it.
func (api *StreamingAPI) recordAttributedCodingAgentUserMessageAt(at time.Time, sessionID, message, provider, messageID, deliveryStatus, clientMessageID string, attribution map[string]interface{}) {
	message = strings.TrimSpace(message)
	if sessionID == "" || message == "" || api == nil || api.eventStore == nil {
		return
	}

	eventData := unifiedevents.NewUserMessageEvent(0, message, "user")
	eventData.Metadata = map[string]interface{}{
		"source":          "coding_agent_live_input",
		"provider":        provider,
		"agent_label":     providerDisplayLabel(provider),
		"message_id":      messageID,
		"delivery_status": deliveryStatus,
	}
	// Display-only fields from admitted delivery; queue IDs/status remain authoritative.
	for _, key := range []string{"source", "sender_label", "sender_kind", "sender_agent", "sender_channel", "sender_user_id", "display_content", "display_content_truncated", "agent_message_inbox", "agent_message_instructions", "shared_message_id", "system_notice_kind"} {
		if value, ok := attribution[key]; ok {
			eventData.Metadata[key] = value
		}
	}
	if deliveryStatus == "failed" {
		eventData.Metadata["confirmation"] = "failed"
		eventData.Metadata["delivery_error"] = queuedTurnFailureMessage
	}
	eventID := messageID
	if clientMessageID != "" {
		eventData.Metadata["client_message_id"] = clientMessageID
		eventID = clientUserMessageEventID(clientMessageID)
	}
	agentEvent := unifiedevents.NewAgentEvent(eventData)
	agentEvent.SessionID = sessionID
	agentEvent.Component = "coding_agent_live_input"

	event := events.Event{
		ID:        eventID,
		Type:      string(unifiedevents.UserMessage),
		Timestamp: at,
		Data:      agentEvent,
		SessionID: sessionID,
	}
	// Every accepted tmux live delivery funnels through here (running
	// agent, durable mcpagent session, cold-retained compatibility, and
	// background-agent steers), so the durability watch starts here too:
	// one choke point no future path can miss. When the provider can confirm
	// the CLI took the message, the row is recorded at that point so it sits
	// after any answer the CLI was still writing when the message was sent.
	if deliveryStatus == string(mcpagent.UserMessageDeliveryStatusSentToCLI) {
		if clientMessageID != "" && attribution["source"] != "agent_message" {
			api.noteRestartLivePersonInput(sessionID, messageID)
		}
		if liveInputHasDurableAck(provider) {
			api.watchLiveInputDurableRecording(sessionID, provider, messageID, message, clientMessageID, &event)
			return
		}
		api.eventStore.AddEvent(sessionID, event)
		return
	}
	api.eventStore.AddEvent(sessionID, event)
}

// liveInputStallWarnings are the waits (counted from the start of the delivery) at which a delivery that the coding agent
// has still not accepted is logged.
var liveInputStallWarnings = []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute}

// waitForLiveInputDelivery waits for a background delivery to finish and logs a [DELIVERY_STALL] line at each warning age
// while it has not. waited is how long the delivery had already run when this started.
func waitForLiveInputDelivery(done <-chan bool, sessionID, queryID string, waited time.Duration) bool {
	started := time.Now().Add(-waited)
	warnings := liveInputStallWarnings
	for {
		var next <-chan time.Time
		var timer *time.Timer
		if len(warnings) > 0 {
			timer = time.NewTimer(time.Until(started.Add(warnings[0])))
			next = timer.C
		}
		select {
		case handled := <-done:
			if timer != nil {
				timer.Stop()
			}
			return handled
		case <-next:
			log.Printf("[DELIVERY_STALL] session=%s query_id=%s the coding agent has not accepted the message after %s", sessionID, queryID, warnings[0])
			warnings = warnings[1:]
		}
	}
}
