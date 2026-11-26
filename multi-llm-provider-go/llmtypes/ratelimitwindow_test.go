package llmtypes

import (
	"encoding/json"
	"testing"
	"time"
)

// PLAT-101: the reset instant must survive as data. Formatting it away is what
// left the runtime unable to say how long a workflow had to wait.
func TestRateLimitWindowsSurviveOnStatusLine(t *testing.T) {
	reset := time.Date(2026, 8, 18, 23, 30, 0, 0, time.UTC)
	status := &StatusLine{Provider: "claudecode"}
	status.SetRateLimitWindows([]RateLimitWindow{
		{Name: "five_hour", UsedPercent: 42},
		{Name: "seven_day", UsedPercent: 100, ResetsAt: reset},
	})

	got := status.RateLimitWindows()
	if len(got) != 2 {
		t.Fatalf("windows = %d, want 2", len(got))
	}
	if got[1].ResetsAt != reset {
		t.Errorf("resets_at = %v, want %v", got[1].ResetsAt, reset)
	}
	if got[0].Exhausted() {
		t.Error("five_hour at 42%% must not report exhausted")
	}
	if !got[1].Exhausted() {
		t.Error("seven_day at 100%% must report exhausted")
	}
}

// A StatusLine crosses a process boundary between the adapter that fills it and
// the runtime that reads it, so the JSON round trip is the real path.
func TestRateLimitWindowsSurviveJSONRoundTrip(t *testing.T) {
	reset := time.Date(2026, 8, 18, 23, 30, 0, 0, time.UTC)
	status := &StatusLine{}
	status.SetRateLimitWindows([]RateLimitWindow{{Name: "seven_day", UsedPercent: 100, ResetsAt: reset}})

	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var decoded StatusLine
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	got := decoded.RateLimitWindows()
	if len(got) != 1 {
		t.Fatalf("windows after round trip = %d, want 1", len(got))
	}
	if !got[0].ResetsAt.Equal(reset) {
		t.Errorf("resets_at after round trip = %v, want %v", got[0].ResetsAt, reset)
	}
}

// The caller is unblocked by the FIRST exhausted window to reopen; windows that
// are not exhausted, have no reset, or already passed say nothing about the
// current block.
func TestEarliestResetPicksTheSoonestBlockingWindow(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	windows := []RateLimitWindow{
		{Name: "not_exhausted", UsedPercent: 10, ResetsAt: now.Add(time.Minute)},
		{Name: "seven_day", UsedPercent: 100, ResetsAt: now.Add(6 * time.Hour)},
		{Name: "five_hour", UsedPercent: 100, ResetsAt: now.Add(2 * time.Hour)},
		{Name: "stale", UsedPercent: 100, ResetsAt: now.Add(-time.Hour)},
		{Name: "unknown_reset", UsedPercent: 100},
	}
	at, name := EarliestReset(windows, now)
	if name != "five_hour" {
		t.Errorf("window = %q, want five_hour (soonest exhausted future reset)", name)
	}
	if !at.Equal(now.Add(2 * time.Hour)) {
		t.Errorf("reset = %v, want %v", at, now.Add(2*time.Hour))
	}
}

// "Exhausted, reset time unknown" must stay distinguishable from "not
// exhausted": PLAT-101 requires the caller to enter an unknown-capacity state
// rather than invent a timestamp.
func TestEarliestResetReturnsZeroWhenNoReliableTime(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	at, name := EarliestReset([]RateLimitWindow{{Name: "seven_day", UsedPercent: 100}}, now)
	if !at.IsZero() || name != "" {
		t.Errorf("EarliestReset = (%v, %q), want zero time and empty name", at, name)
	}
}

// TestMostConstrainedResetAnswersFromANearlyFullWindow is the case that
// motivated the function. The statusline sidecar is written asynchronously, so
// at the instant the CLI prints a usage-limit line the percentages routinely
// read 99.x rather than a clean 100. EarliestReset only looks at windows at
// 100, so it returns nothing here — and the caller silently falls back to
// reconstructing a timestamp from pane text, which is the exact demotion
// PLAT-101 exists to prevent.
func TestMostConstrainedResetAnswersFromANearlyFullWindow(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	fiveHourReset := now.Add(2 * time.Hour)
	windows := []RateLimitWindow{
		{Name: "five_hour", UsedPercent: 99.6, ResetsAt: fiveHourReset},
		{Name: "seven_day", UsedPercent: 31, ResetsAt: now.Add(72 * time.Hour)},
	}

	if got, _ := EarliestReset(windows, now); !got.IsZero() {
		t.Fatalf("EarliestReset should still ignore a window under 100%%, got %v", got)
	}

	got, name := MostConstrainedReset(windows, now)
	if !got.Equal(fiveHourReset) {
		t.Errorf("reset = %v, want the fullest window's reset %v", got, fiveHourReset)
	}
	if name != "five_hour" {
		t.Errorf("window = %q, want five_hour", name)
	}
}

// TestMostConstrainedResetIgnoresWindowsThatAlreadyReopened pins the one
// exclusion. A reset in the past describes a window that has since reopened,
// so it cannot be what is blocking the caller now — returning it would suspend
// a run until an instant that has already passed, which resumes immediately
// into the same wall.
func TestMostConstrainedResetIgnoresWindowsThatAlreadyReopened(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	future := now.Add(90 * time.Minute)
	windows := []RateLimitWindow{
		{Name: "five_hour", UsedPercent: 100, ResetsAt: now.Add(-10 * time.Minute)},
		{Name: "seven_day", UsedPercent: 40, ResetsAt: future},
	}

	got, name := MostConstrainedReset(windows, now)
	if !got.Equal(future) {
		t.Errorf("reset = %v, want the only window still blocking %v", got, future)
	}
	if name != "seven_day" {
		t.Errorf("window = %q, want seven_day", name)
	}
}

// TestMostConstrainedResetReturnsNothingRatherThanGuess: no stated reset means
// unknown. PLAT-101 requires an explicit unknown-capacity state over a
// fabricated instant, because a wrong instant schedules a resume that fails.
func TestMostConstrainedResetReturnsNothingRatherThanGuess(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	windows := []RateLimitWindow{{Name: "five_hour", UsedPercent: 100}}

	if got, name := MostConstrainedReset(windows, now); !got.IsZero() || name != "" {
		t.Errorf("got (%v, %q), want the zero time and no window", got, name)
	}
}
