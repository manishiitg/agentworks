package mcpagent

import (
	"context"
	"errors"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"
)

// Exercise the production backoff timer at every delay from the incident.
func TestRetryBackoffCancellationP0(t *testing.T) {
	for attempt := 0; attempt < 3; attempt++ {
		t.Run(time.Duration(10*(1<<attempt)*int(time.Second)).String(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a := &Agent{logger: loggerv2.NewNoop()}
			type result struct {
				retry bool
				delay time.Duration
				err   error
			}
			done := make(chan result, 1)
			go func() {
				retry, delay, err := retryOriginalModel(a, ctx, "throttling_error", attempt, 5, 10*time.Second, 300*time.Second, 0, a.logger, observability.UsageMetrics{})
				done <- result{retry, delay, err}
			}()
			// First prove that the production backoff is actually waiting.
			select {
			case got := <-done:
				t.Fatalf("returned before Stop: %+v", got)
			case <-time.After(50 * time.Millisecond):
			}
			cancel()
			select {
			case got := <-done:
				if got.retry || !errors.Is(got.err, context.Canceled) {
					t.Fatalf("retry survived Stop: %+v", got)
				}
				if got.delay != 10*time.Second*time.Duration(1<<attempt) {
					t.Fatalf("wrong backoff: %v", got.delay)
				}
			case <-time.After(time.Second):
				t.Fatal("Stop did not interrupt backoff")
			}
		})
	}
}
