package step_based_workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// ExecutionManager centralizes all execution lifecycle decisions
// It handles:
// - Mapping ExecutionStrategy to CleanupScope
// - Applying cleanup (folders + progress)
// - Preparing execution for batch groups
type ExecutionManager struct {
	orchestrator *StepBasedWorkflowOrchestrator
}

// NewExecutionManager creates a new ExecutionManager
func NewExecutionManager(orch *StepBasedWorkflowOrchestrator) *ExecutionManager {
	return &ExecutionManager{
		orchestrator: orch,
	}
}

// ============================================================================
// PHASE 1: DECIDE - Prepare execution setup based on options
// ============================================================================

// PrepareExecution analyzes execution options and returns ExecutionSetup
// This is the SINGLE place that maps ExecutionStrategy -> CleanupScope
func (em *ExecutionManager) PrepareExecution(
	ctx context.Context,
	opts *ExecutionOptions,
	existingProgress *StepProgress,
	totalSteps int,
	runFolder string,
) (*ExecutionSetup, error) {
	orch := em.orchestrator

	setup := &ExecutionSetup{
		RunFolder: runFolder,
		Context:   &ExecutionContext{},
		Cleanup: CleanupScope{
			NewTotalSteps: totalSteps,
		},
		StartFromStep: 0,
	}

	// Handle nil opts - default to resume if progress exists, fresh start otherwise
	if opts == nil {
		if existingProgress != nil && len(existingProgress.CompletedStepIndices) > 0 {
			setup.Mode = ExecutionModeResume
			setup.StartFromStep = findNextIncompleteStep(existingProgress)
		} else {
			setup.Mode = ExecutionModeFresh
			setup.Cleanup = CleanupScope{
				InitFreshProgress: true,
				NewTotalSteps:     totalSteps,
			}
		}
		return setup, nil
	}

	// Map strategy to mode and cleanup
	// All strategies use learning enabled, no human feedback.
	switch opts.ExecutionStrategy {

	// === Fresh Start ===
	// All fresh start strategies (including deprecated aliases) map here
	case "start_from_beginning", "start_from_beginning_no_human":
		setup.Mode = ExecutionModeFresh
		setup.Cleanup = CleanupScope{
			DeleteProgress:    true,
			InitFreshProgress: true,
			CleanAllSteps:     true,
			NewTotalSteps:     totalSteps,
		}
		setup.Context.SkipHumanInput = true

	// === Resume Strategies ===
	// All resume strategies (including deprecated aliases) map here
	case "resume_from_step", "resume_from_step_no_human", "fast_resume_from_step":
		resumeStep := opts.ResumeFromStep // 1-based
		// Normalize resume_from_step=0 to 1 (start from step 1)
		if resumeStep == 0 {
			resumeStep = 1
		}
		if resumeStep < 0 {
			// CRITICAL: resume_from_step < 0 is invalid!
			// Request blocking human feedback approval before proceeding
			orch.GetLogger().Error(fmt.Sprintf("🚨 CRITICAL: Resume strategy selected but resume_from_step=%d (invalid)! This would delete all completed steps.", resumeStep), fmt.Errorf("invalid resume_from_step=%d", resumeStep))

			// Build context message showing what would be deleted
			var contextMsg strings.Builder
			contextMsg.WriteString("⚠️ **CRITICAL WARNING: Invalid Resume Step Detected**\n\n")
			contextMsg.WriteString(fmt.Sprintf("You selected a resume strategy but `resume_from_step=%d` is invalid.\n\n", resumeStep))

			if existingProgress != nil && len(existingProgress.CompletedStepIndices) > 0 {
				contextMsg.WriteString(fmt.Sprintf("**This would DELETE all %d completed steps and start from the beginning!**\n\n", len(existingProgress.CompletedStepIndices)))
				contextMsg.WriteString("Completed steps that would be deleted:\n")
				for _, idx := range existingProgress.CompletedStepIndices {
					contextMsg.WriteString(fmt.Sprintf("- Step %d\n", idx+1))
				}
				contextMsg.WriteString("\n")
			} else {
				contextMsg.WriteString("**This would start execution from the beginning.**\n\n")
			}

			contextMsg.WriteString("**Options:**\n")
			contextMsg.WriteString("1. **Approve & Continue**: Use next incomplete step (if available) or start from beginning\n")
			contextMsg.WriteString("2. **Reject**: Cancel execution - fix the resume step selection in the frontend\n")

			// Request blocking human feedback
			requestID := fmt.Sprintf("resume_step_validation_%d", time.Now().UnixNano())
			question := fmt.Sprintf("⚠️ Invalid Resume Step Detected (resume_from_step=%d)\n\nDo you want to proceed? This will delete all completed steps and start from the beginning.", resumeStep)

			approved, _, err := orch.RequestHumanFeedback(
				ctx,
				requestID,
				question,
				contextMsg.String(),
				orch.getSessionID(),
				orch.getWorkflowID(),
			)

			if err != nil {
				orch.GetLogger().Error(fmt.Sprintf("❌ Failed to request human feedback for resume step validation: %v", err), err)
				return nil, fmt.Errorf("failed to request approval for invalid resume step: %w", err)
			}

			if !approved {
				orch.GetLogger().Info("❌ User rejected proceeding with invalid resume step - canceling execution")
				return nil, fmt.Errorf("execution canceled: user rejected proceeding with invalid resume_from_step=%d", resumeStep)
			}

			// User approved - proceed with fallback logic
			orch.GetLogger().Info("✅ User approved proceeding with invalid resume step - using fallback logic")

			if existingProgress != nil {
				nextIncomplete := findNextIncompleteStep(existingProgress)
				if nextIncomplete < totalSteps {
					resumeStep = nextIncomplete + 1 // Convert to 1-based
					orch.GetLogger().Info(fmt.Sprintf("✅ Using next incomplete step %d instead", resumeStep))
				} else {
					// All steps complete - fallback to start from beginning
					orch.GetLogger().Warn(fmt.Sprintf("⚠️ All steps are complete, falling back to start from beginning"))
					setup.Mode = ExecutionModeFresh
					setup.Cleanup = CleanupScope{
						DeleteProgress:    true,
						InitFreshProgress: true,
						CleanAllSteps:     true,
						NewTotalSteps:     totalSteps,
					}
					return setup, nil
				}
			} else {
				// No existing progress - fallback to start from beginning
				orch.GetLogger().Warn(fmt.Sprintf("⚠️ No existing progress found, falling back to start from beginning"))
				setup.Mode = ExecutionModeFresh
				setup.Cleanup = CleanupScope{
					DeleteProgress:    true,
					InitFreshProgress: true,
					CleanAllSteps:     true,
					NewTotalSteps:     totalSteps,
				}
				return setup, nil
			}
		}
		setup.Mode = ExecutionModeResumeFromStep
		setup.StartFromStep = resumeStep - 1 // Convert to 0-based
		setup.Cleanup = CleanupScope{
			UpdateProgress: true,
			CleanFromStep:  resumeStep, // Delete step-N and all after
			NewTotalSteps:  totalSteps,
		}
		setup.Context.SkipHumanInput = true

	// === Single Step Execution ===

	case ExecutionStrategyRunSingleStep:
		targetStep := opts.ResumeFromStep // 1-based
		if targetStep <= 0 {
			targetStep = 1
		}
		setup.Mode = ExecutionModeSingleStep
		setup.StartFromStep = targetStep - 1 // Convert to 0-based
		setup.Cleanup = CleanupScope{
			CleanSpecificStep: targetStep, // Only delete step-N
			NewTotalSteps:     totalSteps,
		}
		setup.Context.RunSingleStepOnly = true
		setup.Context.SingleStepTarget = targetStep - 1

	// === Default (Resume) ===

	default:
		// Unknown or empty strategy - default to resume behavior
		if existingProgress != nil && len(existingProgress.CompletedStepIndices) > 0 {
			setup.Mode = ExecutionModeResume
			setup.StartFromStep = findNextIncompleteStep(existingProgress)
		} else {
			setup.Mode = ExecutionModeFresh
			setup.Cleanup = CleanupScope{
				InitFreshProgress: true,
				NewTotalSteps:     totalSteps,
			}
		}
		orch.GetLogger().Warn(fmt.Sprintf("⚠️ Unknown execution strategy '%s', defaulting to mode: %s", opts.ExecutionStrategy, setup.Mode))
	}

	if opts.WebhookStepID != "" {
		setup.Context.SkipHumanInput = true
	}

	// Propagate per-step operator instructions
	if len(opts.HumanInputs) > 0 {
		setup.Context.HumanInputs = opts.HumanInputs
	}

	orch.GetLogger().Info(fmt.Sprintf("📋 Prepared execution: mode=%s, startFrom=%d, cleanup=%s",
		setup.Mode, setup.StartFromStep+1, em.GetCleanupDescription(setup.Cleanup)))

	return setup, nil
}

