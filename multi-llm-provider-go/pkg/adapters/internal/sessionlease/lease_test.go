package sessionlease

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestArmSupersedesQueuedGeneration(t *testing.T) {
	var lease Lease
	var expired atomic.Int32
	first := make(chan struct{})
	if !lease.Arm(50*time.Millisecond, func() {
		expired.Add(1)
		close(first)
	}) {
		t.Fatal("initial arm rejected")
	}
	if !lease.Arm(100*time.Millisecond, func() { expired.Add(1) }) {
		t.Fatal("refresh arm rejected")
	}
	select {
	case <-first:
		t.Fatal("superseded callback ran")
	case <-time.After(60 * time.Millisecond):
	}
	if expired.Load() != 0 {
		t.Fatalf("expired callbacks = %d, want 0", expired.Load())
	}
	lease.Stop()
}

func TestPauseThenRearm(t *testing.T) {
	var lease Lease
	var expired atomic.Int32
	if !lease.Arm(5*time.Millisecond, func() { expired.Add(1) }) {
		t.Fatal("arm rejected")
	}
	lease.Pause()
	time.Sleep(10 * time.Millisecond)
	if expired.Load() != 0 {
		t.Fatal("paused lease expired")
	}
	done := make(chan struct{})
	if !lease.Arm(time.Millisecond, func() { close(done) }) {
		t.Fatal("rearm after pause rejected")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("rearmed lease did not expire")
	}
}

func TestStopRejectsFutureActivity(t *testing.T) {
	var lease Lease
	lease.Stop()
	if lease.Arm(time.Second, func() {}) {
		t.Fatal("stopped lease accepted activity")
	}
}
