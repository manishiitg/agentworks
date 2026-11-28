package musecli

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A Muse chat looked stuck while Muse waited to retry a failed model call: the
// retry is now the chat's retry notice, shown once even when Muse reports it at
// both the turn and the model layer, and closed when the model answers.
func TestMuseMSPRetryShowsUntilTheModelAnswers(t *testing.T) {
	s := &museMSPSession{sessionID: "s1", rpc: &museRPC{done: make(chan struct{})}}
	events := make(chan museRPCMessage, 16)
	send := func(method string, params any) {
		raw, _ := json.Marshal(params)
		events <- museRPCMessage{Method: method, Params: raw}
	}
	retry := map[string]any{"turnId": "t1", "attempt": 1, "nextAttempt": 2, "maxAttempts": 5, "retryDelayMs": 20000, "reason": "overloaded"}
	send("turn/retryScheduled", retry)
	send("turn/modelRetryScheduled", retry)
	send("turn/retryScheduled", map[string]any{"turnId": "other", "attempt": 1, "nextAttempt": 2, "maxAttempts": 5})
	send("item/started", map[string]any{"item": map[string]any{"itemId": "m1", "kind": "agentMessage", "turnId": "t1"}})
	send("item/delta", map[string]any{"itemId": "m1", "field": "text", "delta": "done"})
	send("turn/completed", map[string]any{"turnId": "t1", "terminal": "completed"})
	var retries []llmtypes.NativeRetry
	if _, err := (&MuseCLIAdapter{}).consumeMuseMSPTurn(context.Background(), s, events, "t1", &museMSPTurnTotals{}, &llmtypes.CallOptions{}, func(c llmtypes.StreamChunk) {
		if c.Type == llmtypes.StreamChunkTypeNativeRetry {
			retries = append(retries, *c.NativeRetry)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(retries) != 2 || retries[0].Phase != "start" || retries[0].Attempt != 2 || retries[0].MaxAttempts != 5 || retries[0].DelayMS != 20000 || retries[1].Phase != "end" || !retries[1].Success {
		t.Fatalf("retries = %+v", retries)
	}
}
