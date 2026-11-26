package server

import (
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"testing"
	"time"
)

func TestNativeTerminalPlanUsesTurnOwnedSnapshot(t *testing.T) {
	store := storeevents.NewEventStore(100)
	defer store.Stop()
	store.SetSessionOwner("chat", "person")
	o := &nativeTerminalObserver{api: &StreamingAPI{eventStore: store}, snapshot: terminals.Snapshot{SessionID: "chat"}}
	record := llmtypes.NativeRecord{ID: "result", Provider: "claude-code", SessionID: "native", TurnID: "turn", Kind: "plan", State: "updated", Timestamp: time.Now(), Plan: &llmtypes.CodingAgentPlan{Provider: "claude-code", Items: []llmtypes.CodingAgentPlanItem{{ID: "1", Text: "Inspect", Status: "pending"}}}}
	for _, status := range []string{"pending", "completed"} {
		record.ID = status
		record.Plan.Items[0].Status = status
		if err := o.acceptNativeCodingRecord(record); err != nil {
			t.Fatal(err)
		}
	}
	events := store.GetAllEventsRaw("chat")
	if len(events) != 2 {
		t.Fatalf("events=%d", len(events))
	}
	for _, event := range events {
		if event.Type != string(agentevents.CodingAgentPlan) || event.Data.TurnID != "native:turn" {
			t.Fatalf("wrong checklist ownership: %+v", event)
		}
	}
}
