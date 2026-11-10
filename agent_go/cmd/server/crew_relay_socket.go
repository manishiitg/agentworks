package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// One connection per Mac carries every Crew it offers to the website: the Mac
// pushes events and Crew state, the server sends commands. Identity is the
// connection's sign-in, never the device ID (PLAT-856-509, contract §2).

type crewRelayCrew struct {
	Project string `json:"project"`
	Pair    string `json:"pair"`
	Title   string `json:"title,omitempty"`
}

type crewRelayMessage struct {
	Type         string          `json:"type"`
	DeviceID     string          `json:"device_id,omitempty"`
	MacName      string          `json:"mac_name,omitempty"`
	Crews        []crewRelayCrew `json:"crews,omitempty"`
	Project      string          `json:"project,omitempty"`
	Conversation string          `json:"conversation,omitempty"`
	Seq          int             `json:"seq,omitempty"`
	Since        int             `json:"since,omitempty"`
	Event        json.RawMessage `json:"event,omitempty"`
	State        string          `json:"state,omitempty"`
	LastLine     string          `json:"last_line,omitempty"`
	At           string          `json:"at,omitempty"`
	Call         *crewChatCall   `json:"call,omitempty"`
	Result       *crewChatResult `json:"result,omitempty"`
}

type crewMacSocket struct {
	owner, device, name string
	claims              *UserClaims
	send                func(crewRelayMessage) error
	close               func()
	mu                  sync.Mutex
	twins               map[string]string // project -> pair
	pending             map[string]chan crewChatResult
	dispatched          map[string]chan struct{}
	lastSeen            time.Time
}

var crewMacSockets = struct {
	sync.Mutex
	byDevice map[string]*crewMacSocket
}{byDevice: map[string]*crewMacSocket{}}

const crewRelayDropAfter = 45 * time.Second

// crewRelayAdmit is the access rule for one Crew a Mac offers: the caller's own
// website sign-in, the actual owner of the twin, and the twin's pair.
func (api *StreamingAPI) crewRelayAdmit(ctx context.Context, claims *UserClaims, crew crewRelayCrew) error {
	if !crewChatWebsiteClaims(claims) {
		return fmt.Errorf("not the owner's website sign-in")
	}
	if crew.Project == "" || crew.Pair == "" {
		return fmt.Errorf("missing project or pair")
	}
	b, err := crewroot.ReadSync(crew.Project)
	// A Team member's Mac offers their working copy with the pair the server recorded for them (crew_team_where.go).
	// On a Crew that lives on the server there is no owner's binding: the member's pair alone admits their Mac.
	if t, e := readCrewTeam(crew.Project); e == nil && err == nil && (b == nil || b.Side == "server") && ownerOfCrewProjectID(crew.Project) == t.OwnerID {
		if pair, ok := t.memberMac(claims.UserID); ok {
			if pair != crew.Pair {
				return fmt.Errorf("not this member's pair")
			}
			return nil
		}
		if t.member(claims.UserID) && claims.UserID != t.OwnerID {
			return fmt.Errorf("member not on their Mac")
		}
	}
	if _, err := api.crewSyncRoots(ctx, claims, crew.Project); err != nil {
		return fmt.Errorf("not the owner")
	}
	if err != nil || b == nil || b.Side != "server" || b.PairID != crew.Pair {
		return fmt.Errorf("not this Crew's pair")
	}
	return nil
}

