package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

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

// Claim first under the same lock the normal worker uses, then submit through
// handleQuery again. Its authorization and runtime checks remain authoritative;
// a runtime change may still need a new turn instead of live input.
func (api *StreamingAPI) deliverQueuedMessageAsSteer(turn queuedConversationTurn) {
	req := turn.Request
	req.DeliveryMode = "steer"
	reqMap, err := queryRequestToMap(req)
	if err == nil {
		var ctx context.Context
		ctx, err = api.queuedConversationTurnContext(turn, reqMap)
		if err == nil {
			body, marshalErr := json.Marshal(req)
			err = marshalErr
			if err == nil {
				request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, "/api/query", bytes.NewReader(body))
				err = requestErr
				if err == nil {
					request.Header.Set("X-Session-ID", turn.SessionID)
					recorded := &internalResponseCapture{header: http.Header{}}
					api.handleQuery(recorded, request)
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
