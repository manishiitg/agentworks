package server

import (
	"strconv"
	"testing"
	"time"
)

// PLAT-746-904: mixed feed ordering and Show older use one stable identity cursor.
func TestCrewTeamActivityPagesByMessageID(t *testing.T) {
	view := crewTeamActivityView{}
	now := time.Now()
	for i := 0; i < 21; i++ {
		view.BridgeMessages = append(view.BridgeMessages, crewTeamFeedItem{ID: strconv.Itoa(i), Kind: "message", At: now.Add(-time.Duration(i) * time.Minute)})
	}
	view.Decisions = []crewTeamActivityPost{{ID: "decision", At: now.Add(time.Minute), OwnerID: "owner", Agent: "Main", Line: "Settled"}}
	view.Findings = []crewTeamActivityPost{{ID: "finding", At: now.Add(-30 * time.Second), Schedule: "Daily check", Line: "Found"}}
	rows := crewTeamActivityFeed(view, "owner")
	if rows[0].Kind != "decision" || rows[0].Title != "Your Main" || rows[2].Kind != "finding" {
		t.Fatalf("feed not merged in time order: %+v", rows[:3])
	}
	first, cursor := crewTeamFeedPage(rows, "")
	if len(first) != 20 || cursor != rows[19].ID {
		t.Fatal("mixed feed page not capped")
	}
	rows = append([]crewTeamFeedItem{{ID: "decision:new"}}, rows...)
	second, next := crewTeamFeedPage(rows, cursor)
	if len(second) != 3 || second[0].ID != rows[21].ID || next != "" {
		t.Fatalf("cursor shifted: %+v", second)
	}
}

// PLAT-746-909: a teammate's abandoned wait is not a live activity claim;
// the maker still sees their own pending question after the same deadline.
func TestCrewTeamLiveWaitExpiresOnlyForTeammates(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		activity string
		age      time.Duration
		mine     bool
		want     string
	}{
		{"waiting", 29 * time.Minute, false, "waiting"},
		{"waiting", 30 * time.Minute, false, ""},
		{"waiting", 31 * time.Minute, true, "waiting"},
		{"running", 31 * time.Minute, false, "running"},
	} {
		if got := crewTeamLiveActivity(c.activity, now.Add(-c.age), c.mine, now); got != c.want {
			t.Fatalf("activity=%s age=%s mine=%v: got %q want %q", c.activity, c.age, c.mine, got, c.want)
		}
	}
}
