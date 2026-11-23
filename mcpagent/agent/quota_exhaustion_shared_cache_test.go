package mcpagent

import (
	"os"
	"sync"
	"testing"
	"time"
)

// The whole point of the shared cache: a model marked exhausted by ONE
// *Agent instance (one turn) must be visible to a completely different
// instance (the next turn), which is what a.quotaExhaustedModels alone
// cannot do since agent_go builds a fresh Agent per request.
func TestSharedQuotaExhaustionCrossesAgentInstances(t *testing.T) {
	key := modelQuotaKey(LLMModel{Provider: "cursor-cli", ModelID: "grok-4.6"})
	t.Cleanup(func() { forgetSharedModelQuotaExhaustion(key) })

	if _, exhausted := sharedModelQuotaExhaustion(key); exhausted {
		t.Fatal("must start unmarked")
	}

	resetAt := time.Now().Add(time.Hour)
	markModelQuotaExhaustedShared(key, resetAt)

	got, exhausted := sharedModelQuotaExhaustion(key)
	if !exhausted {
		t.Fatal("a later lookup (simulating the next turn's fresh Agent) must see the mark")
	}
	if !got.Equal(resetAt) {
		t.Errorf("resetAt = %v, want %v", got, resetAt)
	}
}

// A zero reset time means "exhausted, no reliable reset stated" (PLAT-101) —
// distinct from not-exhausted, and must never be turned into a guessed time.
func TestSharedQuotaExhaustionWithoutResetTimeStaysExhausted(t *testing.T) {
	key := modelQuotaKey(LLMModel{Provider: "claude-code", ModelID: "claude-sonnet-5"})
	t.Cleanup(func() { forgetSharedModelQuotaExhaustion(key) })

	markModelQuotaExhaustedShared(key, time.Time{})
	got, exhausted := sharedModelQuotaExhaustion(key)
	if !exhausted || !got.IsZero() {
		t.Fatalf("resetAt=%v exhausted=%v, want zero/true", got, exhausted)
	}
}

// A window that has already reopened must self-clear on read, exactly like
// the per-agent map does, so a later turn retries the model instead of
// treating a stale mark as permanent.
func TestSharedQuotaExhaustionSelfClearsOnceReopened(t *testing.T) {
	key := modelQuotaKey(LLMModel{Provider: "codex-cli", ModelID: "gpt-5.6-sol"})
	t.Cleanup(func() { forgetSharedModelQuotaExhaustion(key) })

	markModelQuotaExhaustedShared(key, time.Now().Add(-time.Minute))
	if _, exhausted := sharedModelQuotaExhaustion(key); exhausted {
		t.Fatal("a reopened window must not read back as exhausted")
	}
	// The self-clear must also have removed the entry outright.
	sharedQuotaExhaustedMu.RLock()
	_, stillPresent := sharedQuotaExhaustedModels[key]
	sharedQuotaExhaustedMu.RUnlock()
	if stillPresent {
		t.Fatal("a reopened window's entry must be forgotten, not merely read as false")
	}
}

// An unknown-reset mark (Cursor's only case: it never states a reset time)
// must not bench a model for the life of the process. It gets a real retry
// once unknownResetCooldown passes, unlike a stated reset time which is
// authoritative and never guessed at.
func TestSharedQuotaExhaustionWithUnknownResetExpiresAfterCooldown(t *testing.T) {
	key := modelQuotaKey(LLMModel{Provider: "cursor-cli", ModelID: "auto"})
	t.Cleanup(func() {
		forgetSharedModelQuotaExhaustion(key)
		os.Unsetenv("QUOTA_UNKNOWN_RESET_COOLDOWN_SECONDS")
	})
	t.Setenv("QUOTA_UNKNOWN_RESET_COOLDOWN_SECONDS", "1")

	markModelQuotaExhaustedShared(key, time.Time{})
	if _, exhausted := sharedModelQuotaExhaustion(key); !exhausted {
		t.Fatal("must be exhausted immediately after marking")
	}

	time.Sleep(1200 * time.Millisecond)

	resetAt, exhausted := sharedModelQuotaExhaustion(key)
	if exhausted {
		t.Fatal("an unknown-reset mark must expire after the cooldown, allowing a real retry")
	}
	if !resetAt.IsZero() {
		t.Fatal("an expired mark must never report a fabricated reset time")
	}
}

// The cooldown must never be applied to a real, provider-stated reset time --
// that value is authoritative (PLAT-101) and only expires when it actually
// passes, not on the shorter unknown-reset schedule.
func TestSharedQuotaExhaustionStatedResetIgnoresTheCooldown(t *testing.T) {
	key := modelQuotaKey(LLMModel{Provider: "claude-code", ModelID: "claude-sonnet-5"})
	t.Cleanup(func() {
		forgetSharedModelQuotaExhaustion(key)
		os.Unsetenv("QUOTA_UNKNOWN_RESET_COOLDOWN_SECONDS")
	})
	t.Setenv("QUOTA_UNKNOWN_RESET_COOLDOWN_SECONDS", "1")

	stated := time.Now().Add(2 * time.Second)
	markModelQuotaExhaustedShared(key, stated)

	time.Sleep(1200 * time.Millisecond)
	got, exhausted := sharedModelQuotaExhaustion(key)
	if !exhausted {
		t.Fatal("a stated reset time in the future must stay exhausted past the shorter unknown-reset cooldown")
	}
	if !got.Equal(stated) {
		t.Errorf("resetAt = %v, want the unmodified stated time %v", got, stated)
	}
}

func TestForgetSharedModelQuotaExhaustion(t *testing.T) {
	key := modelQuotaKey(LLMModel{Provider: "pi-cli", ModelID: "google/gemini-3.8-flash"})
	markModelQuotaExhaustedShared(key, time.Time{})
	forgetSharedModelQuotaExhaustion(key)
	if _, exhausted := sharedModelQuotaExhaustion(key); exhausted {
		t.Fatal("forget must clear the mark")
	}
}

// Concurrent turns across sessions mark and read the same shared cache; run
// under -race to prove the mutex actually protects the map.
func TestSharedQuotaExhaustionConcurrentAccess(t *testing.T) {
	key := modelQuotaKey(LLMModel{Provider: "cursor-cli", ModelID: "auto"})
	t.Cleanup(func() { forgetSharedModelQuotaExhaustion(key) })

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			markModelQuotaExhaustedShared(key, time.Now().Add(time.Hour))
		}()
		go func() {
			defer wg.Done()
			sharedModelQuotaExhaustion(key)
		}()
	}
	wg.Wait()
}
