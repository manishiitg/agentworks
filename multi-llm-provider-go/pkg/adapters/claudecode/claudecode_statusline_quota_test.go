package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

// writeTestStatusline plants a statusline sidecar in the shape Claude Code
// actually writes one. The literal below is trimmed from a real sidecar
// observed on this machine — rate_limits carries an integer percentage and an
// epoch-second reset per window.
func writeTestStatusline(t *testing.T, sessionName string, fiveHourPct float64, fiveHourReset time.Time) {
	t.Helper()
	payload := map[string]interface{}{
		"session_id": "test-session",
		"model":      map[string]interface{}{"display_name": "Sonnet"},
		"rate_limits": map[string]interface{}{
			"five_hour": map[string]interface{}{
				"used_percentage": fiveHourPct,
				"resets_at":       fiveHourReset.Unix(),
			},
			"seven_day": map[string]interface{}{
				"used_percentage": 31,
				"resets_at":       fiveHourReset.Add(72 * time.Hour).Unix(),
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal statusline: %v", err)
	}
	path := claudeStatuslinePath(sessionName)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write statusline: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
}

// TestUsageLimitErrorPrefersTheProviderStatedResetOverThePane is the point of
// the change. Both sources are present and they disagree, so whichever one
// wins is visible in the result.
//
// The pane prints a bare wall clock with no date; the sidecar states an exact
// epoch. Reconstructing the former when the latter is already on disk risks
// suspending a run until the wrong instant, and a resume that wakes into the
// same wall is worse than one that waits slightly too long.
func TestUsageLimitErrorPrefersTheProviderStatedResetOverThePane(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	structuredReset := now.Add(2 * time.Hour) // 14:00 UTC
	sessionName := fmt.Sprintf("plat101-prefers-%d", now.UnixNano())
	writeTestStatusline(t, sessionName, 99.6, structuredReset)

	// The pane names a different time on purpose: 11pm, not 2pm.
	pane := "You've hit your 5-hour limit · resets 11pm (UTC)"

	err := NewClaudeUsageLimitErrorForSession("claudecode", "", sessionName, pane, now)

	if !llmerrors.IsQuotaExhausted(err) {
		t.Fatalf("expected a quota-exhausted failure, got %v", err)
	}
	got := llmerrors.RetryAtOrZero(err)
	if !got.Equal(structuredReset) {
		t.Errorf("RetryAt = %v, want the provider-stated %v (pane text won instead)", got, structuredReset)
	}
	var typed *llmerrors.Error
	if !errors.As(err, &typed) || typed.Window != "five_hour" {
		t.Errorf("window not carried through; got %+v", typed)
	}
}

// TestUsageLimitErrorFallsBackToThePaneWhenNoStatuslineExists keeps the
// fallback alive. Not every wall has a readable sidecar — the file is written
// asynchronously and a session can hit its limit before the first render.
func TestUsageLimitErrorFallsBackToThePaneWhenNoStatuslineExists(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	sessionName := fmt.Sprintf("plat101-missing-%d", now.UnixNano())

	err := NewClaudeUsageLimitErrorForSession("claudecode", "", sessionName,
		"You've hit your 5-hour limit · resets 11pm (UTC)", now)

	want := time.Date(2026, 8, 18, 23, 0, 0, 0, time.UTC)
	if got := llmerrors.RetryAtOrZero(err); !got.Equal(want) {
		t.Errorf("RetryAt = %v, want the pane-stated %v", got, want)
	}
}

// TestUsageLimitErrorStaysUnknownWhenNeitherSourceStatesATime: an unknown
// reset must stay unknown. A fabricated instant schedules a resume that fails
// again, which is strictly worse than a run reporting it does not know.
func TestUsageLimitErrorStaysUnknownWhenNeitherSourceStatesATime(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	sessionName := fmt.Sprintf("plat101-silent-%d", now.UnixNano())

	err := NewClaudeUsageLimitErrorForSession("claudecode", "", sessionName,
		"You've hit your limit", now)

	if !llmerrors.IsQuotaExhausted(err) {
		t.Fatalf("expected a quota-exhausted failure, got %v", err)
	}
	if got := llmerrors.RetryAtOrZero(err); !got.IsZero() {
		t.Errorf("RetryAt = %v, want the zero time rather than a guess", got)
	}
}
