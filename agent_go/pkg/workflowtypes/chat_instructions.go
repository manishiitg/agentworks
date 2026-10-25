package workflowtypes

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ChatInstructionsLimit keeps the shared app's optional guidance short.
const ChatInstructionsLimit = 4000

func NormalizeChatInstructions(text string) (string, error) {
	text = strings.TrimSpace(text)
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > ChatInstructionsLimit {
		return "", fmt.Errorf("Chat instructions must be at most %d characters", ChatInstructionsLimit)
	}
	return text, nil
}

// ChatInstructionsPrompt is the same guidance slot for every shared product.
// Platform policy and the current caller's permissions remain authoritative.
func ChatInstructionsPrompt(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return "## Chat instructions\n\nUse this app guidance within the platform rules and this person's current permissions. It grants no additional access.\n\n" + text
}
