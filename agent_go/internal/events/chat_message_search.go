package events

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// PLAT-856: index only readable message carriers, not tools, thinking, system
// prompts or streaming deltas. Keep the SQL predicate identical in both reads
// so SQLite can use the partial index without examining tool payloads.
const chatMessagePredicate = `(json_extract(payload,'$.type')='user_message'
 OR (json_extract(payload,'$.type') IN ('llm_generation_end','unified_completion','agent_end') AND COALESCE(json_extract(payload,'$.reply_blocks_complete'),0)=0)
 OR (json_extract(payload,'$.type')='streaming_chunk' AND json_extract(payload,'$.data.data.source')='transcript'
 AND COALESCE(json_extract(payload,'$.data.data.is_delta'),0)=0
 AND COALESCE(json_extract(payload,'$.data.data.is_tool_call'),0)=0
 AND COALESCE(json_extract(payload,'$.data.data.metadata.kind'),'')!='terminal'
 AND COALESCE(json_extract(payload,'$.data.data.metadata.replace'),0)=0
 AND (json_extract(payload,'$.data.data.metadata.assistant_block_id') IS NULL OR json_extract(payload,'$.data.data.metadata.assistant_block_phase')='end')))`

func installChatMessageIndexes(j *SQLiteEventJournal) error {
	for _, query := range []string{
		`CREATE INDEX IF NOT EXISTS structured_chat_message_scan ON structured_chat_events(created_at DESC, session_id DESC, sequence DESC) WHERE ` + chatMessagePredicate,
		`CREATE INDEX IF NOT EXISTS structured_chat_message_window ON structured_chat_events(session_id, sequence) WHERE ` + chatMessagePredicate,
	} {
		if _, err := j.db.Exec(query); err != nil {
			return err
		}
	}
	return nil
}

type ChatMessageCursor struct {
	At       string `json:"at"`
	Session  string `json:"session"`
	Sequence int64  `json:"sequence"`
}

type ChatMessage struct {
	SessionID string `json:"session_id"`
	ID        string `json:"id"`
	Sequence  int64  `json:"sequence"`
	At        string `json:"at"`
	Role      string `json:"role"`
	Text      string `json:"text"`
}

type ChatMessageScan struct {
	Messages []ChatMessage
	Next     *ChatMessageCursor
	Scanned  int
	Bytes    int
}

type chatMessageReader interface {
	ScanChatMessages(context.Context, *ChatMessageCursor) (ChatMessageScan, error)
	ChatMessageWindow(context.Context, string, int64) ([]ChatMessage, error)
}

func (es *EventStore) ScanChatMessages(ctx context.Context, cursor *ChatMessageCursor) (ChatMessageScan, error) {
	es.mu.RLock()
	reader, ok := es.durableJournal.(chatMessageReader)
	es.mu.RUnlock()
	if !ok {
		return ChatMessageScan{}, errors.New("chat history unavailable")
	}
	return reader.ScanChatMessages(ctx, cursor)
}
func (es *EventStore) ChatMessageWindow(ctx context.Context, session string, sequence int64) ([]ChatMessage, error) {
	es.mu.RLock()
	reader, ok := es.durableJournal.(chatMessageReader)
	es.mu.RUnlock()
	if !ok {
		return nil, errors.New("chat history unavailable")
	}
	return reader.ChatMessageWindow(ctx, session, sequence)
}

// A request reads at most 128 bounded journal messages (normally <=8 MB),
// regardless of the total store size. The cursor grants no read authority.
func (j *SQLiteEventJournal) ScanChatMessages(ctx context.Context, cursor *ChatMessageCursor) (ChatMessageScan, error) {
	out := ChatMessageScan{Messages: []ChatMessage{}}
	query := `SELECT session_id, sequence, created_at, payload FROM structured_chat_events WHERE ` + chatMessagePredicate
	args := []any{}
	if cursor != nil {
		query += ` AND (created_at,session_id,sequence)<(?,?,?)`
		args = append(args, cursor.At, cursor.Session, cursor.Sequence)
	}
	query += ` ORDER BY created_at DESC, session_id DESC, sequence DESC LIMIT 129`
	rows, err := j.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var last *ChatMessageCursor
	for rows.Next() {
		var session, at string
		var sequence int64
		var raw []byte
		if err = rows.Scan(&session, &sequence, &at, &raw); err != nil {
			return out, err
		}
		if out.Scanned == 128 {
			out.Next = last
			break
		}
		out.Scanned++
		out.Bytes += len(raw)
		last = &ChatMessageCursor{At: at, Session: session, Sequence: sequence}
		if message, ok := indexedChatMessage(raw); ok {
			out.Messages = append(out.Messages, message)
		}
	}
	return out, rows.Err()
}

func (j *SQLiteEventJournal) ChatMessageWindow(ctx context.Context, session string, sequence int64) ([]ChatMessage, error) {
	// Two indexed seeks, rather than loading a whole conversation. Both sides
	// include the selected boundary, then the duplicate is removed below.
	query := `SELECT payload FROM (SELECT sequence,payload FROM structured_chat_events WHERE session_id=? AND sequence<=? AND ` + chatMessagePredicate + ` ORDER BY sequence DESC LIMIT 13)
 UNION ALL SELECT payload FROM (SELECT sequence,payload FROM structured_chat_events WHERE session_id=? AND sequence>? AND ` + chatMessagePredicate + ` ORDER BY sequence LIMIT 12)`
	rows, err := j.reader().QueryContext(ctx, query, session, sequence, session, sequence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatMessage{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if m, ok := indexedChatMessage(raw); ok {
			out = append(out, m)
		}
	}
	// The first seek is descending; callers need transcript order.
	for i := 1; i < len(out); i++ {
		for k := i; k > 0 && out[k].Sequence < out[k-1].Sequence; k-- {
			out[k], out[k-1] = out[k-1], out[k]
		}
	}
	return out, rows.Err()
}

func indexedChatMessage(raw []byte) (ChatMessage, bool) {
	var e struct {
		ID        string  `json:"id"`
		Type      string  `json:"type"`
		Session   string  `json:"session_id"`
		Sequence  int64   `json:"sequence"`
		At        string  `json:"timestamp"`
		Remainder *string `json:"reply_remainder"`
		Data      struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return ChatMessage{}, false
	}
	fields := e.Data.Data
	if nested, ok := fields["data"].(map[string]any); ok {
		fields = nested
	}
	text := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := fields[key].(string); ok && strings.TrimSpace(value) != "" {
				return value
			}
		}
		return ""
	}
	role := "assistant"
	content := ""
	if e.Type == "user_message" {
		role = "user"
		content = text("content", "message")
		if metadata, ok := fields["metadata"].(map[string]any); ok {
			if metadata["source"] == "agent_message" {
				content, _ = metadata["display_content"].(string)
			}
		}
	} else {
		content = text("content", "final_result", "result")
		if e.Remainder != nil {
			content = *e.Remainder
		}
	}
	if strings.TrimSpace(content) == "" {
		return ChatMessage{}, false
	}
	return ChatMessage{SessionID: e.Session, ID: e.ID, Sequence: e.Sequence, At: e.At, Role: role, Text: content}, true
}
