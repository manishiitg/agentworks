package llmtypes

import (
	"reflect"
	"strings"
	"testing"
)

// A realistic turn: narrate -> tool -> (result) -> narrate -> tool -> final,
// expressed once as transcript messages (non-streaming source) and once as the
// stream chunks the tailer emits for the SAME turn (with terminal snapshots
// mixed in). The two must agree on the user-facing message.
func sampleTurnMessages() []MessageContent {
	return []MessageContent{
		{Role: ChatMessageTypeAI, Parts: []ContentPart{TextContent{Text: "I'll search for the code word."}}},
		{Role: ChatMessageTypeAI, Parts: []ContentPart{ToolCall{ID: "c1", Type: "function", FunctionCall: &FunctionCall{Name: "web_search"}}}},
		{Role: ChatMessageTypeTool, Parts: []ContentPart{ToolCallResponse{ToolCallID: "c1", Content: "the code word is ZEBRA_123"}}},
		{Role: ChatMessageTypeAI, Parts: []ContentPart{TextContent{Text: "Now I'll save it to result.txt."}}},
		{Role: ChatMessageTypeAI, Parts: []ContentPart{ToolCall{ID: "c2", Type: "function", FunctionCall: &FunctionCall{Name: "write_file"}}}},
		{Role: ChatMessageTypeAI, Parts: []ContentPart{TextContent{Text: "ZEBRA_123"}}},
	}
}

func sampleTurnChunks() []StreamChunk {
	return []StreamChunk{
		{Type: StreamChunkTypeTerminal, Content: "\x1b[2J[raw pane frame 1]"},
		{Type: StreamChunkTypeContent, Content: "I'll search for the code word."},
		{Type: StreamChunkTypeToolCallStart, ToolName: "web_search", ToolCallID: "c1"},
		{Type: StreamChunkTypeTerminal, Content: "[raw pane frame 2]"},
		{Type: StreamChunkTypeContent, Content: "Now I'll save it to result.txt."},
		{Type: StreamChunkTypeToolCallStart, ToolName: "write_file", ToolCallID: "c2"},
		{Type: StreamChunkTypeContent, Content: "ZEBRA_123"},
	}
}

// Mode 1: show tmux directly to the user — the raw terminal frames are available
// and separable from the assistant text.
func TestUserMessageMode1_RawTmux(t *testing.T) {
	raw := StreamTerminalText(sampleTurnChunks())
	if !strings.Contains(raw, "[raw pane frame 1]") || !strings.Contains(raw, "[raw pane frame 2]") {
		t.Fatalf("raw tmux view missing pane frames: %q", raw)
	}
	// It must be the terminal frames, not the assistant text.
	if strings.Contains(raw, "I'll search") || strings.Contains(raw, "ZEBRA_123") {
		t.Fatalf("raw tmux view leaked assistant text: %q", raw)
	}
}

// Mode 2: non-streaming full message = narration + final answer, TOOLS REMOVED,
// narration separable so a UI can highlight it apart from the final answer.
func TestUserMessageMode2_NonStreamingNarrationFinalNoTools(t *testing.T) {
	narration, final := SplitAssistantText(sampleTurnMessages())

	wantNarration := []string{"I'll search for the code word.", "Now I'll save it to result.txt."}
	if !reflect.DeepEqual(narration, wantNarration) {
		t.Fatalf("narration = %q, want %q", narration, wantNarration)
	}
	if final != "ZEBRA_123" {
		t.Fatalf("final = %q, want %q", final, "ZEBRA_123")
	}
	// Tools removed: no tool name and no tool-result text anywhere in the message.
	full := FullAssistantText(sampleTurnMessages())
	for _, banned := range []string{"web_search", "write_file", "the code word is ZEBRA_123", "tool_use", "tool_call"} {
		if strings.Contains(full, banned) {
			t.Fatalf("tools not removed from message: found %q in %q", banned, full)
		}
	}
}

// Mode 3: streaming to the user — the text-only view (tool chunks dropped) is
// the assistant message, and it MUST equal the non-streaming full message
// (mode 2) so both delivery modes show the user the same thing.
func TestUserMessageMode3_StreamingNoTools(t *testing.T) {
	streamed := StreamAssistantText(sampleTurnChunks())

	// No tool activity leaked into the streamed text.
	for _, banned := range []string{"web_search", "write_file", "raw pane frame"} {
		if strings.Contains(streamed, banned) {
			t.Fatalf("streamed text leaked non-text content: found %q in %q", banned, streamed)
		}
	}
	// Consistency: streaming (mode 3) == non-streaming full message (mode 2).
	full := FullAssistantText(sampleTurnMessages())
	if streamed != full {
		t.Fatalf("streaming and non-streaming messages differ:\n stream=%q\n   full=%q", streamed, full)
	}
	if streamed != "I'll search for the code word.\nNow I'll save it to result.txt.\nZEBRA_123" {
		t.Fatalf("unexpected streamed message: %q", streamed)
	}
}

