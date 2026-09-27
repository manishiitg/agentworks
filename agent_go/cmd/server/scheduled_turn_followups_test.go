package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func withFastFollowUps(t *testing.T) {
	t.Helper()
	old := scheduledFollowUpPollInterval
	scheduledFollowUpPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { scheduledFollowUpPollInterval = old })
}

// startFakeStep registers a step run the way execute_step does and finishes
// it after d, like a real step owning its own completion.
func startFakeStep(api *StreamingAPI, sessionID, name string, d time.Duration) *BackgroundAgent {
	agent := &BackgroundAgent{ID: name, Name: name, SessionID: sessionID, Status: BGAgentRunning, CreatedAt: time.Now()}
	api.bgAgentRegistry.Register(sessionID, agent)
	go func() {
		time.Sleep(d)
		agent.mu.Lock()
		agent.Status = BGAgentCompleted
		agent.Result = name + " done"
		agent.mu.Unlock()
	}()
	return agent
}

// The salesoutreach schedule: the agent starts one group's step per turn and
// waits for its result before starting the next. The scheduler must drive all
// 12 groups, handing each result back as the next turn, and finish only when
// a turn starts nothing new. It used to stop after group 1.
func TestScheduledRunDrivesEveryGroupToCompletion(t *testing.T) {
	withFastFollowUps(t)
	api := &StreamingAPI{bgAgentRegistry: NewBackgroundAgentRegistry()}
	session := "schedule-cron--688df853_test"
	release := api.claimSessionCompletions(session)
	defer release()
	since := time.Now().Add(-time.Second)

	groups := []string{"usa-engineering-ops", "usa-gtm-ops", "usa-shopify-ops", "usa-growth-analytics",
		"dubai-engineering-ops", "dubai-gtm-ops", "dubai-shopify-ops", "dubai-growth-analytics",
		"india-engineering-ops", "india-gtm-ops", "india-shopify-ops", "india-growth-analytics"}

	// The scheduled message's own turn starts group 1.
	startFakeStep(api, session, "send "+groups[0], 30*time.Millisecond)
	next := 1

	var mu sync.Mutex
	var turns []string
	send := func(ctx context.Context, query string) error {
		mu.Lock()
		turns = append(turns, query)
		mu.Unlock()
		// The agent reads the result and starts the next group, or nothing.
		if next < len(groups) {
			startFakeStep(api, session, "send "+groups[next], 20*time.Millisecond)
			next++
		}
		return nil
	}

	rounds, err := api.runScheduledFollowUps(context.Background(), session, since, time.Minute, send)
	if err != nil {
		t.Fatalf("follow-ups failed: %v", err)
	}
	if next != len(groups) {
		t.Fatalf("only %d of %d groups started", next, len(groups))
	}
	if rounds != len(groups) {
		t.Fatalf("want one result turn per group (%d), got %d", len(groups), rounds)
	}
	for i, q := range turns {
		if !strings.Contains(q, "send "+groups[i]+" done") {
			t.Fatalf("turn %d did not carry group %s's result: %s", i+1, groups[i], q)
		}
	}
	for _, agent := range api.bgAgentRegistry.GetAll(session) {
		if !agent.GetSnapshot().CompletionNotified {
			t.Fatalf("%s's result was never recorded as delivered", agent.ID)
		}
	}
}

// While the run owns the session, the notification path must not deliver
// the same result as a competing turn.
func TestOwnedSessionHoldsBackTheNotificationPath(t *testing.T) {
	api := &StreamingAPI{bgAgentRegistry: NewBackgroundAgentRegistry()}
	session := "schedule-cron--test"
	agent := &BackgroundAgent{ID: "step", SessionID: session, Status: BGAgentCompleted, CreatedAt: time.Now().Add(-time.Minute)}
	api.bgAgentRegistry.Register(session, agent)

	release := api.claimSessionCompletions(session)
	if !api.deferWorkflowStepAutoNotification(session, "step") {
		t.Fatal("an owned session's completion must be left for its owner")
	}
	release()
	release() // releasing twice must not unbalance the claim
	if api.sessionCompletionsOwned(session) {
		t.Fatal("the claim must end when released")
	}
}

