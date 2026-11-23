package codexcli

import (
	"sync"
	"testing"
	"time"
)

// PLAT-116: each live turn holds its own session.mu. Final-answer rollout
// resolution must not acquire another session's whole-turn mutex or two turns
// completing together deadlock after both providers already returned.
func TestConcurrentRolloutResolutionDoesNotCrossLockTurnSessions(t *testing.T) {
	first := &codexInteractiveSession{ownerSessionID: "first", workingDir: t.TempDir()}
	second := &codexInteractiveSession{ownerSessionID: "second", workingDir: t.TempDir()}
	old := codexPersistentRegistry.Replace(map[string]*codexInteractiveSession{
		"first":  first,
		"second": second,
	})
	t.Cleanup(func() { codexPersistentRegistry.Replace(old) })

	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	done := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for _, session := range []*codexInteractiveSession{first, second} {
		wg.Add(1)
		go func(session *codexInteractiveSession) {
			defer wg.Done()
			session.mu.Lock()
			defer session.mu.Unlock()
			ready <- struct{}{}
			<-start
			_ = resolveCodexRolloutPathLocked(session, time.Now())
			done <- struct{}{}
		}(session)
	}

	<-ready
	<-ready
	close(start)
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("concurrent rollout resolution deadlocked on another live turn session")
		}
	}
	wg.Wait()
}
