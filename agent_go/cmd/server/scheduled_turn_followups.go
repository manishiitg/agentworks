package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// A scheduled run owns its session the way a step owns its sub-agents
// (reconcileAsyncSubAgentCalls): after each agent turn it waits until every
// step that turn started reports itself done, hands the agent their results as
// its next turn, and repeats until a turn starts nothing new. Only then is the
// scheduled message's work complete.
//
// Before this, the scheduler treated "the agent's turn ended" as "the work is
// done". An agent looping over groups with execute_step ends its turn after
// starting each step, to wait for the result, so the scheduler closed the run
// and started the Pulse finalizer in the same session; the finalizer won the
// race for the session and the step's completion notice landed in its turn.
// salesoutreach's email and LinkedIn schedules ran 1 of 12 groups every day
// from Sep 19 to Sep 26 because of it.
//
// While a run owns the session, the auto-notification path leaves completions
// undelivered (deferWorkflowStepAutoNotification) so there is exactly one
// deliverer and nothing to race.

// maxScheduledFollowUpRounds bounds one scheduled message. 12 groups x 2 steps
// run one at a time is 24 rounds.
const maxScheduledFollowUpRounds = 100

var scheduledFollowUpPollInterval = 500 * time.Millisecond

// claimSessionCompletions makes the caller the only deliverer of step
// completions for the session until the returned release is called.
func (api *StreamingAPI) claimSessionCompletions(sessionID string) (release func()) {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return func() {}
	}
	value, _ := api.completionOwners.LoadOrStore(sessionID, new(int32))
	count := value.(*int32)
	atomic.AddInt32(count, 1)
	var once int32
	return func() {
		if atomic.CompareAndSwapInt32(&once, 0, 1) && atomic.AddInt32(count, -1) == 0 {
			api.redeliverUnclaimedCompletions(sessionID)
		}
	}
}

// redeliverUnclaimedCompletions runs when the last claim on a session is
// released. Completions deferred while it was claimed that the owner did not
// take (steps from an earlier turn, other agents that finished during a direct
// reviewer run, anything left when the owner stopped early) go through normal
// delivery now instead of being lost.
func (api *StreamingAPI) redeliverUnclaimedCompletions(sessionID string) {
	if api.bgAgentRegistry == nil {
		return
	}
	for _, agent := range api.bgAgentRegistry.GetAll(sessionID) {
		snap := agent.GetSnapshot()
		if snap.CompletionNotified || (snap.Status != BGAgentCompleted && snap.Status != BGAgentFailed) {
			continue
		}
		if completionAutoNotificationSuppressed(snap.Metadata) {
			continue
		}
		api.bgAgentRegistry.NotifyCompletion(sessionID, snap.ID)
	}
}

func (api *StreamingAPI) sessionCompletionsOwned(sessionID string) bool {
	if api == nil {
		return false
	}
	value, ok := api.completionOwners.Load(sessionID)
	return ok && atomic.LoadInt32(value.(*int32)) > 0
}

// scheduledStepResult is one finished step handed back to the agent.
type scheduledStepResult struct {
	agent *BackgroundAgent
	snap  BackgroundAgentSnapshot
}

// waitAndClaimScheduledStepResults waits until no step started at or after
// since is still running, then claims every finished one whose result has not
// reached the agent. An empty result means the last turn started nothing new.
func (api *StreamingAPI) waitAndClaimScheduledStepResults(ctx context.Context, sessionID string, since time.Time, ceiling time.Duration) ([]scheduledStepResult, error) {
	started := time.Now()
	ticker := time.NewTicker(scheduledFollowUpPollInterval)
	defer ticker.Stop()
	for {
		running := 0
		var agents []*BackgroundAgent
		if api.bgAgentRegistry != nil {
			for _, agent := range api.bgAgentRegistry.GetAll(sessionID) {
				if agent == nil {
					continue
				}
				snap := agent.GetSnapshot()
				// A resumed thread's earlier runs are not this run's work.
				if (!since.IsZero() && snap.CreatedAt.Before(since)) || completionAutoNotificationSuppressed(snap.Metadata) {
					continue
				}
				if snap.Status == BGAgentRunning {
					running++
					continue
				}
				agents = append(agents, agent)
			}
		}
		if running == 0 {
			var claimed []scheduledStepResult
			for _, agent := range agents {
				if agent.beginCompletionNotification() {
					claimed = append(claimed, scheduledStepResult{agent: agent, snap: agent.GetSnapshot()})
				}
			}
			return claimed, nil
		}
		if ceiling > 0 && time.Since(started) >= ceiling {
			return nil, fmt.Errorf("%d step run(s) still running after %s", running, ceiling)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// formatScheduledStepResults is the agent's next turn: the authoritative
// results of every step its previous turn started.
func formatScheduledStepResults(sessionID string, results []scheduledStepResult) string {
	type entry struct {
		Step   string `json:"step"`
		Status string `json:"status"`
		Result string `json:"result,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	entries := make([]entry, 0, len(results))
	for _, r := range results {
		e := entry{Step: r.snap.Name, Status: string(r.snap.Status), Error: strings.TrimSpace(r.snap.Error)}
		if r.snap.Status == BGAgentCompleted {
			e.Result = strings.TrimSpace(compactScheduledAutoNotificationResult(sessionID, r.snap, r.snap.Result))
		}
		entries = append(entries, e)
	}
	encoded, _ := json.MarshalIndent(entries, "", "  ")
	return fmt.Sprintf(`[AUTO-NOTIFICATION] The scheduler waited for every step you started in your previous turn. These are their final results:

%s

Continue the scheduled task now. Start the next steps it needs, one turn at a time as before; the scheduler will wait for them and bring you their results. When nothing more is needed, give your final summary and start nothing new. Handle failures explicitly.`, string(encoded))
}

// runScheduledFollowUps drives the step-results loop after one scheduled
// message turn. send delivers a turn to the session and waits for it.
func (api *StreamingAPI) runScheduledFollowUps(ctx context.Context, sessionID string, since time.Time, ceiling time.Duration, send func(ctx context.Context, query string) error) (int, error) {
	for round := 1; round <= maxScheduledFollowUpRounds; round++ {
		results, err := api.waitAndClaimScheduledStepResults(ctx, sessionID, since, ceiling)
		if err != nil {
			return round - 1, err
		}
		if len(results) == 0 {
			return round - 1, nil
		}
		sendErr := send(ctx, formatScheduledStepResults(sessionID, results))
		for _, r := range results {
			r.agent.finishCompletionNotification(sendErr == nil)
		}
		if sendErr != nil {
			return round, sendErr
		}
	}
	return maxScheduledFollowUpRounds, fmt.Errorf("stopped after %d step-result rounds", maxScheduledFollowUpRounds)
}
