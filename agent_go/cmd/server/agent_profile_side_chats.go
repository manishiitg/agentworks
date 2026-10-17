package server

import (
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
	store := defaultProductConversationRegistryStore()
	records, err := store.projectChats(r.Context(), userID, slot.profile.ID, projectID)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
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
		row := AgentProfileSideChat{ConversationKey: record.ConversationKey, ConversationID: record.ConversationID, SessionID: record.SessionID, Name: name}
		if agentID := publicationAgentID(record.ConversationKey); agentID != "" {
			if p, e := readPublishedAgent(contextWithoutPublication(r.Context()), agentProfileRuntimeWorkspace(userID, slot.binding.WorkspacePath), agentID); e == nil && p != nil {
				row.AgentID, row.Provider, row.ModelID, row.ConnectionID = p.AgentID, p.Provider, p.ModelID, p.ConnectionID
				row.Name = p.Name
			}
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ConversationKey < out[j].ConversationKey })
	writeAgentProfileJSON(w, http.StatusOK, out)
}
