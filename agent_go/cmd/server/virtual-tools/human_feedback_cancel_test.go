package virtualtools

import (
	"context"
	"errors"
	"testing"
	"time"
)

// PLAT-368: stopping the run that asked releases its wait at once and
// removes the question, so it stops showing as pending and a late answer is
// refused. A deadline on the context does not end the wait early.
func TestFeedbackWaitEndsWhenTheRunIsStopped(t *testing.T) {
	store := GetHumanFeedbackStore()
	id := "plat368-cancel-" + time.Now().Format("150405.000000000")
	if err := store.CreatePendingRequest(id, "Continue?", "", "sess", nil, true, time.Minute); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := store.WaitForResponseCtx(ctx, id, time.Minute)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrFeedbackCancelled) {
			t.Fatalf("err = %v, want ErrFeedbackCancelled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the wait did not end when the run was stopped")
	}
	for _, request := range store.PendingForSession("sess", time.Now()) {
		if request.UniqueID == id {
			t.Fatal("a stopped run's question is still pending")
		}
	}
	if err := store.SubmitResponseForSession("sess", id, "late", time.Now()); err == nil {
		t.Fatal("a late answer to a stopped run was accepted")
	}
}

func TestFeedbackWaitIgnoresAContextDeadline(t *testing.T) {
	store := GetHumanFeedbackStore()
	id := "plat368-deadline-" + time.Now().Format("150405.000000000")
	if err := store.CreatePendingRequest(id, "Continue?", "", "sess", nil, true, time.Minute); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	done := make(chan string, 1)
	go func() {
		response, _ := store.WaitForResponseCtx(ctx, id, time.Minute)
		done <- response
	}()
	time.Sleep(60 * time.Millisecond)
	if err := store.SubmitResponse(id, "yes"); err != nil {
		t.Fatalf("question dropped when a context deadline passed: %v", err)
	}
	if response := <-done; response != "yes" {
		t.Fatalf("response = %q", response)
	}
}
