package claudecode

import (
	"strconv"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Claude streams token deltas within messages, but starts a new message after
// tools. Preserve that explicit boundary in both live text and the saved turn;
// punctuation cannot distinguish a message boundary from a split token.
type claudeStructuredText struct {
	nativeRetry *llmtypes.NativeRetry
	partial     bool
	emitted     bool
	completed   strings.Builder
	messageID   string
	blocks      map[string]*claudeTextBlock
}

type claudeTextBlock struct {
	text     strings.Builder
	complete bool
}

func (t *claudeStructuredText) blockID(message string, index int) string {
	if message == "" {
		return ""
	}
	return "claude:" + message + ":" + strconv.Itoa(index)
}

func (t *claudeStructuredText) startBlock(id string, emit func(llmtypes.StreamChunk)) {
	if id == "" {
		return
	}
	if t.blocks == nil {
		t.blocks = make(map[string]*claudeTextBlock)
	}
	if t.blocks[id] != nil {
		return
	}
	t.blocks[id] = &claudeTextBlock{}
	emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeAssistantBlock, Metadata: map[string]any{llmtypes.AssistantBlockIDMetadataKey: id, llmtypes.AssistantBlockPhaseMetadataKey: "start"}})
}

func (t *claudeStructuredText) endBlock(id, text string, emit func(llmtypes.StreamChunk)) {
	if id == "" {
		return
	}
	t.startBlock(id, emit)
	block := t.blocks[id]
	if block.complete {
		return
	}
	block.complete = true
	emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeAssistantBlock, Content: text, Metadata: map[string]any{llmtypes.AssistantBlockIDMetadataKey: id, llmtypes.AssistantBlockPhaseMetadataKey: "end"}})
}

func (t *claudeStructuredText) observe(ev claudeStreamEvent, emit func(llmtypes.StreamChunk)) {
	if ev.ParentToolUseID == "" {
		if ev.Type == "system" && ev.Subtype == "api_retry" && ev.Attempt > 0 {
			t.endNativeRetry(false, emit)
			t.nativeRetry = &llmtypes.NativeRetry{Provider: "claude-code", NativeSessionID: ev.SessionID, Phase: "start", Attempt: ev.Attempt, MaxAttempts: ev.MaxRetries, DelayMS: ev.RetryDelayMS}
			emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeNativeRetry, NativeRetry: t.nativeRetry})
		} else if ev.Type == "assistant" || ev.Type == "result" || ev.Type == "stream_event" {
			t.endNativeRetry(!ev.IsError, emit)
		}
	}

	switch ev.Type {
	case "stream_event":
		if ev.Event == nil {
			return
		}
		if ev.Event.Type == "message_start" {
			t.partial = false
			t.messageID = ""
			if ev.Event.Message != nil {
				t.messageID = ev.Event.Message.ID
			}
		}
		id := t.blockID(t.messageID, ev.Event.Index)
		if ev.Event.Type == "content_block_start" && ev.Event.ContentBlock != nil && ev.Event.ContentBlock.Type == "text" {
			t.startBlock(id, emit)
		}
		if ev.Event.Type == "content_block_stop" && t.blocks[id] != nil {
			t.endBlock(id, t.blocks[id].text.String(), emit)
		}
		if ev.Event.Type != "content_block_delta" || ev.Event.Delta.Type != "text_delta" || ev.Event.Delta.Text == "" {
			return
		}
		text := ev.Event.Delta.Text
		t.startBlock(id, emit)
		if t.blocks[id] != nil {
			t.blocks[id].text.WriteString(text)
		}
		if !t.partial && t.emitted {
			text = "\n\n" + text
		}
		t.partial, t.emitted = true, true
		emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: text, Metadata: map[string]any{llmtypes.ContentDeltaMetadataKey: true, llmtypes.AssistantBlockIDMetadataKey: id}})
	case "assistant":
		if ev.Message == nil {
			return
		}
		for index, block := range ev.Message.Content {
			// PLAT-746-839: the SDK supplies completed thinking blocks separately
			// from text. Forward them on the existing reasoning channel, never
			// into the reply. Partial thinking deltas remain ignored here so the
			// completed block is emitted once, not repeated after its fragments.
			if block.Type == "thinking" && strings.TrimSpace(block.Thinking) != "" {
				emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeReasoning, Content: block.Thinking})
				continue
			}
			if block.Type != "text" || block.Text == "" {
				continue
			}
			id := t.blockID(ev.Message.ID, index)
			if !t.partial {
				t.startBlock(id, emit)
			}
			if t.completed.Len() > 0 {
				t.completed.WriteString("\n\n")
			}
			t.completed.WriteString(block.Text)
			// The completed message repeats the partial stream. Without partial
			// events (structured transport), it is a complete block instead.
			if !t.partial {
				text := block.Text
				if t.emitted {
					text = "\n\n" + text
				}
				t.emitted = true
				emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: text, Metadata: map[string]any{llmtypes.AssistantBlockIDMetadataKey: id}})
			}
			if !t.partial {
				t.endBlock(id, block.Text, emit)
			}
		}
	}
}

func (t *claudeStructuredText) endNativeRetry(success bool, emit func(llmtypes.StreamChunk)) {
	if t.nativeRetry == nil {
		return
	}
	end := *t.nativeRetry
	end.Phase = "end"
	end.Success = success
	t.nativeRetry = nil
	emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeNativeRetry, NativeRetry: &end})
}
