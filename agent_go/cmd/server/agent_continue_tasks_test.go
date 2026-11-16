package server

import (
	"testing"
	"time"
)

// #2837: only a later send from the recipient closes a delivered task.
func TestAgentBridgeTaskRequiresRecipientThreadReply(t *testing.T) {
	now := time.Now().UTC()
	task := agentMessage{ID: "task", MessageType: "request", Sequence: 2, From: 0, Delivery: "delivered"}
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
		if state != want || status == "in_progress" && !next.Equal(now.Add(2*time.Minute)) {
			t.Fatal("wrong reply state", status, state, next)
		}
	}
	c.Messages = c.Messages[:3]
	task.TaskProgress.NoProgress = 2
	if state, _ := agentTaskState(c, task, now); state != "stalled" {
		t.Fatal("two tool-free wake turns did not stall", state)
	}
	task.MessageType = "notification"
	if state, _ := agentTaskState(c, task, now); state != "" {
		t.Fatal("ordinary reply became a task", state)
	}
}

// #2837: send is FYI unless explicitly marked; action=ask always requests a reply.
func TestAgentBridgeNotificationsNeverOpenTasks(t *testing.T) {
	for _, tc := range []struct {
		args    map[string]interface{}
		request bool
	}{
		{map[string]interface{}{"action": "send"}, false},
		{map[string]interface{}{"action": "ask"}, true},
		{map[string]interface{}{"action": "send", "expects_reply": true}, true},
		{map[string]interface{}{"ask": true}, true},
		{map[string]interface{}{"action": "send", "expects_reply": false}, false},
	} {
		if got := agentMessageRequestArgs(tc.args); got != tc.request {
			t.Fatal("wrong message purpose", tc.args, got)
		}
	}
	notification := agentMessage{MessageType: "notification", Kind: "", Delivery: "delivered", Sequence: 1, From: 0}
	if state, _ := agentTaskState(agentConversation{Messages: []agentMessage{notification}}, notification, time.Now()); state != "" {
		t.Fatal("FYI opened task", state)
	}
}

// #2837: progress is not completion and the guard is hourly, not a lifetime cap.
func TestAgentBridgeProgressPausesTwoMinutesAndWakeBudgetRecovers(t *testing.T) {
	now := time.Now().UTC()
	task := agentMessage{ID: "task", MessageType: "request", Delivery: "delivered", Sequence: 1, From: 0}
	c := agentConversation{Messages: []agentMessage{task, {From: 1, Sequence: 2, ReplyStatus: "in_progress", SentAt: now}}}
	state, due := agentTaskState(c, task, now)
	if state != "in_progress" || !due.Equal(now.Add(2*time.Minute)) {
		t.Fatal("progress must pause, not close", state, due)
	}
	c.Messages = c.Messages[:1]
	for range 12 {
		task.TaskProgress.WakeTimes = append(task.TaskProgress.WakeTimes, now.Add(-30*time.Minute))
	}
	task.TaskProgress.Attempts = 40 // Lifetime history must not strand the task.
	state, due = agentTaskState(c, task, now)
	if state != "open" || !due.Equal(now.Add(30*time.Minute)) {
		t.Fatal("hourly guard did not pause", state, due)
	}
	state, due = agentTaskState(c, task, now.Add(31*time.Minute))
	if state != "open" || due.After(now.Add(31*time.Minute)) {
		t.Fatal("hourly guard never recovered", state, due)
	}
	task.TaskProgress.NoProgress = 2
	if state, _ = agentTaskState(c, task, now); state != "stalled" {
		t.Fatal("tool-free runaway did not stop", state)
	}
	c.Messages = append(c.Messages, agentMessage{From: 1, Sequence: 3, ReplyStatus: "done", SentAt: now})
	if state, _ = agentTaskState(c, task, now); state != "done" {
		t.Fatal("explicit completion failed to settle stalled request", state)
	}
}
