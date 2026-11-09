package orchestrator

import (
	"context"
	orchevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"time"
)

const transcriptFlushInterval = 2 * time.Second

type transcriptBatchKey struct{ session, agent string }
type transcriptBatch struct {
	writer BackgroundAgentTranscriptWriter
	events []orchevents.BackgroundAgentTranscriptEvent
}

// PLAT-746-515: provider events used to each launch a goroutine and a complete
// transcript read/modify/write over HTTP. Keep their order in one pending batch,
// flush at most once per interval, and drain immediately at the existing end barrier.
func (c *ContextAwareEventBridge) queueTranscriptEvents(writer BackgroundAgentTranscriptWriter, session, agent string, events []orchevents.BackgroundAgentTranscriptEvent) {
	if len(events) == 0 {
		return
	}
	c.transcriptBatchMu.Lock()
	defer c.transcriptBatchMu.Unlock()
	if c.transcriptBatches == nil {
		c.transcriptBatches = map[transcriptBatchKey]transcriptBatch{}
	}
	key := transcriptBatchKey{session, agent}
	batch := c.transcriptBatches[key]
	batch.writer = writer
	batch.events = append(batch.events, events...)
	c.transcriptBatches[key] = batch
	if c.transcriptTimer == nil {
		c.transcriptTimer = time.AfterFunc(transcriptFlushInterval, c.flushTranscriptBatches)
	}
}

func (c *ContextAwareEventBridge) flushTranscriptBatches() {
	c.transcriptBatchMu.Lock()
	defer c.transcriptBatchMu.Unlock()
	if c.transcriptTimer != nil {
		c.transcriptTimer.Stop()
		c.transcriptTimer = nil
	}
	for key, batch := range c.transcriptBatches {
		// Add each write to the existing barrier before releasing the batch lock,
		// so a finishing turn also waits for a concurrently firing timer.
		previous := c.transcriptBatchTail
		done := make(chan struct{})
		c.transcriptBatchTail = done
		c.persistTranscriptAsync("background agent transcript batch", func(ctx context.Context) error {
			defer close(done)
			if previous != nil {
				select {
				case <-previous:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return batch.writer.AppendBackgroundAgentTranscriptEvents(ctx, key.session, key.agent, batch.events)
		})
	}
	c.transcriptBatches = nil
}
