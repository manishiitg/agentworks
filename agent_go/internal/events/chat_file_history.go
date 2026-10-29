package events

import (
	"context"
	"encoding/json"
	"errors"
)

// ChatFileHistory reads only saved reply references from the existing journal.
// FirstUserSequence prevents calling a later retained snapshot the chat's start.
type ChatFileHistory struct {
	Reviews           []Event
	References        []Event
	FirstUserSequence int64
	FirstSnapshotID   string
}

// ChatUserMessage is one message the person sent, for naming where a
// comparison starts.
type ChatUserMessage struct {
	Sequence int64
	Text     string
	At       string
}
type chatUserMessageReader interface {
	ChatUserMessageBetween(context.Context, string, int64, int64) (ChatUserMessage, error)
}

// ChatUserMessageBetween returns the first message the person sent after
// sequence after and up to through (one reply's span).
func (es *EventStore) ChatUserMessageBetween(ctx context.Context, sessionID string, after, through int64) (ChatUserMessage, error) {
	es.mu.RLock()
	reader, ok := es.durableJournal.(chatUserMessageReader)
	es.mu.RUnlock()
	if !ok {
		return ChatUserMessage{}, errors.New("chat history unavailable")
	}
	return reader.ChatUserMessageBetween(ctx, sessionID, after, through)
}

func (j *SQLiteEventJournal) ChatUserMessageBetween(ctx context.Context, sessionID string, after, through int64) (ChatUserMessage, error) {
	var out ChatUserMessage
	err := j.db.QueryRowContext(ctx, `SELECT sequence,
		COALESCE(json_extract(payload,'$.data.data.content'), json_extract(payload,'$.data.data.message'), json_extract(payload,'$.data.data.data.content'), json_extract(payload,'$.data.data.data.message'), ''),
		COALESCE(json_extract(payload,'$.timestamp'), '')
		FROM structured_chat_events WHERE session_id=? AND json_extract(payload,'$.type')='user_message' AND sequence>? AND sequence<=? ORDER BY sequence LIMIT 1`, sessionID, after, through).Scan(&out.Sequence, &out.Text, &out.At)
	return out, err
}

type chatFileHistoryReader interface {
	ReadChatFileHistory(context.Context, string) (ChatFileHistory, error)
}

func (es *EventStore) ReadChatFileHistory(ctx context.Context, sessionID string) (ChatFileHistory, error) {
	es.adoptJournaledSession(sessionID)
	es.mu.RLock()
	reader, ok := es.durableJournal.(chatFileHistoryReader)
	class := es.persistenceClasses[sessionID]
	es.mu.RUnlock()
	if !ok || class != SessionPersistenceInteractiveChat {
		return ChatFileHistory{}, errors.New("chat history unavailable")
	}
	return reader.ReadChatFileHistory(ctx, sessionID)
}

func (j *SQLiteEventJournal) ReadChatFileHistory(ctx context.Context, sessionID string) (ChatFileHistory, error) {
	out := ChatFileHistory{References: []Event{}}
	err := j.db.QueryRowContext(ctx, `SELECT COALESCE(MIN(sequence),0) FROM structured_chat_events WHERE session_id=? AND json_extract(payload,'$.type')='user_message'`, sessionID).Scan(&out.FirstUserSequence)
	if err != nil {
		return out, err
	}
	// Arrival can lag the next reply. Order by the saved boundary, not arrival.
	// A reply whose changes were not saved has no snapshot_id and is skipped.
	err = j.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT json_extract(payload,'$.data.data.snapshot_id') FROM structured_chat_events WHERE session_id=? AND json_extract(payload,'$.type')='reply_file_changes' AND COALESCE(json_extract(payload,'$.data.data.snapshot_id'),'')<>'' ORDER BY CAST(json_extract(payload,'$.data.data.start_sequence') AS INTEGER) ASC LIMIT 1),'')`, sessionID).Scan(&out.FirstSnapshotID)
	if err != nil {
		return out, err
	}
	rows, err := j.db.QueryContext(ctx, `SELECT payload FROM structured_chat_events WHERE session_id=? AND json_extract(payload,'$.type')='reply_file_changes' AND COALESCE(json_extract(payload,'$.data.data.snapshot_id'),'')<>'' ORDER BY CAST(json_extract(payload,'$.data.data.end_sequence') AS INTEGER) DESC LIMIT 50`, sessionID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return out, err
		}
		var event Event
		if err := json.Unmarshal(raw, &event); err != nil {
			return out, err
		}
		out.References = append(out.References, event)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	rows.Close()
	reviews, err := j.db.QueryContext(ctx, `SELECT payload FROM structured_chat_events WHERE session_id=? AND json_extract(payload,'$.type')='file_reviewed' ORDER BY sequence`, sessionID)
	if err != nil {
		return out, err
	}
	defer reviews.Close()
	for reviews.Next() {
		var raw []byte
		if err := reviews.Scan(&raw); err != nil {
			return out, err
		}
		var event Event
		if err := json.Unmarshal(raw, &event); err != nil {
			return out, err
		}
		out.Reviews = append(out.Reviews, event)
	}
	return out, reviews.Err()
}
