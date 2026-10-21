package server

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// The schedule half of /api/header-summary resolved every schedule's runtime
// state (reconcile + two schedule-runs.json reads + a durable-run lookup) on
// each 5s poll from every open tab. Its answer only changes when a scheduled
// run starts or finishes, or a manifest changes; those paths bump the
// generation below. The safety TTL bounds drift from anything unhooked.
const scheduleSummarySafetyTTL = 30 * time.Second

var scheduleSummaryGeneration atomic.Uint64

func noteScheduleSummaryChange() { scheduleSummaryGeneration.Add(1) }

type scheduleSummaryCache struct {
	mu         sync.Mutex // guards the fields below; never held while computing
	computeMu  sync.Mutex // one recomputation at a time
	value      WorkflowScheduleSummary
	gen        uint64
	at         time.Time
	ok         bool
	refreshing bool
}

var workflowScheduleSummaryCache scheduleSummaryCache

func (c *scheduleSummaryCache) freshLocked() bool {
	return c.ok && c.gen == scheduleSummaryGeneration.Load() && time.Since(c.at) < scheduleSummarySafetyTTL
}

// CachedWorkflowScheduleSummary returns an up-to-date summary. Concurrent callers share one recomputation instead of each
// paying for it. The live feed uses this: it must see a change as soon as it happens.
func (svc *SchedulerService) CachedWorkflowScheduleSummary(ctx context.Context) (WorkflowScheduleSummary, error) {
	c := &workflowScheduleSummaryCache
	c.mu.Lock()
	if c.freshLocked() {
		value := c.value
		c.mu.Unlock()
		return value, nil
	}
	c.mu.Unlock()

	c.computeMu.Lock()
	defer c.computeMu.Unlock()
	c.mu.Lock()
	if c.freshLocked() { // another caller finished while this one waited
		value := c.value
		c.mu.Unlock()
		return value, nil
	}
	c.mu.Unlock()

	gen := scheduleSummaryGeneration.Load()
	summary, err := svc.SummarizeWorkflowSchedules(ctx)
	if err != nil {
		return WorkflowScheduleSummary{}, err
	}
	c.mu.Lock()
	c.value, c.gen, c.at, c.ok = summary, gen, time.Now(), true
	c.mu.Unlock()
	return summary, nil
}

// LatestWorkflowScheduleSummary is for the header poll that every open tab sends every few seconds. It answers at once
// from the last good summary and refreshes a stale one in the background, so a slow recomputation (several seconds on a
// busy server) is never waited on by every tab. Only the very first call computes inline.
func (svc *SchedulerService) LatestWorkflowScheduleSummary(ctx context.Context) (WorkflowScheduleSummary, error) {
	c := &workflowScheduleSummaryCache
	c.mu.Lock()
	if !c.ok {
		c.mu.Unlock()
		return svc.CachedWorkflowScheduleSummary(ctx)
	}
	value := c.value
	if !c.freshLocked() && !c.refreshing {
		c.refreshing = true
		go func() {
			_, _ = svc.CachedWorkflowScheduleSummary(context.Background())
			c.mu.Lock()
			c.refreshing = false
			c.mu.Unlock()
		}()
	}
	c.mu.Unlock()
	return value, nil
}
