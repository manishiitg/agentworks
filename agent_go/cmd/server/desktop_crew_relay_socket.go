package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// The Mac keeps one connection per saved server for every Crew it offers to
// the website (relay twins and paired Crews edited here). It pushes events and
// Crew state and answers commands; sleep, wake and network changes reconnect.
func (api *StreamingAPI) startDesktopCrewChats() context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	if !IsLocalMode() {
		return cancel
	}
	go func() {
		workers := map[string]context.CancelFunc{}
		defer func() {
			for _, stop := range workers {
				stop()
			}
		}()
		for {
			live := map[string]bool{}
			for _, crew := range desktopRelayCrews("") {
				live[crew.connection] = true
			}
			for id := range live {
				if workers[id] == nil {
					child, stop := context.WithCancel(ctx)
					workers[id] = stop
					go api.runDesktopCrewRelay(child, id)
				}
			}
			for id, stop := range workers {
				if !live[id] {
					stop()
					delete(workers, id)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
		}
	}()
	return cancel
}

type desktopRelayCrew struct {
	crewRelayCrew
	connection string
}

// desktopRelayCrews lists the Crews this Mac offers, optionally for one connection.
func desktopRelayCrews(connection string) []desktopRelayCrew {
	state, err := workflowCLIStateRoot()
	if err != nil {
		return nil
	}
	entries, _ := os.ReadDir(filepath.Join(state, "crew-sync"))
	var out []desktopRelayCrew
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if entry.IsDir() || !ok || strings.Contains(id, "-files") {
			continue
		}
		b, e := crewroot.ReadSync(id)
		if e != nil || b == nil || b.Side != "local" || b.Editing != "local" || b.PendingAction != "" || b.ConnectionID == "" {
			continue
		}
		if connection != "" && b.ConnectionID != connection {
			continue
		}
		out = append(out, desktopRelayCrew{crewRelayCrew{Project: b.PeerProjectID, Pair: b.PairID}, b.ConnectionID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Project < out[j].Project })
	return out
}

func (api *StreamingAPI) runDesktopCrewRelay(ctx context.Context, connectionID string) {
	wait := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := api.desktopCrewRelaySession(ctx, connectionID)
		if time.Since(started) > time.Minute {
			wait = time.Second
		}
		if ctx.Err() != nil {
			return
		}
		log.Printf("[CREW_RELAY] desktop connection=%s drop reason=%q retry_in=%s", connectionID, fmt0(err), wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait *= 2; wait > 30*time.Second {
			wait = 30 * time.Second
		}
	}
}

func fmt0(err error) string {
	if err == nil {
		return "closed"
	}
	return err.Error()
}

// localCrewForPeer maps a twin's server project back to the local Crew.
func localCrewForPeer(peer, pair string) string {
	state, err := workflowCLIStateRoot()
	if err != nil {
		return ""
	}
	entries, _ := os.ReadDir(filepath.Join(state, "crew-sync"))
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if entry.IsDir() || !ok {
			continue
		}
		if b, e := crewroot.ReadSync(id); e == nil && b != nil && b.Side == "local" && b.PeerProjectID == peer && b.PairID == pair {
			return id
		}
	}
	return ""
}

func (api *StreamingAPI) desktopCrewRelaySession(ctx context.Context, connectionID string) error {
	owner := GetDefaultUserID()
	connection, err := desktopConnectionFor(owner, connectionID)
	if err != nil {
		return err
	}
	server, err := url.Parse(connection.Server)
	if err != nil {
		return err
	}
	server.Scheme = map[string]string{"https": "wss", "http": "ws"}[server.Scheme]
	server.Path = strings.TrimSuffix(server.Path, "/") + "/api/crew-relay/connect"
	conn, response, err := websocket.DefaultDialer.DialContext(ctx, server.String(), http.Header{"Authorization": {"Bearer " + connection.Token}})
	if response != nil {
		if token := response.Header.Get("X-Renewed-Token"); token != "" {
			saveRenewedDesktopToken(owner, connection, token)
		}
	}
	if err != nil {
		return err
	}
	session, stop := context.WithCancel(ctx)
	defer stop()
	defer conn.Close()
	conn.SetReadLimit(4 << 20)
	var writeMu sync.Mutex
	send := func(msg crewRelayMessage) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
		return conn.WriteJSON(msg)
	}
	offered := func() ([]crewRelayCrew, map[string]string) {
		var crews []crewRelayCrew
		pairs := map[string]string{}
		for _, crew := range desktopRelayCrews(connectionID) {
			crews = append(crews, crew.crewRelayCrew)
			pairs[crew.Project] = crew.Pair
		}
		return crews, pairs
	}
	crews, pairs := offered()
	if err = send(crewRelayMessage{Type: "hello", DeviceID: connection.ID, MacName: desktopMacDisplayName(), Crews: crews}); err != nil {
		return err
	}
	log.Printf("[CREW_RELAY] desktop connect connection=%s crews=%d", connectionID, len(crews))
	var pairsMu sync.Mutex
	followers := &desktopRelayFollowers{api: api, send: send, active: map[string]*desktopRelayFollower{}}
	go func() {
		ping := time.NewTicker(15 * time.Second)
		defer ping.Stop()
		states := time.NewTicker(10 * time.Second)
		defer states.Stop()
		reported := map[string]string{}
		for {
			select {
			case <-session.Done():
				return
			case <-ping.C:
				if send(crewRelayMessage{Type: "ping"}) != nil {
					_ = conn.Close()
					return
				}
				// A Crew added or removed since the last hello is offered again.
				next, nextPairs := offered()
				pairsMu.Lock()
				changed := len(nextPairs) != len(pairs)
				for project, pair := range nextPairs {
					changed = changed || pairs[project] != pair
				}
				pairs = nextPairs
				pairsMu.Unlock()
				if changed {
					_ = send(crewRelayMessage{Type: "hello", DeviceID: connection.ID, MacName: desktopMacDisplayName(), Crews: next})
				}
			case <-states.C:
				pairsMu.Lock()
				current := map[string]string{}
				for project, pair := range pairs {
					current[project] = pair
				}
				pairsMu.Unlock()
				for project, pair := range current {
					state := api.desktopCrewRelayState(session, localCrewForPeer(project, pair), pair)
					if state != "" && reported[project] != state {
						reported[project] = state
						_ = send(crewRelayMessage{Type: "state", Project: project, State: state, At: time.Now().UTC().Format(time.RFC3339)})
					}
				}
			}
		}
	}()
	for {
		_ = conn.SetReadDeadline(time.Now().Add(crewRelayDropAfter))
		var msg crewRelayMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return err
		}
		pairsMu.Lock()
		pair := pairs[msg.Project]
		pairsMu.Unlock()
		local := ""
		if pair != "" {
			local = localCrewForPeer(msg.Project, pair)
		}
		switch msg.Type {
		case "command":
			if msg.Call == nil {
				continue
			}
			call := *msg.Call
			go func() {
				callCtx, done := context.WithTimeout(context.WithoutCancel(session), 55*time.Second)
				if call.Operation == "handoff" {
					callCtx, done = context.WithTimeout(context.WithoutCancel(session), 15*time.Minute)
				}
				defer done()
				result := crewChatResult{ID: call.ID, Status: 403, Body: json.RawMessage(`{"error":"This Mac does not offer that Crew."}`)}
				if local != "" {
					result = api.runDesktopCrewChat(callCtx, local, pair, call)
					result.ID = call.ID
				}
				_ = send(crewRelayMessage{Type: "ack", Project: msg.Project, Result: &result})
			}()
		case "resume":
			if local != "" && msg.Conversation != "" {
				followers.watch(session, msg.Project, local, pair, msg.Conversation, msg.Since)
			}
		}
	}
}

