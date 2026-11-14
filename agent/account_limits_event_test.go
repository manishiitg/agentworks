package mcpagent

import (
	"context"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"testing"
	"time"
)

// PLAT-746-990: a later account selection must not relabel an earlier stream.
func TestStatusLineKeepsGenerationAccountIdentity(t *testing.T) {
	listener := &recordingAgentEventListener{}
	a := &Agent{sessionID: "chat", listeners: []AgentEventListener{listener}, llmConfig: AgentLLMConfiguration{Primary: LLMModel{ConnectionID: "next-account"}}}
	sm := &streamingManager{accountConnectionID: "running-account", streamChan: make(chan llmtypes.StreamChunk, 1), streamingDone: make(chan bool, 1), startTime: time.Now()}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeStatusLine, StatusLine: &llmtypes.StatusLine{Provider: "codex-cli"}}
	close(sm.streamChan)
	go sm.processChunks(context.Background(), a)
	<-sm.streamingDone
	for _, event := range listener.events {
		if status, ok := event.Data.(*events.StreamingStatusLineEvent); ok {
			if status.ConnectionID != "running-account" {
				t.Fatalf("misattributed status: %q", status.ConnectionID)
			}
			return
		}
	}
	t.Fatal("status event missing")
}
