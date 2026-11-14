package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type nativeGoalReader interface {
	LatestNativeGoal(context.Context, string) (*Event, error)
}

// LatestNativeGoal reads the current projection from accepted chat history,
// not the bounded live window. The caller authorizes the session first.
func (es *EventStore) LatestNativeGoal(ctx context.Context, sessionID string) (*Event, error) {
	es.mu.RLock()
	reader, ok := es.durableJournal.(nativeGoalReader)
	es.mu.RUnlock()
	if !ok {
		return nil, nil
	}
	return reader.LatestNativeGoal(ctx, sessionID)
}

func (j *SQLiteEventJournal) LatestNativeGoal(ctx context.Context, sessionID string) (*Event, error) {
	var raw []byte
	err := j.db.QueryRowContext(ctx, `SELECT payload FROM structured_chat_events
 WHERE session_id=? AND json_extract(payload,'$.type')='native_goal_updated'
 AND COALESCE(json_extract(payload,'$.parent_execution_id'),'') IN ('','session:' || ?)
 AND COALESCE(json_extract(payload,'$.execution_kind'),'') IN ('','main_agent')
 ORDER BY sequence DESC LIMIT 1`, sessionID, sessionID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var event Event
	if err = json.Unmarshal(raw, &event); err != nil {
		return nil, err
	}
	return &event, nil
}
