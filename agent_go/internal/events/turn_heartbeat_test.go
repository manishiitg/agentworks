package events

import (
	"testing"
	"time"
)

// A stuck turn must surface in the log: heartbeats at 2, 5, 10 and 20 minutes, then every 20 minutes, one at a time.
func TestTurnHeartbeatDue(t *testing.T) {
	cases := []struct {
		age   time.Duration
		beats int
		want  bool
	}{
		{time.Minute, 0, false},
		{2 * time.Minute, 0, true},
		{3 * time.Minute, 1, false},
		{5 * time.Minute, 1, true},
		{10 * time.Minute, 2, true},
		{20 * time.Minute, 3, true},
		{30 * time.Minute, 4, false},
		{40 * time.Minute, 4, true},
		{40 * time.Minute, 5, false},
		{60 * time.Minute, 5, true},
	}
	for _, c := range cases {
		if got := turnHeartbeatDue(c.age, c.beats); got != c.want {
			t.Errorf("age %s beats %d: got %v want %v", c.age, c.beats, got, c.want)
		}
	}
}
