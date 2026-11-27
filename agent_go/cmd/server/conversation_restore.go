package server

import (
	"log"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func (api *StreamingAPI) captureChatHistoryAgentRuntime(sessionID, provider, modelID, workspacePath string, underlyingAgent *mcpagent.Agent) *ChatHistoryAgentRuntime {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelID = strings.TrimSpace(modelID)
	workspacePath = strings.TrimSpace(workspacePath)
	if provider == "" && modelID == "" && workspacePath == "" && underlyingAgent == nil {
		return nil
	}

	runtime := &ChatHistoryAgentRuntime{
		Kind:          "llm_agent",
		Provider:      provider,
		ModelID:       modelID,
		WorkspacePath: workspacePath,
		CapturedAt:    time.Now().Format(time.RFC3339),
	}
	if common.IsCLIProvider(provider) {
		runtime.Kind = "coding_agent"
	}

	if underlyingAgent != nil {
		runtime.ModelOptionsKey = workflowModelOptionsKey(mcpagent.ReadAgentRuntimeInfo(underlyingAgent).LLMConfig.Options)
		if handle := mcpagent.SnapshotAgentSession(underlyingAgent); handle != nil && !handle.Empty() {
			runtime.AgentSessionHandle = handle
			if handle.Provider.Provider != "" && runtime.Provider == "" {
				runtime.Provider = strings.ToLower(strings.TrimSpace(handle.Provider.Provider))
			}
			if handle.Provider.Model != "" && runtime.ModelID == "" {
				runtime.ModelID = strings.TrimSpace(handle.Provider.Model)
			}
			if handle.Provider.Transport != "" && runtime.Transport == "" {
				runtime.Transport = strings.ToLower(strings.TrimSpace(handle.Provider.Transport))
			}
			if common.IsCLIProvider(runtime.Provider) {
				runtime.Kind = "coding_agent"
			}
			if handle.Provider.NativeSessionID != "" {
				runtime.ExternalSessionID = handle.Provider.NativeSessionID
				if codingAgentProviderSupportsNativeResume(runtime.Provider, runtime.ModelID) {
					runtime.ResumeSupported = true
				}
			}
			if handle.Provider.ProjectDirID != "" {
				runtime.ProjectDirID = handle.Provider.ProjectDirID
			}
		}
		provider = runtime.Provider
		if runtime.ExternalSessionID != "" {
			switch provider {
			case "claude-code", "cursor-cli":
				runtime.ResumeFlag = "--resume"
			case "codex-cli":
				runtime.ResumeFlag = "resume"
			case "pi-cli":
				runtime.ResumeFlag = "--session-id"
			}
			log.Printf("[%s] Saved native session ID %s for session %s", provider, runtime.ExternalSessionID, sessionID)
		}
		// Persist stable MCP server/tool selection only. Current prompts and
		// browser availability are rebuilt for each request.
		runtimeInfo := mcpagent.ReadAgentRuntimeInfo(underlyingAgent)
		runtime.ServerName = strings.TrimSpace(runtimeInfo.ConfiguredServerName)
		runtime.SelectedTools = runtimeInfo.SelectedTools
	}
	api.conversationMux.RLock()
	runtime.ChatPolicyKey = api.lastChatPolicyBySession[sessionID]
	runtime.ChatPolicyRoleKey = api.lastChatPolicyRoleBySession[sessionID]
	if snapshot, ok := chatPolicyRoleSnapshotFor(sessionID); ok {
		runtime.ChatPolicyRoleOrigin, runtime.ChatPolicyRoleAuthority = snapshot.Origin, snapshot.Authority
	}
	runtime.AgentProfileKey = api.lastAgentProfileKeyBySession[sessionID]
	runtime.AgentToolsMode = api.lastAgentToolsModeBySession[sessionID]
	api.conversationMux.RUnlock()
	if admitted, ok := api.agentProfileAdmissions.Load(underlyingAgent); ok {
		runtime.AgentProfileKey = admitted.(string)
	}
	runtime.AgentProfileKeyParts = agentProfileKeyPartsOf(runtime.AgentProfileKey)

	normalizeChatHistoryRuntime(runtime)

	return runtime
}

// seedCodingAgentRuntimeFromCurrentConversation recovers this session's own
// persisted native-resume handle (when the in-memory agent is gone — e.g. after a
// long idle) and seeds it onto the fresh /api/query agent. It returns the
// recovered runtime alongside the success flag so the caller can route an
// idled-out active tab through the FIX B re-launch (relaunch with --resume +
// re-materialize the live tmux) instead of starting a fresh agent.
func (api *StreamingAPI) seedCodingAgentRuntimeFromCurrentConversation(sessionID, userID, currentProvider, currentWorkshopMode, workspacePath string, underlyingAgent *mcpagent.Agent) (bool, *ChatHistoryAgentRuntime) {
	if api == nil || underlyingAgent == nil || codingAgentHasNativeResume(currentProvider, underlyingAgent) {
		return false, nil
	}
	runtime, ok, err := ReadChatHistoryRuntimeForSession(userID, sessionID, workspacePath)
	if err != nil {
		log.Printf("[CHAT_HISTORY] Failed to read current conversation runtime for session %s: %v", sessionID, err)
		return false, nil
	}
	if !ok || runtime == nil {
		return false, nil
	}
	runtime = pickSavedRuntime(runtime, currentProvider)
	seeded := api.seedCodingAgentRuntimeFromRestoredConversation(sessionID, currentProvider, currentWorkshopMode, runtime, underlyingAgent)
	if seeded {
		log.Printf("[CHAT_HISTORY] Restored native coding-agent runtime from current conversation for session %s", sessionID)
		return true, runtime
	}
	return false, nil
}

// restorePersistedConversationHistory hydrates the process-local UI cache for a
// stable project session after a server restart. The transcript is authoritative
// for what the user sees, while a coding provider's native session handle is
// authoritative for agent context. Workflow history is restored only into its
// original Workshop/Run boundary. Callers must never treat this return value as
// a reason to suppress a compatible native resume.
func (api *StreamingAPI) restorePersistedConversationHistory(sessionID, userID, workspacePath, currentWorkshopMode string) bool {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}

	api.conversationMux.RLock()
	_, alreadyLoaded := api.conversationHistory[sessionID]
	api.conversationMux.RUnlock()
	if alreadyLoaded {
		return false
	}

	target, ok, err := readRestoredChatHistoryPersistTargetForSession(userID, sessionID, workspacePath)
	if err != nil {
		log.Printf("[CHAT_HISTORY] Failed to restore durable conversation for session %s: %v", sessionID, err)
		return false
	}
	if !ok || target == nil || len(target.History) == 0 {
		return false
	}
	restoredMode := target.WorkshopMode
	if target.Runtime != nil {
		restoredMode = firstNonEmptyTrimmed(target.Runtime.WorkshopMode, restoredMode)
	}
	if !chatHistoryResumeModesCompatible(restoredMode, currentWorkshopMode) {
		log.Printf("[CHAT_HISTORY] Skipping durable conversation replay for session %s: restored mode=%s current mode=%s", sessionID, normalizeChatHistoryWorkshopMode(restoredMode), normalizeChatHistoryWorkshopMode(currentWorkshopMode))
		return false
	}

	// Recheck under the write lock so simultaneous reconnects cannot replace a
	// history that was populated by the first request.
	history := append([]llmtypes.MessageContent(nil), target.History...)
	api.conversationMux.Lock()
	if _, exists := api.conversationHistory[sessionID]; exists {
		api.conversationMux.Unlock()
		return false
	}
	if api.conversationHistory == nil {
		api.conversationHistory = make(map[string][]llmtypes.MessageContent)
	}
	api.replaceConversationHistoryLocked(sessionID, history)
	api.conversationMux.Unlock()

	// Keep writing to the same conversation JSON after recovery. This matters
	// when a session crosses a date boundary and prevents a second transcript
	// from being created for the same project.
	api.rememberRestoredConversationPersistTarget(sessionID, *target)
	log.Printf("[CHAT_HISTORY] Restored %d durable UI messages for project session %s from %s", len(history), sessionID, target.ConversationPath)
	return true
}

