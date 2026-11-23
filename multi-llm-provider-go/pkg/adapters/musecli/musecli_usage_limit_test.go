package musecli

import (
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

const museLimitFixture = `Usage limit reached · /upgrade
(https://accountscenter.meta.com/muse_code/?ep=xgrade) for increased limits, or
wait for usage to reset at Sep 14 at 5:30 AM`

func TestMuseUsageLimitErrorPreservesResetAndStopsRetry(t *testing.T) {
	location := time.FixedZone("IST", 5*60*60+30*60)
	now := time.Date(2026, time.September, 11, 16, 0, 0, 0, location)
	err := museUsageLimitError("muse-spark-1.3-contributor", now, museLimitFixture)
	if err == nil {
		t.Fatal("expected Muse usage wall to be an error")
	}
	if got := llmerrors.KindOf(err); got != llmerrors.KindQuotaExhausted {
		t.Fatalf("kind = %q, want quota_exhausted", got)
	}
	if llmerrors.IsRetryable(err) {
		t.Fatal("quota wall must not enter the transient retry loop")
	}
	want := time.Date(2026, time.September, 14, 5, 30, 0, 0, location)
	if got := llmerrors.RetryAtOrZero(err); !got.Equal(want) {
		t.Fatalf("RetryAt = %v, want %v", got, want)
	}
}

func TestMuseUsageLimitErrorIgnoresOrdinaryReplies(t *testing.T) {
	for _, reply := range []string{
		"I can explain usage limits if helpful.",
		"Check https://accountscenter.meta.com/muse_code/?ep=xgrade for your account.",
		"The old error said usage limit reached; that has been fixed.",
	} {
		if err := museUsageLimitError("muse-spark", time.Now(), reply); err != nil {
			t.Fatalf("ordinary reply misclassified: %v", err)
		}
	}
}

func TestMuseUsageResetDoesNotInventYearLongWait(t *testing.T) {
	now := time.Date(2026, time.September, 15, 0, 0, 0, 0, time.UTC)
	if got := museUsageResetAt(museLimitFixture, now); !got.IsZero() {
		t.Fatalf("stale reset should be unknown, got %v", got)
	}
	now = time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC)
	want := time.Date(2027, time.January, 1, 5, 30, 0, 0, time.UTC)
	if got := museUsageResetAt("wait for usage to reset at Jan 1 at 5:30 AM", now); !got.Equal(want) {
		t.Fatalf("year boundary reset = %v, want %v", got, want)
	}
}
