package server

import (
	"context"
	"time"
)

// AgentBridge task state belongs to the admitted message, not to a model plan.
// A reply is a later send from the receiving side of that same conversation.
type agentTaskProgress struct {
	WakeTimes      []time.Time                `json:"wake_times,omitempty"`
	PendingAttempt int                        `json:"pending_attempt,omitempty"`
	NoProgress     int                        `json:"no_progress,omitempty"`
	Status         string                     `json:"status,omitempty"`
	Attempts       int                        `json:"attempts,omitempty"`
	NextWake       time.Time                  `json:"next_wake,omitempty"`
	RemoteState    string                     `json:"remote_state,omitempty"`
	RemoteDue      time.Time                  `json:"remote_due,omitempty"`
	LocalOwner     string                     `json:"local_owner,omitempty"`
	LocalRecord    *ProductConversationRecord `json:"local_record,omitempty"`
	Session        string                     `json:"session,omitempty"`
}

func validAgentReplyStatus(status string) bool {
	switch status {
	case "", "done", "in_progress", "blocked", "needs_owner":
		return true
	}
	return false
}

func agentTaskLatestReply(c agentConversation, task agentMessage) (agentMessage, bool) {
	var latest agentMessage
	found := false
	for _, m := range c.Messages {
		if m.Sequence > task.Sequence && m.From != task.From && m.Kind == "" {
			if !found || m.Sequence > latest.Sequence {
				latest, found = m, true
			}
		}
	}
	return latest, found
}

// Derive closure from durable sends, never final chat text or tool stdout.
func agentTaskState(c agentConversation, task agentMessage, now time.Time) (string, time.Time) {
	if task.MessageType != "request" || task.Kind != "" || task.Delivery != "delivered" {
		return "", time.Time{}
	}
	if task.TaskProgress.LocalRecord != nil {
		switch task.TaskProgress.RemoteState {
		case "done", "blocked", "needs_owner":
			return task.TaskProgress.RemoteState, time.Time{}
		}
	}
	state := "open"
	due := task.TaskProgress.NextWake
	if task.TaskProgress.RemoteDue.After(due) {
		due = task.TaskProgress.RemoteDue
	}
	if reply, ok := agentTaskLatestReply(c, task); ok {
		switch reply.ReplyStatus {
		case "blocked", "needs_owner":
			return reply.ReplyStatus, time.Time{}
		case "in_progress":
			state = "in_progress"
			pause := reply.SentAt.Add(2 * time.Minute)
			if pause.After(due) {
				due = pause
			}
		default:
			return "done", time.Time{}
		}
	}
	if task.TaskProgress.NoProgress >= 2 {
		return "stalled", time.Time{}
	}
	if task.TaskProgress.PendingAttempt != 0 {
		return state, now.Add(time.Second)
	}
	recent := agentTaskRecentWakes(task.TaskProgress.WakeTimes, now)
	if len(recent) >= 12 && recent[0].Add(time.Hour).After(due) {
		due = recent[0].Add(time.Hour)
	}
	return state, due
}

func agentTaskRecentWakes(times []time.Time, now time.Time) []time.Time {
	var recent []time.Time
	for _, at := range times {
		if at.After(now.Add(-time.Hour)) {
			recent = append(recent, at)
		}
	}
	return recent
}

func agentTaskWakeDelay(int) time.Duration { return time.Second }

type agentTaskSendKey struct{}
type agentTaskSend struct {
	AutomaticFor string
	Status       string
	ExpectsReply bool
}

func agentTaskSendContext(ctx context.Context, status string, request bool) context.Context {
	auto, _ := ctx.Value(agentTaskAutoKey{}).(string)
	return context.WithValue(ctx, agentTaskSendKey{}, agentTaskSend{Status: status, ExpectsReply: request, AutomaticFor: auto})
}
func agentMessageRequestArgs(args map[string]interface{}) bool {
	request, _ := args["expects_reply"].(bool)
	ask, _ := args["ask"].(bool)
	action, _ := args["action"].(string)
	return request || ask || action == "ask"
}
func agentMessagePurpose(m agentMessage) string {
	if m.MessageType == "request" {
		return "Reply expected"
	}
	if m.MessageType == "notification" {
		return "For your information"
	}
	return ""
}
func agentMessageDeliveryInstructions(inbox string, m agentMessage) string {
	label := agentMessagePurpose(m)
	if label == "" {
		return agentMessageInstructions(inbox)
	}
	return label + ". " + agentMessageInstructions(inbox)
}

func agentReplyStatusSchema() map[string]interface{} {
	return map[string]interface{}{"type": "string", "enum": []string{"done", "in_progress", "blocked", "needs_owner"}, "description": "Reply in the original thread: done (default), in_progress (pause reminders about 2 minutes), blocked or needs_owner (stop reminders)."}
}

func agentMessageLabeledBody(m agentMessage) string {
	if label := agentMessagePurpose(m); label != "" {
		return label + ".\n\n" + m.Text
	}
	return m.Text
}

func quietAgentTaskMessage(m agentMessage) bool {
	return m.Kind == agentTaskUpdateKind || m.ReplyStatus == "in_progress" && m.MessageType != "request"
}
func agentRequestTaskStates(c agentConversation, side int) map[string]map[string]interface{} {
	out := map[string]map[string]interface{}{}
	for _, m := range c.Messages {
		if m.From == side || m.MessageType != "request" || m.Kind != "" {
			continue
		}
		state, due := agentTaskState(c, m, time.Now())
		out[m.ID] = map[string]interface{}{"state": state, "due": due}
	}
	return out
}

type agentTaskAutoKey struct{}
