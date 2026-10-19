package server

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
)

// AgentProfileSideChat is one open tab of a Code or Crew project as the server knows it.
type AgentProfileSideChat struct {
	ConversationKey string `json:"conversation_key"`
	ConversationID  string `json:"conversation_id,omitempty"`
	SessionID       string `json:"session_id"`
	Name            string `json:"name"`
	AgentID         string `json:"agent_id,omitempty"`
	Provider        string `json:"provider,omitempty"`
	ModelID         string `json:"model_id,omitempty"`
	ConnectionID    string `json:"connection_id,omitempty"`
	// Position is where the person put this tab in the strip (1 is first); 0 means not placed yet.
	Position int `json:"position,omitempty"`
}

// handleListAgentProfileSideChats lists the caller's open side tabs of one project: GET /api/agent-profiles/{id}/side-chats
// ?project_id=…. A tab another chat opened (open_project_chat, the code tool) exists only here until a browser asks, so
// the project screen reads this list and opens the tabs it does not have yet.
func (api *StreamingAPI) handleListAgentProfileSideChats(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	userID, slot, ok := api.agentProfileConversationSlot(w, r, projectID)
	if !ok {
		return
	}
	if !limitsSideChats(slot.profile.ID) {
		writeAgentProfileJSON(w, http.StatusOK, []AgentProfileSideChat{})
		return
	}
	out, err := api.sideChatsFor(r, userID, slot, projectID)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, out)
}

// handleOrderAgentProfileSideChats saves the order the person dragged their side tabs into: POST
// /api/agent-profiles/{id}/side-chats/order {"project_id": …, "conversation_keys": [first, …]}. It answers with the
// list in its new order, which the screen shows as it is.
func (api *StreamingAPI) handleOrderAgentProfileSideChats(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var input struct {
		ProjectID        string   `json:"project_id"`
		ConversationKeys []string `json:"conversation_keys"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes)).Decode(&input); err != nil || len(input.ConversationKeys) > externalCrewMaxSideChats {
		writeAgentProfileError(w, http.StatusBadRequest, "send the project and its tabs in their new order")
		return
	}
	projectID := strings.TrimSpace(input.ProjectID)
	userID, slot, ok := api.agentProfileConversationSlot(w, r, projectID)
	if !ok {
		return
	}
	if !limitsSideChats(slot.profile.ID) {
		writeAgentProfileError(w, http.StatusBadRequest, "this product has no tabs to order")
		return
	}
	if err := defaultProductConversationRegistryStore().setSideChatOrder(r.Context(), userID, slot.profile.ID, projectID, input.ConversationKeys); err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	out, err := api.sideChatsFor(r, userID, slot, projectID)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, out)
}

// sideChatsFor lists the caller's open side tabs of one project in the order the person arranged them; tabs they
// have not placed follow, oldest key first.
func (api *StreamingAPI) sideChatsFor(r *http.Request, userID string, slot profileAndBinding, projectID string) ([]AgentProfileSideChat, error) {
	store := defaultProductConversationRegistryStore()
	records, err := store.projectChats(r.Context(), userID, slot.profile.ID, projectID)
	if err != nil {
		return nil, err
	}
	identities, _ := store.agentIdentities(r.Context(), userID, slot.profile.ID, projectID)
	titles := map[string]string{}
	for id, session := range chatHistorySessionsByID(userID, slot.binding.WorkspacePath) {
		titles[id] = strings.TrimSpace(session.Title)
	}
	out := []AgentProfileSideChat{}
	for _, record := range records {
		if !isSideChatConversationKey(record.ConversationKey) || !workspacePathsMatchForUser(userID, record.WorkspacePath, slot.binding.WorkspacePath) {
			continue
		}
		id := strings.TrimPrefix(record.ConversationKey, projectID+codeChatSideMarker)
		name := firstNonEmptyTrimmed(identities[record.ConversationKey].Name, titles[record.SessionID], "Chat "+id)
		row := AgentProfileSideChat{ConversationKey: record.ConversationKey, ConversationID: record.ConversationID, SessionID: record.SessionID, Name: name, Position: identities[record.ConversationKey].Position}
		if agentID := publicationAgentID(record.ConversationKey); agentID != "" {
			if p, e := readPublishedAgent(contextWithoutPublication(r.Context()), agentProfileRuntimeWorkspace(userID, slot.binding.WorkspacePath), agentID); e == nil && p != nil {
				row.AgentID, row.Provider, row.ModelID, row.ConnectionID = p.AgentID, p.Provider, p.ModelID, p.ConnectionID
				row.Name = p.Name
			}
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Position, out[j].Position
		if (a > 0) != (b > 0) {
			return a > 0
		}
		if a != b {
			return a < b
		}
		return out[i].ConversationKey < out[j].ConversationKey
	})
	return out, nil
}
