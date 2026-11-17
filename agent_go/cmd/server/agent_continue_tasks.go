package server

import (
	"context"
	"strings"
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
	switch agentReplyStatus(status) {
	case "", "done", "in_progress", "blocked", "needs_you":
		return true
	}
	return false
}

// #2965: needs_owner was renamed needs_you. Older Mac apps and older servers
// still send the old name for one release; remove this with the work log.
func agentReplyStatus(status string) string {
	if status = strings.TrimSpace(status); status == "needs_owner" {
		return "needs_you"
	}
	return status
}

func agentTaskLatestReply(c agentConversation, task agentMessage) (agentMessage, bool) {
	var latest agentMessage
	found := false
	for _, m := range c.Messages {
		if m.Sequence > task.Sequence && m.From != task.From && m.Kind == "" && !m.QueuedRequest && !m.Decision {
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
		switch remote := agentReplyStatus(task.TaskProgress.RemoteState); remote {
		case "done", "blocked", "needs_you":
			return remote, time.Time{}
		}
	}
	state := "open"
	due := task.TaskProgress.NextWake
	if task.TaskProgress.RemoteDue.After(due) {
		due = task.TaskProgress.RemoteDue
	}
	if reply, ok := agentTaskLatestReply(c, task); ok {
		switch status := agentReplyStatus(reply.ReplyStatus); status {
		case "blocked", "needs_you":
			return status, time.Time{}
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
	Queue        bool
	Fields       agentTaskFields
}

func agentTaskSendContext(ctx context.Context, status string, request bool, queue ...bool) context.Context {
	auto, _ := ctx.Value(agentTaskAutoKey{}).(string)
	return context.WithValue(ctx, agentTaskSendKey{}, agentTaskSend{Status: agentReplyStatus(status), ExpectsReply: request || len(queue) > 0 && queue[0], Queue: len(queue) > 0 && queue[0], AutomaticFor: auto})
}

// agentTaskArgsContext is agentTaskSendContext plus the task fields a person or agent typed.
func agentTaskArgsContext(ctx context.Context, args map[string]interface{}) (context.Context, error) {
	ctx = agentTaskSendContext(ctx, stringToolArg(args, "status"), agentMessageRequestArgs(args), agentMessageQueueArgs(args))
	fields, err := agentTaskFieldsFromArgs(args)
	if err != nil {
		return ctx, err
	}
	send, _ := ctx.Value(agentTaskSendKey{}).(agentTaskSend)
	send.Fields = fields
	return context.WithValue(ctx, agentTaskSendKey{}, send), nil
}
func agentMessageRequestArgs(args map[string]interface{}) bool {
	request, _ := args["expects_reply"].(bool)
	ask, _ := args["ask"].(bool)
	action, _ := args["action"].(string)
	return request || ask || action == "ask" || args["message_kind"] == "ask" || agentMessageQueueArgs(args)
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
	return map[string]interface{}{"type": "string", "enum": []string{"done", "in_progress", "blocked", "needs_you"}, "description": "Reply in the original thread: done (default), in_progress (you took it; pauses reminders about 2 minutes), blocked or needs_you (waiting on a person; stops reminders)."}
}

func agentMessageLabeledBody(m agentMessage) string {
	if label := agentMessagePurpose(m); label != "" {
		return label + ".\n\n" + m.Text
	}
	return m.Text
}

func quietAgentTaskMessage(m agentMessage) bool {
	return m.Kind == agentTaskUpdateKind || m.MessageType == "notification" && m.TaskReplyTo == "" || m.ReplyStatus == "in_progress" && m.MessageType != "request"
}

// Delivery is independent of starting a turn. Automatic progress remains quiet.
func quietAgentAttachedMessage(m agentMessage, _ namedMessageAgent) bool {
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

func agentMessageQueueArgs(args map[string]interface{}) bool {
	queue, _ := args["queue"].(bool)
	action, _ := args["action"].(string)
	return queue || action == "queue" || args["message_kind"] == "queue"
}
