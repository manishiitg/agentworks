package keyedlock

import (
	"sync"
	"testing"
)

// Lock maps keyed by schedule paths and Watchers grew for the life of the
// server; a key must still exclude while held and be forgotten once free.
func TestKeyExcludesWhileHeldAndIsForgottenWhenFree(t *testing.T) {
	var locks Map
	counter, inside := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := locks.Get("workflow-a")
			l.Lock()
			inside++
			if inside != 1 {
				t.Error("two holders inside one key")
			}
			counter++
			inside--
			l.Unlock()
		}()
	}
	wg.Wait()
	if counter != 50 {
		t.Fatalf("counter %d", counter)
	}
	held := locks.Get("workflow-a")
	held.Lock()
	if locks.Get("workflow-a").TryLock() {
		t.Fatal("TryLock took a held key")
	}
	held.Unlock()
	if n := locks.Len(); n != 0 {
		t.Fatalf("%d keys kept after every holder left", n)
	}
}
