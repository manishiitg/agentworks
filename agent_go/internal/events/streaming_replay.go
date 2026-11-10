package events

import (
	"log"

	"github.com/manishiitg/mcpagent/events"
)

// StreamingPrefix returns the current main-chat token stream from its start,
// and the transport packet IDs already covered by this snapshot. Durable SSE
// restores journal rows, not token deltas (PLAT-746-838). Reuse the existing
// bounded working set rather than retaining another copy of every reply.
// The caller subscribes first and reads this after durable backfill; covered
// packets queued during backfill must not append their text a second time.
func (es *EventStore) StreamingPrefix(sessionID string) (prefix []Event, covered map[string]bool) {
	es.mu.RLock()
	defer es.mu.RUnlock()
	covered = make(map[string]bool)
	start := -1
	source := es.events[sessionID]
	for i, event := range source {
		if !mainStreamingReplayEvent(sessionID, event) {
			continue
		}
		switch event.Type {
		case "streaming_start":
			covered[event.ID] = true
			start = i
		case "streaming_chunk":
			// Whole transcript messages are journal rows, never transient replay.
			if !IsTranscriptMessage(event) {
				covered[event.ID] = true
			}
		case "streaming_end", "unified_completion", "conversation_end", "conversation_error":
			if event.Type == "streaming_end" {
				covered[event.ID] = true
			}
			start = -1
		}
	}
	if start < 0 {
		return nil, nil
	}
	prefix = append(prefix, source[start])
	expected := 1
	for _, event := range source[start+1:] {
		if event.Type != "streaming_chunk" || !mainStreamingReplayEvent(sessionID, event) {
			continue
		}
		content, index, origin, delta := streamingReplayChunk(event)
		if content == "" {
			continue
		}
		// Other transports already restore whole messages / terminal frames.
		// This replay is for contiguous prose deltas, not screen snapshots.
		if origin != "content" || !delta {
			return nil, nil
		}
		if index != expected {
			log.Printf("[CHAT_STREAM_REPLAY] session=%s result=incomplete expected=%d got=%d", sessionID, expected, index)
			return nil, nil
		}
		expected++
		prefix = append(prefix, event)
	}
	if len(prefix) == 1 {
		return nil, nil
	}
	return prefix, covered
}

func StreamingPrefixBytes(prefix []Event) int {
	total := 0
	for _, event := range prefix {
		if event.Type != "streaming_chunk" {
			continue
		}
		content, _, _, _ := streamingReplayChunk(event)
		total += len(content)
	}
	return total
}

func mainStreamingReplayEvent(sessionID string, event Event) bool {
	if event.TerminalOwnerID != "" {
		return event.TerminalOwnerID == "main:"+sessionID
	}
	return event.TerminalID == TerminalIDForOwner(sessionID, "main:"+sessionID)
}

func streamingReplayChunk(event Event) (content string, index int, source string, delta bool) {
	if event.Data != nil {
		if chunk, ok := event.Data.Data.(*events.StreamingChunkEvent); ok && chunk != nil {
			return chunk.Content, chunk.ChunkIndex, chunk.Source, chunk.IsDelta
		}
	}
	payload := eventPayloadMap(&event)
	content, _ = payload["content"].(string)
	source, _ = payload["source"].(string)
	delta, _ = payload["is_delta"].(bool)
	switch n := payload["chunk_index"].(type) {
	case int:
		index = n
	case float64:
		index = int(n)
	}
	return
}
