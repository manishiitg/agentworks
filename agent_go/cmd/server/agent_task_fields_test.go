package server

import "testing"

// A task shown under its issue number must not repeat it in its title ("#3018 #3018 Fix…").
func TestAgentTaskTitleDropsItsOwnIssueNumber(t *testing.T) {
	for _, c := range []struct{ ref, message, want string }{
		{"", "#3018 Remove the duplicate name beside the tab.", "Remove the duplicate name beside the tab."},
		{"", "#3018: Remove the duplicate name", "Remove the duplicate name"},
		{"", "Fix #3018 in the Team tab", "Fix #3018 in the Team tab"},
		{"#3018", "#30180 is a different issue", "#30180 is a different issue"},
	} {
		if got := agentTaskTitle(agentTaskFields{Ref: c.ref}, c.message); got != c.want {
			t.Errorf("%q: got %q, want %q", c.message, got, c.want)
		}
	}
}
