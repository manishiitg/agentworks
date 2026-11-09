package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// A native conversation is an explicit choice for this exact folder and account.
// Claude/Codex open independent copies; Cursor explicitly continues the original CLI conversation.
func (api *StreamingAPI) resumeLocalProjectSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProjectID  string `json:"project_id"`
		ID         string `json:"id"`
		RecentOnly bool   `json:"recent_only"`
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
	sessions, _, err := api.localCrewNativeSessions(r, crew, roots.Files, roots.Owner)
	if err != nil {
		externalError(w, 503, "sessions_unavailable", "Your local conversations could not be checked. Try again.")
		return
	}
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

	key := input.ProjectID + codeChatSideMarker + "import-" + selected.ID
	if input.RecentOnly {
		key += "-recent"
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
	// longer exist. The copied conversation keeps its original account.
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
		if !input.RecentOnly {
			if reason := localNativeCopyUnavailable(*selected, roots.Private); reason != "" {
				externalError(w, http.StatusUnprocessableEntity, "native_copy_unavailable", reason)
				return
			}
		}
		messages, readErr := readLocalNativeMessages(*selected)
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
		var runtime *ChatHistoryAgentRuntime
		if input.RecentOnly {
			// A fresh provider chat gets only the selected recent messages.
			// It must never claim to resume the original native context.
			runtime = nil
		} else if selected.Provider == "cursor-cli" {
			runtime, err = api.continueLocalCursorRuntime(r, roots, conversation, *selected, model)
		} else {
			runtime, err = api.copyLocalNativeRuntime(r, roots, conversation, *selected, model)
		}
		if err != nil {
			externalError(w, 422, "resume_unavailable", err.Error())
			return
		}
		if runtime != nil {
			runtime.WorkshopMode, runtime.AgentToolsMode = mode, normalizeAgentToolsMode(profile.Runtime.AgentTools.Mode)
		}

		data, _ := json.Marshal(map[string]any{"session_id": conversation.SessionID, "user_id": roots.Owner, "agent_mode": "multi-agent", "workspace_path": crew.Binding.WorkspacePath, "workshop_mode": mode, "title": selected.Title, "conversation_history": history, "runtime": runtime, "local_history_notice": localHistoryNotice(input.RecentOnly), "created_at": conversation.CreatedAt, "updated_at": time.Now().UTC().Format(time.RFC3339)})
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
		native := ""
		if runtime != nil {
			native = runtime.ExternalSessionID
		}
		log.Printf("[LOCAL_PROJECT] project=%s action=continue provider=%s source_native=%s native=%s session=%s account=%s recent_only=%t messages=%d", roots.ID, selected.Provider, selected.NativeID, native, conversation.SessionID, selected.Source.ConnectionID, input.RecentOnly, len(history))
	}
	next := localHistoryNotice(input.RecentOnly)
	if selected.Provider == "cursor-cli" && !input.RecentOnly {
		next = "The same Cursor CLI conversation is open in its own tab, with its saved history. Close it in Cursor before sending here. The Crew's current permissions apply."
	}
	writeAgentProfileJSON(w, 200, map[string]any{"conversation_key": key, "conversation_id": conversation.ConversationID, "session_id": conversation.SessionID, "provider": selected.Provider, "connection_id": selected.Source.ConnectionID, "name": selected.Title, "account": selected.Account, "engine": engine, "model_id": model, "next": next, "recent_only": input.RecentOnly})
}

// Preview uses the same owner/account/folder admission as importing. The caller
// supplies an opaque list key, never a native path or another user's identity.
func (api *StreamingAPI) previewLocalProjectSession(w http.ResponseWriter, r *http.Request, crew crewProjectBinding, root, owner string) {
	sessions, _, err := api.localCrewNativeSessions(r, crew, root, owner)
	if err != nil {
		externalError(w, 503, "sessions_unavailable", "Your local conversations could not be checked. Try again.")
		return
	}
	writeLocalProjectPreview(w, r, sessions)
}

