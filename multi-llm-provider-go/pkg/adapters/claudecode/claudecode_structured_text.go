package claudecode

import (
	"regexp"
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
	// Thinking that may be a progress note (see claudeWritesUpdatesAsThinking).
	lastThinkingMessage string
	pendingThinking     *claudePendingThinking
	// The latest root message's text, and the answers Stop hooks continued past.
	lastTextMessage string
	lastText        strings.Builder
	stopHooks       []claudeStopHook
}

type claudePendingThinking struct {
	messageID string
	index     int
	text      string
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
	// Held thinking ends with its message: a new message, a tool result or the turn's result.
	if ev.Type == "result" || ev.Type == "user" || (ev.Type == "stream_event" && ev.Event != nil && ev.Event.Type == "message_start") {
		t.flushPendingThinking(emit)
	}
	if reason, ok := claudeStopHookFeedback(ev); ok {
		t.stopHooks = append(t.stopHooks, claudeStopHook{answer: strings.TrimSpace(t.lastText.String()), reason: reason})
		t.lastText.Reset()
		t.lastTextMessage = ""
	}
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
		messageID := ev.Message.ID
		for index, block := range ev.Message.Content {
			// PLAT-746-839: the SDK supplies completed thinking blocks separately
			// from text. Forward them on the existing reasoning channel, never
			// into the reply. Partial thinking deltas remain ignored here so the
			// completed block is emitted once, not repeated after its fragments.
			if block.Type == "thinking" {
				t.flushPendingThinking(emit)
				follows := messageID != "" && t.lastThinkingMessage == messageID
				t.lastThinkingMessage = messageID
				if strings.TrimSpace(block.Thinking) == "" {
					continue
				}
				// A second thinking block may be a note to the person; it waits to see what follows.
				if follows && claudeWritesUpdatesAsThinking(ev.Message.Model) {
					t.pendingThinking = &claudePendingThinking{messageID: messageID, index: index, text: block.Thinking}
					continue
				}
				emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeReasoning, Content: block.Thinking})
				continue
			}
			t.lastThinkingMessage = ""
			if pending := t.pendingThinking; pending != nil {
				t.pendingThinking = nil
				if block.Type == "tool_use" && pending.messageID == messageID {
					t.promoteThinking(pending, emit)
				} else {
					emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeReasoning, Content: pending.text})
				}
			}
			if block.Type != "text" || block.Text == "" {
				continue
			}
			t.noteText(messageID, block.Text)
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

// Claude 5.1 and later write the note before a tool call (a reply to a steer, a
// progress line) as a second thinking block (T3 Code #41085). Claude 5.0 and
// older models keep it as reasoning.
var claudeModelVersion = regexp.MustCompile(`claude-[a-z]+-(\d+)(?:-(\d{1,2}))?(?:\D|$)`)

func claudeWritesUpdatesAsThinking(model string) bool {
	m := claudeModelVersion.FindStringSubmatch(model)
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > 5 || (major == 5 && minor >= 1)
}

// flushPendingThinking shows held thinking as reasoning: no tool call followed it.
func (t *claudeStructuredText) flushPendingThinking(emit func(llmtypes.StreamChunk)) {
	if pending := t.pendingThinking; pending != nil {
		t.pendingThinking = nil
		emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeReasoning, Content: pending.text})
	}
}

// promoteThinking shows a note Claude wrote as thinking right before a tool call as reply text.
func (t *claudeStructuredText) promoteThinking(pending *claudePendingThinking, emit func(llmtypes.StreamChunk)) {
	text := strings.TrimSpace(pending.text)
	id := t.blockID(pending.messageID, pending.index)
	t.startBlock(id, emit)
	if t.completed.Len() > 0 {
		t.completed.WriteString("\n\n")
	}
	t.completed.WriteString(text)
	shown := text
	if t.emitted {
		shown = "\n\n" + shown
	}
	t.emitted = true
	emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: shown, Metadata: map[string]any{llmtypes.AssistantBlockIDMetadataKey: id}})
	t.endBlock(id, text, emit)
}

func (t *claudeStructuredText) noteText(messageID, text string) {
	if messageID != t.lastTextMessage {
		t.lastText.Reset()
		t.lastTextMessage = messageID
	}
	if t.lastText.Len() > 0 {
		t.lastText.WriteString("\n\n")
	}
	t.lastText.WriteString(text)
}

type claudeStopHook struct {
	answer string
	reason string
}

// takeStopHooks returns the Stop hooks that continued the turn since the last result.
func (t *claudeStructuredText) takeStopHooks() []claudeStopHook {
	hooks := t.stopHooks
	t.stopHooks = nil
	return hooks
}

// A Claude /goal check is a Stop hook too; its reason starts "[objective]: ".
var claudeGoalCheckFeedback = regexp.MustCompile(`^\[[^\]\n]+\]: `)

// claudeStopHookFeedback returns what a blocking Stop hook told Claude when it
// made the root turn carry on.
func claudeStopHookFeedback(ev claudeStreamEvent) (string, bool) {
	if ev.Type != "user" || !ev.IsSynthetic || ev.ParentToolUseID != "" || ev.Message == nil {
		return "", false
	}
	var text strings.Builder
	for _, block := range ev.Message.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	const prefix = "Stop hook feedback:\n"
	reason, ok := strings.CutPrefix(text.String(), prefix)
	if !ok || claudeGoalCheckFeedback.MatchString(reason) {
		return "", false
	}
	return reason, true
}

// claudeStopHookResult keeps each answer a Stop hook continued past, with a
// short note of what the hook asked, in front of the final reply.
func claudeStopHookResult(hooks []claudeStopHook, reply string) string {
	var parts []string
	for _, hook := range hooks {
		if hook.answer != "" {
			parts = append(parts, hook.answer)
		}
		reason, _, _ := strings.Cut(strings.TrimSpace(hook.reason), "\n")
		if r := []rune(reason); len(r) > 200 {
			reason = string(r[:200]) + "…"
		}
		note := "_A Stop hook asked Claude to continue._"
		if reason != "" {
			note = "_A Stop hook asked Claude to continue: " + reason + "_"
		}
		parts = append(parts, note)
	}
	if reply = strings.TrimSpace(reply); reply != "" {
		parts = append(parts, reply)
	}
	return strings.Join(parts, "\n\n")
}
