package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

// Canonical JSON treats nil/empty options alike and preserves all launch knobs.
func workflowModelOptionsKey(options map[string]interface{}) string {
	if len(options) == 0 {
		options = map[string]interface{}{}
	}
	data, err := json.Marshal(options)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

type workflowPolicyRefreshKey struct{}

// Check the same policy key used by normal definition construction, before
// either warm SDK delivery or cold terminal delivery can bypass construction.
// Do not publish the new key here: the reconnect path still needs the old key
// to rebuild the definition and preserve the conversation with a handoff.
func (api *StreamingAPI) workflowRetainedPolicyCompatible(ctx context.Context, session string, req QueryRequest) (bool, error) {
	compatible, _, err := api.workflowRetainedCompatibility(ctx, session, req)
	return compatible, err
}

// Runtime changes wait for the current turn; authority changes cannot retain
// old permissions. Check authority first so runtime drift never masks it.
func (api *StreamingAPI) workflowRetainedCompatibility(ctx context.Context, session string, req QueryRequest) (compatible, runtimeChanged bool, err error) {
	if req.AgentProfileID != "" {
		compatible, err := api.agentProfileRetainedPolicyCompatible(ctx, session, req)
		return compatible, false, err
	}
	folder := req.SelectedFolder
	active, _ := api.getActiveSession(session)
	if !strings.HasPrefix(folder, "Workflow/") {
		if active != nil && strings.HasPrefix(active.WorkspacePath, "Workflow/") {
			folder = active.WorkspacePath
		} else {
			return true, false, nil
		}
	}
	validated, err := api.revalidateExecutionPrincipal(ctx, req)
	if err != nil {
		return false, false, err
	}
	access, err := api.conversationTargetAccess(validated, req)
	if err != nil || access == WorkflowAccessNone {
		if err == nil {
			err = fmt.Errorf("workflow access denied")
		}
		return false, false, err
	}
	manifest, found, err := ReadWorkflowManifest(validated, folder)
	if err != nil {
		return false, false, err
	}
	workflowKind := ""
	if found && manifest.Kind == "relay" {
		workflowKind = "relay"
	}
	// The same key as the turn stored, Pulse parts included: without them every
	// follow-up to a Pulse workflow's Builder chat looked like a policy change
	// and cancelled the running turn (local 2026-10-08).
	key := api.chatPolicySessionKey(resolveWorkflowChatPolicy(session, req, active, readOnlyForRequest(access, req)), workflowKind) +
		pulsePolicyKeySuffix(session, req.PhaseID, folder)
	api.conversationMux.RLock()
	previous, known := api.lastChatPolicyBySession[session]
	api.conversationMux.RUnlock()
	var runtime *ChatHistoryAgentRuntime
	if known {
		if previous != key {
			return false, false, nil
		}
	} else {
		var exists bool
		runtime, exists, err = ReadChatHistoryRuntimeForSession(GetUserIDFromContext(validated), session, folder)
		if err != nil {
			return false, false, err
		}
		if !exists || runtime == nil || runtime.ChatPolicyKey != key {
			return false, false, nil
		}
	}
	if found && manifest.Capabilities.LLMConfig != nil {
		selected, _ := workshopResolveLLMConfig(lockedPresetLLMConfig(manifest.Capabilities.LLMConfig))
		if selected != nil {
			api.lastQueryMu.RLock()
			previousRequest, warm := api.lastQueryRequests[session]
			api.lastQueryMu.RUnlock()
			if warm {
				provider, model, connection := previousRequest.Provider, previousRequest.ModelID, previousRequest.ConnectionID
				var previousOptions map[string]interface{}
				if previousRequest.LLMConfig != nil {
					connection = previousRequest.LLMConfig.Primary.ConnectionID
					previousOptions = previousRequest.LLMConfig.Primary.Options
				}
				if provider != selected.Provider || model != selected.ModelID || connection != selected.ConnectionID || workflowModelOptionsKey(previousOptions) != workflowModelOptionsKey(selected.Options) {
					return false, true, nil
				}
			} else {
				if runtime == nil {
					runtime, _, err = ReadChatHistoryRuntimeForSession(GetUserIDFromContext(validated), session, folder)
					if err != nil {
						return false, false, err
					}
				}
				// Legacy snapshots cannot prove account/option identity. Rebuild
				// once while preserving the native conversation.
				if runtime == nil || runtime.Provider != selected.Provider || runtime.ModelID != selected.ModelID || selected.ConnectionID != "" || runtime.ModelOptionsKey == "" || runtime.ModelOptionsKey != workflowModelOptionsKey(selected.Options) {
					return false, true, nil
				}
			}
		}
	}
	return true, false, nil
}

// requestedProviderOf is the coding provider a request selects ("" when it names none).
func requestedProviderOf(req QueryRequest) string {
	provider := strings.TrimSpace(req.Provider)
	if provider == "" && req.LLMConfig != nil {
		provider = strings.TrimSpace(req.LLMConfig.Primary.Provider)
	}
	return provider
}

// effectiveProviderOf is the coding provider this request will actually run on, resolved in the order handleQuery applies: the manifest's LLM for a Builder chat
// (unless the request's LLM config comes from a product profile), then the server's locked default (a locked server honours the request only for a product profile
// or a published model), then the request's own choice. Comparing the retained CLI with the raw request provider called every send a provider change whenever the
// request named a provider that is never used (local Codex chat that answered "queued", RTS-style locked servers, 2026-10-04).
func (api *StreamingAPI) effectiveProviderOf(ctx context.Context, req QueryRequest) string {
	if req.AgentMode == "workflow_phase" && !requestLLMConfigOverridesManifest(req) {
		folder := strings.TrimSpace(req.SelectedFolder)
		if req.PresetQueryID != "" {
			if resolved, err := api.resolveWorkspacePathFromPreset(ctx, req.PresetQueryID); err == nil && resolved != "" {
				folder = resolved
			}
		}
		if folder != "" {
			if manifest, found, err := ReadWorkflowManifest(ctx, folder); err == nil && found && manifest != nil && manifest.Capabilities.LLMConfig != nil {
				if phaseLLM, _ := workshopResolveLLMConfig(lockedPresetLLMConfig(manifest.Capabilities.LLMConfig)); phaseLLM != nil && phaseLLM.Provider != "" && phaseLLM.ModelID != "" {
					return phaseLLM.Provider
				}
			}
		}
	}
	if isGlobalLLMConfigLocked() {
		provider, _ := resolveLockedLLM(req.LLMConfig, req.LLMConfigSource)
		return provider
	}
	return requestedProviderOf(req)
}

// retainedCLIProviderDiffers reports whether the CLI or agent retained for this chat runs a different provider than the request selects. It is false when the
// request names no provider or nothing is retained.
func (api *StreamingAPI) retainedCLIProviderDiffers(sessionID, requested string) bool {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if api == nil || requested == "" || strings.TrimSpace(sessionID) == "" {
		return false
	}
	live := ""
	if snapshot, ok := api.liveMainCodingTmuxSnapshot(sessionID); ok {
		live = retainedCodingAgentProvider(snapshot)
	} else {
		api.runningAgentsMux.RLock()
		agent := api.runningAgents[sessionID]
		api.runningAgentsMux.RUnlock()
		if agent != nil {
			live = string(mcpagent.ReadAgentRuntimeInfo(agent).Provider)
		}
	}
	live = strings.ToLower(strings.TrimSpace(live))
	return live != "" && live != requested
}

func (api *StreamingAPI) interruptWorkflowPolicySession(session, provider string) {
	api.conversationMux.Lock()
	delete(api.launchedAgentProfileKeyBySession, session)
	api.conversationMux.Unlock()

	// Close the process receiving messages, even when the original request
	// omitted its provider and setup selected it from the workflow manifest.
	if snapshot, live := api.liveMainCodingTmuxSnapshot(session); live {
		if actual := retainedCodingAgentProvider(snapshot); actual != "" {
			provider = actual
		}
	} else {
		api.runningAgentsMux.RLock()
		agent := api.runningAgents[session]
		api.runningAgentsMux.RUnlock()
		if agent != nil {
			provider = string(mcpagent.ReadAgentRuntimeInfo(agent).Provider)
		}
	}
	api.agentCancelMux.RLock()
	cancel := api.agentCancelFuncs[session]
	api.agentCancelMux.RUnlock()
	if cancel != nil {
		cancel()
	}
	closeWorkflowPolicyCLI(session, provider, "workflow chat policy changed; rebuilding definition")
}

func closeWorkflowPolicyCLI(session, provider, reason string) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "cursor-cli":
		llmproviders.CloseCursorCLIInteractiveSessionForOwner(session, reason)
	case "codex-cli":
		llmproviders.CloseCodexCLIInteractiveSessionForOwner(session, reason)
	case "claude-code":
		llmproviders.CloseClaudeCodeInteractiveSessionForOwner(session, reason)
	case "pi-cli":
		llmproviders.ClosePiCLIInteractiveSessionForOwner(session, reason)
	case "muse-cli":
		llmproviders.CloseMuseCLIInteractiveSessionForOwner(session, reason)
	case "agy-cli":
		llmproviders.CloseAgyCLIInteractiveSessionForOwner(session, reason)
	}
}

