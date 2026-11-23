package tmuxinput

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestBrokerHoldsNormalInputUntilInitialPromptReady(t *testing.T) {
	broker := NewBroker()
	sessionID := "startup-order"
	MarkStarting(sessionID)
	defer RemoveReadiness(sessionID)

	var order []string
	initialStarted := make(chan struct{})
	initialRelease := make(chan struct{})
	initialDone := make(chan error, 1)
	go func() {
		_, err := broker.Do(context.Background(), Request{
			SessionID:       sessionID,
			Source:          "initial",
			BypassReadiness: true,
		}, func(context.Context) error {
			close(initialStarted)
			<-initialRelease
			order = append(order, "initial")
			return nil
		})
		initialDone <- err
	}()
	<-initialStarted

	liveDone := make(chan error, 1)
	go func() {
		_, err := broker.Do(context.Background(), Request{SessionID: sessionID, Source: "live"}, func(context.Context) error {
			order = append(order, "live")
			return nil
		})
		liveDone <- err
	}()

	select {
	case err := <-liveDone:
		t.Fatalf("live input ran before readiness: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(initialRelease)
	if err := <-initialDone; err != nil {
		t.Fatalf("initial prompt: %v", err)
	}
	MarkReady(sessionID)
	if err := <-liveDone; err != nil {
		t.Fatalf("live input: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"initial", "live"}) {
		t.Fatalf("order = %v", order)
	}
}

func TestBrokerInterruptBypassesStartupReadiness(t *testing.T) {
	broker := NewBroker()
	sessionID := "startup-interrupt"
	MarkStarting(sessionID)
	defer RemoveReadiness(sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	called := false
	_, err := broker.Do(ctx, Request{
		SessionID: sessionID,
		Source:    "control",
		Priority:  PriorityInterrupt,
	}, func(context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	if !called {
		t.Fatal("interrupt operation did not run")
	}
}

func TestWaitUntilReadyFailsWhenStartupSessionCloses(t *testing.T) {
	sessionID := "startup-close"
	MarkStarting(sessionID)
	done := make(chan error, 1)
	go func() { done <- WaitUntilReady(context.Background(), sessionID) }()
	time.Sleep(10 * time.Millisecond)
	RemoveReadiness(sessionID)
	if err := <-done; err == nil {
		t.Fatal("expected closed-startup error")
	}
}
