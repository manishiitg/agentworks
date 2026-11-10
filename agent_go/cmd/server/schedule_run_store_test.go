package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	wh "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	"github.com/spf13/viper"
)

func scheduleHistoryFixture(t *testing.T) string {
	t.Helper()
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("MULTI_USER_MODE", "")
	state := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	scheduleHistoryTestCleanup(t, state)
	return docs
}
func scheduleHistoryTestCleanup(t *testing.T, state string) {
	t.Helper()
	physical, err := sqliteopen.CanonicalPath(state)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		scheduleHistoryConnections.Lock()
		defer scheduleHistoryConnections.Unlock()
		for file, db := range scheduleHistoryConnections.byFile {
			if strings.HasPrefix(file, physical+string(filepath.Separator)) {
				db.Close()
				delete(scheduleHistoryConnections.byFile, file)
			}
		}
	})
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

// PLAT-871-44: unreadable history is kept privately but cannot block new
// runs. Post-commit retirement can retry without replaying rows or failing APIs.
func TestScheduleHistoryUnreadableImportAndCleanupRetry(t *testing.T) {
	docs := scheduleHistoryFixture(t)
	ctx := context.Background()
	workspace := "Workflow/unreadable"
	source := filepath.Join(docs, workspace, "schedule-runs.json")
	if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
		t.Fatal(err)
	}
	const broken = `[{"id":"unfinished"`
	if err := os.WriteFile(source, []byte(broken), 0600); err != nil {
		t.Fatal(err)
	}
	runs, err := ReadScheduleRuns(ctx, workspace)
	if err != nil || len(runs) != 0 {
		t.Fatalf("bad history blocked opening: %+v %v", runs, err)
	}
	h, err := openScheduleHistory(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(h.importDir, "schedule-runs.json.unreadable")); err != nil || string(raw) != broken {
		t.Fatal("unreadable source bytes were lost")
	}
	if err = AppendScheduleRun(ctx, workspace, &ScheduleRunEntry{ID: "fresh", Status: "running"}); err != nil {
		t.Fatal("unreadable import blocked future run")
	}
	// Obstruct publishing another scope's backup after its rows commit.
	workspace = "Workflow/retry"
	original := []ScheduleRunEntry{{ID: "only", Status: "running", StartedAt: time.Now().UTC()}}
	source = writeLegacyScheduleHistory(t, workspace, original)
	physical, err := sqliteopen.CanonicalPath(filepath.Dir(source))
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(filepath.Dir(filepath.Dir(h.importDir)), "imports", scheduleHistoryHash(physical), "schedule-runs.json.imported")
	if err = os.MkdirAll(backup, 0700); err != nil {
		t.Fatal(err)
	}
	runs, err = ReadScheduleRuns(ctx, workspace)
	if err != nil || !reflect.DeepEqual(runs, original) {
		t.Fatalf("retirement failure blocked SQL history: %+v %v", runs, err)
	}
	if _, err = os.Stat(source); err != nil {
		t.Fatal("failed retirement removed source")
	}
	if err = os.Remove(backup); err != nil {
		t.Fatal(err)
	}
	runs, err = ReadScheduleRuns(ctx, workspace)
	if err != nil || !reflect.DeepEqual(runs, original) {
		t.Fatalf("cleanup retry replayed rows: %+v %v", runs, err)
	}
	if _, err = os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("retry did not retire source")
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

