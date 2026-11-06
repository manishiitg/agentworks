package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// Chat history (crew_team_chat_private.go): a teammate reads an agent's finished turns and nothing of a running one.
// One rule covers a chat kept on this server and a Mac agent's chat alike: the newest row that ends a turn is the
// limit, and no read goes past it. A turn that never ended stays hidden until the next one ends. Only named fields of a
// reply pass, so what describes a running turn (its query, runtime state, queue) never reaches the teammate.

// crewChatTurnEnds are the rows that end a main-agent turn; reply_file_changes follows the last of them.
var crewChatTurnEnds = map[string]bool{"unified_completion": true, "agent_end": true, "conversation_end": true, "conversation_error": true, "agent_error": true, "context_cancelled": true, "reply_file_changes": true}

const (
	crewChatHistoryWalkPages = 20 // how far back a very long running turn is walked, in pages of 300 rows
	crewChatHistoryTick      = 3 * time.Second
	crewChatHistoryRefresh   = 30 * time.Second
)

// crewChatRead reads one page of a chat's events, as GET /events does.
type crewChatRead func(url.Values) crewChatResult

type crewChatRow struct {
	Type          string `json:"type"`
	Sequence      int    `json:"sequence"`
	ExecutionKind string `json:"execution_kind"`
}

func (row crewChatRow) endsTurn() bool {
	kind := strings.ToLower(strings.TrimSpace(row.ExecutionKind))
	return row.Sequence > 0 && crewChatTurnEnds[row.Type] && (kind == "" || kind == "main" || kind == "main_agent" || kind == "chat")
}

type crewChatPage struct {
	Events             []json.RawMessage `json:"events"`
	HasMore            bool              `json:"has_more"`
	LastProcessedIndex int               `json:"last_processed_index"`
}

func (p crewChatPage) rows() []crewChatRow {
	rows := make([]crewChatRow, len(p.Events))
	for i, raw := range p.Events {
		_ = json.Unmarshal(raw, &rows[i])
	}
	return rows
}

func crewChatUnreadable() crewChatResult {
	return crewChatResult{Status: http.StatusBadGateway, Body: json.RawMessage(`{"error":"This chat could not be read. Try again."}`)}
}

// crewChatLastTurnEnd is the sequence of the newest row that ends a turn (0: no turn has ended), walking back from the
// newest page, and that newest page.
func crewChatLastTurnEnd(read crewChatRead, workspace string) (int, crewChatResult) {
	q := url.Values{"durable_chat": {"1"}, "limit": {"300"}}
	if workspace != "" {
		q.Set("workspace_path", workspace)
	}
	var newest crewChatResult
	for page := 0; page < crewChatHistoryWalkPages; page++ {
		result := read(q)
		if page == 0 {
			newest = result
		}
		var body crewChatPage
		if result.Status != http.StatusOK {
			return 0, result
		}
		if json.Unmarshal(result.Body, &body) != nil {
			return 0, crewChatUnreadable()
		}
		rows := body.rows()
		for i := len(rows) - 1; i >= 0; i-- {
			if rows[i].endsTurn() {
				return rows[i].Sequence, newest
			}
		}
		if !body.HasMore || len(rows) == 0 || rows[0].Sequence <= 0 {
			break
		}
		q.Set("before_sequence", strconv.Itoa(rows[0].Sequence))
	}
	return 0, newest
}

// crewChatHistoryRead answers GET /events for Chat history: the page the caller asked for, never past the last turn
// end. A tail or older page is read up to it, a forward page is cut at it, and the cursor stops there so the rows of the
// running turn arrive whole once it ends. The compact view would show the running turn as the latest, so it is off here.
func crewChatHistoryRead(read crewChatRead, asked url.Values) crewChatResult {
	end, newest := crewChatLastTurnEnd(read, asked.Get("workspace_path"))
	if newest.Status != http.StatusOK {
		return newest
	}
	since := -1
	if value := asked.Get("since"); value != "" {
		since, _ = strconv.Atoi(value)
	}
	if asked.Get("cursor_only") == "1" {
		return crewChatFinishedPage(newest, end, since, true)
	}
	if end == 0 {
		return crewChatFinishedPage(newest, 0, since, false)
	}
	q := url.Values{"durable_chat": {"1"}}
	for _, name := range []string{"limit", "workspace_path", "sync_native_transcript"} {
		if value := asked.Get(name); value != "" {
			q.Set(name, value)
		}
	}
	if since >= 0 {
		q.Set("since", strconv.Itoa(since))
	} else {
		if q.Get("limit") == "" || asked.Get("view") != "" {
			q.Set("limit", "300")
		}
		before, _ := strconv.Atoi(asked.Get("before_sequence"))
		if before <= 0 || before > end+1 {
			before = end + 1
		}
		q.Set("before_sequence", strconv.Itoa(before))
	}
	result := read(q)
	if result.Status != http.StatusOK {
		return result
	}
	return crewChatFinishedPage(result, end, since, false)
}

