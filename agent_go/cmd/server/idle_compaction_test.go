package server

import (
	"context"
	"testing"
	"time"
)

// PLAT-872: a long chat a person left about 50 minutes ago is compacted once,
// while the provider cache is warm; anything else is skipped with its reason.
func TestIdleCompactDecide(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	cfg := idleCompactConfig{MinTokens: 200_000, MinPercent: 60, IdleFrom: 50 * time.Minute, IdleTo: 58 * time.Minute, LongIdle: 3 * time.Hour, PersonWithin: 6 * time.Hour}
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
		{"no person turn in 6 h", ready(func(c *idleCompactCandidate) { c.LastPerson = now.Add(-7 * time.Hour) }), true, false, "no_recent_person_turn"},
		{"left for the night: compacted once after 3 h", ready(func(c *idleCompactCandidate) {
			c.LastActivity, c.LastPerson = now.Add(-182*time.Minute), now.Add(-190*time.Minute)
		}), true, true, "idle_long_break"},
		{"long break already compacted in the warm window", ready(func(c *idleCompactCandidate) {
			c.LastActivity, c.LastPerson, c.AttemptedAt = now.Add(-182*time.Minute), now.Add(-190*time.Minute), now.Add(-130*time.Minute)
		}), true, false, "already_compacted"},
		{"already compacted since the person's turn", ready(func(c *idleCompactCandidate) { c.AttemptedAt = now.Add(-5 * time.Minute) }), true, false, "already_compacted"},
		{"compacted before the latest person turn", ready(func(c *idleCompactCandidate) { c.AttemptedAt = now.Add(-2 * time.Hour) }), true, true, "idle_cache_warm"},
		{"background work running", ready(func(c *idleCompactCandidate) { c.BackgroundWork = true }), true, false, "background_work"},
		{"queued message", ready(func(c *idleCompactCandidate) { c.Queued = true }), true, false, "queued_input"},
		{"schedule or function session", ready(func(c *idleCompactCandidate) { c.Interactive = false }), true, false, "not_interactive"},
		{"provider without a native trigger", ready(func(c *idleCompactCandidate) { c.Supported = false }), true, false, "unsupported"},
		{"owner at token limit", ready(func(c *idleCompactCandidate) { c.LimitReached = true }), true, false, "limit"},
		{"tab owner turned it off in the model menu (PLAT-872-71)", ready(func(c *idleCompactCandidate) { c.TabOff = true }), true, false, "disabled"},
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

// An idle compaction carries the last person request only to build the agent.
// If it were queued, the drainer would run that request again as a real turn.
func TestIdleCompactionTurnIsNeverQueued(t *testing.T) {
	ctx := context.WithValue(context.Background(), idleCompactRunKey{}, &idleCompactRun{})
	if _, _, err := (&StreamingAPI{}).enqueueConversationTurn(ctx, "user-1", "session-1", QueryRequest{Query: "last person message"}); err == nil {
		t.Fatal("an idle compaction turn was accepted into the conversation queue")
	}
}

// PLAT-746-880: a waiting message caused early compaction to reject its own
// dispatch reservation, then (after taking the lane) reject the same backlog.
func TestQueuedCompactionDoesNotRefuseItsOwnReservationOrBacklog(t *testing.T) {
	const sessionID = "queued-compact-regression"
	api := newConversationTurnQueueTestAPI(map[string]string{})
	ctx := context.Background()
	if _, _, err := api.enqueueConversationTurn(ctx, "person-1", sessionID, QueryRequest{Query: "next message", AgentMode: "multi-agent"}); err != nil {
		t.Fatal(err)
	}
	api.conversationTurnQueueDraining[sessionID] = true
	run := newIdleCompactRun()
	run.beforeQueued = true
	if !api.reserveQueuedCompaction(sessionID, run) {
		t.Fatal("could not reserve early compaction")
	}
	t.Cleanup(func() {
		idleCompacts.end(sessionID, run)
		compactBeforeQueuedAttempts.Delete(sessionID)
	})
	if by := api.conversationTurnOccupiedBy(sessionID); by != "" {
		t.Fatalf("compaction would refuse its own reservation: %s", by)
	}
	// The registered compaction still holds the waiting message, even though
	// the dispatcher reservation is released.
	api.kickConversationTurnQueue(sessionID)
	if !api.conversationTurnHasBacklog("person-1", sessionID) || api.conversationTurnDispatchPending(sessionID) {
		t.Fatal("queued input must stay waiting behind the reserved compaction")
	}
	release, ok := api.tryLockSessionInputLane(sessionID, 0)
	if !ok {
		t.Fatal("early compaction could not take the free input lane")
	}
	defer release() // cleanup runs after this; compaction still holds the queue
	compactCtx := context.WithValue(ctx, idleCompactRunKey{}, run)
	if shouldYieldToConversationBacklog(compactCtx) {
		t.Fatal("early compaction would reject the waiting message backlog after taking the lane")
	}
	if !shouldYieldToConversationBacklog(ctx) || !shouldYieldToConversationBacklog(context.WithValue(ctx, idleCompactRunKey{}, newIdleCompactRun())) {
		t.Fatal("ordinary input and idle-sweep compaction must still yield to waiting messages")
	}
	if !api.conversationTurnOccupied(sessionID) {
		t.Fatal("the active compaction lane must still exclude competing turns")
	}
}

// PLAT-746-880: a refused compaction used to erase its cooldown, so each
// queue wake immediately tried again while the current reply was still running.
func TestRefusedQueuedCompactionRetainsCooldownWhileTurnRuns(t *testing.T) {
	const session = "compact-refusal-cooldown"
	api := newConversationTurnQueueTestAPI(map[string]string{})
	release, ok := api.tryLockSessionInputLane(session, 0)
	if !ok {
		t.Fatal("could not occupy input lane")
	}
	defer release()
	run := newIdleCompactRun()
	run.beforeQueued = true
	if !api.reserveQueuedCompaction(session, run) {
		t.Fatal("could not reserve racing compaction")
	}
	t.Cleanup(func() { idleCompacts.end(session, run); compactBeforeQueuedAttempts.Delete(session) })
	api.runIdleCompaction(session, "person-1", idleCompactChat{personReq: QueryRequest{Query: "last message", AgentMode: "multi-agent"}}, 310000, 0, run)
	if _, ok := compactBeforeQueuedAttempts.Load(session); !ok {
		t.Fatal("refused attempt erased its cooldown")
	}
	attempts := 1
	for i := 0; i < 1000; i++ {
		if api.compactBeforeQueuedTurn(session) {
			attempts++
		}
	}
	if attempts != 1 {
		t.Fatalf("running turn caused %d compaction attempts", attempts)
	}
}