// crewRelayRegister admits a Mac's connection. A second connection from the same
// person and Mac replaces the first; another account can never displace it.
func (api *StreamingAPI) crewRelayRegister(ctx context.Context, claims *UserClaims, hello crewRelayMessage, send func(crewRelayMessage) error, closeConn func()) (*crewMacSocket, error) {
	if !crewChatWebsiteClaims(claims) {
		return nil, fmt.Errorf("Open AgentWorks on your Mac with your own sign-in.")
	}
	if hello.Type != "hello" || hello.DeviceID == "" || len(hello.DeviceID) > 128 {
		return nil, fmt.Errorf("invalid hello")
	}
	sock := &crewMacSocket{owner: claims.UserID, device: hello.DeviceID, name: hello.MacName, claims: claims, send: send, close: closeConn, twins: map[string]string{}, pending: map[string]chan crewChatResult{}, dispatched: map[string]chan struct{}{}, lastSeen: time.Now()}
	api.crewRelayOffer(ctx, sock, hello.Crews)
	key := claims.UserID + "/" + hello.DeviceID
	crewMacSockets.Lock()
	old := crewMacSockets.byDevice[key]
	crewMacSockets.byDevice[key] = sock
	crewMacSockets.Unlock()
	if old != nil && old.close != nil {
		// Two computers with one saved connection share its device ID and knock each other off; name both.
		log.Printf("[CREW_RELAY] replace owner=%s device=%s old_mac=%q new_mac=%q", sanitizeUserIDForPath(claims.UserID), hello.DeviceID, old.name, sock.name)
		old.mu.Lock()
		for project := range old.twins {
			if crewRelayStreams.clearRunning(old.owner, project) {
				go publishCrewPanelChanged(project)
			}
		}
		old.mu.Unlock()
		old.close()
	}
	log.Printf("[CREW_RELAY] connect owner=%s device=%s mac=%q crews=%d", sanitizeUserIDForPath(claims.UserID), hello.DeviceID, sock.name, len(sock.twins))
	sock.mu.Lock()
	for project := range sock.twins {
		publishCrewPanelChanged(project)
	}
	sock.mu.Unlock()
	api.deliverCrewMacWaitingAll(sock)
	return sock, nil
}

func (api *StreamingAPI) crewRelayOffer(ctx context.Context, sock *crewMacSocket, crews []crewRelayCrew) {
	twins := map[string]string{}
	for _, crew := range crews {
		if err := api.crewRelayAdmit(ctx, sock.claims, crew); err != nil {
			log.Printf("[CREW_RELAY] refused owner=%s device=%s project=%s reason=%q", sock.owner, sock.device, crew.Project, err.Error())
			// The online copy is not this account's (the Mac signed in again as someone else, or it was removed):
			// tell the Mac, which starts a new online copy for this account. The old one is never touched.
			if err.Error() == "not the owner" && sock.send != nil {
				_ = sock.send(crewRelayMessage{Type: "refused", Project: crew.Project, State: "not_owner"})
			}
			continue
		}
		// One Mac serves a Crew: a second computer with a copied setup offers the same pair, and would get its messages.
		if other := crewMacSocketServing(sock, crew.Project, crew.Pair); other != "" {
			log.Printf("[CREW_RELAY] refused owner=%s device=%s mac=%q project=%s reason=%q serving_mac=%q", sock.owner, sock.device, sock.name, crew.Project, "served by another Mac", other)
			if sock.send != nil {
				_ = sock.send(crewRelayMessage{Type: "refused", Project: crew.Project, State: "other_mac"})
			}
			continue
		}
		twins[crew.Project] = crew.Pair
		// A pair made before Macs were named learns its Mac from the Mac that offers it, so lists group it there.
		if b, e := crewroot.ReadSync(crew.Project); e == nil && b != nil && b.MacID == "" && sock.device != "" && ownerOfCrewProjectID(crew.Project) == sock.owner {
			b.MacID, b.MacName = sock.device, sock.name
			if e = crewroot.WriteSync(crew.Project, *b); e == nil {
				log.Printf("[CREW_RELAY] named mac owner=%s device=%s project=%s", sock.owner, sock.device, crew.Project)
			}
		}
	}
	sock.mu.Lock()
	for project := range sock.twins {
		publishCrewPanelChanged(project)
	}
	sock.twins = twins
	for project := range twins {
		publishCrewPanelChanged(project)
	}
	sock.mu.Unlock()
}

