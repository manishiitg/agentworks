package server

import (
	"strings"
	"testing"
)

// The daily digest is one email of the day's runs that names failures in its
// subject, and nothing at all on a day with no runs (owner, 2026-10-24).
func TestDailyDigestArgsSummarisesTheDayAndSkipsEmptyDays(t *testing.T) {
	if args := dailyDigestArgs("Sales outreach", "2026-10-24", nil); args != nil {
		t.Fatalf("no runs must send nothing, got %v", args)
	}
	args := dailyDigestArgs("Sales outreach", "2026-10-24", []dailyDigestRun{
		{title: "Sales outreach run finished", status: "completed", message: "Sales outreach run finished."},
		{title: "Sales outreach run failed", status: "failed", message: "Apollo path closed"},
	})
	if got := args["email_subject"]; got != "Sales outreach today: 2 runs, 1 failed or blocked" {
		t.Fatalf("subject = %v", got)
	}
	if args["delivery_mode"] != "external_only" || args["summary_status"] != "failed" {
		t.Fatalf("digest must go to channels only and read failed: %v", args)
	}
	if msg := args["message_for_user"].(string); !strings.Contains(msg, "- Sales outreach run failed: Apollo path closed") {
		t.Fatalf("message lacks the failed run line: %q", msg)
	}
}
