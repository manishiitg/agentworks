package server

import (
	"strings"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/toolcallrecovery"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

// installToolResultRecovery lets the event store recover tool calls the live
// stream never completed.
//
// PLAT-141: some tool calls produce no tool_call_end at all — one measured
// completing in 41ms with no end event under any session — so the chat showed a
// finished command as unresolved. The provider keeps its own complete record
// (the same session's transcript holds 215 tool_use and 215 tool_result), and
// this reads the real output and runtime back out of it.
//
// Wired here rather than inside the event store so that package keeps no
// provider knowledge: it asks for a result, this decides how to find one.
func (api *StreamingAPI) installToolResultRecovery() {
	storeevents.SetToolResultResolver(api.recoverToolResult)
}

// recoverToolResult resolves one tool call against its session's provider
// record. Returns ok=false whenever the answer would be a guess.
func (api *StreamingAPI) recoverToolResult(sessionID, toolCallID, toolName string, startedAt time.Time) (string, time.Duration, bool) {
	if api == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(toolCallID) == "" {
		return "", 0, false
	}

	// Tried first: toolcalllog is fed by every provider's HTTP bridge handler
	// identically and is essentially perfect (1 of 36 unpaired measured live).
	// No live agent handle, no transcript, no provider check needed — it is
	// already keyed by the same session id the orphaned event arrived under
	// (PLAT-143).
	if result, duration, ok := toolcallrecovery.Recover(sessionID, toolcallrecovery.Candidate{
		ToolName: toolName, StartedAt: startedAt,
	}); ok {
		return result, duration, true
	}

	// Fall back to the Claude-specific transcript recovery (PLAT-141/142) for
	// whatever toolcalllog does not cover. Every live coding-agent handle is a
	// candidate, not just the one under the
	// event's own session id.
	//
	// A workflow step runs in its own private working directory, so Claude Code
	// writes that step's transcript under a temp mlp-cli-session project slug
	// rather than the workflow's workspace. The tool events, however, are
	// recorded against the PARENT schedule session. Resolving only that parent's
	// handle therefore looks in the workflow's own transcript and misses every
	// step-issued call — measured on latency-report, 10 of 10 settled calls lived in
	// a temp directory the parent handle does not name.
	//
	// Tool-call ids are unique, so trying each live handle is safe: a hit can
	// only come from the transcript that actually recorded the call.
	api.runningAgentsMux.RLock()
	agents := make([]*mcpagent.Agent, 0, len(api.runningAgents)+1)
	if own := api.runningAgents[sessionID]; own != nil {
		agents = append(agents, own)
	}
	for id, agent := range api.runningAgents {
		if id != sessionID && agent != nil {
			agents = append(agents, agent)
		}
	}
	api.runningAgentsMux.RUnlock()

	for _, agent := range agents {
		handle := mcpagent.SnapshotAgentSession(agent)
		if handle == nil || handle.Provider.Empty() {
			continue
		}
		provider := strings.ToLower(strings.TrimSpace(handle.Provider.Provider))
		if provider != strings.ToLower(string(llm.ProviderClaudeCode)) && provider != "claudecode" {
			// Only Claude Code's transcript shape is understood here. Other
			// providers fall back to an empty settle rather than a wrong one;
			// their interactive transports have the same gap (PLAT-141).
			continue
		}
		nativeSessionID := strings.TrimSpace(handle.Provider.NativeSessionID)
		if nativeSessionID == "" {
			continue
		}
		entry, ok := claudecode.ToolResultsFromTranscript(nativeSessionID, strings.TrimSpace(handle.Provider.WorkingDir))[toolCallID]
		if !ok || strings.TrimSpace(entry.Result) == "" {
			continue
		}
		return entry.Result, entry.Duration(), true
	}
	return "", 0, false
}
