package server

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

// The wait returns once the chat's own tasks leave the roster, and stops at its limit with the tasks still listed.
func TestWaitForClaudeBackgroundTasks(t *testing.T) {
	var mu sync.Mutex
	roster := []claudecode.SDKBackgroundTask{{TaskID: "t1", Description: "tests"}, {TaskID: "t2", Description: "build"}}
	previous, previousPoll := claudeBackgroundTasks, claudeBackgroundWaitPoll
	claudeBackgroundTasks = func(string) []claudecode.SDKBackgroundTask {
		mu.Lock()
		defer mu.Unlock()
		return append([]claudecode.SDKBackgroundTask(nil), roster...)
	}
	claudeBackgroundWaitPoll = 10 * time.Millisecond
	t.Cleanup(func() { claudeBackgroundTasks, claudeBackgroundWaitPoll = previous, previousPoll })

	decode := func(raw string) claudeBackgroundWaitResult {
		var out claudeBackgroundWaitResult
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	go func() {
		time.Sleep(60 * time.Millisecond)
		mu.Lock()
		roster = nil
		mu.Unlock()
	}()
	raw, err := waitForClaudeBackgroundTasks(context.Background(), "chat-1", map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if got := decode(raw); got.StoppedWaiting != "finished" || len(got.Finished) != 2 || len(got.StillRunning) != 0 {
		t.Fatalf("tasks finished, got %+v", got)
	}

	mu.Lock()
	roster = []claudecode.SDKBackgroundTask{{TaskID: "t3", Description: "dev server"}}
	mu.Unlock()
	raw, err = waitForClaudeBackgroundTasks(context.Background(), "chat-1", map[string]interface{}{"task_ids": []interface{}{"t3", "missing"}, "max_seconds": float64(1)})
	if err != nil {
		t.Fatal(err)
	}
	if got := decode(raw); got.StoppedWaiting != "limit" || len(got.StillRunning) != 1 || got.StillRunning[0].TaskID != "t3" || len(got.NotFound) != 1 || got.NotFound[0] != "missing" {
		t.Fatalf("limit reached with t3 running and one unknown id, got %+v", got)
	}
}
