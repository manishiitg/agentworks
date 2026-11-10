package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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
	// Copies waiting in a debounce when the app last stopped were lost with it: offer every shared agent once.
	time.AfterFunc(crewStandbyStartupDelay, api.catchUpCrewStandby)
	go func() {
		workers := map[string]context.CancelFunc{}
		defer func() {
			for _, stop := range workers {
				stop()
			}
		}()
		for {
			followSavedConnections(GetDefaultUserID())
			live := map[string]bool{}
			for _, crew := range desktopRelayCrews("") {
				live[crew.connection] = true
			}
			for id := range live {
				if workers[id] == nil {
					child, stop := context.WithCancel(ctx)
					workers[id] = stop
					log.Printf("[CREW_RELAY] desktop connection=%s start: a Crew has Reach on", id)
					go api.runDesktopCrewRelay(child, id)
				}
			}
			for id, stop := range workers {
				if !live[id] {
					// The Mac stops reaching this server when no Crew on it has Reach on; say so, it is otherwise silent.
					log.Printf("[CREW_RELAY] desktop connection=%s stop: no Crew has Reach on for it", id)
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

// followSavedConnections moves a Crew whose saved server connection was removed onto the one connection now saved
// for that same server; signing in again otherwise left it retrying a dead connection forever, off the phone and
// unsyncable (PLAT-856-509). The server still checks the pair against the new connection's account. With no
// connection, or several, for that server the Crew is left as it is.
func followSavedConnections(owner string) {
	connections, err := readDesktopConnections(owner)
	if err != nil {
		return
	}
	saved := map[string]bool{}
	byServer := map[string][]string{}
	for _, c := range connections {
		saved[c.ID] = true
		server := strings.TrimSuffix(c.Server, "/")
		byServer[server] = append(byServer[server], c.ID)
	}
	state, err := workflowCLIStateRoot()
	if err != nil {
		return
	}
	entries, _ := os.ReadDir(filepath.Join(state, "crew-sync"))
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if entry.IsDir() || !ok || strings.Contains(id, "-files") {
			continue
		}
		b, e := crewroot.ReadSync(id)
		if e != nil || b == nil || b.Side != "local" || b.ConnectionID == "" || saved[b.ConnectionID] {
			continue
		}
		current := byServer[strings.TrimSuffix(b.PeerServer, "/")]
		if len(current) != 1 {
			continue
		}
		old := b.ConnectionID
		b.ConnectionID = current[0]
		if e = crewroot.WriteSync(id, *b); e != nil {
			log.Printf("[CREW_RELAY] project=%s could not follow connection %s to %s: %v", id, old, current[0], e)
			continue
		}
		log.Printf("[CREW_RELAY] project=%s pair=%s followed removed connection=%s to connection=%s", id, b.PairID, old, current[0])
	}
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
		// A Crew being moved online is no longer offered by this Mac.
		if e != nil || b == nil || b.Side != "local" || b.Editing != "local" || b.PendingAction == "server" || b.ConnectionID == "" {
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
			_ = saveRenewedDesktopToken(owner, connection, token)
		}
	}
	if err != nil {
		return err
	}
	session, stop := context.WithCancel(ctx)
	defer stop()
	defer conn.Close()
	// Stopping (app quit, last Crew removed) closes the socket now, not after the read timeout.
	go func() {
		<-session.Done()
		_ = conn.Close()
	}()
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
	if err = send(crewRelayMessage{Type: "hello", DeviceID: desktopRelayDeviceID(connection.ID), MacName: desktopMacDisplayName(), Crews: crews}); err != nil {
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
		turns := time.NewTicker(3 * time.Second)
		defer turns.Stop()
		reported := map[string]string{}
		running := map[string]bool{} // project/conversation the server was last told is working
		chats := map[string]*desktopCrewChatList{}
		for {
			select {
			case <-session.Done():
				return
			case <-turns.C:
				// Teammates see an agent working without opening it: say when a turn starts or ends in each chat.
				pairsMu.Lock()
				current := map[string]string{}
				for project, pair := range pairs {
					current[project] = pair
				}
				pairsMu.Unlock()
				now := map[string]bool{}
				for project, pair := range current {
					if chats[project] == nil {
						chats[project] = &desktopCrewChatList{}
					}
					for _, conversation := range api.desktopCrewRunningChats(session, localCrewForPeer(project, pair), pair, chats[project]) {
						now[project+"/"+conversation] = true
					}
				}
				for key := range now {
					if !running[key] {
						project, conversation, _ := strings.Cut(key, "/")
						log.Printf("[CREW_RELAY] turn project=%s conversation=%s state=running", project, conversation)
						_ = send(crewRelayMessage{Type: "running", Project: project, Conversation: conversation, State: "running"})
					}
				}
				for key := range running {
					if !now[key] {
						project, conversation, _ := strings.Cut(key, "/")
						log.Printf("[CREW_RELAY] turn project=%s conversation=%s state=idle", project, conversation)
						_ = send(crewRelayMessage{Type: "running", Project: project, Conversation: conversation, State: "idle"})
					}
				}
				running = now
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
					_ = send(crewRelayMessage{Type: "hello", DeviceID: desktopRelayDeviceID(connection.ID), MacName: desktopMacDisplayName(), Crews: next})
				}
			case <-states.C:
				pairsMu.Lock()
				current := map[string]string{}
				for project, pair := range pairs {
					current[project] = pair
				}
				pairsMu.Unlock()
				for project, pair := range current {
					local := localCrewForPeer(project, pair)
					state := api.desktopCrewRelayState(session, local, pair)
					if state != "" && reported[project] != state {
						reported[project] = state
						_ = send(crewRelayMessage{Type: "state", Project: project, State: state, LastLine: api.desktopCrewLastLine(session, local, pair), At: time.Now().UTC().Format(time.RFC3339)})
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
				defer done()
				result := crewChatResult{ID: call.ID, Status: 403, Body: json.RawMessage(`{"error":"This Mac does not offer that Crew."}`)}
				if local != "" {
					result = api.runDesktopCrewChat(callCtx, local, pair, call)
					result.ID = call.ID
				}
				_ = send(crewRelayMessage{Type: "ack", Project: msg.Project, Result: &result})
			}()
		case "refused":
			if local != "" && msg.State == "not_owner" {
				api.repairDesktopCrewTwin(session, local, pair, connectionID)
			}
			if msg.State == "other_mac" {
				log.Printf("[CREW_RELAY] project=%s not reached from this computer: another of your computers serves it", msg.Project)
			}
		case "resume":
			if local != "" && msg.Conversation != "" {
				followers.watch(session, msg.Project, local, pair, msg.Conversation, msg.Since)
			}
		case "handback":
			// Always on: the server answered for this agent while the Mac was off; take it back now.
			if local != "" && crewMacTabID(msg.State) {
				go api.handBackCrewStandby(local, msg.State)
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
	return desktopCrewChatState(api.runDesktopCrewChat(ctx, local, pair, crewChatCall{ID: "state", Operation: "status"}))
}

// desktopCrewChatState words one chat's status reply: working, waiting or idle ("" when unreadable).
func desktopCrewChatState(result crewChatResult) string {
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

// desktopCrewChatList is one local Crew's chats (Main and its tabs), reread every 30 seconds.
type desktopCrewChatList struct {
	at  time.Time
	ids []string
}

// desktopCrewRunningChats is the chats of one local Crew with a turn going now.
func (api *StreamingAPI) desktopCrewRunningChats(ctx context.Context, local, pair string, list *desktopCrewChatList) []string {
	if local == "" {
		return nil
	}
	if time.Since(list.at) > 30*time.Second {
		ids := []string{}
		if main := api.runDesktopCrewChat(ctx, local, pair, crewChatCall{ID: "turns-main", Operation: "resolve"}); main.Status == http.StatusOK {
			var out struct {
				ConversationID string `json:"conversation_id"`
			}
			if json.Unmarshal(main.Body, &out) == nil && out.ConversationID != "" {
				ids = append(ids, out.ConversationID)
			}
		}
		if tabs := api.runDesktopCrewChat(ctx, local, pair, crewChatCall{ID: "turns-tabs", Operation: "tabs"}); tabs.Status == http.StatusOK {
			var rows []struct {
				ConversationID string `json:"conversation_id"`
			}
			if json.Unmarshal(tabs.Body, &rows) == nil {
				for _, row := range rows {
					if row.ConversationID != "" {
						ids = append(ids, row.ConversationID)
					}
				}
			}
		}
		list.at, list.ids = time.Now(), ids
	}
	out := []string{}
	for _, conversation := range list.ids {
		if desktopCrewChatState(api.runDesktopCrewChat(ctx, local, pair, crewChatCall{ID: "turns", Operation: "status", ConversationID: conversation})) == "working" {
			out = append(out, conversation)
		}
	}
	return out
}

// desktopCrewLastLine is the last line of the main chat's latest reply (or the
// person's last message), for the phone's Crew list.
func (api *StreamingAPI) desktopCrewLastLine(ctx context.Context, local, pair string) string {
	if local == "" {
		return ""
	}
	result := api.runDesktopCrewChat(ctx, local, pair, crewChatCall{ID: "last-line", Operation: "events", Query: url.Values{"view": {"messages"}, "durable_chat": {"1"}, "limit": {"2"}}})
	if result.Status != 200 {
		return ""
	}
	return crewEventsLastLine(result.Body)
}

func crewEventsLastLine(raw []byte) string {
	var envelope struct {
		Events []struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		} `json:"events"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return ""
	}
	for _, kinds := range [][]string{{"unified_completion", "conversation_end", "agent_end"}, {"user_message"}} {
		for i := len(envelope.Events) - 1; i >= 0; i-- {
			event := envelope.Events[i]
			if !slices.Contains(kinds, event.Type) {
				continue
			}
			var data any
			if json.Unmarshal(event.Data, &data) == nil {
				if text := crewEventText(data, 0); text != "" {
					lines := strings.Split(strings.TrimSpace(text), "\n")
					line := strings.TrimSpace(lines[len(lines)-1])
					if runes := []rune(line); len(runes) > 280 {
						line = string(runes[:279]) + "…"
					}
					return line
				}
			}
		}
	}
	return ""
}

func crewEventText(value any, depth int) string {
	object, ok := value.(map[string]any)
	if !ok || depth > 3 {
		return ""
	}
	for _, key := range []string{"final_result", "content", "display_content", "message", "text"} {
		if text, ok := object[key].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	for _, key := range []string{"data", "result", "response"} {
		if text := crewEventText(object[key], depth+1); text != "" {
			return text
		}
	}
	return ""
}

// As base64 in JSON this is about 6.7 MB; it must stay under crewChatMaxReply (8 MB).
const crewRelayMaxFile = 5 << 20

// desktopCrewFile returns one file of the Crew's folder for its owner's phone
// (load on tap). os.Root refuses ".." and symlinks that leave the folder.
func (api *StreamingAPI) desktopCrewFile(ctx context.Context, claims *UserClaims, project string, call crewChatCall) crewChatResult {
	fail := func(code int, message string) crewChatResult {
		body, _ := json.Marshal(map[string]string{"error": message})
		return crewChatResult{ID: call.ID, Status: code, Body: body}
	}
	roots, err := api.crewSyncRoots(ctx, claims, project)
	if err != nil {
		return fail(403, "This Crew is unavailable on your Mac.")
	}
	rel := strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+call.Path)), "/")
	// Reply cards name files relative to the Crew's files, with "_files/" in front when they live
	// in a chosen folder (crew_file_snapshots.go); accept them exactly as the card gives them.
	if roots.SeparateFiles() {
		rel = strings.TrimPrefix(rel, crewroot.FilesSegment+"/")
	}
	if rel == "" || rel == "." {
		return fail(400, "Choose a file.")
	}
	root, err := os.OpenRoot(roots.Files)
	if err != nil {
		return fail(404, "The Crew's folder is unavailable on your Mac.")
	}
	defer root.Close()
	file, err := root.Open(rel)
	if err != nil {
		return fail(404, "That file is not in this Crew's folder.")
	}
	defer file.Close()
	info, err := file.Stat()
	// Only regular files: a FIFO or device would block the read and hold the command.
	if err != nil || !info.Mode().IsRegular() {
		return fail(404, "That file is not in this Crew's folder.")
	}
	if info.Size() > crewRelayMaxFile {
		return fail(413, "This file is too large to open from your phone. Open it on your Mac.")
	}
	data, err := io.ReadAll(io.LimitReader(file, crewRelayMaxFile+1))
	if err != nil {
		return fail(500, "Could not read the file on your Mac.")
	}
	body, _ := json.Marshal(map[string]any{"path": rel, "size": len(data), "content_base64": data})
	return crewChatResult{ID: call.ID, Status: 200, Body: body}
}

// repairDesktopCrewTwin forgets a Crew's online copy that the server says belongs to another account, so the twin
// loop pairs the Crew again for the account this Mac is signed in with (PLAT-856-509). The pairing and its file
// record are kept as .bak; the Crew's files and chats on this Mac are untouched, and nothing is sent to the old copy.
func (api *StreamingAPI) repairDesktopCrewTwin(ctx context.Context, local, pair, connection string) {
	b, err := crewroot.ReadSync(local)
	if err != nil || b == nil || b.Side != "local" || b.Editing != "local" || b.PendingAction != "" || b.PairID != pair {
		return
	}
	// A member's working copy belongs to the owner's online copy; a refusal means the server no longer takes it here.
	if readCrewTeamState(local).MemberOf != "" {
		log.Printf("[CREW_TEAM_LOCAL] project=%s event=member_pair_refused", local)
		return
	}
	state, err := workflowCLIStateRoot()
	if err != nil {
		return
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	dir := filepath.Join(state, "crew-sync")
	for _, name := range []string{local + "-files.json", local + ".json"} {
		path := filepath.Join(dir, name)
		if _, e := os.Stat(path); e != nil {
			continue
		}
		if e := os.Rename(path, path+"."+stamp+".bak"); e != nil {
			log.Printf("[CREW_RELAY] project=%s could not repair twin: %v", local, e)
			return
		}
	}
	log.Printf("[CREW_RELAY] project=%s repaired twin for connection=%s old_pair=%s old_server_project=%s", local, connection, pair, b.PeerProjectID)
	// Pair it again now, on the connection it already used. If that fails, the Crew shows Offline until its owner turns
	// Reach on again.
	owner := GetDefaultUserID()
	saved, err := desktopConnectionFor(owner, connection)
	if err != nil {
		log.Printf("[CREW_RELAY] project=%s stays offline: connection %s unavailable", local, connection)
		return
	}
	_, manifest, ok := api.resolveExternalWorkspace(ctx, &UserClaims{UserID: owner, Username: owner}, local)
	if !ok {
		log.Printf("[CREW_RELAY] project=%s stays offline: Crew unavailable", local)
		return
	}
	if err := api.createDesktopCrewRelayTwin(ctx, owner, saved, local, manifest.displayTitle()); err != nil {
		log.Printf("[CREW_RELAY] twin project=%s failed: %v", local, err)
	}
}
