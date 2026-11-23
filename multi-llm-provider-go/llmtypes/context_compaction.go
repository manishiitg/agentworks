package llmtypes

import (
	"fmt"
	"sync"
	"time"
)

// StreamChunkTypeContextCompaction carries a coding CLI's own record that it
// compacted (summarised) its conversation context. The payload is
// StreamChunk.ContextCompaction.
//
// Adapters emit it only from the CLI's structured output (stream-json events,
// rollout/transcript records, session databases). Never from terminal text:
// a pane can show "compacted" for many reasons and carries no token counts.
const StreamChunkTypeContextCompaction StreamChunkType = "context_compaction"

// ContextCompactionPhase is where a compaction is in its lifecycle.
type ContextCompactionPhase string

const (
	// ContextCompactionPhaseStart is emitted when the CLI announces that it
	// began compacting (Claude, Pi and Muse record this; Codex and Agy do not).
	ContextCompactionPhaseStart ContextCompactionPhase = "start"
	// ContextCompactionPhaseEnd is emitted once the compaction finished (or
	// failed). CLIs that only write a record afterwards emit just this phase,
	// with StartedAt/DurationMs when the record carries them.
	ContextCompactionPhaseEnd ContextCompactionPhase = "end"
)

// Outcomes for ContextCompaction.Outcome on the end phase.
const (
	ContextCompactionOutcomeSuccess = "success"
	ContextCompactionOutcomeFailed  = "failed"
	ContextCompactionOutcomeAborted = "aborted"
)

// ContextCompaction is one provider-neutral compaction lifecycle event.
//
// Every field except Provider and Phase is optional: each CLI records a
// different subset, and a consumer renders only what is known (a missing
// TokensAfter is "unknown", never zero).
type ContextCompaction struct {
	Provider string                 `json:"provider"`
	Phase    ContextCompactionPhase `json:"phase"`
	// ID correlates a start with its end when the CLI makes that possible
	// (a record id, or the start time). Empty when it cannot be known; the
	// consumer then pairs an end with the newest open start.
	ID string `json:"id,omitempty"`
	// Trigger is "auto" or "manual" where the CLI says so, otherwise the
	// CLI's own reason ("threshold", "overflow").
	Trigger string `json:"trigger,omitempty"`
	// Outcome is set on the end phase: success, failed or aborted.
	Outcome      string    `json:"outcome,omitempty"`
	TokensBefore int       `json:"tokens_before,omitempty"`
	TokensAfter  int       `json:"tokens_after,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	EndedAt      time.Time `json:"ended_at,omitempty"`
	DurationMs   int64     `json:"duration_ms,omitempty"`
}

// ContextCompactionChunk wraps a compaction as a stream chunk.
func ContextCompactionChunk(c ContextCompaction) StreamChunk {
	return StreamChunk{Type: StreamChunkTypeContextCompaction, ContextCompaction: &c}
}

// Context-fill keys on StatusLine.Metadata. Display strings ("ctx 42%") live
// under StatusExtrasMetaKey; these are the numbers behind them so a UI can draw
// a meter and a runtime can act on them.
const (
	// ContextUsedTokensMetaKey is the prompt size of the most recent model
	// call: what the conversation currently occupies in the context window.
	ContextUsedTokensMetaKey = "context_used_tokens"
	// ContextWindowTokensMetaKey is the model's context window, when the CLI
	// reports it. Absent means unknown (no percentage can be drawn).
	ContextWindowTokensMetaKey = "context_window_tokens"
)

// SetContextUsage stores the context fill on a StatusLine's Metadata. A zero
// used count is a no-op; a zero window leaves the window key absent.
func (s *StatusLine) SetContextUsage(used, window int) {
	if s == nil || used <= 0 {
		return
	}
	if s.Metadata == nil {
		s.Metadata = map[string]interface{}{}
	}
	s.Metadata[ContextUsedTokensMetaKey] = used
	if window > 0 {
		s.Metadata[ContextWindowTokensMetaKey] = window
	}
}

// DefaultLiveUsageInterval is how often a live usage snapshot may be emitted
// during a turn. Codex writes a token_count record on every model call; the UI
// needs a meter, not every record.
const DefaultLiveUsageInterval = 3 * time.Second

// LiveUsageThrottle keeps only the newest status-line snapshot and releases it
// at most once per interval, and only when it differs from the last one
// released. Offer may be called on every record; Take on every poll tick.
// Safe for concurrent use.
type LiveUsageThrottle struct {
	Interval time.Duration

	mu       sync.Mutex
	pending  *StatusLine
	lastSent time.Time
	lastSig  string
}

// Offer records the newest snapshot, replacing any not yet taken.
func (t *LiveUsageThrottle) Offer(status *StatusLine) {
	if t == nil || status == nil {
		return
	}
	t.mu.Lock()
	t.pending = status
	t.mu.Unlock()
}

// Take returns the pending snapshot when the interval has passed since the
// last release and it differs from that release; otherwise nil. force skips
// the interval (used for the final flush of a turn).
func (t *LiveUsageThrottle) Take(now time.Time, force bool) *StatusLine {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending == nil {
		return nil
	}
	interval := t.Interval
	if interval <= 0 {
		interval = DefaultLiveUsageInterval
	}
	if !force && !t.lastSent.IsZero() && now.Sub(t.lastSent) < interval {
		return nil
	}
	sig := statusLineSignature(t.pending)
	if sig == t.lastSig {
		t.pending = nil
		return nil
	}
	out := t.pending
	t.pending = nil
	t.lastSent = now
	t.lastSig = sig
	return out
}

func statusLineSignature(s *StatusLine) string {
	if s == nil {
		return ""
	}
	return fmt.Sprintf("%s|%d|%d|%d|%d|%v|%v|%v", s.Model, s.InputTokens, s.OutputTokens, s.CacheReadInputTokens,
		s.CacheCreationInputTokens, s.Metadata[ContextUsedTokensMetaKey], s.Metadata[ContextWindowTokensMetaKey], s.Metadata[RateLimitWindowsMetaKey])
}
