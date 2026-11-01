package browserrelay

import (
	"crypto/sha256"
	"sync"
	"testing"
	"time"
)

// PLAT-746-282: grants carry one account and must not redeem twice or after expiry.
func TestAccountPairingHandoffOneUseAndExpiry(t *testing.T) {
	m := New()
	defer m.Close()
	handoff := m.BeginAccountPairing("owner", "owner@example.com")
	if len(handoff) != 43 {
		t.Fatal("handoff must contain 256 random bits")
	}
	for range 2 {
		grant, err := m.AccountPairing(handoff, false)
		if err != nil || grant.User != "owner" || grant.Account != "owner@example.com" {
			t.Fatal("preview lost issuing account")
		}
	}
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := m.AccountPairing(handoff, true); results <- err == nil }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for ok := range results {
		if ok {
			successes++
		}
	}
	if successes != 1 {
		t.Fatal("handoff redeemed more than once")
	}
	if _, err := m.AccountPairing(handoff, false); err == nil {
		t.Fatal("consumed grant could still preview")
	}
	expired := m.BeginAccountPairing("another-owner", "other@example.com")
	hash := sha256.Sum256([]byte(expired))
	m.mu.Lock()
	grant := m.accountPairings[hash]
	grant.Expires = time.Now().Add(-time.Second)
	m.accountPairings[hash] = grant
	m.mu.Unlock()
	if _, err := m.AccountPairing(expired, true); err == nil {
		t.Fatal("expired grant accepted")
	}
	if _, err := m.AccountPairing(handoff+"invalid", true); err == nil {
		t.Fatal("unknown grant accepted")
	}
}
