package toolclock

import (
	"testing"
	"time"
)

func TestElapsedMeasuresAndForgets(t *testing.T) {
	started := map[string]time.Time{"call-1": time.Now().Add(-250 * time.Millisecond)}

	got := Elapsed(started, "call-1")
	if got < 200*time.Millisecond {
		t.Errorf("Elapsed = %v, want at least ~250ms", got)
	}
	// Forgetting matters: a stream that never delivers an end event for some
	// calls would otherwise grow this map for the life of the turn.
	if _, still := started["call-1"]; still {
		t.Error("the call id was not forgotten after measuring")
	}
}

// A missing start is "not measured", which must stay distinguishable from
// "instant" -- reporting a fabricated 0 is exactly the bug this package fixes.
func TestElapsedReturnsZeroForUnknownCall(t *testing.T) {
	if got := Elapsed(map[string]time.Time{}, "never-started"); got != 0 {
		t.Errorf("Elapsed = %v, want 0 for an unknown call", got)
	}
	if got := Elapsed(nil, "call-1"); got != 0 {
		t.Errorf("Elapsed(nil) = %v, want 0", got)
	}
	if got := Elapsed(map[string]time.Time{"call-1": {}}, "call-1"); got != 0 {
		t.Errorf("Elapsed(zero-time) = %v, want 0", got)
	}
}

func TestSinceHandlesTheZeroTime(t *testing.T) {
	if got := Since(time.Time{}); got != 0 {
		t.Errorf("Since(zero) = %v, want 0", got)
	}
	if got := Since(time.Now().Add(-100 * time.Millisecond)); got < 50*time.Millisecond {
		t.Errorf("Since = %v, want at least ~100ms", got)
	}
}