func (sock *crewMacSocket) unregister() {
	key := sock.owner + "/" + sock.device
	crewMacSockets.Lock()
	current := crewMacSockets.byDevice[key] == sock
	if current {
		delete(crewMacSockets.byDevice, key)
	}
	crewMacSockets.Unlock()
	sock.mu.Lock()
	for project := range sock.twins {
		crewMacLastHeard.Store(project, sock.lastSeen)
		// A replaced socket's chats were cleared when its replacement arrived, which reports them afresh.
		if current {
			crewRelayStreams.clearRunning(sock.owner, project)
		}
		publishCrewPanelChanged(project)
	}
	for id, result := range sock.pending {
		select {
		case result <- crewChatResult{ID: id, Status: 503, Body: json.RawMessage(`{"error":"Your Mac disconnected. Reopen the chat to check whether your message arrived before sending it again."}`)}:
		default:
		}
	}
	sock.mu.Unlock()
}

// crewMacSocketFor routes a twin's commands only to a connection of its owner
// that offered this pair.
// crewMacSocketServing is the name of another live connection of this Mac's owner, from a different computer, that
// already serves this Crew pair ("" when none).
func crewMacSocketServing(sock *crewMacSocket, project, pair string) string {
	crewMacSockets.Lock()
	defer crewMacSockets.Unlock()
	for _, other := range crewMacSockets.byDevice {
		if other == sock || other.owner != sock.owner || other.device == sock.device {
			continue
		}
		other.mu.Lock()
		offered, seen := other.twins[project], other.lastSeen
		other.mu.Unlock()
		if offered == pair && time.Since(seen) <= crewRelayDropAfter {
			return firstNonEmptyTrimmed(other.name, other.device)
		}
	}
	return ""
}

func crewMacSocketFor(owner, project, pair string) *crewMacSocket {
	crewMacSockets.Lock()
	defer crewMacSockets.Unlock()
	var best *crewMacSocket
	var bestSeen time.Time
	for _, sock := range crewMacSockets.byDevice {
		if sock.owner != owner {
			continue
		}
		sock.mu.Lock()
		offered, seen := sock.twins[project], sock.lastSeen
		sock.mu.Unlock()
		if offered == pair && time.Since(seen) <= crewRelayDropAfter && seen.After(bestSeen) {
			best, bestSeen = sock, seen
		}
	}
	return best
}

// crewMacLastHeard is when a Crew's Mac was last heard from before its connection closed (the Mac online card's
// "Last seen"), kept only while this server runs.
var crewMacLastHeard sync.Map

// crewMacOffSince is when the Crew's Mac was last heard from while it is offline; zero when unknown.
func crewMacOffSince(project string) time.Time {
	at, _ := crewMacLastHeard.Load(project)
	seen, _ := at.(time.Time)
	return seen
}

// crewMacLastSeen is when the twin's Mac was last heard from; zero if not since the server started.
func crewMacLastSeen(owner, project, pair string) time.Time {
	if sock := crewMacSocketFor(owner, project, pair); sock != nil {
		sock.mu.Lock()
		defer sock.mu.Unlock()
		return sock.lastSeen
	}
	return time.Time{}
}

