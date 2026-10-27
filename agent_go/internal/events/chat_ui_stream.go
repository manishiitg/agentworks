package events

import pkgevents "github.com/manishiitg/mcpagent/events"

// Rendering follows native message framing, never a provider name or pane text.
// Run after CloneAgentEvent: callers must not observe mutations to their event.
func stampChatUIStreamMode(event Event) {
	if event.Data == nil {
		return
	}
	chunk, ok := event.Data.Data.(*pkgevents.StreamingChunkEvent)
	if !ok || chunk == nil {
		return
	}
	mode := "complete"
	if chunk.Source == pkgevents.StreamingChunkSourceContent && chunk.IsDelta && !chunk.IsToolCall {
		mode = "progressive"
	}
	if chunk.Metadata == nil {
		chunk.Metadata = map[string]interface{}{}
	}
	chunk.Metadata["chat_ui_stream_mode"] = mode
}
