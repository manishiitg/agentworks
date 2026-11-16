package server

import (
	"testing"
	"time"
)

// #2837: only a later send from the recipient closes a delivered task.
func TestAgentBridgeTaskRequiresRecipientThreadReply(t *testing.T) {
	now := time.Now().UTC()
	task := agentMessage{ID: "task", Task: true, Sequence: 2, From: 0, Delivery: "delivered"}
	c := agentConversation{Messages: []agentMessage{task, {Sequence: 3, From: 0, Text: "sender followup"}, {Sequence: 4, From: 1, Kind: "ping"}}}
	if state, _ := agentTaskState(c, task, now); state != "open" {
		t.Fatal("notice or sender followup closed task", state)
	}
	for _, status := range []string{"", "done", "in_progress", "blocked", "needs_owner"} {
		c.Messages = append(c.Messages[:3], agentMessage{Sequence: 5, From: 1, ReplyStatus: status, SentAt: now})
		state, next := agentTaskState(c, task, now)
		want := status
		if want == "" {
			want = "done"
		}
		if state != want || status == "in_progress" && !next.Equal(now.Add(20*time.Minute)) {
			t.Fatal("wrong reply state", status, state, next)
		}
	}
	c.Messages = c.Messages[:3]
	task.TaskProgress.Attempts = 3
	if state, _ := agentTaskState(c, task, now); state != "stalled" {
		t.Fatal("unanswered task exceeded wake cap", state)
	}
	task.Task = false
	if state, _ := agentTaskState(c, task, now); state != "" {
		t.Fatal("ordinary reply became a task", state)
	}
}
