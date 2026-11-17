package server

import "strings"

// Network addresses retain the exact saved login, never a Crew pair.
type desktopAgentNetworkRequest struct {
	Queue        bool   `json:"queue,omitempty"`
	Ask          bool   `json:"ask,omitempty"`
	ProgressTask string `json:"progress_task,omitempty"`
	AutomaticFor string `json:"automatic_for,omitempty"`
	ExpectsReply bool   `json:"expects_reply,omitempty"`
	Status       string `json:"status,omitempty"`
	Message      string `json:"message,omitempty"`
	SubmissionID string `json:"submission_id,omitempty"`
	Read         bool   `json:"read,omitempty"`
	After        int    `json:"after,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	// TaskArgs carries the #2965 task fields (title, ref, files, decision, why) as typed.
	TaskArgs map[string]any `json:"-"`
}

func splitDesktopAgentNetworkAddress(raw string) (connection, address string, ok bool) {
	rest, ok := strings.CutPrefix(raw, "network:")
	if !ok {
		return "", "", false
	}
	connection, address, ok = strings.Cut(rest, ":")
	_, teamAddress := agentTeamTarget(address) // "team" or "person:<name>" on that network (#2965)
	return connection, address, ok && connection != "" && (strings.HasPrefix(address, "agent:") || strings.HasPrefix(address, "inbox-") || teamAddress)
}

// Keep every callable address on the same verified network route. Conversation
// IDs and message text remain server data, not local mailbox addresses.
func qualifyDesktopAgentReplyAddresses(out map[string]any, connection string) {
	qualify := func(row map[string]any) {
		for _, key := range []string{"inbox_id", "reply_address"} {
			if address, _ := row[key].(string); strings.HasPrefix(address, "inbox-") {
				row[key] = "network:" + connection + ":" + address
			}
		}
	}
	qualify(out)
	// The response has just been decoded from the selected network's JSON.
	messages, _ := out["messages"].([]any)
	for _, item := range messages {
		if row, ok := item.(map[string]any); ok {
			qualify(row)
		}
	}
}

// Preserve routing intent separately from expects_reply: a request may be a
// direct send, while only a new ask selects the current admitted ask target.
func (in desktopAgentNetworkRequest) messageAction(address string) string {
	if in.Queue {
		return "queue"
	}
	if in.Ask && !strings.HasPrefix(address, "inbox-") {
		return "ask"
	}
	return "send"
}

func agentMessageAskArgs(args map[string]any) bool { ask, _ := args["ask"].(bool); return ask }