// A turn that starts nothing leaves nothing to hand back, and a resumed
// thread's older, never-delivered runs are not this run's work.
func TestScheduledRunEndsWhenATurnStartsNothing(t *testing.T) {
	withFastFollowUps(t)
	api := &StreamingAPI{bgAgentRegistry: NewBackgroundAgentRegistry()}
	session := "schedule-cron--test"
	old := &BackgroundAgent{ID: "yesterday", SessionID: session, Status: BGAgentCompleted, CreatedAt: time.Now().Add(-24 * time.Hour)}
	api.bgAgentRegistry.Register(session, old)

	calls := 0
	rounds, err := api.runScheduledFollowUps(context.Background(), session, time.Now().Add(-time.Second), time.Minute, func(context.Context, string) error {
		calls++
		return nil
	})
	if err != nil || rounds != 0 || calls != 0 {
		t.Fatalf("want no result turns, got rounds=%d calls=%d err=%v", rounds, calls, err)
	}
}

// A failed result turn returns the result to "not delivered", so it is not
// lost, and the error stops the run.
func TestScheduledRunKeepsResultsWhenTheTurnFails(t *testing.T) {
	withFastFollowUps(t)
	api := &StreamingAPI{bgAgentRegistry: NewBackgroundAgentRegistry()}
	session := "schedule-cron--test"
	startFakeStep(api, session, "send group-1", 10*time.Millisecond)
	_, err := api.runScheduledFollowUps(context.Background(), session, time.Now().Add(-time.Second), time.Minute, func(context.Context, string) error {
		return fmt.Errorf("provider down")
	})
	if err == nil {
		t.Fatal("a failed result turn must fail the run")
	}
	if api.bgAgentRegistry.Get(session, "send group-1").GetSnapshot().CompletionNotified {
		t.Fatal("an undelivered result must not be marked delivered")
	}
}

// A step still running past the ceiling stops the run with a clear error
// instead of hanging it.
func TestScheduledRunStopsAtTheCeiling(t *testing.T) {
	withFastFollowUps(t)
	api := &StreamingAPI{bgAgentRegistry: NewBackgroundAgentRegistry()}
	session := "schedule-cron--test"
	startFakeStep(api, session, "stuck", time.Hour)
	_, err := api.runScheduledFollowUps(context.Background(), session, time.Now().Add(-time.Second), 100*time.Millisecond, func(context.Context, string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("want a still-running error, got %v", err)
	}
}

// Completions deferred while a session was claimed and not taken by the owner
// are delivered when the last claim is released, not lost.
func TestReleasingTheLastClaimRedeliversUntakenCompletions(t *testing.T) {
	api := &StreamingAPI{bgAgentRegistry: NewBackgroundAgentRegistry()}
	const session = "schedule-cron--redeliver"
	ch := api.bgAgentRegistry.GetNotificationChannel(session)
	finished := func(id string, metadata map[string]string) *BackgroundAgent {
		agent := &BackgroundAgent{ID: id, SessionID: session, Status: BGAgentCompleted, CreatedAt: time.Now(), Metadata: metadata}
		api.bgAgentRegistry.Register(session, agent)
		return agent
	}
	finished("bg-untaken", nil)
	finished("bg-suppressed", map[string]string{"suppress_auto_notification": "true"})
	taken := finished("bg-taken", nil)
	taken.completionNotification = completionNotificationDelivered
	api.bgAgentRegistry.Register(session, &BackgroundAgent{ID: "bg-running", SessionID: session, Status: BGAgentRunning, CreatedAt: time.Now()})

	outer := api.claimSessionCompletions(session)
	inner := api.claimSessionCompletions(session)
	inner()
	inner() // a second call is a no-op
	select {
	case id := <-ch:
		t.Fatalf("delivered %s while the session is still claimed", id)
	default:
	}
	outer()
	var got []string
	for {
		select {
		case id := <-ch:
			got = append(got, id)
			continue
		default:
		}
		break
	}
	if len(got) != 1 || got[0] != "bg-untaken" {
		t.Fatalf("want only the untaken finished completion redelivered, got %v", got)
	}
}