// A follower pushes one conversation's new events while someone is viewing it.
type desktopRelayFollower struct {
	until time.Time
}

type desktopRelayFollowers struct {
	sync.Mutex
	api    *StreamingAPI
	send   func(crewRelayMessage) error
	active map[string]*desktopRelayFollower
}

func (f *desktopRelayFollowers) watch(ctx context.Context, project, local, pair, conversation string, since int) {
	key := project + "/" + conversation
	f.Lock()
	if follower := f.active[key]; follower != nil {
		follower.until = time.Now().Add(6 * time.Minute)
		f.Unlock()
		return
	}
	follower := &desktopRelayFollower{until: time.Now().Add(6 * time.Minute)}
	f.active[key] = follower
	f.Unlock()
	go func() {
		defer func() {
			f.Lock()
			delete(f.active, key)
			f.Unlock()
		}()
		for ctx.Err() == nil {
			f.Lock()
			expired := time.Now().After(follower.until)
			f.Unlock()
			if expired {
				return
			}
			result := f.api.runDesktopCrewChat(ctx, local, pair, crewChatCall{ID: "follow", Operation: "events", ConversationID: conversation, Query: url.Values{"since": {strconv.Itoa(since)}, "limit": {"200"}}})
			if result.Status == 409 || result.Status == 403 {
				return
			}
			if result.Status == 200 {
				var envelope struct {
					LastProcessedIndex int               `json:"last_processed_index"`
					Events             []json.RawMessage `json:"events"`
				}
				if json.Unmarshal(result.Body, &envelope) == nil && envelope.LastProcessedIndex > since && len(envelope.Events) > 0 {
					if f.send(crewRelayMessage{Type: "event", Project: project, Conversation: conversation, Seq: envelope.LastProcessedIndex, Event: result.Body}) != nil {
						return
					}
				}
				if envelope.LastProcessedIndex > since {
					since = envelope.LastProcessedIndex
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(700 * time.Millisecond):
			}
		}
	}()
}

// desktopCrewRelayState is the list's working, waiting or idle for one local Crew.
func (api *StreamingAPI) desktopCrewRelayState(ctx context.Context, local, pair string) string {
	if local == "" {
		return ""
	}
	result := api.runDesktopCrewChat(ctx, local, pair, crewChatCall{ID: "state", Operation: "status"})
	if result.Status == http.StatusNotFound {
		return "idle"
	}
	var status struct {
		Status        string `json:"status"`
		DisplayStatus string `json:"display_status"`
	}
	if result.Status != 200 || json.Unmarshal(result.Body, &status) != nil {
		return ""
	}
	switch {
	case strings.Contains(strings.ToLower(status.DisplayStatus), "wait"):
		return "waiting"
	case status.Status == "running" || status.Status == "active" || status.Status == "processing":
		return "working"
	}
	return "idle"
}

func saveRenewedDesktopToken(owner string, connection desktopConnection, token string) {
	desktopConnectionsMu.Lock()
	defer desktopConnectionsMu.Unlock()
	records, err := readDesktopConnections(owner)
	if err != nil {
		return
	}
	for i := range records {
		if records[i].ID == connection.ID && records[i].Token == connection.Token {
			records[i].Token = token
		}
	}
	_ = writeDesktopConnections(owner, records)
}