// PrepareForBatchGroup prepares execution for a specific group in batch mode
// Each group gets its own run folder and fresh execution state
func (em *ExecutionManager) PrepareForBatchGroup(
	ctx context.Context,
	groupName string,
	runFolder string,
	totalSteps int,
	variableValues map[string]string,
	isNewFolder bool, // true if folder was just created
	baseExecCtx *ExecutionContext, // Base context to inherit settings from
	isFirstGroup bool, // true if this is the first group in the batch
) (*ExecutionSetup, error) {
	orch := em.orchestrator

	// Clone base context or create new one
	var execCtx *ExecutionContext
	if baseExecCtx != nil {
		cloned := *baseExecCtx
		cloned.HumanInputs = cloneWorkflowStringMap(baseExecCtx.HumanInputs)
		execCtx = &cloned
	} else {
		execCtx = &ExecutionContext{}
	}

	// Check execution strategy and resume step
	// IMPORTANT: Resume step should ONLY apply to the first group
	// Subsequent groups should always start from the beginning
	resumeStep := 0
	isStartFromBeginningStrategy := false
	if orch.executionOptions != nil {
		strategy := orch.executionOptions.ExecutionStrategy

		// Check if this is a "start from beginning" strategy
		isStartFromBeginningStrategy = strategy == ExecutionStrategyStartFromBeginningNoHuman

		// Check if this is a resume strategy
		isResumeStrategy := strategy == ExecutionStrategyResumeFromStepNoHuman ||
			strategy == ExecutionStrategyRunSingleStep

		// Only use resume step for the first group
		// All subsequent groups start from the beginning
		if (isFirstGroup || orch.executionOptions.WebhookStepID != "") && isResumeStrategy && orch.executionOptions.ResumeFromStep > 0 {
			resumeStep = orch.executionOptions.ResumeFromStep
		} else if !isFirstGroup {
			// Subsequent groups always start from beginning
			resumeStep = 0
		} else if orch.executionOptions.ResumeFromStep > 0 {
			orch.GetLogger().Warn(fmt.Sprintf("⚠️ Batch group cleanup: ResumeFromStep=%d but strategy=%s is not a resume strategy, ignoring ResumeFromStep",
				orch.executionOptions.ResumeFromStep, strategy))
		} else if isResumeStrategy {
			orch.GetLogger().Warn(fmt.Sprintf("⚠️ Batch group cleanup: strategy=%s is a resume strategy but ResumeFromStep=%d (<=0), will not resume",
				strategy, orch.executionOptions.ResumeFromStep))
		}
	} else {
	}

	// Determine cleanup scope
	cleanup := CleanupScope{
		NewTotalSteps: totalSteps,
	}

	// CleanAllSteps should ONLY be set for "start from beginning" strategies
	// Never set it when resuming from a step
	if resumeStep > 0 {
		// Resuming from a specific step - preserve progress, only clean from resume step
		cleanup.DeleteProgress = false     // Don't delete progress - we need to preserve it
		cleanup.InitFreshProgress = false  // Don't reinit - we're updating existing progress
		cleanup.CleanFromStep = resumeStep // Delete step-N and all after
		cleanup.UpdateProgress = true      // Update progress to remove steps >= resumeStep
		orch.GetLogger().Info(fmt.Sprintf("🔧 Batch group cleanup: will clean from step %d onwards (preserving steps 1-%d)", resumeStep, resumeStep-1))
	} else if isStartFromBeginningStrategy && !isNewFolder {
		// Only set CleanAllSteps if it's explicitly a "start from beginning" strategy AND folder exists
		// Fresh start: delete old and init new
		cleanup.DeleteProgress = true
		cleanup.InitFreshProgress = true
		cleanup.CleanAllSteps = true
		orch.GetLogger().Info(fmt.Sprintf("🔧 Batch group cleanup: will clean ALL steps (start from beginning strategy)"))
	} else if !isNewFolder {
		// Folder exists but not a "start from beginning" strategy and not resuming
		// Don't clean anything - preserve existing step folders
		cleanup.DeleteProgress = false
		cleanup.InitFreshProgress = false
		orch.GetLogger().Info(fmt.Sprintf("🔧 Batch group cleanup: folder exists but not starting from beginning and not resuming - preserving existing step folders"))
	} else {
		// New folder - initialize fresh progress
		cleanup.DeleteProgress = false
		cleanup.InitFreshProgress = true
	}

	// Determine start step and mode: if resuming, use resume step; otherwise start from beginning
	startFromStep := 0
	executionMode := ExecutionModeFresh // Default: each group starts fresh
	if resumeStep > 0 {
		startFromStep = resumeStep - 1              // Convert to 0-based (step 3 -> index 2)
		executionMode = ExecutionModeResumeFromStep // Use resume mode when resuming
		orch.GetLogger().Info(fmt.Sprintf("🔧 Batch group execution: will start from step %d (0-based index: %d) in resume mode", resumeStep, startFromStep))
	}

	setup := &ExecutionSetup{
		Mode:           executionMode,
		GroupName:      groupName,
		RunFolder:      runFolder,
		VariableValues: variableValues,
		Context:        execCtx,
		StartFromStep:  startFromStep, // Start from resume step if resuming, otherwise from beginning
		Cleanup:        cleanup,
	}

	return setup, nil
}

