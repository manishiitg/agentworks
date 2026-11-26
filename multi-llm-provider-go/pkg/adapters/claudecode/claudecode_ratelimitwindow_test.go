package claudecode

import (
	"encoding/json"
	"testing"
	"time"
)

// The exact status-line shape PLAT-101 quotes from Claude Code. The adapter
// already read these fields, but only to format them into display text -- the
// timestamp was destroyed at the one place it entered the system.
const claudeStatusLineJSON = `{
  "rate_limits": {
    "five_hour":  {"used_percentage": 100, "resets_at": 1786644000},
    "seven_day":  {"used_percentage": 100, "resets_at": 1786721400}
  },
  "context_window": {"used_percentage": 4},
  "effort": {"level": "xhigh"}
}`

func TestClaudeRateLimitWindowsPreserveResetInstants(t *testing.T) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(claudeStatusLineJSON), &raw); err != nil {
		t.Fatal(err)
	}

	windows := claudeRateLimitWindows(raw)
	if len(windows) != 2 {
		t.Fatalf("windows = %d, want 2 (five_hour, seven_day)", len(windows))
	}

	byName := map[string]time.Time{}
	for _, w := range windows {
		if !w.Exhausted() {
			t.Errorf("%s at %v%% should report exhausted", w.Name, w.UsedPercent)
		}
		byName[w.Name] = w.ResetsAt
	}
	if got, want := byName["five_hour"], time.Unix(1786644000, 0).UTC(); !got.Equal(want) {
		t.Errorf("five_hour resets_at = %v, want %v", got, want)
	}
	if got, want := byName["seven_day"], time.Unix(1786721400, 0).UTC(); !got.Equal(want) {
		t.Errorf("seven_day resets_at = %v, want %v", got, want)
	}
}

// A window the provider omitted a reset for is still reported when exhausted:
// "exhausted, reset unknown" is a distinct state the caller must be able to
// see, and is not the same as "not exhausted".
func TestClaudeRateLimitWindowsKeepExhaustedWindowWithoutReset(t *testing.T) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(`{"rate_limits":{"seven_day":{"used_percentage":100}}}`), &raw); err != nil {
		t.Fatal(err)
	}
	windows := claudeRateLimitWindows(raw)
	if len(windows) != 1 {
		t.Fatalf("windows = %d, want 1", len(windows))
	}
	if !windows[0].Exhausted() {
		t.Error("window should report exhausted")
	}
	if !windows[0].ResetsAt.IsZero() {
		t.Errorf("resets_at = %v, want zero when the provider stated none", windows[0].ResetsAt)
	}
}

// Absent or malformed rate-limit data must degrade to nothing rather than
// fabricate windows -- the display path already behaves this way.
func TestClaudeRateLimitWindowsDegradeCleanly(t *testing.T) {
	for _, body := range []string{`{}`, `{"rate_limits":{}}`, `{"rate_limits":{"five_hour":{}}}`} {
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatal(err)
		}
		if windows := claudeRateLimitWindows(raw); len(windows) != 0 {
			t.Errorf("claudeRateLimitWindows(%s) = %#v, want none", body, windows)
		}
	}
}

// The display extras must keep working unchanged: the structured reader is an
// addition, not a replacement, and the two must not interfere.
func TestClaudeStatusExtrasStillRenderAlongsideStructuredWindows(t *testing.T) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(claudeStatusLineJSON), &raw); err != nil {
		t.Fatal(err)
	}
	extras := claudeStatusExtras(raw)
	if len(extras) == 0 {
		t.Fatal("expected display extras to still be produced")
	}
	if len(claudeRateLimitWindows(raw)) != 2 {
		t.Fatal("expected structured windows alongside the display extras")
	}
}
