package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
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
