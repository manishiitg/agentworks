package codexcli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"testing"
	"time"
)

type capacityRetryWriter struct {
	t          *testing.T
	rpc        *codexRPC
	starts     int
	alwaysBusy bool
}

func (w *capacityRetryWriter) Close() error { return nil }
func (w *capacityRetryWriter) Write(raw []byte) (int, error) {
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Input []json.RawMessage `json:"input"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return 0, err
	}
	if request.Method != "turn/start" || len(request.Params.Input) != 0 {
		w.t.Errorf("retry must continue with empty input, got %s", request.Method)
	}
	w.starts++
	turn := fmt.Sprintf("retry-%d", w.starts)
	result, _ := json.Marshal(map[string]any{"turn": map[string]any{"id": turn}})
	w.rpc.mu.Lock()
	ch := w.rpc.pending[string(request.ID)]
	w.rpc.mu.Unlock()
	ch <- codexRPCMessage{Result: result}
	status := "completed"
	var failure any
	if w.alwaysBusy || w.starts < 2 {
		status = "failed"
		failure = map[string]any{"message": "at capacity", "codexErrorInfo": "serverOverloaded"}
	}
	params, _ := json.Marshal(map[string]any{"threadId": "thread", "turn": map[string]any{"id": turn, "status": status, "error": failure}})
	w.rpc.events <- codexRPCMessage{Method: "turn/completed", Params: params}
	return len(raw), nil
}

// PLAT-746-941: a typed capacity failure retries its persisted thread, never
// replays the person's message, and stops at five continuations.
func TestCodexCapacityRetryContinuesEmptyInputAndCaps(t *testing.T) {
	prior := codexCapacityRetryWait
	codexCapacityRetryWait = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(func() { codexCapacityRetryWait = prior })
	for _, always := range []bool{false, true} {
		r := &codexRPC{events: make(chan codexRPCMessage, 12), done: make(chan struct{}), pending: map[string]chan codexRPCMessage{}}
		w := &capacityRetryWriter{t: t, rpc: r, alwaysBusy: always}
		r.stdin = w
		r.events <- codexRPCMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"first","status":"failed","error":{"message":"busy","codexErrorInfo":"serverOverloaded"}}}`)}
		retries := 0
		_, err := (&CodexCLIAdapter{}).consumeAppTurnWithCapacityRetry(context.Background(), &codexAppSession{threadID: "thread"}, r, "owner", "thread", "first", "task", &llmtypes.CallOptions{}, func(c llmtypes.StreamChunk) {
			if c.NativeRetry != nil && c.NativeRetry.Phase == "start" {
				retries++
			}
		})
		want := 2
		if always {
			want = 5
		}
		if w.starts != want || retries != want || (err != nil) != always {
			t.Fatalf("always=%t starts=%d retries=%d err=%v", always, w.starts, retries, err)
		}
	}
	// Stop during backoff must not send another turn/start.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &codexRPC{events: make(chan codexRPCMessage, 1), done: make(chan struct{}), pending: map[string]chan codexRPCMessage{}}
	w := &capacityRetryWriter{t: t, rpc: r}
	r.stdin = w
	r.events <- codexRPCMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"first","status":"failed","error":{"message":"busy","codexErrorInfo":"serverOverloaded"}}}`)}
	_, err := (&CodexCLIAdapter{}).consumeAppTurnWithCapacityRetry(ctx, &codexAppSession{threadID: "thread"}, r, "owner", "thread", "first", "task", &llmtypes.CallOptions{}, func(c llmtypes.StreamChunk) {
		if c.NativeRetry != nil && c.NativeRetry.Phase == "start" {
			cancel()
		}
	})
	if err != context.Canceled || w.starts != 0 {
		t.Fatalf("cancelled retry starts=%d err=%v", w.starts, err)
	}

	// T3 capacity recovery explicitly excludes /compact. Its failure remains a
	// failure, with no empty-input task continuation and no retry progress row.
	compactRPC := &codexRPC{events: make(chan codexRPCMessage, 1), done: make(chan struct{}), pending: map[string]chan codexRPCMessage{}}
	compactWriter := &capacityRetryWriter{t: t, rpc: compactRPC}
	compactRPC.stdin = compactWriter
	compactRPC.events <- codexRPCMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"compact","status":"failed","error":{"message":"busy","codexErrorInfo":"serverOverloaded"}}}`)}
	compactRetries := 0
	_, compactErr := (&CodexCLIAdapter{}).consumeAppTurnWithCapacityRetry(context.Background(), &codexAppSession{threadID: "thread"}, compactRPC, "owner", "thread", "compact", " /compact ", &llmtypes.CallOptions{}, func(chunk llmtypes.StreamChunk) {
		if chunk.NativeRetry != nil {
			compactRetries++
		}
	})
	if compactErr == nil || compactWriter.starts != 0 || compactRetries != 0 {
		t.Fatalf("compact became task continuation: starts=%d notices=%d err=%v", compactWriter.starts, compactRetries, compactErr)
	}

}
