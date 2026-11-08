package step_based_workflow

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

const (
	defaultRunRetentionCount = 10
)

// prepareCurrentRun allocates one run identity or continues a server-authorized
// existing run. Input labels never determine its folder.
func (hcpo *StepBasedWorkflowOrchestrator) prepareCurrentRun(ctx context.Context, workspacePath string) (string, error) {
	if opts := hcpo.GetExecutionOptions(); opts != nil && opts.WebhookInputFile != "" {
		if !IsPermanentRunFolder(opts.SelectedRunFolder) {
			return "", fmt.Errorf("invalid webhook run folder")
		}
		return opts.SelectedRunFolder, hcpo.createRunFolderStructure(ctx, filepath.Join(workspacePath, "runs", opts.SelectedRunFolder))
	}
	if opts := hcpo.GetExecutionOptions(); opts != nil && (opts.RunKind == "schedule" || opts.RunKind == "slack") {
		if !IsPermanentRunFolder(opts.SelectedRunFolder) || opts.ScheduleRunID == "" || opts.ScheduleID == "" {
			return "", fmt.Errorf("invalid scheduled run binding")
		}
		return opts.SelectedRunFolder, hcpo.createRunFolderStructure(ctx, filepath.Join(workspacePath, "runs", opts.SelectedRunFolder))
	}
	opts := hcpo.GetExecutionOptions()
	folder := ""
	if opts != nil {
		folder = opts.SelectedRunFolder
	}
	if folder == "" || folder == currentWorkflowRunFolder || (opts != nil && !opts.RunFolderAllocated && opts.ResumeFromStep <= 0) {
		folder = "run-" + workflowExecutionIDToken()
	}
	if err := ValidateWorkflowRunFolder(folder); err != nil {
		return "", err
	}
	if opts != nil && opts.ResumeFromStep > 0 {
		if !opts.AllowRunContinuation {
			return "", fmt.Errorf("only a Builder can continue an existing run; start a new run instead")
		}
		if _, err := hcpo.readExistingRunInputs(ctx, folder); err != nil {
			return "", err
		}
	}
	if opts != nil {
		opts.SelectedRunFolder = folder
	}
	return folder, hcpo.createRunFolderStructure(ctx, filepath.Join(workspacePath, "runs", folder))
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
