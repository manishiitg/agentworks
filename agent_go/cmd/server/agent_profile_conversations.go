package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// A product's earlier conversations. The registry remembers every
// conversation a "new chat" or a switch moved out of a slot; chat_history
// holds the transcripts and a first line to title them by. Chats rotated
// before the registry kept history are found by their workspace, so a
// product's list is complete either way.

// AgentProfileConversationSummary is one conversation of a slot as a client
// lists it: identity, when, and a title from its first message.
type AgentProfileConversationSummary struct {
	SessionID      string `json:"session_id"`
	ConversationID string `json:"conversation_id,omitempty"`
	Title          string `json:"title"`
	CreatedAt      string `json:"created_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
	MessageCount   int    `json:"message_count,omitempty"`
	Current        bool   `json:"current"`
}

type AgentProfileConversationsResponse struct {
	Current  *AgentProfileConversationSummary  `json:"current,omitempty"`
	Previous []AgentProfileConversationSummary `json:"previous"`
}

const agentProfileConversationTitleLimit = 72

// conversationTitleFrom titles a chat by its first message, trimmed to one
// line; a chat with no message yet is "New chat".
func conversationTitleFrom(session *ChatHistorySession, fallback string) string {
	if session != nil {
		if title := strings.TrimSpace(session.Title); title != "" {
			return title
		}
		first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(session.Query), "\n", 2)[0])
		if first != "" {
			if runes := []rune(first); len(runes) > agentProfileConversationTitleLimit {
				first = strings.TrimSpace(string(runes[:agentProfileConversationTitleLimit])) + "…"
			}
			return first
		}
	}
	if fallback = strings.TrimSpace(fallback); fallback != "" {
		return fallback
	}
	return "New chat"
}

// normalizeConversationWorkspace compares workspaces the way chat_history
// and the registry each record them: with or without the per-user prefix.
func normalizeConversationWorkspace(workspacePath string) string {
	clean := strings.Trim(strings.TrimSpace(strings.ReplaceAll(workspacePath, "\\", "/")), "/")
	// Runtime history may store the user-relative path while the resolved
	// project binding is absolute under the document root. Compare both from
	// the stable _users/<id>/ boundary.
	if index := strings.Index(clean, "_users/"); index >= 0 && (index == 0 || clean[index-1] == '/') {
		if rest := strings.SplitN(clean[index:], "/", 3); len(rest) == 3 {
			clean = rest[2]
		}
	}
	return clean
}

func chatHistorySessionWorkspace(session ChatHistorySession) string {
	if session.Runtime != nil && strings.TrimSpace(session.Runtime.WorkspacePath) != "" {
		return normalizeConversationWorkspace(session.Runtime.WorkspacePath)
	}
	return normalizeConversationWorkspace(session.WorkspacePath)
}

// chatHistorySessionsByID indexes the user's chat history for titling and
// for finding chats the registry never listed.
func chatHistorySessionsByID(userID, workspacePath string) map[string]ChatHistorySession {
	// Work transcripts live inside their project rather than the user's legacy
	// global chat_history directory. Scope the lookup to the resolved product
	// workspace so resume and ownership checks see the same rows as Workshop.
	sessions, err := ListChatHistorySessions(userID, 2000, 0, normalizeConversationWorkspace(workspacePath))
	if err != nil {
		return map[string]ChatHistorySession{}
	}
	byID := make(map[string]ChatHistorySession, len(sessions))
	for _, session := range sessions {
		byID[session.SessionID] = session
	}
	return byID
}

func summarizeProductConversation(record ProductConversationRecord, session *ChatHistorySession, current bool) AgentProfileConversationSummary {
	summary := AgentProfileConversationSummary{
		SessionID:      record.SessionID,
		ConversationID: record.ConversationID,
		Title:          conversationTitleFrom(session, record.Title),
		CreatedAt:      record.CreatedAt,
		UpdatedAt:      record.UpdatedAt,
		Current:        current,
	}
	if session != nil {
		summary.MessageCount = session.MessageCount
		if strings.TrimSpace(session.CreatedAt) != "" {
			summary.CreatedAt = session.CreatedAt
		}
		if strings.TrimSpace(session.UpdatedAt) != "" {
			summary.UpdatedAt = session.UpdatedAt
		}
	}
	return summary
}

func (api *StreamingAPI) agentProfileConversationSlot(w http.ResponseWriter, r *http.Request, conversationKey string) (string, profileAndBinding, bool) {
	if api.agentProfiles == nil {
		writeAgentProfileError(w, http.StatusServiceUnavailable, "agent profiles are unavailable")
		return "", profileAndBinding{}, false
	}
	userID := productWorkspaceUserID(r.Context())
	profile, err := api.agentProfiles.Resolve(strings.TrimSpace(mux.Vars(r)["id"]), 0, userID)
	if err != nil {
		writeAgentProfileError(w, http.StatusNotFound, "agent profile not found")
		return "", profileAndBinding{}, false
	}
	if !userAllowedProduct(GetUserFromContext(r.Context()), profile.Product) {
		writeAgentProfileError(w, http.StatusNotFound, "agent profile not found")
		return "", profileAndBinding{}, false
	}
	binding, _, err := resolveConversationBindingForUser(r.Context(), userID, profile, conversationKey)
	if err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
		return "", profileAndBinding{}, false
	}
	return userID, profileAndBinding{profile: profile, binding: binding}, true
}

type profileAndBinding struct {
	profile agentprofiles.Profile
	binding productConversationBinding
}

// handleListAgentProfileConversations lists a slot's live conversation and
// its earlier ones, newest first: GET /api/agent-profiles/{id}/conversations
// ?conversation_key=….
func (api *StreamingAPI) handleListAgentProfileConversations(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	userID, slot, ok := api.agentProfileConversationSlot(w, r, r.URL.Query().Get("conversation_key"))
	if !ok {
		return
	}
	store := defaultProductConversationRegistryStore()
	current, hasCurrent, previous, err := store.history(r.Context(), userID, slot.profile, slot.binding)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	live, err := store.liveSessionIDs(r.Context(), userID, slot.profile.ID)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sessions := chatHistorySessionsByID(userID, slot.binding.WorkspacePath)
	lookup := func(sessionID string) *ChatHistorySession {
		if session, ok := sessions[sessionID]; ok {
			return &session
		}
		return nil
	}

	response := AgentProfileConversationsResponse{Previous: []AgentProfileConversationSummary{}}
	if hasCurrent {
		summary := summarizeProductConversation(current, lookup(current.SessionID), true)
		response.Current = &summary
	}
	listed := map[string]bool{}
	for _, record := range previous {
		if listed[record.SessionID] || live[record.SessionID] {
			continue
		}
		listed[record.SessionID] = true
		response.Previous = append(response.Previous, summarizeProductConversation(record, lookup(record.SessionID), false))
	}
	// Chats rotated before the registry kept history: same workspace as this
	// slot, not live anywhere, not listed yet.
	workspace := normalizeConversationWorkspace(slot.binding.WorkspacePath)
	for sessionID, session := range sessions {
		if listed[sessionID] || live[sessionID] || (hasCurrent && sessionID == current.SessionID) {
			continue
		}
		if workspace == "" || chatHistorySessionWorkspace(session) != workspace {
			continue
		}
		listed[sessionID] = true
		found := session
		response.Previous = append(response.Previous, summarizeProductConversation(ProductConversationRecord{SessionID: sessionID, ConversationKey: slot.binding.ConversationKey, ProfileID: slot.profile.ID}, &found, false))
	}
	sort.SliceStable(response.Previous, func(i, j int) bool {
		return response.Previous[i].UpdatedAt > response.Previous[j].UpdatedAt
	})
	writeAgentProfileJSON(w, http.StatusOK, response)
}

// handleSwitchAgentProfileConversation makes an earlier conversation the
// slot's live one: POST /api/agent-profiles/{id}/conversation/switch
// {"conversation_key": …, "session_id": …}. The client then reopens the
// slot the same way it does after "new chat".
func (api *StreamingAPI) handleSwitchAgentProfileConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes))
	decoder.DisallowUnknownFields()
	var input AgentProfileConversationRequest
	if err := decoder.Decode(&input); err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, "invalid product conversation request: "+err.Error())
		return
	}
	if strings.TrimSpace(input.SessionID) == "" {
		writeAgentProfileError(w, http.StatusBadRequest, "session_id is required")
		return
	}
	conversationKey := agentProfileResumeConversationKey(strings.TrimSpace(mux.Vars(r)["id"]), input)
	userID, slot, ok := api.agentProfileConversationSlot(w, r, conversationKey)
	if !ok {
		return
	}
	conversation, err := resumeProductConversation(r.Context(), userID, slot.profile, slot.binding, input.SessionID)
	if err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, AgentProfileConversationResponse{
		ConversationID:  conversation.ConversationID,
		ConversationKey: conversation.ConversationKey,
		SessionID:       conversation.SessionID,
	})
}

// handleDeleteAgentProfileConversation forgets an earlier conversation and
// deletes its transcript: DELETE /api/agent-profiles/{id}/conversations/
// {session_id}?conversation_key=…. The live conversation is refused.
func (api *StreamingAPI) handleDeleteAgentProfileConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	sessionID := strings.TrimSpace(mux.Vars(r)["session_id"])
	if sessionID == "" {
		writeAgentProfileError(w, http.StatusBadRequest, "session_id is required")
		return
	}
	userID, slot, ok := api.agentProfileConversationSlot(w, r, r.URL.Query().Get("conversation_key"))
	if !ok {
		return
	}
	store := defaultProductConversationRegistryStore()
	live, err := store.liveSessionIDs(r.Context(), userID, slot.profile.ID)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if live[sessionID] {
		writeAgentProfileError(w, http.StatusConflict, "this is a live conversation — start a new chat before deleting it")
		return
	}
	// Only this slot's chats can go: listed by the registry, or in its
	// workspace per chat_history.
	owned := false
	for _, record := range func() []ProductConversationRecord {
		_, _, previous, _ := store.history(r.Context(), userID, slot.profile, slot.binding)
		return previous
	}() {
		if record.SessionID == sessionID {
			owned = true
		}
	}
	if !owned {
		if session, found := chatHistorySessionsByID(userID, slot.binding.WorkspacePath)[sessionID]; found {
			owned = chatHistorySessionWorkspace(session) == normalizeConversationWorkspace(slot.binding.WorkspacePath)
		}
	}
	if !owned {
		writeAgentProfileError(w, http.StatusNotFound, "conversation not found")
		return
	}
	if err := store.forget(r.Context(), userID, slot.profile, slot.binding, sessionID); err != nil {
		writeAgentProfileError(w, http.StatusConflict, err.Error())
		return
	}
	result, err := DeleteChatHistorySession(userID, sessionID, "")
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "delete conversation transcript: "+err.Error())
		return
	}
	if err := api.deleteDurableChatSessions([]string{sessionID}); err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "delete conversation event log: "+err.Error())
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "deleted_paths": result.DeletedPaths})
}

// handleDeleteAgentProfileProject permanently removes one authenticated keyed
// product project, including its workspace and durable conversation slot. It is
// intentionally separate from deleting an earlier chat: the live slot may only
// disappear together with the project that owns it.
func (api *StreamingAPI) handleDeleteAgentProfileProject(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if api.agentProfiles == nil {
		writeAgentProfileError(w, http.StatusServiceUnavailable, "agent profiles are unavailable")
		return
	}
	userID := productWorkspaceUserID(r.Context())
	profile, err := api.agentProfiles.Resolve(strings.TrimSpace(mux.Vars(r)["id"]), 0, userID)
	if err != nil || !userAllowedProduct(GetUserFromContext(r.Context()), profile.Product) {
		writeAgentProfileError(w, http.StatusNotFound, "agent profile not found")
		return
	}
	projectID := strings.TrimSpace(mux.Vars(r)["project_id"])
	binding, err := resolveProductConversationBinding(r.Context(), userID, profile, projectID)
	if err != nil {
		writeAgentProfileError(w, http.StatusNotFound, "project not found")
		return
	}
	store := defaultProductConversationRegistryStore()
	current, hasCurrent, previous, err := store.history(r.Context(), userID, profile, binding)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	records := append([]ProductConversationRecord(nil), previous...)
	if hasCurrent {
		records = append([]ProductConversationRecord{current}, records...)
	}
	for _, record := range records {
		if api.sessionHasActiveWork(record.SessionID) {
			writeAgentProfileError(w, http.StatusConflict, "Crew is still working; stop it before deleting the project")
			return
		}
	}
	client := workspace.NewClient(getWorkspaceAPIURL(), workspace.WithUserID(userID))
	if err := client.DeleteFolder(r.Context(), binding.WorkspacePath); err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "delete project folder: "+err.Error())
		return
	}
	removed, err := store.removeSlot(r.Context(), userID, profile, binding)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "remove project conversation binding: "+err.Error())
		return
	}
	removedIDs := make([]string, 0, len(removed))
	for _, record := range removed {
		// The project folder deletion normally removed the transcript itself;
		// this also clears any durable index or compatibility copy elsewhere.
		_, _ = DeleteChatHistorySession(userID, record.SessionID, "")
		removedIDs = append(removedIDs, record.SessionID)
	}
	api.deleteDurableChatSessionsAfterBulkDelete("project delete", removedIDs)
	// Other crews and workflows that attached this crew stop pointing at it.
	go pruneDeletedCrewReferences(context.WithoutCancel(r.Context()), userID, binding.WorkspacePath)
	if strings.EqualFold(profile.ID, codeproduct.ProfileID) {
		// A deleted Code takes its share list with it, and everyone's
		// personal MCP switches for it.
		owner := sanitizeUserIDForPath(userID)
		codeRoot := cleanCodeRoot(agentProfileRuntimeWorkspace(userID, binding.WorkspacePath))
		people := []string{owner}
		for _, grant := range codeSharesView(r.Context(), owner, projectID, codeRoleOwner).Grants {
			people = append(people, grant.UserID)
		}
		for _, person := range people {
			if err := forgetPersonalMCPCode(person, codeRoot); err != nil {
				log.Printf("[PERSONAL_MCP] forget deleted Code for %s: %v", person, err)
			}
		}
		_ = codeShares.update(context.WithoutCancel(r.Context()), func(doc *codeSharesDoc) error {
			delete(doc.Projects, codeShareKey(owner, projectID))
			return nil
		})
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// resumeProductConversation owns saved-history verification and slot switching
// for both browser and connector adapters.
func resumeProductConversation(ctx context.Context, userID string, profile agentprofiles.Profile, binding productConversationBinding, sessionID string) (ProductConversationRecord, error) {
	verified := false
	if session, found := chatHistorySessionsByID(userID, binding.WorkspacePath)[strings.TrimSpace(sessionID)]; found {
		verified = chatHistorySessionWorkspace(session) == normalizeConversationWorkspace(binding.WorkspacePath)
	}
	return defaultProductConversationRegistryStore().switchTo(ctx, userID, profile, binding, sessionID, verified)
}
