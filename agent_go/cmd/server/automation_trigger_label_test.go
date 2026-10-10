package server

import "testing"

func TestAutomationTriggerLabel(t *testing.T) {
	cases := map[[2]string]string{
		{"webhook", "Called by project-a Flow Tester"}: "Called by project-a Flow Tester",
		{"webhook", "GitHub push"}:                     "Webhook: GitHub push",
		{"cron", "Daily digest"}:                       "Schedule: Daily digest",
		{"cron", ""}:                                   "Schedule",
	}
	for in, want := range cases {
		if got := automationTriggerLabel(in[0], in[1]); got != want {
			t.Errorf("automationTriggerLabel(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}
