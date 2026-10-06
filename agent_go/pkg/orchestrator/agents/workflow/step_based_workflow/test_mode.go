package step_based_workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/testmode"

	_ "modernc.org/sqlite" // VACUUM INTO for the test run's DB copy
)

// Test mode (PLAT-562, docs/design/step_test_mode.md): one step runs with
// reads real and every external effect stubbed or redirected into the test
// run's own folder runs/test-<id>/.

// testRunUpstreamCopyLimit bounds the upstream step outputs copied into a test
// run so a step can read what earlier steps produced.
const testRunUpstreamCopyLimit int64 = 512 << 20

// beginTestRun prepares the test run's folder (upstream outputs and a copy of
// the workflow DB) and marks this controller's sessions as test sessions.
// realRunFolder is the run folder the step would normally use; the controller's
// selected run folder must already be the test run's.
func (hcpo *StepBasedWorkflowOrchestrator) beginTestRun(ctx context.Context, runID, realRunFolder, explicitSource string) (*testmode.Run, error) {
	_ = ctx
	if !strings.HasPrefix(runID, testmode.FolderPrefix) || strings.ContainsAny(runID, `/\`) {
		return nil, fmt.Errorf("invalid test run id %q", runID)
	}
	if !strings.HasPrefix(hcpo.selectedRunFolder, runID+"/") && hcpo.selectedRunFolder != runID {
		return nil, fmt.Errorf("test run %s: run folder %q is not the test run's", runID, hcpo.selectedRunFolder)
	}
	docsRoot, err := filepath.Abs(GetPromptDocsRoot())
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	workflowPath := strings.Trim(filepath.ToSlash(hcpo.GetWorkspacePath()), "/")
	if workflowPath == "" || workflowPath == "." {
		return nil, fmt.Errorf("test run %s: no workflow folder", runID)
	}
	workflowAbs := filepath.Join(docsRoot, filepath.FromSlash(workflowPath))
	sourceRun, err := resolveTestSourceRun(workflowAbs, realRunFolder, explicitSource)
	if err != nil {
		return nil, err
	}
	run := &testmode.Run{
		ID:           runID,
		WorkflowPath: workflowPath,
		RunFolder:    hcpo.selectedRunFolder,
		DocsRootAbs:  docsRoot,
		SourceRun:    sourceRun,
	}
	rootAbs := filepath.Join(docsRoot, filepath.FromSlash(run.Root()))
	if err := os.MkdirAll(rootAbs, 0o775); err != nil {
		return nil, fmt.Errorf("create test run folder: %w", err)
	}
	run.ActionsPath = filepath.Join(rootAbs, "test_mode_actions.jsonl")

	// Every entry of the workflow folder except runs/, and every other run
	// under runs/, is write-blocked for the test run's sessions.
	entries, err := os.ReadDir(workflowAbs)
	if err != nil {
		return nil, fmt.Errorf("list workflow folder: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == "runs" {
			continue
		}
		run.FixedBlockedWrites = append(run.FixedBlockedWrites, workflowPath+"/"+entry.Name())
	}
	if runs, err := os.ReadDir(filepath.Join(workflowAbs, "runs")); err == nil {
		for _, entry := range runs {
			if entry.Name() != runID {
				run.FixedBlockedWrites = append(run.FixedBlockedWrites, workflowPath+"/runs/"+entry.Name())
			}
		}
	}
	// The DB file itself is denied even if it was created after the listing.
	run.FixedBlockedWrites = append(run.FixedBlockedWrites, workflowPath+"/"+DBFolderName)

	// Upstream outputs: the step reads what earlier steps wrote in the real run.
	if sourceRun != "" {
		src := filepath.Join(workflowAbs, "runs", filepath.FromSlash(sourceRun), "execution")
		dst := filepath.Join(docsRoot, filepath.FromSlash(workflowPath), "runs", filepath.FromSlash(run.RunFolder), "execution")
		if err := copyTreeLimited(src, dst, testRunUpstreamCopyLimit); err != nil {
			return nil, fmt.Errorf("copy upstream outputs into the test run: %w", err)
		}
	}

	// The workflow DB: a consistent copy (committed WAL rows included).
	realDB := filepath.Join(workflowAbs, DBFolderName, "db.sqlite")
	if _, err := os.Stat(realDB); err == nil {
		copyAbs := filepath.Join(rootAbs, DBFolderName, "db.sqlite")
		if err := copySQLiteDatabase(realDB, copyAbs); err != nil {
			return nil, fmt.Errorf("copy the workflow database for the test run: %w", err)
		}
		run.DBAbsPath = copyAbs
		run.DBPath = run.Root() + "/" + DBFolderName + "/db.sqlite"
	}

	hcpo.testRun.Store(run)
	if sessionID := strings.TrimSpace(hcpo.GetMCPSessionID()); sessionID != "" {
		testmode.Register(sessionID, run)
	}
	hcpo.GetLogger().Info(fmt.Sprintf("🧪 [TEST_MODE] Started test run %s: folder=runs/%s db=%q", run.ID, run.RunFolder, run.DBPath))
	return run, nil
}

// endTestRun releases the run's sessions and writes its record.
func (hcpo *StepBasedWorkflowOrchestrator) endTestRun(run *testmode.Run, stepID string, execErr error) {
	if run == nil {
		return
	}
	hcpo.testRun.CompareAndSwap(run, nil)
	testmode.End(run)
	record := map[string]interface{}{
		"test_run_id": run.ID,
		"step_id":     stepID,
		"run_folder":  run.RunFolder,
		"source_run":  run.SourceRun,
		"db_copy":     run.DBPath,
		"finished_at": time.Now().UTC().Format(time.RFC3339),
		"succeeded":   execErr == nil,
		"actions":     run.Actions(),
	}
	if execErr != nil {
		record["error"] = execErr.Error()
	}
	if data, err := json.MarshalIndent(record, "", "  "); err == nil {
		path := filepath.Join(run.DocsRootAbs, filepath.FromSlash(run.Root()), "test_mode.json")
		_ = os.WriteFile(path, data, 0o644) //nolint:gosec // server-owned record in the test run folder
	}
}

// activeTestRun returns the controller's running test run, or nil.
func (hcpo *StepBasedWorkflowOrchestrator) activeTestRun() *testmode.Run {
	return hcpo.testRun.Load()
}

// registerTestSession puts a newly configured tool session into the active
// test run. Called for every step session the controller sets up.
func (hcpo *StepBasedWorkflowOrchestrator) registerTestSession(sessionID string) {
	if run := hcpo.activeTestRun(); run != nil {
		testmode.Register(sessionID, run)
	}
}

// testModeWritePaths narrows a step's write grants to the test run folder,
// plus the run's DB copy folder for steps that write the DB directly.
func (hcpo *StepBasedWorkflowOrchestrator) testModeWritePaths(paths []string) []string {
	run := hcpo.activeTestRun()
	if run == nil {
		return paths
	}
	kept := make([]string, 0, len(paths)+1)
	for _, p := range paths {
		if run.Writable(p) {
			kept = append(kept, p)
		}
	}
	if run.DBPath != "" {
		kept = append(kept, run.Root()+"/"+DBFolderName)
	}
	return kept
}

// testModeDBAbsPath is the DB_PATH a step's shell and scripts get.
func (hcpo *StepBasedWorkflowOrchestrator) testModeDBAbsPath(real string) string {
	run := hcpo.activeTestRun()
	if run == nil || real == "" {
		return real
	}
	return run.DBAbsPath
}

// copySQLiteDatabase writes a consistent copy of src (including committed WAL
// rows) to dst with VACUUM INTO. The source is opened query-only.
func copySQLiteDatabase(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o775); err != nil {
		return err
	}
	_ = os.Remove(dst)
	db, err := sql.Open("sqlite", "file:"+src+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec("VACUUM INTO ?", dst); err != nil {
		return err
	}
	if info, err := os.Stat(src); err == nil {
		_ = os.Chmod(dst, info.Mode().Perm()) //nolint:gosec // same permissions as the workflow's own DB
	}
	return nil
}

// copyTreeLimited copies a directory tree, failing when it exceeds limit bytes.
// A missing source is not an error (no upstream outputs yet). Symlinks are not
// followed.
func copyTreeLimited(src, dst string, limit int64) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	}
	var total int64
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o775)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if total > limit {
			return fmt.Errorf("upstream outputs exceed %d MB", limit>>20)
		}
		in, err := os.Open(path) //nolint:gosec // walking the workflow's own run folder
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm()) //nolint:gosec // inside the test run folder
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}

// testModePromptSection tells the step it is in test mode.
func testModePromptSection(run *testmode.Run) string {
	if run == nil {
		return ""
	}
	return "## TEST MODE\n" +
		"This is a test run (" + run.ID + ") to verify the step without real-world side effects. Reads are real. " +
		"Actions with external effects (sending or posting messages, submitting forms, clicking in the browser, " +
		"MCP tools that are not read-only, notifications, Crew and schedule calls) are NOT performed: they return a " +
		"\"TEST MODE ... was NOT run\" result and are recorded. Treat such a result as if the action succeeded and finish " +
		"the step; never retry it or look for another way to perform it (shell, curl, scripts). The workflow database is a " +
		"copy and files go to the test run folder; nothing written here reaches the real workflow. $" + testmode.EnvFlag + "=1 in the shell.\n"
}

// resolveTestSourceRun picks the real run a test run copies upstream outputs
// from. The workshop's own folder (iteration-0) is a scratch run that usually
// holds only the steps the Builder ran by hand, so a late step such as a
// recorder finds nothing upstream. Without an explicit source, the most
// complete of the ten newest real runs of the group is used (the one with the
// most populated step folders, newest on a tie); the fallback is the workshop's
// own run folder. An explicit source is model input and must be a plain
// relative run folder that has an execution folder.
func resolveTestSourceRun(workflowAbs, fallback, explicit string) (string, error) {
	runsDir := filepath.Join(workflowAbs, "runs")
	group := path.Base(filepath.ToSlash(fallback))
	if explicit = strings.Trim(strings.TrimSpace(filepath.ToSlash(explicit)), "/"); explicit != "" {
		first, _, _ := strings.Cut(explicit, "/")
		if strings.Contains(explicit, "..") || filepath.IsAbs(explicit) || strings.HasPrefix(first, testmode.FolderPrefix) {
			return "", fmt.Errorf("source_run %q is not a real run folder", explicit)
		}
		if info, err := os.Stat(filepath.Join(runsDir, filepath.FromSlash(explicit), "execution")); err != nil || !info.IsDir() {
			return "", fmt.Errorf("source_run %q has no execution folder", explicit)
		}
		return explicit, nil
	}
	entries, err := os.ReadDir(runsDir)
	if err != nil || group == "" || group == "." {
		return fallback, nil
	}
	type candidate struct {
		folder  string
		modTime time.Time
		steps   int
	}
	var candidates []candidate
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || strings.HasPrefix(entry.Name(), testmode.FolderPrefix) {
			continue
		}
		execution := filepath.Join(runsDir, entry.Name(), group, "execution")
		info, err := os.Stat(execution)
		if err != nil || !info.IsDir() {
			continue
		}
		steps := 0
		if children, err := os.ReadDir(execution); err == nil {
			for _, child := range children {
				name := child.Name()
				if !child.IsDir() || strings.HasPrefix(name, ".") || name == "archived" || name == "Downloads" {
					continue
				}
				if files, err := os.ReadDir(filepath.Join(execution, name)); err == nil && len(files) > 0 {
					steps++
				}
			}
		}
		candidates = append(candidates, candidate{folder: entry.Name() + "/" + group, modTime: info.ModTime(), steps: steps})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].modTime.After(candidates[j].modTime) })
	if len(candidates) > 10 {
		candidates = candidates[:10]
	}
	best := -1
	for i, c := range candidates {
		if best < 0 || c.steps > candidates[best].steps {
			best = i
		}
	}
	if best < 0 || candidates[best].steps == 0 {
		return fallback, nil
	}
	return candidates[best].folder, nil
}
