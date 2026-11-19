package events

import (
	"context"
	"encoding/json"
)

// MaxChatHelperEvents bounds one helper-list read: enough rows for the
// helpers of a long chat, small enough to send in one response.
const MaxChatHelperEvents = 1000

type chatHelperEventsReader interface {
	ChatHelperEvents(context.Context, string, int) ([]Event, error)
}

// ChatHelperEvents returns the saved events that describe a chat's helper
// agents, oldest first: helper activity from Claude Code and Cursor, and the
// tool steps that start or check a Codex helper or a coding-agent job
// (#3245). It reads the whole saved chat, not the live window, so a long
// chat keeps its early helpers. The caller authorizes the session first.
func (es *EventStore) ChatHelperEvents(ctx context.Context, sessionID string) ([]Event, error) {
	if es == nil || sessionID == "" {
		return []Event{}, nil
	}
	es.adoptJournaledSession(sessionID)
	es.mu.RLock()
	reader, ok := es.durableJournal.(chatHelperEventsReader)
	es.mu.RUnlock()
	if !ok || reader == nil {
		return []Event{}, nil
	}
	return reader.ChatHelperEvents(ctx, sessionID, MaxChatHelperEvents)
}

// The tools whose steps the helper list reads (see chatHelpers.ts). The job
// tools arrive with their server's prefix (mcp__llm-provider__...), so their
// names are matched by ending.
const chatHelperToolMatch = `(tool_name='spawnAgent'
   OR tool_name LIKE '%delegate\_coding\_agent' ESCAPE '\'
   OR tool_name LIKE '%get\_coding\_agent\_job' ESCAPE '\'
   OR tool_name LIKE '%cancel\_coding\_agent\_job' ESCAPE '\')`

func (j *SQLiteEventJournal) ChatHelperEvents(ctx context.Context, sessionID string, limit int) ([]Event, error) {
	if j == nil || j.db == nil || sessionID == "" {
		return []Event{}, nil
	}
	if limit <= 0 || limit > MaxChatHelperEvents {
		limit = MaxChatHelperEvents
	}
	rows, err := j.reader().QueryContext(ctx, `SELECT payload FROM (
  SELECT sequence, payload, json_extract(payload,'$.type') AS type,
   json_extract(payload,'$.data.data.tool_name') AS tool_name,
   COALESCE(json_extract(payload,'$.data.data.record.kind'), json_extract(payload,'$.data.record.kind')) AS record_kind
  FROM structured_chat_events WHERE session_id=?)
 WHERE (type='coding_agent_native_activity' AND record_kind='helper')
  OR (type IN ('tool_call_start','tool_call_end','tool_call_error') AND `+chatHelperToolMatch+`)
 ORDER BY sequence DESC LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := make([]Event, 0, 32)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var event Event
		if err := json.Unmarshal(payload, &event); err != nil {
			continue // one unreadable row must not hide the rest of the list
		}
		found = append(found, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(found)-1; left < right; left, right = left+1, right-1 {
		found[left], found[right] = found[right], found[left]
	}
	return found, nil
}