// crewChatFinishedPage keeps a page's rows through the turn end, by position, so a row with no sequence after it (a
// live update in the Mac-off cache) cannot pass, and rebuilds the reply from named fields only.
func crewChatFinishedPage(result crewChatResult, end, since int, cursorOnly bool) crewChatResult {
	var page crewChatPage
	var fields map[string]json.RawMessage
	if json.Unmarshal(result.Body, &page) != nil || json.Unmarshal(result.Body, &fields) != nil {
		return crewChatUnreadable()
	}
	keep, oldest, latest := 0, 0, 0
	for i, row := range page.rows() {
		if row.Sequence > end {
			break
		}
		if row.Sequence > 0 {
			keep, latest = i+1, row.Sequence
			if oldest == 0 {
				oldest = row.Sequence
			}
		}
	}
	cursor := min(page.LastProcessedIndex, end)
	if cursorOnly {
		keep, oldest, latest, cursor = 0, 0, 0, end
	}
	out := map[string]any{"events": append([]json.RawMessage{}, page.Events[:keep]...), "last_processed_index": max(cursor, since),
		"has_more": page.HasMore && end > 0 && (since < 0 || latest < end), "oldest_sequence": oldest, "latest_sequence": latest}
	for _, name := range []string{"session_id", "session_status", "display_status", "mac_off", "history_on_mac"} {
		if value, ok := fields[name]; ok {
			out[name] = value
		}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return crewChatUnreadable()
	}
	return crewChatResult{Status: http.StatusOK, Body: body}
}

