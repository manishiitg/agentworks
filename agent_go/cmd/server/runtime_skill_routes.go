package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/cliruntime"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	mcpagent "github.com/manishiitg/mcpagent/agent"
)

type runtimeSkillsResponse struct {
	*skills.RuntimeInventory
	SessionID string `json:"session_id"`
	Provider  string `json:"provider,omitempty"`
	Directory string `json:"directory,omitempty"`
	Prepared  bool   `json:"prepared"`
	Message   string `json:"message,omitempty"`
	ReadAt    string `json:"read_at"`
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
	content, exists, readErr := readFileFromWorkspace(ctx, workflowBuilderBindingPath(claims.UserID, manifest.ID))
	if readErr != nil {
		return "", "", "", 500, readErr
	}
	if !exists {
		return "", "", "workshop", 0, nil
	}
	var binding workflowBuilderBinding
	if json.Unmarshal([]byte(content), &binding) != nil || binding.UserID != claims.UserID || binding.SessionID == "" {
		return "", "", "", 500, fmt.Errorf("invalid current Builder binding")
	}
	if requested != "" && binding.SessionID != requested {
		return "", "", "", 409, fmt.Errorf("the current Builder changed; reload its skills")
	}
	return binding.SessionID, "", "workshop", 0, nil
}

func (api *StreamingAPI) runtimeSkillsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	workspace := r.URL.Query().Get("workspace_path")
	session, provider, mode, status, err := currentSkillBuilder(r.Context(), workspace, strings.TrimSpace(r.URL.Query().Get("session_id")))
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	response := runtimeSkillsResponse{RuntimeInventory: &skills.RuntimeInventory{Skills: []skills.RuntimeSkill{}, Folders: []string{}, Warnings: []string{}}, SessionID: session, Provider: provider, ReadAt: time.Now().UTC().Format(time.RFC3339Nano)}
	send := func() { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(response) }
	if session == "" {
		response.Message = "This Builder has no current session yet. Start its chat to prepare runtime skills."
		send()
		return
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
			http.Error(w, "could not read the current Builder's runtime identity", 500)
			return
		}
	}
	if runtime == nil || runtime.AgentSessionHandle == nil || runtime.AgentSessionHandle.Provider.WorkingDir == "" {
		response.Message = "This Builder has no prepared local runtime folder. Send a message in its current chat to prepare one."
		send()
		return
	}
	if runtime.WorkshopMode == "run" {
		response.Message = "The current chat has a Run runtime. Open its Builder chat to inspect Builder skills."
		send()
		return
	}
	if provider != "" && provider != runtime.Provider {
		response.Message = "The selected provider has not prepared this Builder's runtime yet."
		send()
		return
	}
	provider = runtime.Provider
	response.Provider = provider
	state, err := workflowCLIStateRoot()
	if err != nil {
		http.Error(w, "runtime storage unavailable", 500)
		return
	}
	legacy := ""
	if shared, ok := workspaceref.MustParse(workspace).SharedProject(); ok {
		legacy = crewLegacyKey(shared)
	}
	dir, err := cliruntime.ExistingLinkedProject(state, fsutil.WorkspaceDocsRoot(), user, codingAgentWorkspaceWorkingDir(workspace), legacy, session, provider, mode)
	if os.IsNotExist(err) {
		response.Message = "This Builder's runtime folder is not present on this server. No older session was substituted."
		send()
		return
	}
	if err != nil || !cliruntime.CanResume(dir, runtime.AgentSessionHandle.Provider.WorkingDir) {
		http.Error(w, "current runtime does not match this Builder and workspace", http.StatusConflict)
		return
	}
	inventory, err := skills.ReadRuntimeInventory(dir)
	if err != nil {
		http.Error(w, "could not read this Builder's runtime skills", 500)
		return
	}
	// Recheck the pointer after disk reads so a newly rotated Builder cannot
	// receive content from the previously current session.
	if _, _, _, status, err = currentSkillBuilder(r.Context(), workspace, session); err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if file := r.URL.Query().Get("file"); file != "" {
		content, err := skills.ReadRuntimeSkillFile(dir, file, inventory)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"path": file, "content": content, "session_id": session})
		return
	}
	response.RuntimeInventory, response.Directory, response.Prepared = inventory, dir, true
	send()
}
