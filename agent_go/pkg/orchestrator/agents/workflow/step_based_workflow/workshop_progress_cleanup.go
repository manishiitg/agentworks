package step_based_workflow

import (
	"context"
	"fmt"
	"sync"
)

// A progress mirror has no worker of its own. Keep its retry guard until the
// full workflow AND every async worker it owned have actually returned. The
// existing bounded cancellation wait can time out while a worker is unwinding.
type workshopSubAgentCleanup struct {
	mu       sync.Mutex
	contexts map[*SubAgentExecutionContext]struct{}
}

func (c *workshopSubAgentCleanup) track(execCtx *SubAgentExecutionContext) {
	if c == nil || execCtx == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contexts == nil {
		c.contexts = make(map[*SubAgentExecutionContext]struct{})
	}
	c.contexts[execCtx] = struct{}{}
}

func (c *workshopSubAgentCleanup) pending() []<-chan struct{} {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	contexts := make([]*SubAgentExecutionContext, 0, len(c.contexts))
	for execCtx := range c.contexts {
		contexts = append(contexts, execCtx)
	}
	c.mu.Unlock()
	var pending []<-chan struct{}
	for _, execCtx := range contexts {
		execCtx.asyncMu.Lock()
		for _, call := range execCtx.asyncCalls {
			select {
			case <-call.done:
			default:
				pending = append(pending, call.done)
			}
		}
		execCtx.asyncMu.Unlock()
	}
	return pending
}

func (b *workflowProgressBridge) finishProgressAfterChildrenStop() <-chan struct{} {
	done := make(chan struct{})
	ctx := context.Background()
	if b.session != nil && b.session.sessionCtx != nil {
		ctx = b.session.sessionCtx
	}
	finish := func() {
		defer close(done)
		if ctx.Err() != nil || b.session == nil || b.session.StepRegistry == nil {
			return
		}
		b.progressMu.Lock()
		ids := make([]string, 0, len(b.progressID))
		for _, id := range b.progressID {
			ids = append(ids, id)
		}
		b.progressMu.Unlock()
		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}
			exec := b.session.StepRegistry.Get(id)
			if exec == nil {
				continue
			}
			exec.mu.Lock()
			if exec.Status != WorkshopStepRunning {
				exec.mu.Unlock()
				continue
			}
			exec.Status = WorkshopStepFailed
			exec.Err = fmt.Errorf("parent execution %s finished without an end event for this step", b.parentID)
			exec.mu.Unlock()
			if b.logger != nil {
				b.logger.Info(fmt.Sprintf("[WORKFLOW_PROGRESS] Settled orphan execution=%s parent=%s status=failed after all owned workers stopped", id, b.parentID))
			}
		}
		// PLAT-091/117's server notifier still owns durable settlement and
		// notifications. This only repairs the workshop duplicate-run guard.
	}
	if len(b.ownedSubAgents.pending()) == 0 {
		finish()
		return done
	}
	if b.logger != nil {
		b.logger.Warn(fmt.Sprintf("[WORKFLOW_PROGRESS] Retaining progress for finished parent=%s until owned workers stop", b.parentID))
	}
	go func() {
		for {
			pending := b.ownedSubAgents.pending()
			if len(pending) == 0 {
				break
			}
			for _, stopped := range pending {
				select {
				case <-ctx.Done():
					close(done)
					return
				case <-stopped:
				}
			}
			// A finishing worker may have registered nested work since the
			// previous snapshot. Observe it before releasing the retry guard.
		}
		finish()
	}()
	return done
}