func writeLocalProjectPreview(w http.ResponseWriter, r *http.Request, sessions []localProjectNativeSession) {
	for _, session := range sessions {
		if session.ID != r.URL.Query().Get("native_id") {
			continue
		}
		messages, err := readLocalNativeMessages(session)
		if err != nil {
			externalError(w, 404, "session_unavailable", "This conversation is no longer available. Refresh the list.")
			return
		}
		messages = filterNativeContinuityMessagesUncapped(messages)
		total := len(messages)
		preview := []map[string]string{}
		for _, message := range messages {
			parts := []string{}
			for _, part := range message.Parts {
				if part.Text != "" {
					parts = append(parts, part.Text)
				}
			}
			body := strings.Join(parts, "\n\n")
			if body != "" {
				preview = append(preview, map[string]string{"role": message.Role, "text": body})
			}
		}
		writeAgentProfileJSON(w, 200, map[string]any{"session": session, "messages": preview, "total_messages": total, "history_note": "Most recent messages. Older messages stay in the original chat.", "native_copy_unavailable": localNativeCopyUnavailable(session)})
		return
	}
	externalError(w, 404, "session_unavailable", "This conversation is no longer available. Refresh the list.")
}

func localProjectRecognitionAction(api *StreamingAPI, w http.ResponseWriter, r *http.Request, args map[string]any) {
	action := externalArg(args, "action")
	query := r.URL.Query()
	query.Set("project_id", externalArg(args, "crew_id"))
	if action == "native_list" || action == "native_preview" {
		query.Set("root_path", externalArg(args, "root_path"))
	}
	if action == "native_preview" {
		query.Set("native_id", externalArg(args, "native_id"))
	}
	if action == "native_list" {
		query.Set("sessions", "true")
	}
	if action == "native_resume" {
		status, body := scheduleHandler(r, api.handleLocalProject, http.MethodPost, "/api/agent-profiles/work/local-project", nil, nil, map[string]any{"project_id": externalArg(args, "crew_id"), "id": externalArg(args, "native_id"), "recent_only": args["recent_only"] == true})
		externalScheduleRespond(w, status, body)
		return
	}
	if action != "project_context" && action != "native_list" && action != "native_preview" {
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

func (api *StreamingAPI) copyLocalNativeRuntime(r *http.Request, roots crewroot.Roots, conversation ProductConversationRecord, selected localProjectNativeSession, model string) (*ChatHistoryAgentRuntime, error) {
	staging, err := os.MkdirTemp(roots.Private, "local-chat-fork-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	transport := "sdk"
	if selected.Provider == "codex-cli" {
		transport = "app-server"
	}
	bundle, err := sessiontransfer.ExportJSONL(selected.Provider, transport, filepath.Dir(selected.Source.Root), roots.Files, selected.NativeID, filepath.Join(staging, "source"))
	if err != nil {
		return nil, err
	}
	cwd, err := crewCLIWorkingDir(roots.Virtual, roots.Owner, conversation.SessionID, selected.Provider, false)
	if err != nil {
		return nil, err
	}
	fork, err := sessiontransfer.ForkJSONL(bundle, filepath.Join(staging, "fork"), cwd)
	if err != nil {
		return nil, err
	}
	destination := conversation
	destination.Provider, destination.ModelID, destination.ConnectionID = selected.Provider, model, selected.Source.ConnectionID
	return api.importCrewNativeInto(r.Context(), GetUserFromContext(r.Context()), roots, destination, conversation.SessionID, fork, "")
}

// A large native fork is distinct from displaying recent messages. Refuse
// before exporting/staging anything; the explicit recent-only action still works.
func localNativeCopyUnavailable(session localProjectNativeSession, destinations ...string) string {
	if session.Provider == "cursor-cli" {
		return ""
	}
	info, err := os.Stat(session.Path)
	if err != nil {
		return "This conversation is no longer available. Refresh the list."
	}
	// The existing transfer makes and validates multiple full copies. Keep that
	// work out of the interactive path for large journals until native fork jobs land.
	if info.Size() > 64<<20 {
		return "This chat is too large to copy here. You can import its recent messages into a new chat; older history stays in the original."
	}
	destination := filepath.Dir(session.Path)
	if len(destinations) > 0 && destinations[0] != "" {
		destination = destinations[0]
	}
	free, err := localChatFreeBytes(destination)
	if err != nil {
		return "Available storage could not be checked. You can import recent messages instead."
	}
	if free < uint64(info.Size())*3+(256<<20) {
		return "There is not enough free space to copy this chat. You can import recent messages instead; the original stays unchanged."
	}
	return ""
}

func localHistoryNotice(recentOnly bool) string {
	if recentOnly {
		return "Recent messages imported into a new chat. Older context remains in the original chat."
	}
	return "Showing recent messages. Your agent keeps the original conversation context in its separate copy."
}