// PR2013 P2: deletion followed by same-name creation must not resurrect another
// project's responses, nested histories or backups. Exercise both workspace
// HTTP deletion routes, while a similarly named sibling and Watcher survive.
func TestScheduleHistoryFolderDeletionPreventsResurrection(t *testing.T) {
	for _, route := range []string{"folder", "document"} {
		t.Run(route, func(t *testing.T) {
			docs := scheduleHistoryFixture(t)
			ctx := context.Background()
			oldDocs := viper.Get("docs-dir")
			viper.Set("docs-dir", docs)
			t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
			for _, scope := range []string{"Workflow/example", "Workflow/example/nested", "Workflow/example-other", "_users/owner/watchers/profile/clock"} {
				writeLegacyScheduleHistory(t, scope, []ScheduleRunEntry{{ID: "old", ScheduleID: "clock", Status: "success", FinalResponse: scope}})
				if _, err := ReadScheduleRuns(ctx, scope); err != nil {
					t.Fatal(err)
				}
			}
			deleted, err := openScheduleHistory(ctx, "Workflow/example")
			if err != nil {
				t.Fatal(err)
			}
			child, err := openScheduleHistory(ctx, "Workflow/example/nested")
			if err != nil {
				t.Fatal(err)
			}
			router := gin.New()
			router.DELETE("/api/folders/*folderpath", wh.DeleteFolder)
			router.DELETE("/api/documents/*filepath", wh.DeleteDocument)
			endpoint := "/api/folders/Workflow/example?confirm=true"
			if route == "document" {
				endpoint = "/api/documents/Workflow/example?confirm=true"
			}
			refused := httptest.NewRecorder()
			router.ServeHTTP(refused, httptest.NewRequest(http.MethodDelete, strings.Replace(endpoint, "confirm=true", "confirm=false", 1), nil))
			if refused.Code != http.StatusBadRequest {
				t.Fatalf("unconfirmed deletion was not refused: %d %s", refused.Code, refused.Body.String())
			}
			if found, err := FindScheduleRun(ctx, "Workflow/example", "old"); err != nil || found.FinalResponse != "Workflow/example" {
				t.Fatal("refused deletion removed history", err)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, endpoint, nil))
			if response.Code != 200 {
				t.Fatalf("delete route failed: %d %s", response.Code, response.Body.String())
			}
			for _, dir := range []string{deleted.importDir, child.importDir} {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatal("deleted project's private backup remains")
				}
			}
			for _, scope := range []string{"Workflow/example", "Workflow/example/nested"} {
				if err = os.MkdirAll(filepath.Join(docs, scope), 0700); err != nil {
					t.Fatal(err)
				}
				page, total, err := ListScheduleRuns(ctx, scope, "clock", 10, 0)
				if err != nil || total != 0 || len(page) != 0 {
					t.Fatalf("old response resurrected in %s: %+v %d %v", scope, page, total, err)
				}
			}
			for _, scope := range []string{"Workflow/example-other", "_users/owner/watchers/profile/clock"} {
				found, err := FindScheduleRun(ctx, scope, "old")
				if err != nil || found.FinalResponse != scope {
					t.Fatalf("unrelated history removed: %s %v", scope, err)
				}
			}
		})
	}
}

// PLAT-871-44: private history cannot live in docs, including a state path
// reached through a symlink whose final child does not exist yet.
func TestScheduleHistoryRejectsPrivateStateInsideDocs(t *testing.T) {
	docs := scheduleHistoryFixture(t)
	outside := t.TempDir()
	paths := []string{filepath.Join(docs, "state")}
	link := filepath.Join(outside, "linked")
	if err := os.Symlink(docs, link); err == nil {
		paths = append(paths, filepath.Join(link, "new-state"))
	}
	for _, state := range paths {
		t.Setenv("AGENTWORKS_STATE_ROOT", state)
		if _, err := ReadScheduleRuns(context.Background(), "Workflow/example"); err == nil || !strings.Contains(err.Error(), "outside the workspace documents") {
			t.Fatalf("private history admitted workspace state: %v", err)
		}
	}
}

