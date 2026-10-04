package events

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// Compact chat view (PLAT-466).
//
// The compact view is what an interactive chat restore asks for: the latest
// turn arrives complete (a running turn too), every older turn arrives as its
// messages only. Nothing is deleted; the dropped rows stay in the journal and
// are simply not returned. The default ReadPage path is untouched.
//
// Rules, all defined over the journal's sequence order:
//   - A turn starts at a user_message row. A user_message whose immediately
//     preceding row is a tool call is a mid-turn steering message and does not
//     start a new turn when choosing the latest turn.
//   - The first page holds the whole latest turn (every row, tool rows
//     included, capped at CompactLatestTurnCap rows from the tail) followed by
//     older rows of the compact kept set until CompactMessageWindow messages
//     are collected. The cut is then moved back to the start of that turn, so
//     a page never starts between a user message and its reply.
//   - A before_sequence page applies the same cut over the compact view only.
//   - has_older is true only when a kept row exists below the page's first
//     row, so "load earlier" never offers an empty page.
const (
	CompactMessageWindow = 40
	CompactLatestTurnCap = 1000
)

// compactKeptTypes are the rows an older turn keeps: its user and assistant
// messages (a transcript chunk, or the final-answer events the transcript
// renders as the reply), failures, and every question, approval and feedback
// row with its resolution so an unanswered one stays answerable. Tool calls,
// results, status, live-input receipts and background-agent chatter are not
// returned for older turns.
var compactKeptTypes = []string{
	"user_message", "streaming_chunk",
	"unified_completion", "agent_end", "conversation_end", "background_agent_completed",
	"agent_error", "conversation_error",
	"coding_agent_question", "plan_approval", "request_human_feedback",
	"blocking_human_feedback", "human_feedback_resolved",
}

func isCompactMessageType(eventType string) bool {
	switch eventType {
	case "user_message", "streaming_chunk", "unified_completion", "agent_end", "conversation_end":
		return true
	}
	return false
}

// CompactPageOptions selects a compact page. Messages is the message window
// (CompactMessageWindow when zero); BeforeSequence > 0 reads an older page.
type CompactPageOptions struct {
	BeforeSequence int64
	Messages       int
	LatestTurnCap  int
}

// DurableEventJournalCompactReader is implemented by journals that can serve
// the compact view.
type DurableEventJournalCompactReader interface {
	ReadCompactPage(sessionID string, opts CompactPageOptions) (DurableEventPage, error)
}

func compactTypePlaceholders() string {
	return strings.TrimSuffix(strings.Repeat("?,", len(compactKeptTypes)), ",")
}

func compactTypeArgs(prefix ...interface{}) []interface{} {
	args := append([]interface{}{}, prefix...)
	for _, kind := range compactKeptTypes {
		args = append(args, kind)
	}
	return args
}

type compactRow struct {
	sequence int64
	kind     string
}

// compactLatestTurnStart returns the first sequence of the latest turn.
func (j *SQLiteEventJournal) compactLatestTurnStart(sessionID string, cap int) (int64, error) {
	rows, err := j.db.Query(`SELECT sequence, COALESCE(json_extract(payload, '$.type'), '') FROM structured_chat_events WHERE session_id = ? ORDER BY sequence DESC LIMIT ?`, sessionID, cap)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var tail []compactRow
	for rows.Next() {
		var row compactRow
		if err := rows.Scan(&row.sequence, &row.kind); err != nil {
			return 0, err
		}
		tail = append(tail, row)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(tail) == 0 {
		return 0, nil
	}
	for index, row := range tail {
		if row.kind != "user_message" {
			continue
		}
		if index+1 < len(tail) && strings.HasPrefix(tail[index+1].kind, "tool_call_") {
			continue // steering message inside a running turn
		}
		return row.sequence, nil
	}
	return tail[len(tail)-1].sequence, nil
}

func decodeCompactRows(rows *sql.Rows) ([]Event, error) {
	var out []Event
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var event Event
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

// compactOlder reads kept rows below `before`, newest first, until `messages`
// messages are collected and the oldest collected row is a user_message (or
// the history ends). It returns the rows oldest first.
func (j *SQLiteEventJournal) compactOlder(sessionID string, before int64, messages int) ([]Event, error) {
	rows, err := j.db.Query(`SELECT payload FROM structured_chat_events WHERE session_id = ? AND sequence < ? AND json_extract(payload, '$.type') IN (`+compactTypePlaceholders()+`) ORDER BY sequence DESC`, compactTypeArgs(sessionID, before)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var collected []Event
	count := 0
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var event Event
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, err
		}
		if event.Type == "streaming_chunk" && !IsTranscriptMessage(event) {
			continue
		}
		collected = append(collected, event)
		if isCompactMessageType(event.Type) {
			count++
		}
		if count >= messages && event.Type == "user_message" {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(collected)-1; left < right; left, right = left+1, right-1 {
		collected[left], collected[right] = collected[right], collected[left]
	}
	return collected, nil
}

func (j *SQLiteEventJournal) compactHasOlder(sessionID string, before int64) (bool, error) {
	var found int
	err := j.db.QueryRow(`SELECT 1 FROM structured_chat_events WHERE session_id = ? AND sequence < ? AND json_extract(payload, '$.type') IN (`+compactTypePlaceholders()+`) LIMIT 1`, compactTypeArgs(sessionID, before)...).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (j *SQLiteEventJournal) ReadCompactPage(sessionID string, opts CompactPageOptions) (DurableEventPage, error) {
	if j == nil || j.db == nil || sessionID == "" {
		return DurableEventPage{Events: []Event{}}, nil
	}
	messages := opts.Messages
	if messages <= 0 {
		messages = CompactMessageWindow
	}
	turnCap := opts.LatestTurnCap
	if turnCap <= 0 {
		turnCap = CompactLatestTurnCap
	}
	var total int
	var maximum sql.NullInt64
	if err := j.db.QueryRow(`SELECT COUNT(*), MAX(sequence) FROM structured_chat_events WHERE session_id = ?`, sessionID).Scan(&total, &maximum); err != nil {
		return DurableEventPage{}, err
	}
	page := DurableEventPage{Events: []Event{}, Exists: total > 0}
	if total == 0 {
		return page, nil
	}
	page.JournalLatestSequence = maximum.Int64

	var head []Event
	boundary := opts.BeforeSequence
	if opts.BeforeSequence <= 0 {
		start, err := j.compactLatestTurnStart(sessionID, turnCap)
		if err != nil {
			return DurableEventPage{}, err
		}
		rows, err := j.db.Query(`SELECT payload FROM structured_chat_events WHERE session_id = ? AND sequence >= ? ORDER BY sequence ASC`, sessionID, start)
		if err != nil {
			return DurableEventPage{}, err
		}
		head, err = decodeCompactRows(rows)
		rows.Close()
		if err != nil {
			return DurableEventPage{}, err
		}
		boundary = start
	} else {
		page.HasNewer = opts.BeforeSequence <= maximum.Int64
	}
	older, err := j.compactOlder(sessionID, boundary, messages)
	if err != nil {
		return DurableEventPage{}, fmt.Errorf("compact older rows: %w", err)
	}
	page.Events = append(append(make([]Event, 0, len(older)+len(head)), older...), head...)
	if len(page.Events) == 0 {
		return page, nil
	}
	page.OldestSequence = page.Events[0].Sequence
	page.LatestSequence = page.Events[len(page.Events)-1].Sequence
	if page.HasOlder, err = j.compactHasOlder(sessionID, page.OldestSequence); err != nil {
		return DurableEventPage{}, err
	}
	return page, nil
}
