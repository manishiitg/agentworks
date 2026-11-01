package server

// A paired Crew's owner can use its Mac conversation from the website. The
// server holds only in-flight requests; history and execution stay on the Mac.
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
	"sync"
	"time"

	"github.com/google/uuid"
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
type crewChatPending struct {
	call       crewChatCall
	ctx        context.Context
	result     chan crewChatResult
	dispatched chan struct{}
}
type crewChatMailbox struct {
	lastPoll time.Time
	queue    chan *crewChatPending
	pending  map[string]*crewChatPending
}

var crewChatRelays = struct {
	sync.Mutex
	boxes map[string]*crewChatMailbox
}{boxes: make(map[string]*crewChatMailbox)}

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

func crewChatMailboxKey(owner, project, pair string) string {
	return owner + "/" + project + "/" + pair
}
func crewChatBox(key string) *crewChatMailbox { // caller holds crewChatRelays
	b := crewChatRelays.boxes[key]
	if b == nil {
		b = &crewChatMailbox{queue: make(chan *crewChatPending, 32), pending: make(map[string]*crewChatPending)}
		crewChatRelays.boxes[key] = b
	}
	return b
}

// Both poll and response recheck the saved login against the actual Crew owner.
// Knowing a pair ID never admits another person's otherwise-valid sign-in.
func (api *StreamingAPI) handleCrewChatPoll(w http.ResponseWriter, r *http.Request) {
	project, pair := mux.Vars(r)["project"], r.URL.Query().Get("pair")
	b, err := api.crewChatPairState(r, project, pair, r.Method == http.MethodPost)
	if err != nil || pair == "" {
		log.Printf("[CREW_CHAT_RELAY] poll refused owner=%s project=%s", GetUserIDFromContext(r.Context()), project)
		externalError(w, 403, "mac_chat_forbidden", "This sign-in cannot connect that Crew's Mac conversation.")
		return
	}
	key := crewChatMailboxKey(GetUserIDFromContext(r.Context()), project, b.PairID)
	crewChatRelays.Lock()
	box := crewChatBox(key)
	box.lastPoll = time.Now()
	crewChatRelays.Unlock()
	if r.Method == http.MethodPost {
		var result crewChatResult
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, crewChatMaxReply+4096)).Decode(&result) != nil || result.Status < 200 || result.Status > 599 {
			externalError(w, 400, "invalid_reply", "Invalid Mac reply.")
			return
		}
		crewChatRelays.Lock()
		pending := box.pending[result.ID]
		crewChatRelays.Unlock()
		if b.Editing != "local" && (pending == nil || pending.call.Operation != "handoff") {
			externalError(w, 403, "mac_chat_forbidden", "This reply is not for the active session transfer.")
			return
		}
		if pending != nil {
			select {
			case pending.result <- result:
			default:
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	timer := time.NewTimer(25 * time.Second)
	defer timer.Stop()
	for {
		select {
		case pending := <-box.queue:
			if pending.ctx.Err() != nil {
				continue
			}
			if _, err := api.crewChatPair(r, project, pair); err != nil {
				externalError(w, 403, "mac_chat_forbidden", err.Error())
				return
			}
			if pending.dispatched != nil {
				close(pending.dispatched)
			}
			externalJSON(w, pending.call)
			return
		case <-timer.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			return
		}
	}
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
	timeout := 55 * time.Second
	if call.Operation == "handoff" {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	call.ID = uuid.NewString()
	pending := &crewChatPending{call: call, ctx: ctx, result: make(chan crewChatResult, 1), dispatched: make(chan struct{})}
	key := crewChatMailboxKey(GetUserIDFromContext(r.Context()), project, b.PairID)
	crewChatRelays.Lock()
	box := crewChatBox(key)
	if time.Since(box.lastPoll) > 40*time.Second {
		crewChatRelays.Unlock()
		return failure(503, "Your Mac is offline. Open AgentWorks on it, or use Project sync on your Mac to switch editing to the server.")
	}
	box.pending[call.ID] = pending
	crewChatRelays.Unlock()
	defer func() { crewChatRelays.Lock(); delete(box.pending, call.ID); crewChatRelays.Unlock() }()
	select {
	case box.queue <- pending:
	case <-ctx.Done():
		if call.Operation == "handoff" {
			return failure(409, "The session transfer was interrupted. Reopen the Crew to check its progress.")
		}
		return failure(504, "Your Mac did not respond. Reopen the chat to check whether your message arrived before sending it again.")
	}
	if call.Operation == "handoff" {
		// Only an undispatched request is safe to replace with the last mirror.
		// Once the Mac starts flushing, wait for its result instead of activating
		// a stale mirror while the real transfer is still running.
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-pending.dispatched:
		case <-timer.C:
			cancel()
			return failure(409, "Your Mac has not started the transfer yet. Try again shortly.")
		case <-ctx.Done():
			return failure(409, "The session transfer was interrupted. Reopen the Crew to check its progress.")
		}
	}
	select {
	case result := <-pending.result:
		if call.Operation != "handoff" {
			if _, err := api.crewChatPair(r, project, b.PairID); err != nil {
				return failure(403, err.Error())
			}
		}
		if call.Operation == "send" || call.Operation == "resolve" {
			log.Printf("[CREW_CHAT_RELAY] completed owner=%s project=%s operation=%s request=%s status=%d", GetUserIDFromContext(r.Context()), project, call.Operation, call.ID, result.Status)
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
	case <-ctx.Done():
		if call.Operation == "handoff" {
			return failure(409, "The session transfer was interrupted. Reopen the Crew to check its progress.")
		}
		return failure(504, "Your Mac did not respond. Reopen the chat to check whether your message arrived before sending it again.")
	}
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
	result := api.callCrewMac(r, project, call)
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

// The browser keeps its existing SSE renderer. Only the Mac transport polls;
// no new frontend queue, transcript store or streaming protocol is introduced.
func (api *StreamingAPI) serveCrewMacEvents(w http.ResponseWriter, r *http.Request, project, generation string) {
	q := crewChatEventQuery(r.URL.Query())
	q.Set("limit", "200")
	result := api.callCrewMac(r, project, crewChatCall{Operation: "events", ConversationID: generation, Query: q})
	if result.Status != 200 {
		writeCrewChatResult(w, result)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	for {
		var envelope struct {
			LastProcessedIndex int `json:"last_processed_index"`
		}
		if json.Unmarshal(result.Body, &envelope) != nil {
			return
		}
		_, err := fmt.Fprintf(w, "id: %d\nevent: event\ndata: %s\n\n", envelope.LastProcessedIndex, bytes.TrimSpace(result.Body))
		if err != nil {
			return
		}
		flusher.Flush()
		q.Set("since", strconv.Itoa(envelope.LastProcessedIndex))
		select {
		case <-r.Context().Done():
			return
		case <-time.After(time.Second):
		}
		result = api.callCrewMac(r, project, crewChatCall{Operation: "events", ConversationID: generation, Query: q})
		if result.Status != 200 {
			return
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
