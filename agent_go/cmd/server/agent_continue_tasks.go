package server

import "time"

// AgentBridge task state belongs to the admitted message, not to a model plan.
// A reply is a later send from the receiving side of that same conversation.
type agentTaskProgress struct {
	Status   string    `json:"status,omitempty"`
	Attempts int       `json:"attempts,omitempty"`
	NextWake time.Time `json:"next_wake,omitempty"`
	Session  string    `json:"session,omitempty"`
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
	if !task.Task || task.Kind != "" || task.Delivery != "delivered" {
		return "", time.Time{}
	}
	if reply, ok := agentTaskLatestReply(c, task); ok {
		switch reply.ReplyStatus {
		case "blocked", "needs_owner":
			return reply.ReplyStatus, time.Time{}
		case "in_progress":
			return "in_progress", reply.SentAt.Add(20 * time.Minute)
		default:
			return "done", time.Time{}
		}
	}
	if task.TaskProgress.Attempts >= 3 {
		return "stalled", time.Time{}
	}
	return "open", task.TaskProgress.NextWake
}

func agentTaskWakeDelay(attempt int) time.Duration {
	switch attempt {
	case 0:
		return 2 * time.Minute
	case 1:
		return 10 * time.Minute
	default:
		return 30 * time.Minute
	}
}
