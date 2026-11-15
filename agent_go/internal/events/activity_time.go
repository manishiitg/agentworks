package events

import "time"

// RetainedActivityTime is display-only and intentionally memory-only. A cold
// chat has unknown recency until an existing event read/turn populates it.
func (es *EventStore) RetainedActivityTime(session string) time.Time {
	es.mu.RLock()
	defer es.mu.RUnlock()
	rows := es.events[session]
	if len(rows) == 0 {
		return time.Time{}
	}
	return rows[len(rows)-1].Timestamp
}
