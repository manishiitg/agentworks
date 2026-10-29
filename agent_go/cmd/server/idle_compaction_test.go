package server

import (
	"testing"
	"time"
)

// PLAT-872: a long chat a person left about 50 minutes ago is compacted once,
// while the provider cache is warm; anything else is skipped with its reason.
func TestIdleCompactDecide(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	cfg := idleCompactConfig{MinTokens: 200_000, MinPercent: 60, IdleFrom: 50 * time.Minute, IdleTo: 58 * time.Minute, PersonWithin: 3 * time.Hour}
	ready := func(change func(*idleCompactCandidate)) idleCompactCandidate {
		c := idleCompactCandidate{Interactive: true, Supported: true, HasHandle: true,
			LastActivity: now.Add(-52 * time.Minute), LastPerson: now.Add(-60 * time.Minute), ContextTokens: 310_000}
		if change != nil {
			change(&c)
		}
		return c
	}
	cases := []struct {
		name      string
		candidate idleCompactCandidate
		inWindow  bool
		compact   bool
		reason    string
	}{
		{"long chat idle 52 min", ready(nil), true, true, "idle_cache_warm"},
		{"small chat at 60% of its window", ready(func(c *idleCompactCandidate) { c.ContextTokens, c.ContextWindow = 150_000, 250_000 }), true, true, "idle_cache_warm"},
		{"small chat", ready(func(c *idleCompactCandidate) { c.ContextTokens, c.ContextWindow = 150_000, 1_000_000 }), true, false, "small_context"},
		{"still within 50 min", ready(func(c *idleCompactCandidate) { c.LastActivity = now.Add(-30 * time.Minute) }), false, false, "outside_idle_window"},
		{"cache likely expired", ready(func(c *idleCompactCandidate) { c.LastActivity = now.Add(-70 * time.Minute) }), false, false, "outside_idle_window"},
		{"no person turn in 3 h", ready(func(c *idleCompactCandidate) { c.LastPerson = now.Add(-4 * time.Hour) }), true, false, "no_recent_person_turn"},
		{"already compacted since the person's turn", ready(func(c *idleCompactCandidate) { c.AttemptedAt = now.Add(-5 * time.Minute) }), true, false, "already_compacted"},
		{"compacted before the latest person turn", ready(func(c *idleCompactCandidate) { c.AttemptedAt = now.Add(-2 * time.Hour) }), true, true, "idle_cache_warm"},
		{"background work running", ready(func(c *idleCompactCandidate) { c.BackgroundWork = true }), true, false, "background_work"},
		{"queued message", ready(func(c *idleCompactCandidate) { c.Queued = true }), true, false, "queued_input"},
		{"schedule or function session", ready(func(c *idleCompactCandidate) { c.Interactive = false }), true, false, "not_interactive"},
		{"provider without a native trigger", ready(func(c *idleCompactCandidate) { c.Supported = false }), true, false, "unsupported"},
		{"owner at token limit", ready(func(c *idleCompactCandidate) { c.LimitReached = true }), true, false, "limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inWindow, compact, reason := idleCompactDecide(tc.candidate, cfg, now)
			if inWindow != tc.inWindow || compact != tc.compact || reason != tc.reason {
				t.Fatalf("got in_window=%t compact=%t reason=%s, want %t %t %s", inWindow, compact, reason, tc.inWindow, tc.compact, tc.reason)
			}
		})
	}
}
