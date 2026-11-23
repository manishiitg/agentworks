package mcpagent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"
)

func TestAgentCloseClosesStreamingTracersInSessionScopedMode(t *testing.T) {
	tracer := NewStreamingTracer(observability.NoopTracer{}, 1)
	agent := &Agent{
		logger:    loggerv2.NewDefault(),
		sessionID: "test-session",
		tracers:   []observability.Tracer{tracer},
	}

	_ = agent.Close()

	ch, unsubscribe := tracer.SubscribeToEvents(context.Background())
	defer unsubscribe()
	if ch != nil {
		t.Fatal("expected SubscribeToEvents to return nil after Agent.Close closes streaming tracer")
	}
}

func TestStreamingTracerEmitAfterCloseDoesNotPanic(t *testing.T) {
	tracer := NewStreamingTracer(observability.NoopTracer{}, 1)
	if err := tracer.(interface{ Close() error }).Close(); err != nil {
		t.Fatalf("close tracer: %v", err)
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("EmitEvent after Close panicked: %v", recovered)
		}
	}()

	if err := tracer.EmitEvent(&events.AgentEvent{
		Type:      events.StreamingChunk,
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("EmitEvent after Close returned error: %v", err)
	}
}

func TestStreamingTracerUnsubscribeDuringForwardDoesNotPanic(t *testing.T) {
	tracer := NewStreamingTracer(observability.NoopTracer{}, 16)
	defer func() {
		_ = tracer.(interface{ Close() error }).Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	ch, unsubscribe := tracer.SubscribeToEvents(ctx)
	if ch == nil {
		t.Fatal("expected subscriber channel")
	}
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)
	panicCh := make(chan interface{}, 2)

	go func() {
		defer wg.Done()
		defer func() {
			if recovered := recover(); recovered != nil {
				panicCh <- recovered
			}
		}()
		for i := 0; i < 1000; i++ {
			_ = tracer.EmitEvent(&events.AgentEvent{
				Type:       events.StreamingChunk,
				Timestamp:  time.Now(),
				EventIndex: i,
			})
		}
	}()

	go func() {
		defer wg.Done()
		defer func() {
			if recovered := recover(); recovered != nil {
				panicCh <- recovered
			}
		}()
		unsubscribe()
	}()

	wg.Wait()
	close(panicCh)
	for recovered := range panicCh {
		t.Fatalf("streaming tracer panicked during concurrent unsubscribe/emit: %v", recovered)
	}
}