// ============================================================================
// PHASE 2: EXECUTE - Apply cleanup and prepare for execution
// ============================================================================

// ApplyCleanup performs all cleanup based on CleanupScope
// This should be called BEFORE starting execution
func (em *ExecutionManager) ApplyCleanup(ctx context.Context, setup *ExecutionSetup) error {
	orch := em.orchestrator
	scope := setup.Cleanup

	if !scope.HasCleanup() {
		orch.GetLogger().Info(fmt.Sprintf("✅ No cleanup needed for mode: %s", setup.Mode))
		return nil
	}

	orch.GetLogger().Info(fmt.Sprintf("🧹 Applying cleanup: %s", em.GetCleanupDescription(scope)))

	// Ensure run folder is set (required for all cleanup operations)
	if setup.RunFolder == "" {
		return fmt.Errorf(fmt.Sprintf("run folder not set - cannot apply cleanup"), nil)
	}

	// Temporarily set the run folder on orchestrator for cleanup operations
	// (This is needed because low-level functions use hcpo.selectedRunFolder)
	previousRunFolder := orch.selectedRunFolder
	orch.selectedRunFolder = setup.RunFolder
	defer func() {
		// Restore if we're not in batch mode (batch mode keeps the new folder)
		if setup.GroupName == "" {
			// For single execution, we want to keep the folder set
			// Only restore if explicitly told to
		}
	}()

	// 1. Delete progress file if requested
	// This is a fresh start - delete progress file which includes RoutingEvaluationCounts
	if scope.DeleteProgress {
		if err := orch.deleteStepProgress(ctx); err != nil {
			orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to delete progress: %v (continuing)", err))
		} else {
			orch.GetLogger().Info("🗑️ Cleared persisted workflow progress state")
		}
	}

	// 2. Handle execution folder cleanup
	// Safety check: CleanAllSteps should never be true when resuming from a specific step
	if scope.CleanAllSteps {
		if setup.Mode == ExecutionModeResumeFromStep || (setup.StartFromStep > 0 && setup.Mode != ExecutionModeFresh) {
			orch.GetLogger().Warn(fmt.Sprintf("🚨 BUG: CleanAllSteps=true but mode=%s, startFromStep=%d! This should never happen when resuming. Falling back to CleanFromStep=%d",
				setup.Mode, setup.StartFromStep, setup.StartFromStep+1))
			// Fall back to cleaning only from the resume step
			scope.CleanAllSteps = false
			if setup.StartFromStep >= 0 {
				scope.CleanFromStep = setup.StartFromStep + 1 // Convert to 1-based
				scope.UpdateProgress = true
			}
		} else {
			// Delete entire execution/ folder (only for fresh starts)
			executionDir := fmt.Sprintf("%s/runs/%s/execution", orch.GetWorkspacePath(), setup.RunFolder)
			orch.GetLogger().Info(fmt.Sprintf("🗑️ CleanAllSteps=true: Deleting entire execution/ folder (mode=%s, startFromStep=%d)", setup.Mode, setup.StartFromStep))
			if err := orch.CleanupDirectory(ctx, executionDir, "execution"); err != nil {
				orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to clean all steps: %v (continuing)", err))
			} else {
				orch.GetLogger().Info(fmt.Sprintf("🗑️ Cleaned entire execution/ folder"))
			}
			// Also delete logs/ folder
			logsDir := fmt.Sprintf("%s/runs/%s/logs", orch.GetWorkspacePath(), setup.RunFolder)
			if err := orch.CleanupDirectory(ctx, logsDir, "logs"); err != nil {
				orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to clean logs folder: %v (continuing)", err))
			} else {
				orch.GetLogger().Info(fmt.Sprintf("🗑️ Cleaned entire logs/ folder"))
			}
		}
	}

	if scope.CleanFromStep > 0 {
		// Delete step-N through step-Total
		cleanedCount := 0
		for stepNum := scope.CleanFromStep; stepNum <= scope.NewTotalSteps; stepNum++ {
			if err := orch.deleteStepExecutionFolder(ctx, stepNum); err != nil {
				orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to delete step %d: %v (continuing)", stepNum, err))
			} else {
				cleanedCount++
			}
		}
		orch.GetLogger().Info(fmt.Sprintf("🗑️ Cleaned %d step folders (step-%d to step-%d)", cleanedCount, scope.CleanFromStep, scope.NewTotalSteps))
	} else if scope.CleanSpecificStep > 0 {
		// Delete only specific step
		if err := orch.deleteStepExecutionFolder(ctx, scope.CleanSpecificStep); err != nil {
			orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to delete step %d: %v (continuing)", scope.CleanSpecificStep, err))
		} else {
			orch.GetLogger().Info(fmt.Sprintf("🗑️ Cleaned step-%d folder", scope.CleanSpecificStep))
		}
	}

	// 3. Update existing progress if needed (remove steps >= StartFromStep)
	// Note: StartFromStep can be 0 (resuming from step 1), so we check >= 0
	if scope.UpdateProgress && setup.StartFromStep >= 0 {
		progress, err := orch.loadStepProgress(ctx)
		if err != nil {
			// If progress file doesn't exist or can't be loaded, log warning but don't fail
			// This can happen if the file was deleted or corrupted
			orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to load progress for update: %v (progress file may not exist yet)", err))
		} else if progress != nil {
			// Preserve TotalSteps from existing progress (don't overwrite with NewTotalSteps)
			// Only update CompletedStepIndices to remove steps >= StartFromStep
			newCompleted := []int{}
			for _, idx := range progress.CompletedStepIndices {
				if idx < setup.StartFromStep {
					newCompleted = append(newCompleted, idx)
				}
			}
			removedCount := len(progress.CompletedStepIndices) - len(newCompleted)

			// Safety check: if we're about to clear all steps, log a warning
			if len(newCompleted) == 0 && len(progress.CompletedStepIndices) > 0 && setup.StartFromStep > 0 {
				orch.GetLogger().Warn(fmt.Sprintf("⚠️ WARNING: About to clear ALL completed steps! StartFromStep=%d, existing steps: %v. This might be a bug.",
					setup.StartFromStep, progress.CompletedStepIndices))
			}

			progress.CompletedStepIndices = newCompleted

			// Ensure TotalSteps is preserved (use existing value, or fallback to NewTotalSteps if 0)
			if progress.TotalSteps == 0 && scope.NewTotalSteps > 0 {
				progress.TotalSteps = scope.NewTotalSteps
			}

			if err := orch.saveStepProgress(ctx, progress); err != nil {
				orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to update progress: %v", err))
			} else {
				orch.GetLogger().Info(fmt.Sprintf("📝 Updated progress: removed %d completed steps >= step-%d, preserved TotalSteps=%d",
					removedCount, setup.StartFromStep+1, progress.TotalSteps))
			}
		} else {
			// Progress is nil (shouldn't happen if loadStepProgress succeeded, but handle it)
			orch.GetLogger().Warn(fmt.Sprintf("⚠️ Progress is nil after successful load (unexpected)"))
		}
	}

	// 4. Initialize fresh progress if needed
	if scope.InitFreshProgress {
		if err := orch.initializeFreshProgress(ctx, scope.NewTotalSteps); err != nil {
			return fmt.Errorf("failed to initialize progress: %w", err)
		}
	}

	// Restore previous run folder only if explicitly needed
	_ = previousRunFolder // Suppress unused warning

	orch.GetLogger().Info(fmt.Sprintf("✅ Cleanup completed for mode: %s", setup.Mode))
	return nil
}

