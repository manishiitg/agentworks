package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

const (
	defaultRunRetentionCount = 10
	maxRunRetentionCount     = 50
)

// prepareCurrentRun rotates the previous iteration-0 into an archive, then
// returns the fresh current run slot. workflow.json run_retention_count controls
// the number of archives retained.
func (hcpo *StepBasedWorkflowOrchestrator) prepareCurrentRun(ctx context.Context, workspacePath string) (string, error) {
	if opts := hcpo.GetExecutionOptions(); opts != nil && opts.WebhookInputFile != "" {
		if !regexp.MustCompile(`^iteration-[0-9]+-(?:hook|slack-[a-f0-9]+)$`).MatchString(opts.SelectedRunFolder) {
			return "", fmt.Errorf("invalid webhook run folder")
		}
		return opts.SelectedRunFolder, hcpo.createRunFolderStructure(ctx, filepath.Join(workspacePath, "runs", opts.SelectedRunFolder))
	}
	if opts := hcpo.GetExecutionOptions(); opts != nil && (opts.RunKind == "schedule" || opts.RunKind == "slack") {
		if !regexp.MustCompile(`^iteration-[0-9]+-(?:sched|slack-[a-f0-9]+)$`).MatchString(opts.SelectedRunFolder) || opts.ScheduleRunID == "" || opts.ScheduleID == "" {
			return "", fmt.Errorf("invalid scheduled run binding")
		}
		return opts.SelectedRunFolder, hcpo.createRunFolderStructure(ctx, filepath.Join(workspacePath, "runs", opts.SelectedRunFolder))
	}
	runsPath := fmt.Sprintf("%s/runs", workspacePath)
	runRetentionCount := hcpo.resolveRunRetentionCount(ctx)
	if err := hcpo.rotateIterationZero(ctx, runsPath, runRetentionCount); err != nil {
		return "", err
	}
	hcpo.GetLogger().Info(fmt.Sprintf("Using iteration-0 for workflow execution (keeping %d backup iteration(s))", runRetentionCount))
	return currentWorkflowRunFolder, nil
}

func (hcpo *StepBasedWorkflowOrchestrator) resolveRunRetentionCount(ctx context.Context) int {
	content, err := hcpo.ReadWorkspaceFile(ctx, "workflow.json")
	if err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("Could not read workflow.json for run retention, using default %d: %v", defaultRunRetentionCount, err))
		return defaultRunRetentionCount
	}

	var manifest struct {
		RunRetentionCount *int `json:"run_retention_count,omitempty"`
	}
	if err := json.Unmarshal([]byte(content), &manifest); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("Could not parse workflow.json run_retention_count, using default %d: %v", defaultRunRetentionCount, err))
		return defaultRunRetentionCount
	}
	if manifest.RunRetentionCount == nil {
		return defaultRunRetentionCount
	}
	if *manifest.RunRetentionCount < 1 || *manifest.RunRetentionCount > maxRunRetentionCount {
		hcpo.GetLogger().Warn(fmt.Sprintf("Invalid workflow.json run_retention_count=%d, using default %d", *manifest.RunRetentionCount, defaultRunRetentionCount))
		return defaultRunRetentionCount
	}
	return *manifest.RunRetentionCount
}

