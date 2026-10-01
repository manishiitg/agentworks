package server

import (
	"context"
	"fmt"
	"sync"
)

// Pausing or resuming a schedule reads the workflow's whole manifest, changes
// one schedule and writes the whole manifest back. The Schedules page sends one
// such request per schedule at once when a workflow (or several) is paused, so
// without a lock each request started from the same old manifest and the last
// write won: only some schedules stayed paused (linkedin 1/4, substack 3/6,
// upwork 3/5 on 2026-09-30). One lock per workflow makes each request read the
// manifest the previous one wrote.
var scheduleManifestLocks sync.Map // workspace path -> *sync.Mutex

func lockScheduleManifest(workspacePath string) func() {
	lock, _ := scheduleManifestLocks.LoadOrStore(workspacePath, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// setScheduleEnabled sets one schedule's enabled flag on a manifest read under
// the workflow's lock and returns the lookup result with the new value. check
// runs before the change (e.g. an owner check) and may refuse it.
func setScheduleEnabled(ctx context.Context, scheduleID string, enabled bool, check func(workspacePath string) bool, beforeWrite func(workspacePath string)) (*ScheduleSearchResult, error) {
	found, err := findScheduleByIDAny(ctx, scheduleID)
	if err != nil {
		return nil, err
	}
	if check != nil && !check(found.WorkspacePath) {
		return nil, errScheduleChangeRefused
	}
	unlock := lockScheduleManifest(found.WorkspacePath)
	defer unlock()
	// Read again under the lock: another request may have written since, so a
	// cached manifest index must not be used.
	invalidateWorkflowManifestIndex()
	result, err := findScheduleByIDAny(ctx, scheduleID)
	if err != nil {
		return nil, err
	}
	if beforeWrite != nil {
		beforeWrite(result.WorkspacePath)
	}
	result.Manifest.Schedules[result.Index].Enabled = enabled
	if err := WriteWorkflowManifest(ctx, result.WorkspacePath, result.Manifest); err != nil {
		return nil, fmt.Errorf("failed to write manifest: %w", err)
	}
	// The next request's re-read under this lock must see this write, not a
	// cached manifest.
	invalidateWorkflowManifestIndex()
	return result, nil
}

var errScheduleChangeRefused = fmt.Errorf("schedule change refused")
