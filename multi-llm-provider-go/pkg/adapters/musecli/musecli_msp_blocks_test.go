package musecli

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A message sent mid-turn showed above all of Muse's reply, including text written before it: each Muse message is
// now its own reply block, opened at its first text and closed with its whole text.
func TestMuseMSPMessagesAreSeparateReplyBlocks(t *testing.T) {
	s := &museMSPSession{sessionID: "s1", rpc: &museRPC{done: make(chan struct{})}}
	events := make(chan museRPCMessage, 16)
	send := func(method string, params any) {
		raw, _ := json.Marshal(params)
		events <- museRPCMessage{Method: method, Params: raw}
	}
	for _, item := range []struct{ id, text string }{{"m1", "before the steer"}, {"m2", "after the steer"}} {
		send("item/started", map[string]any{"item": map[string]any{"itemId": item.id, "kind": "agentMessage", "turnId": "t1"}})
		send("item/delta", map[string]any{"itemId": item.id, "field": "text", "delta": item.text})
		send("item/completed", map[string]any{"item": map[string]any{"itemId": item.id, "kind": "agentMessage", "turnId": "t1", "text": item.text}})
	}
	send("turn/completed", map[string]any{"turnId": "t1", "terminal": "completed"})
	var chunks []llmtypes.StreamChunk
	if _, err := (&MuseCLIAdapter{}).consumeMuseMSPTurn(context.Background(), s, events, "t1", &museMSPTurnTotals{}, &llmtypes.CallOptions{}, func(c llmtypes.StreamChunk) { chunks = append(chunks, c) }); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range chunks {
		id, _ := c.Metadata[llmtypes.AssistantBlockIDMetadataKey].(string)
		phase, _ := c.Metadata[llmtypes.AssistantBlockPhaseMetadataKey].(string)
		switch c.Type {
		case llmtypes.StreamChunkTypeAssistantBlock:
			got = append(got, phase+" "+id+" "+c.Content)
		case llmtypes.StreamChunkTypeContent:
			got = append(got, "text "+id+" "+c.Content)
		}
	}
	want := []string{
		"start muse:s1:t1:m1 ", "text muse:s1:t1:m1 before the steer", "end muse:s1:t1:m1 before the steer",
		"start muse:s1:t1:m2 ", "text muse:s1:t1:m2 after the steer", "end muse:s1:t1:m2 after the steer",
	}
	if len(got) != len(want) {
		t.Fatalf("chunks = %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chunk %d = %q, want %q", i, got[i], want[i])
		}
	}
}
