package server

import "time"

// A queue request remains a normal tracked request. Only its delivery timing
// differs: native inboxes release the oldest pending item at an idle boundary.
func nativeQueueRequestReady(a namedMessageAgent, conversations []agentConversation, m agentMessage) bool {
	if !m.QueuedRequest || a.Hosted != nil || a.Attachment != nil && a.Attachment.DeliveryRoute == appAgentDeliveryRoute {
		return true
	}
	if nativeQueueReleaseReason(a, m, time.Now()) == "" {
		return false
	}
	for _, c := range conversations {
		for _, other := range c.Messages {
			if !other.QueuedRequest || other.ID == m.ID || !other.InboxRemovedAt.IsZero() || c.Endpoints[1-other.From].NamedAgentID != a.ID {
				continue
			}
			// Admission of one request consumes this idle boundary. Another queue
			// request needs a later observed idle, or the bounded fallback cooldown.
			if other.Delivery == "delivered" && !other.DeliveredAt.IsZero() && (a.Attachment == nil || a.Attachment.Activity != "idle" || !a.Attachment.ActivityAt.After(other.DeliveredAt)) && time.Since(other.DeliveredAt) < 10*time.Minute {
				return false
			}
			if (other.Delivery == "stored" || other.Delivery == "pending" || other.Delivery == "dispatching") && agentQueueBefore(other, m) {
				return false
			}
		}
	}
	return true
}

func agentQueueBefore(a, b agentMessage) bool {
	if !a.SentAt.Equal(b.SentAt) {
		return a.SentAt.Before(b.SentAt)
	}
	if a.AgentSequence != b.AgentSequence {
		return a.AgentSequence < b.AgentSequence
	}
	return a.ID < b.ID
}

func olderAgentQueueRequest(conversations []agentConversation, dest agentMessageEndpoint, m agentMessage) bool {
	for _, c := range conversations {
		for _, other := range c.Messages {
			recipient := c.Endpoints[1-other.From]
			same := dest.UserID == recipient.UserID && (dest.NamedAgentID != "" && dest.NamedAgentID == recipient.NamedAgentID || dest.NamedAgentID == "" && dest.Session != "" && dest.Session == recipient.Session)
			if same && other.QueuedRequest && (other.Delivery == "pending" || other.Delivery == "dispatching") && agentQueueBefore(other, m) {
				return true
			}
		}
	}
	return false
}

// An unavailable native activity signal must not strand work forever. This is
// the owner-approved fallback to normal ask delivery, not a new timer/poller.
func nativeQueueReleaseReason(a namedMessageAgent, m agentMessage, now time.Time) string {
	if a.Attachment == nil || a.Attachment.Activity == "" || a.Attachment.Activity == "unknown" {
		return "activity_unknown"
	}
	if !m.SentAt.IsZero() && now.Sub(m.SentAt) >= 10*time.Minute {
		return "queue_timeout"
	}
	if a.Attachment.Activity == "idle" {
		return "queue_kind"
	}
	return ""
}
