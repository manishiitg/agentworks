package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

func (api *StreamingAPI) handleMakeAgentProfileMainChat(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var input struct {
		ProjectID       string `json:"project_id"`
		ConversationKey string `json:"conversation_key"`
		MainSessionID   string `json:"main_session_id"`
		SideSessionID   string `json:"side_session_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes)).Decode(&input); err != nil || strings.TrimSpace(input.MainSessionID) == "" || strings.TrimSpace(input.SideSessionID) == "" {
		writeAgentProfileError(w, http.StatusBadRequest, "send the project and both current chat bindings")
		return
	}
	userID, slot, ok := api.agentProfileConversationSlot(w, r, strings.TrimSpace(input.ProjectID))
	if !ok {
		return
	}
	result, err := defaultProductConversationRegistryStore().setMainChat(r.Context(), userID, slot.profile, slot.binding, strings.TrimSpace(input.ConversationKey), input.MainSessionID, input.SideSessionID, func(main, side string) error {
		if api.sessionRunsATurn(main) || api.sessionRunsATurn(side) {
			return errMainChatBusy
		}
		turns, err := api.readConversationTurnQueue(r.Context(), userID)
		if err != nil {
			return err
		}
		for _, turn := range turns {
			if (turn.SessionID == main || turn.SessionID == side) && turn.DeliveryError == "" {
				return errMainChatBusy
			}
		}
		return nil
	})
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errInvalidMainChat) {
			status = http.StatusUnprocessableEntity
		}
		if errors.Is(err, errMainChatChanged) || errors.Is(err, errMainChatBusy) {
			status = http.StatusConflict
		}
		writeAgentProfileError(w, status, err.Error())
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, result)
}
