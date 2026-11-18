package cursorcli

import (
	"errors"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

func TestCursorUsageLimitIsQuotaExhaustion(t *testing.T) {
	pane := "  Cursor Agent\n\n  Error: Weekly usage limit reached. It resets in 6 days.\n  Buy credits to keep going now, or upgrade your plan for more weekly usage.\n"
	if !IsCursorUsageLimitText(pane) {
		t.Fatal("the weekly limit notice was not recognised")
	}
	now := time.Date(2026, 9, 29, 3, 3, 0, 0, time.UTC)
	err := NewCursorUsageLimitError("auto", pane, now)
	if !llmerrors.IsQuotaExhausted(err) {
		t.Fatalf("error kind = %v, want quota exhausted", llmerrors.KindOf(err))
	}
	var typed *llmerrors.Error
	if !errors.As(err, &typed) || !typed.RetryAt.Equal(now.Add(6*24*time.Hour)) {
		t.Fatalf("retry at = %v, want 6 days later", typed)
	}
	for _, prose := range []string{
		"The Notion API returned: usage limit reached for this integration.",
		"If you hit your weekly limit, Cursor stops.",
		"I checked the logs; the weekly usage limit reached error came from Stripe.",
	} {
		if IsCursorUsageLimitText(prose) {
			t.Errorf("prose misread as a limit wall: %q", prose)
		}
	}
	if IsCursorUsageLimitText("You've hit your usage limit") == false {
		t.Error("the \"You've hit your usage limit\" wording was not recognised")
	}
	if !CursorUsageLimitResetAt("resets in 3 hours", now).Equal(now.Add(3 * time.Hour)) {
		t.Error("hour reset not parsed")
	}
}

// agentworks#3135: on the SDK path a spent plan arrives as a typed failure, not pane text; it must reach the same
// quota handling as tmux. A plain rate limit is a short wait, and a refused model stays model_not_found.
func TestCursorSDKFailureClassifiesUsageLimits(t *testing.T) {
	for _, tc := range []struct {
		name, message, code, errName string
		status                       int
		want                         llmerrors.Kind
	}{
		{"plan code", "Request failed", "PRO_USER_USAGE_LIMIT", "", 0, llmerrors.KindQuotaExhausted},
		{"plan message", "You've hit your usage limit", "", "RateLimitError", 429, llmerrors.KindQuotaExhausted},
		{"rate limit", "Too many requests", "resource_exhausted", "RateLimitError", 429, llmerrors.KindRateLimit},
		{"refused model", "Cannot use this model: gpt-x. Available models: composer-2.5, auto.", "", "ConfigurationError", 400, llmerrors.KindModelNotFound},
		{"other", "internal boom", "", "", 0, ""},
	} {
		err := cursorSDKFailure(tc.message, tc.code, tc.errName, tc.status, "composer-2.5", time.Now())
		if (err == nil) != (tc.want == "") || (err != nil && llmerrors.KindOf(err) != tc.want) {
			t.Fatalf("%s: got %v (%v), want %s", tc.name, err, llmerrors.KindOf(err), tc.want)
		}
	}
}
