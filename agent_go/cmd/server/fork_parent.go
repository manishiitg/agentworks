package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
)

// A fork hangs under the agent it was forked from in the person's left menu (#3288). The person may move it out, so it
// becomes an agent of its own, and right after that put it back (#3504, owner 21 Nov). Moving it out also ends its
// parent's right to delete it (#3500: a chat deletes only a fork still nested under it).

// setForkParent saves which agent a fork hangs under: "" moves it out, a key puts it under that agent. The server
// decides: only the fork's own person, never Main, never under itself or one of its own forks, and only within the
// same Crew. The status is 0 when it was saved, else an HTTP status with a plain reason.
func (api *StreamingAPI) setForkParent(ctx context.Context, userID, profileID, projectID, workspacePath, key, parent string) (int, string) {
	key, parent = strings.TrimSpace(key), strings.TrimSpace(parent)
	if key == "" || crewResourceProjectID(key) != projectID {
		return http.StatusBadRequest, "Choose one of this project's agents."
	}
	if key == projectID {
		return http.StatusBadRequest, "Main is never under another agent."
	}
	tab, found, err := crewTabForKey(ctx, userID, profileID, key, workspacePath)
	if err != nil {
		return http.StatusServiceUnavailable, "Couldn't read this project's agents. Try again."
	}
	if !found {
		return http.StatusNotFound, "That agent no longer exists."
	}
	if tab.OwnerID != "" && tab.OwnerID != userID {
		return http.StatusForbidden, "Only " + crewOwnerDisplayName(tab.OwnerID) + " can move this agent."
	}
	store := defaultProductConversationRegistryStore()
	if parent != "" {
		if parent == key {
			return http.StatusBadRequest, "An agent can't be under itself."
		}
		if crewResourceProjectID(parent) != projectID {
			return http.StatusBadRequest, "Put it back under an agent of this project."
		}
		home, ok, err := crewTabForKey(ctx, userID, profileID, parent, workspacePath)
		if err != nil {
			return http.StatusServiceUnavailable, "Couldn't read this project's agents. Try again."
		}
		if !ok || (home.OwnerID != "" && home.OwnerID != userID) {
			return http.StatusNotFound, "The agent it was under is no longer yours or no longer exists."
		}
		// Never under one of its own forks: that would make a loop the menu can't draw.
		seen := map[string]bool{key: true}
		for up := parent; up != ""; {
			if seen[up] {
				return http.StatusBadRequest, "It can't go under one of its own forks."
			}
			seen[up] = true
			identity, err := store.agentIdentity(ctx, userID, profileID, up)
			if err != nil {
				return http.StatusServiceUnavailable, "Couldn't read this project's agents. Try again."
			}
			up = strings.TrimSpace(identity.ForkedFrom)
		}
	}
	if _, err := store.setChatContextOptions(ctx, userID, profileID, key, chatContextChange{ForkedFrom: &parent}, userID); err != nil {
		return http.StatusUnprocessableEntity, err.Error()
	}
	log.Printf("[CHAT_FORK] person=%s fork=%s parent=%q step=%s", userID, key, parent, map[bool]string{true: "move_out", false: "put_back"}[parent == ""])
	return 0, ""
}

// handleAgentForkParent is POST /api/agent-profiles/{id}/side-chats/fork-parent {project_id, conversation_key,
// forked_from}: an empty forked_from moves the fork out from under its agent, a key puts it back. It answers with the
// project's tabs, as ordering and pinning do.
func (api *StreamingAPI) handleAgentForkParent(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var input struct {
		ProjectID       string `json:"project_id"`
		ConversationKey string `json:"conversation_key"`
		ForkedFrom      string `json:"forked_from"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes)).Decode(&input); err != nil || strings.TrimSpace(input.ConversationKey) == "" {
		writeAgentProfileError(w, http.StatusBadRequest, "send the project, the agent and where it goes")
		return
	}
	projectID := strings.TrimSpace(input.ProjectID)
	if api.crewMacTwinForOwner(r, projectID) {
		writeAgentProfileError(w, http.StatusConflict, "Arrange this project's agents on your Mac.")
		return
	}
	userID, slot, ok := api.agentProfileConversationSlot(w, r, projectID)
	if !ok {
		return
	}
	if !limitsSideChats(slot.profile.ID) {
		writeAgentProfileError(w, http.StatusBadRequest, "only a project's agents have forks")
		return
	}
	if status, reason := api.setForkParent(r.Context(), userID, slot.profile.ID, projectID, slot.binding.WorkspacePath, input.ConversationKey, input.ForkedFrom); status != 0 {
		writeAgentProfileError(w, status, reason)
		return
	}
	out, err := api.sideChatsFor(r, userID, slot, projectID)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, out)
}
