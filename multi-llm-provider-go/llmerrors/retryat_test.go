package llmerrors

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// PLAT-101: RetryAfter (a duration) cannot survive being persisted and
// reloaded, because it is only meaningful relative to when it was computed. A
// suspension that outlives a restart needs the absolute instant.
func TestRetryAtSurvivesWrappingAndIsDistinctFromRetryAfter(t *testing.T) {
	reset := time.Date(2026, 8, 18, 23, 30, 0, 0, time.UTC)
	base := &Error{
		Kind: KindQuotaExhausted, Provider: "claudecode", Model: "sonnet",
		RetryAt: reset, Window: "seven_day", Err: errors.New("weekly limit reached"),
	}
	wrapped := fmt.Errorf("workflow step failed: %w", base)

	if got := RetryAtOrZero(wrapped); !got.Equal(reset) {
		t.Errorf("RetryAtOrZero through a wrap = %v, want %v", got, reset)
	}
	if got := QuotaWindow(wrapped); got != "seven_day" {
		t.Errorf("QuotaWindow through a wrap = %q, want seven_day", got)
	}
	if !IsQuotaExhausted(wrapped) {
		t.Error("quota exhaustion must remain classifiable through a wrap")
	}
}

// An unclassified error must answer "unknown" rather than panic or lie, so a
// caller can branch on it uniformly.
func TestRetryAtAccessorsHandlePlainErrors(t *testing.T) {
	if got := RetryAtOrZero(errors.New("boom")); !got.IsZero() {
		t.Errorf("RetryAtOrZero(plain) = %v, want zero", got)
	}
	if got := QuotaWindow(nil); got != "" {
		t.Errorf("QuotaWindow(nil) = %q, want empty", got)
	}
}

// A quota error with no reliable reset time is a real state (PLAT-101's
// "unknown reset time" branch) and must not be conflated with having one.
func TestQuotaErrorWithoutResetTimeReportsZero(t *testing.T) {
	err := &Error{Kind: KindQuotaExhausted, Provider: "claudecode", Err: errors.New("limit reached")}
	if !RetryAtOrZero(err).IsZero() {
		t.Error("a quota error with no stated reset must report a zero RetryAt, never a guess")
	}
	if !IsQuotaExhausted(err) {
		t.Error("still a quota exhaustion even without a reset time")
	}
}
