package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Pausing a workflow sends one disable per schedule at the same moment. Each
// reads the whole manifest and writes it back, so without the per-workflow lock
// the last write wins and only some schedules stay paused.
func TestConcurrentScheduleDisablesAllPersist(t *testing.T) {
	server, workspaceStore := newSlackListableWorkspaceServer(t)
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	ctx := context.Background()
	const workspace = "Workflow/pausetest"
	const n = 8
	schedules := make([]string, 0, n)
	for i := 0; i < n; i++ {
		schedules = append(schedules, fmt.Sprintf(`{"id":"sched-%d","name":"s%d","cron_expression":"0 9 * * *","timezone":"UTC","group_names":["default"],"enabled":true}`, i, i))
	}
	workspaceStore.files[workspace+"/workflow.json"] = `{"schema_version":1,"id":"wf_pausetest","label":"Pausetest","schedules":[` + strings.Join(schedules, ",") + `]}`

	// Reading a manifest that needs migrating (older shape) writes it back, which
	// would race the disables below for a reason unrelated to this test. Real
	// manifests were normalized long ago, so load it once first.
	if _, found, err := ReadWorkflowManifest(ctx, workspace); err != nil || !found {
		t.Fatalf("normalize manifest: found=%v err=%v", found, err)
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := setScheduleEnabled(ctx, id, false, nil, nil); err != nil {
				t.Errorf("disable %s: %v", id, err)
			}
		}(fmt.Sprintf("sched-%d", i))
	}
	wg.Wait()

	got, found, err := ReadWorkflowManifest(ctx, workspace)
	if err != nil || !found {
		t.Fatalf("read manifest: found=%v err=%v", found, err)
	}
	for _, s := range got.Schedules {
		if s.Enabled {
			t.Errorf("schedule %s is still enabled: a concurrent disable was lost", s.ID)
		}
	}
	if len(got.Schedules) != n {
		t.Errorf("schedules = %d, want %d", len(got.Schedules), n)
	}
}
