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
		if state != want || status == "in_progress" && !next.Equal(now.Add(20*time.Minute)) {
			t.Fatal("wrong reply state", status, state, next)
		}
	}
	c.Messages = c.Messages[:3]
	task.TaskProgress.Attempts = 3
	if state, _ := agentTaskState(c, task, now); state != "stalled" {
		t.Fatal("unanswered task exceeded wake cap", state)
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
