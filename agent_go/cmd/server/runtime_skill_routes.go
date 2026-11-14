package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/cliruntime"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	mcpagent "github.com/manishiitg/mcpagent/agent"
)

type runtimeSkillsResponse struct {
	*skills.RuntimeInventory
	SessionID        string `json:"session_id"`
	Provider         string `json:"provider,omitempty"`
	Transport        string `json:"transport,omitempty"`
	Directory        string `json:"directory,omitempty"`
	WorkingDirectory string `json:"-"`
	Prepared         bool   `json:"prepared"`
	Message          string `json:"message,omitempty"`
	ReadAt           string `json:"read_at"`
}

// currentSkillBuilder never rotates a conversation, scans history for the newest
// chat, or falls back to a previous slot. Workspace admission runs before it.
func currentSkillBuilder(ctx context.Context, workspace, requested string) (session, provider, mode string, status int, err error) {
	claims := GetUserFromContext(ctx)
	if claims == nil {
		return "", "", "", http.StatusUnauthorized, fmt.Errorf("sign in to read runtime skills")
	}
	if crew, ok := resolveCrewPath(ctx, claims.UserID, workspace); ok {
		// Builder projections contain private authoring context. Shared Run access
		// to a Crew does not grant access to its author's native runtime.
		if crewAccessFor(claims, crew) != crewAccessOwner {
			return "", "", "", http.StatusForbidden, fmt.Errorf("Crew Builder access required")
		}
		if requested == "" {
			return "", "", "builder", 0, nil
		}
		store := defaultProductConversationRegistryStore()
		lock := productConversationRegistryMutex(productConversationRegistryPath(claims.UserID))
		lock.Lock()
		doc, loadErr := store.loadDocument(ctx, productConversationRegistryPath(claims.UserID))
		lock.Unlock()
		if loadErr != nil {
			return "", "", "", 500, loadErr
		}
		for _, record := range doc.Entries {
			if record.ProfileID == crewProfileID && record.SessionID == requested &&
				canonicalChatHistoryWorkspacePath(claims.UserID, record.WorkspacePath) == canonicalChatHistoryWorkspacePath(claims.UserID, workspace) {
				return record.SessionID, record.Provider, "builder", 0, nil
			}
		}
		return "", "", "", http.StatusConflict, fmt.Errorf("this chat is no longer a current Crew conversation; reopen the current chat")
	}
	if !strings.HasPrefix(workspace, "Workflow/") {
		return "", "", "", 400, fmt.Errorf("runtime skills require a Crew, Goal or Relay")
	}
	manifest, exists, readErr := ReadWorkflowManifest(ctx, workspace)
	if readErr != nil || !exists {
		return "", "", "", 404, fmt.Errorf("workflow unavailable")
	}
	records, readErr := defaultProductConversationRegistryStore().projectChats(ctx, claims.UserID, goalChatProfile, manifest.ID)
	if readErr != nil {
		return "", "", "", 500, readErr
	}
	for _, record := range records {
		if record.WorkspacePath != workspace {
			continue
		}
		if requested == record.SessionID || requested == "" && record.ConversationKey == crewOwnerDefaultChatKey(manifest.ID, claims.UserID) {
			return record.SessionID, record.Provider, "workshop", 0, nil
		}
	}
	if requested == "" {
		return "", "", "workshop", 0, nil
	}
	return "", "", "", 409, fmt.Errorf("this Builder tab is unavailable; reopen your tab")
}

func (api *StreamingAPI) runtimeSkillsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	response, status, err := api.loadRuntimeSkillInventory(r)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if file := r.URL.Query().Get("file"); file != "" && response.Prepared {
		content, err := skills.ReadRuntimeSkillFile(response.Directory, file, response.RuntimeInventory)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeUsersJSON(w, 200, map[string]string{"path": file, "content": content, "session_id": response.SessionID})
		return
	}
	writeUsersJSON(w, 200, response)
}

