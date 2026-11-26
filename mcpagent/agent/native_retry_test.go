package mcpagent

import (
	"context"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-987: report the CLI-owned retry; do not invoke another generation.
func TestStreamingManagerCarriesNativeRetryLifecycle(t *testing.T) {
	listener := &recordingAgentEventListener{}
	a := &Agent{sessionID: "retry-chat", listeners: []AgentEventListener{listener}}
	sm := &streamingManager{streamChan: make(chan llmtypes.StreamChunk, 2), streamingDone: make(chan bool, 1), startTime: time.Now()}
	for _, phase := range []string{"start", "end"} {
		sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeNativeRetry, NativeRetry: &llmtypes.NativeRetry{Provider: "pi-cli", NativeSessionID: "native", NativeTurnID: "turn", Phase: phase, Attempt: 2, MaxAttempts: 3, DelayMS: 1200, Success: phase == "end"}}
	}
	close(sm.streamChan)
	go sm.processChunks(context.Background(), a)
	<-sm.streamingDone
	var notices []*events.RetryAttemptEvent
	for _, event := range listener.events {
		if notice, ok := event.Data.(*events.RetryAttemptEvent); ok {
			notices = append(notices, notice)
		}
	}
	if len(notices) != 2 || notices[0].Phase != "native_retry_start" || notices[1].Phase != "native_retry_end" || !notices[1].Success || notices[0].AttemptIndex != 1 || notices[0].Duration != "1.2s" || notices[0].Error != "" || notices[1].Error != "" {
		t.Fatalf("native retry facts lost: %+v", notices)
	}
	if notices[0].Metadata["native_turn_id"] != "turn" || notices[0].SessionID != "retry-chat" {
		t.Fatal("retry lost session/turn attribution")
	}
}