func codingAgentHasNativeResume(provider string, underlyingAgent *mcpagent.Agent) bool {
	if underlyingAgent == nil {
		return false
	}
	handle := mcpagent.SnapshotAgentSession(underlyingAgent)
	if handle == nil || handle.Provider.Empty() || strings.TrimSpace(handle.Provider.NativeSessionID) == "" {
		return false
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != "" && !strings.EqualFold(handle.Provider.Provider, provider) {
		return false
	}

	// Provider identity alone is not a continuation. A newly configured coding
	// agent already has its provider/model/working directory, but it cannot
	// resume until the provider has returned a native conversation ID. Treating
	// that incomplete handle as resumable prevents the current conversation's
	// persisted handle from being restored on the next request.
	if strings.TrimSpace(handle.Provider.NativeSessionID) != "" {
		return true
	}
	// Codex can resume from its project directory when a native thread ID is
	// unavailable. Other providers require their native session ID.
	return strings.EqualFold(handle.Provider.Provider, "codex-cli") && strings.TrimSpace(handle.Provider.ProjectDirID) != ""
}

func codingAgentProviderSupportsNativeResume(provider, modelID string) bool {
	contract, ok := codingAgentProviderContract(provider, modelID)
	return ok && contract.SupportsNativeResume
}

func isCodingAgentProvider(provider, modelID string) bool {
	_, ok := codingAgentProviderContract(provider, modelID)
	return ok
}

func codingAgentProviderContract(provider, modelID string) (llmproviders.CodingAgentProviderContract, bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return llmproviders.CodingAgentProviderContract{}, false
	}
	return llmproviders.GetCodingAgentProviderContract(llmproviders.Provider(provider), strings.TrimSpace(modelID))
}

