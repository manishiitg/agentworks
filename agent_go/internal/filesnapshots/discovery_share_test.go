package filesnapshots

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Overlapping captures of one checkout share a single read (each keeping its own copy), and reads of different
// checkouts never run more than discoverySlots at a time, however many captures ask at once.
func TestShareRepoReadSharesOneCheckoutAndBoundsAllReads(t *testing.T) {
	var runs atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	read := func(context.Context) (gitTurnRepo, error) {
		if runs.Add(1) == 1 {
			close(started)
		}
		<-release
		return gitTurnRepo{Head: "abc", Dirty: []string{"a.go"}}, nil
	}
	var wg sync.WaitGroup
	got := make([]gitTurnRepo, 3)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], _, _ = shareRepoRead(t.Context(), "root\x00same", read)
		}()
	}
	<-started
	time.Sleep(100 * time.Millisecond) // let the others join the running read
	close(release)
	wg.Wait()
	if runs.Load() != 1 {
		t.Fatalf("one checkout was read %d times by overlapping captures", runs.Load())
	}
	got[0].Dirty[0] = "changed.go"
	if got[1].Dirty[0] != "a.go" || got[2].Head != "abc" {
		t.Fatalf("captures share their answer's memory: %+v", got)
	}

	var running, peak atomic.Int32
	slow := func(context.Context) (gitTurnRepo, error) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		return gitTurnRepo{}, nil
	}
	for i := 0; i < 5*discoverySlots; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			shareRepoRead(t.Context(), fmt.Sprintf("root\x00repo-%d", i), slow)
		}()
	}
	wg.Wait()
	if peak.Load() > discoverySlots || peak.Load() == 0 {
		t.Fatalf("%d checkout reads ran at once, limit %d", peak.Load(), discoverySlots)
	}
}
