package server

import (
	"context"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
)

const (
	savedCompareCacheTTL        = 60 * time.Second
	savedCompareCacheMaxEntries = 128
)

type savedCompareCacheEntry struct {
	comparison filesnapshots.Comparison
	expires    time.Time
}

// A chat's saved file comparisons run one at a time, and the same request asked twice at once shares one run. A chat
// with several reply cards asked for all of them together: eight compares over a 4 GB Crew ran at once on a 2-CPU
// host, each took 14-17s, and the chat's own page load missed the browser's 15s limit (4 Nov 2026).
// Immutable saved comparisons are also cached briefly so repeated reply_card polls skip redundant git diffs (#4295).
var savedCompareGate = struct {
	mu    sync.Mutex
	chats map[string]chan struct{}
	cache map[string]savedCompareCacheEntry
	group singleflight.Group
}{
	chats: map[string]chan struct{}{},
	cache: map[string]savedCompareCacheEntry{},
}

func cloneSavedComparison(comparison filesnapshots.Comparison) filesnapshots.Comparison {
	// Callers trim and relabel these in place; each gets its own copy of a shared result.
	if comparison.Changes != nil {
		comparison.Changes = append(make([]filesnapshots.Change, 0, len(comparison.Changes)), comparison.Changes...)
	}
	if comparison.Record.Skipped != nil {
		comparison.Record.Skipped = append(make([]string, 0, len(comparison.Record.Skipped)), comparison.Record.Skipped...)
	}
	return comparison
}

func savedCompareSlot(sessionID string) chan struct{} {
	savedCompareGate.mu.Lock()
	defer savedCompareGate.mu.Unlock()
	slot := savedCompareGate.chats[sessionID]
	if slot == nil {
		slot = make(chan struct{}, 1)
		savedCompareGate.chats[sessionID] = slot
	}
	return slot
}

// compareSavedOnce runs one comparison for this chat; key must name everything the result depends on, the person
// included, since what a person may read decides what a comparison shows.
func compareSavedOnce(ctx context.Context, sessionID string, key []string, run func() (filesnapshots.Comparison, error)) (filesnapshots.Comparison, error) {
	cacheKey := sessionID + "\x00" + strings.Join(key, "\x00")
	now := time.Now()
	savedCompareGate.mu.Lock()
	if entry, ok := savedCompareGate.cache[cacheKey]; ok && now.Before(entry.expires) {
		savedCompareGate.mu.Unlock()
		return cloneSavedComparison(entry.comparison), nil
	}
	savedCompareGate.mu.Unlock()

	value, err, _ := savedCompareGate.group.Do(cacheKey, func() (any, error) {
		slot := savedCompareSlot(sessionID)
		select {
		case slot <- struct{}{}:
		case <-ctx.Done():
			return filesnapshots.Comparison{}, ctx.Err()
		}
		defer func() { <-slot }()
		comparison, runErr := run()
		if runErr == nil {
			storeNow := time.Now()
			savedCompareGate.mu.Lock()
			for k, cached := range savedCompareGate.cache {
				if storeNow.After(cached.expires) {
					delete(savedCompareGate.cache, k)
				}
			}
			if len(savedCompareGate.cache) >= savedCompareCacheMaxEntries {
				for k := range savedCompareGate.cache {
					delete(savedCompareGate.cache, k)
					break
				}
			}
			savedCompareGate.cache[cacheKey] = savedCompareCacheEntry{
				comparison: cloneSavedComparison(comparison),
				expires:    storeNow.Add(savedCompareCacheTTL),
			}
			savedCompareGate.mu.Unlock()
		}
		return comparison, runErr
	})
	if err != nil {
		return filesnapshots.Comparison{}, err
	}
	return cloneSavedComparison(value.(filesnapshots.Comparison)), nil
}