// ApplyExecutionContext applies the ExecutionSetup context to the orchestrator
// This sets the orchestrator flags based on the resolved setup
func (em *ExecutionManager) ApplyExecutionContext(setup *ExecutionSetup) {
	orch := em.orchestrator

	if setup.Context == nil {
		return
	}

	// Apply context flags to orchestrator
	orch.SetSkipHumanInput(setup.Context.SkipHumanInput)
	if len(setup.Context.HumanInputs) > 0 {
		orch.humanInputOverrides = setup.Context.HumanInputs
		orch.GetLogger().Info(fmt.Sprintf("🔧 Set humanInputOverrides for %d step(s)", len(setup.Context.HumanInputs)))
	}

	orch.SetRunSingleStepMode(setup.Context.RunSingleStepOnly, setup.Context.SingleStepTarget)

	// Set run folder
	if setup.RunFolder != "" {
		orch.selectedRunFolder = setup.RunFolder
		// Also update iteration folder for token persistence
		// This ensures token_usage.json is written to the correct group folder during batch execution
		orch.SetIterationFolder(setup.RunFolder)
		// Update session working dir to the run's execution folder so all shell commands
		// from execution agents default there instead of falling back to workspace root.
		if orch.httpSessionID != "" && orch.GetWorkspacePath() != "" {
			executionPath := fmt.Sprintf("%s/runs/%s/execution", orch.GetWorkspacePath(), setup.RunFolder)
			common.SetSessionWorkingDir(orch.httpSessionID, executionPath)
		}
	}

	// Set variable values for batch execution
	if setup.VariableValues != nil {
		orch.variableValues = setup.VariableValues
		SyncVariablesToWorkspaceEnv(orch.BaseOrchestrator, setup.VariableValues)
	}

	orch.GetLogger().Info(fmt.Sprintf("🔧 Applied execution context: skipHuman=%v, singleStep=%v",
		setup.Context.SkipHumanInput, setup.Context.RunSingleStepOnly))
}

