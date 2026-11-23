package llmtypes

import "context"

// StreamSink is the unified emission point every adapter uses to
// publish a StreamChunk. It fans out a single Emit() call to three
// destinations:
//
//  1. The caller's opts.StreamChan (so the chat UI / wrapper sees it)
//  2. The synthetic terminal log (so the terminal pane populates)
//  3. The inspector emitter (so debug-event timeline records it)
//
// Why this exists: adapters previously wrote to opts.StreamChan
// directly, then duplicated the call as term.AssistantText / term.ToolStart
// / inspector.EmitEvent. Each of those copies is a place an author
// can forget. Replacing the trio with sink.Emit(ctx, chunk) makes
// the unified observability surface structurally inescapable — the
// body literally has no other route to publish a chunk.
//
// The legacy direct-write path (opts.StreamChan <- chunk) still works
// for now, but the inline term/inspector calls are no longer needed
// once an adapter migrates fully.
type StreamSink struct {
	Ch        chan<- StreamChunk
	Term      *SyntheticTerminal
	Inspector *InspectorEmitter
}

// Emit publishes a chunk through the unified fan-out:
//   - forwards to Ch (if non-nil), respecting ctx cancellation
//   - routes content/tool/terminal chunks into the synthetic terminal
//   - mirrors content/tool deltas as inspector events
//
// Adapters call this in place of `opts.StreamChan <- chunk`. The
// auto-routing means inline term.AssistantText / inspector.EmitEvent
// calls are no longer needed alongside the channel send.
func (s *StreamSink) Emit(ctx context.Context, chunk StreamChunk) error {
	if s == nil {
		return nil
	}
	if s.Ch != nil {
		select {
		case s.Ch <- chunk:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.routeToTerminal(chunk)
	s.routeToInspector(chunk)
	return nil
}

// routeToTerminal mirrors a chunk into the synthetic terminal log.
// Terminal chunks (real pane snapshots) are skipped — they don't
// need to be re-buffered into the synthetic log, the caller's
// StreamChan already carries them.
func (s *StreamSink) routeToTerminal(chunk StreamChunk) {
	if s.Term == nil || !s.Term.Enabled() {
		return
	}
	switch chunk.Type {
	case StreamChunkTypeContent:
		if chunk.Content != "" {
			s.Term.AssistantText(chunk.Content)
		}
	case StreamChunkTypeToolCallStart, StreamChunkTypeToolCall:
		s.Term.ToolStart(chunk.ToolName, chunk.ToolArgs)
	case StreamChunkTypeToolCallEnd:
		s.Term.ToolEnd(chunk.ToolName, chunk.ToolResult, chunk.ToolDuration)
	}
}

// routeToInspector mirrors a chunk into the inspector emitter as
// either a content_delta event or a tool_call event.
func (s *StreamSink) routeToInspector(chunk StreamChunk) {
	if s.Inspector == nil || !s.Inspector.Enabled() {
		return
	}
	switch chunk.Type {
	case StreamChunkTypeContent:
		if chunk.Content != "" {
			s.Inspector.EmitEvent("content_delta", map[string]interface{}{
				"delta_text_length": len(chunk.Content),
			})
		}
	case StreamChunkTypeToolCallStart, StreamChunkTypeToolCall:
		s.Inspector.EmitToolCall(map[string]interface{}{
			"tool_name":    chunk.ToolName,
			"tool_call_id": chunk.ToolCallID,
			"phase":        "start",
		})
	case StreamChunkTypeToolCallEnd:
		s.Inspector.EmitToolCall(map[string]interface{}{
			"tool_name":    chunk.ToolName,
			"tool_call_id": chunk.ToolCallID,
			"phase":        "end",
		})
	}
}
