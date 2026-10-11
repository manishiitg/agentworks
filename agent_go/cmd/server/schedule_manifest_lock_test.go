package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

// Drive the sharing handler, real workspace HTTP writer and disk lock. A brief
// concurrent save must not make a just-created workflow unshareable; cancelled
// or persistently blocked requests must leave its access record untouched.
func TestWorkflowManifestAccessWaitsForConcurrentSave(t *testing.T) {
	f := newTeamTestFixture(t)
	m, _, err := ReadWorkflowManifest(teamTestContext("leader"), teamTestRoot)
	if err != nil {
		t.Fatal(err)
	}
	m.Team = nil
	f.manifest(t, m)
	lockPath := filepath.Join(f.root, teamTestRoot, "workflow.json")
	hold := func() func() {
		unlock, e := knowledgeManifestLock(lockPath)
		if e != nil {
			t.Fatal(e)
		}
		var once sync.Once
		release := func() { once.Do(unlock) }
		t.Cleanup(release)
		return release
	}
	save := func(ctx context.Context, readers []string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"workspace_path": teamTestRoot, "owners": []string{"leader"}, "editors": []string{}, "readers": readers})
		r := httptest.NewRequest(http.MethodPut, "/api/workflow/access", strings.NewReader(string(body))).WithContext(ctx)
		w := httptest.NewRecorder()
		f.api.handleSetWorkflowAccess(w, r)
		return w
	}
	release := hold()
	timer := time.AfterFunc(60*time.Millisecond, release)
	defer timer.Stop()
	if w := save(teamTestContext("leader"), []string{"observer"}); w.Code != http.StatusOK {
		t.Fatalf("brief concurrent save refused: %d %s", w.Code, w.Body.String())
	}
	after, _, err := ReadWorkflowManifest(teamTestContext("leader"), teamTestRoot)
	if err != nil || after == nil || !containsID(after.effectiveReaders(), "observer") {
		t.Fatalf("share not persisted: %v", err)
	}
	before := f.file("workflow.json")
	release = hold()
	ctx, cancel := context.WithCancel(teamTestContext("leader"))
	defer cancel()
	timer = time.AfterFunc(60*time.Millisecond, cancel)
	defer timer.Stop()
	if w := save(ctx, []string{}); w.Code == http.StatusOK {
		t.Fatal("cancelled save was admitted")
	}
	if f.file("workflow.json") != before {
		t.Fatal("cancelled save changed access")
	}
	if w := save(teamTestContext("leader"), []string{}); w.Code == http.StatusOK || !strings.Contains(w.Body.String(), "VERSION_CONFLICT") {
		t.Fatalf("persistent lock did not return retry refusal: %d %s", w.Code, w.Body.String())
	}
	if f.file("workflow.json") != before {
		t.Fatal("timed-out save changed access")
	}
	release()
	if w := save(teamTestContext("leader"), []string{}); w.Code != http.StatusOK {
		t.Fatalf("retry after release: %d %s", w.Code, w.Body.String())
	}
}