// Synthetic notifications and explicit new turns must wait for their normal
// lane; they cannot interrupt a foreground turn merely to try warm delivery.
func (api *StreamingAPI) prepareWorkflowRetainedDelivery(ctx context.Context, session string, req QueryRequest, eligible bool) (bool, bool, error) {
	if !eligible || api.externalBuilderOwnsSession(session) {
		return false, false, nil
	}
	return api.workflowRetainedCompatibility(ctx, session, req)
}

// Live-input requests carry no fresh product configuration. Resolve it from
// current trusted project state before allowing the native CLI to receive input.
func (api *StreamingAPI) agentProfileRetainedPolicyCompatible(ctx context.Context, session string, req QueryRequest) (bool, error) {
	user := GetUserIDFromContext(ctx)
	profile, _, admissionErr := api.admitQueryTarget(ctx, &req, user, session)
	if admissionErr != nil {
		return false, admissionErr
	}
	if profile == nil {
		return false, fmt.Errorf("product profile is unavailable")
	}
	key := agentProfileSessionKey(profile)
	api.conversationMux.RLock()
	launched, known := api.launchedAgentProfileKeyBySession[session]
	api.conversationMux.RUnlock()
	if known {
		return launched == key, nil
	}

	runtime, found, err := ReadChatHistoryRuntimeForSession(user, session, req.SelectedFolder)
	if err != nil {
		return false, err
	}
	return found && runtime != nil && runtime.AgentProfileKey == key, nil
}
