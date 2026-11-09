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

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// Fork snapshots the caller's own admitted chat into a new tab. Opening it
// starts no model turn and never rotates, writes, or stops the source session.
func (api *StreamingAPI) handleForkCrewChat(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var input struct {
		ConversationKey string `json:"conversation_key"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.ConversationKey) == "" {
		externalError(w, 400, "invalid_request", "Choose a chat to fork.")
		return
	}
	user, slot, ok := api.agentProfileConversationSlot(w, r, input.ConversationKey)
	if !ok {
		return
	}
	if slot.profile.ID != crewProfileID || !slot.canBuild || !canUseCapLayerProfile(r.Context(), slot.profile.ID) {
		externalError(w, 403, "fork_unavailable", "Open one of your own Crew chats to fork it.")
		return
	}
	source, found, err := crewTabForKey(r.Context(), user, slot.profile.ID, slot.binding.ConversationKey, slot.binding.WorkspacePath)
	if err != nil {
		externalFailure(w, err)
		return
	}
	if !found || source.OwnerID != user {
		externalError(w, 403, "tab_owner", "Fork one of your own chats, or message this agent from your own tab.")
		return
	}
	if api.crewMacTwinForOwner(r, crewResourceProjectID(source.Record.ConversationKey)) {
		externalError(w, 409, "use_mac", "Fork this chat in the Mac app where it runs.")
		return
	}
	raw, err := ReadChatHistoryConversation(user, source.Record.SessionID, source.Record.WorkspacePath)
	if err != nil {
		externalError(w, 422, "history_unavailable", "This chat has no saved conversation yet. Send a message first.")
		return
	}
	var saved struct {
		History []llmtypes.MessageContent `json:"conversation_history"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		externalFailure(w, err)
		return
	}
	if len(saved.History) == 0 {
		externalError(w, 422, "history_unavailable", "This chat has no saved conversation yet. Send a message first.")
		return
	}
	choice, err := effectiveProfileChatModel(slot.profile, slot.binding, source.Record, source.Identity)
	if err != nil {
		externalFailure(w, err)
		return
	}
	query := QueryRequest{Provider: choice.Provider, ModelID: choice.ModelID, ConnectionID: choice.ConnectionID}
	if err := constrainProductChatModel(r.Context(), &query); err != nil {
		externalError(w, 422, "model_unavailable", err.Error())
		return
	}
	project := crewResourceProjectID(source.Record.ConversationKey)
	key := project + codeChatSideMarker + "fork-" + uuid.NewString()
	baseName := []rune(firstNonEmptyTrimmed(source.Identity.Name, source.Record.Title, "Chat"))
	if len(baseName) > productAgentNameMaxRunes-7 {
		baseName = baseName[:productAgentNameMaxRunes-7]
	}
	name := string(baseName) + " (fork)"
	request := r.Clone(r.Context())
	request.Header = r.Header.Clone()
	request.Header.Del("X-Session-ID")
	request.Header.Del("X-Conversation-Continuation")
	destination, err := api.resolveAgentProfileConversation(request, slot.profile, key, name)
	if err != nil {
		externalFailure(w, err)
		return
	}
	if _, err = defaultProductConversationRegistryStore().setAgentModel(r.Context(), user, slot.profile.ID, key, choice.Engine, query.ModelID, choice.ReasoningEffort, query.ConnectionID, user); err != nil {
		externalFailure(w, err)
		return
	}
	if source.Identity.ClaudeTerminal && query.Provider == "claude-code" {
		terminal := true
		if _, err := defaultProductConversationRegistryStore().setChatContextOptions(r.Context(), user, slot.profile.ID, key, chatContextChange{ClaudeTerminal: &terminal}, user); err != nil {
			externalFailure(w, err)
			return
		}
	}
	destination.Provider, destination.ModelID, destination.ConnectionID = query.Provider, query.ModelID, query.ConnectionID
	runtime, nativeErr := api.forkCrewNativeRuntime(r, source.Record, destination)
	note := "Conversation copied into an independent chat."
	if runtime == nil {
		note += " This CLI will start a new native session using the copied conversation."
	}
	if nativeErr != nil {
		log.Printf("[CHAT_FORK] source=%s session=%s native=false reason=%q", source.Record.SessionID, destination.SessionID, nativeErr.Error())
	}
	if runtime != nil {
		applyCrewNativeToolPolicy(&slot.profile, true, slot.binding.ProjectNativeAgentTools)
		runtime.WorkshopMode, runtime.AgentToolsMode = crewCLIMode(false), normalizeAgentToolsMode(slot.profile.Runtime.AgentTools.Mode)
	}
	path, pathOK := workProjectChatHistoryConversationPath(user, destination.WorkspacePath, destination.SessionID, time.Now())
	if !pathOK || path == "" {
		externalError(w, 500, "history_unavailable", "Could not save the copied conversation. Try again.")
		return
	}
	data, err := json.Marshal(map[string]any{"session_id": destination.SessionID, "user_id": user, "agent_mode": "multi-agent", "workspace_path": destination.WorkspacePath, "workshop_mode": crewCLIMode(false), "title": name, "conversation_history": saved.History, "runtime": runtime, "created_at": destination.CreatedAt, "updated_at": time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		externalFailure(w, err)
		return
	}
	if err = writeRawFileToWorkspace(r.Context(), path, string(data)); err != nil {
		externalFailure(w, err)
		return
	}
	if err = writeChatHistoryResumeSnapshot(r.Context(), path, data); err != nil {
		externalFailure(w, err)
		return
	}
	if err = updatePersistedChatHistoryIndex(user, destination.SessionID, "multi-agent", saved.History, runtime, path, int64(len(data)), time.Now(), nil); err != nil {
		externalFailure(w, err)
		return
	}
	log.Printf("[CHAT_FORK] project=%s source=%s session=%s native=%t messages=%d", project, source.Record.SessionID, destination.SessionID, runtime != nil, len(saved.History))
	writeAgentProfileJSON(w, 200, map[string]any{"conversation_key": key, "conversation_id": destination.ConversationID, "session_id": destination.SessionID, "name": name, "engine": choice.Engine, "provider": query.Provider, "model_id": query.ModelID, "connection_id": query.ConnectionID, "reasoning_effort": choice.ReasoningEffort, "native_resume": runtime != nil, "next": note})
}

func (api *StreamingAPI) forkCrewNativeRuntime(r *http.Request, source, destination ProductConversationRecord) (*ChatHistoryAgentRuntime, error) {
	user := productWorkspaceUserID(r.Context())
	original, found, err := ReadChatHistoryRuntimeForSession(user, source.SessionID, source.WorkspacePath)
	if err != nil {
		return nil, err
	}
	if !found || original == nil || original.AgentSessionHandle == nil {
		return nil, fmt.Errorf("no saved native session")
	}
	handle := original.AgentSessionHandle.Provider
	if handle.NativeSessionID == "" || handle.Provider != destination.Provider {
		return nil, fmt.Errorf("no matching native session")
	}
	roots, found, err := crewStorageRoots(source.WorkspacePath)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("Crew runtime unavailable")
	}
	return api.copyCrewNativeFork(r, roots, source, destination, original)
}

