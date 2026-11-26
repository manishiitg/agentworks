package events

import (
	"testing"
	"time"
)

type activityReadForbiddenJournal struct{ t *testing.T }

func (j activityReadForbiddenJournal) Append(_ string, e Event) (Event, bool, error) {
	return e, true, nil
}
func (j activityReadForbiddenJournal) LoadTail(string, int) ([]Event, error) {
	j.t.Fatal("activity display hydrated a cold chat journal")
	return nil, nil
}
func (j activityReadForbiddenJournal) Close() error { return nil }

// PLAT-856-572: roster activity must never hydrate a journal per idle agent.
func TestRetainedActivityTimeDoesNotHydrateColdSessions(t *testing.T) {
	at := time.Now()
	store := &EventStore{events: map[string][]Event{"warm": {{Timestamp: at}}}, durableJournal: activityReadForbiddenJournal{t}}
	for range 100 {
		if got := store.RetainedActivityTime("cold"); !got.IsZero() {
			t.Fatal(got)
		}
		if got := store.RetainedActivityTime("warm"); !got.Equal(at) {
			t.Fatal(got)
		}
	}
	if len(store.events) != 1 {
		t.Fatal("activity created or hydrated a session")
	}
}
