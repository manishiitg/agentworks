package events

// generationPosition uses the actual server start event, not a native message
// boundary. One generation can continue responding to a steer in the same row.
// It deliberately does not change delivery, receipt or turn attribution.
type generationPosition struct {
	id          string
	sequence    int64
	executionID string
	turnID      string
}

// stampGenerationPosition runs under mu, after sequence and owner assignment.
func (es *EventStore) stampGenerationPosition(sessionID string, event *Event) {
	event.GenerationID = ""
	event.GenerationStartSequence = 0
	// Main transcript only: child terminals cannot acquire another chat's row.
	if event.TerminalOwnerID != "main:"+sessionID {
		return
	}
	if event.Type == "streaming_start" {
		if es.generationPositions == nil {
			es.generationPositions = make(map[string]generationPosition)
		}
		turnID := ""
		if event.Data != nil {
			turnID = event.Data.TurnID
		}
		es.generationPositions[sessionID] = generationPosition{event.ID, event.Sequence, event.ExecutionID, turnID}
	}
	position, ok := es.generationPositions[sessionID]
	if !ok {
		return
	}
	if position.executionID != event.ExecutionID {
		return
	}
	if position.turnID != "" && event.Data != nil && event.Data.TurnID != "" && event.Data.TurnID != position.turnID {
		return
	}
	switch event.Type {
	case "streaming_start", "streaming_chunk", "streaming_end", "llm_generation_end", "agent_end", "unified_completion":
		event.GenerationID = position.id
		event.GenerationStartSequence = position.sequence
	case "conversation_end", "conversation_error":
		delete(es.generationPositions, sessionID)
	}
}