func (api *StreamingAPI) copyCrewNativeFork(r *http.Request, roots crewroot.Roots, source, destination ProductConversationRecord, original *ChatHistoryAgentRuntime) (*ChatHistoryAgentRuntime, error) {
	claims := GetUserFromContext(r.Context())
	handle := original.AgentSessionHandle.Provider
	source.Provider = handle.Provider
	if original.AgentSessionHandle.ConnectionID != "" {
		source.ConnectionID = original.AgentSessionHandle.ConnectionID
	}
	store, runtimeDir, err := api.crewProgramStore(r.Context(), claims, roots, source, handle.Transport, false)
	if err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(roots.Private, "chat-fork-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	cwds := []string{handle.WorkingDir}
	if cwd, e := crewCLIWorkingDir(roots.Virtual, claims.UserID, source.SessionID, handle.Provider, false); e == nil {
		cwds = append([]string{cwd}, cwds...)
	}
	if handle.Provider == "claude-code" {
		cwds = append(cwds, claudeSessionWorkingDirs(store, handle.NativeSessionID, handle.WorkingDir)...)
	}
	var bundle *sessiontransfer.Bundle
	for _, cwd := range cwds {
		if cwd == "" {
			continue
		}
		bundle, err = exportCrewNativeProgram(r.Context(), claims.UserID, runtimeDir, store, cwd, handle.NativeSessionID, handle.Provider, handle.Transport, filepath.Join(staging, "source"), false)
		if err == nil && bundle != nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	if bundle == nil {
		return nil, fmt.Errorf("native conversation unavailable")
	}
	// Match local import's existing full-copy budget before making another
	// native copy. Saved dialogue remains the continuity path for larger chats.
	var bytes int64
	for _, file := range bundle.Files {
		bytes += file.Size
	}
	free, spaceErr := localChatFreeBytes(roots.Private)
	if bytes > 64<<20 || spaceErr != nil || free < uint64(bytes)*3+(256<<20) {
		return nil, fmt.Errorf("native copy budget exceeded; continued from copied conversation")
	}
	cwd, err := crewCLIWorkingDir(roots.Virtual, claims.UserID, destination.SessionID, handle.Provider, false)
	if err != nil {
		return nil, err
	}
	// Import alone preserves the native ID. Always fork first: unsupported
	// formats use saved history instead of sharing a writer with the original.
	fork, err := sessiontransfer.ForkJSONL(bundle, filepath.Join(staging, "fork"), cwd)
	if err != nil {
		return nil, err
	}
	return api.importCrewNativeInto(r.Context(), claims, roots, destination, destination.SessionID, fork, cwd)
}
