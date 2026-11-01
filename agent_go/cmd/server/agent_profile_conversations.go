package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
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

// normalizeConversationWorkspace is the logical spelling of a workspace
// path: ANY owner's _users/<id>/ prefix stripped (also under an absolute
// document root). It answers "which product/project is this", never "may this
// caller touch it"; access identity is workspacepathsMatchForUser.
func normalizeConversationWorkspace(workspacePath string) string {
	return workspaceref.MustParse(workspacePath).Logical()
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
	binding, canBuild, err := resolveConversationBindingForUser(r.Context(), userID, profile, conversationKey)
	if err != nil {
		log.Printf("[PRODUCT_CONVERSATION] stage=slot profile=%s key=%q caller=%q cause=%v", profile.ID, conversationKey, userID, err)
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
		return "", profileAndBinding{}, false
	}
	if r.Method != http.MethodGet {
		if err := crewTabWriteError(r.Context(), userID, profile.ID, binding.ConversationKey, binding.WorkspacePath); err != nil {
			externalError(w, 403, "tab_owner", err.Error())
			return "", profileAndBinding{}, false
		}
	}
	return userID, profileAndBinding{profile: profile, binding: binding, canBuild: canBuild}, true
}

type profileAndBinding struct {
	profile  agentprofiles.Profile
	binding  productConversationBinding
	canBuild bool
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
	unlock, _, capErr := guardNewSideChat(r.Context(), userID, slot.profile.ID, slot.binding.ConversationKey)
	if capErr != nil {
		writeAgentProfileError(w, http.StatusConflict, capErr.Error())
		return
	}
	defer unlock()
	conversation, err := resumeProductConversation(r.Context(), userID, slot.profile, slot.binding, input.SessionID)
	if err != nil {
		writeAgentProfileError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, AgentProfileConversationResponse{
		CanWrite:        true,
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
	if err := crewQueryTabWriteError(r.Context(), userID, slot.profile.ID, slot.binding.WorkspacePath, sessionID); err != nil {
		externalError(w, 403, "tab_owner", err.Error())
		return
	}
	if isCrewProjectPath(slot.binding.WorkspacePath) {
		if data, e := ReadChatHistoryConversation(userID, sessionID, slot.binding.WorkspacePath); e == nil {
			author, _ := chatHistoryConversationIdentity(data)
			if author != "" && author != userID {
				externalError(w, 403, "tab_owner", "Only this tab’s owner can delete it.")
				return
			}
		}
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
	deleteOwnCodeSessionPin(codeSessionPinCaller(r.Context()), sessionID)
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
	// actingFor is whose Crew this is: the caller, or the MAIN owner when the caller deletes as a co-owner (owner,
	// 2026-10-16: co-owners may delete). Its chats, registry entry and connections are the main owner's.
	actingFor, coOwner := userID, false
	binding, err := resolveProductConversationBinding(r.Context(), userID, profile, projectID)
	if err != nil && isProjectProfileID(profile.ID) {
		if crew, crewErr := resolveCrewProjectBinding(r.Context(), userID, profile, projectID, ""); crewErr == nil && crew.CoOwner {
			binding, err, actingFor, coOwner = crew.Binding, nil, crew.OwnerID, true
		}
	}
	if err != nil {
		writeAgentProfileError(w, http.StatusNotFound, "project not found")
		return
	}
	if strings.EqualFold(profile.ID, crewProfileID) && isAgentWorksCrew(r.Context(), binding.WorkspacePath) {
		writeAgentProfileError(w, http.StatusConflict, errAgentWorksDeleteRefused.Error())
		return
	}
	store := defaultProductConversationRegistryStore()
	current, hasCurrent, previous, err := store.history(r.Context(), actingFor, profile, binding)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	records := append([]ProductConversationRecord(nil), previous...)
	if hasCurrent {
		records = append([]ProductConversationRecord{current}, records...)
	}
	for _, record := range records {
		if api.sessionStillWorkingAfterIdleRelease(record.SessionID) {
			writeAgentProfileError(w, http.StatusConflict, "Crew is still working; stop it before deleting the project")
			return
		}
	}
	if strings.EqualFold(profile.ID, crewProfileID) {
		used, err := crewUsedByWorkflow(r.Context(), profile.ID, binding.ResourceID, binding.WorkspacePath)
		if err != nil {
			log.Printf("[CREW_DELETE] could not check workflow dependencies for project %s: %v", binding.ResourceID, err)
			writeAgentProfileError(w, http.StatusServiceUnavailable, "Could not check workflow dependencies. Try deleting the Crew again later.")
			return
		}
		if used {
			writeAgentProfileError(w, http.StatusConflict, "Remove this Crew from every workflow before deleting it.")
			return
		}
	}
	publicationRoot := agentProfileRuntimeWorkspace(actingFor, binding.WorkspacePath)
	defer lockPublication("target:" + canonicalCrewWorkspaceRoot(publicationRoot))()
	deleteCtx := context.WithValue(r.Context(), publicationLifecycleKey{}, publicationRoot)
	client := workspace.NewClient(getWorkspaceAPIURL(), workspace.WithUserID(actingFor))
	var deleteErr error
	if _, bound, rootErr := localCrewRemovalRoots(binding.WorkspacePath); rootErr != nil {
		deleteErr = rootErr
	} else if bound {
		deleteErr = removeLocalCrew(binding.WorkspacePath)
	} else {
		deleteErr = client.DeleteFolder(deleteCtx, binding.WorkspacePath)
	}
	if err := deleteErr; err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "delete project folder: "+err.Error())
		return
	}
	if err := deletePublicationState(r.Context(), publicationRoot, profile.ID, projectID); err != nil {
		log.Printf("[CREW_PUBLISH] workspace=%s cleanup=incomplete", projectID)
		writeAgentProfileError(w, 500, "The Crew folder was removed, but imported publication history cleanup needs a retry.")
		return
	}

	if id, ok := projectIdentityOf(binding.WorkspacePath); ok {
		// The project is gone: so is its registry entry (best effort; a stale entry only reserves a folder name).
		if err := defaultProjectOwners().Remove(id.Product, id.Folder); err != nil {
			log.Printf("[OWNER_REGISTRY] could not remove %s/%s: %v", id.Product, id.Folder, err)
		}
	}
	removed, err := store.removeSlot(r.Context(), actingFor, profile, binding)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "remove project conversation binding: "+err.Error())
		return
	}
	removedIDs := make([]string, 0, len(removed))
	for _, record := range removed {
		// The project folder deletion normally removed the transcript itself;
		// this also clears any durable index or compatibility copy elsewhere.
		_, _ = DeleteChatHistorySession(actingFor, record.SessionID, "")
		removedIDs = append(removedIDs, record.SessionID)
	}
	api.deleteDurableChatSessionsAfterBulkDelete("project delete", codeSessionPinCaller(r.Context()), removedIDs)
	if strings.EqualFold(profile.ID, crewProfileID) {
		// Older context-path references do not prevent deletion.
		go pruneDeletedCrewReferences(context.WithoutCancel(r.Context()), actingFor, binding.WorkspacePath)
	}
	if isProjectProfileID(profile.ID) {
		// A deleted Crew or Code takes its MCP connections, and their logins,
		// with it.
		forgetPlaceConnections(agentProfileRuntimeWorkspace(actingFor, binding.WorkspacePath))
	}
	if strings.EqualFold(profile.ID, workproduct.ProfileID) {
		// A deleted Code takes its shells with it.
		stopCodeShellsFor(sanitizeUserIDForPath(actingFor), projectID, nil)
	}
	if coOwner {
		log.Printf("[CREW_DELETE] project=%s deleted by=%s as co-owner of %s", projectID, sanitizeUserIDForPath(userID), actingFor)
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// resumeProductConversation owns saved-history verification and slot switching
// for both browser and connector adapters.
func resumeProductConversation(ctx context.Context, userID string, profile agentprofiles.Profile, binding productConversationBinding, sessionID string) (ProductConversationRecord, error) {
	if err := crewQueryTabWriteError(ctx, userID, profile.ID, binding.WorkspacePath, sessionID); err != nil {
		return ProductConversationRecord{}, err
	}
	verified := false
	if session, found := chatHistorySessionsByID(userID, binding.WorkspacePath)[strings.TrimSpace(sessionID)]; found {
		if isCrewProjectPath(binding.WorkspacePath) && session.UserID != "" && session.UserID != userID {
			return ProductConversationRecord{}, fmt.Errorf("Only this tab’s owner can resume it. Use your own tab.")
		}
		verified = chatHistorySessionWorkspace(session) == normalizeConversationWorkspace(binding.WorkspacePath)
	}
	return defaultProductConversationRegistryStore().switchTo(ctx, userID, profile, binding, sessionID, verified)
}
