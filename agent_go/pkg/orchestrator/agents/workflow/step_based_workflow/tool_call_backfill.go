package step_based_workflow

import (
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/toolcallrecovery"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

func backfillMissingToolResults(sessionID string, toolCalls []orchestrator.ToolCallEntry, executionAgent agents.OrchestratorAgent) {
	if len(toolCalls) == 0 {
		return
	}

	for i := range toolCalls {
		entry := &toolCalls[i]
		if !toolCallEntryLooksOrphaned(*entry) {
			continue
		}
		if result, duration, ok := toolcallrecovery.Recover(sessionID, toolcallrecovery.Candidate{
			ToolName: entry.ToolName, StartedAt: entry.StartedAt,
		}); ok {
			entry.Result = result
			entry.Duration = duration
			if !entry.StartedAt.IsZero() {
				entry.CompletedAt = entry.StartedAt.Add(duration)
			}
		}
	}

	if executionAgent == nil {
		return
	}
	base := executionAgent.GetBaseAgent()
	if base == nil {
		return
	}
	handle := base.SessionHandle()
	if handle == nil || handle.Provider.Empty() {
		return
	}
	provider := strings.ToLower(strings.TrimSpace(handle.Provider.Provider))
	if provider != "claude-code" && provider != "claudecode" {
		// Only Claude Code's transcript shape is understood here. The other
		// interactive providers share the same underlying gap and are recorded
		// as follow-up in PLAT-142, not silently assumed fixed.
		return
	}
	nativeSessionID := strings.TrimSpace(handle.Provider.NativeSessionID)
	if nativeSessionID == "" {
		return
	}
	workingDir := strings.TrimSpace(handle.Provider.WorkingDir)

	var recovered map[string]claudecode.TranscriptToolResult
	for i := range toolCalls {
		entry := &toolCalls[i]
		if !toolCallEntryLooksOrphaned(*entry) {
			continue
		}
		if recovered == nil {
			recovered = claudecode.ToolResultsFromTranscript(nativeSessionID, workingDir)
		}
		found, ok := recovered[entry.ToolCallID]
		if !ok {
			continue
		}
		entry.Result = found.Result
		entry.Duration = found.Duration()
		if !found.EndedAt.IsZero() {
			entry.CompletedAt = found.EndedAt
		}
	}
}

// toolCallEntryLooksOrphaned reports whether an entry is Start-with-no-End —
// the shape a missing tool_call_end event leaves behind — rather than a call
// that genuinely failed or genuinely returned an empty string. Only that exact
// shape is safe to overwrite; anything else already has a real outcome.
func toolCallEntryLooksOrphaned(entry orchestrator.ToolCallEntry) bool {
	return entry.ToolCallID != "" && entry.Result == "" && entry.Error == "" && entry.Duration == 0
}