// ============================================================================
// HELPERS
// ============================================================================

// GetCleanupDescription returns a human-readable description of the cleanup scope
func (em *ExecutionManager) GetCleanupDescription(scope CleanupScope) string {
	if !scope.HasCleanup() {
		return "none"
	}

	var parts []string

	// Progress cleanup
	if scope.DeleteProgress {
		parts = append(parts, "delete_progress")
	}
	if scope.InitFreshProgress {
		parts = append(parts, fmt.Sprintf("init_progress(%d steps)", scope.NewTotalSteps))
	}
	if scope.UpdateProgress {
		parts = append(parts, "update_progress")
	}

	// Folder cleanup
	if scope.CleanAllSteps {
		parts = append(parts, "clean_all_steps")
	} else if scope.CleanFromStep > 0 {
		parts = append(parts, fmt.Sprintf("clean_steps_%d_to_%d", scope.CleanFromStep, scope.NewTotalSteps))
	} else if scope.CleanSpecificStep > 0 {
		parts = append(parts, fmt.Sprintf("clean_step_%d", scope.CleanSpecificStep))
	}

	return strings.Join(parts, ", ")
}

// findNextIncompleteStep finds the next step that hasn't been completed
func findNextIncompleteStep(progress *StepProgress) int {
	if progress == nil || progress.TotalSteps == 0 {
		return 0
	}

	// Create a set of completed indices
	completedSet := make(map[int]bool)
	for _, idx := range progress.CompletedStepIndices {
		completedSet[idx] = true
	}

	// Find first incomplete step
	for i := 0; i < progress.TotalSteps; i++ {
		if !completedSet[i] {
			return i
		}
	}

	// All steps complete - return total (will be handled by caller)
	return progress.TotalSteps
}

