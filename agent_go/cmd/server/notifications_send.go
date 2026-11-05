package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

// What the server notifies about, decided here and only here (notifications.go).

type notifyJob struct {
	event   string // finished | failed | needs_you
	session string
	owner   string // who started the turn, when the caller knows
}

var notifyQueue = make(chan notifyJob, 512)

// notifyTurnEnded: a chat turn went from running to completed or error. A person's own stop sends nothing; scheduled
// runs and bot conversations are not chats someone waits on.
func (api *StreamingAPI) notifyTurnEnded(sessionID, previous, status string, info *ActiveSessionInfo) {
	if previous != "running" || status != "completed" && status != "error" || info == nil {
		return
	}
	if isScheduledSessionIdentity(sessionID, info.TriggeredBy) || strings.HasPrefix(info.TurnProvider, "bot_") {
		return
	}
	event := notifyEventFinished
	if status == "error" {
		event = notifyEventFailed
	}
	notifyEnqueue(notifyJob{event: event, session: sessionID, owner: info.UserID})
}

// notifyNeedsYou: an agent asked the person something (a question, a choice, an approval). Once per request; a
// replayed old event sends nothing.
var notifyAsked sync.Map // event id -> time

func (api *StreamingAPI) notifyNeedsYou(sessionID string, event events.Event) {
	if event.Type != "blocking_human_feedback" && event.Type != "request_human_feedback" && event.Type != "coding_agent_question" {
		return
	}
	if !event.Timestamp.IsZero() && time.Since(event.Timestamp) > 2*time.Minute {
		return
	}
	if _, seen := notifyAsked.LoadOrStore(event.ID, time.Now()); seen {
		return
	}
	owner := ""
	if active, ok := api.getActiveSession(sessionID); ok {
		owner = active.UserID
	}
	notifyEnqueue(notifyJob{event: notifyEventNeedsYou, session: sessionID, owner: owner})
}

func notifyEnqueue(job notifyJob) {
	select {
	case notifyQueue <- job:
	default:
		log.Printf("[NOTIFY] session=%s event=%s result=skipped reason=queue_full", job.session, job.event)
	}
}

// startNotifier sends from one worker, so a turn never waits on a push service; in a Mac app it also relays the
// company servers' messages for this Mac.
func (api *StreamingAPI) startNotifier(ctx context.Context) {
	go func() {
		cleanup := time.NewTicker(10 * time.Minute)
		defer cleanup.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case job := <-notifyQueue:
				api.notifySend(ctx, job)
			case <-cleanup.C:
				notifyAsked.Range(func(key, value any) bool {
					if time.Since(value.(time.Time)) > time.Hour {
						notifyAsked.Delete(key)
					}
					return true
				})
			}
		}
	}()
	if IsLocalMode() {
		go api.runNotifyRelays(ctx)
	}
}

// notifyChat is what a notification says about a chat: which agent, which Crew, where tapping goes. Never content.
type notifyChat struct {
	owner, agent, crew, route, project string
	tab                                chatContextTab
	found                              bool
}

func (api *StreamingAPI) notifyChatFor(ctx context.Context, job notifyJob) notifyChat {
	owner := api.idleCompactOwner(job.session, job.owner)
	chat := notifyChat{owner: owner, agent: "AgentWorks", route: "/"}
	tab, ok := chatContextTabForSession(ctx, owner, job.session)
	if !ok {
		return chat
	}
	chat.tab, chat.found = tab, true
	key := tab.Record.ConversationKey
	if tab.ProfileID != crewProfileID {
		return chat
	}
	chat.project = crewResourceProjectID(key)
	chat.agent = "Main"
	chat.route = "/crew/projects/" + url.PathEscape(chat.project)
	if isSideChatConversationKey(key) {
		chat.agent = firstNonEmptyTrimmed(tab.Identity.Name, "An agent")
		_, side, _ := strings.Cut(key, codeChatSideMarker)
		chat.route += "?agent=" + url.QueryEscape(side)
	}
	if raw, exists, err := readFileFromWorkspace(ctx, strings.TrimRight(tab.Record.WorkspacePath, "/")+"/product.json"); err == nil && exists {
		var manifest productProjectManifest
		if json.Unmarshal([]byte(raw), &manifest) == nil {
			chat.crew = firstNonEmptyTrimmed(manifest.Identity.Name, manifest.Title)
		}
	}
	return chat
}

