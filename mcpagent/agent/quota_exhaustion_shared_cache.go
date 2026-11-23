package mcpagent

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// quotaModelKey keeps quota state with the account that incurred it. Blank
// connections use the server account; credentials never become part of the key.
type quotaModelKey struct {
	provider     string
	modelID      string
	connectionID string
}

func modelQuotaKey(model LLMModel) quotaModelKey {
	connectionID := strings.TrimSpace(model.ConnectionID)
	if connectionID == "" {
		connectionID = "global:" + model.Provider
	}
	return quotaModelKey{provider: model.Provider, modelID: model.ModelID, connectionID: connectionID}
}

func (key quotaModelKey) String() string {
	return fmt.Sprintf("%s/%s account=%q", key.provider, key.modelID, key.connectionID)
}

// sharedQuotaExhaustedModels persists account/model exhaustion across fresh
// Agent instances. Both caches use the same key. A provider-stated reset is
// authoritative (PLAT-101); an unknown reset stays zero and is never invented.
// Unknown-reset shared marks expire after an internal cooldown so later turns
// can discover recovery. That throttle must never be returned as RetryAt.
var (
	sharedQuotaExhaustedMu     sync.RWMutex
	sharedQuotaExhaustedModels = map[quotaModelKey]sharedQuotaExhaustionEntry{}
)

type sharedQuotaExhaustionEntry struct {
	resetAt   time.Time // provider-stated reset time; zero = unknown, never guessed
	learnedAt time.Time // when this process last confirmed the model was exhausted
}

// unknownResetCooldown bounds how long an unknown-reset mark is trusted
// before the next turn is allowed a real attempt. Override with
// QUOTA_UNKNOWN_RESET_COOLDOWN_SECONDS for tests or a different deployment's
// observed recovery time.
func unknownResetCooldown() time.Duration {
	if raw := os.Getenv("QUOTA_UNKNOWN_RESET_COOLDOWN_SECONDS"); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 3 * time.Minute
}

// markModelQuotaExhaustedShared records (or refreshes) a model's exhaustion
// across the process, so every subsequent turn -- regardless of which Agent
// instance handles it -- skips the model immediately instead of relearning
// it from a slow provider round trip.
func markModelQuotaExhaustedShared(key quotaModelKey, resetAt time.Time) {
	sharedQuotaExhaustedMu.Lock()
	defer sharedQuotaExhaustedMu.Unlock()
	sharedQuotaExhaustedModels[key] = sharedQuotaExhaustionEntry{resetAt: resetAt, learnedAt: time.Now()}
}

// sharedModelQuotaExhaustion reports whether a model is currently known
// exhausted process-wide, and its stated reset time (zero = unknown reset --
// never a guess). A reopened stated window (resetAt in the past) is treated
// as not exhausted and forgotten, mirroring the per-agent reopened-window
// handling. An unknown-reset mark additionally expires after
// unknownResetCooldown so the model gets a real retry periodically instead
// of being benched for the rest of the process's life.
func sharedModelQuotaExhaustion(key quotaModelKey) (resetAt time.Time, exhausted bool) {
	sharedQuotaExhaustedMu.RLock()
	entry, exhausted := sharedQuotaExhaustedModels[key]
	sharedQuotaExhaustedMu.RUnlock()
	if !exhausted {
		return time.Time{}, false
	}
	now := time.Now()
	expired := false
	if !entry.resetAt.IsZero() {
		expired = !entry.resetAt.After(now)
	} else {
		expired = now.Sub(entry.learnedAt) >= unknownResetCooldown()
	}
	if expired {
		sharedQuotaExhaustedMu.Lock()
		delete(sharedQuotaExhaustedModels, key)
		sharedQuotaExhaustedMu.Unlock()
		return time.Time{}, false
	}
	return entry.resetAt, true
}

// forgetSharedModelQuotaExhaustion clears a model's shared exhaustion record.
// Used by tests to avoid cross-test leakage of the package-level cache.
func forgetSharedModelQuotaExhaustion(key quotaModelKey) {
	sharedQuotaExhaustedMu.Lock()
	delete(sharedQuotaExhaustedModels, key)
	sharedQuotaExhaustedMu.Unlock()
}
