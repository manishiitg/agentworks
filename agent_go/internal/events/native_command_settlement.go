package events

import (
	"context"
	"log"
	"strings"
	"time"

	agentevents "github.com/manishiitg/mcpagent/events"
)

// The journal returns only unresolved native command identifiers, not event
// payloads, arguments, output, or the session's replay tail.
type DurableNativeCommandReader interface {
	OpenNativeCommandIDs(sessionID string) ([]string, error)
}

func (j *SQLiteEventJournal) OpenNativeCommandIDs(sessionID string) ([]string, error) {
	if j == nil || j.db == nil || sessionID == "" {
		return nil, nil
	}
	conn, finish, err := j.coldReadConnection("open_native_commands", sessionID)
	if err != nil {
		return nil, err
	}
	defer finish()
	rows, err := conn.QueryContext(context.Background(), `WITH calls AS MATERIALIZED (
 SELECT sequence, json_extract(payload,'$.type') AS kind,
 json_extract(payload,'$.data.data.tool_call_id') AS tool_id,
 json_extract(payload,'$.data.data.tool_name') AS tool_name
 FROM structured_chat_events WHERE session_id=?
 AND json_extract(payload,'$.type') IN ('tool_call_start','tool_call_end','tool_call_error')
 ), lifecycle AS (
 SELECT tool_id,
 MAX(CASE WHEN kind='tool_call_start' AND tool_name='exec_command' THEN sequence ELSE 0 END) AS started,
 MAX(CASE WHEN kind IN ('tool_call_end','tool_call_error') THEN sequence ELSE 0 END) AS ended
 FROM calls GROUP BY tool_id)
 SELECT tool_id FROM lifecycle WHERE tool_id LIKE 'exec-%' AND started>ended ORDER BY started`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (es *EventStore) openNativeCommandIDs(sessionID string) []string {
	es.adoptJournaledSession(sessionID)
	es.mu.RLock()
	journal := es.durableJournal
	durable := es.persistenceClasses[sessionID] == SessionPersistenceInteractiveChat
	es.mu.RUnlock()
	if reader, ok := journal.(DurableNativeCommandReader); ok && durable {
		ids, err := reader.OpenNativeCommandIDs(sessionID)
		if err == nil {
			return ids
		}
		log.Printf("[EventStore] open native command lookup failed session=%s: %v", sessionID, err)
	}
	// Non-journal stores (including workflow execution stores) already hold their
	// accepted events. Scan those, never trigger replay as a side effect of Stop.
	es.mu.RLock()
	defer es.mu.RUnlock()
	pending := map[string]bool{}
	for _, event := range es.events[sessionID] {
		if event.Data == nil {
			continue
		}
		id, name := "", ""
		switch data := event.Data.Data.(type) {
		case *agentevents.ToolCallStartEvent:
			id, name = data.ToolCallID, data.ToolName
		case *agentevents.ToolCallEndEvent:
			id = data.ToolCallID
		case *agentevents.ToolCallErrorEvent:
			id = data.ToolCallID
		case *GenericEventData:
			id = stringField(data.Fields, "tool_call_id")
			name = stringField(data.Fields, "tool_name")
		}
		switch event.Type {
		case "tool_call_start":
			if name == "exec_command" && strings.HasPrefix(id, "exec-") {
				pending[id] = true
			}
		case "tool_call_end", "tool_call_error":
			delete(pending, id)
		}
	}
	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	return ids
}

// EndNativeCommandsWithoutResult is called only after the runtime is gone.
// It records the same neutral ends, but never loads the chat to find commands.
func (es *EventStore) EndNativeCommandsWithoutResult(sessionID string) {
	for _, id := range es.openNativeCommandIDs(sessionID) {
		now := time.Now()
		event := Event{ID: "native-command-unavailable:" + id, Type: "tool_call_end", Timestamp: now, SessionID: sessionID,
			Data: &agentevents.AgentEvent{Type: agentevents.EventType("tool_call_end"), Timestamp: now, SessionID: sessionID,
				Data: &agentevents.ToolCallEndEvent{ToolName: "exec_command", ToolCallID: id, SyntheticSettle: true, Result: "No result was captured before the command's runtime ended."}}}
		// Run the ordinary preparation, durable append, ownership, callbacks and SSE
		// publication. Only historical replay hydration is omitted for this end.
		if err := es.addEventChecked(sessionID, event, false); err != nil {
			log.Printf("[EventStore] native command settlement failed session=%s: %v", sessionID, err)
		}
	}
}
