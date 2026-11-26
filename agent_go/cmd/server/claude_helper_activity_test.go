package server

import (
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-872: real native helper progress must refresh its own parent's idle
// window, without keeping a different chat or child execution alive.
func TestNativeHelperProgressKeepsOnlyItsParentActive(t *testing.T) {
	now := time.Now()
	for _, kind := range []string{"main_agent", "workflow_step"} {
		state := turnInactivityState{sessionID: "parent", startedAt: now.Add(-time.Minute), lastOutputAt: now.Add(-time.Minute), executionID: "turn", tools: map[string]idleTool{}}
		activity := agentevents.NewAgentEvent(agentevents.NewCodingAgentNativeActivity(llmtypes.NativeRecord{Kind: "helper", State: "progress"}))
		activity.SessionID = "parent"
		event := storeevents.Event{SessionID: "parent", ExecutionID: "turn", Type: string(agentevents.CodingAgentNativeActivity), Timestamp: now, ExecutionKind: kind, Data: activity}
		state.observe(event, now)
		if state.lastOutputAt.Equal(now) != (kind == "main_agent") {
			t.Fatalf("progress attributed to wrong parent: %s", kind)
		}
	}
}

// A turn sitting on a long step said only "Opening drawers…": the quiet line must
// name the step by the agent's own summary, not a command's first line ("set -e").
func TestQuietTurnNamesTheStepItWaitsOn(t *testing.T) {
	now := time.Now()
	state := turnInactivityState{sessionID: "chat", startedAt: now.Add(-time.Hour), lastOutputAt: now.Add(-time.Hour), executionID: "turn", tools: map[string]idleTool{}}
	start := &agentevents.ToolCallStartEvent{ToolName: "set -e", ToolCallID: "call-1", ToolParams: agentevents.ToolParams{Arguments: `{"CommandLine":"set -e\n./deploy.sh verify","toolSummary":"Re-verify the pull request"}`}}
	state.observe(storeevents.Event{SessionID: "chat", ExecutionID: "turn", Type: "tool_call_start", Timestamp: now, ExecutionKind: "main_agent", Data: agentevents.NewAgentEvent(start)}, now)
	if got := state.waitingOn(); got != "Re-verify the pull request" {
		t.Fatalf("waiting on %q, want the step's own summary", got)
	}
	end := &agentevents.ToolCallEndEvent{ToolName: "set -e", ToolCallID: "call-1"}
	state.observe(storeevents.Event{SessionID: "chat", ExecutionID: "turn", Type: "tool_call_end", Timestamp: now, ExecutionKind: "main_agent", Data: agentevents.NewAgentEvent(end)}, now)
	if got := state.waitingOn(); got != "" {
		t.Fatalf("still waiting on finished step %q", got)
	}
}
