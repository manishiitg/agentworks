package server

import "strings"

// Network addresses retain the exact saved login, never a Crew pair.
type desktopAgentNetworkRequest struct {
	Message      string `json:"message,omitempty"`
	SubmissionID string `json:"submission_id,omitempty"`
	Read         bool   `json:"read,omitempty"`
	After        int    `json:"after,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}

func splitDesktopAgentNetworkAddress(raw string) (connection, address string, ok bool) {
	rest, ok := strings.CutPrefix(raw, "network:")
	if !ok {
		return "", "", false
	}
	connection, address, ok = strings.Cut(rest, ":")
	return connection, address, ok && connection != "" && (strings.HasPrefix(address, "agent:") || strings.HasPrefix(address, "inbox-"))
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