// Reviewer follow-up: a source changed after the committed import must not
// block SQL history, replace it, or keep reading a mismatched backup on polls.
func TestScheduleHistoryBackupConflictKeepsCommittedOutcome(t *testing.T) {
	scheduleHistoryFixture(t)
	ctx := context.Background()
	workspace := "Workflow/example"
	source := writeLegacyScheduleHistory(t, workspace, []ScheduleRunEntry{{ID: "run", Status: "running"}})
	h, err := openScheduleHistory(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(h.backup)
	if err != nil {
		t.Fatal(err)
	}
	if err = UpdateScheduleRunResult(ctx, workspace, "run", ScheduleRunCompletion{Status: "success", FinalResponse: "SQL outcome"}); err != nil {
		t.Fatal(err)
	}
	const changed = `[{"id":"run","status":"error","final_response":"stale file"}]`
	if err = os.WriteFile(source, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = h.db.ExecContext(ctx, `UPDATE history_imports SET cleanup_pending=1 WHERE scope=?`, h.scope); err != nil {
		t.Fatal(err)
	}
	run, err := FindScheduleRun(ctx, workspace, "run")
	if err != nil || run.Status != "success" || run.FinalResponse != "SQL outcome" {
		t.Fatal("backup conflict blocked or replaced SQL outcome", err)
	}
	if raw, err := os.ReadFile(source); err != nil || string(raw) != changed {
		t.Fatal("conflicting source changed")
	}
	if raw, err := os.ReadFile(h.backup); err != nil || string(raw) != string(original) {
		t.Fatal("committed backup changed")
	}
	var pending int
	if err = h.db.QueryRowContext(ctx, `SELECT cleanup_pending FROM history_imports WHERE scope=?`, h.scope).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("conflict would retry file reads on every poll", err)
	}
}

// PR2018 P2 / PLAT-871-45: deleting an allowed directory link removes only
// that link. The surviving target must retain its private responses/backups.
func TestScheduleHistoryDirectoryLinkDeletionPreservesTarget(t *testing.T) {
	for _, route := range []string{"folder", "document"} {
		t.Run(route, func(t *testing.T) {
			docs := scheduleHistoryFixture(t)
			ctx := context.Background()
			oldDocs := viper.Get("docs-dir")
			viper.Set("docs-dir", docs)
			t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
			target := "Workflow/target"
			writeLegacyScheduleHistory(t, target, []ScheduleRunEntry{{ID: "saved", Status: "success", FinalResponse: "keep target answer"}})
			h, err := openScheduleHistory(ctx, target)
			if err != nil {
				t.Fatal(err)
			}
			// Immediate Workflow/<name> links are refused by the existing Relay
			// deletion policy. A nested link is an allowed directory-link removal.
			logicalLink := target + "/link"
			link := filepath.Join(docs, logicalLink)
			if err = os.Symlink(filepath.Join(docs, target), link); err != nil {
				t.Skipf("directory symlinks unavailable: %v", err)
			}
			captured, err := scheduleHistoryRemovalScope(logicalLink)
			if err != nil || captured != "" {
				t.Fatalf("app deletion would purge a link target: %q %v", captured, err)
			}
			router := gin.New()
			router.DELETE("/api/folders/*folderpath", wh.DeleteFolder)
			router.DELETE("/api/documents/*filepath", wh.DeleteDocument)
			endpoint := "/api/folders/" + logicalLink + "?confirm=true"
			if route == "document" {
				endpoint = "/api/documents/" + logicalLink + "?confirm=true"
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, endpoint, nil))
			if response.Code != 200 {
				t.Fatalf("link deletion: %d %s", response.Code, response.Body.String())
			}
			if _, err = os.Lstat(link); !os.IsNotExist(err) {
				t.Fatal("link remains after successful removal")
			}
			if err = purgeRemovedScheduleHistory(ctx, captured); err != nil {
				t.Fatal(err)
			}
			run, err := FindScheduleRun(ctx, target, "saved")
			if err != nil || run.FinalResponse != "keep target answer" {
				t.Fatal("link deletion erased target history", err)
			}
			if _, err = os.Stat(h.backup); err != nil {
				t.Fatal("link deletion erased target backup")
			}
		})
	}
}

// PR2018 P2 / PLAT-871-45: reproduce deletion overlapping first import. Hold
// the empty history's deletion writer transaction, wait until the importer is
// blocked acquiring its own writer, then remove the old folder and commit.
// Before the fix the importer had already read the old JSON and reinserted it;
// now its first source read occurs after the writer boundary and finds no file.
func TestScheduleHistoryImportCannotResurrectAfterDeletionWriter(t *testing.T) {
	docs := scheduleHistoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	workspace := "Workflow/deleted"
	writeLegacyScheduleHistory(t, workspace, []ScheduleRunEntry{{ID: "old", Status: "success", FinalResponse: "deleted answer"}})
	h, err := openScheduleHistory(ctx, "Workflow/schema-only")
	if err != nil {
		t.Fatal(err)
	}
	deletion, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer deletion.Rollback()
	type result struct {
		runs []ScheduleRunEntry
		err  error
	}
	done := make(chan result, 1)
	go func() { runs, e := ReadScheduleRuns(ctx, workspace); done <- result{runs, e} }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	waiting := false
	stable := 0
	for !waiting {
		select {
		case early := <-done:
			t.Fatalf("import did not wait for deletion writer: %+v %v", early.runs, early.err)
		case <-deadline.C:
			t.Fatal("import never reached the writer boundary")
		case <-tick.C:
			if h.db.Stats().InUse >= 2 {
				stable++
			} else {
				stable = 0
			}
			waiting = stable >= 5
		}
	}
	if err = os.RemoveAll(filepath.Join(docs, workspace)); err != nil {
		t.Fatal(err)
	}
	if err = deletion.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case after := <-done:
		if after.err != nil || len(after.runs) != 0 {
			t.Fatalf("deleted responses resurrected: %+v %v", after.runs, after.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = os.MkdirAll(filepath.Join(docs, workspace), 0700); err != nil {
		t.Fatal(err)
	}
	if after, err := ReadScheduleRuns(ctx, workspace); err != nil || len(after) != 0 {
		t.Fatal("recreated project inherited history", err)
	}
}
