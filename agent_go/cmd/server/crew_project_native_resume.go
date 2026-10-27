package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A native conversation is an explicit choice, never the latest session for
// a folder. Only its original account home may continue it.
func (api *StreamingAPI) resumeLocalProjectSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProjectID string `json:"project_id"`
		ID        string `json:"id"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ID == "" {
		externalError(w, 400, "invalid_request", "Choose a conversation from this folder's session list.")
		return
	}
	crew, roots, ok := api.localProjectAccess(w, r, input.ProjectID, true)
	if !ok {
		return
	}
	sources, err := api.localNativeSources(r.Context(), roots.Owner, crew.Binding.WorkspacePath)
	if err != nil {
		externalError(w, 503, "sessions_unavailable", "Your local conversations could not be checked. Try again.")
		return
	}
	sessions, _ := listLocalNativeSessions(r.Context(), roots.Files, sources)
	var selected *localProjectNativeSession
	for i := range sessions {
		if sessions[i].ID == input.ID {
			selected = &sessions[i]
			break
		}
	}
	if selected == nil {
		externalError(w, 404, "session_unavailable", "This session is no longer available on this computer. Refresh the session list.")
		return
	}
	key := input.ProjectID + codeChatSideMarker + "native-" + selected.ID
	// Do not make a second writer for an existing AgentWorks chat's native
	// conversation. Imported tabs use the deterministic key above and reopen it.
	for _, chat := range api.crewChats(r, roots.Owner, input.ProjectID, crew.Binding.WorkspacePath) {
		if chat.key == key {
			continue
		}
		runtime, found, _ := ReadChatHistoryRuntimeForSession(roots.Owner, chat.SessionID, crew.Binding.WorkspacePath)
		if found && runtime != nil && runtime.Provider == selected.Provider && runtime.ExternalSessionID == selected.NativeID {
			externalError(w, 409, "already_open", "This conversation is already open in this Crew. Use its existing chat tab.")
			return
		}
	}
	profile, err := api.agentProfiles.Resolve("work", 0, roots.Owner)
	if err != nil {
		externalFailure(w, err)
		return
	}
	applyCrewNativeToolPolicy(&profile, crewProjectOwnedByCaller(roots.Owner, crew.Binding.WorkspacePath), crew.Binding.ProjectNativeAgentTools)
	mode := crewCLIMode(!crewProjectOwnedByCaller(roots.Owner, crew.Binding.WorkspacePath))
	engine, model := "", ""
	for _, option := range profile.Runtime.ProviderOptions {
		if option.Provider == selected.Provider {
			engine, model = option.ID, option.ModelID
			break
		}
	}
	if engine == "" || model == "" {
		externalError(w, 422, "provider_unavailable", "This agent is unavailable here. Choose an available account in Models.")
		return
	}
	// Use the current admitted model for that provider; the old model may no
	// longer exist. Native session identity and original account stay unchanged.
	query := QueryRequest{Provider: selected.Provider, ModelID: model, ConnectionID: selected.Source.ConnectionID}
	if err := constrainProductChatModel(r.Context(), &query); err != nil {
		externalError(w, 422, "model_unavailable", err.Error())
		return
	}
	model = query.ModelID
	request := r.Clone(r.Context())
	request.Header = r.Header.Clone()
	request.Header.Del("X-Session-ID")
	request.Header.Del("X-Conversation-Continuation")
	conversation, err := api.resolveAgentProfileConversation(request, profile, key, selected.Title)
	if err != nil {
		externalError(w, 422, "chat_unavailable", err.Error())
		return
	}
	store := defaultProductConversationRegistryStore()
	path, _ := workProjectChatHistoryConversationPath(roots.Owner, crew.Binding.WorkspacePath, conversation.SessionID, time.Now())
	if path == "" {
		externalError(w, 500, "chat_unavailable", "Could not save this private chat.")
		return
	}
	// Once adopted, reopening only selects the tab; it never overwrites its
	// subsequent history, account choice or an agent running in it.
	if _, found, err := FindChatHistoryConversationPathForSession(roots.Owner, conversation.SessionID, crew.Binding.WorkspacePath); err != nil {
		externalFailure(w, err)
		return
	} else if !found {
		var messages []builderConversationMessage
		var readErr error
		if selected.Provider == "claude-code" {
			messages, _, readErr = readNewClaudeTranscriptMessages(selected.Path, time.Time{})
		} else {
			messages, _, readErr = readCodexTranscriptMessages(selected.Path)
		}
		if readErr != nil || len(messages) == 0 {
			externalError(w, 404, "session_unavailable", "This session is no longer available on this computer. Refresh the session list.")
			return
		}
		history := []llmtypes.MessageContent{}
		for _, message := range filterNativeContinuityMessagesUncapped(messages) {
			role := llmtypes.ChatMessageTypeHuman
			if message.Role == "ai" || message.Role == "assistant" {
				role = llmtypes.ChatMessageTypeAI
			}
			parts := []string{}
			for _, part := range message.Parts {
				if part.Text != "" {
					parts = append(parts, part.Text)
				}
			}
			if len(parts) > 0 {
				history = append(history, llmtypes.TextPart(role, strings.Join(parts, "\n\n")))
			}
		}
		if len(history) == 0 {
			externalError(w, 422, "empty_session", "This conversation has no saved messages to resume. Start a new chat instead.")
			return
		}
		if _, err := store.setAgentModel(r.Context(), roots.Owner, "work", key, engine, model, "", selected.Source.ConnectionID, roots.Owner); err != nil {
			externalFailure(w, err)
			return
		}
		runtime := &ChatHistoryAgentRuntime{Kind: "coding_agent", Provider: selected.Provider, ModelID: model, ExternalSessionID: selected.NativeID, ResumeSupported: true, WorkspacePath: crew.Binding.WorkspacePath, WorkshopMode: mode, AgentToolsMode: normalizeAgentToolsMode(profile.Runtime.AgentTools.Mode), AgentSessionHandle: &mcpagent.AgentSessionHandle{ConnectionID: selected.Source.ConnectionID, SessionID: conversation.SessionID, OwnerID: conversation.SessionID, Scope: "coding_agent", Provider: llmtypes.CodingProviderSessionHandle{Provider: selected.Provider, NativeSessionID: selected.NativeID, WorkingDir: roots.Files, Model: model}}}
		data, _ := json.Marshal(map[string]any{"session_id": conversation.SessionID, "user_id": roots.Owner, "agent_mode": "multi-agent", "workspace_path": crew.Binding.WorkspacePath, "workshop_mode": mode, "title": selected.Title, "conversation_history": history, "runtime": runtime, "created_at": conversation.CreatedAt, "updated_at": time.Now().UTC().Format(time.RFC3339)})
		if err := writeRawFileToWorkspace(r.Context(), path, string(data)); err != nil {
			externalFailure(w, err)
			return
		}
		if err := writeChatHistoryResumeSnapshot(r.Context(), path, data); err != nil {
			externalFailure(w, err)
			return
		}
		if err := updatePersistedChatHistoryIndex(roots.Owner, conversation.SessionID, "multi-agent", history, runtime, path, int64(len(data)), time.Now(), nil); err != nil {
			externalFailure(w, err)
			return
		}
		log.Printf("[LOCAL_PROJECT] project=%s action=resume provider=%s native=%s session=%s account=%s", roots.ID, selected.Provider, selected.NativeID, conversation.SessionID, selected.Source.ConnectionID)
	}
	writeAgentProfileJSON(w, 200, map[string]any{"conversation_key": key, "conversation_id": conversation.ConversationID, "session_id": conversation.SessionID, "provider": selected.Provider, "connection_id": selected.Source.ConnectionID, "name": selected.Title, "account": selected.Account, "next": "The conversation is open in its own tab. Send your next message to continue with this account and the Crew's current permissions."})
}

func localProjectRecognitionAction(api *StreamingAPI, w http.ResponseWriter, r *http.Request, args map[string]any) {
	action := externalArg(args, "action")
	query := r.URL.Query()
	query.Set("project_id", externalArg(args, "crew_id"))
	if action == "native_list" {
		query.Set("sessions", "true")
	}
	if action == "native_resume" {
		status, body := scheduleHandler(r, api.handleLocalProject, http.MethodPost, "/api/agent-profiles/work/local-project", nil, nil, map[string]string{"project_id": externalArg(args, "crew_id"), "id": externalArg(args, "native_id")})
		externalScheduleRespond(w, status, body)
		return
	}
	if action != "project_context" && action != "native_list" {
		externalFailure(w, fmt.Errorf("unknown local project action"))
		return
	}
	request := r.Clone(r.Context())
	request.Method = http.MethodGet
	u := *r.URL
	u.RawQuery = query.Encode()
	request.URL = &u
	api.handleLocalProject(w, request)
}