// rotateIterationZero rotates the workflow runs tree's iteration-0 to the next
// backup name, then creates a fresh iteration-0.
func (hcpo *StepBasedWorkflowOrchestrator) rotateIterationZero(ctx context.Context, runsPath string, keep int) error {
	workflowIter0 := fmt.Sprintf("%s/iteration-0", runsPath)

	workflowExists := hcpo.workspaceFileExists(ctx, workflowIter0)

	if workflowExists {
		backupName := hcpo.nextAvailableIterationAcross(ctx, runsPath)
		workflowBackup := fmt.Sprintf("%s/%s", runsPath, backupName)

		hcpo.GetLogger().Info(fmt.Sprintf("Backing up %s/iteration-0 -> %s", runsPath, backupName))
		if err := hcpo.MoveWorkspaceFile(ctx, workflowIter0, workflowBackup); err != nil {
			return fmt.Errorf("refusing to rotate runs: failed to back up workflow iteration-0 to %s: %w", backupName, err)
		}
		// Cost records keep the UUID as their immutable identity. Rotation only
		// updates their human-readable archived path, so the next iteration-0
		// run cannot inherit historical spend.
		if err := hcpo.ArchiveRunCostPaths(ctx, "iteration-0", backupName); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Could not update archived cost paths for %s: %v", backupName, err))
		}
		// Run history is the second thing that names this folder, and the only
		// one that was never repointed. Cost records above follow the rotation;
		// the history entry kept claiming iteration-0, so the schedule popup
		// looked every historical run's cost up against the live slot and
		// showed the current run's spend on every row.
		if err := hcpo.ArchiveScheduleRunFolder(ctx, "iteration-0", backupName); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Could not update run history folder for %s: %v", backupName, err))
		}
	}

	hcpo.pruneOldIterations(ctx, runsPath, keep)

	if err := hcpo.createRunFolderStructure(ctx, workflowIter0); err != nil {
		return fmt.Errorf("failed to create workflow iteration-0: %w", err)
	}
	if err := hcpo.writeRunIndex(ctx, "full_run_rotation"); err != nil {
		return fmt.Errorf("failed to publish run provenance: %w", err)
	}
	return nil
}

// nextAvailableIterationAcross returns "iteration-{N+1}" where N is the highest
// iteration-N found across all supplied runs paths.
func (hcpo *StepBasedWorkflowOrchestrator) nextAvailableIterationAcross(ctx context.Context, paths ...string) string {
	maxIter := 0
	// Builder archives, webhook runs, and scheduled runs share one numeric
	// namespace. Counting all three prevents iteration-12 from being allocated
	// after iteration-12-sched (or -hook) already exists.
	re := regexp.MustCompile(`^iteration-(\d+)(?:-(?:hook|sched|slack-[a-f0-9]+))?$`)
	for _, p := range paths {
		folders, err := hcpo.listRunFolders(ctx, p)
		if err != nil {
			continue
		}
		for _, folder := range folders {
			matches := re.FindStringSubmatch(folder)
			if len(matches) > 1 {
				var n int
				if _, err := fmt.Sscanf(matches[1], "%d", &n); err == nil && n > maxIter {
					maxIter = n
				}
			}
		}
	}
	return fmt.Sprintf("iteration-%d", maxIter+1)
}

// pruneOldIterations deletes backup iteration folders (iteration-1, iteration-2, ...)
// keeping only the most recent `keep` backups. iteration-0 is never deleted.
func (hcpo *StepBasedWorkflowOrchestrator) pruneOldIterations(ctx context.Context, runsPath string, keep int) {
	existingFolders, err := hcpo.listRunFolders(ctx, runsPath)
	if err != nil {
		return
	}

	// Collect backup iteration numbers (skip iteration-0)
	type iterEntry struct {
		num  int
		name string
	}
	var backups []iterEntry
	// Retention of Builder archives is intentionally independent. Trigger-owned
	// immutable folders have their own retention policies and must not be pruned
	// by an interactive Builder run.
	re := regexp.MustCompile(`^iteration-(\d+)$`)
	for _, folder := range existingFolders {
		matches := re.FindStringSubmatch(folder)
		if len(matches) > 1 {
			var n int
			if _, scanErr := fmt.Sscanf(matches[1], "%d", &n); scanErr == nil && n > 0 {
				backups = append(backups, iterEntry{num: n, name: folder})
			}
		}
	}

	if len(backups) <= keep {
		return
	}

	// Sort descending by iteration number — keep the highest N
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].num > backups[j].num
	})

	for _, entry := range backups[keep:] {
		folderPath := fmt.Sprintf("%s/%s", runsPath, entry.name)
		hcpo.GetLogger().Info(fmt.Sprintf("🗑️ Pruning old iteration backup: %s", entry.name))
		if delErr := hcpo.DeleteWorkspaceFile(ctx, folderPath); delErr != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to delete old iteration %s: %v", entry.name, delErr))
		}
	}
}

