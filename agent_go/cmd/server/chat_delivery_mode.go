package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// This is presentation and dispatch eligibility, never an authorization grant.
// handleQuery still revalidates the stored principal, project and provider.
func (api *StreamingAPI) queuedMessageDeliveryState(sessionID string, req QueryRequest) (string, bool) {
	api.conversationTurnQueueMu.Lock()
	refreshing := api.conversationTurnQueueRuntimeChange[sessionID]
	api.conversationTurnQueueMu.Unlock()
	if refreshing || req.RestartCodingCLI || sessionSecretsChanged(sessionID) || api.retainedCLIProviderDiffers(sessionID, queuedRequestProvider(req)) || api.launchedAgentProfileDiffers(sessionID) {
		return "refreshing", false
	}
	if api.externalBuilderOwnsSession(sessionID) {
		return "waiting", false
	}
	api.runningAgentsMux.RLock()
	agent := api.runningAgents[sessionID]
	api.runningAgentsMux.RUnlock()
	occupied := api.conversationTurnOccupied(sessionID)
	if agent == nil {
		// A retained native turn survives the foreground running-agent entry.
		// Use its accepted turn ownership plus the live transport, not merely
		// a retained pane or a coding-provider capability flag.
		if api.hasRetainedQueuedSteerTurn(sessionID) {
			if snapshot, live := api.liveMainCodingTmuxSnapshot(sessionID); live {
				provider := retainedCodingAgentProvider(snapshot)
				contract, coding := codingAgentProviderContract(provider, "")
				if coding && contract.SupportsLiveInput {
					if !api.liveSteerProviderTransportReady(sessionID, provider) {
						return "starting", false
					}
					return "running", !req.DisableLiveInputDelivery
				}
			}
		}
		if occupied {
			return "starting", false
		}
		return "waiting", false
	}
	if agentSupportsLiveInputDelivery(agent) && occupied && !api.liveSteerTransportReady(sessionID, agent) {
		return "starting", false
	}
	if !occupied {
		return "waiting", false
	}
	return "running", !req.DisableLiveInputDelivery && agentSupportsLiveInputDelivery(agent) && api.canSteerSession(sessionID)
}

func (api *StreamingAPI) hasRetainedQueuedSteerTurn(sessionID string) bool {
	if session, ok := mcpagent.LookupSession(sessionID); ok && session.ActiveTurnID() != "" {
		return true
	}
	api.retainedMainTurnsMu.Lock()
	_, accepted := api.retainedMainTurns[sessionID]
	api.retainedMainTurnsMu.Unlock()
	return accepted
}

func queuedRequestProvider(req QueryRequest) string {
	if req.LLMConfig != nil && req.LLMConfig.Primary.Provider != "" {
		return req.LLMConfig.Primary.Provider
	}
	return req.Provider
}

func (api *StreamingAPI) projectQueuedMessageDelivery(sessionID string, turns []queuedConversationTurn, messages []PendingQueuedMessage) {
	byID := make(map[string]queuedConversationTurn, len(turns))
	for _, turn := range turns {
		byID[turn.ID] = turn
	}
	for i := range messages {
		message := &messages[i]
		turn, ok := byID[message.MessageID]
		if !ok {
			continue
		}
		message.QueueReason, message.CanSteer = api.queuedMessageDeliveryState(sessionID, turn.Request)
		message.Refreshing = message.QueueReason == "refreshing"
		// Steer now is only for a message the person queued on purpose (queue mode). In steer mode the server hands the
		// message in by itself as soon as it can, so offering the button there only adds a choice.
		if message.Started || message.Steering || message.DeliveryError != "" || turn.Request.DeliveryMode != "queue" {
			message.CanSteer = false
		}
	}
}

type queuedMessageSteerKey struct{}

var errQueuedSteerUnavailable = errors.New("Steer is not available; the message remains queued")

func isQueuedMessageSteer(ctx context.Context) bool {
	value, _ := ctx.Value(queuedMessageSteerKey{}).(bool)
	return value
}

func rejectUnavailableQueuedSteer(w http.ResponseWriter) {
	w.Header().Set("X-Queued-Steer-Unavailable", "true")
	http.Error(w, errQueuedSteerUnavailable.Error(), http.StatusConflict)
}

func validChatDeliveryMode(mode string) bool {
	return mode == "" || mode == "steer" || mode == "queue"
}

// The owner-scoped durable queue is authoritative, including after a reload.
// Return only presentation fields, never the stored request or principal.
func (api *StreamingAPI) handleQueuedConversationMessages(w http.ResponseWriter, r *http.Request) {
	userID := GetUserIDFromContext(r.Context())
	if claims := GetUserFromContext(r.Context()); userID == "" || (claims != nil && claims.AccessToken != nil) {
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return
	}
	sessionID := mux.Vars(r)["session_id"]
	// Read errors must not look like an empty queue and hide waiting messages.
	turns, err := api.readConversationTurnQueue(r.Context(), userID)
	if err != nil {
		http.Error(w, "Cannot read queued messages", http.StatusServiceUnavailable)
		return
	}
	messages := pendingQueuedMessagesFromTurns(turns, userID, sessionID)
	if messages == nil {
		messages = []PendingQueuedMessage{}
	}
	api.projectQueuedMessageDelivery(sessionID, turns, messages)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"messages": messages})
}

