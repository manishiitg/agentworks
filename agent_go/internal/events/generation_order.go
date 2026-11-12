package events

import agentevents "github.com/manishiitg/mcpagent/events"

// Native text starts use the same server-owned first-position contract as a
// whole generation. Without native identity the generation behavior is unchanged.
type generationPosition struct {
	id          string
	sequence    int64
	executionID string
	blockID     string
	complete    bool
	hasText     bool
	unframed    bool
	blocks      map[string]generationPosition
}

// stampGenerationPosition runs under mu, after sequence and owner assignment.
func (es *EventStore) stampGenerationPosition(sessionID string, event *Event) {
	event.GenerationID = ""
	event.GenerationStartSequence = 0
	event.ReplyBlocksComplete = false
	if event.TerminalOwnerID != "main:"+sessionID {
		return
	}
	var metadata map[string]interface{}
	var content, source string
	if event.Data != nil {
		switch data := event.Data.Data.(type) {
		case *agentevents.StreamingStartEvent:
			metadata = data.Metadata
		case *agentevents.StreamingChunkEvent:
			metadata, content, source = data.Metadata, data.Content, data.Source
		}
	}
	blockID, _ := metadata["assistant_block_id"].(string)
	phase, _ := metadata["assistant_block_phase"].(string)
	if event.Type == "streaming_start" {
		if es.generationPositions == nil {
			es.generationPositions = make(map[string]generationPosition)
		}
		position := generationPosition{id: event.ID, sequence: event.Sequence, executionID: event.ExecutionID}
		if blockID != "" && phase == "start" {
			previous := es.generationPositions[sessionID]
			if previous.executionID == event.ExecutionID {
				position.blocks, position.unframed = previous.blocks, previous.unframed
			}
			if position.blocks == nil {
				position.blocks = make(map[string]generationPosition)
			}
			position.blockID = blockID
			position.blocks[blockID] = generationPosition{id: event.ID, sequence: event.Sequence, executionID: event.ExecutionID, blockID: blockID}
		}
		es.generationPositions[sessionID] = position
	}
	position, ok := es.generationPositions[sessionID]
	if !ok || position.executionID != event.ExecutionID {
		return
	}
	carrier := position
	if event.Type == "streaming_chunk" {
		if blockID != "" {
			var known bool
			carrier, known = position.blocks[blockID]
			if !known || carrier.executionID != event.ExecutionID {
				return
			}
			if phase == "end" {
				carrier.complete = true
			}
			if content != "" {
				carrier.hasText = true
			}
			position.blocks[blockID] = carrier
		} else if source == "content" || source == "transcript" {
			if content != "" {
				position.unframed = true
			}
		}
		es.generationPositions[sessionID] = position
	}
	switch event.Type {
	case "agent_end", "unified_completion":
		complete, text := len(position.blocks) > 0 && !position.unframed, false
		for _, block := range position.blocks {
			complete = complete && block.complete
			text = text || block.hasText
		}
		event.ReplyBlocksComplete = complete && text
		fallthrough
	case "streaming_start", "streaming_chunk", "streaming_end", "llm_generation_end":
		event.GenerationID = carrier.id
		event.GenerationStartSequence = carrier.sequence
	case "conversation_end", "conversation_error":
		delete(es.generationPositions, sessionID)
	}
}