func (api *StreamingAPI) seedCodingAgentRuntimeFromRestoredConversation(sessionID, currentProvider, currentWorkshopMode string, runtime *ChatHistoryAgentRuntime, underlyingAgent *mcpagent.Agent) bool {
	if api == nil || underlyingAgent == nil || runtime == nil {
		return false
	}
	if !workflowCLIResumeAllowed(underlyingAgent, runtime) {
		log.Printf("[CHAT_HISTORY] Native resume skipped for session %s: CLI runtime directory changed; using saved application history", sessionID)
		return false
	}
	hasAgentSessionHandle := runtime.AgentSessionHandle != nil && !runtime.AgentSessionHandle.Empty()
	if runtime.Kind != "coding_agent" || (!runtime.ResumeSupported && !hasAgentSessionHandle) {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(runtime.Provider))
	if provider == "" && hasAgentSessionHandle {
		provider = strings.ToLower(strings.TrimSpace(runtime.AgentSessionHandle.Provider.Provider))
	}
	currentProvider = strings.ToLower(strings.TrimSpace(currentProvider))
	if provider == "" || (currentProvider != "" && provider != currentProvider) {
		return false
	}
	if !chatHistoryResumeModesCompatible(runtime.WorkshopMode, currentWorkshopMode) {
		log.Printf("[CHAT_HISTORY] Skipping native coding-agent resume for session %s: restored mode=%s current mode=%s", sessionID, normalizeChatHistoryWorkshopMode(runtime.WorkshopMode), normalizeChatHistoryWorkshopMode(currentWorkshopMode))
		return false
	}
	api.conversationMux.RLock()
	currentProfileKey, profileKeyKnown := api.lastAgentProfileKeyBySession[sessionID]
	api.conversationMux.RUnlock()
	// A changed profile definition (system prompt, tool allowlist, MCP servers,
	// Crew identity) must never cost the conversation its memory: the CLI is
	// relaunched with the current definition and resumes the same native
	// session. Retained live input is still refused for a stale process (see
	// agentProfileAllowsRetainedLiveInput), which is what forces that relaunch.
	if profileKeyKnown && strings.TrimSpace(runtime.AgentProfileKey) != currentProfileKey {
		// Switching a Crew's native agent tools (hybrid <-> AgentWorks-only) is
		// a capability change, like Builder <-> Run: the old transcript is full
		// of tool use the new mode cannot perform. Start fresh with the recent
		// dialogue instead of resuming it.
		api.conversationMux.RLock()
		currentToolsMode, toolsModeKnown := api.lastAgentToolsModeBySession[sessionID]
		api.conversationMux.RUnlock()
		if toolsModeKnown && normalizeAgentToolsMode(runtime.AgentToolsMode) != normalizeAgentToolsMode(currentToolsMode) {
			log.Printf("[CHAT_HISTORY] Agent tools mode changed for session %s (%s -> %s); starting a fresh native session with the recent dialogue", sessionID, normalizeAgentToolsMode(runtime.AgentToolsMode), normalizeAgentToolsMode(currentToolsMode))
			return false
		}
		if !codingProviderReloadsInstructionsOnResume(provider) && !runtime.NativeHandoff {
			log.Printf("[CHAT_HISTORY] Agent profile definition changed for session %s (changed: %s); %s cannot load new instructions into a resumed session, starting a fresh one with the conversation archive", sessionID, agentProfileKeyPartsChange(runtime.AgentProfileKeyParts, currentProfileKey), provider)
			return false
		}
		log.Printf("[CHAT_HISTORY] Agent profile definition changed for session %s (changed: %s); resuming the same native coding-agent session with the current definition", sessionID, agentProfileKeyPartsChange(runtime.AgentProfileKeyParts, currentProfileKey))
	}
	externalSessionID := strings.TrimSpace(runtime.ExternalSessionID)
	projectDirID := strings.TrimSpace(runtime.ProjectDirID)
	currentOwnerSessionID := ""
	if current := mcpagent.SnapshotAgentSession(underlyingAgent); current != nil {
		currentOwnerSessionID = strings.TrimSpace(current.SessionID)
	}
	var resumeHandle mcpagent.AgentSessionHandle
	if hasAgentSessionHandle {
		resumeHandle = *runtime.AgentSessionHandle
		if externalSessionID == "" {
			externalSessionID = strings.TrimSpace(runtime.AgentSessionHandle.Provider.NativeSessionID)
		}
		if projectDirID == "" {
			projectDirID = strings.TrimSpace(runtime.AgentSessionHandle.Provider.ProjectDirID)
		}
	}
	if externalSessionID == "" && projectDirID == "" {
		return false
	}

	// Restore provider-native conversation state only. The current /api/query
	// path has already rebuilt the prompt, tools, browser intent, secrets, and
	// permissions. Re-applying persisted instructions here would overwrite live
	// state with a stale snapshot.

	if provider != "codex-cli" && externalSessionID == "" {
		return false
	}
	resumeHandle.SessionID = firstNonEmptyTrimmed(currentOwnerSessionID, sessionID)
	resumeHandle.OwnerID = resumeHandle.SessionID
	resumeHandle.Provider.Provider = provider
	resumeHandle.Provider.Model = firstNonEmptyTrimmed(resumeHandle.Provider.Model, runtime.ModelID)
	resumeHandle.Provider.NativeSessionID = externalSessionID
	resumeHandle.Provider.ProjectDirID = projectDirID
	if !mcpagent.ApplyAgentResumeHandle(underlyingAgent, &resumeHandle) {
		log.Printf("[%s] Rejected stale resume handle for session %s; using saved application history", provider, sessionID)
		return false
	}
	log.Printf("[%s] Restored native runtime from chat history for session %s", provider, sessionID)
	return true
}

