package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// The website and phone report what they see on a Crew chat's connection (PLAT-856-509), so a "my phone lost the
// chat" report can be traced from both ends. Logs only: the backend decides nothing from these. IDs and short codes,
// never message text; rate-limited per person.
var clientEventKinds = map[string]bool{
	"network_offline": true, "network_online": true, "page_hidden": true, "page_visible": true,
	"reconnect_attempt": true, "reconnect_gave_up": true, "send_stuck": true, "file_open_failed": true, "mac_off_notice": true,
}

var (
	clientEventID     = regexp.MustCompile(`^[A-Za-z0-9._:=-]{0,160}$`)
	clientEventDetail = regexp.MustCompile(`^[a-z0-9_=.,-]{0,64}$`)
)

const clientEventsPerMinute = 60

var clientEventBudget = struct {
	sync.Mutex
	window map[string]time.Time
	count  map[string]int
}{window: map[string]time.Time{}, count: map[string]int{}}

func clientEventAllowed(user string, now time.Time) bool {
	clientEventBudget.Lock()
	defer clientEventBudget.Unlock()
	if now.Sub(clientEventBudget.window[user]) > time.Minute {
		clientEventBudget.window[user], clientEventBudget.count[user] = now, 0
	}
	clientEventBudget.count[user]++
	return clientEventBudget.count[user] <= clientEventsPerMinute
}

func handleClientEvent(w http.ResponseWriter, r *http.Request) {
	user := GetUserIDFromContext(r.Context())
	if user == "" {
		externalError(w, 401, "sign_in", "Sign in first.")
		return
	}
	var in struct {
		Kind         string `json:"kind"`
		Project      string `json:"project"`
		Conversation string `json:"conversation"`
		Submission   string `json:"submission"`
		Detail       string `json:"detail"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 2048)).Decode(&in) != nil || !clientEventKinds[in.Kind] ||
		!clientEventID.MatchString(in.Project) || !clientEventID.MatchString(in.Conversation) || !clientEventID.MatchString(in.Submission) || !clientEventDetail.MatchString(in.Detail) {
		externalError(w, 400, "invalid_event", "Send a known event kind with IDs and a short code.")
		return
	}
	if clientEventAllowed(user, time.Now()) {
		log.Printf("[CLIENT_EVENT] username=%s device=%s project=%s conversation=%s submission=%s kind=%s detail=%s", logUsernameForUserID(user), clientDeviceClass(r.UserAgent()), logContextValue(in.Project), logContextValue(in.Conversation), logContextValue(in.Submission), in.Kind, logContextValue(in.Detail))
	}
	w.WriteHeader(http.StatusNoContent)
}
