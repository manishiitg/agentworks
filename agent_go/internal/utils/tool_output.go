package utils

import "strings"

// DefaultLargeToolOutputThreshold is retained for the integration test commands.
const DefaultLargeToolOutputThreshold = 20000

// ExtractActualContent unwraps the text form returned by MCP tools.
func ExtractActualContent(content string) string {
	if strings.HasPrefix(content, `{"type":"text","text":"`) {
		start := len(`{"type":"text","text":"`)
		end := strings.LastIndex(content, `"}`)
		if end >= start {
			text := content[start:end]
			text = strings.ReplaceAll(text, `\"`, `"`)
			text = strings.ReplaceAll(text, `\n`, "\n")
			text = strings.ReplaceAll(text, `\t`, "\t")
			return text
		}
	}

	if strings.HasPrefix(content, "TOOL RESULT for ") {
		if colon := strings.Index(content, ": "); colon >= 0 {
			return content[colon+2:]
		}
	}

	return content
}