func (api *StreamingAPI) changeQueuedConversationMessage(ctx context.Context, userID, sessionID, messageID string, steer bool) (queuedConversationTurn, int, error) {
	lock := productConversationRegistryMutex(conversationTurnQueuePath(userID))
	lock.Lock()
	defer lock.Unlock()
	turns, err := api.readConversationTurnQueue(ctx, userID)
	if err != nil {
		return queuedConversationTurn{}, http.StatusServiceUnavailable, err
	}
	for i, turn := range turns {
		if turn.ID != messageID || turn.UserID != userID || turn.SessionID != sessionID || sanitizeClientMessageID(turn.SubmissionID) == "" {
			continue
		}
		if turn.StartedAt != nil {
			// A retry of Steer now adopts the same dispatch; it never injects twice.
			if steer && turn.Steering {
				return turn, http.StatusAccepted, nil
			}
			return turn, http.StatusConflict, fmt.Errorf("This message has already started")
		}
		if steer {
			if turn.DeliveryError != "" {
				return turn, http.StatusConflict, fmt.Errorf("Delivery is unconfirmed. Check the chat before resending")
			}
			if _, canSteer := api.queuedMessageDeliveryState(sessionID, turn.Request); !canSteer {
				return turn, http.StatusConflict, errQueuedSteerUnavailable
			}
			now := time.Now().UTC()
			turns[i].StartedAt = &now
			turns[i].Steering = true
			turn = turns[i]
		} else {
			turns = append(turns[:i], turns[i+1:]...)
		}
		if err := api.writeConversationTurnQueue(ctx, userID, turns); err != nil {
			return turn, http.StatusServiceUnavailable, err
		}
		return turn, http.StatusOK, nil
	}
	return queuedConversationTurn{}, http.StatusNotFound, fmt.Errorf("Queued message not found")
}

func (api *StreamingAPI) queuedMessageAction(w http.ResponseWriter, r *http.Request, steer bool) {
	userID := GetUserIDFromContext(r.Context())
	claims := GetUserFromContext(r.Context())
	if userID == "" || (claims != nil && claims.AccessToken != nil) {
		http.Error(w, "A signed-in chat owner is required", http.StatusForbidden)
		return
	}
	vars := mux.Vars(r)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Second)
	defer cancel()
	turn, status, err := api.changeQueuedConversationMessage(ctx, userID, vars["session_id"], vars["message_id"], steer)
	if err != nil {
		logTurnQueue("session %s: queued action message=%s steer=%t status=%d reason=%v", vars["session_id"], vars["message_id"], steer, status, err)
		if errors.Is(err, errQueuedSteerUnavailable) {
			rejectUnavailableQueuedSteer(w)
			return
		}
		http.Error(w, err.Error(), status)
		return
	}
	if steer && status == http.StatusOK {
		go api.deliverQueuedMessageAsSteer(turn)
	} else if !steer {
		api.kickConversationTurnQueue(turn.SessionID)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"message_id": turn.ID, "accepted": true})
}

func (api *StreamingAPI) handleRemoveQueuedConversationMessage(w http.ResponseWriter, r *http.Request) {
	api.queuedMessageAction(w, r, false)
}

func (api *StreamingAPI) handleSteerQueuedConversationMessage(w http.ResponseWriter, r *http.Request) {
	api.queuedMessageAction(w, r, true)
}

// Retry is explicit because a failed execution may already have done some work.
// Keep the submission identity, but use a fresh dispatch/receipt identity.
func (api *StreamingAPI) retryQueuedConversationMessage(ctx context.Context, userID, sessionID, messageID string) (queuedConversationTurn, int, error) {
	lock := productConversationRegistryMutex(conversationTurnQueuePath(userID))
	lock.Lock()
	defer lock.Unlock()
	turns, err := api.readConversationTurnQueue(ctx, userID)
	if err != nil {
		return queuedConversationTurn{}, http.StatusServiceUnavailable, err
	}
	for i, turn := range turns {
		if turn.ID != messageID || turn.UserID != userID || turn.SessionID != sessionID || sanitizeClientMessageID(turn.SubmissionID) == "" {
			continue
		}
		if turn.StartedAt != nil || turn.Steering || turn.DeliveryError == "" {
			return turn, http.StatusConflict, fmt.Errorf("Only a failed queued message can be retried")
		}
		turn.ID, turn.DeliveryError = uuid.NewString(), ""
		turns[i] = turn
		if err := api.writeConversationTurnQueue(ctx, userID, turns); err != nil {
			return turn, http.StatusServiceUnavailable, err
		}
		return turn, http.StatusOK, nil
	}
	return queuedConversationTurn{}, http.StatusNotFound, fmt.Errorf("Queued message not found")
}