// deltaChunk builds a Content chunk marked as a token-level delta, the way pi's
// marker stream emits fragments that can split mid-word.
func deltaChunk(text string) StreamChunk {
	return StreamChunk{Type: StreamChunkTypeContent, Content: text, Metadata: map[string]interface{}{ContentDeltaMetadataKey: true}}
}

// TestStreamAssistantText_PiDeltasNotGarbled is the regression for the real bug
// the agentic review caught: pi streams token-level deltas that split MID-TOKEN
// ("PI_STREAM_A_4a0..." arrives as "...cea332" + "c7"). The old "\n"-join
// inserted a newline INSIDE the token. Delta chunks must concatenate verbatim so
// the token survives intact.
func TestStreamAssistantText_PiDeltasNotGarbled(t *testing.T) {
	chunks := []StreamChunk{
		{Type: StreamChunkTypeTerminal, Content: "[pane]"},
		deltaChunk("Calling echo_contract with the first token."),
		{Type: StreamChunkTypeToolCallStart, ToolName: "mcp", ToolCallID: "t1"},
		deltaChunk("The result is PI_STREAM_B_cea332"),
		deltaChunk("c7"), // splits the token across two deltas
	}
	got := StreamAssistantText(chunks)
	// The token must be intact — no newline injected mid-token.
	if !strings.Contains(got, "PI_STREAM_B_cea332c7") {
		t.Fatalf("delta token was garbled by reassembly: %q", got)
	}
	if strings.Contains(got, "cea332\nc7") {
		t.Fatalf("reassembly inserted a newline mid-token: %q", got)
	}
	// Tool + terminal chunks are still stripped.
	for _, banned := range []string{"mcp", "[pane]"} {
		if strings.Contains(got, banned) {
			t.Fatalf("non-text leaked: %q in %q", banned, got)
		}
	}
}

// TestStreamAssistantText_UnicodeAndMarkdownPreserved covers real assistant
// output: emoji, accents, code fences, and tables must pass through byte-for-byte
// (neither trimmed internally nor mangled).
func TestStreamAssistantText_UnicodeAndMarkdownPreserved(t *testing.T) {
	block := "Here's the fix ✅ (café update):\n```go\nfunc F() {}\n```\n| a | b |\n|---|---|"
	got := StreamAssistantText([]StreamChunk{{Type: StreamChunkTypeContent, Content: block}})
	if got != block {
		t.Fatalf("unicode/markdown block not preserved:\n got=%q\nwant=%q", got, block)
	}
}

// TestStreamAssistantText_MixedDeltaAndBlock proves a turn that mixes a live
// delta run with a final block chunk reassembles cleanly: the deltas fold into
// one block, and the trailing block is its own line.
func TestStreamAssistantText_MixedDeltaAndBlock(t *testing.T) {
	chunks := []StreamChunk{
		deltaChunk("Saving "),
		deltaChunk("the file now."),
		{Type: StreamChunkTypeToolCallStart, ToolName: "write_file", ToolCallID: "t1"},
		{Type: StreamChunkTypeContent, Content: "Done: ZEBRA_9"}, // block chunk (no delta flag)
	}
	got := StreamAssistantText(chunks)
	want := "Saving the file now.\nDone: ZEBRA_9"
	if got != want {
		t.Fatalf("mixed delta+block reassembly:\n got=%q\nwant=%q", got, want)
	}
}

// TestStreamAssistantText_BlockChunksUnchanged guards that the granularity-aware
// path did not regress the block-level providers (claude/codex/cursor): unmarked
// content chunks are still one-line-each, "\n"-joined.
func TestStreamAssistantText_BlockChunksUnchanged(t *testing.T) {
	chunks := []StreamChunk{
		{Type: StreamChunkTypeContent, Content: "First sentence."},
		{Type: StreamChunkTypeToolCallStart, ToolName: "x", ToolCallID: "t1"},
		{Type: StreamChunkTypeContent, Content: "Second sentence."},
	}
	if got := StreamAssistantText(chunks); got != "First sentence.\nSecond sentence." {
		t.Fatalf("block reassembly regressed: %q", got)
	}
}
