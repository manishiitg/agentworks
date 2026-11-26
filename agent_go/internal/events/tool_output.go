package events

// Live output of a command a coding agent runs (#3131). Each chunk is one
// live-only "tool_call_output" event carrying the command's current output
// tail; a newer one replaces the older ones of the same command in memory, so a
// chatty command (a dev server) costs one bounded tail, never its whole log.
// When the command ends and its card holds no other result (Claude's own
// background commands), its last tail is stored once with "final": true.
const ToolCallOutputEventType = "tool_call_output"

func toolOutputFields(event *Event) map[string]interface{} {
	if event == nil || event.Type != ToolCallOutputEventType || event.Data == nil {
		return nil
	}
	if generic, ok := event.Data.Data.(*GenericEventData); ok && generic != nil {
		return generic.Fields
	}
	return eventPayloadMap(event)
}

// liveToolOutputCallID is the command of a live (not final) output event.
func liveToolOutputCallID(event *Event) string {
	fields := toolOutputFields(event)
	if fields == nil {
		return ""
	}
	if final, _ := fields["final"].(bool); final {
		return ""
	}
	id, _ := fields["tool_call_id"].(string)
	return id
}

func isFinalToolOutput(event Event) bool {
	fields := toolOutputFields(&event)
	final, _ := fields["final"].(bool)
	return final
}

// supersedeToolOutput drops the earlier live output events of the same command
// from the session's events. The caller holds es.mu.
func (es *EventStore) supersedeToolOutput(sessionID string, event Event) int {
	id := liveToolOutputCallID(&event)
	if id == "" {
		return 0
	}
	kept := es.events[sessionID][:0]
	dropped := 0
	for _, existing := range es.events[sessionID] {
		if existing.Type == ToolCallOutputEventType && liveToolOutputCallID(&existing) == id {
			dropped++
			continue
		}
		kept = append(kept, existing)
	}
	if dropped == 0 {
		return 0
	}
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
