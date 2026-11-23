package llmtypes

import (
	"testing"
	"time"
)

// TestAccountRateLimitSharingAcrossSessions is the reason the cache is keyed by
// account rather than by session.
//
// A rate limit belongs to the subscription. With ten sessions on one account, a
// session that has not completed a turn yet still needs to know what another
// session observed a minute ago — a per-session cache would report "unknown"
// and let it walk into a wall that was already visible.
func TestAccountRateLimitSharingAcrossSessions(t *testing.T) {
	ResetAccountRateLimitWindowsForTest()
	now := time.Date(2026, 8, 18, 14, 0, 0, 0, time.UTC)
	key := AccountRateLimitKey("oauth-token-A")

	RecordAccountRateLimitWindows(key, []RateLimitWindow{
		{Name: "five_hour", UsedPercent: 97, ResetsAt: now.Add(time.Hour)},
	}, now)

	windows, observedAt, ok := AccountRateLimitWindows(key, now.Add(time.Minute), time.Hour)
	if !ok || len(windows) != 1 || windows[0].UsedPercent != 97 {
		t.Fatalf("account reading not shared: windows=%+v ok=%v", windows, ok)
	}
	if !observedAt.Equal(now) {
		t.Errorf("observedAt = %v, want %v", observedAt, now)
	}
}

// TestAccountRateLimitKeysAreDistinctAndDoNotStoreTheCredential.
//
// Two credentials are two quota pools; sharing a bucket would let one account's
// exhaustion gate another account's work. An empty credential is deliberately
// not a bucket at all, for the same reason.
func TestAccountRateLimitKeysAreDistinctAndDoNotStoreTheCredential(t *testing.T) {
	ResetAccountRateLimitWindowsForTest()
	now := time.Date(2026, 8, 18, 14, 0, 0, 0, time.UTC)

	const secret = "oauth-token-A"
	keyA := AccountRateLimitKey(secret)
	keyB := AccountRateLimitKey("oauth-token-B")
	if keyA == keyB {
		t.Fatal("two credentials collapsed to one account key")
	}
	if keyA == secret || len(keyA) == 0 {
		t.Fatalf("account key must be a hash, not the credential itself: %q", keyA)
	}
	if AccountRateLimitKey("   ") != "" {
		t.Error("a blank credential produced a shared default bucket")
	}

	RecordAccountRateLimitWindows(keyA, []RateLimitWindow{{Name: "five_hour", UsedPercent: 99}}, now)
	if _, _, ok := AccountRateLimitWindows(keyB, now, time.Hour); ok {
		t.Error("account B read account A's usage")
	}
	if _, _, ok := AccountRateLimitWindows("", now, time.Hour); ok {
		t.Error("an unidentified caller read a cached account reading")
	}
}

// TestAccountRateLimitStaleReadingIsReportedAbsent.
//
// A stale number is not a milder version of the truth — it looks exactly as
// confident as a fresh one, and a caller gating work on it would block or
// proceed for reasons that stopped being true hours ago. Unknown must read as
// unknown so the caller proceeds.
func TestAccountRateLimitStaleReadingIsReportedAbsent(t *testing.T) {
	ResetAccountRateLimitWindowsForTest()
	now := time.Date(2026, 8, 18, 14, 0, 0, 0, time.UTC)
	key := AccountRateLimitKey("oauth-token-A")
	RecordAccountRateLimitWindows(key, []RateLimitWindow{{Name: "five_hour", UsedPercent: 99}}, now)

	if _, _, ok := AccountRateLimitWindows(key, now.Add(3*time.Hour), 30*time.Minute); ok {
		t.Error("a three-hour-old reading was served as current")
	}
	if _, _, ok := AccountRateLimitWindows(key, now.Add(10*time.Minute), 30*time.Minute); !ok {
		t.Error("a ten-minute-old reading was discarded inside the freshness window")
	}
}

// TestAccountRateLimitLateStaleObservationDoesNotOverwrite.
//
// Sessions on one account report concurrently and out of order. Letting a
// late-arriving older reading win would move the account's known usage
// backwards, which is exactly the direction that causes a gate to let work
// through into a wall.
func TestAccountRateLimitLateStaleObservationDoesNotOverwrite(t *testing.T) {
	ResetAccountRateLimitWindowsForTest()
	now := time.Date(2026, 8, 18, 14, 0, 0, 0, time.UTC)
	key := AccountRateLimitKey("oauth-token-A")

	RecordAccountRateLimitWindows(key, []RateLimitWindow{{Name: "five_hour", UsedPercent: 97}}, now)
	RecordAccountRateLimitWindows(key, []RateLimitWindow{{Name: "five_hour", UsedPercent: 12}}, now.Add(-time.Hour))

	windows, _, ok := AccountRateLimitWindows(key, now, time.Hour)
	if !ok || windows[0].UsedPercent != 97 {
		t.Errorf("stale observation overwrote a newer one: %+v", windows)
	}
}