// notifyCanSee: the person can watch this chat now, by the same rules as watching it: their own, a co-owner's tab
// they may view, or a Team's chat (Team on, joined, not Only me).
func (api *StreamingAPI) notifyCanSee(ctx context.Context, user string, chat notifyChat, session string) bool {
	if user == chat.owner {
		return true
	}
	if !chat.found || chat.project == "" || !crewTeamAccountActive(user) {
		return false
	}
	claims := &UserClaims{UserID: user, Username: user}
	r := (&http.Request{}).WithContext(context.WithValue(ctx, UserContextKey, claims))
	if api.crewSessionTabView(r, session) {
		return true
	}
	t, err := readCrewTeam(chat.project)
	if err != nil || !t.On || !t.member(user) {
		return false
	}
	if chat.owner == t.OwnerID {
		return t.member(user) && !chat.tab.Identity.OnlyMe
	}
	return slices.ContainsFunc(crewTeamMemberTabs(ctx, chat.project, user), func(tab AgentProfileSideChat) bool { return tab.SessionID == session })
}

func notifyText(event string, chat notifyChat) (string, string) {
	what, body := " finished", "Tap to open the chat."
	switch event {
	case notifyEventFailed:
		what, body = " couldn't finish", "Tap to see what happened."
	case notifyEventNeedsYou:
		what, body = " needs you", "It's asking you something."
	}
	title := chat.agent + what
	if chat.crew != "" && chat.crew != chat.agent {
		title += " · " + chat.crew
	}
	return title, body
}

func (api *StreamingAPI) notifySend(ctx context.Context, job notifyJob) {
	chat := api.notifyChatFor(ctx, job)
	title, body := notifyText(job.event, chat)
	for _, rec := range notifyRecordsWithDevices() {
		user := rec.UserID
		if user != chat.owner && rec.Prefs.Chats != "all" {
			continue
		}
		if job.event == notifyEventNeedsYou && rec.Prefs.NeedsMeOff {
			log.Printf("[NOTIFY] user=%s event=%s session=%s result=skipped reason=needs_me_off", logUsernameForUserID(user), job.event, job.session)
			continue
		}
		if !api.notifyCanSee(ctx, user, chat, job.session) {
			if user == chat.owner || rec.Prefs.Chats == "all" && chat.found && chat.project != "" {
				log.Printf("[NOTIFY] user=%s event=%s session=%s result=skipped reason=no_access", logUsernameForUserID(user), job.event, job.session)
			}
			continue
		}
		for _, device := range rec.Devices {
			api.notifyDeliver(ctx, user, device, job, notifyMessage{Title: title, Body: body, Route: chat.route, Session: job.session, Event: job.event, Sound: device.Sound})
		}
	}
}

func (api *StreamingAPI) notifyDeliver(ctx context.Context, user string, device notifyDevice, job notifyJob, msg notifyMessage) {
	result, reason := "sent", ""
	defer func() {
		log.Printf("[NOTIFY] user=%s device=%.8s kind=%s event=%s session=%s result=%s reason=%s", logUsernameForUserID(user), device.ID, device.Kind, job.event, job.session, result, firstNonEmptyTrimmed(reason, "-"))
		if result == "sent" {
			_, _ = updateNotifyRecord(user, func(rec *notifyRecord) error {
				if at := slices.IndexFunc(rec.Devices, func(d notifyDevice) bool { return d.ID == device.ID }); at >= 0 {
					rec.Devices[at].LastSent = time.Now().UTC().Format(time.RFC3339)
				}
				return nil
			})
		}
	}()
	if notifyFocused(device.ID, user, job.session) {
		result, reason = "skipped", "focused"
		return
	}
	switch device.Kind {
	case notifyKindMac:
		target := device.ID
		if IsLocalMode() {
			target = notifyMacAppDevice
		}
		if !notifyStreamSend(target, msg) {
			result, reason = "skipped", "offline"
		}
	case notifyKindWeb:
		status, err := notifyWebPush(ctx, device, msg)
		switch {
		case status == http.StatusNotFound || status == http.StatusGone:
			result, reason = "skipped", "gone"
			_, _ = updateNotifyRecord(user, func(rec *notifyRecord) error {
				rec.Devices = slices.DeleteFunc(rec.Devices, func(d notifyDevice) bool { return d.ID == device.ID })
				return nil
			})
		case err != nil || status >= 300:
			result, reason = "error", fmt.Sprintf("status_%d", status)
			if err != nil {
				reason = "unreachable"
			}
		}
	}
}

