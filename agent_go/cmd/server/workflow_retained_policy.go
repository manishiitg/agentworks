package server

import (
	"context"
	"fmt"
	"strings"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

type workflowPolicyRefreshKey struct{}

// Check the same policy key used by normal definition construction, before
// either warm SDK delivery or cold terminal delivery can bypass construction.
// Do not publish the new key here: the reconnect path still needs the old key
// to rebuild the definition and preserve the conversation with a handoff.
func (api *StreamingAPI) workflowRetainedPolicyCompatible(ctx context.Context, session string, req QueryRequest) (bool, error) {
	if req.AgentProfileID != "" {
		return api.agentProfileRetainedPolicyCompatible(ctx, session, req)
	}
	folder := req.SelectedFolder
	if !strings.HasPrefix(folder, "Workflow/") {
		// Follow-ups often omit the folder and rely on session resumption.
		// Resolve it from the session's last known workspace so a manifest
		// provider change still trips the fingerprint compare below instead
		// of silently reusing a stale retained runtime.
		if active, ok := api.getActiveSession(session); ok && active != nil && strings.HasPrefix(active.WorkspacePath, "Workflow/") {
			folder = active.WorkspacePath
		} else {
			return true, nil
		}
	}
	validated, err := api.revalidateExecutionPrincipal(ctx, req)
	if err != nil {
		return false, err
	}
	access, err := api.conversationTargetAccess(validated, req)
	if err != nil || access == WorkflowAccessNone {
		if err == nil {
			err = fmt.Errorf("workflow access denied")
		}
		return false, err
	}
	manifest, found, err := ReadWorkflowManifest(validated, folder)
	if err != nil {
		return false, err
	}
	if found && manifest.Capabilities.LLMConfig != nil {
		selected, _ := workshopResolveLLMConfig(lockedPresetLLMConfig(manifest.Capabilities.LLMConfig))
		if selected != nil {
			api.lastQueryMu.RLock()
			previousRequest, warm := api.lastQueryRequests[session]
			api.lastQueryMu.RUnlock()
			if warm {
				provider, model, connection := previousRequest.Provider, previousRequest.ModelID, previousRequest.ConnectionID
				if previousRequest.LLMConfig != nil {
					connection = previousRequest.LLMConfig.Primary.ConnectionID
				}
				if provider != selected.Provider || model != selected.ModelID || connection != selected.ConnectionID {
					return false, nil
				}
			} else {
				runtime, exists, readErr := ReadChatHistoryRuntimeForSession(GetUserIDFromContext(validated), session, folder)
				if readErr != nil {
					return false, readErr
				}
				if !exists || runtime == nil || runtime.Provider != selected.Provider || runtime.ModelID != selected.ModelID {
					return false, nil
				}
				// Cold restores cannot prove account identity from legacy snapshots.
				if selected.ConnectionID != "" {
					return false, nil
				}
			}
		}
	}
	active, _ := api.getActiveSession(session)
	workflowKind := ""
	if found && manifest.Kind == "relay" {
		workflowKind = "relay"
	}
	key := api.chatPolicySessionKey(resolveWorkflowChatPolicy(session, req, active, readOnlyForRequest(access, req)), workflowKind)
	api.conversationMux.RLock()
	previous, known := api.lastChatPolicyBySession[session]
	api.conversationMux.RUnlock()
	if known {
		return previous == key, nil
	}
	runtime, found, err := ReadChatHistoryRuntimeForSession(GetUserIDFromContext(validated), session, req.SelectedFolder)
	if err != nil {
		return false, err
	}
	// An unverified old native process must not retain stale permissions.
	return found && runtime != nil && runtime.ChatPolicyKey == key, nil
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
func (api *StreamingAPI) prepareWorkflowRetainedDelivery(ctx context.Context, session string, req QueryRequest, eligible bool) (bool, error) {
	if !eligible || api.externalBuilderOwnsSession(session) {
		return false, nil
	}
	compatible, err := api.workflowRetainedPolicyCompatible(ctx, session, req)
	if err == nil && !compatible {
		api.interruptWorkflowPolicySession(session, req.Provider)
	}
	return compatible, err
}

// Live-input requests carry no fresh product configuration. Resolve it from
// current trusted project state before allowing the native CLI to receive input.
func (api *StreamingAPI) agentProfileRetainedPolicyCompatible(ctx context.Context, session string, req QueryRequest) (bool, error) {
	user := GetUserIDFromContext(ctx)
	profile, err := api.resolveAgentProfileForQuery(ctx, &req, user, session)
	if err != nil {
		return false, err
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
