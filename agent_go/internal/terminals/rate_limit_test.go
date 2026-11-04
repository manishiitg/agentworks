package terminals

import (
	"strings"
	"testing"
)

func TestDetectRateLimitClaudeSessionLimit(t *testing.T) {
	limited := []string{
		"  X  You've hit your session limit · resets 1:30pm (Asia/Calcutta)\n     /usage-credits to finish what you're working on.",
		"You've hit your usage limit",
		"5-hour limit reached",
		"Your usage limit will reset at 3pm",
		"You have run out of credits",
		"   /usage-credits to finish what you're working on.",
		"rate limit exceeded",
		"RESOURCE_EXHAUSTED",
		"you have reached your usage limit",
	}
	for _, content := range limited {
		if !DetectRateLimit(content) {
			t.Errorf("expected rate-limit detection for:\n%q", content)
		}
	}

	for _, content := range []string{
		"",
		"Working on the task...",
		"> ",
		"Allocator done; the Opportunity Scanner is running.",
		"Reading the plan.json to apply the scoped fixes.",
	} {
		if DetectRateLimit(content) {
			t.Errorf("false positive on benign pane: %q", content)
		}
	}
}

// Status is derived on every streamed chunk; matching the whole transcript each time took most of a server's CPU
// (4 Nov). Only the end is read: a limit at the end of a long transcript is still seen, and a pattern that needs
// no "limit" word ("tokens per minute exceeded") passes the keyword check.
func TestDetectRateLimitReadsTheEndOfALongTranscript(t *testing.T) {
	long := strings.Repeat("Working on the task, reading files and running tests.\n", 5000)
	if !DetectRateLimit(long + "You've hit your session limit · resets 1:30pm") {
		t.Fatal("a limit at the end of a long transcript was missed")
	}
	if DetectRateLimit("rate limit exceeded earlier\n" + long) {
		t.Fatal("an old limit line far above the latest output still counted")
	}
	if !DetectRateLimit("tokens per minute exceeded") {
		t.Fatal("tokens per minute exceeded was missed")
	}
}
