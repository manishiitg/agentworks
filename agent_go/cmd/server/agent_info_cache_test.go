package server

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// PLAT-856-572: simultaneous directory/panel/registration readers must not
// fan out into a registry and manifest read for every agent or connection.
func TestAgentInfoMetadataCoalescesPerPersonAndExpires(t *testing.T) {
	key := agentInfoCacheKey{api: &StreamingAPI{}, owner: t.Name(), kind: "test"}
	var reads atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	load := func() (string, error) {
		if reads.Add(1) == 1 {
			close(started)
		}
		<-release
		return "metadata", nil
	}
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := agentInfoCached(context.Background(), key, load)
			if err != nil || value != "metadata" {
				t.Errorf("value=%q err=%v", value, err)
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	if reads.Load() != 1 {
		t.Fatalf("100 concurrent readers caused %d loads", reads.Load())
	}
	for range 100 {
		_, _ = agentInfoCached(context.Background(), key, load)
	}
	if reads.Load() != 1 {
		t.Fatalf("cached batch repeated %d loads", reads.Load())
	}
	other := key
	other.owner += "-other"
	_, _ = agentInfoCached(context.Background(), other, load)
	if reads.Load() != 2 {
		t.Fatal("another person's metadata used the first person's cache")
	}
	agentInfoReadCache.Lock()
	agentInfoReadCache.entries[key].expires = time.Now().Add(-time.Second)
	agentInfoReadCache.Unlock()
	_, _ = agentInfoCached(context.Background(), key, load)
	if reads.Load() != 3 {
		t.Fatal("expired metadata did not refresh")
	}
	// Permission is intentionally not cached here: the canonical Team-reach
	// regression separately checks immediate revocation with this cache populated.
	t.Log("200 same-person readers => 1 metadata load; owner isolation and expiry refresh verified")
}