// BuildExecutionContextFromSetup creates an ExecutionContext from setup
// Useful when you need the context without applying to orchestrator
func (em *ExecutionManager) BuildExecutionContextFromSetup(setup *ExecutionSetup) *ExecutionContext {
	if setup == nil || setup.Context == nil {
		return &ExecutionContext{}
	}
	return setup.Context
}

// ============================================================================
// CONVENIENCE METHODS - For gradual migration from scattered cleanup calls
// ============================================================================

// CleanupForSingleStep handles cleanup when running a single step
// This is a convenience method for the common pattern in controller.go
func (em *ExecutionManager) CleanupForSingleStep(ctx context.Context, targetStep int, runFolder string) error {
	orch := em.orchestrator

	if runFolder == "" {
		return fmt.Errorf(fmt.Sprintf("run folder not set - cannot cleanup for single step"), nil)
	}

	// Set run folder temporarily for cleanup
	previousRunFolder := orch.selectedRunFolder
	orch.selectedRunFolder = runFolder
	defer func() {
		orch.selectedRunFolder = previousRunFolder
	}()

	orch.GetLogger().Info(fmt.Sprintf("🗑️ Cleaning up for single step execution: step-%d", targetStep))

	// Only delete the specific step folder
	if err := orch.deleteStepExecutionFolder(ctx, targetStep); err != nil {
		orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to delete step %d folder: %v", targetStep, err))
		return err
	}

	orch.GetLogger().Info(fmt.Sprintf("✅ Cleaned up step-%d folder for single step execution", targetStep))
	return nil
}

