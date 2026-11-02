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
	"strconv"
	"strings"
	"time"

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
	if !crewChatWebsiteClaims(c) {
		return nil, fmt.Errorf("Open this Crew using its owner's website sign-in.")
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
		return failure(503, "Your Mac is offline. Open AgentWorks on it, or use Project sync on your Mac to switch editing to the server.")
	}
	timeout := 55 * time.Second
	if call.Operation == "handoff" {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	result, dispatched, err := sock.command(ctx, project, call)
	if err != nil {
		if call.Operation == "handoff" {
			if !dispatched {
				return failure(409, "Your Mac has not started the transfer yet. Try again shortly.")
			}
			return failure(409, "The session transfer was interrupted. Reopen the Crew to check its progress.")
		}
		return failure(504, "Your Mac did not respond. Reopen the chat to check whether your message arrived before sending it again.")
	}
	if call.Operation != "handoff" {
		if _, err := api.crewChatPair(r, project, b.PairID); err != nil {
			return failure(403, err.Error())
		}
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
	resolved, err := api.agentProfiles.Resolve(profile, 0, GetUserIDFromContext(r.Context()))
	if err != nil {
		return false
	}
	binding, _, err := resolveConversationBindingForUser(r.Context(), GetUserIDFromContext(r.Context()), resolved, key)
	if err != nil || binding.ConversationKey != binding.ProjectMainChatKey {
		return false
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
			out["conversation_key"] = project
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
		operation := ""
		suffix := strings.TrimPrefix(r.URL.Path, "/api/sessions/"+session+"/")
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
		default:
			externalError(w, 409, "mac_chat_action", "Use your Mac for this action. Messages, replies and Stop are available here.")
			return
		}
		call := crewChatCall{Operation: operation, ConversationID: generation, Query: crewChatEventQuery(r.URL.Query())}
		if operation == "answer" {
			call.Answer, _ = io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<10))
			if !json.Valid(call.Answer) {
				externalError(w, 400, "invalid_answer", "Send a valid answer.")
				return
			}
		}
		writeCrewChatResult(w, api.callCrewMac(r, project, call))
	})
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
	write := func(kind string, id int, body []byte) bool {
		if id > last {
			last = id
		}
		_, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", last, kind, bytes.TrimSpace(body))
		flusher.Flush()
		return err == nil
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
		log.Printf("[CREW_RELAY] resume project=%s since=%d source=mac", project, since)
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
		state := "idle"
		if s := readCrewMacState(project); s != nil {
			state = s.State
		}
		lastSeq := since
		if len(replay) > 0 {
			lastSeq = crewRelayLastSeq(replay)
		}
		body, _ := json.Marshal(map[string]any{"conversation": crewChatSessionID(project, generation), "state": state, "last_seq": lastSeq, "events": events})
		if !write("snapshot", lastSeq, body) {
			return
		}
		log.Printf("[CREW_RELAY] snapshot project=%s since=%d", project, since)
	} else {
		for _, batch := range replay {
			if !write(batch.Kind, batch.Seq, batch.Body) {
				return
			}
		}
		log.Printf("[CREW_RELAY] cache hit project=%s since=%d batches=%d", project, since, len(replay))
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
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-refresh.C:
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