// nextAvailableIteration finds the next available iteration-N name (N >= 1) under runsPath.
//
//nolint:unused // kept for future non-destructive iteration allocation work.
func (hcpo *StepBasedWorkflowOrchestrator) nextAvailableIteration(ctx context.Context, runsPath string) (string, error) {
	existingFolders, err := hcpo.listRunFolders(ctx, runsPath)
	if err != nil {
		// If we can't list, start from 1
		return "iteration-1", nil
	}

	maxIter := 0
	re := regexp.MustCompile(`^iteration-(\d+)(?:-(?:hook|sched|slack-[a-f0-9]+))?$`)
	for _, folder := range existingFolders {
		matches := re.FindStringSubmatch(folder)
		if len(matches) > 1 {
			var n int
			if _, err := fmt.Sscanf(matches[1], "%d", &n); err == nil && n > maxIter {
				maxIter = n
			}
		}
	}
	return fmt.Sprintf("iteration-%d", maxIter+1), nil
}

// workspaceFileExists checks if a file or directory exists in the workspace
func (hcpo *StepBasedWorkflowOrchestrator) workspaceFileExists(ctx context.Context, path string) bool {
	// Try to read the path directly (for files)
	_, err := hcpo.ReadWorkspaceFile(ctx, path)
	if err == nil {
		return true
	}

	// Check if it exists by listing parent directory (for both files and directories)
	parent := filepath.Dir(path)
	filename := filepath.Base(path)

	// List files and directories in parent using BaseOrchestrator method
	items, err := hcpo.BaseOrchestrator.ListWorkspaceFiles(ctx, parent)
	if err == nil {
		for _, item := range items {
			if item == filename {
				return true
			}
		}
	}

	return false
}

// listRunFolders lists existing run folder names
func (hcpo *StepBasedWorkflowOrchestrator) listRunFolders(ctx context.Context, runsPath string) ([]string, error) {
	// Use BaseOrchestrator's ListWorkspaceDirectories function
	return hcpo.BaseOrchestrator.ListWorkspaceDirectories(ctx, runsPath)
}

