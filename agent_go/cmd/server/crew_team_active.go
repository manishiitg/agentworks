package server

import (
	"fmt"
	"sync"
	"time"
)

// The Team's people dot means "active now", decided here (owner, 5 Nov): green while the person has the website open
// (its live connection, renewed by every heartbeat) or their Mac is connected; grey otherwise. A server copy's sync
// does not count.
const webActiveWithin = 3 * time.Minute

var webSeen = struct {
	sync.Mutex
	at   map[string]time.Time
	open map[string]int
}{at: map[string]time.Time{}, open: map[string]int{}}

// noteWebOpen marks a live connection opened (+1) or closed (-1) for user; delta 0 renews it.
func noteWebOpen(user string, delta int) {
	if user == "" {
		return
	}
	webSeen.Lock()
	defer webSeen.Unlock()
	webSeen.at[user] = time.Now()
	if webSeen.open[user] += delta; webSeen.open[user] <= 0 {
		delete(webSeen.open, user)
	}
}

func webActive(user string) (bool, time.Time) {
	webSeen.Lock()
	defer webSeen.Unlock()
	at := webSeen.at[user]
	return webSeen.open[user] > 0 || (!at.IsZero() && time.Since(at) <= webActiveWithin), at
}

// teamActive is the dot and its words for one person.
func teamActive(user string, macOn bool) (bool, string) {
	web, at := webActive(user)
	switch {
	case web:
		return true, "Active now on the web"
	case macOn:
		return true, "Mac on"
	case at.IsZero():
		return false, "Not active"
	}
	return false, "Last seen " + roughAgo(time.Since(at))
}

func roughAgo(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}
