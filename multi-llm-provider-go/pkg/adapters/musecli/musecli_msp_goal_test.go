package musecli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// /goal in a Muse chat uses Muse's own goal (no AgentWorks loop): the goal
// turn starts, goal/set attaches the goal to it, the chat follows the next
// turn Muse starts on its own while the goal is active, and Muse's
// session/goalChanged (with its progress and now/next) becomes the chat's goal
// bar until it says complete.
func TestMuseMSPGoalSetsMuseGoalAndFollowsItsTurns(t *testing.T) {
	toHost, hostIn := io.Pipe()
	fromHost, hostOut := io.Pipe()
	rpc := &museRPC{stdin: hostIn, pending: map[string]chan museRPCMessage{}, events: make(chan museRPCMessage, 64), done: make(chan struct{}), exited: make(chan struct{}), stop: func() {}}
	go rpc.read(fromHost)
	t.Cleanup(func() { rpc.finish(fmt.Errorf("test over")); _ = hostOut.Close() })
	var mu sync.Mutex
	var calls []string
	write := func(v any) {
		raw, _ := json.Marshal(v)
		_, _ = hostOut.Write(append(raw, '\n'))
	}
	note := func(method string, params map[string]any) {
		params["sessionId"] = "native-1"
		write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	}
	goal := func(status string) map[string]any {
		return map[string]any{"goal": map[string]any{"objective": "make the tests pass", "status": status, "createdAtMs": 1000, "updatedAtMs": 61000, "percentComplete": 60, "currentWork": "fixing the drag test", "nextWork": "rerun the suite"}}
	}
	reply := func(turn, id, text string) {
		note("item/started", map[string]any{"item": map[string]any{"itemId": id, "kind": "agentMessage", "turnId": turn}})
		note("item/completed", map[string]any{"item": map[string]any{"itemId": id, "kind": "agentMessage", "turnId": turn, "text": text}})
		note("turn/completed", map[string]any{"turnId": turn, "terminal": "completed"})
	}
	go func() {
		in := bufio.NewScanner(toHost)
		for in.Scan() {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params map[string]any  `json:"params"`
			}
			if json.Unmarshal(in.Bytes(), &req) != nil || len(req.ID) == 0 {
				continue
			}
			mu.Lock()
			calls = append(calls, fmt.Sprintf("%s %v", req.Method, req.Params["objective"]))
			mu.Unlock()
			switch req.Method {
			case "turn/start":
				write(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"turnId": "t1"}})
			case "goal/set":
				write(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"status": "accepted", "turnId": "t1"}})
				note("session/goalChanged", goal("active"))
				reply("t1", "m1", "first part done")
				note("turn/started", map[string]any{"turnId": "t2"})
				reply("t2", "m2", "all tests pass")
				note("session/goalChanged", goal("complete"))
			}
		}
	}()
	s := &museMSPSession{owner: "chat-goal", sessionID: "native-1", rpc: rpc}
	a := &MuseCLIAdapter{}
	go a.pumpMuseMSP(s)
	command := llmtypes.NativeGoalCommand{SessionID: "chat-goal", Action: "set", Objective: "make the tests pass"}
	s.goalCommand = &command
	var goals []*llmtypes.NativeGoal
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	params := map[string]any{"commandId": museCommandID(), "sessionId": "native-1", "input": []map[string]any{{"type": "text", "text": "Goal: make the tests pass"}}}
	resp, err := a.runMuseMSPTurns(ctx, s, "chat-goal", params, false, &llmtypes.CallOptions{}, func(c llmtypes.StreamChunk) {
		if c.Type == llmtypes.StreamChunkTypeNativeGoal {
			goals = append(goals, c.NativeGoal.Goal)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || calls[0] != "turn/start <nil>" || calls[1] != "goal/set make the tests pass" {
		t.Fatalf("calls = %v", calls)
	}
	if text := resp.Choices[0].Content; !strings.Contains(text, "first part done") || !strings.Contains(text, "all tests pass") {
		t.Fatalf("reply = %q", text)
	}
	if len(goals) != 2 || goals[0].Status != "active" || goals[1].Status != "complete" || goals[1].TimeUsedSeconds == nil || *goals[1].TimeUsedSeconds != 60 ||
		goals[1].PercentComplete == nil || *goals[1].PercentComplete != 60 || goals[1].CurrentWork != "fixing the drag test" || goals[1].NextWork != "rerun the suite" {
		t.Fatalf("goals = %+v", goals)
	}
}
