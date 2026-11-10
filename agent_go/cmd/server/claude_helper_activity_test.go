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
