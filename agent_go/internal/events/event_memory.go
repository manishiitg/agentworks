package events

import (
	"os"
	"strconv"
	"strings"

	"github.com/manishiitg/mcpagent/events"
)

// Bounding what the event store keeps in memory (2026-10-16, the owner's laptop: a server up 12 hours held 3.6 GB while each
// session was already capped at 1,500 EVENTS, because the events are not equal: about 50-60 KB of heap per retained event, most
// of it terminal-screen snapshots). Three measures, all about memory only; polling and replay read the same events as before:
//
//  1. a snapshot that REPLACES the previous one (a terminal chunk with replace=true) supersedes it: only the latest per terminal
//     is kept (supersedeTerminalSnapshot), because every earlier one is dead weight by definition;
//  2. a per-session BYTE budget besides the count cap (pruneToByteBudget), so a session of heavy events cannot hold hundreds of MB;
//  3. the bytes each session holds are measured and shown in the memory log (Stats), so the next growth shows its owner.

// defaultSessionEventByteBudget is the most estimated payload one session keeps in memory (AGENTWORKS_EVENT_SESSION_MAX_MB overrides;
// 0 turns the byte cap off).
const defaultSessionEventByteBudget = 32 << 20

func sessionEventByteBudget() int64 {
	if raw := strings.TrimSpace(os.Getenv("AGENTWORKS_EVENT_SESSION_MAX_MB")); raw != "" {
		if mb, err := strconv.Atoi(raw); err == nil && mb >= 0 {
			return int64(mb) << 20
		}
	}
	return defaultSessionEventByteBudget
}

// estimateEventBytes is a cheap estimate of the heap an event holds: the text it carries plus a fixed overhead. The heavy
// payloads (terminal snapshots, tool results, messages) are measured; every other event counts as a small constant.
func estimateEventBytes(event Event) int64 {
	size := int64(512 + len(event.ID) + len(event.Type) + len(event.Error))
	if event.Data == nil {
		return size
	}
	switch d := event.Data.Data.(type) {
	case *events.StreamingChunkEvent:
		size += int64(len(d.Content))
		for key, value := range d.Metadata {
			size += int64(len(key) + 16)
			if s, ok := value.(string); ok {
				size += int64(len(s))
			}
		}
	case *events.ToolCallEndEvent:
		size += int64(len(d.Result))
	case *events.UserMessageEvent:
		size += int64(len(d.Content))
	case *events.LLMGenerationEndEvent:
		size += int64(len(d.Content))
	case *events.GenericEventData:
		size += 1024
	default:
		size += 1024
	}
	return size
}

// isReplaceTerminalSnapshot reports a terminal-pane snapshot that replaces the previous one of its terminal.
func isReplaceTerminalSnapshot(event Event) bool {
	if event.Type != "streaming_chunk" || event.TerminalID == "" || event.Data == nil {
		return false
	}
	chunk, ok := event.Data.Data.(*events.StreamingChunkEvent)
	if !ok || chunk.Source != events.StreamingChunkSourceTerminal {
		return false
	}
	replace, _ := chunk.Metadata["replace"].(bool)
	return replace
}

// supersedeTerminalSnapshot drops the earlier replace=true snapshots of the same terminal from the session's events and its
// terminal index, and returns the new event slice and how many were dropped. The caller holds es.mu.
func (es *EventStore) supersedeTerminalSnapshot(sessionID string, event Event) int {
	if !isReplaceTerminalSnapshot(event) {
		return 0
	}
	kept := es.events[sessionID][:0]
	dropped := 0
	for _, existing := range es.events[sessionID] {
		if existing.TerminalID == event.TerminalID && isReplaceTerminalSnapshot(existing) {
			dropped++
			continue
		}
		kept = append(kept, existing)
	}
	if dropped == 0 {
		return 0
	}
	// Clear the tail the compaction left behind so the dropped payloads can be collected.
	tail := es.events[sessionID][len(kept):]
	for i := range tail {
		tail[i] = Event{}
	}
	es.events[sessionID] = kept
	es.sessionStartIndices[sessionID] += dropped
	es.rebuildTerminalEventsLocked(sessionID)
	es.recomputeSessionBytesLocked(sessionID)
	return dropped
}

func (es *EventStore) recomputeSessionBytesLocked(sessionID string) {
	var total int64
	for _, event := range es.events[sessionID] {
		total += estimateEventBytes(event)
	}
	if es.sessionBytes == nil {
		es.sessionBytes = make(map[string]int64)
	}
	es.sessionBytes[sessionID] = total
}

// pruneToByteBudget drops the oldest noisy events (then the oldest of anything) until the session fits its byte budget, and
// returns how many it dropped. The newest event is never dropped. The caller holds es.mu.
func (es *EventStore) pruneToByteBudget(sessionID string, budget int64) int {
	if budget <= 0 || es.sessionBytes[sessionID] <= budget {
		return 0
	}
	list := es.events[sessionID]
	drop := make([]bool, len(list))
	total := es.sessionBytes[sessionID]
	dropped := 0
	pass := func(onlyNoisy bool) {
		for i := 0; i < len(list)-1 && total > budget; i++ {
			if drop[i] {
				continue
			}
			if onlyNoisy && (STRUCTURAL_EVENTS[list[i].Type] || IsTranscriptMessage(list[i])) {
				continue
			}
			drop[i] = true
			total -= estimateEventBytes(list[i])
			dropped++
		}
	}
	pass(true)
	pass(false)
	if dropped == 0 {
		return 0
	}
	pruned := make([]Event, 0, len(list)-dropped)
	for i, event := range list {
		if !drop[i] {
			pruned = append(pruned, event)
		}
	}
	es.events[sessionID] = pruned
	es.sessionStartIndices[sessionID] += dropped
	es.rebuildTerminalEventsLocked(sessionID)
	es.recomputeSessionBytesLocked(sessionID)
	return dropped
}
