package server

import (
	"context"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
)

// A chat's saved file comparisons run one at a time, and the same request asked twice at once shares one run. A chat
// with several reply cards asked for all of them together: eight compares over a 4 GB Crew ran at once on a 2-CPU
// host, each took 14-17s, and the chat's own page load missed the browser's 15s limit (4 Nov 2026).
var savedCompareGate = struct {
	mu    sync.Mutex
	chats map[string]chan struct{}
	group singleflight.Group
}{chats: map[string]chan struct{}{}}

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
	value, err, _ := savedCompareGate.group.Do(sessionID+"\x00"+strings.Join(key, "\x00"), func() (any, error) {
		slot := savedCompareSlot(sessionID)
		select {
		case slot <- struct{}{}:
		case <-ctx.Done():
			return filesnapshots.Comparison{}, ctx.Err()
		}
		defer func() { <-slot }()
		return run()
	})
	if err != nil {
		return filesnapshots.Comparison{}, err
	}
	comparison := value.(filesnapshots.Comparison)
	// Callers trim and relabel these in place; each gets its own copy of a shared result.
	if comparison.Changes != nil {
		comparison.Changes = append(make([]filesnapshots.Change, 0, len(comparison.Changes)), comparison.Changes...)
	}
	if comparison.Record.Skipped != nil {
		comparison.Record.Skipped = append(make([]string, 0, len(comparison.Record.Skipped)), comparison.Record.Skipped...)
	}
	return comparison, nil
}
