package step_based_workflow

import (
	"testing"
	"time"
)

// PLAT-635: agents get UTC, not bare server-local time.
func TestPromptClockIsUTCWithLocalAlongside(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+1800)
	date, clock := promptClock(time.Date(2026, 10, 8, 2, 15, 0, 0, ist))
	if date != "2026-10-07" || clock != "20:45:00 UTC (server local 2026-10-08 02:15 IST)" {
		t.Fatalf("got %q %q", date, clock)
	}
	if _, clock := promptClock(time.Date(2026, 10, 7, 20, 45, 0, 0, time.UTC)); clock != "20:45:00 UTC" {
		t.Fatalf("UTC server: %q", clock)
	}
}
