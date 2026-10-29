package events

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

type usageMessageReader interface {
	UsageTurnMessages(context.Context, string, []string) (map[string]string, error)
}

// UsageTurnMessages reads existing chat text by exact execution identity. It
// never puts prompts in the cost ledger or guesses from model-call timestamps.
func (es *EventStore) UsageTurnMessages(ctx context.Context, session string, ids []string) (map[string]string, error) {
	es.adoptJournaledSession(session)
	es.mu.RLock()
	reader, ok := es.durableJournal.(usageMessageReader)
	es.mu.RUnlock()
	if !ok {
		return nil, errors.New("chat history unavailable")
	}
	return reader.UsageTurnMessages(ctx, session, ids)
}

func (j *SQLiteEventJournal) UsageTurnMessages(ctx context.Context, session string, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		var text string
		// Saved reply boundaries also link older messages recorded before the
		// observer supplied usage_execution_id. Missing/retained-away spans stay absent.
		err := j.db.QueryRowContext(ctx, `WITH span AS (
   SELECT CAST(json_extract(payload,'$.data.data.start_sequence') AS INTEGER) AS start,
          CAST(json_extract(payload,'$.data.data.end_sequence') AS INTEGER) AS finish
   FROM structured_chat_events WHERE session_id=? AND json_extract(payload,'$.type')='reply_file_changes'
   AND json_extract(payload,'$.execution_id')=? ORDER BY sequence LIMIT 1
  ) SELECT COALESCE(
   json_extract(payload,'$.data.data.metadata.display_content'),
   json_extract(payload,'$.data.data.data.metadata.display_content'),
   json_extract(payload,'$.data.data.content'), json_extract(payload,'$.data.data.message'),
   json_extract(payload,'$.data.data.data.content'), json_extract(payload,'$.data.data.data.message'), '')
  FROM structured_chat_events WHERE session_id=? AND json_extract(payload,'$.type')='user_message'
   AND COALESCE(json_extract(payload,'$.execution_kind'),'main_agent') IN ('main','main_agent','chat','')
   AND (json_extract(payload,'$.usage_execution_id')=?
     OR (json_extract(payload,'$.execution_id')=? AND json_extract(payload,'$.execution_id')<>?)
     OR sequence>(SELECT start FROM span) AND sequence<=(SELECT finish FROM span))
  ORDER BY sequence LIMIT 1`, session, id, session, id, id, "main:"+session).Scan(&text)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(text) != "" {
			out[id] = text
		}
	}
	return out, nil
}