func notifyWebPush(ctx context.Context, device notifyDevice, msg notifyMessage) (int, error) {
	vapid, err := notifyVAPIDPair()
	if err != nil {
		return 0, err
	}
	payload, _ := json.Marshal(msg)
	subscriber := "notifications@example.com" // the library adds mailto: unless it is an https URL
	if public := strings.TrimRight(os.Getenv("PUBLIC_URL"), "/"); strings.HasPrefix(public, "https://") {
		subscriber = public
	}
	send, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := webpush.SendNotificationWithContext(send, payload, &webpush.Subscription{Endpoint: device.Endpoint, Keys: webpush.Keys{P256dh: device.P256dh, Auth: device.Auth}},
		&webpush.Options{Subscriber: subscriber, VAPIDPublicKey: vapid.Public, VAPIDPrivateKey: vapid.Private, TTL: 3600, Urgency: webpush.UrgencyHigh})
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// --- The Mac app's relay of its company servers ---

var notifyRelayKick = make(chan struct{}, 1)

func (api *StreamingAPI) kickNotifyRelays() {
	select {
	case notifyRelayKick <- struct{}{}:
	default:
	}
}

// runNotifyRelays keeps, for this Mac's person while a Mac device is on, its device registered on each company server
// it is signed in to and one stream from each, forwarding their messages to the Mac app; turning the bell off removes
// the device there too.
func (api *StreamingAPI) runNotifyRelays(ctx context.Context) {
	type relay struct {
		device string
		stop   context.CancelFunc
	}
	relays := map[string]relay{} // connection id -> its stream
	registered := map[string]string{}
	for ctx.Err() == nil {
		owner := GetDefaultUserID()
		rec, _ := readNotifyRecord(owner)
		var mac *notifyDevice
		for i := range rec.Devices {
			if rec.Devices[i].Kind == notifyKindMac {
				mac = &rec.Devices[i]
				break
			}
		}
		connections, _ := readDesktopConnections(owner)
		live := map[string]bool{}
		for _, c := range connections {
			live[c.ID] = true
			if mac == nil {
				if device := registered[c.ID]; device != "" {
					call, done := context.WithTimeout(ctx, 15*time.Second)
					if resp, err := crewTeamRemoteDo(call, owner, c, http.MethodDelete, "/api/notifications/devices/"+url.PathEscape(device), "application/json", nil); err == nil {
						resp.Body.Close()
						delete(registered, c.ID)
					}
					done()
				}
				continue
			}
			if registered[c.ID] != mac.ID {
				body, _ := json.Marshal(map[string]any{"id": mac.ID, "kind": notifyKindMac, "label": mac.Label, "sound": mac.Sound})
				call, done := context.WithTimeout(ctx, 15*time.Second)
				resp, err := crewTeamRemoteDo(call, owner, c, http.MethodPost, "/api/notifications/devices", "application/json", bytes.NewReader(body))
				done()
				if err != nil {
					log.Printf("[NOTIFY] relay connection=%.8s event=register_failed reason=%q", c.ID, err)
					continue
				}
				resp.Body.Close()
				registered[c.ID] = mac.ID
			}
			if r, ok := relays[c.ID]; !ok || r.device != mac.ID {
				if ok {
					r.stop()
				}
				streamCtx, stop := context.WithCancel(ctx)
				relays[c.ID] = relay{device: mac.ID, stop: stop}
				go notifyRelayStream(streamCtx, owner, c, mac.ID)
			}
		}
		for id, r := range relays {
			if mac == nil || !live[id] {
				r.stop()
				delete(relays, id)
			}
		}
		select {
		case <-ctx.Done():
		case <-notifyRelayKick:
		case <-time.After(30 * time.Second):
		}
	}
}

// notifyRelayStream reads one company server's stream for this Mac and forwards each message, reconnecting.
func notifyRelayStream(ctx context.Context, owner string, c desktopConnection, device string) {
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for ctx.Err() == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Server+"/api/notifications/stream?device="+url.QueryEscape(device), nil)
		if err == nil {
			// The token may have been renewed since this stream started.
			if records, readErr := readDesktopConnections(owner); readErr == nil {
				for _, record := range records {
					if record.ID == c.ID {
						c = record
					}
				}
			}
			req.Header.Set("Authorization", "Bearer "+c.Token)
			req.Header.Set("Accept", "text/event-stream")
			var resp *http.Response
			if resp, err = client.Do(req); err == nil {
				if resp.StatusCode == http.StatusOK {
					scanner := bufio.NewScanner(resp.Body)
					event := ""
					for scanner.Scan() {
						line := scanner.Text()
						switch {
						case strings.HasPrefix(line, "event: "):
							event = strings.TrimPrefix(line, "event: ")
						case strings.HasPrefix(line, "data: ") && event == "notify":
							var msg notifyMessage
							if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &msg) == nil {
								msg.Connection = c.ID
								notifyStreamSend(notifyMacAppDevice, msg)
								log.Printf("[NOTIFY] relay connection=%.8s event=%s session=%s result=forwarded", c.ID, msg.Event, msg.Session)
							}
						case line == "":
							event = ""
						}
					}
				}
				resp.Body.Close()
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(15 * time.Second):
		}
	}
}
