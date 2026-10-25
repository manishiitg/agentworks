package server

import (
	"fmt"
	"strings"
	"time"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
)

func (api *StreamingAPI) diagnoseAndCleanupStalledConversationTurn(sessionID, rootExecutionID string, since time.Time, baseErr error) error {
	if api == nil || baseErr == nil {
		return baseErr
	}
	sessionID = strings.TrimSpace(sessionID)
	if evidence := api.diagnoseStalledConversationTurn(sessionID, since); evidence != "" {
		baseErr = fmt.Errorf("%w; provider-side completion evidence: %s", baseErr, evidence)
	}
	api.cleanupOrphanedConversationTurn(sessionID, rootExecutionID)
	return baseErr
}

// diagnoseStalledConversationTurn is a best-effort read, not a live
// completion oracle. It reads api.runningAgents without releasing/awaiting
// anything the stalled goroutine holds, and every provider lookup below is
// the same passive file read the interactive adapters already treat as safe
// to run concurrently with a live turn.
func (api *StreamingAPI) diagnoseStalledConversationTurn(sessionID string, since time.Time) string {
	if api == nil || sessionID == "" {
		return ""
	}
	api.runningAgentsMux.RLock()
	agent := api.runningAgents[sessionID]
	api.runningAgentsMux.RUnlock()
	if agent == nil {
		return ""
	}
	handle := mcpagent.SnapshotAgentSession(agent)
	if handle == nil || handle.Provider.Empty() {
		return ""
	}
	provider := strings.ToLower(strings.TrimSpace(handle.Provider.Provider))
	workingDir := strings.TrimSpace(handle.Provider.WorkingDir)
	nativeSessionID := strings.TrimSpace(handle.Provider.NativeSessionID)

	switch provider {
	case strings.ToLower(string(llm.ProviderCodexCLI)):
		if workingDir == "" {
			return ""
		}
		found, completedAt, message := codexcli.DiagnoseTurnCompletion(workingDir, since)
		if !found {
			return ""
		}
		message = strings.TrimSpace(message)
		if message == "" {
			message = "(no final message recorded)"
		}
		return fmt.Sprintf(
			"Codex CLI's own rollout shows this turn completed at %s: %q — the platform's bridge never observed it (PLAT-116)",
			completedAt.Format(time.RFC3339), message,
		)
	case strings.ToLower(string(llm.ProviderClaudeCode)):
		if nativeSessionID == "" {
			return ""
		}
		found, text := claudecode.DiagnoseTurnCompletion(nativeSessionID, workingDir, since)
		if !found {
			return ""
		}
		text = strings.TrimSpace(text)
		if text == "" {
			text = "(no final message recorded)"
		}
		return fmt.Sprintf(
			"Claude Code's own transcript shows this turn committed a final response: %q — the platform's bridge never observed it (PLAT-116)",
			text,
		)
	default:
		// Pi CLI's marker file has no OS-standard location to read from outside
		// its live session registry (no export exists yet); Cursor's default
		// transport never reaches this interactive wait path at all.
		return ""
	}
}

// cleanupOrphanedConversationTurn settles one foreground turn. The input lane
// guarantees a session has at most one such turn, so the session-keyed cancel
// function is the cancel function for rootExecutionID. The durable mcpagent
// Session intentionally survives: it is conversation state, not turn state.
// Closing its HTTP session or browser resources here is a conversation-level
// stop and is reserved for cancelSessionRuntimeWork/handleStopSession.
//
// Reproduced live (PLAT-116 follow-up): the first version of this function
// cleared only the coarse active-session status, not api.sessionBusy or the
// TrackedWorkflowExecution record for rootExecutionID. Both of those, not
// session status, are what the workflow selector's spinner
// (ModePresetBar.tsx, currentSessionTone) and Global Monitor's runtime_state
// actually key off, so a stalled turn kept showing as "running" in the UI
// for hours after the scheduler had already recorded it as errored.
func (api *StreamingAPI) cleanupOrphanedConversationTurn(sessionID, rootExecutionID string) {
	if api == nil || sessionID == "" {
		return
	}
	rootExecutionID = strings.TrimSpace(rootExecutionID)
	api.agentCancelMux.Lock()
	turnCancel := api.agentCancelFuncs[sessionID]
	api.agentCancelMux.Unlock()
	if turnCancel != nil {
		turnCancel()
	}
	api.updateSessionStatus(sessionID, "error")
	api.setSessionBusy(sessionID, false)
	api.completeTrackedExecution(rootExecutionID, trackedExecutionStatusFailed, "workshop idle wait timed out", nil)
	api.removeSessionQueryID(sessionID, rootExecutionID)
}