func (api *StreamingAPI) handleRetryQueuedConversationMessage(w http.ResponseWriter, r *http.Request) {
	userID := GetUserIDFromContext(r.Context())
	claims := GetUserFromContext(r.Context())
	if userID == "" || (claims != nil && claims.AccessToken != nil) {
		http.Error(w, "A signed-in chat owner is required", http.StatusForbidden)
		return
	}
	vars := mux.Vars(r)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Second)
	defer cancel()
	turn, status, err := api.retryQueuedConversationMessage(ctx, userID, vars["session_id"], vars["message_id"])
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	api.conversationTurnQueueMu.Lock()
	api.registerConversationQueueOwnerLocked(userID, turn.SessionID)
	api.conversationTurnQueueMu.Unlock()
	api.kickConversationTurnQueue(turn.SessionID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"message_id": turn.ID, "accepted": true})
}

// Claim first under the same lock the normal worker uses, then submit through
// handleQuery again. Its authorization and runtime checks remain authoritative;
// a runtime change may still need a new turn instead of live input.
func (api *StreamingAPI) deliverQueuedMessageAsSteer(turn queuedConversationTurn) {
	// The target can finish or change between the HTTP claim and this goroutine.
	// No delivery has been attempted: release the claim without an ambiguous error.
	if _, canSteer := api.queuedMessageDeliveryState(turn.SessionID, turn.Request); !canSteer {
		api.restoreQueuedSteer(turn)
		return
	}
	req := turn.Request
	req.DeliveryMode = "steer"
	reqMap, err := queryRequestToMap(req)
	if err == nil {
		var ctx context.Context
		ctx, err = api.queuedConversationTurnContext(turn, reqMap)
		if err == nil {
			ctx = context.WithValue(ctx, queuedMessageSteerKey{}, true)
			body, marshalErr := json.Marshal(req)
			err = marshalErr
			if err == nil {
				request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, "/api/query", bytes.NewReader(body))
				err = requestErr
				if err == nil {
					request.Header.Set("X-Session-ID", turn.SessionID)
					recorded := &internalResponseCapture{header: http.Header{}}
					api.handleQuery(recorded, request)
					if recorded.header.Get("X-Queued-Steer-Unavailable") == "true" {
						api.restoreQueuedSteer(turn)
						return
					}
					status := recorded.status
					if status == 0 {
						status = http.StatusOK
					}
					var response QueryResponse
					if decodeErr := json.Unmarshal(recorded.body.Bytes(), &response); decodeErr != nil || status >= 400 || strings.TrimSpace(response.Status) == "" {
						err = fmt.Errorf("Delivery was not confirmed. Check the chat before resending")
					}
				}
			}
		}
	}
	storageCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err == nil {
		if removeErr := api.removeConversationTurn(storageCtx, turn); removeErr != nil {
			logTurnQueue("cannot remove steered message %s: %v", turn.ID, removeErr)
		}
	} else {
		// Keep an ambiguous failure visible, but never auto-dispatch it again.
		lock := productConversationRegistryMutex(conversationTurnQueuePath(turn.UserID))
		lock.Lock()
		turns, readErr := api.readConversationTurnQueue(storageCtx, turn.UserID)
		if readErr == nil {
			for i := range turns {
				if turns[i].ID == turn.ID {
					turns[i].StartedAt = nil
					turns[i].Steering = false
					turns[i].DeliveryError = "Delivery unconfirmed. Check the chat before resending."
				}
			}
			if writeErr := api.writeConversationTurnQueue(storageCtx, turn.UserID, turns); writeErr != nil {
				logTurnQueue("cannot record failed steer %s: %v", turn.ID, writeErr)
			}
		}
		lock.Unlock()
		logTurnQueue("steer of queued message %s failed: %v", turn.ID, err)
	}
	api.kickConversationTurnQueue(turn.SessionID)
}

func (api *StreamingAPI) restoreQueuedSteer(turn queuedConversationTurn) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	lock := productConversationRegistryMutex(conversationTurnQueuePath(turn.UserID))
	lock.Lock()
	turns, err := api.readConversationTurnQueue(ctx, turn.UserID)
	if err == nil {
		for i := range turns {
			if turns[i].ID == turn.ID && turns[i].UserID == turn.UserID && turns[i].SessionID == turn.SessionID && turns[i].Steering {
				turns[i].StartedAt, turns[i].Steering = nil, false
			}
		}
		err = api.writeConversationTurnQueue(ctx, turn.UserID, turns)
	}
	lock.Unlock()
	if err != nil {
		logTurnQueue("cannot release unavailable steer %s: %v", turn.ID, err)
	}
	api.kickConversationTurnQueue(turn.SessionID)
}

// launchedAgentProfileDiffers reports that the product's setup changed since its CLI was launched (a function,
// skill or instruction edit): the next message relaunches the CLI after the current reply instead of going in live,
// so it waits as "updating this chat", not as an ordinary queue.
func (api *StreamingAPI) launchedAgentProfileDiffers(sessionID string) bool {
	api.conversationMux.RLock()
	defer api.conversationMux.RUnlock()
	current, known := api.lastAgentProfileKeyBySession[sessionID]
	launched, launchKnown := api.launchedAgentProfileKeyBySession[sessionID]
	return known && launchKnown && current != "" && launched != current
}