// crewChatStatusOnly answers GET /status for Chat history: whether the agent is working, not what it is working on.
func crewChatStatusOnly(result crewChatResult) crewChatResult {
	if result.Status != http.StatusOK {
		return result
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(result.Body, &fields) != nil {
		return crewChatUnreadable()
	}
	out := map[string]json.RawMessage{}
	for _, name := range []string{"session_id", "status", "display_status"} {
		if value, ok := fields[name]; ok {
			out[name] = value
		}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return crewChatUnreadable()
	}
	return crewChatResult{Status: http.StatusOK, Body: body}
}

// serveCrewChatHistoryStream is the events stream of Chat history: status frames, and finished turns as ordinary event
// frames when a turn ends. No live row is ever subscribed to. still is asked before every write: a change of the
// person's choice ends the stream, and the browser's reconnect gets what the new choice allows.
func serveCrewChatHistoryStream(w http.ResponseWriter, r *http.Request, project, conversation string, read crewChatRead, running, still func() bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	cursor := crewRelaySince(r)
	if r.Header.Get("Last-Event-ID") == "" && r.URL.Query().Get("since") == "" {
		var page crewChatPage
		if result := crewChatHistoryRead(read, url.Values{"cursor_only": {"1"}}); result.Status == http.StatusOK && json.Unmarshal(result.Body, &page) == nil {
			cursor = page.LastProcessedIndex
		}
	}
	streamLog := newChatStreamLog(r, project, conversation, cursor)
	defer streamLog.close()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()
	refresh := func() bool {
		for {
			result := crewChatHistoryRead(read, url.Values{"since": {strconv.Itoa(cursor)}})
			var page crewChatPage
			if result.Status != http.StatusOK || json.Unmarshal(result.Body, &page) != nil || len(page.Events) == 0 {
				return true // nothing new, or a read that failed: the next tick tries again
			}
			cursor = max(cursor, page.LastProcessedIndex)
			if writeSSEEvent(w, "event", cursor, json.RawMessage(result.Body)) != nil {
				streamLog.reason = "write_error"
				return false
			}
			flusher.Flush()
			streamLog.sent++
			if !page.HasMore {
				return true
			}
		}
	}
	ticker := time.NewTicker(crewChatHistoryTick)
	defer ticker.Stop()
	working, due, refreshed := running(), 1, time.Now()
	for {
		if !still() {
			streamLog.reason = "chat_share_changed"
			return
		}
		if due > 0 || time.Since(refreshed) >= crewChatHistoryRefresh {
			if !refresh() {
				return
			}
			due, refreshed = max(due-1, 0), time.Now()
		}
		status := sseStatusMessage{SessionStatus: "completed", DisplayStatus: sessionExecutionDisplayIdle}
		if working {
			status = sseStatusMessage{SessionStatus: "running", DisplayStatus: sessionExecutionDisplayBusy}
		}
		if err := writeSSEEvent(w, "status", -1, status); err != nil {
			streamLog.reason = "write_error"
			return
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
		now := running()
		if working && !now {
			due = 2 // the turn ended; its last rows may land just after
		}
		working = now
	}
}

// serveCrewServerChatLimited answers a session route of a chat kept on this server for a teammate its person limits.
// Only what Chat history allows is named; every other route, a terminal or a queue, is a live view and is refused.
func (api *StreamingAPI) serveCrewServerChatLimited(w http.ResponseWriter, r *http.Request, next http.Handler, limit *crewChatRefusal) {
	session := mux.Vars(r)["session_id"]
	suffix := strings.TrimPrefix(r.URL.Path, "/api/sessions/"+session+"/")
	viewer := GetUserIDFromContext(r.Context())
	call := func(handler http.HandlerFunc, q url.Values) crewChatResult {
		in := r.Clone(r.Context())
		in.URL.RawQuery = q.Encode()
		recorder := httptest.NewRecorder()
		handler(recorder, in)
		return crewChatResult{Status: recorder.Code, Body: recorder.Body.Bytes()}
	}
	read := func(q url.Values) crewChatResult { return call(api.handleGetSessionEvents, q) }
	switch {
	case limit.share == crewChatShareAgent || r.Method != http.MethodGet:
		limit.refuse(w, viewer)
	case suffix == "events":
		writeCrewChatResult(w, crewChatHistoryRead(read, r.URL.Query()))
	case suffix == "events/stream":
		serveCrewChatHistoryStream(w, r, "-", session, read, func() bool { return api.sessionTurnRunning(session) }, func() bool {
			now := api.crewSessionChatLimit(r, session)
			return now != nil && now.share == crewChatShareHistory
		})
	case suffix == "status":
		writeCrewChatResult(w, crewChatStatusOnly(call(api.handleGetSessionStatus, url.Values{})))
	case strings.HasPrefix(suffix, "chat-artifacts/"):
		next.ServeHTTP(w, r) // the full text behind a row this read already returned
	default:
		limit.refuse(w, viewer)
	}
}

// serveCrewMacChatHistory is the same for a Mac agent's chat, which is read from the Mac, or from the relay's copy while
// the Mac is off. The middleware has narrowed it to events, events/stream and status. r may read as the agent's person
// (crewMacTeamWatchContext); asked is the teammate's own request, for asking again what they may see.
func (api *StreamingAPI) serveCrewMacChatHistory(w http.ResponseWriter, r, asked *http.Request, project, conversation, suffix string) {
	reader := GetUserIDFromContext(r.Context())
	read := func(q url.Values) crewChatResult {
		if b, err := api.crewChatPair(r, project, ""); err == nil && crewMacSocketFor(reader, project, b.PairID) == nil {
			return api.crewMacOffRead(reader, project, conversation, "events")
		}
		return api.callCrewMac(r, project, crewChatCall{Operation: "events", ConversationID: conversation, Query: crewChatEventQuery(q)})
	}
	switch suffix {
	case "events/stream":
		if _, err := api.crewChatPair(r, project, ""); err != nil {
			writeCrewChatResult(w, crewChatResult{Status: http.StatusForbidden, Body: json.RawMessage(`{"error":"This Crew is not currently connected to its Mac conversation. Reopen the Crew."}`)})
			return
		}
		serveCrewChatHistoryStream(w, r, project, conversation, read, func() bool { return crewMacConversationRunning(reader, project, conversation) }, func() bool {
			now := api.crewMacChatLimit(asked, project, conversation)
			return now != nil && now.share == crewChatShareHistory && crewMacTeamWatchAllowed(r, project)
		})
	case "status":
		writeCrewChatResult(w, crewChatStatusOnly(api.callCrewMac(r, project, crewChatCall{Operation: "status", ConversationID: conversation})))
	default:
		writeCrewChatResult(w, crewChatHistoryRead(read, r.URL.Query()))
	}
}
