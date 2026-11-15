package mcpagent

import (
	"context"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746: preserve the provider's list as a typed event, not a tool summary.
func TestStreamingManagerPublishesNativeChecklistSnapshots(t *testing.T) {
	listener := &recordingAgentEventListener{}
	a := &Agent{sessionID: "chat", listeners: []AgentEventListener{listener}}
	sm := &streamingManager{streamChan: make(chan llmtypes.StreamChunk, 8), streamingDone: make(chan bool, 1), startTime: time.Now()}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallStart, ToolName: "TodoWrite", ToolCallID: "todo1", ToolArgs: `{"todos":[{"content":"Inspect","status":"in_progress"}]}`}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallEnd, ToolName: "TodoWrite", ToolCallID: "todo1", ToolResult: "Updated"}
	// Repeated snapshots aren't progress; a completed list is still shown after text.
	for i := 0; i < 2; i++ {
		sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeCodingAgentPlan, CodingAgentPlan: &llmtypes.CodingAgentPlan{PlanID: "native", Provider: "codex-cli", Items: []llmtypes.CodingAgentPlanItem{{ID: "step-1", Text: "Inspect", Status: "completed"}}}}
	}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "Done"}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeCodingAgentPlan, CodingAgentPlan: &llmtypes.CodingAgentPlan{PlanID: "native", Items: []llmtypes.CodingAgentPlanItem{}}}
	close(sm.streamChan)
	go sm.processChunks(context.Background(), a)
	<-sm.streamingDone
	var snapshots []*events.CodingAgentPlanEvent
	for _, e := range listener.events {
		if p, ok := e.Data.(*events.CodingAgentPlanEvent); ok {
			snapshots = append(snapshots, p)
		}
	}
	if len(snapshots) != 3 {
		t.Fatalf("got %d snapshots, want running/done/clear", len(snapshots))
	}
	if snapshots[0].Items[0].Status != "running" || snapshots[1].Items[0].Status != "completed" || snapshots[2].Items == nil || len(snapshots[2].Items) != 0 {
		t.Fatalf("native checklist state changed: %+v", snapshots)
	}
	if snapshots[0].SessionID != "chat" {
		t.Fatalf("checklist lost owner chat: %+v", snapshots[0])
	}
}

// The retained path creates a fresh streaming manager on each read: the
// canonical turn, not that temporary manager, must own task deltas.
func TestClaudeTaskPlanRetainedTurnOwnership(t *testing.T) {
	ctx := withCanonicalTurnLifecycle(context.Background(), newCanonicalTurnLifecycle("turn-one"))
	p, ok := (&streamingManager{}).successfulToolPlan(ctx, "TaskCreate", `{"subject":"Inspect"}`, `Task #1 created successfully: Inspect`)
	if !ok || len(p.Items) != 1 {
		t.Fatal("create dropped")
	}
	p, ok = (&streamingManager{}).successfulToolPlan(ctx, "TaskUpdate", `{"taskId":"1","status":"completed"}`, `Updated task #1 status`)
	if !ok || p.Items[0].Status != "completed" {
		t.Fatal("later read lost task")
	}
	other := withCanonicalTurnLifecycle(context.Background(), newCanonicalTurnLifecycle("turn-two"))
	if _, ok = (&streamingManager{}).successfulToolPlan(other, "TaskUpdate", `{"taskId":"1","status":"completed"}`, `Updated task #1 status`); ok {
		t.Fatal("another turn inherited private task")
	}
}
