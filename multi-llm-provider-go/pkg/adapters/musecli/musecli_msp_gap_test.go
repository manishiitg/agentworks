package musecli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"
)

// When Muse's delivery queue falls behind it drops events and sends view/gap.
// The missed events (text, a tool, the turn's end) are read back with
// view/page and reach the running turn in order, stopping before `next`,
// which was delivered live.
func TestMuseMSPGapReadsTheMissedEventsBack(t *testing.T) {
	toHost, hostIn := io.Pipe()
	fromHost, hostOut := io.Pipe()
	rpc := &museRPC{stdin: hostIn, pending: map[string]chan museRPCMessage{}, events: make(chan museRPCMessage, 64), done: make(chan struct{}), exited: make(chan struct{}), stop: func() {}}
	go rpc.read(fromHost)
	t.Cleanup(func() { rpc.finish(fmt.Errorf("test over")); _ = hostOut.Close() })
	event := func(method, cursor string) map[string]any {
		return map[string]any{"method": method, "params": map[string]any{"sessionId": "native-1", "viewCursor": cursor, "turnId": "t1"}}
	}
	pageRequests := make(chan map[string]any, 4)
	go func() {
		in := bufio.NewScanner(toHost)
		for in.Scan() {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params map[string]any  `json:"params"`
			}
			if json.Unmarshal(in.Bytes(), &req) != nil || req.Method != "view/page" {
				continue
			}
			pageRequests <- req.Params
			reply, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"events":     []any{event("item/delta", "c2"), event("turn/completed", "c3"), event("item/started", "c4"), event("item/delta", "c5")},
				"nextCursor": "c5",
			}})
			_, _ = hostOut.Write(append(reply, '\n'))
		}
	}()
	s := &museMSPSession{owner: "chat-gap", sessionID: "native-1", rpc: rpc}
	listener := make(chan museRPCMessage, 16)
	s.listener = listener
	go (&MuseCLIAdapter{}).pumpMuseMSP(s)

	gap, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "view/gap", "params": map[string]any{"sessionId": "native-1", "after": "c1", "next": "c4"}})
	_, _ = hostOut.Write(append(gap, '\n'))
	var got []string
	for len(got) < 2 {
		select {
		case msg := <-listener:
			got = append(got, msg.Method)
		case <-time.After(5 * time.Second):
			t.Fatalf("missed events = %v", got)
		}
	}
	if got[0] != "item/delta" || got[1] != "turn/completed" {
		t.Fatalf("missed events = %v", got)
	}
	select {
	case msg := <-listener:
		t.Fatalf("passed on %s at or after next", msg.Method)
	case <-time.After(100 * time.Millisecond):
	}
	if req := <-pageRequests; req["cursor"] != "c1" || req["sessionId"] != "native-1" || req["direction"] != "forward" {
		t.Fatalf("page request = %v", req)
	}
}