// CleanupForResumeFromStep handles cleanup when resuming from a specific step
// This deletes step N and all subsequent steps, and updates progress
func (em *ExecutionManager) CleanupForResumeFromStep(ctx context.Context, resumeStep int, totalSteps int, runFolder string) error {
	orch := em.orchestrator

	if runFolder == "" {
		return fmt.Errorf(fmt.Sprintf("run folder not set - cannot cleanup for resume"), nil)
	}

	// Set run folder temporarily for cleanup
	previousRunFolder := orch.selectedRunFolder
	orch.selectedRunFolder = runFolder
	defer func() {
		orch.selectedRunFolder = previousRunFolder
	}()

	orch.GetLogger().Info(fmt.Sprintf("🗑️ Cleaning up for resume from step %d (total: %d)", resumeStep, totalSteps))

	// Delete step folders from resumeStep to totalSteps
	cleanedCount := 0
	for stepNum := resumeStep; stepNum <= totalSteps; stepNum++ {
		if err := orch.deleteStepExecutionFolder(ctx, stepNum); err != nil {
			orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to delete step %d: %v (continuing)", stepNum, err))
		} else {
			cleanedCount++
		}
	}

	// Update progress: remove steps >= resumeStep-1 (0-based)
	progress, err := orch.loadStepProgress(ctx)
	if err != nil {
		// If progress file doesn't exist or can't be loaded, log warning but don't fail
		orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to load progress for resume cleanup: %v (progress file may not exist yet)", err))
	} else if progress != nil {
		newCompleted := []int{}
		startFromStep := resumeStep - 1 // Convert to 0-based
		for _, idx := range progress.CompletedStepIndices {
			if idx < startFromStep {
				newCompleted = append(newCompleted, idx)
			}
		}
		progress.CompletedStepIndices = newCompleted

		// Preserve TotalSteps - use existing value, or fallback to provided totalSteps if 0
		if progress.TotalSteps == 0 && totalSteps > 0 {
			progress.TotalSteps = totalSteps
			orch.GetLogger().Info(fmt.Sprintf("📝 Progress had TotalSteps=0, setting to %d", totalSteps))
		}

		if err := orch.saveStepProgress(ctx, progress); err != nil {
			orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to update progress: %v", err))
		} else {
		}
	} else {
		// Progress is nil (shouldn't happen if loadStepProgress succeeded, but handle it)
		orch.GetLogger().Warn(fmt.Sprintf("⚠️ Progress is nil after successful load (unexpected)"))
	}

	orch.GetLogger().Info(fmt.Sprintf("✅ Cleaned %d step folders for resume from step %d", cleanedCount, resumeStep))
	return nil
}

// CleanupForFreshStart handles cleanup when starting from beginning
// This deletes all execution artifacts and initializes fresh progress
func (em *ExecutionManager) CleanupForFreshStart(ctx context.Context, totalSteps int, runFolder string) error {
	orch := em.orchestrator

	if runFolder == "" {
		return fmt.Errorf(fmt.Sprintf("run folder not set - cannot cleanup for fresh start"), nil)
	}

	// Set run folder temporarily for cleanup
	previousRunFolder := orch.selectedRunFolder
	orch.selectedRunFolder = runFolder
	defer func() {
		orch.selectedRunFolder = previousRunFolder
	}()

	orch.GetLogger().Info(fmt.Sprintf("🗑️ Cleaning up for fresh start (total: %d steps)", totalSteps))

	// Delete progress file
	if err := orch.deleteStepProgress(ctx); err != nil {
		orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to delete progress: %v", err))
	}

	// Delete entire execution folder
	executionDir := fmt.Sprintf("%s/runs/%s/execution", orch.GetWorkspacePath(), runFolder)
	if err := orch.CleanupDirectory(ctx, executionDir, "execution"); err != nil {
		orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to clean execution folder: %v", err))
	}
	// Also delete logs folder
	logsDir := fmt.Sprintf("%s/runs/%s/logs", orch.GetWorkspacePath(), runFolder)
	if err := orch.CleanupDirectory(ctx, logsDir, "logs"); err != nil {
		orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to clean logs folder: %v", err))
	}

	// Initialize fresh progress
	if err := orch.initializeFreshProgress(ctx, totalSteps); err != nil {
		return fmt.Errorf("failed to initialize progress: %w", err)
	}

	orch.GetLogger().Info(fmt.Sprintf("✅ Fresh start cleanup completed"))
	return nil
}