// crewRelayHandle applies one message from a Mac. Anything about a Crew the
// connection did not offer, or that it may not offer, is dropped and logged.
func (api *StreamingAPI) crewRelayHandle(ctx context.Context, sock *crewMacSocket, msg crewRelayMessage) {
	sock.mu.Lock()
	sock.lastSeen = time.Now()
	pair, offered := sock.twins[msg.Project]
	sock.mu.Unlock()
	switch msg.Type {
	case "ping":
		_ = sock.send(crewRelayMessage{Type: "pong"})
		return
	case "hello":
		api.crewRelayOffer(ctx, sock, msg.Crews)
		api.deliverCrewMacWaitingAll(sock)
		// Always on: agents the server answered for while this Mac was off go back to it (crew_agent_handback.go).
		offerCrewHandbacks(sock)
		return
	case "ack":
		if msg.Result == nil {
			return
		}
		sock.mu.Lock()
		result := sock.pending[msg.Result.ID]
		sock.mu.Unlock()
		if result != nil {
			select {
			case result <- *msg.Result:
			default:
			}
		}
		return
	}
	if !offered {
		log.Printf("[CREW_RELAY] refused owner=%s device=%s project=%s reason=%q", sock.owner, sock.device, msg.Project, "not offered by this connection")
		return
	}
	switch msg.Type {
	case "running":
		// The Mac's own report that a turn started or ended in one of its chats (desktop_crew_relay_socket.go).
		if msg.Conversation != "" && (msg.State == "running" || msg.State == "idle") {
			crewRelayStreams.setRunning(sock.owner, msg.Project, msg.Conversation, msg.State == "running")
		}
	case "event":
		if msg.Conversation != "" && json.Valid(msg.Event) {
			crewRelayStreams.append(sock.owner, msg.Project, msg.Conversation, crewRelayBatch{Kind: "event", Seq: msg.Seq, Body: msg.Event})
		}
	case "state":
		if owner := ownerOfCrewProjectID(msg.Project); owner != "" && owner != sock.owner {
			return // the Crew's Mac state is its owner's Mac; a member's Mac reports through its own agents
		}
		defer publishCrewPanelChanged(msg.Project)
		saveCrewMacState(msg.Project, crewMacState{State: msg.State, LastLine: msg.LastLine, At: msg.At})
		if msg.State == "working" {
			crewRelaySubmissions.working(sock.owner, msg.Project)
		}
	case "crew.removed":
		defer publishCrewPanelChanged(msg.Project)
		sock.mu.Lock()
		delete(sock.twins, msg.Project)
		sock.mu.Unlock()
		log.Printf("[CREW_RELAY] removed owner=%s device=%s project=%s pair=%s", sock.owner, sock.device, msg.Project, pair)
	default:
		log.Printf("[CREW_RELAY] refused owner=%s device=%s project=%s reason=%q", sock.owner, sock.device, msg.Project, "unknown message "+msg.Type)
	}
}

// crewRelayCommand sends one command and waits for the Mac's result.
func (sock *crewMacSocket) command(ctx context.Context, project string, call crewChatCall) (crewChatResult, bool, error) {
	call.ID = uuid.NewString()
	result := make(chan crewChatResult, 1)
	sock.mu.Lock()
	sock.pending[call.ID] = result
	sock.mu.Unlock()
	defer func() {
		sock.mu.Lock()
		delete(sock.pending, call.ID)
		sock.mu.Unlock()
	}()
	if err := sock.send(crewRelayMessage{Type: "command", Project: project, Call: &call}); err != nil {
		return crewChatResult{}, false, err
	}
	select {
	case got := <-result:
		return got, true, nil
	case <-ctx.Done():
		return crewChatResult{}, true, ctx.Err()
	}
}

func (api *StreamingAPI) handleCrewRelayConnect(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if !crewChatWebsiteClaims(claims) {
		externalError(w, 403, "mac_chat_forbidden", "Open AgentWorks on your Mac with your own sign-in.")
		return
	}
	upgrader := websocket.Upgrader{ReadBufferSize: 8192, WriteBufferSize: 8192}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(crewChatMaxReply + 64<<10)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var hello crewRelayMessage
	if err = conn.ReadJSON(&hello); err != nil {
		return
	}
	var writeMu sync.Mutex
	send := func(msg crewRelayMessage) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
		return conn.WriteJSON(msg)
	}
	sock, err := api.crewRelayRegister(r.Context(), claims, hello, send, func() { _ = conn.Close() })
	if err != nil {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, err.Error()), time.Now().Add(time.Second))
		return
	}
	defer sock.unregister()
	reason := "closed"
	for {
		_ = conn.SetReadDeadline(time.Now().Add(crewRelayDropAfter))
		var msg crewRelayMessage
		if err := conn.ReadJSON(&msg); err != nil {
			reason = err.Error()
			break
		}
		api.crewRelayHandle(r.Context(), sock, msg)
	}
	log.Printf("[CREW_RELAY] drop owner=%s device=%s reason=%q", sanitizeUserIDForPath(sock.owner), sock.device, reason)
}

