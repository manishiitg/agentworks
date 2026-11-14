package claudecode

import (
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// claudeCompactMetadata is Claude Code's compaction record, in both spellings
// it uses: snake_case on stream-json `system/compact_boundary.compact_metadata`
// (from the bundled 2.1.289 schema) and camelCase on the transcript's
// `system/compact_boundary.compactMetadata` (verified on real transcripts:
// {"trigger":"auto","preTokens":935757,"postTokens":21900,"durationMs":195257}).
type claudeCompactMetadata struct {
	Trigger         string `json:"trigger"`
	PreTokens       int    `json:"pre_tokens"`
	PostTokens      int    `json:"post_tokens"`
	DurationMs      int64  `json:"duration_ms"`
	PreTokensCamel  int    `json:"preTokens"`
	PostTokensCamel int    `json:"postTokens"`
	DurationMsCamel int64  `json:"durationMs"`
}

func (m *claudeCompactMetadata) normalized() (pre, post int, durationMs int64) {
	if m == nil {
		return 0, 0, 0
	}
	pre, post, durationMs = m.PreTokens, m.PostTokens, m.DurationMs
	if pre == 0 {
		pre = m.PreTokensCamel
	}
	if post == 0 {
		post = m.PostTokensCamel
	}
	if durationMs == 0 {
		durationMs = m.DurationMsCamel
	}
	return pre, post, durationMs
}

// claudeCompactionFromBoundary maps a compact_boundary record to the shared end
// event. endedAt is the record's timestamp (or now for stream-json, which has
// none).
func claudeCompactionFromBoundary(id string, meta *claudeCompactMetadata, endedAt time.Time) llmtypes.ContextCompaction {
	pre, post, durationMs := meta.normalized()
	c := llmtypes.ContextCompaction{
		Provider:     "claude-code",
		Phase:        llmtypes.ContextCompactionPhaseEnd,
		ID:           id,
		Outcome:      llmtypes.ContextCompactionOutcomeSuccess,
		TokensBefore: pre,
		TokensAfter:  post,
		EndedAt:      endedAt.UTC(),
		DurationMs:   durationMs,
	}
	if meta != nil {
		c.Trigger = strings.TrimSpace(meta.Trigger)
	}
	if durationMs > 0 && !endedAt.IsZero() {
		c.StartedAt = endedAt.Add(-time.Duration(durationMs) * time.Millisecond).UTC()
	}
	return c
}

// claudeStructuredCompaction follows Claude's stream-json compaction signals:
//
//	{"type":"system","subtype":"status","status":"compacting"}            start (repeated every 30s)
//	{"type":"system","subtype":"compact_boundary","compact_metadata":{…}} end with token counts
//	{"type":"system","subtype":"status","status":null,"compact_result":…} end ("success"|"failed")
//
// The order of the last two is not documented, so an end is emitted by
// whichever arrives first, and a later boundary re-emits the end with the same
// ID carrying its numbers (consumers keep the newest end per ID).
type claudeStructuredCompaction struct {
	open      bool
	id        string
	startedAt time.Time
	boundary  bool // the current compaction's boundary was already emitted
}

func (s *claudeStructuredCompaction) observe(ev claudeStreamEvent, now time.Time) []llmtypes.ContextCompaction {
	if ev.Type != "system" {
		return nil
	}
	switch ev.Subtype {
	case "status":
		status := ""
		if ev.Status != nil {
			status = *ev.Status
		}
		if status == "compacting" {
			if s.open {
				return nil
			}
			s.open, s.boundary = true, false
			s.startedAt = now.UTC()
			s.id = "claude-compact-" + strconv.FormatInt(now.UnixMilli(), 10)
			return []llmtypes.ContextCompaction{{Provider: "claude-code", Phase: llmtypes.ContextCompactionPhaseStart, ID: s.id, StartedAt: s.startedAt}}
		}
		result := strings.TrimSpace(ev.CompactResult)
		if result == "" || !s.open {
			return nil
		}
		s.open = false
		if s.boundary {
			return nil
		}
		end := llmtypes.ContextCompaction{Provider: "claude-code", Phase: llmtypes.ContextCompactionPhaseEnd, ID: s.id,
			Outcome: llmtypes.ContextCompactionOutcomeSuccess, StartedAt: s.startedAt, EndedAt: now.UTC(), DurationMs: now.Sub(s.startedAt).Milliseconds()}
		if result != "success" {
			end.Outcome = llmtypes.ContextCompactionOutcomeFailed
		}
		return []llmtypes.ContextCompaction{end}
	case "compact_boundary":
		if s.id == "" {
			// A boundary with no announced start (manual /compact from a
			// resumed session, or an older CLI): still an end.
			s.id = "claude-compact-" + strconv.FormatInt(now.UnixMilli(), 10)
		}
		end := claudeCompactionFromBoundary(s.id, ev.CompactMetadata, now)
		if end.DurationMs == 0 && !s.startedAt.IsZero() {
			end.StartedAt = s.startedAt
			end.DurationMs = now.Sub(s.startedAt).Milliseconds()
		}
		s.boundary = true
		return []llmtypes.ContextCompaction{end}
	}
	return nil
}

// claudeLiveUsage builds the live context/plan snapshot for `claude -p`, which
// runs no statusline command: context fill from each assistant message's
// usage (the prompt of that API call: input + cache read + cache write) and
// plan windows from rate_limit_event. The context window is not reported on
// this transport, so only the token count is known.
type claudeLiveUsage struct {
	model   string
	context int
	window  int
	windows []llmtypes.RateLimitWindow
}

func (u *claudeLiveUsage) observeUsage(usage *claudeStreamUsage) bool {
	if usage == nil {
		return false
	}
	total := usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	if total <= 0 {
		return false
	}
	u.context = total
	return true
}

// claudeRateLimitInfo is stream-json's rate_limit_event.rate_limit_info
// (2.1.289 schema): utilization is a 0-1 fraction, resetsAt unix seconds.
type claudeRateLimitInfo struct {
	UnifiedWindows map[string]struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    int64   `json:"resetsAt"`
	} `json:"unifiedWindows"`
	RateLimitType string  `json:"rateLimitType"`
	Utilization   float64 `json:"utilization"`
	ResetsAt      int64   `json:"resetsAt"`
}

