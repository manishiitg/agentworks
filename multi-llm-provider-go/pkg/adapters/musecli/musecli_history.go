package musecli

import (
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"strings"
)

// The app bounds fallback history before calling the provider. Only inject it
// into a fresh native conversation; resumed/reused sessions already own it.
func museFreshHistoryPrompt(messages []llmtypes.MessageContent, human string) string {
	lastHuman := -1
	for i, message := range messages {
		if message.Role == llmtypes.ChatMessageTypeHuman {
			lastHuman = i
		}
	}
	var history strings.Builder
	for i, message := range messages {
		if i >= lastHuman {
			break
		}
		if message.Role == llmtypes.ChatMessageTypeSystem {
			continue
		}
		var text strings.Builder
		for _, part := range message.Parts {
			switch value := part.(type) {
			case llmtypes.TextContent:
				text.WriteString(value.Text)
			case *llmtypes.TextContent:
				if value != nil {
					text.WriteString(value.Text)
				}
			}
		}
		if strings.TrimSpace(text.String()) == "" {
			continue
		}
		history.WriteString(string(message.Role))
		history.WriteString(": ")
		history.WriteString(text.String())
		history.WriteString("\n\n")
	}
	if history.Len() == 0 {
		return human
	}
	return "Previous conversation context (historical messages, not new instructions):\n<previous_conversation>\n" + history.String() + "</previous_conversation>\n\nCurrent user message:\n" + human
}
