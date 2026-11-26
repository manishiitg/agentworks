package events

import (
	agentevents "github.com/manishiitg/mcpagent/events"
	"strings"
)

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
	remainder   *unframedReply
	blocks      map[string]generationPosition
}

// Only a mixed stream needs this projection at completion. Exact deltas are
// joined verbatim; an explicit whole-message snapshot replaces its pending
// delta copy, never by comparing text. Actual model/history content is intact.
type unframedReply struct {
	complete strings.Builder
	pending  strings.Builder
}

func (r *unframedReply) flush() {
	if r.pending.Len() == 0 {
		return
	}
	r.block(r.pending.String())
	r.pending.Reset()
}
func (r *unframedReply) block(text string) {
	if r.complete.Len() > 0 {
		r.complete.WriteString("\n\n")
	}
	r.complete.WriteString(text)
}
func (r *unframedReply) append(text string, delta bool) {
	if delta {
		r.pending.WriteString(text)
		return
	}
	r.pending.Reset()
	r.block(text)
}

// stampGenerationPosition runs under mu, after sequence and owner assignment.
func (es *EventStore) stampGenerationPosition(sessionID string, event *Event) {
	event.GenerationID = ""
	event.GenerationStartSequence = 0
	event.ReplyBlocksComplete = false
	event.ReplyRemainder = nil
	if event.TerminalOwnerID != "main:"+sessionID {
		return
	}
	var metadata map[string]interface{}
	var content, source string
	var delta bool
	if event.Data != nil {
		switch data := event.Data.Data.(type) {
		case *agentevents.StreamingStartEvent:
			metadata = data.Metadata
		case *agentevents.StreamingChunkEvent:
			metadata, content, source, delta = data.Metadata, data.Content, data.Source, data.IsDelta
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
				position.blocks, position.unframed, position.remainder = previous.blocks, previous.unframed, previous.remainder
				if position.remainder != nil {
					position.remainder.flush()
				}
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
				if position.remainder == nil {
					position.remainder = &unframedReply{}
				}
				position.remainder.append(content, delta)
			}
		}
		es.generationPositions[sessionID] = position
	}
	switch event.Type {
	case "llm_generation_end", "agent_end", "unified_completion":
		complete, text := len(position.blocks) > 0, false
		for _, block := range position.blocks {
			complete = complete && block.complete
			text = text || block.hasText
		}
		event.ReplyBlocksComplete = complete && text && !position.unframed
		if complete && text && position.unframed && position.remainder != nil {
			position.remainder.flush()
			remainder := position.remainder.complete.String()
			event.ReplyRemainder = &remainder
		}
		fallthrough
	case "streaming_start", "streaming_chunk", "streaming_end":
		event.GenerationID = carrier.id
		event.GenerationStartSequence = carrier.sequence
	case "conversation_end", "conversation_error":
		delete(es.generationPositions, sessionID)
	}
}
