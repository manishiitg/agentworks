package server

import (
	"slices"
	"testing"
	"time"
)

// The Mac relay pushes a chat's busy state only when the coordinator says it changed (#3187), so every
// turn start, wait and end must reach a watcher, and repeated reads of an unchanged turn must not.
func TestTurnWatcherSeesEachTurnChangeOnce(t *testing.T) {
	c := NewRuntimeCoordinator()
	w, stop := c.WatchTurns()
	defer stop()
	took := func() []string {
		select {
		case <-w.Wake():
			return w.Take()
		default:
			return nil
		}
	}
	c.StartGeneration("chat", "new user turn")
	if got := took(); !slices.Equal(got, []string{"chat"}) {
		t.Fatalf("turn start: %v", got)
	}
	later := time.Now().Add(time.Second)
	running := RuntimeSnapshot{SessionID: "chat", Phase: runtimePhaseRunning, RawSessionStatus: "running", StartedAt: later, LastProgressAt: later}
	c.Observe(running)
	running.LastProgressAt = later.Add(time.Second)
	c.Observe(running)
	if got := took(); got != nil {
		t.Fatalf("starting -> running is the same busy turn, told again: %v", got)
	}
	waiting := running
	waiting.Phase, waiting.WaitingForUser = runtimePhaseWaiting, true
	c.Observe(waiting)
	if got := took(); !slices.Equal(got, []string{"chat"}) {
		t.Fatalf("waiting for the person: %v", got)
	}
	c.MarkTerminalBoundary("chat", runtimePhaseCanceled, "stopped")
	if got := took(); !slices.Equal(got, []string{"chat"}) {
		t.Fatalf("stop: %v", got)
	}
}
