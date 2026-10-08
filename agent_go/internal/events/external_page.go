package events

import (
	"encoding/json"
	"strings"
)

// ForwardEventPage is a bounded page for external clients. Cursors are absolute
// event indices, including filtered events. CursorReset tells a client that its
// cursor is outside the retained log (for example, after a server restart).
type ForwardEventPage struct {
	GetEventsResult
	CursorReset         bool
	FirstAvailableIndex int
}

// GetForwardEventPage returns at most limit non-streaming events in order. It
// scans the retained log in place instead of allocating a filtered full copy.
// Unlike the legacy UI polling path, an initial request is bounded as well.
func (es *EventStore) GetForwardEventPage(sessionID string, sinceIndex, limit int) ForwardEventPage {
	return es.GetForwardEventPageBudget(sessionID, sinceIndex, limit, 0)
}

// GetForwardEventPageBudget is GetForwardEventPage with a size budget: it stops
// adding events once their JSON would pass maxBytes (always returning at least
// one), sets HasMore, and leaves LastProcessedIndex at the last event returned,
// so a client continues from there. Every event carries its full tool output;
// a count limit alone returned 400-600 KB pages (MCP feedback, 2026-10-08).
// maxBytes <= 0 means no size budget.
func (es *EventStore) GetForwardEventPageBudget(sessionID string, sinceIndex, limit, maxBytes int) ForwardEventPage {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	es.mu.RLock()
	defer es.mu.RUnlock()
	rows, exists := es.events[sessionID]
	base := es.sessionStartIndices[sessionID]
	page := ForwardEventPage{
		GetEventsResult:     GetEventsResult{Events: []Event{}, Exists: exists, TotalCount: len(rows), LastProcessedIndex: base - 1},
		FirstAvailableIndex: base,
	}
	last := base + len(rows) - 1
	if !exists || sinceIndex > last {
		page.CursorReset = sinceIndex > last
		page.LastProcessedIndex = last
		return page
	}
	if sinceIndex < base-1 {
		page.CursorReset = true
	}
	start := 0
	if sinceIndex >= base {
		start = sinceIndex - base + 1
	}
	page.LastProcessedIndex = base + start - 1
	used := 0
	for i := start; i < len(rows); i++ {
		if shouldReturnEvent(rows[i], false) {
			if len(page.Events) == limit {
				page.HasMore = true
				break
			}
			if maxBytes > 0 {
				size := 0
				if raw, err := json.Marshal(rows[i]); err == nil {
					size = len(raw)
				}
				if len(page.Events) > 0 && used+size > maxBytes {
					page.HasMore = true
					break
				}
				used += size
			}
			page.Events = append(page.Events, rows[i])
		}
		page.LastProcessedIndex = base + i
	}
	return page
}

// LastIndex is the index of the newest retained event of the session, or -1.
func (es *EventStore) LastIndex(sessionID string) int {
	es.mu.RLock()
	defer es.mu.RUnlock()
	rows, exists := es.events[sessionID]
	if !exists {
		return -1
	}
	return es.sessionStartIndices[sessionID] + len(rows) - 1
}

// Event types that carry an assistant reply: the main turn's reply first, the
// others (agent and background-agent ends) only when it is missing.
var (
	mainReplyEventTypes  = map[string]bool{"unified_completion": true, "llm_generation_end": true}
	otherReplyEventTypes = map[string]bool{"agent_end": true, "background_agent_completed": true, "orchestrator_agent_end": true}
)

// LatestAnswer is the newest assistant reply at an index after afterIndex.
func (es *EventStore) LatestAnswer(sessionID string, afterIndex int) (string, bool) {
	es.mu.RLock()
	defer es.mu.RUnlock()
	rows, exists := es.events[sessionID]
	if !exists {
		return "", false
	}
	base := es.sessionStartIndices[sessionID]
	for _, kinds := range []map[string]bool{mainReplyEventTypes, otherReplyEventTypes} {
		for i := len(rows) - 1; i >= 0 && base+i > afterIndex; i-- {
			if kinds[rows[i].Type] {
				if text := eventReplyText(rows[i]); text != "" {
					return text, true
				}
			}
		}
	}
	return "", false
}

func eventReplyText(e Event) string {
	if e.Data == nil {
		return ""
	}
	raw, err := json.Marshal(e.Data)
	if err != nil {
		return ""
	}
	var outer map[string]interface{}
	if json.Unmarshal(raw, &outer) != nil {
		return ""
	}
	payload := outer
	if nested, ok := outer["data"].(map[string]interface{}); ok {
		payload = nested
	}
	for _, key := range []string{"content", "final_result", "result"} {
		if text, ok := payload[key].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}