func (u *claudeLiveUsage) observeRateLimit(info *claudeRateLimitInfo) bool {
	if info == nil {
		return false
	}
	var windows []llmtypes.RateLimitWindow
	for _, name := range []string{"five_hour", "seven_day", "seven_day_overage_included"} {
		w, ok := info.UnifiedWindows[name]
		if !ok {
			continue
		}
		window := llmtypes.RateLimitWindow{Name: name, UsedPercent: w.Utilization * 100}
		if w.ResetsAt > 0 {
			window.ResetsAt = time.Unix(w.ResetsAt, 0).UTC()
		}
		windows = append(windows, window)
	}
	if len(windows) == 0 && info.RateLimitType != "" && info.Utilization > 0 {
		window := llmtypes.RateLimitWindow{Name: info.RateLimitType, UsedPercent: info.Utilization * 100}
		if info.ResetsAt > 0 {
			window.ResetsAt = time.Unix(info.ResetsAt, 0).UTC()
		}
		windows = append(windows, window)
	}
	if len(windows) == 0 {
		return false
	}
	u.windows = windows
	return true
}

func (u *claudeLiveUsage) statusLine() *llmtypes.StatusLine {
	if u == nil || (u.context <= 0 && u.window <= 0 && len(u.windows) == 0) {
		return nil
	}
	status := &llmtypes.StatusLine{Provider: "claudecode", Model: u.model}
	status.SetContextUsage(u.context, u.window)
	// A reported capacity alone is not a known current-context token count.
	if u.window > 0 {
		if status.Metadata == nil {
			status.Metadata = map[string]interface{}{}
		}
		status.Metadata[llmtypes.ContextWindowTokensMetaKey] = u.window
	}
	status.SetRateLimitWindows(u.windows)
	var extras []string
	for _, w := range u.windows {
		label := map[string]string{"five_hour": "5h", "seven_day": "7d"}[w.Name]
		if label == "" {
			continue
		}
		var reset int64
		if !w.ResetsAt.IsZero() {
			reset = w.ResetsAt.Unix()
		}
		extras = append(extras, llmtypes.FormatUsageExtraWithReset(label, w.UsedPercent, reset, time.Now()))
	}
	status.SetStatusExtras(extras)
	return status
}
