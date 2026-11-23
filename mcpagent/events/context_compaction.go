package events

// ContextCompaction is a coding CLI's own record that it compacted
// (summarised) its conversation context. Producers emit a start (when the CLI
// announces one) and an end; consumers pair them by CompactionID and keep the
// newest end for an ID.
const ContextCompaction EventType = "context_compaction"

// ContextCompactionEvent is one provider-neutral compaction lifecycle event.
// Fields other than Provider and Phase are optional; zero means unknown.
type ContextCompactionEvent struct {
	BaseEventData
	Provider     string `json:"provider"`
	Phase        string `json:"phase"` // start | end
	CompactionID string `json:"compaction_id,omitempty"`
	Trigger      string `json:"trigger,omitempty"`
	Outcome      string `json:"outcome,omitempty"` // success | failed | aborted (end)
	TokensBefore int    `json:"tokens_before,omitempty"`
	TokensAfter  int    `json:"tokens_after,omitempty"`
	StartedAt    string `json:"started_at,omitempty"` // RFC3339
	EndedAt      string `json:"ended_at,omitempty"`   // RFC3339
	DurationMs   int64  `json:"duration_ms,omitempty"`
}

func (*ContextCompactionEvent) GetEventType() EventType { return ContextCompaction }
