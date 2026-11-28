package filesnapshots

import (
	"os"
	"testing"
	"time"
)

// #4080: reclaiming expired replies' files rewrites the whole private store
// under the gate, so it runs at most once per gcEvery, and only when an
// expired reply left files behind.
func TestStoreReclaimsExpiredFilesAtMostOncePerInterval(t *testing.T) {
	ctx := t.Context()
	s := Store{Root: t.TempDir(), Private: t.TempDir()}
	if err := s.init(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if s.collectGarbage(ctx, now) {
		t.Fatal("reclaimed with nothing expired")
	}
	if err := os.WriteFile(s.gcPendingPath(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !s.collectGarbage(ctx, now) {
		t.Fatal("first reclaim did not run")
	}
	if _, err := os.Stat(s.gcPendingPath()); !os.IsNotExist(err) {
		t.Fatalf("pending mark kept after reclaim: %v", err)
	}
	if err := os.WriteFile(s.gcPendingPath(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if s.collectGarbage(ctx, now.Add(gcEvery/2)) {
		t.Fatal("reclaimed again inside the interval")
	}
	if !s.collectGarbage(ctx, now.Add(gcEvery+time.Minute)) {
		t.Fatal("pending reclaim did not run after the interval")
	}
}

func TestStoreScheduleGarbageCollectionRunsAsyncAndSkipsWhenBusy(t *testing.T) {
	ctx := t.Context()
	s := Store{Root: t.TempDir(), Private: t.TempDir()}
	if err := s.init(ctx); err != nil {
		t.Fatal(err)
	}
	origDelay := gcIdleDelay
	gcIdleDelay = 10 * time.Millisecond
	t.Cleanup(func() { gcIdleDelay = origDelay })

	if err := os.WriteFile(s.gcPendingPath(), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// Hold the gate: scheduled GC must skip without blocking.
	unlock, err := s.lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.scheduleGarbageCollection()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && s.state().gcRunning.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	if s.state().gcRunning.Load() {
		unlock()
		t.Fatal("scheduled GC blocked while store gate was held")
	}
	if _, err := os.Stat(s.gcPendingPath()); err != nil {
		unlock()
		t.Fatalf("expected gc-pending to remain while busy, got err=%v", err)
	}
	unlock()

	// Once gate is free, scheduled GC runs asynchronously and clears gc-pending.
	s.scheduleGarbageCollection()
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(s.gcPendingPath()); os.IsNotExist(err) && !s.state().gcRunning.Load() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("scheduled async GC did not clear gc-pending when idle")
}
