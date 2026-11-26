// Package toolclock times structured-CLI tool calls.
//
// Every structured adapter emits a ToolCallStart and a later ToolCallEnd for
// the same call id, and llmtypes.StreamChunk has carried a ToolDuration field
// throughout — but no adapter ever set it. The whole chain downstream then
// reported zero: ToolCallEndEvent.Duration, ToolCallEntry.Duration, and the
// persisted timing summary's tools.total_duration_ms. A turn whose wall clock
// included real tool work looked purely generation-bound.
package toolclock

import "time"

// Elapsed returns how long the call has been running and forgets it, so a
// stream that never delivers an end event cannot grow the map without bound.
// An unknown id yields 0 rather than a fabricated duration: a missing start is
// "not measured", which must stay distinguishable from "instant".
func Elapsed(startedAt map[string]time.Time, callID string) time.Duration {
	if startedAt == nil || callID == "" {
		return 0
	}
	start, ok := startedAt[callID]
	if !ok || start.IsZero() {
		return 0
	}
	delete(startedAt, callID)
	return time.Since(start)
}

// Since is the variant for adapters that already carry the start time on their
// own pending-call record (claude-code keeps name/args there too), so they do
// not need a second map keyed by call id.
func Since(startedAt time.Time) time.Duration {
	if startedAt.IsZero() {
		return 0
	}
	return time.Since(startedAt)
}
