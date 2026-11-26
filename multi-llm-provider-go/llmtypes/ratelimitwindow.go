package llmtypes

import "time"

// RateLimitWindowsMetaKey is the StatusLine.Metadata key under which an
// adapter exposes its rate-limit windows as STRUCTURED data, alongside the
// human-readable segments under StatusExtrasMetaKey.
//
// Both exist deliberately. StatusExtrasMetaKey is display-ready text a UI
// renders verbatim ("7d 100% →Fri"); this key is machine-readable state a
// runtime can act on. PLAT-101: the reset timestamp was being read from the
// provider and then formatted away, so nothing downstream could tell how long
// a workflow had to wait, or persist that across a restart. A formatted
// string cannot answer either question.
const RateLimitWindowsMetaKey = "rate_limit_windows"

// RateLimitWindow is one provider usage window (Claude's five_hour /
// seven_day, and equivalents elsewhere) in machine-readable form.
type RateLimitWindow struct {
	// Name is the provider's own window identifier, e.g. "five_hour".
	Name string `json:"name"`
	// UsedPercent is 0-100. 100 means the window is exhausted.
	UsedPercent float64 `json:"used_percent"`
	// ResetsAt is the absolute instant the window reopens. Zero when the
	// provider did not state one — callers must treat that as "unknown", never
	// substitute a guess.
	ResetsAt time.Time `json:"resets_at,omitempty"`
}

// Exhausted reports whether this window is at its limit.
func (w RateLimitWindow) Exhausted() bool { return w.UsedPercent >= 100 }

// EarliestReset returns the soonest future reset among exhausted windows, and
// its window name. This is the instant a caller may retry: a run blocked by
// several exhausted windows is unblocked by the first one that reopens.
//
// Windows with no reset time, windows already in the past, and windows that
// are not exhausted are all skipped — a non-exhausted window is not what is
// blocking the caller, and a past reset carries no information about the
// current block. Returns the zero time when nothing qualifies.
func EarliestReset(windows []RateLimitWindow, now time.Time) (time.Time, string) {
	var best time.Time
	var name string
	for _, w := range windows {
		if !w.Exhausted() || w.ResetsAt.IsZero() || !w.ResetsAt.After(now) {
			continue
		}
		if best.IsZero() || w.ResetsAt.Before(best) {
			best, name = w.ResetsAt, w.Name
		}
	}
	return best, name
}

// SetRateLimitWindows stores structured windows on a StatusLine's Metadata,
// allocating the map if needed. No-op when empty so the key stays absent
// rather than carrying an empty list.
func (s *StatusLine) SetRateLimitWindows(windows []RateLimitWindow) {
	if s == nil || len(windows) == 0 {
		return
	}
	if s.Metadata == nil {
		s.Metadata = map[string]interface{}{}
	}
	s.Metadata[RateLimitWindowsMetaKey] = windows
}

// RateLimitWindows reads structured windows back off a StatusLine. It accepts
// both the in-process []RateLimitWindow and the []interface{} shape the same
// value takes after a JSON round trip, because a StatusLine crosses process
// boundaries between the adapter that fills it and the runtime that reads it.
func (s *StatusLine) RateLimitWindows() []RateLimitWindow {
	if s == nil || s.Metadata == nil {
		return nil
	}
	raw, ok := s.Metadata[RateLimitWindowsMetaKey]
	if !ok {
		return nil
	}
	if windows, ok := raw.([]RateLimitWindow); ok {
		return windows
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	var out []RateLimitWindow
	for _, item := range list {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		w := RateLimitWindow{}
		if name, ok := m["name"].(string); ok {
			w.Name = name
		}
		if pct, ok := m["used_percent"].(float64); ok {
			w.UsedPercent = pct
		}
		if ts, ok := m["resets_at"].(string); ok && ts != "" {
			if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
				w.ResetsAt = parsed
			}
		}
		out = append(out, w)
	}
	return out
}

// MostConstrainedReset returns the reset instant of the window closest to its
// limit, and that window's name.
//
// This is the companion to EarliestReset, and the two answer different
// questions on purpose. EarliestReset asks "several windows are exhausted —
// which one unblocks me first?" and therefore only considers windows already
// at 100%. That is the right question after a request was rejected because
// every blocking window is, by definition, at its limit.
//
// It is the wrong question when a caller already knows it hit a wall from
// another signal (PLAT-101: the CLI printed a usage-limit line) and needs the
// reset instant to suspend on. The statusline that carries the percentages is
// written asynchronously, so at the moment of the wall it routinely reads 99.x
// rather than a clean 100 — and EarliestReset would return nothing at all,
// silently demoting an exact provider-stated instant to a parsed-from-text
// guess. Ranking by fullness instead answers "which window is blocking me",
// which is what the caller actually asked.
//
// Windows without a future reset are skipped: a reset already in the past
// describes a window that has since reopened and cannot be what is blocking
// the caller now. Returns the zero time when nothing qualifies — never a
// guess.
func MostConstrainedReset(windows []RateLimitWindow, now time.Time) (time.Time, string) {
	var best time.Time
	var name string
	var highest float64 = -1
	for _, w := range windows {
		if w.ResetsAt.IsZero() || !w.ResetsAt.After(now) {
			continue
		}
		if w.UsedPercent > highest {
			best, name, highest = w.ResetsAt, w.Name, w.UsedPercent
		}
	}
	return best, name
}
