package server

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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
	via    map[string]map[string]int // user -> where (Mac app, Windows app, website, phone) -> open connections
	last   map[string]string         // user -> where they were last seen
	loaded bool
	saved  time.Time
}{at: map[string]time.Time{}, open: map[string]int{}, via: map[string]map[string]int{}, last: map[string]string{}}

// clientWhere names where a person uses AgentWorks, from the live connection's User-Agent (owner, 13 Nov: show
// whether they are on the Mac or Windows app or the website).
func clientWhere(ua string) string {
	lower := strings.ToLower(ua)
	switch {
	case strings.Contains(lower, "electron") || strings.Contains(lower, "agentworks"):
		if strings.Contains(lower, "windows") {
			return "Windows app"
		}
		return "Mac app"
	case strings.Contains(lower, "iphone") || strings.Contains(lower, "android") || strings.Contains(lower, "mobile"):
		return "phone"
	}
	return "website"
}

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

// noteWebOpen marks a live connection from where opened (+1) or closed (-1) for user; delta 0 renews it.
func noteWebOpen(user, where string, delta int) {
	if user == "" {
		return
	}
	webSeen.Lock()
	defer webSeen.Unlock()
	loadWebSeenLocked()
	now := time.Now()
	webSeen.at[user] = now
	webSeen.last[user] = where
	if webSeen.open[user] += delta; webSeen.open[user] <= 0 {
		delete(webSeen.open, user)
	}
	if webSeen.via[user] == nil {
		webSeen.via[user] = map[string]int{}
	}
	if webSeen.via[user][where] += delta; webSeen.via[user][where] <= 0 {
		delete(webSeen.via[user], where)
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

// webActive says whether user is active now, where (open places in a fixed order, else where they were last), and when.
func webActive(user string) (bool, []string, time.Time) {
	webSeen.Lock()
	defer webSeen.Unlock()
	loadWebSeenLocked()
	at := webSeen.at[user]
	places := []string{}
	for _, where := range []string{"Mac app", "Windows app", "website", "phone"} {
		if webSeen.via[user][where] > 0 {
			places = append(places, where)
		}
	}
	if len(places) == 0 && webSeen.last[user] != "" {
		places = append(places, webSeen.last[user])
	}
	return webSeen.open[user] > 0 || (!at.IsZero() && time.Since(at) <= webActiveWithin), places, at
}

// teamActive is the dot and its words for one person, saying where they are: "Active now · Mac app and website",
// "Mac app connected", "Last seen 2 h ago · website".
func teamActive(user string, macOn bool) (bool, string) {
	active, places, at := webActive(user)
	where := strings.Join(places, " and ")
	switch {
	case active && where != "":
		return true, "Active now · " + where
	case active:
		return true, "Active now"
	case macOn:
		return true, "Mac app connected"
	case at.IsZero():
		return false, "Not active"
	case where != "":
		return false, "Last seen " + roughAgo(time.Since(at)) + " · " + where
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