func (api *StreamingAPI) loadRuntimeSkillInventory(r *http.Request) (runtimeSkillsResponse, int, error) {
	response := runtimeSkillsResponse{RuntimeInventory: &skills.RuntimeInventory{Skills: []skills.RuntimeSkill{}, Folders: []string{}, Warnings: []string{}}, ReadAt: time.Now().UTC().Format(time.RFC3339Nano)}
	workspace := r.URL.Query().Get("workspace_path")
	session, provider, mode, status, err := currentSkillBuilder(r.Context(), workspace, strings.TrimSpace(r.URL.Query().Get("session_id")))
	if err != nil {
		return response, status, err
	}
	response.SessionID, response.Provider = session, provider

	if session == "" {
		response.Message = "This Builder has no current session yet. Start its chat to prepare runtime skills."
		return response, 0, nil
	}
	user := GetUserIDFromContext(r.Context())
	// The live provider handle wins. After a server restart, only this exact
	// current session's retained handle may locate its files; its skill content
	// is never read from transcript snapshots.
	var runtime *ChatHistoryAgentRuntime
	api.sessionAgentsMux.RLock()
	wrapper := api.sessionAgents[session]
	api.sessionAgentsMux.RUnlock()
	if wrapper != nil {
		if agent := wrapper.GetUnderlyingAgent(); agent != nil {
			if handle := mcpagent.SnapshotAgentSession(agent); handle != nil {
				runtime = &ChatHistoryAgentRuntime{Provider: handle.Provider.Provider, AgentSessionHandle: handle}
			}
		}
	}
	if runtime == nil {
		runtime, _, err = ReadChatHistoryRuntimeForSession(user, session, workspace)
		if err != nil {
			return response, 500, fmt.Errorf("could not read the current Builder's runtime identity")
		}
	}
	if runtime == nil || runtime.AgentSessionHandle == nil || runtime.AgentSessionHandle.Provider.WorkingDir == "" {
		response.Message = "This Builder has no prepared local runtime folder. Send a message in its current chat to prepare one."
		return response, 0, nil
	}
	if runtime.WorkshopMode == "run" {
		response.Message = "The current chat has a Run runtime. Open its Builder chat to inspect Builder skills."
		return response, 0, nil
	}
	if provider != "" && provider != runtime.Provider {
		response.Message = "The selected provider has not prepared this Builder's runtime yet."
		return response, 0, nil
	}
	response.Transport = runtime.AgentSessionHandle.Provider.Transport
	provider = runtime.Provider
	response.Provider = provider
	state, err := workflowCLIStateRoot()
	if err != nil {
		return response, 500, fmt.Errorf("runtime storage unavailable")
	}
	var dir string
	if _, crew := resolveCrewPath(r.Context(), user, workspace); crew {
		dir, err = existingCrewCLIWorkingDir(workspace, user, session, provider, false)
	} else {
		dir, err = cliruntime.ExistingLinkedProject(state, fsutil.WorkspaceDocsRoot(), user, codingAgentWorkspaceWorkingDir(workspace), workspace, session, provider, mode)
	}
	if os.IsNotExist(err) {
		response.Message = "This Builder's runtime folder is not present on this server. No older session was substituted."
		return response, 0, nil
	}
	if err != nil || !cliruntime.CanResume(dir, runtime.AgentSessionHandle.Provider.WorkingDir) {
		return response, 409, fmt.Errorf("current runtime does not match this Builder and workspace")
	}
	response.WorkingDirectory = dir
	if _, crew := resolveCrewPath(r.Context(), user, workspace); crew {
		artifact, readErr := existingCrewPrivateRuntimeDir(workspace, user, session, provider, false)
		if os.IsNotExist(readErr) {
			response.Message = "This chat's private skill files are not present. Send a message to prepare them."
			return response, 0, nil
		}
		if readErr != nil {
			return response, 409, fmt.Errorf("current private skill storage does not match this chat")
		}
		if artifact != "" {
			if !cliruntime.CanResume(artifact, runtime.AgentSessionHandle.Provider.RuntimeDir) {
				return response, 409, fmt.Errorf("current private runtime does not match this chat")
			}
			dir = artifact
		}
	}
	inventory, err := skills.ReadRuntimeInventory(dir)
	if err != nil {
		return response, 500, fmt.Errorf("could not read this Builder's runtime skills")
	}
	// Recheck the pointer after disk reads so a newly rotated Builder cannot
	// receive content from the previously current session.
	if _, _, _, status, err = currentSkillBuilder(r.Context(), workspace, session); err != nil {
		return response, status, err
	}

	response.RuntimeInventory, response.Directory, response.Prepared = inventory, dir, true
	return response, 0, nil
}
