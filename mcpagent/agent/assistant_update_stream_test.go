package mcpagent

import (
	"context"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"testing"
)

func TestStreamingAssistantUpdatePresentationSurvives(t *testing.T) {
	listener := &recordingAgentEventListener{}
	a := &Agent{listeners: []AgentEventListener{listener}}
	sm := &streamingManager{streamChan: make(chan llmtypes.StreamChunk, 2), streamingDone: make(chan bool, 1)}
	go sm.processChunks(context.Background(), a)
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeReasoning, Content: "Checking the file.", Metadata: map[string]interface{}{"presentation": "assistant_update"}}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeReasoning, Content: "Ordinary reasoning."}
	close(sm.streamChan)
	<-sm.streamingDone
	if len(listener.events) != 2 {
		t.Fatalf("events = %d", len(listener.events))
	}
	first := listener.events[0].Data.(*events.ConversationThinkingEvent)
	second := listener.events[1].Data.(*events.ConversationThinkingEvent)
	if first.Metadata["presentation"] != "assistant_update" || first.Thinking != "Checking the file." {
		t.Fatalf("lost update: %+v", first)
	}
	if second.Metadata["presentation"] != nil {
		t.Fatal("other providers must retain their presentation")
	}
}

func TestStreamingEmptyThinkingActivitySurvives(t *testing.T) {
	listener := &recordingAgentEventListener{}
	a := &Agent{listeners: []AgentEventListener{listener}}
	sm := &streamingManager{streamChan: make(chan llmtypes.StreamChunk, 1), streamingDone: make(chan bool, 1)}
	go sm.processChunks(context.Background(), a)
	sm.streamChan <- llmtypes.StreamChunk{
		Type:     llmtypes.StreamChunkTypeReasoning,
		Metadata: map[string]interface{}{"thinking_active": true},
	}
	close(sm.streamChan)
	<-sm.streamingDone

	if len(listener.events) != 1 {
		t.Fatalf("events = %d, want 1", len(listener.events))
	}
	thinking := listener.events[0].Data.(*events.ConversationThinkingEvent)
	if thinking.Thinking != "" || thinking.Metadata["thinking_active"] != true {
		t.Fatalf("thinking event = %+v, want explicit empty thinking activity", thinking)
	}
}