// createRunFolderStructure creates the basic structure for a run folder.
// Creates folders via Workspace API only (ensures consistency with workspace listings).
//
// The runPath can be in any format - it will be normalized by createFolderViaAPI.
// Typically runPath is already relative to workspace-docs root (e.g., "Workflow/ICICI.../runs/...").
//
// NOTE: Do NOT use os.MkdirAll here - see controller_execution.go for explanation.
func (hcpo *StepBasedWorkflowOrchestrator) createRunFolderStructure(ctx context.Context, runPath string) error {
	if runPath == "" {
		return fmt.Errorf("invalid run path: empty")
	}

	// Create run folder via Workspace API - normalization happens inside
	if err := createFolderViaAPI(ctx, runPath); err != nil {
		return fmt.Errorf("failed to create run folder: %w", err)
	}

	// Builder-triggered single-step runs expect the same baseline execution tree as full
	// workflow runs. Create execution/ and execution/Downloads/ eagerly so browser-based
	// steps can inspect or move downloads before any step-specific folder is created.
	executionPath := filepath.Join(runPath, "execution")
	if err := createFolderViaAPI(ctx, executionPath); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to create execution folder via API: %v (continuing)", err))
	} else {
		hcpo.GetLogger().Info(fmt.Sprintf("✅ Created execution folder: %s", executionPath))
	}

	downloadsPath := filepath.Join(executionPath, "Downloads")
	if err := createFolderViaAPI(ctx, downloadsPath); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to create execution Downloads folder via API: %v (continuing)", err))
	} else {
		hcpo.GetLogger().Info(fmt.Sprintf("✅ Created execution Downloads folder: %s", downloadsPath))
	}

	// Create knowledgebase folder at workspace root (shared across all runs) - only if enabled
	if hcpo.UseKnowledgebase() {
		workspacePath := hcpo.GetWorkspacePath()
		if err := createFolderViaAPI(ctx, KnowledgebaseFolderName, workspacePath); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to create knowledgebase folder via API: %v (continuing)", err))
			// Don't fail - knowledgebase folder will be created when first file is written
		} else {
			hcpo.GetLogger().Info(fmt.Sprintf("✅ Created knowledgebase folder: %s/%s", workspacePath, KnowledgebaseFolderName))
		}
		if err := createFolderViaAPI(ctx, filepath.Join(KnowledgebaseFolderName, KnowledgebaseContextFolderName), workspacePath); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to create knowledgebase context folder via API: %v (continuing)", err))
		}
		// Seed knowledgebase/notes/_index.json so the first agent read sees a valid registry.
		// Pre-existing files are left untouched. kbShape is retained for config compatibility
		// but runtime behavior is always notes-only (see workflowtypes.ResolveKBShape).
		kbShape := hcpo.KBShape()
		if err := InitKBGraphFiles(ctx, hcpo.BaseOrchestrator, workspacePath, kbShape); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to initialize KB files: %v (continuing)", err))
		} else {
			hcpo.GetLogger().Info(fmt.Sprintf("✅ Initialized KB files (shape=%s) in %s/%s/", workflowtypes.ResolveKBShape(kbShape), workspacePath, KnowledgebaseFolderName))
		}
	} else {
		hcpo.GetLogger().Info("⏭️ Skipping knowledgebase folder creation (disabled in preset)")
	}

	// Create db folder at workspace root (always enabled, no preset toggle).
	// Structured JSON data shared across all runs and groups. See DBFolderName in controller_execution.go.
	if hcpo.platformStoresEnabled() {
		workspacePath := hcpo.GetWorkspacePath()
		if err := createFolderViaAPI(ctx, DBFolderName, workspacePath); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to create db folder via API: %v (continuing)", err))
		} else {
			hcpo.GetLogger().Info(fmt.Sprintf("✅ Created db folder: %s/%s", workspacePath, DBFolderName))
		}
		if err := createFolderViaAPI(ctx, filepath.Join(DBFolderName, DBAssetsFolderName), workspacePath); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to create db assets folder via API: %v (continuing)", err))
		}
	}

	// Create soul folder at workspace root (builder's long-term memory, always enabled).
	// Only the workflow interactive builder writes here. See SoulFolderName in controller_execution.go.
	{
		workspacePath := hcpo.GetWorkspacePath()
		if err := createFolderViaAPI(ctx, SoulFolderName, workspacePath); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to create soul folder via API: %v (continuing)", err))
		} else {
			hcpo.GetLogger().Info(fmt.Sprintf("✅ Created soul folder: %s/%s", workspacePath, SoulFolderName))
		}
	}

	hcpo.GetLogger().Info(fmt.Sprintf("✅ Created run folder structure: %s", runPath))
	return nil
}

// determineRunFolderForCleanup determines which run folder will be used (if any) without creating it.
// Always returns iteration-0 since that is the only folder workflows run in.
func (hcpo *StepBasedWorkflowOrchestrator) determineRunFolderForCleanup(ctx context.Context, workspacePath string) (string, bool, error) {
	runsPath := fmt.Sprintf("%s/runs", workspacePath)
	iteration0Path := fmt.Sprintf("%s/iteration-0", runsPath)

	if hcpo.workspaceFileExists(ctx, iteration0Path) {
		return "iteration-0", true, nil
	}
	// iteration-0 doesn't exist yet — nothing to clean
	return "", false, nil
}

