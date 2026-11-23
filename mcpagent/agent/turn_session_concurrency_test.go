package mcpagent

import (
	"context"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/llm"
)

func TestSessionSendDoesNotWaitForRunningTurnLock(t *testing.T) {
	agent := &Agent{provider: llm.ProviderOpenAI}
	session := &Session{agent: agent}

	// Simulate Run owning its serialization lock. Send must use the independent
	// state path so a live steering request can arrive while Run is blocked in a
	// provider call.
	session.runMu.Lock()
	defer session.runMu.Unlock()
	agent.setTurnInFlight(true) // a running turn is what drains the queued message

	type outcome struct {
		result DeliveryResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := session.Send(context.Background(), "change direction")
		done <- outcome{result: result, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if !got.result.Queued || got.result.Status != UserMessageDeliveryStatusQueuedForInjection {
			t.Fatalf("delivery = %#v", got.result)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Session.Send blocked behind the active Run lock")
	}
}
