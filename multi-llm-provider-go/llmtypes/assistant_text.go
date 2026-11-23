package llmtypes

import "strings"

// This file holds the "message to the user" helpers for design-first UIs that
// stream/display a coding-agent turn without showing the terminal. There are
// three consumer modes:
//
//  1. Raw tmux        — show the terminal pane directly (StreamTerminalText).
//  2. Non-streaming   — full assistant MESSAGE = narration + final answer, with
//                       TOOLS REMOVED, narration separable (SplitAssistantText).
//  3. Streaming       — stream assistant TEXT live, TOOLS REMOVED
//                       (StreamAssistantText over Content chunks only).
//
// In modes 2 and 3 the tool calls are intentionally dropped — the user sees the
// assistant's words, not its tool activity.

// SplitAssistantText extracts the user-facing assistant message from a
// transcript message list, REMOVING tool calls and tool results. `narration` is
// the intermediate assistant text blocks (what it said while working); `final`
// is the last assistant text block (the concluding answer) — so a UI can
// highlight them separately. Non-streaming mode (2).
func SplitAssistantText(msgs []MessageContent) (narration []string, final string) {
	texts := assistantTextBlocks(msgs)
	if len(texts) == 0 {
		return nil, ""
	}
	return texts[:len(texts)-1], texts[len(texts)-1]
}

// FullAssistantText is narration + final joined — the complete message, tools
// removed. Non-streaming callers that don't need the narration/final split use
// this; it MUST equal the concatenation of a streaming consumer's text chunks.
func FullAssistantText(msgs []MessageContent) string {
	return strings.Join(assistantTextBlocks(msgs), "\n")
}

func assistantTextBlocks(msgs []MessageContent) []string {
	var texts []string
	for _, m := range msgs {
		if m.Role != ChatMessageTypeAI {
			continue
		}
		for _, p := range m.Parts {
			if tc, ok := p.(TextContent); ok {
				if s := strings.TrimSpace(tc.Text); s != "" {
					texts = append(texts, s)
				}
			}
			// ToolCall / ToolCallResponse are intentionally dropped — the user
			// sees the assistant's words, not its tool activity.
		}
	}
	return texts
}

// ContentDeltaMetadataKey marks a Content StreamChunk as a token-level DELTA
// (a fragment of an assistant message that may split mid-word) rather than a
// whole assistant text BLOCK. Providers that stream fine-grained deltas (pi's
// marker stream) MUST set Metadata[ContentDeltaMetadataKey]=true on those chunks;
// the transcript-tailing providers (claude/codex/cursor) emit block-level chunks
// and leave it unset. Without this, StreamAssistantText cannot tell a mid-word
// fragment ("B_cea33" | "2c7.") from a complete line and would "\n"-join it into
// garbled, token-split text.
const ContentDeltaMetadataKey = "content_delta"

// contentChunkIsDelta reports whether a Content chunk is a token-level delta.
func contentChunkIsDelta(c StreamChunk) bool {
	if c.Metadata == nil {
		return false
	}
	v, ok := c.Metadata[ContentDeltaMetadataKey].(bool)
	return ok && v
}

// StreamAssistantText joins the TEXT of a streamed turn, DROPPING tool and
// terminal chunks — the text-only view a streaming UI shows (mode 3). It is
// granularity-aware so the result equals FullAssistantText over the same turn
// regardless of how finely a provider chunks its output:
//
//   - BLOCK chunks (claude/codex/cursor: one chunk per assistant text block) are
//     each a line and are joined with "\n".
//   - DELTA chunks (pi's marker stream: token-level fragments that can split
//     mid-word) are concatenated VERBATIM — never "\n"-joined, which would split
//     tokens — and a contiguous run of deltas forms one block.
//
// A turn may mix both (deltas for live typing plus a block at message end); the
// blocks win and deltas between blocks are folded into the preceding/following
// block boundary. If a turn is deltas-only, they concatenate into a single block.
func StreamAssistantText(chunks []StreamChunk) string {
	var blocks []string
	var deltaBuf strings.Builder
	flushDeltas := func() {
		if deltaBuf.Len() == 0 {
			return
		}
		if s := strings.TrimSpace(deltaBuf.String()); s != "" {
			blocks = append(blocks, s)
		}
		deltaBuf.Reset()
	}
	for _, c := range chunks {
		if c.Type != StreamChunkTypeContent {
			continue
		}
		if contentChunkIsDelta(c) {
			deltaBuf.WriteString(c.Content) // verbatim — do NOT insert separators
			continue
		}
		// A block chunk ends any accumulating delta run.
		flushDeltas()
		if s := strings.TrimSpace(c.Content); s != "" {
			blocks = append(blocks, s)
		}
	}
	flushDeltas()
	return strings.Join(blocks, "\n")
}

// StreamTerminalText concatenates the raw terminal-snapshot chunks — the "show
// tmux directly" view (mode 1). Content/tool chunks are ignored here.
func StreamTerminalText(chunks []StreamChunk) string {
	var b strings.Builder
	for _, c := range chunks {
		if c.Type == StreamChunkTypeTerminal {
			b.WriteString(c.Content)
		}
	}
	return b.String()
}

// Native assistant framing is separate from text aggregation. Unknown/empty
// identities never opt a transport into block-level presentation.
const AssistantBlockIDMetadataKey = "assistant_block_id"
const AssistantBlockPhaseMetadataKey = "assistant_block_phase"