// shouldAskDeleteOldProgress determines if we should ask the "Delete old progress" question
// Returns true only when we're reusing an existing folder that might have old progress
func (hcpo *StepBasedWorkflowOrchestrator) shouldAskDeleteOldProgress(ctx context.Context, workspacePath string) bool {
	_, shouldClean, err := hcpo.determineRunFolderForCleanup(ctx, workspacePath)
	if err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to determine run folder for cleanup check: %v, defaulting to ask question", err))
		return true // Default to asking if we can't determine
	}
	return shouldClean
}

// cleanupExecutionArtifactsForFreshStart cleans execution and validation artifacts in the current run slot.
// This handles both new runs folder structure and old structure for backward compatibility.
func (hcpo *StepBasedWorkflowOrchestrator) cleanupExecutionArtifactsForFreshStart(ctx context.Context, workspacePath string) {
	hcpo.GetLogger().Info("🧹 Starting cleanup of execution artifacts for fresh start")

	// Check if a specific run folder was already selected (from frontend or earlier resolution)
	var runFolderName string
	var shouldCleanSpecificFolder bool

	if hcpo.selectedRunFolder != "" {
		// Use the already-selected run folder (from frontend options)
		runFolderName = hcpo.selectedRunFolder
		shouldCleanSpecificFolder = true
		hcpo.GetLogger().Info(fmt.Sprintf("📁 Using already-selected run folder for cleanup: %s", runFolderName))
	} else {
		// Determine which run folder will be used (if any)
		var err error
		runFolderName, shouldCleanSpecificFolder, err = hcpo.determineRunFolderForCleanup(ctx, workspacePath)
		if err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to determine run folder for cleanup: %v, will only clean old structure", err))
			shouldCleanSpecificFolder = false
		}
	}

	// Clean specific run folder if we're reusing it
	if shouldCleanSpecificFolder && runFolderName != "" {
		hcpo.GetLogger().Info(fmt.Sprintf("📁 Cleaning specific run folder: %s", runFolderName))
		runFolderPath := fmt.Sprintf("%s/runs/%s", workspacePath, runFolderName)

		// Clean execution directory in run folder (this will recursively delete all step-* subdirectories)
		executionDir := fmt.Sprintf("%s/execution", runFolderPath)
		if err := hcpo.CleanupDirectory(ctx, executionDir, "execution"); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to cleanup execution directory in run folder: %v", err))
		} else {
			hcpo.GetLogger().Info(fmt.Sprintf("🗑️ Cleaned up execution directory in run folder: %s (including all step-* subdirectories)", executionDir))
		}
		// Also clean logs directory in run folder
		logsDir := fmt.Sprintf("%s/logs", runFolderPath)
		if err := hcpo.CleanupDirectory(ctx, logsDir, "logs"); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to cleanup logs directory in run folder: %v", err))
		} else {
			hcpo.GetLogger().Info(fmt.Sprintf("🗑️ Cleaned up logs directory in run folder: %s", logsDir))
		}
	} else {
		hcpo.GetLogger().Info(fmt.Sprintf("📁 No specific run folder to clean (will create new folder or use new structure)"))
	}

	// Always clean old structure for backward compatibility
	hcpo.GetLogger().Info(fmt.Sprintf("🧹 Cleaning old structure for backward compatibility"))

	// Clean old execution directory
	oldExecutionDir := fmt.Sprintf("%s/execution", workspacePath)
	if err := hcpo.CleanupDirectory(ctx, oldExecutionDir, "execution"); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to cleanup old execution directory: %v", err))
	} else {
		hcpo.GetLogger().Info(fmt.Sprintf("🗑️ Cleaned up old execution directory: %s", oldExecutionDir))
	}
	// Also clean old logs directory
	oldLogsDir := fmt.Sprintf("%s/logs", workspacePath)
	if err := hcpo.CleanupDirectory(ctx, oldLogsDir, "logs"); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to cleanup old logs directory: %v", err))
	} else {
		hcpo.GetLogger().Info(fmt.Sprintf("🗑️ Cleaned up old logs directory: %s", oldLogsDir))
	}

	hcpo.GetLogger().Info(fmt.Sprintf("✅ Completed cleanup of execution artifacts for fresh start"))
}
