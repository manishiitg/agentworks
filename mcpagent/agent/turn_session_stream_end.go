package mcpagent

import (
	"context"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
)

// emitRetainedStreamEnd ends the stream of a turn typed into the running terminal. A normal turn ends its stream when
// the model call returns; a retained turn streamed its progress as chunks but only emitted a completion, so the stream
// of that turn was never ended: the event store logged no "[STREAM] ... turn ended", its "still_running" heartbeat kept
// firing for hours, and a client that leaves its streaming state on the stream's end stayed in it, so a typed message
// waited behind a turn that was over.
//
// It is emitted after the completion, stamped with the instant just before it, so a handler that settles a retained
// turn on the first terminal event never takes it for the end of a later turn that the completion promoted.
func (s *Session) emitRetainedStreamEnd(lifecycle *canonicalTurnLifecycle, provider llm.Provider, startedAt, endedAt time.Time) {
	end := &events.StreamingEndEvent{
		BaseEventData: events.BaseEventData{Timestamp: endedAt},
		Duration:      endedAt.Sub(startedAt).String(),
		FinishReason:  "retained_completion",
	}
	end.Metadata = map[string]interface{}{
		"kind":        "terminal",
		"provider":    string(provider),
		"duration_ms": endedAt.Sub(startedAt).Milliseconds(),
		"source":      "mcpagent_session",
		"retained":    true,
	}
	s.agent.emitTypedEvent(withCanonicalTurnLifecycle(context.Background(), lifecycle), end)
}
