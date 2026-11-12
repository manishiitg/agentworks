package server

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// The Team's people dot means "active now", decided here (owner, 5 Nov): green while the person has the website open
// (its live connection, renewed by every heartbeat) or their Mac is connected; grey otherwise. A server copy's sync
// does not count.
const webActiveWithin = 3 * time.Minute

var webSeen = struct {
	sync.Mutex
	at     map[string]time.Time
	open   map[string]int
	loaded bool
	saved  time.Time
}{at: map[string]time.Time{}, open: map[string]int{}}

// Last seen survives a restart (owner, 12 Nov: every deploy showed everyone "Not active"): the times are kept in one
// small app-private file, written at most once a minute. Only user IDs and times, nothing else.
const webSeenSaveEvery = time.Minute

func webSeenPath() (string, error) {
	state, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "team-last-seen.json"), nil
}

// loadWebSeenLocked reads the saved times once; the caller holds webSeen's lock.
func loadWebSeenLocked() {
	if webSeen.loaded {
		return
	}
	webSeen.loaded = true
	path, err := webSeenPath()
	if err != nil {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	saved := map[string]time.Time{}
	if json.Unmarshal(raw, &saved) != nil {
		log.Printf("[CREW_TEAM_ACTIVE] event=last_seen_unreadable")
		return
	}
	for user, at := range saved {
		if at.After(webSeen.at[user]) {
			webSeen.at[user] = at
		}
	}
	log.Printf("[CREW_TEAM_ACTIVE] event=last_seen_loaded people=%d", len(saved))
}

// noteWebOpen marks a live connection opened (+1) or closed (-1) for user; delta 0 renews it.
func noteWebOpen(user string, delta int) {
	if user == "" {
		return
	}
	webSeen.Lock()
	defer webSeen.Unlock()
	loadWebSeenLocked()
	now := time.Now()
	webSeen.at[user] = now
	if webSeen.open[user] += delta; webSeen.open[user] <= 0 {
		delete(webSeen.open, user)
	}
	if now.Sub(webSeen.saved) < webSeenSaveEvery && delta >= 0 {
		return // a close is always saved, so the time a person left is the one kept
	}
	webSeen.saved = now
	copied := make(map[string]time.Time, len(webSeen.at))
	for id, at := range webSeen.at {
		copied[id] = at.UTC()
	}
	go func() {
		path, err := webSeenPath()
		if err == nil {
			err = fsutil.WriteJSONAtomic(path, copied, 0o600)
		}
		if err != nil {
			log.Printf("[CREW_TEAM_ACTIVE] event=last_seen_save_failed error=%v", err)
		}
	}()
}

func webActive(user string) (bool, time.Time) {
	webSeen.Lock()
	defer webSeen.Unlock()
	loadWebSeenLocked()
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
