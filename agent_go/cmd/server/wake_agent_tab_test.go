package server

import "testing"

// Only a run that ended badly wakes the Crew tab that looks after it (PLAT-825-94):
// a failure, a partial run, or a "success" in which no workflow actually ran.
// Routine good runs, a run the owner stopped and a quota wait must stay quiet.
func TestWakeReasonForRunOnlyWakesForBadRuns(t *testing.T) {
	cases := []struct {
		status   string
		evidence bool
		wake     bool
	}{
		{"error", true, true},
		{"failed", true, true},
		{"blocked", false, true},
		{"no_run", false, true},
		{"waiting_for_platform", false, true},
		{"completed", true, false},
		{"partial", true, true},
		{"success", false, true},
		{"success", true, false},
		{"stopped", false, false},
		{"interrupted", false, false},
		{scheduleRunStatusWaitingForCapacity, false, false},
	}
	for _, c := range cases {
		if got := wakeReasonForRun(c.status, c.evidence) != ""; got != c.wake {
			t.Errorf("status %q evidence %v: wake = %v, want %v", c.status, c.evidence, got, c.wake)
		}
	}
}
