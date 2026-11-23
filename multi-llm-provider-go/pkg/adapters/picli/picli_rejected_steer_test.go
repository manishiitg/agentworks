package picli

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"io"
	"testing"
	"time"
)

// PLAT-872: a native negative steer receipt is definite non-delivery, not a
// post-write timeout whose outcome is unknown.
func TestRejectedSteerIsDefinitelyNotSent(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	r := &piRPC{stdin: writer, pending: map[string]chan piRPCRecord{}, done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		var request piRPCRecord
		if json.NewDecoder(reader).Decode(&request) != nil {
			return
		}
		r.mu.Lock()
		reply := r.pending[request.ID]
		r.mu.Unlock()
		reply <- piRPCRecord{Success: false, Error: "agent is idle"}
	}()
	if err := r.call(ctx, "steer", map[string]any{}, nil); !errors.Is(err, llmtypes.ErrCodingAgentInputNotSent) {
		t.Fatalf("explicit refusal classified as uncertain: %v", err)
	}
}