// GetExecutionManager is a convenience method to get ExecutionManager from orchestrator
func (hcpo *StepBasedWorkflowOrchestrator) GetExecutionManager() *ExecutionManager {
	return NewExecutionManager(hcpo)
}

// CleanupProgressOnly deletes the progress file only (no folder cleanup)
// Used for fast execute scenarios where we want to re-run but keep artifacts
func (em *ExecutionManager) CleanupProgressOnly(ctx context.Context) error {
	orch := em.orchestrator

	orch.GetLogger().Info(fmt.Sprintf("🗑️ Cleaning up progress file only (fast execute mode)"))

	if err := orch.deleteStepProgress(ctx); err != nil {
		orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to delete progress: %v", err))
		return err
	}

	orch.GetLogger().Info(fmt.Sprintf("✅ Deleted progress file"))
	return nil
}

// CleanupForPlanChange handles cleanup when plan structure changed
// This is used when user chooses to delete old progress and start fresh after plan change
// It handles backward compatibility with old folder structure
func (em *ExecutionManager) CleanupForPlanChange(ctx context.Context, totalSteps int, workspacePath string) error {
	orch := em.orchestrator

	orch.GetLogger().Info(fmt.Sprintf("🧹 Cleaning up for plan change (total: %d steps)", totalSteps))

	// Delete progress file
	if err := orch.deleteStepProgress(ctx); err != nil {
		orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to delete progress: %v", err))
	}

	// Clean execution artifacts (handles both new and old structure for backward compat)
	orch.cleanupExecutionArtifactsForFreshStart(ctx, workspacePath)

	// Initialize fresh progress
	if err := orch.initializeFreshProgress(ctx, totalSteps); err != nil {
		return fmt.Errorf("failed to initialize progress: %w", err)
	}

	orch.GetLogger().Info(fmt.Sprintf("✅ Plan change cleanup completed"))
	return nil
}

// CleanupForStartFromBeginning handles cleanup when starting from beginning
// Similar to CleanupForPlanChange but used in different context
func (em *ExecutionManager) CleanupForStartFromBeginning(ctx context.Context, workspacePath string) error {
	orch := em.orchestrator

	orch.GetLogger().Info(fmt.Sprintf("🧹 Cleaning up for start from beginning"))

	// Delete progress file
	if err := orch.deleteStepProgress(ctx); err != nil {
		orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to delete progress: %v", err))
	}

	// Clean execution artifacts (handles both new and old structure)
	orch.cleanupExecutionArtifactsForFreshStart(ctx, workspacePath)

	orch.GetLogger().Info(fmt.Sprintf("✅ Start from beginning cleanup completed"))
	return nil
}

// CleanupExecutionFolder cleans only the execution folder (no progress changes)
// Used for fast execute range where we clean folders but keep progress structure
func (em *ExecutionManager) CleanupExecutionFolder(ctx context.Context, runFolder string) error {
	orch := em.orchestrator

	if runFolder == "" && orch.selectedRunFolder == "" {
		return fmt.Errorf(fmt.Sprintf("run folder not set - cannot cleanup execution folder"), nil)
	}

	targetFolder := runFolder
	if targetFolder == "" {
		targetFolder = orch.selectedRunFolder
	}

	var runWorkspacePath string
	if targetFolder != "" {
		runWorkspacePath = fmt.Sprintf("%s/runs/%s", orch.GetWorkspacePath(), targetFolder)
	} else {
		runWorkspacePath = orch.GetWorkspacePath()
	}

	executionDir := fmt.Sprintf("%s/execution", runWorkspacePath)

	orch.GetLogger().Info(fmt.Sprintf("🗑️ Cleaning execution folder: %s", executionDir))

	if err := orch.CleanupDirectory(ctx, executionDir, "execution"); err != nil {
		orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to cleanup execution directory: %v", err))
		return err
	}

	orch.GetLogger().Info(fmt.Sprintf("✅ Cleaned execution folder"))

	// Also clean logs folder
	logsDir := fmt.Sprintf("%s/logs", runWorkspacePath)
	orch.GetLogger().Info(fmt.Sprintf("🗑️ Cleaning logs folder: %s", logsDir))
	if err := orch.CleanupDirectory(ctx, logsDir, "logs"); err != nil {
		orch.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to cleanup logs directory: %v", err))
		return err
	}

	orch.GetLogger().Info(fmt.Sprintf("✅ Cleaned logs folder"))
	return nil
}
