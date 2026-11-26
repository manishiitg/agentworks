package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
)

// wait_for_background_tasks: Claude calls it before ending a reply while a background task it started (a test run, a
// build) is still running and its answer needs the result. It returns when those tasks finish, or after a limit, and
// it only reads the chat's existing task list (claudeBackgroundTasks); it never starts or stops a task. Without the
// call Claude ends its reply and the chat follows up when the work finishes (claude_background_tasks.go).
const (
	claudeBackgroundWaitTool = "wait_for_background_tasks"
	// Under the 15 minute idle limit of a turn, so a wait never ends the reply by itself being quiet.
	claudeBackgroundWaitMax     = 10 * time.Minute
	claudeBackgroundWaitTimeout = 12 * time.Minute
)

// claudeBackgroundWaitInstruction is the one line the prompt carries for Claude; the tool description has the rest.
const claudeBackgroundWaitInstruction = "If a background task you started (tests, a build) is still running and your answer needs its result, call wait_for_background_tasks before you end your reply; do not wait on dev servers or watchers."

var claudeBackgroundWaitPoll = 2 * time.Second

func claudeBackgroundWaitSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "additionalProperties": false, "properties": map[string]interface{}{
		"task_ids":    map[string]interface{}{"type": "array", "maxItems": 20, "items": map[string]interface{}{"type": "string"}, "description": "Task IDs to wait for. Leave empty to wait for all background tasks of this chat."},
		"max_seconds": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": int(claudeBackgroundWaitMax / time.Second), "description": "Longest wait in seconds; default and maximum 600."},
	}}
}

func (api *StreamingAPI) registerClaudeBackgroundWaitTool(registrar definitionToolRegistrar, sessionID string) error {
	return registrar.RegisterCustomToolWithTimeout(claudeBackgroundWaitTool,
		"Wait for background tasks you started in this chat (a test run, a build) to finish, then return. Use it only when your answer needs their result; the chat shows the wait as working. Returns early when they finish, or after max_seconds with the tasks still running; call it again if you still need them. It does not stop any task. Do not use it for dev servers, watchers or PR watches: end your reply instead.",
		claudeBackgroundWaitSchema(), func(ctx context.Context, args map[string]interface{}) (string, error) {
			return waitForClaudeBackgroundTasks(ctx, sessionID, args)
		}, claudeBackgroundWaitTimeout, "background_tasks")
}

type claudeBackgroundWaitResult struct {
	Finished       []string                   `json:"finished"`
	StillRunning   []claudeBackgroundWaitTask `json:"still_running"`
	NotFound       []string                   `json:"not_found,omitempty"`
	WaitedSeconds  int                        `json:"waited_seconds"`
	StoppedWaiting string                     `json:"stopped_waiting"` // finished, limit
}

type claudeBackgroundWaitTask struct {
	TaskID string `json:"task_id"`
	Label  string `json:"label,omitempty"`
}

func waitForClaudeBackgroundTasks(ctx context.Context, sessionID string, args map[string]interface{}) (string, error) {
	want := map[string]bool{}
	if raw, ok := args["task_ids"].([]interface{}); ok {
		for _, item := range raw {
			if id, ok := item.(string); ok && strings.TrimSpace(id) != "" {
				want[strings.TrimSpace(id)] = true
			}
		}
	}
	limit := claudeBackgroundWaitMax
	if seconds, ok := args["max_seconds"].(float64); ok && seconds >= 1 && time.Duration(seconds)*time.Second < limit {
		limit = time.Duration(seconds) * time.Second
	}

	// running is the chat's task list narrowed to the tasks asked for (all of them when none were named).
	running := func() map[string]claudeBackgroundWaitTask {
		out := map[string]claudeBackgroundWaitTask{}
		for _, task := range claudeBackgroundTasks(sessionID) {
			if len(want) > 0 && !want[task.TaskID] {
				continue
			}
			label := strings.TrimSpace(task.Description)
			if label == "" {
				label = strings.TrimSpace(task.Command)
			}
			out[task.TaskID] = claudeBackgroundWaitTask{TaskID: task.TaskID, Label: label}
		}
		return out
	}

	started := time.Now()
	watching := running()
	result := claudeBackgroundWaitResult{Finished: []string{}, StillRunning: []claudeBackgroundWaitTask{}}
	for id := range want {
		if _, ok := watching[id]; !ok {
			result.NotFound = append(result.NotFound, id)
		}
	}
	log.Printf("[CLAUDE_BG_WAIT] start session=%s tasks=%d not_found=%d limit=%s", sessionID, len(watching), len(result.NotFound), limit)

	ticker := time.NewTicker(claudeBackgroundWaitPoll)
	defer ticker.Stop()
	for {
		now := running()
		result.StillRunning = result.StillRunning[:0]
		for id, task := range watching {
			if _, still := now[id]; still {
				result.StillRunning = append(result.StillRunning, task)
			}
		}
		if len(result.StillRunning) == 0 || time.Since(started) >= limit {
			break
		}
		select {
		case <-ctx.Done():
			log.Printf("[CLAUDE_BG_WAIT] end session=%s reason=cancelled waited=%s remaining=%d", sessionID, time.Since(started).Round(time.Second), len(result.StillRunning))
			return "", fmt.Errorf("the wait was interrupted")
		case <-ticker.C:
		}
	}

	stillRunning := map[string]bool{}
	for _, task := range result.StillRunning {
		stillRunning[task.TaskID] = true
	}
	for id := range watching {
		if !stillRunning[id] {
			result.Finished = append(result.Finished, id)
		}
	}
	sort.Strings(result.Finished)
	sort.Slice(result.StillRunning, func(i, j int) bool { return result.StillRunning[i].TaskID < result.StillRunning[j].TaskID })
	result.WaitedSeconds = int(time.Since(started).Round(time.Second) / time.Second)
	result.StoppedWaiting = "finished"
	if len(result.StillRunning) > 0 {
		result.StoppedWaiting = "limit"
	}
	log.Printf("[CLAUDE_BG_WAIT] end session=%s reason=%s waited=%ds finished=%d remaining=%d", sessionID, result.StoppedWaiting, result.WaitedSeconds, len(result.Finished), len(result.StillRunning))
	raw, err := json.Marshal(result)
	return string(raw), err
}