// The last Crew state a Mac reported, kept on disk so a Mac that is off still
// shows where it stood.
type crewMacState struct {
	State    string `json:"state"`
	LastLine string `json:"last_line,omitempty"`
	At       string `json:"at,omitempty"`
}

func crewMacStatePath(project string) (string, error) {
	p, err := crewSyncManifestPath(project)
	if err != nil {
		return "", err
	}
	return p + ".mac-state", nil
}

func saveCrewMacState(project string, state crewMacState) {
	if state.State != "working" && state.State != "idle" && state.State != "waiting" {
		return
	}
	if len(state.LastLine) > 280 {
		state.LastLine = state.LastLine[:280]
	}
	if p, err := crewMacStatePath(project); err == nil {
		_ = os.MkdirAll(filepath.Dir(p), 0700)
		_ = fsutil.WriteJSONAtomic(p, state, 0600)
	}
}

func readCrewMacState(project string) *crewMacState {
	p, err := crewMacStatePath(project)
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(p)
	var state crewMacState
	if err != nil || json.Unmarshal(raw, &state) != nil {
		return nil
	}
	return &state
}

// Recent events per conversation (contract §3): the last 1,000 batches or 8 MB,
// kept 24 hours after the last one. Viewers read the cache; the Mac pays nothing extra.
type crewRelayBatch struct {
	Kind string
	Seq  int
	Body json.RawMessage
}

type crewRelayStream struct {
	batches []crewRelayBatch
	bytes   int
	last    time.Time
	// running: a turn is going in this chat (its latest lifecycle event is a start); since is when that event arrived.
	running bool
	since   time.Time
	subs    map[chan crewRelayBatch]struct{}
}

type crewRelayStreamSet struct {
	sync.Mutex
	streams map[string]*crewRelayStream
}

var crewRelayStreams = &crewRelayStreamSet{streams: map[string]*crewRelayStream{}}

func (s *crewRelayStreamSet) get(key string) *crewRelayStream {
	stream := s.streams[key]
	if stream == nil {
		stream = &crewRelayStream{subs: map[chan crewRelayBatch]struct{}{}}
		s.streams[key] = stream
	}
	return stream
}

// setRunning records a Mac's own word that a turn started or ended in one of its chats, so teammates see the agent
// working without anyone having it open (a chat's events reach the server only while someone watches it).
func (s *crewRelayStreamSet) setRunning(owner, project, conversation string, running bool) {
	s.Lock()
	defer s.Unlock()
	stream := s.get(owner + "/" + project + "/" + conversation)
	stream.last = time.Now()
	if stream.running != running {
		stream.running, stream.since = running, time.Now()
		log.Printf("[CREW_RELAY] turn owner=%s project=%s conversation=%s running=%t", sanitizeUserIDForPath(owner), project, conversation, running)
		go publishCrewPanelChanged(project)
	}
}

// clearRunning: the Mac that reported these chats is gone, so none of them is working any more (a turn it never
// reported the end of would otherwise show a teammate's agent working forever).
func (s *crewRelayStreamSet) clearRunning(owner, project string) bool {
	s.Lock()
	defer s.Unlock()
	prefix, cleared := owner+"/"+project+"/", false
	for key, stream := range s.streams {
		if stream.running && strings.HasPrefix(key, prefix) {
			stream.running, stream.since, cleared = false, time.Now(), true
		}
	}
	return cleared
}

func (s *crewRelayStreamSet) append(owner, project, conversation string, batch crewRelayBatch) {
	s.Lock()
	defer s.Unlock()
	for key, stream := range s.streams {
		if len(stream.subs) == 0 && time.Since(stream.last) > 24*time.Hour {
			delete(s.streams, key)
		}
	}
	stream := s.get(owner + "/" + project + "/" + conversation)
	if batch.Kind == "event" && len(stream.batches) > 0 && batch.Seq <= crewRelayLastSeq(stream.batches) {
		return // already cached; a resumed Mac may resend
	}
	stream.batches = append(stream.batches, batch)
	stream.bytes += len(batch.Body)
	stream.last = time.Now()
	if batch.Kind == "event" {
		if now, known := crewRelayBatchRunning(batch.Body); known && now != stream.running {
			stream.running, stream.since = now, time.Now()
			// The Team tab reads this state: tell it the moment an agent starts or stops working.
			go publishCrewPanelChanged(project)
		}
	}
	for len(stream.batches) > 1000 || stream.bytes > 8<<20 {
		stream.bytes -= len(stream.batches[0].Body)
		stream.batches = stream.batches[1:]
	}
	for sub := range stream.subs {
		select {
		case sub <- batch:
		default: // a stalled viewer resumes from its last event number
		}
	}
}

