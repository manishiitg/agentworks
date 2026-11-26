package llmtypes

// PendingHumanMessages preserves every queued input after the last assistant
// turn. Native resume already owns the preceding conversation (PLAT-872).
func PendingHumanMessages(messages []MessageContent) []MessageContent {
	start := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == ChatMessageTypeAI {
			start = i + 1
			break
		}
	}
	result := make([]MessageContent, 0, len(messages)-start)
	for _, m := range messages[start:] {
		if m.Role == ChatMessageTypeHuman {
			result = append(result, m)
		}
	}
	return result
}
