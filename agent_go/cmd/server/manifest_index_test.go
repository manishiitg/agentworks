package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type fakeManifestWorkspace struct {
	folders []string
	stamps  map[string]manifestStamp
	lists   atomic.Int32
	reads   atomic.Int32
	readLog []string
}

func (f *fakeManifestWorkspace) index() *workflowManifestIndex {
	return &workflowManifestIndex{
		list: func(context.Context) ([]string, map[string]manifestStamp, error) {
			f.lists.Add(1)
			stamps := map[string]manifestStamp{}
			for k, v := range f.stamps {
				stamps[k] = v
			}
			return append([]string(nil), f.folders...), stamps, nil
		},
		read: func(_ context.Context, folder string) (*WorkflowManifest, bool, error) {
			f.reads.Add(1)
			f.readLog = append(f.readLog, folder)
			return &WorkflowManifest{ID: folder}, true, nil
		},
	}
}

func stampAt(modified string) manifestStamp {
	return manifestStamp{modified: modified, size: 100, known: true, present: true}
}

func resetManifestIndexGlobals(t *testing.T) {
	t.Helper()
	manifestDirtyAll.Store(false)
	manifestDirtyFolders.Range(func(k, _ any) bool { manifestDirtyFolders.Delete(k); return true })
}

func TestManifestIndexServesWarmPollsWithoutWorkspaceIO(t *testing.T) {
	resetManifestIndexGlobals(t)
	fake := &fakeManifestWorkspace{
		folders: []string{"Workflow/a", "Workflow/b"},
		stamps:  map[string]manifestStamp{"Workflow/a": stampAt("t1"), "Workflow/b": stampAt("t1")},
	}
	idx := fake.index()
	ctx := context.Background()

	if got, _ := idx.discover(ctx, time.Minute); len(got) != 2 {
		t.Fatalf("initial discover = %d workflows, want 2", len(got))
	}
	for i := 0; i < 20; i++ {
		if _, err := idx.discover(ctx, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if fake.lists.Load() != 1 || fake.reads.Load() != 2 {
		t.Fatalf("warm polls did workspace I/O: lists=%d reads=%d, want 1/2", fake.lists.Load(), fake.reads.Load())
	}
}

func TestManifestIndexRevalidatesWithOneListingAndRereadsOnlyChanged(t *testing.T) {
	resetManifestIndexGlobals(t)
	fake := &fakeManifestWorkspace{
		folders: []string{"Workflow/a", "Workflow/b", "Workflow/c"},
		stamps:  map[string]manifestStamp{"Workflow/a": stampAt("t1"), "Workflow/b": stampAt("t1"), "Workflow/c": stampAt("t1")},
	}
	idx := fake.index()
	ctx := context.Background()
	_, _ = idx.discover(ctx, 0)
	fake.readLog = nil

	fake.stamps["Workflow/b"] = stampAt("t2")           // edited out of band
	fake.folders = []string{"Workflow/a", "Workflow/b"} // c deleted
	got, err := idx.discover(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("discover = %d workflows, want 2 after deleting c", len(got))
	}
	if len(fake.readLog) != 1 || fake.readLog[0] != "Workflow/b" {
		t.Fatalf("re-read %v, want only the changed Workflow/b", fake.readLog)
	}
}

func TestManifestIndexRereadsFolderWrittenByThisServerEvenWithSameStamp(t *testing.T) {
	resetManifestIndexGlobals(t)
	fake := &fakeManifestWorkspace{
		folders: []string{"Workflow/a", "Workflow/b"},
		stamps:  map[string]manifestStamp{"Workflow/a": stampAt("t1"), "Workflow/b": stampAt("t1")},
	}
	idx := fake.index()
	ctx := context.Background()
	_, _ = idx.discover(ctx, time.Minute)
	fake.readLog = nil

	// Same-second rewrite with identical size: the stamp cannot tell, the hook can.
	noteWorkspaceMutation("Workflow/a/workflow.json", false)
	_, _ = idx.discover(ctx, time.Minute)
	if len(fake.readLog) != 1 || fake.readLog[0] != "Workflow/a" {
		t.Fatalf("re-read %v after local write, want only Workflow/a", fake.readLog)
	}

	fake.readLog = nil
	invalidateWorkflowManifestIndex()
	_, _ = idx.discover(ctx, time.Minute)
	if len(fake.readLog) != 2 {
		t.Fatalf("explicit invalidation re-read %v, want every manifest", fake.readLog)
	}
}

func TestManifestIndexFolderDeletionDoesNotRereadOtherWorkflows(t *testing.T) {
	resetManifestIndexGlobals(t)
	fake := &fakeManifestWorkspace{
		folders: []string{"Workflow/a", "Workflow/b"},
		stamps:  map[string]manifestStamp{"Workflow/a": stampAt("t1"), "Workflow/b": stampAt("t1")},
	}
	idx := fake.index()
	_, _ = idx.discover(context.Background(), time.Minute)
	fake.readLog = nil
	fake.folders = []string{"Workflow/b"}
	noteWorkspaceMutation("Workflow/a", true)
	got, err := idx.discover(context.Background(), time.Minute)
	if err != nil || len(got) != 1 || got[0].WorkspacePath != "Workflow/b" {
		t.Fatalf("discover after delete = %+v, %v", got, err)
	}
	if len(fake.readLog) != 0 {
		t.Fatalf("deletion re-read unrelated manifests: %v", fake.readLog)
	}
	if _, dirty := manifestDirtyFolders.Load("Workflow/a"); dirty {
		t.Fatal("deleted workflow retained a dirty-cache entry")
	}
}

func TestParseWorkflowFolderListingReadsManifestStamps(t *testing.T) {
	body := []byte(`{"data":[{"filepath":"Workflow","type":"folder","children":[
		{"filepath":"Workflow/a","type":"folder","children":[
			{"filepath":"Workflow/a/workflow.json","size":321,"last_modified":"2026-09-23T10:00:00Z"},
			{"filepath":"Workflow/a/runs","type":"folder"}]},
		{"filepath":"Workflow/b","type":"folder","children":[{"filepath":"Workflow/b/notes.md","size":5,"last_modified":"x"}]},
		{"filepath":"Workflow/c","type":"folder"}]}]}`)
	folders, stamps, err := parseWorkflowFolderListing(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 3 {
		t.Fatalf("folders = %v", folders)
	}
	if s := stamps["Workflow/a"]; !s.known || !s.present || s.size != 321 || s.modified != "2026-09-23T10:00:00Z" {
		t.Fatalf("Workflow/a stamp = %+v", s)
	}
	if s := stamps["Workflow/b"]; !s.known || s.present {
		t.Fatalf("Workflow/b (no manifest) stamp = %+v", s)
	}
	if s := stamps["Workflow/c"]; s.known {
		t.Fatalf("Workflow/c (children unknown) must be read, stamp = %+v", s)
	}
}

func TestScheduleSummaryCacheInvalidatesOnGeneration(t *testing.T) {
	c := &workflowScheduleSummaryCache
	c.mu.Lock()
	c.ok, c.value, c.gen, c.at = true, WorkflowScheduleSummary{TotalSchedules: 7}, scheduleSummaryGeneration.Load(), time.Now()
	c.mu.Unlock()
	t.Cleanup(func() { c.mu.Lock(); c.ok = false; c.mu.Unlock() })

	svc := &SchedulerService{}
	got, err := svc.CachedWorkflowScheduleSummary(context.Background())
	if err != nil || got.TotalSchedules != 7 {
		t.Fatalf("warm summary = %+v, %v; want cached 7", got, err)
	}

	noteWorkspaceMutation("Workflow/a/schedule-runs.json", false)
	c.mu.Lock()
	stale := c.gen != scheduleSummaryGeneration.Load()
	c.mu.Unlock()
	if !stale {
		t.Fatal("a schedule-runs.json write must invalidate the cached summary")
	}
}
