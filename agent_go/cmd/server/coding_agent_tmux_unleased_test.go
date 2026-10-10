package server

import (
	"strconv"
	"testing"
	"time"
)

func init() { unleasedTmuxSweepEnabled = false }

// A pane whose turn failed before any terminal event has no lease, so the lease-based reaper never closed it.
func TestStaleUnleasedCodingAgentTmuxClosesOnlyIdleUnknownPanes(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	old, recent := now.Add(-3*time.Hour), now.Add(-10*time.Minute)
	sessions := parseTmuxSessionActivity(
		"mlp-muse-schedule-a\t" + itoa(old) + "\t" + itoa(old) + "\t0\n" + // idle, unknown: close
			"mlp-muse-schedule-b\t" + itoa(old) + "\t" + itoa(recent) + "\t0\n" + // recent output: keep
			"mlp-muse-schedule-c\t" + itoa(old) + "\t" + itoa(old) + "\t1\n" + // someone attached: keep
			"mlp-muse-schedule-d\t" + itoa(old) + "\t" + itoa(old) + "\t0\n" + // a lease knows it: the reaper decides
			"my-own-session\t" + itoa(old) + "\t" + itoa(old) + "\t0\n") // not a coding-agent pane: keep
	got := staleUnleasedCodingAgentTmux(sessions, map[string]bool{"mlp-muse-schedule-d": true}, now, time.Hour)
	if len(got) != 1 || got[0] != "mlp-muse-schedule-a" {
		t.Fatalf("closed %v, want only mlp-muse-schedule-a", got)
	}
}

func itoa(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }
