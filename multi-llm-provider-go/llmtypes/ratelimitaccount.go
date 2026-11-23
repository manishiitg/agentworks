package llmtypes

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// The provider states its remaining quota on every turn, and until now that
// number was rendered into a UI pill and then discarded. Nothing on the server
// retained it, so anything wanting to ask "how much is left?" before starting
// work had nothing to ask (PLAT-101).
//
// The cache is keyed by ACCOUNT, not by session, because a rate limit belongs
// to the subscription and every session authenticated with the same credential
// draws from one pool. A per-session cache would leave a freshly started
// session with no history at all — walking into a wall another session had
// already observed a minute earlier. Sharing inverts the usual concurrency
// problem: the more sessions run on an account, the fresher its reading.
var (
	accountRateLimitMu      sync.RWMutex
	accountRateLimitWindows = map[string]accountRateLimitEntry{}
)

type accountRateLimitEntry struct {
	windows    []RateLimitWindow
	observedAt time.Time
}

// AccountRateLimitKey derives a stable cache key from a provider credential.
//
// The credential is hashed and never stored. It identifies an account exactly —
// same credential, same quota pool — but it is also a secret, and a cache is
// precisely the kind of structure that ends up in a memory dump or a debug log.
// An empty credential yields an empty key, which callers treat as "no account
// identity" rather than as a shared default bucket: lumping every unidentified
// session together would let one account's exhaustion block another's work.
func AccountRateLimitKey(credential string) string {
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(credential))
	return hex.EncodeToString(sum[:8])
}

// RecordAccountRateLimitWindows stores the newest observation for an account.
//
// Older observations never overwrite newer ones. Sessions on one account report
// concurrently and out of order, and a late-arriving stale reading would
// otherwise move the account's known usage backwards.
func RecordAccountRateLimitWindows(accountKey string, windows []RateLimitWindow, observedAt time.Time) {
	if accountKey == "" || len(windows) == 0 || observedAt.IsZero() {
		return
	}
	accountRateLimitMu.Lock()
	defer accountRateLimitMu.Unlock()
	if existing, ok := accountRateLimitWindows[accountKey]; ok && existing.observedAt.After(observedAt) {
		return
	}
	stored := make([]RateLimitWindow, len(windows))
	copy(stored, windows)
	accountRateLimitWindows[accountKey] = accountRateLimitEntry{windows: stored, observedAt: observedAt.UTC()}
}

// AccountRateLimitWindows returns an account's last observed windows, and when
// they were observed.
//
// A reading older than maxAge is reported as absent rather than returned with a
// caveat. Quota moves continuously and a stale number is not a worse answer
// than none — it is a different answer that looks equally confident, and a
// caller that gates work on it would block or proceed for reasons that stopped
// being true hours ago. Absent means "unknown", and unknown must mean proceed.
func AccountRateLimitWindows(accountKey string, now time.Time, maxAge time.Duration) ([]RateLimitWindow, time.Time, bool) {
	if accountKey == "" {
		return nil, time.Time{}, false
	}
	accountRateLimitMu.RLock()
	entry, ok := accountRateLimitWindows[accountKey]
	accountRateLimitMu.RUnlock()
	if !ok || len(entry.windows) == 0 {
		return nil, time.Time{}, false
	}
	if maxAge > 0 && now.Sub(entry.observedAt) > maxAge {
		return nil, entry.observedAt, false
	}
	out := make([]RateLimitWindow, len(entry.windows))
	copy(out, entry.windows)
	return out, entry.observedAt, true
}

// ResetAccountRateLimitWindowsForTest clears the cache between tests.
func ResetAccountRateLimitWindowsForTest() {
	accountRateLimitMu.Lock()
	defer accountRateLimitMu.Unlock()
	accountRateLimitWindows = map[string]accountRateLimitEntry{}
}
