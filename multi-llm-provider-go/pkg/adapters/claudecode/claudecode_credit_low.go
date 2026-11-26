package claudecode

import (
	"regexp"
	"strings"
)

// ClaudeCreditLowError means the Claude login this turn ran on has no credit left. Claude prints the sentence as
// its answer, so without this the person saw it as the model's reply. The text is what the person sees.
type ClaudeCreditLowError struct{}

func (*ClaudeCreditLowError) Error() string {
	return "This Claude login is out of credit. Pick another agent, or add credit to it. Your message is saved."
}

// claudeCreditLowLine is Claude's own notice, as its own line (optionally after "API Error:"). A line that merely
// mentions the words, or one the person typed (">" / "❯" prompts are not stripped below), does not count.
var claudeCreditLowLine = regexp.MustCompile(`(?i)^(?:api\s+error:\s*)?credit balance is too low\b`)

// IsClaudeCreditLowText reports whether text contains Claude's "Credit balance is too low" notice as its own line.
func IsClaudeCreditLowText(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if claudeCreditLowLine.MatchString(strings.Trim(line, claudeUsageLimitLineMarkers)) {
			return true
		}
	}
	return false
}