// forget drops every cached chat of one Crew, so nothing of it is served after its online copy is removed.
func (s *crewRelayStreamSet) forget(owner, project string) {
	s.Lock()
	defer s.Unlock()
	for key := range s.streams {
		if strings.HasPrefix(key, owner+"/"+project+"/") {
			delete(s.streams, key)
		}
	}
}

func crewRelayLastSeq(batches []crewRelayBatch) int {
	for i := len(batches) - 1; i >= 0; i-- {
		if batches[i].Kind == "event" {
			return batches[i].Seq
		}
	}
	return 0
}

// subscribe returns cached batches after since and a channel for new ones.
func (s *crewRelayStreamSet) subscribe(owner, project, conversation string, since int) ([]crewRelayBatch, bool, chan crewRelayBatch, func()) {
	s.Lock()
	defer s.Unlock()
	key := owner + "/" + project + "/" + conversation
	stream := s.get(key)
	var replay []crewRelayBatch
	gap := since > 0 && len(stream.batches) > 0
	for _, batch := range stream.batches {
		if batch.Seq > since || batch.Kind != "event" && batch.Seq >= since {
			replay = append(replay, batch)
		}
		if batch.Kind == "event" && batch.Seq <= since {
			gap = false
		}
	}
	sub := make(chan crewRelayBatch, 64)
	stream.subs[sub] = struct{}{}
	return replay, gap, sub, func() {
		s.Lock()
		delete(stream.subs, sub)
		s.Unlock()
	}
}

// Message states for messages sent from the website (contract §4).
type crewRelaySubmissionSet struct {
	sync.Mutex
	latest map[string]struct{ conversation, submission string }
}

var crewRelaySubmissions = &crewRelaySubmissionSet{latest: map[string]struct{ conversation, submission string }{}}

func crewRelayMessageState(owner, project, conversation, submission, state, reason string) {
	if submission == "" {
		return
	}
	body, _ := json.Marshal(map[string]string{"submission_id": submission, "conversation": conversation, "state": state, "at": time.Now().UTC().Format(time.RFC3339), "reason": reason})
	crewRelayStreams.Lock()
	seq := crewRelayLastSeq(crewRelayStreams.get(owner + "/" + project + "/" + conversation).batches)
	crewRelayStreams.Unlock()
	crewRelayStreams.append(owner, project, conversation, crewRelayBatch{Kind: "message_state", Seq: seq, Body: body})
	crewRelaySubmissions.Lock()
	if state == "received" {
		crewRelaySubmissions.latest[owner+"/"+project] = struct{ conversation, submission string }{conversation, submission}
	} else if state != "sent" {
		delete(crewRelaySubmissions.latest, owner+"/"+project)
	}
	crewRelaySubmissions.Unlock()
	log.Printf("[CREW_RELAY] message owner=%s project=%s conversation=%s submission=%s state=%s", owner, project, conversation, submission, state)
}

func (s *crewRelaySubmissionSet) working(owner, project string) {
	s.Lock()
	latest, ok := s.latest[owner+"/"+project]
	s.Unlock()
	if ok {
		crewRelayMessageState(owner, project, latest.conversation, latest.submission, "working", "")
	}
}

func crewRelaySince(r *http.Request) int {
	value := r.Header.Get("Last-Event-ID")
	if value == "" {
		value = r.URL.Query().Get("since")
	}
	since, _ := strconv.Atoi(value)
	return since
}
