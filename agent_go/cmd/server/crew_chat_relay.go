package server

// A Mac Crew's owner can use its Mac conversation from the website. The server
// keeps only recent events and in-flight commands; history and execution stay
// on the Mac (transport: crew_relay_socket.go).
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

const pairedMacSessionPrefix = "paired-mac-"
const crewChatMaxReply = 8 << 20

type crewChatCall struct {
	SubmissionID   string          `json:"submission_id,omitempty"`
	ConversationID string          `json:"conversation_id,omitempty"`
	ID             string          `json:"id"`
	Operation      string          `json:"operation"`
	Message        string          `json:"message,omitempty"`
	DeliveryMode   string          `json:"delivery_mode,omitempty"`
	Query          url.Values      `json:"query,omitempty"`
	Answer         json.RawMessage `json:"answer,omitempty"`
	Path           string          `json:"path,omitempty"`
	// Workspace: the owner's own AI app call (ask, read_chat, reply_input), run on the Mac as its named tab.
	Action string          `json:"action,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
	Caller string          `json:"caller,omitempty"`
	// Tab: one of the Mac Crew's own side tabs (the <id> of "<project>:chat:<id>"); empty is Main. Name names a new one.
	Tab  string `json:"tab,omitempty"`
	Name string `json:"name,omitempty"`
}

// One rule on both sides for a tab id carried over the relay: the ids the app makes (8 characters of a UUID, or
// "agent-"/"asuser-" plus one), never path characters.
var crewMacTabPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func crewMacTabID(id string) bool { return crewMacTabPattern.MatchString(id) }

// crewMacTabName is a new tab's name as the Mac's own + New agent takes it: plain text, trimmed, at most 60 characters.
func crewMacTabName(name string) string {
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name))
	if utf8.RuneCountInString(name) > 60 {
		name = string([]rune(name)[:60])
	}
	return name
}

type crewChatResult struct {
	ID     string          `json:"id"`
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

func crewChatWebsiteClaims(c *UserClaims) bool {
	return c != nil && c.AccessToken == nil && personalLaptopSettingsClaims(c) && websiteDeviceClaims(c)
}

func (api *StreamingAPI) crewChatPair(r *http.Request, project, pair string) (*crewroot.SyncBinding, error) {
	return api.crewChatPairState(r, project, pair, false)
}

func (api *StreamingAPI) crewChatPairState(r *http.Request, project, pair string, completion bool) (*crewroot.SyncBinding, error) {
	c := GetUserFromContext(r.Context())
	if !crewChatWebsiteClaims(c) || !crewMacTeamWatchAllowed(r, project) {
		return nil, fmt.Errorf("Open this Crew using its owner's website sign-in.")
	}
	// A Team member whose agents work on their Mac reads (only reads) their own Mac's chats here, through their pair.
	if pair, ok := crewTeamMemberMacRead(r, project, c.UserID); ok {
		b, err := crewroot.ReadSync(project)
		if err == nil && b == nil {
			b = &crewroot.SyncBinding{Mode: "relay", Side: "server", Editing: "local"} // a Crew on the server
		}
		if err != nil || b == nil || b.Side != "server" {
			return nil, fmt.Errorf("This Crew is not currently connected to its Mac conversation. Reopen the Crew.")
		}
		member := *b
		member.PairID = pair
		return &member, nil
	}
	if _, err := api.crewSyncRoots(r.Context(), c, project); err != nil {
		return nil, err
	}
	b, err := crewroot.ReadSync(project)
	if err != nil || b == nil || b.Side != "server" || (!completion && b.Editing != "local") || b.PendingAction != "" || (pair != "" && b.PairID != pair) {
		return nil, fmt.Errorf("This Crew is not currently connected to its Mac conversation. Reopen the Crew.")
	}
	return b, nil
}

func (api *StreamingAPI) callCrewMac(r *http.Request, project string, call crewChatCall) crewChatResult {
	failure := func(code int, message string) crewChatResult {
		data, _ := json.Marshal(map[string]any{"error": message})
		return crewChatResult{Status: code, Body: data}
	}
	b, err := api.crewChatPair(r, project, "")
	if err != nil {
		return failure(403, err.Error())
	}
	sock := crewMacSocketFor(GetUserIDFromContext(r.Context()), project, b.PairID)
	if sock == nil {
		return failure(503, crewMacOffMessage)
	}
	timeout := 55 * time.Second
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	result, _, err := sock.command(ctx, project, call)
	if err != nil {
		return failure(504, "Your Mac did not respond. Reopen the chat to check whether your message arrived before sending it again.")
	}
	if _, err := api.crewChatPair(r, project, b.PairID); err != nil {
		return failure(403, err.Error())
	}
	if call.Operation == "send" || call.Operation == "resolve" {
		log.Printf("[CREW_CHAT_RELAY] completed owner=%s project=%s operation=%s request=%s status=%d", GetUserIDFromContext(r.Context()), project, call.Operation, result.ID, result.Status)
	}
	// The browser addresses the pair. Native IDs remain in the local
	// journal; a top-level runtime ID must not redirect its event cursor.
	var envelope map[string]json.RawMessage
	if json.Unmarshal(result.Body, &envelope) == nil && envelope != nil {
		if _, ok := envelope["session_id"]; ok {
			envelope["session_id"], _ = json.Marshal(crewChatSessionID(project, call.ConversationID))
			result.Body, _ = json.Marshal(envelope)
		}
	}
	return result
}

func writeCrewChatResult(w http.ResponseWriter, result crewChatResult) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(result.Status)
	_, _ = w.Write(result.Body)
}

// Only the owner's main website chat routes to the Mac. Side chats and every
// unattended/token-based caller keep their existing server execution path.
func (api *StreamingAPI) routeCrewMainChat(w http.ResponseWriter, r *http.Request, profile, key string, call crewChatCall) bool {
	if IsLocalMode() || profile != "work" || !crewChatWebsiteClaims(GetUserFromContext(r.Context())) {
		return false
	}
	project := crewResourceProjectID(key)
	if project == "" {
		return false
	}
	if _, err := api.crewSyncRoots(r.Context(), GetUserFromContext(r.Context()), project); err != nil {
		return false
	}
	b, err := crewroot.ReadSync(project)
	if err != nil || b == nil || b.Side != "server" || b.Editing != "local" {
		return false
	}
	if place, e := crewPublishedAgent(project, crewAgentPublishTab(key)); e != nil {
		externalError(w, 409, "publish_unavailable", e.Error())
		return true
	} else if place != nil {
		if place.Location == "server" {
			return false
		}
		externalError(w, 409, "publishing", "This agent is being published. Finish the publish from your Mac, then reopen it here.")
		return true
	}
	// A Team member's own tab lives on the server, not on the owner's Mac (owner, 5 Nov): the owner opens it here.
	if crewTeamMemberTab(r.Context(), project, key) != "" {
		return false
	}
	// An agent the server answers for from its standby copy (Always on): its chats, teammates' threads on it included,
	// run here until it is handed back to the Mac.
	if crewStandbyServesKey(key) {
		return false
	}
	// Always on: while the Mac is off, the owner's In team agent answers here from its standby copy (or already does).
	// Opening it brings the copy in; sending makes the server its writer. A new chat stays the Mac's to start.
	standby := func(tab string) bool {
		if call.Operation != "send" && call.Operation != "resolve" {
			return false
		}
		started := time.Now()
		if !api.crewStandbyRoute(r.Context(), GetUserIDFromContext(r.Context()), project, tab, "", call.Operation == "send") {
			return false
		}
		if call.Operation == "send" {
			crewAlwaysOnLog(project, tab, "answered_on_server", "owner_chat", started)
			// The chat handler that called this releases the flight once the turn is admitted (else it expires).
			if holder, ok := r.Context().Value(crewStandbyFlightHolderKey{}).(*crewStandbyFlightHolder); ok {
				holder.project, holder.tab = project, tab
			}
		}
		return true
	}
	// A side tab of a Crew that lives on the Mac is the Mac's own tab (PLAT-856-509); the server keeps none.
	if base, tab, ok := strings.Cut(strings.TrimSpace(key), codeChatSideMarker); ok {
		if base != project || !crewMacTabID(tab) {
			externalError(w, 400, "invalid_tab", "This tab is unavailable.")
			return true
		}
		if standby(crewStandbyTabForKey(project, key)) {
			return false
		}
		call.Tab = tab
		if call.Operation == "resolve" {
			call.Name = crewMacTabName(call.Name)
		}
	} else {
		resolved, err := api.agentProfiles.Resolve(profile, 0, GetUserIDFromContext(r.Context()))
		if err != nil {
			return false
		}
		binding, _, err := resolveConversationBindingForUser(r.Context(), GetUserIDFromContext(r.Context()), resolved, key)
		if err != nil || binding.ConversationKey != binding.ProjectMainChatKey {
			return false
		}
		if standby(macMainTab) {
			return false
		}
	}
	if call.Operation == "resolve" && call.Tab == "" && crewMacSocketFor(GetUserIDFromContext(r.Context()), project, b.PairID) == nil {
		entries, e := readCrewAgentPlacements(project)
		if e != nil {
			externalError(w, 409, "publish_unavailable", e.Error())
			return true
		}
		for _, entry := range entries {
			if entry.MacMainConversationID == "" {
				continue
			}
			writeAgentProfileJSON(w, http.StatusOK, map[string]any{"session_id": crewChatSessionID(project, entry.MacMainConversationID), "conversation_id": entry.MacMainConversationID, "conversation_key": key, "tab_owner_id": GetUserIDFromContext(r.Context()), "can_write": true, "is_main_chat": true, "chat_location": "mac", "mac_off": true})
			return true
		}
	}
	if call.Operation == "rotate" {
		externalError(w, 409, "mac_main_chat", "Start a new main chat from your Mac, then reopen this Crew here.")
		return true
	}
	if call.Operation == "send" {
		selectedProject, generation := crewChatSessionParts(r.Header.Get("X-Session-ID"))
		if selectedProject != project || generation == "" {
			externalError(w, 409, "reopen_mac_chat", "Reopen this Crew to continue its current Mac conversation.")
			return true
		}
		call.ConversationID = generation
		call.SubmissionID = r.Header.Get("Idempotency-Key")
	}
	owner := GetUserIDFromContext(r.Context())
	if call.Operation == "send" {
		crewRelayMessageState(owner, project, call.ConversationID, call.SubmissionID, "sent", "")
	}
	result := api.callCrewMac(r, project, call)
	if call.Operation == "send" {
		state, reason := "received", ""
		if result.Status >= 300 {
			state, reason = "failed", crewChatResultError(result)
		}
		crewRelayMessageState(owner, project, call.ConversationID, call.SubmissionID, state, reason)
	}
	if call.Operation == "resolve" && result.Status == 200 {
		var out map[string]any
		if json.Unmarshal(result.Body, &out) == nil {
			out["session_id"] = crewChatSessionID(project, fmt.Sprint(out["conversation_id"]))
			out["conversation_key"] = strings.TrimSpace(key)
			out["tab_owner_id"] = GetUserIDFromContext(r.Context())
			out["tab_owner_name"] = GetUserFromContext(r.Context()).Username
			out["chat_location"] = "mac"
			delete(out, "connection_id")
			result.Body, _ = json.Marshal(out)
		}
	} else if call.Operation == "send" && result.Status < 300 {
		var out map[string]any
		if json.Unmarshal(result.Body, &out) == nil {
			out["session_id"] = crewChatSessionID(project, call.ConversationID)
			result.Body, _ = json.Marshal(out)
		}
	}
	writeCrewChatResult(w, result)
	return true
}

// A virtual session address selects a private pair, not a native session ID.
// The Mac resolves the canonical local conversation for every operation.
func (api *StreamingAPI) crewChatRelayMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsLocalMode() {
			next.ServeHTTP(w, r)
			return
		}
		session := mux.Vars(r)["session_id"]
		if session == "" {
			session = r.Header.Get("X-Session-ID")
		}
		if !strings.HasPrefix(session, pairedMacSessionPrefix) {
			// Every session route of a chat kept on this server; admins' audited reviews keep their own rule.
			if !strings.HasPrefix(r.URL.Path, "/api/admin/") {
				if private := api.crewSessionChatPrivate(r, mux.Vars(r)["session_id"]); private != nil {
					private.refuse(w, GetUserIDFromContext(r.Context()))
					return
				}
			}
			next.ServeHTTP(w, r)
			return
		}
		// A session header also accompanies unrelated app API calls. Only
		// session routes are handled here; profile sends have their own seam.
		if !strings.HasPrefix(r.URL.Path, "/api/sessions/") && !strings.HasPrefix(r.URL.Path, "/api/session/") {
			next.ServeHTTP(w, r)
			return
		}
		project, generation := crewChatSessionParts(session)
		if project == "" || generation == "" {
			externalError(w, 409, "reopen_mac_chat", "Reopen this Crew to continue its current Mac conversation.")
			return
		}
		if private := api.crewMacChatPrivate(r, project, generation); private != nil {
			private.refuse(w, GetUserIDFromContext(r.Context()))
			return
		}
		operation := ""
		suffix := strings.TrimPrefix(r.URL.Path, "/api/sessions/"+session+"/")
		// A Team member watches the owner's In team Mac agent: reads only, and only that conversation (crew_team_mac_watch.go).
		if r.Method == "GET" && (suffix == "events/stream" || suffix == "events" || suffix == "status") {
			if watch, ok := api.crewMacTeamWatchContext(r, project, generation); ok {
				r = watch
			}
		}
		read := r.Method == "GET" && (suffix == "events/stream" || suffix == "events" || suffix == "status" || suffix == "queued-messages")
		if read {
			r = r.WithContext(context.WithValue(r.Context(), crewRelayReadKey{}, true))
		} else if crewTeamMemberOnMac(project, GetUserIDFromContext(r.Context())) {
			externalError(w, 409, "mac_chat_action", "This Crew works on your Mac. Open it in the AgentWorks app on your Mac to send, answer or stop.")
			return
		}
		switch {
		case r.Method == "GET" && suffix == "events/stream":
			api.serveCrewMacEvents(w, r, project, generation)
			return
		case r.Method == "GET" && suffix == "events":
			operation = "events"
		case r.Method == "GET" && suffix == "status":
			operation = "status"
		case r.Method == "GET" && suffix == "queued-messages":
			operation = "queue"
		case r.Method == "POST" && r.URL.Path == "/api/session/cancel-turn":
			operation = "cancel"
		case r.Method == "POST" && r.URL.Path == "/api/session/stop":
			operation = "cancel"
		case r.Method == "POST" && suffix == "coding-agent-question/answer":
			operation = "answer"
		case r.Method == "GET" && suffix == "file":
			operation = "file"
		default:
			externalError(w, 409, "mac_chat_action", "Use your Mac for this action. Messages, replies and Stop are available here.")
			return
		}
		call := crewChatCall{Operation: operation, ConversationID: generation, Query: crewChatEventQuery(r.URL.Query()), Path: r.URL.Query().Get("path")}
		if operation == "answer" {
			call.Answer, _ = io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<10))
			if !json.Valid(call.Answer) {
				externalError(w, 400, "invalid_answer", "Send a valid answer.")
				return
			}
		}
		// With the Mac off, history and the queue come from the server's own copy (the relay cache), never a 503:
		// a failed history read made the page look like a new Crew, and the queue lives on the Mac (PLAT-856-509).
		if operation == "events" || operation == "queue" {
			if b, err := api.crewChatPair(r, project, ""); err == nil && crewMacSocketFor(GetUserIDFromContext(r.Context()), project, b.PairID) == nil {
				writeCrewChatResult(w, api.crewMacOffRead(GetUserIDFromContext(r.Context()), project, generation, operation))
				return
			}
		}
		writeCrewChatResult(w, api.callCrewMac(r, project, call))
	})
}

// crewMacOffRead answers a read while the owner's Mac is off: the queue is empty here (it lives on the Mac), and the
// history is what the relay cache holds, marked so the page says earlier messages are on the Mac.
func (api *StreamingAPI) crewMacOffRead(owner, project, generation, operation string) crewChatResult {
	if operation == "queue" {
		return crewChatResult{Status: 200, Body: json.RawMessage(`{"messages":[],"mac_off":true}`)}
	}
	crewRelayStreams.Lock()
	batches := append([]crewRelayBatch(nil), crewRelayStreams.get(owner+"/"+project+"/"+generation).batches...)
	crewRelayStreams.Unlock()
	events := []json.RawMessage{}
	for _, batch := range batches {
		if batch.Kind != "event" {
			continue
		}
		var envelope struct {
			Events []json.RawMessage `json:"events"`
		}
		if json.Unmarshal(batch.Body, &envelope) == nil {
			events = append(events, envelope.Events...)
		}
	}
	// Always on: an agent with a copy on this server shows that copy's conversation, so a teammate (or the owner on
	// the website) sees the chat while the Mac is off, also after this server restarted and its relay cache is empty.
	// Only for the Crew's owner (a Team member watching reads as the owner, for the one conversation it may watch);
	// a member's read of their own Mac never reaches the owner's copies.
	onMac := true
	if owner != ownerOfCrewProjectID(project) {
		// keep the relay cache only
	} else if served := api.crewStandbyServedEvents(owner, project, generation); len(served) > 0 {
		events, onMac = served, false // the server answers for the agent now: its own chat has the newest turns
	} else if len(events) == 0 {
		if copied := crewStandbyHistoryEvents(project, generation); len(copied) > 0 {
			events, onMac = copied, false
		}
	}
	if len(events) > 200 {
		events = events[len(events)-200:]
	}
	body, _ := json.Marshal(map[string]any{"events": events, "last_processed_index": crewRelayLastSeq(batches), "has_more": false, "mac_off": true, "history_on_mac": onMac})
	log.Printf("[CREW_RELAY] mac off read owner=%s project=%s conversation=%s operation=%s events=%d from_copy=%t", owner, project, generation, operation, len(events), !onMac)
	return crewChatResult{Status: 200, Body: body}
}

func crewChatEventQuery(in url.Values) url.Values {
	out := url.Values{}
	for _, name := range []string{"since", "limit", "offset", "before_sequence", "cursor_only", "durable_chat", "sync_native_transcript", "view", "working_set"} {
		if v := in.Get(name); v != "" {
			out.Set(name, v)
		}
	}
	return out
}

// The browser keeps its SSE renderer. The Mac pushes over its one connection;
// events are numbered so a dropped viewer resumes from its last one (contract §3).
func (api *StreamingAPI) serveCrewMacEvents(w http.ResponseWriter, r *http.Request, project, generation string) {
	owner := GetUserIDFromContext(r.Context())
	b, err := api.crewChatPair(r, project, "")
	if err != nil {
		writeCrewChatResult(w, crewChatResult{Status: 403, Body: json.RawMessage(`{"error":"This Crew is not currently connected to its Mac conversation. Reopen the Crew."}`)})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	since := crewRelaySince(r)
	streamLog := newChatStreamLog(r, project, generation, since)
	defer streamLog.close()
	replay, gap, sub, stop := crewRelayStreams.subscribe(owner, project, generation, since)
	defer stop()
	sock := crewMacSocketFor(owner, project, b.PairID)
	// With the Mac on, it is the source: fetch what the viewer lacks, then the Mac pushes.
	var first *crewChatResult
	if sock != nil && (since == 0 || gap || len(replay) == 0) {
		q := crewChatEventQuery(r.URL.Query())
		q.Set("limit", "200")
		q.Set("since", strconv.Itoa(since))
		result := api.callCrewMac(r, project, crewChatCall{Operation: "events", ConversationID: generation, Query: q})
		if result.Status != 200 {
			writeCrewChatResult(w, result)
			return
		}
		first = &result
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	last := since
	allowed := func() bool {
		if crewMacTeamWatchAllowed(r, project) {
			return true
		}
		streamLog.reason = "access_revoked"
		log.Printf("[CREW_TEAM] project=%s event=watch_closed reason=access_revoked", project)
		return false
	}
	write := func(kind string, id int, body []byte) bool {
		if !allowed() {
			return false
		}
		if id > last {
			last = id
		}
		_, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", last, kind, bytes.TrimSpace(body))
		flusher.Flush()
		if err != nil {
			streamLog.reason = "write_error"
			return false
		}
		streamLog.sent++
		return true
	}
	if first != nil {
		var envelope struct {
			LastProcessedIndex int `json:"last_processed_index"`
		}
		_ = json.Unmarshal(first.Body, &envelope)
		crewRelayStreams.append(owner, project, generation, crewRelayBatch{Kind: "event", Seq: envelope.LastProcessedIndex, Body: first.Body})
		if !write("event", envelope.LastProcessedIndex, first.Body) {
			return
		}
		streamLog.resume(since, last, 1, false, "mac")
	} else if gap || sock == nil && (since == 0 || len(replay) == 0) {
		// The viewer is older than the cache, or the Mac is off: replace, not append.
		// With nothing cached (a server restart) the saved state still greys the Crew.
		events := []json.RawMessage{}
		for _, batch := range replay {
			if batch.Kind == "event" {
				var envelope struct {
					Events []json.RawMessage `json:"events"`
				}
				if json.Unmarshal(batch.Body, &envelope) == nil {
					events = append(events, envelope.Events...)
				}
			}
		}
		if len(events) > 50 {
			events = events[len(events)-50:]
		}
		state := crewMacState{State: "idle"}
		if s := readCrewMacState(project); s != nil {
			state = *s
		}
		lastSeq := since
		if len(replay) > 0 {
			lastSeq = crewRelayLastSeq(replay)
		}
		body, _ := json.Marshal(map[string]any{"conversation": crewChatSessionID(project, generation), "state": state, "last_seq": lastSeq, "events": events})
		if !write("snapshot", lastSeq, body) {
			return
		}
		streamLog.resume(since, lastSeq, len(events), true, map[bool]string{true: "cache", false: "saved_state"}[len(replay) > 0])
	} else {
		for _, batch := range replay {
			if !write(batch.Kind, batch.Seq, batch.Body) {
				return
			}
		}
		streamLog.resume(since, last, len(replay), false, "cache")
	}
	resume := func() {
		if sock := crewMacSocketFor(owner, project, b.PairID); sock != nil {
			_ = sock.send(crewRelayMessage{Type: "resume", Project: project, Conversation: generation, Since: last})
		}
	}
	resume()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	refresh := time.NewTicker(3 * time.Minute)
	defer refresh.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case batch := <-sub:
			if batch.Kind == "event" && batch.Seq <= last {
				continue
			}
			if !write(batch.Kind, batch.Seq, batch.Body) {
				return
			}
		case <-heartbeat.C:
			if !allowed() {
				return
			}
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				streamLog.keepaliveMiss()
				return
			}
			flusher.Flush()
		case <-refresh.C:
			if !allowed() {
				return
			}
			resume()
		}
	}
}

// The route includes the canonical conversation generation. A Mac New chat
// must never reuse another transcript's browser cache or durable event cursor.
func crewChatSessionID(project, conversation string) string {
	return pairedMacSessionPrefix + base64.RawURLEncoding.EncodeToString([]byte(project)) + "." + conversation
}
func crewChatSessionParts(session string) (string, string) {
	if !strings.HasPrefix(session, pairedMacSessionPrefix) {
		return "", ""
	}
	project, conversation, ok := strings.Cut(strings.TrimPrefix(session, pairedMacSessionPrefix), ".")
	if !ok {
		return "", ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(project)
	if err != nil {
		return "", ""
	}
	return string(decoded), conversation
}

func crewChatResultError(result crewChatResult) string {
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(result.Body, &body) == nil && body.Error != "" {
		return body.Error
	}
	return http.StatusText(result.Status)
}

const crewMacOffMessage = "Your Mac is off. Your message will be answered when it's back on."

// callCrewMacFor sends one call to the Mac of a Crew's actual owner, for the owner's own callers that are not a
// website sign-in (their AI apps, a Slack direct message, their online schedules). The caller has already been
// checked to be that owner; the call goes only to the owner's own Mac connection.
func callCrewMacFor(ctx context.Context, owner, project string, call crewChatCall, timeout time.Duration) crewChatResult {
	failure := func(code int, message string) crewChatResult {
		data, _ := json.Marshal(map[string]any{"error": message})
		return crewChatResult{Status: code, Body: data}
	}
	b, err := crewroot.ReadSync(project)
	if err != nil || b == nil || b.Side != "server" || b.Editing != "local" || b.PendingAction != "" {
		return failure(409, "This Crew is not on its Mac right now.")
	}
	sock := crewMacSocketFor(owner, project, b.PairID)
	if sock == nil {
		return failure(503, crewMacOffMessage)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, _, err := sock.command(ctx, project, call)
	if err != nil {
		return failure(504, "Your Mac did not answer in time. Ask again to see whether your message arrived.")
	}
	log.Printf("[CREW_CHAT_RELAY] completed owner=%s project=%s operation=%s action=%s caller=%q status=%d", owner, project, call.Operation, call.Action, call.Caller, result.Status)
	return result
}

// routeCrewMacTabs lists a Mac Crew's own side tabs for its owner on the website (PLAT-856-509): the Mac's tab keys with
// the project part swapped to this server's copy. With the Mac off only published agents and In team agents answering
// from their standby copy are here; Main still shows.
func (api *StreamingAPI) routeCrewMacTabs(w http.ResponseWriter, r *http.Request, project string) bool {
	if IsLocalMode() || !crewChatWebsiteClaims(GetUserFromContext(r.Context())) {
		return false
	}
	b, err := api.crewChatPair(r, project, "")
	if err != nil || b.Mode != "relay" {
		return false
	}
	owner := GetUserIDFromContext(r.Context())
	tabs := []AgentProfileSideChat{}
	placements, err := readCrewAgentPlacements(project)
	if err != nil {
		externalError(w, 409, "publish_unavailable", err.Error())
		return true
	}
	if crewMacSocketFor(owner, project, b.PairID) != nil {
		result := api.callCrewMac(r, project, crewChatCall{Operation: "tabs"})
		var rows []AgentProfileSideChat
		if result.Status != http.StatusOK || json.Unmarshal(result.Body, &rows) != nil {
			writeCrewChatResult(w, result)
			return true
		}
		for _, row := range rows {
			base, id, ok := strings.Cut(row.ConversationKey, codeChatSideMarker)
			if !ok || base != b.PeerProjectID || !crewMacTabID(id) || row.ConversationID == "" {
				continue
			}
			if _, published := placements[id]; published || crewStandbyServerWriter(project, id) {
				continue
			}
			tabs = append(tabs, AgentProfileSideChat{TabOwnerID: owner, TabOwnerName: GetUserFromContext(r.Context()).Username, CanWrite: true,
				ConversationKey: project + codeChatSideMarker + id, ConversationID: row.ConversationID, SessionID: crewChatSessionID(project, row.ConversationID),
				ChatLocation: "mac", Name: crewMacTabName(row.Name), Position: row.Position, Pinned: row.Pinned})
		}
	}
	// Always on: an In team agent the server answers for (its Mac off, or not handed back yet) is opened here, from its
	// standby copy.
	serving := map[string]bool{}
	if rows, e := readCrewStandby(project); e == nil {
		for tab := range rows {
			if tab == macMainTab || placements[tab].Tab != "" {
				continue
			}
			if api.crewStandbyRoute(r.Context(), owner, project, tab, "", false) {
				serving[tab] = true
			}
		}
	}
	if len(placements) > 0 || len(serving) > 0 {
		doc, e := defaultProductConversationRegistryStore().loadDocument(r.Context(), productConversationRegistryPath(owner))
		if e != nil {
			externalError(w, 409, "publish_unavailable", e.Error())
			return true
		}
		for tab := range serving {
			placements[tab] = crewAgentPlacement{Tab: tab, Location: "server"}
		}
		for tab, place := range placements {
			if place.Location != "server" || tab == macMainTab {
				continue
			}
			key := productConversationRegistryEntryKey(crewProfileID, crewAgentPublishKey(project, tab))
			record, ok := doc.Entries[key]
			if !ok {
				continue
			}
			identity := doc.Agents[key]
			tabs = append(tabs, AgentProfileSideChat{ConversationKey: record.ConversationKey, ConversationID: record.ConversationID, SessionID: record.SessionID, ChatLocation: "server", Name: identity.Name, Purpose: identity.Purpose, Position: identity.Position, Pinned: !identity.Unpinned, TabOwnerID: owner, CanWrite: true})
		}
	}
	// The Team's members' own agents (owner, 5 Nov: "I need to see the other user's agent"): watch-only for the owner.
	tabs = append(tabs, crewTeamMemberTabs(r.Context(), project, owner)...)
	writeAgentProfileJSON(w, http.StatusOK, tabs)
	return true
}

// crewTeamMemberTabs are the joined members' own tabs in this Crew's online copy, for someone else in the Crew to
// watch: each member's Main (their default chat) and side tabs, never one they set to Only me.
func crewTeamMemberTabs(ctx context.Context, project, viewer string) []AgentProfileSideChat {
	t, err := readCrewTeam(project)
	if err != nil || !t.On {
		return nil
	}
	store := defaultProductConversationRegistryStore()
	out := []AgentProfileSideChat{}
	for _, p := range t.People {
		if p.Invited || p.UserID == viewer || p.UserID == t.OwnerID || !crewTeamAccountActive(p.UserID) {
			continue
		}
		if _, onMac := t.memberMac(p.UserID); onMac {
			continue // their agents here work on their Mac (crewTeamMacMemberTabs below)
		}
		records, err := store.projectChats(ctx, p.UserID, crewProfileID, project)
		if err != nil {
			continue
		}
		identities, _ := store.agentIdentities(ctx, p.UserID, crewProfileID, project)
		for _, record := range records {
			identity := identities[record.ConversationKey]
			if !t.agentInTeam(p.UserID, record.ConversationKey) || hiddenFromTabStrip(record.ConversationKey) || record.SessionID == "" {
				continue
			}
			main := record.IsMainChat || record.ConversationKey == crewOwnerDefaultChatKey(project, p.UserID)
			if !main && !isSideChatConversationKey(record.ConversationKey) {
				continue
			}
			name := firstNonEmptyTrimmed(identity.Name, record.Title, "Agent")
			if main {
				name = "Main"
			}
			out = append(out, AgentProfileSideChat{ConversationKey: record.ConversationKey, ConversationID: record.ConversationID, SessionID: record.SessionID, Name: name,
				Purpose: identity.Purpose, Pinned: true, TabOwnerID: p.UserID, TabOwnerName: crewOwnerDisplayName(p.UserID), ChatLocation: "server"})
		}
	}
	return append(out, crewTeamMacMemberTabs(ctx, project, viewer)...)
}

// crewTeamMemberTab is the joined member whose own tab this key is, or "". A tab its member set to Only me is
// nobody else's to open or message, so it is "" too.
func crewTeamMemberTab(ctx context.Context, project, key string) string {
	t, err := readCrewTeam(project)
	if err != nil || !t.On {
		return ""
	}
	store := defaultProductConversationRegistryStore()
	for _, p := range t.People {
		if p.Invited || p.UserID == t.OwnerID {
			continue
		}
		records, err := store.projectChats(ctx, p.UserID, crewProfileID, project)
		if err != nil {
			continue
		}
		for _, record := range records {
			if record.ConversationKey == strings.TrimSpace(key) {
				if !t.agentInTeam(p.UserID, record.ConversationKey) {
					return ""
				}
				return p.UserID
			}
		}
	}
	return ""
}

// crewMacTwinForOwner: the owner's website copy of a Crew that lives on the Mac, whose tabs are the Mac's.
func (api *StreamingAPI) crewMacTwinForOwner(r *http.Request, project string) bool {
	if IsLocalMode() || !crewChatWebsiteClaims(GetUserFromContext(r.Context())) {
		return false
	}
	b, err := api.crewChatPair(r, project, "")
	return err == nil && b.Mode == "relay"
}
