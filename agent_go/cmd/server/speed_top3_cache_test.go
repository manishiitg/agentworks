package server

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// #4295: repeated saved snapshot comparisons for the same immutable turn ids
// reuse the short-TTL cached result and return independent copies.
func TestCompareSavedOnceCachesImmutableResult(t *testing.T) {
	var runs atomic.Int32
	key := []string{"alice", "Crew/test", "", "false", "", "", "", "snap-1"}
	run := func() (filesnapshots.Comparison, error) {
		runs.Add(1)
		return filesnapshots.Comparison{
			Record:  filesnapshots.Record{ID: "snap-1", Skipped: []string{"big.bin"}},
			Changes: []filesnapshots.Change{{Path: "a.go", Status: "M"}},
		}, nil
	}
	first, err := compareSavedOnce(context.Background(), "sess-cache-test", key, run)
	if err != nil {
		t.Fatal(err)
	}
	// Mutating the caller's slice must not corrupt the cached entry.
	first.Changes[0].Path = "mutated.go"
	first.Record.Skipped[0] = "mutated.bin"

	second, err := compareSavedOnce(context.Background(), "sess-cache-test", key, run)
	if err != nil {
		t.Fatal(err)
	}
	if runs.Load() != 1 {
		t.Fatalf("runs = %d, want 1 (cached)", runs.Load())
	}
	if second.Changes[0].Path != "a.go" || second.Record.Skipped[0] != "big.bin" {
		t.Fatalf("cached entry mutated: %+v", second)
	}
}

// #4295: GET /tool-changes serves stale cached remote Vault tools immediately
// instead of blocking on a slow remote server.
func TestCachedServerVaultToolsServesStaleImmediately(t *testing.T) {
	lender := "stale-vault-user"
	conn := desktopConnection{ID: "conn-stale-test", Server: "http://127.0.0.1:1"}
	storeServerVaultToolsCache(lender, conn.ID, []vaultRuntimeServer{{ID: "s1", Name: "vault_s1", Label: "Slack"}})

	key := serverVaultToolsCacheKey(lender, conn.ID)
	serverVaultToolsCache.Lock()
	entry := serverVaultToolsCache.entries[key]
	entry.expires = time.Now().Add(-time.Second)
	serverVaultToolsCache.entries[key] = entry
	serverVaultToolsCache.Unlock()

	start := time.Now()
	rows, err := cachedServerVaultTools(context.Background(), lender, conn)
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("stale hit blocked for %v", elapsed)
	}
	if err != nil || len(rows) != 1 || rows[0].Name != "vault_s1" {
		t.Fatalf("rows = %+v, err = %v", rows, err)
	}
}

// #4411: cachedReplyGroupChanges must cache when allow != nil, isolate callers
// by user ID, bypass stale cache if a path grant is revoked, and coalesce
// concurrent misses via singleflight.
func TestCachedReplyGroupChangesCachesWithAllowIsolatesUsersAndCoalesces(t *testing.T) {
	origFn := replyGroupChangesFn
	t.Cleanup(func() {
		replyGroupChangesFn = origFn
		replyGroupCache.Lock()
		replyGroupCache.entries = make(map[string]replyGroupCacheEntry)
		replyGroupCache.Unlock()
	})
	replyGroupCache.Lock()
	replyGroupCache.entries = make(map[string]replyGroupCacheEntry)
	replyGroupCache.Unlock()

	var runs atomic.Int32
	gate := make(chan struct{})
	var gateOnce sync.Once
	entered := make(chan struct{}, 2)

	replyGroupChangesFn = func(_ context.Context, _ string, changes []filesnapshots.Change, _ []string, allow func(string) bool) []filesnapshots.Group {
		runs.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
		kept := make([]filesnapshots.Change, 0, len(changes))
		for _, c := range changes {
			if allow == nil || allow(c.Path) {
				kept = append(kept, c)
			}
		}
		return []filesnapshots.Group{{
			Kind:  "repo",
			Root:  "repo-a",
			Name:  "repo-a",
			Files: kept,
		}}
	}

	roots := crewroot.Roots{Files: "/tmp/reply-group-cache-test"}
	comp := filesnapshots.Comparison{
		Record: filesnapshots.Record{ID: "snap-reply-group-1"},
		Changes: []filesnapshots.Change{
			{Path: "repo-a/a.go", Status: "M"},
			{Path: "repo-a/secret.go", Status: "M"},
		},
	}
	aliceCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "alice"})
	bobCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "bob"})
	allowAll := func(string) bool { return true }

	// 1. Concurrent misses for the same caller coalesce into a single build.
	var wg sync.WaitGroup
	results := make([][]filesnapshots.Group, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = cachedReplyGroupChanges(aliceCtx, roots, comp, allowAll)
		}(i)
	}
	<-entered
	time.Sleep(15 * time.Millisecond)
	gateOnce.Do(func() { close(gate) })
	wg.Wait()
	if got := runs.Load(); got != 1 {
		t.Fatalf("concurrent runs = %d, want 1", got)
	}

	// Mutating a returned copy must not corrupt the cached entry.
	results[0][0].Files[0].Path = "mutated.go"

	// 2. Subsequent call for alice with allow != nil hits cache.
	cached := cachedReplyGroupChanges(aliceCtx, roots, comp, allowAll)
	if got := runs.Load(); got != 1 {
		t.Fatalf("runs after cache hit = %d, want 1", got)
	}
	if cached[0].Files[0].Path != "repo-a/a.go" {
		t.Fatalf("cached entry mutated: %+v", cached)
	}

	// 3. Different user gets an isolated cache entry.
	_ = cachedReplyGroupChanges(bobCtx, roots, comp, allowAll)
	if got := runs.Load(); got != 2 {
		t.Fatalf("runs after second user = %d, want 2", got)
	}

	// 4. Revoking a path in allow bypasses the cached entry and recomputes fresh.
	allowNoSecret := func(p string) bool { return p != "repo-a/secret.go" }
	filtered := cachedReplyGroupChanges(aliceCtx, roots, comp, allowNoSecret)
	if got := runs.Load(); got != 3 {
		t.Fatalf("runs after path revocation = %d, want 3", got)
	}
	if len(filtered[0].Files) != 1 || filtered[0].Files[0].Path != "repo-a/a.go" {
		t.Fatalf("revoked path leaked: %+v", filtered[0].Files)
	}
}
