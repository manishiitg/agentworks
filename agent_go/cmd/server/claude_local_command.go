package server

import (
	"encoding/json"
	"strings"
)

// Claude Code stores local slash commands and their stdout as user records.
// Match only a whole CLI envelope, not prose/code quoting these tag names.
func isClaudeLocalCommandRecord(text string) bool {
	rest := strings.TrimSpace(text)
	matched := false
	for rest != "" {
		found := false
		for _, tag := range []string{"local-command-stdout", "local-command-stderr", "local-command-caveat", "command-name", "command-message", "command-args"} {
			open, close := "<"+tag+">", "</"+tag+">"
			if !strings.HasPrefix(rest, open) {
				continue
			}
			end := strings.Index(rest[len(open):], close)
			if end < 0 {
				return false
			}
			rest = strings.TrimSpace(rest[len(open)+end+len(close):])
			found, matched = true, true
			break
		}
		if !found {
			return false
		}
	}
	return matched
}

func isClaudeLocalCommandMessage(role, text string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	// Claude also writes a background task's completion (<task-notification>…) as a user
	// turn; it is the CLI talking to itself, not something the person sent.
	return (role == "human" || role == "user") && (isClaudeLocalCommandRecord(text) || isProviderTaskNotification(text))
}

// Cached queries/previews may be truncated in the middle of an envelope.
// This only invalidates the cache; the full source is classified on reread.
func chatHistoryHasLocalCommandPreview(session ChatHistorySession) bool {
	suspect := func(text string) bool {
		text = strings.TrimSpace(text)
		return strings.HasPrefix(text, "<local-command-") || strings.HasPrefix(text, "<command-name>")
	}
	if suspect(session.Query) {
		return true
	}
	for _, msg := range session.PreviewMessages {
		if (msg.Role == "human" || msg.Role == "user") && suspect(msg.Text) {
			return true
		}
	}
	return false
}

// Hide legacy CLI records in a full history response without rewriting the
// source log or dropping tool traces, runtime metadata, or other message fields.
func filterClaudeLocalCommandHistory(data []byte) []byte {
	var doc map[string]json.RawMessage
	if json.Unmarshal(data, &doc) != nil {
		return data
	}
	var history []json.RawMessage
	if json.Unmarshal(doc["conversation_history"], &history) != nil {
		return data
	}
	clean := make([]json.RawMessage, 0, len(history))
	for _, message := range history {
		role, text := chatHistoryMessageRoleAndText(message)
		if !isClaudeLocalCommandMessage(role, text) {
			clean = append(clean, message)
		}
	}
	if len(clean) == len(history) {
		return data
	}
	encoded, err := json.Marshal(clean)
	if err != nil {
		return data
	}
	doc["conversation_history"] = encoded
	return marshalChatHistoryProjectionOrOriginal(doc, data)
}
