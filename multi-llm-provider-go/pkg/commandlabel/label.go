package commandlabel

import (
	"regexp"
	"strings"
)

const limit = 120

var inlineAssignment = regexp.MustCompile(`^(?:export\s+)?[A-Za-z_][A-Za-z0-9_]*=(?:'[^']*'|"[^"]*"|\S*)\s+`)

// Label is the short command shown to the person: the
// first line, without leading NAME=value assignments (they can carry values
// that must never be shown), trimmed to a readable length.
func Label(command string) string {
	command = strings.TrimSpace(strings.SplitN(strings.TrimSpace(command), "\n", 2)[0])
	for {
		trimmed := inlineAssignment.ReplaceAllString(command, "")
		if trimmed == command {
			break
		}
		command = strings.TrimSpace(trimmed)
	}
	if inlineAssignment.MatchString(command + " ") {
		return ""
	}
	if runes := []rune(command); len(runes) > limit {
		command = strings.TrimSpace(string(runes[:limit-1])) + "…"
	}
	return command
}
