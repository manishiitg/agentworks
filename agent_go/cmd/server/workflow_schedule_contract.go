package server

import (
	"fmt"
	"regexp"
	"strings"
)

// Markers that are effectively never ordinary prose: a message containing one
// is quoting a command or a tool call wherever it appears.
var scheduleProcedureSubstrings = []string{
	"sqlite3 ", "curl ", "execute_step(", "notify_user", "backup/",
}

// Bare SQL and shell verbs cannot be told from prose by the verb alone.
// customer-login's reconciliation schedule became unsavable because a message
// read "...update the issue status..." and tripped the marker "update ";
// "git " also matches inside "digit ", and an imperative sentence opens with
// its verb, so anchoring to the start of a line does not separate them either.
//
// What does separate them is shape. SQL is recognized by its clause pairs, and
// a shell command by an actual subcommand — neither of which appears in an
// English instruction by accident.
var scheduleProcedurePatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"a SELECT ... FROM query", regexp.MustCompile(`(?is)\bselect\b.{0,200}?\bfrom\b`)},
	{"an INSERT INTO statement", regexp.MustCompile(`(?i)\binsert\s+into\b`)},
	{"an UPDATE ... SET statement", regexp.MustCompile(`(?is)\bupdate\b.{0,200}?\bset\b`)},
	{"a DELETE FROM statement", regexp.MustCompile(`(?i)\bdelete\s+from\b`)},
	{"a git command", regexp.MustCompile(`(?i)\bgit\s+(add|commit|push|pull|clone|checkout|switch|status|log|diff|rebase|merge|stash|tag|fetch|reset|revert|rm|mv|branch|show)\b`)},
	{"numbered procedure steps", regexp.MustCompile(`(?im)^[\s>*+\-–—]*(?:\d+[.)]|step\s+\d)`)},
}

// scheduleMessagesNeedExplicitReason distinguishes a compact trigger from a
// schedule-local procedure. Direct procedures are supported, but unlike planned
// routes they do not automatically receive the canonical step learning,
// validation, retry, and Pulse-attribution lifecycle. Requiring an explicit
// reason makes that architecture choice visible instead of silently banning it.
func scheduleMessagesNeedExplicitReason(messages []string) (bool, string) {
	nonEmptyCount := 0
	for index, raw := range messages {
		message := strings.TrimSpace(raw)
		if message == "" {
			continue
		}
		nonEmptyCount++
		if len(message) > 1600 {
			return true, fmt.Sprintf("messages[%d] is %d characters", index, len(message))
		}
		lower := strings.ToLower(message)
		for _, marker := range scheduleProcedureSubstrings {
			if strings.Contains(lower, marker) {
				return true, fmt.Sprintf("messages[%d] contains procedure marker %q", index, marker)
			}
		}
		for _, candidate := range scheduleProcedurePatterns {
			if candidate.pattern.MatchString(message) {
				return true, fmt.Sprintf("messages[%d] contains %s", index, candidate.name)
			}
		}
	}
	if nonEmptyCount > 1 {
		return true, fmt.Sprintf("schedule has %d sequential workshop messages", nonEmptyCount)
	}
	return false, ""
}

func validateScheduleMessages(messages []string, directMessagesReason string) error {
	needsReason, signal := scheduleMessagesNeedExplicitReason(messages)
	if needsReason && strings.TrimSpace(directMessagesReason) == "" {
		return fmt.Errorf("direct schedule messages are supported, but this queue needs direct_messages_reason because %s; explain why this work is genuinely schedule-specific and acknowledge that it lacks canonical step-level learnings, validation/retry, and Pulse attribution, or move the durable procedure into a planned route", signal)
	}
	return nil
}

func scheduleMessagesAdvisory(messages []string, directMessagesReason string) string {
	needsReason, _ := scheduleMessagesNeedExplicitReason(messages)
	if !needsReason {
		return ""
	}
	return fmt.Sprintf("Direct schedule sequence retained intentionally (%s). It runs as workshop conversation turns, not canonical plan steps, so step-level learnings, validation/retry, and Pulse attribution are not automatic.", strings.TrimSpace(directMessagesReason))
}
