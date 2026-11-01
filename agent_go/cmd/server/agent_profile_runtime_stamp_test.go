package server

import (
	"testing"
	"time"

	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

func TestStampEventDataFillsAZeroTimestampOnly(t *testing.T) {
	now := time.Date(2026, 9, 3, 13, 30, 0, 0, time.UTC)
	ev := &orchestratorevents.ProductInteractionEvent{Product: "example-app", Kind: "suggestions"}
	stampEventData(ev, now)
	if !ev.Timestamp.Equal(now) {
		t.Fatalf("zero timestamp must be stamped, got %v", ev.Timestamp)
	}
	earlier := now.Add(-time.Hour)
	ev2 := &orchestratorevents.ProductInteractionEvent{BaseEventData: unifiedevents.BaseEventData{Timestamp: earlier}}
	stampEventData(ev2, now)
	if !ev2.Timestamp.Equal(earlier) {
		t.Fatalf("an existing timestamp must be kept, got %v", ev2.Timestamp)
	}
}
