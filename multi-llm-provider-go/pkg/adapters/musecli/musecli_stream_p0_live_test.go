package musecli

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestMuseCLIRealTmuxTranscriptStreamP0 certifies structured_streaming and
// stream_no_history_replay for the production tmux lane (PLAT-354): chat text
// and tool activity stream from session.jsonl, not from the pane, on one
// retained session across turns.
//
//   - Turn 1 plants a unique token in the session's history, unstreamed.
//   - Turn 2 is answered almost immediately (an "early commit"): its text must
//     still reach the stream, although the tailer starts after submit, and
//     turn 1's token must never be replayed.
//   - Turn 3 runs one native tool: exactly one start and one matching end per
//     call ID, and still no replay of either earlier turn.
func TestMuseCLIRealTmuxTranscriptStreamP0(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	owner := "stream-p0-" + museRandomHex(t, 3)
	t.Cleanup(func() { KillMusePersistentSession(owner) })
	workdir := t.TempDir()
	base := []llmtypes.CallOption{WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner), WithWorkingDir(workdir), llmtypes.WithReasoningEffort("low")}
	adapter := museLiveAdapter()
	turn := func(prompt string, stream chan llmtypes.StreamChunk) string {
		t.Helper()
		opts := append([]llmtypes.CallOption{}, base...)
		if stream != nil {
			opts = append(opts, llmtypes.WithStreamingChan(stream), WithStreamTranscript(true), WithStreamTmuxScreen(false))
		}
		resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, opts...)
		if err != nil {
			t.Fatalf("turn %q: %v", prompt, err)
		}
		return strings.TrimSpace(resp.Choices[0].Content)
	}
	streamed := func(chunks []llmtypes.StreamChunk) string {
		var b strings.Builder
		for _, c := range chunks {
			if c.Type == llmtypes.StreamChunkTypeContent {
				b.WriteString(c.Content)
			}
		}
		return b.String()
	}

	tokenA, tokenB, tokenC := "HISTA-"+museRandomHex(t, 4), "EARLYB-"+museRandomHex(t, 4), "TOOLC-"+museRandomHex(t, 4)
	if final := turn(fmt.Sprintf("Reply with exactly %s and nothing else. Do not use tools.", tokenA), nil); !strings.Contains(final, tokenA) {
		t.Fatalf("turn 1 final = %q, want %s", final, tokenA)
	}

	stream2 := make(chan llmtypes.StreamChunk, 4096)
	final2 := turn(fmt.Sprintf("Reply with exactly %s and nothing else. Do not use tools.", tokenB), stream2)
	chunks2 := museDrainStream(stream2)
	text2 := streamed(chunks2)
	if !strings.Contains(final2, tokenB) || !strings.Contains(text2, tokenB) {
		t.Fatalf("early commit not streamed: final=%q streamed=%q (%d chunks)", final2, text2, len(chunks2))
	}
	if strings.Contains(text2, tokenA) {
		t.Fatalf("turn 2 stream replayed turn 1 history: %q", text2)
	}

	stream3 := make(chan llmtypes.StreamChunk, 4096)
	final3 := turn(fmt.Sprintf("Use your shell tool exactly once to run: echo %s . Then reply with exactly DONE-%s and nothing else.", tokenC, tokenC), stream3)
	chunks3 := museDrainStream(stream3)
	text3 := streamed(chunks3)
	if !strings.Contains(final3, "DONE-"+tokenC) || !strings.Contains(text3, "DONE-"+tokenC) {
		t.Fatalf("tool turn answer not streamed: final=%q streamed=%q", final3, text3)
	}
	if strings.Contains(text3, tokenA) || strings.Contains(text3, tokenB) {
		t.Fatalf("turn 3 stream replayed earlier turns: %q", text3)
	}
	starts, ends := map[string]int{}, map[string]int{}
	for _, c := range chunks3 {
		switch c.Type {
		case llmtypes.StreamChunkTypeToolCallStart:
			starts[c.ToolCallID]++
		case llmtypes.StreamChunkTypeToolCallEnd:
			ends[c.ToolCallID]++
		}
	}
	if len(starts) == 0 {
		t.Fatalf("no tool start streamed for the shell call: %+v", chunks3)
	}
	for id, n := range starts {
		if id == "" || n != 1 || ends[id] != 1 {
			t.Fatalf("tool %q: %d starts, %d ends; want exactly one of each (starts=%v ends=%v)", id, n, ends[id], starts, ends)
		}
	}
	for id := range ends {
		if starts[id] != 1 {
			t.Fatalf("tool end %q without a matching start (starts=%v ends=%v)", id, starts, ends)
		}
	}
	t.Logf("PASS: early commit streamed (%d chunks), tool pairing %v, no history replay across 3 retained turns", len(chunks2), starts)
}
