package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

func scheduleHistoryFixture(t *testing.T) string {
	t.Helper()
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	return docs
}
func writeLegacyScheduleHistory(t *testing.T, workspace string, runs []ScheduleRunEntry) string {
	t.Helper()
	file, err := fsutil.WorkspacePath(fsutil.WorkspaceDocsRoot(), scheduleRunsPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(runs)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

// PLAT-871-44: import must preserve ordering, duplicate IDs and the complete
// public record; Watcher users must never share history just because IDs match.
func TestScheduleHistoryImportPreservesRecordsAndPhysicalScope(t *testing.T) {
	docs := scheduleHistoryFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	duration := int64(912)
	ran := true
	original := []ScheduleRunEntry{
		{ID: "same", ScheduleID: "clock", Status: "success", StartedAt: now, CompletedAt: &now, DurationMs: &duration, FinalResponse: "exact answer", SessionID: "session", ScheduledFor: &now, TriggerSource: "calendar", RunFolder: "runs/one", ExecutionWorkspacePath: "Crew/example", ConcurrencyMode: "parallel", ParallelRiskAcknowledged: true, GroupNames: []string{"default"}, RanWorkflow: &ran, Caller: &workflowtypes.CrewRunCaller{WorkflowID: "caller", RunID: "parent", StepID: "step"}, Usage: &workflowtypes.CrewRunTokenUsage{PromptTokens: 42, CostUSD: 0.01}, Webhook: &WebhookRunMetadata{DeliveryID: "delivery"}},
		{ID: "same", ScheduleID: "clock", Status: "error", StartedAt: now.Add(-time.Hour), Error: "original"},
		{ID: "", ScheduleID: "clock", Status: "queued"},
	}
	workspace := "Workflow/example"
	source := writeLegacyScheduleHistory(t, workspace, original)
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ReadScheduleRuns(ctx, workspace)
	if err != nil || !reflect.DeepEqual(after, original) {
		t.Fatalf("import lost a record: %+v %v", after, err)
	}
	h, err := openScheduleHistory(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer h.db.Close()
	if backup, err := os.ReadFile(h.backup); err != nil || string(backup) != string(raw) {
		t.Fatalf("private backup changed: %v", err)
	}
	if _, err = os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("import source was not retired")
	}
	if info, err := os.Stat(filepath.Dir(h.backup)); err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("backup directory admits other users")
	}
	if info, err := os.Stat(filepath.Join(filepath.Dir(h.importDir), "..", "history.sqlite")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("history database admits other users")
	}
	// A normalized logical alias must find the same rows and update the same
	// first matching duplicate, without importing again.
	if err = UpdateScheduleRunFinalResponse(ctx, "Workflow//example", "same", "new answer"); err != nil {
		t.Fatal(err)
	}
	after, err = ReadScheduleRuns(ctx, workspace)
	if err != nil || after[0].FinalResponse != "new answer" || after[1].FinalResponse != "" {
		t.Fatalf("duplicate/alias update changed the wrong row: %+v %v", after, err)
	}
	// Re-creating the retired JSON must never roll the database back.
	writeLegacyScheduleHistory(t, workspace, original)
	found, err := FindScheduleRun(ctx, workspace, "same")
	if err != nil || found.FinalResponse != "new answer" {
		t.Fatal("normal read replayed old JSON")
	}
	for _, user := range []string{"first", "second"} {
		scope := "_users/" + user + "/watchers/profile/clock"
		if err = AppendScheduleRun(ctx, scope, &ScheduleRunEntry{ID: "same", ScheduleID: "clock", Status: "running", FinalResponse: user}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := FindScheduleRun(ctx, "_users/first/watchers/profile/clock", "same")
	if err != nil || first.FinalResponse != "first" {
		t.Fatal("Watcher scope leaked another user's row")
	}
	// The docs mount's physical identity must survive a symlink spelling.
	alias := filepath.Join(t.TempDir(), "docs")
	if err = os.Symlink(docs, alias); err == nil {
		t.Setenv("WORKSPACE_DOCS_PATH", alias)
		found, err = FindScheduleRun(ctx, workspace, "same")
		if err != nil || found.FinalResponse != "new answer" {
			t.Fatal("docs alias split the history")
		}
	}
}

// PLAT-871-44: malformed input cannot retire the only copy. A post-commit
// cleanup failure must resume the rename without importing rows twice.
func TestScheduleHistoryFailedImportAndCleanupRetry(t *testing.T) {
	docs := scheduleHistoryFixture(t)
	ctx := context.Background()
	workspace := "Workflow/example"
	source := filepath.Join(docs, workspace, "schedule-runs.json")
	if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
		t.Fatal(err)
	}
	const broken = `[{"id":"unfinished"`
	if err := os.WriteFile(source, []byte(broken), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadScheduleRuns(ctx, workspace); err == nil {
		t.Fatal("malformed history was accepted")
	}
	if raw, err := os.ReadFile(source); err != nil || string(raw) != broken {
		t.Fatal("failed import changed the source")
	}
	// Initialize only the schema in another scope, then obstruct backup publish.
	h, err := openScheduleHistory(ctx, "Workflow/schema-only")
	if err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(filepath.Dir(source))
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(filepath.Dir(filepath.Dir(h.importDir)), "imports", scheduleHistoryHash(physical), "schedule-runs.json.imported")
	if err = os.MkdirAll(backup, 0700); err != nil {
		t.Fatal(err)
	}
	original := []ScheduleRunEntry{{ID: "only", Status: "running", StartedAt: time.Now().UTC()}}
	writeLegacyScheduleHistory(t, workspace, original)
	if _, err = ReadScheduleRuns(ctx, workspace); err == nil {
		t.Fatal("backup failure was hidden")
	}
	var count int
	if err = h.db.QueryRowContext(ctx, `SELECT count(*) FROM history_entries WHERE scope=?`, physical).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rows were not committed before cleanup: %d %v", count, err)
	}
	h.db.Close()
	if _, err = os.Stat(source); err != nil {
		t.Fatal("failed cleanup removed source")
	}
	if err = os.Remove(backup); err != nil {
		t.Fatal(err)
	}
	after, err := ReadScheduleRuns(ctx, workspace)
	if err != nil || !reflect.DeepEqual(after, original) {
		t.Fatalf("cleanup retry replayed/lost history: %+v %v", after, err)
	}
}

// PLAT-871-44: keep 90 days OR the latest 200 terminal runs, plus every active
// and undated record. Retention is part of the same committed write.
func TestScheduleHistoryRetentionKeepsActiveUndatedAndRecent(t *testing.T) {
	scheduleHistoryFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	runs := []ScheduleRunEntry{}
	for i := 0; i < 205; i++ {
		runs = append(runs, ScheduleRunEntry{ID: fmt.Sprint(i), Status: "success", StartedAt: now.AddDate(0, 0, -100).Add(-time.Duration(i) * time.Hour)})
	}
	runs = append(runs, ScheduleRunEntry{ID: "recent", Status: "success", StartedAt: now.Add(-time.Hour)}, ScheduleRunEntry{ID: "undated", Status: "success"}, ScheduleRunEntry{ID: "running", Status: "running", StartedAt: now.AddDate(0, 0, -200)}, ScheduleRunEntry{ID: "waiting", Status: "waiting_for_workflow", StartedAt: now.AddDate(0, 0, -200)})
	writeLegacyScheduleHistory(t, "Workflow/example", runs)
	before, err := ReadScheduleRuns(ctx, "Workflow/example")
	if err != nil || len(before) != len(runs) {
		t.Fatal("import unexpectedly pruned records")
	}
	if err = UpdateScheduleRunFinalResponse(ctx, "Workflow/example", "recent", "saved"); err != nil {
		t.Fatal(err)
	}
	after, err := ReadScheduleRuns(ctx, "Workflow/example")
	if err != nil || len(after) != 203 {
		t.Fatalf("retention count: %d %v", len(after), err)
	}
	for _, id := range []string{"recent", "undated", "running", "waiting"} {
		if _, err = FindScheduleRun(ctx, "Workflow/example", id); err != nil {
			t.Fatalf("retention deleted %s: %v", id, err)
		}
	}
	deleted, err := DeleteScheduleRunsOlderThan(ctx, "Workflow/example", nil, 30)
	if err != nil || deleted != 199 {
		t.Fatalf("explicit cleanup: %d %v", deleted, err)
	}
}

// PLAT-871-44: redelivery has exactly one winner across database connections;
// a stale reconciler cannot overwrite a completion's response or cost receipt.
func TestScheduleHistoryConcurrentClaimAndCompletion(t *testing.T) {
	scheduleHistoryFixture(t)
	ctx := context.Background()
	workspace := "Workflow/example"
	if _, err := ReadScheduleRuns(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan bool, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, claimed, err := ClaimScheduleRun(ctx, workspace, &ScheduleRunEntry{ID: "delivery", ScheduleID: "clock", Status: "running", StartedAt: time.Now().UTC()})
			results <- claimed
			failures <- err
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	winners := 0
	for claimed := range results {
		if claimed {
			winners++
		}
	}
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("claim winners=%d", winners)
	}
	duration := int64(1234)
	if err := UpdateScheduleRunResult(ctx, workspace, "delivery", ScheduleRunCompletion{Status: "success", DurationMs: &duration, SessionID: "session", FinalResponse: "done", Usage: &workflowtypes.CrewRunTokenUsage{PromptTokens: 21, CostUSD: 0.02}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := finalizeRunningScheduleRun(ctx, workspace, "delivery", "error", "stale poll", &now); err != nil {
		t.Fatal(err)
	}
	found, err := FindScheduleRun(ctx, workspace, "delivery")
	if err != nil || found.Status != "success" || found.FinalResponse != "done" || found.DurationMs == nil || *found.DurationMs != duration || found.SessionID != "session" || found.CompletedAt == nil || found.Usage == nil || found.Usage.PromptTokens != 21 {
		t.Fatalf("completion torn or overwritten: %+v %v", found, err)
	}
	page, total, err := ListScheduleRuns(ctx, workspace, "clock", 10, 0)
	if err != nil || total != 1 || len(page) != 1 {
		t.Fatal("paged API lost run")
	}
	if _, _, err = ListScheduleRuns(ctx, workspace, "clock", 1, -1); err == nil {
		t.Fatal("negative page offset accepted")
	}
	projection, count, err := readScheduleRuntimeProjection(ctx, workspace, "clock")
	if err != nil || count != 1 || len(projection) != 1 || projection[0].Status != found.Status || projection[0].FinalResponse != "" {
		t.Fatal("status projection changed outcome or loaded response")
	}
}

// PLAT-871-44: SQL duration summaries must preserve manual-run exclusion and
// use the latest successful samples, rather than all historical durations.
func TestScheduleHistoryDurationSummaryMatchesRunSemantics(t *testing.T) {
	scheduleHistoryFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	runs := []ScheduleRunEntry{}
	for i := 0; i < 15; i++ {
		duration := int64(i+1) * 1000
		runs = append(runs, ScheduleRunEntry{ID: fmt.Sprint(i), ScheduleID: "clock", Status: "success", StartedAt: now.Add(-time.Duration(i) * time.Hour), DurationMs: &duration})
	}
	duration := int64(999999)
	runs = append(runs, ScheduleRunEntry{ID: "manual", ScheduleID: "clock", Status: "success", TriggerSource: "manual", StartedAt: now, DurationMs: &duration}, ScheduleRunEntry{ID: "failed", ScheduleID: "clock", Status: "error", StartedAt: now, DurationMs: &duration})
	if err := WriteScheduleRuns(ctx, "Workflow/example", runs); err != nil {
		t.Fatal(err)
	}
	actual, err := readScheduleDurationAverages(ctx, "Workflow/example", 10)
	if err != nil || !reflect.DeepEqual(actual, recentSuccessfulRunAverages(runs, 10)) {
		t.Fatalf("duration summary changed: %+v %v", actual, err)
	}
}