// deserializeSerializedMessage converts a SerializedMessage (typed) back to llmtypes.MessageContent
//
//nolint:unused // kept for the serialized-history rehydration path during polling refactors.
func deserializeSerializedMessage(serialized unifiedevents.SerializedMessage) *llmtypes.MessageContent {
	var role llmtypes.ChatMessageType
	switch serialized.Role {
	case "human": // Standard value from llmtypes
		role = llmtypes.ChatMessageTypeHuman
	case "ai": // Standard value from llmtypes
		role = llmtypes.ChatMessageTypeAI
	case "tool": // Standard value from llmtypes
		role = llmtypes.ChatMessageTypeTool
	case "system": // Standard value from llmtypes
		role = llmtypes.ChatMessageTypeSystem
	case "user", "assistant": // Fallback for compatibility (shouldn't happen but handle gracefully)
		if serialized.Role == "user" {
			role = llmtypes.ChatMessageTypeHuman
		} else {
			role = llmtypes.ChatMessageTypeAI
		}
	default:
		// Default to human if unknown role
		log.Printf("[DESERIALIZE] Unknown role '%s', defaulting to human", serialized.Role)
		role = llmtypes.ChatMessageTypeHuman
	}

	msg := &llmtypes.MessageContent{
		Role:  role,
		Parts: []llmtypes.ContentPart{},
	}

	for _, part := range serialized.Parts {
		switch part.Type {
		case "text":
			if content, ok := part.Content.(string); ok {
				msg.Parts = append(msg.Parts, llmtypes.TextContent{Text: content})
			}
		case "tool_call":
			if contentMap, ok := part.Content.(map[string]interface{}); ok {
				toolCall := llmtypes.ToolCall{
					FunctionCall: &llmtypes.FunctionCall{}, // Initialize pointer
				}
				if id, ok := contentMap["id"].(string); ok {
					toolCall.ID = id
				}
				if fnName, ok := contentMap["function_name"].(string); ok {
					toolCall.FunctionCall.Name = fnName
				}
				if fnArgs, ok := contentMap["function_args"].(string); ok {
					toolCall.FunctionCall.Arguments = fnArgs
				}
				msg.Parts = append(msg.Parts, toolCall)
			}
		case "tool_response":
			if contentMap, ok := part.Content.(map[string]interface{}); ok {
				toolResp := llmtypes.ToolCallResponse{}
				if toolCallID, ok := contentMap["tool_call_id"].(string); ok {
					toolResp.ToolCallID = toolCallID
				}
				if content, ok := contentMap["content"].(string); ok {
					toolResp.Content = content
				}
				msg.Parts = append(msg.Parts, toolResp)
			}
		}
	}

	return msg
}
