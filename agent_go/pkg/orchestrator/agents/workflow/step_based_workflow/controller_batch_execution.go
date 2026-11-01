package step_based_workflow

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	baseevents "github.com/manishiitg/mcpagent/events"
)

// BatchExecutionResult contains the result of batch execution
type BatchExecutionResult struct {
	TotalGroups         int
	CompletedGroups     int
	FailedGroups        int
	CanceledGroups      int
	Duration            time.Duration
	Success             bool
	Error               string
	CompletedGroupNames []string
	FailedGroupNames    []string
}

// getEnabledGroupsForExecution resolves one invocation or explicitly selected input sets.
// An unnamed item is transient execution state, never a persisted default group.
func (hcpo *StepBasedWorkflowOrchestrator) getEnabledGroupsForExecution() ([]VariableGroup, error) {
	var selected []string
	if hcpo.executionOptions != nil {
		selected = hcpo.executionOptions.EnabledGroupNames
	}
	return ResolveExecutionInputs(hcpo.variablesManifest, selected)
}

// runBatchExecution executes the workflow for multiple variable groups sequentially
// Uses ExecutionManager for centralized cleanup and progress management
func (hcpo *StepBasedWorkflowOrchestrator) runBatchExecution(
	ctx context.Context,
	breakdownSteps []PlanStepInterface,
	iteration int,
	execCtx *ExecutionContext,
) (*BatchExecutionResult, error) {
	enabledGroups, err := hcpo.getEnabledGroupsForExecution()
	if err != nil {
		return nil, err
	}
	if opts := hcpo.GetExecutionOptions(); opts != nil && (opts.WebhookInputFile != "" || len(opts.WebhookVariables) > 0) {
		enabledGroups = applyWebhookVariableOverrides(enabledGroups, opts.WebhookVariables)
	}
	totalGroups := len(enabledGroups)

	if totalGroups == 0 {
		return nil, fmt.Errorf("no enabled variable groups found for batch execution")
	}

	enabledGroupNames := make([]string, len(enabledGroups))
	for i, g := range enabledGroups {
		enabledGroupNames[i] = g.Name
	}

	hcpo.GetLogger().Info(fmt.Sprintf("🔄 Starting batch execution for %d variable groups", totalGroups))
	hcpo.ApplyWorkflowLogContext(hcpo.GetWorkspacePath(), orchestrator.SingleSelectedGroupName(enabledGroupNames))

	// Create ExecutionManager for centralized cleanup management
	execManager := NewExecutionManager(hcpo)

	result := &BatchExecutionResult{
		TotalGroups:         totalGroups,
		CompletedGroupNames: make([]string, 0),
		FailedGroupNames:    make([]string, 0),
	}
	startTime := time.Now()

	// Determine base iteration folder
	baseIterationFolder := hcpo.determineBaseIterationFolder()

	// Execute for each enabled group sequentially
	for groupIndex, group := range enabledGroups {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Batch execution canceled during group %s", group.Name))
			result.CanceledGroups = totalGroups - groupIndex
			remainingGroupNames := make([]string, 0)
			for i := groupIndex + 1; i < totalGroups; i++ {
				remainingGroupNames = append(remainingGroupNames, enabledGroups[i].Name)
			}
			hcpo.emitBatchExecutionCanceledEvent(ctx, totalGroups, groupIndex, group.Name, remainingGroupNames, "context canceled")
			result.Error = "batch execution canceled"
			result.Duration = time.Since(startTime)
			return result, ctx.Err()
		default:
		}

		hcpo.ApplyWorkflowLogContext(hcpo.GetWorkspacePath(), group.Name)
		hcpo.GetLogger().Info(fmt.Sprintf("📦 Batch execution: processing group %d/%d (%s)", groupIndex+1, totalGroups, group.Name))

		// Log group values being used for this execution
		if len(group.Values) > 0 {
			hcpo.GetLogger().Info(fmt.Sprintf("🔍 [GROUP EXECUTION] Using variable values for group %s: %v", group.Name, group.Values))
		} else {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [GROUP EXECUTION] Group %s has no variable values!", group.Name))
		}

		// Determine run folder for this group (uses sanitized group name as folder name)
		// Special case: if single group and selectedRunFolder already contains a group path, use it directly
		var runFolder string
		if totalGroups == 1 && hcpo.selectedRunFolder != "" && strings.Contains(hcpo.selectedRunFolder, "/") {
			// User explicitly selected a group folder (e.g., "iteration-13/siddharth")
			// Use it directly instead of recreating the path
			runFolder = hcpo.selectedRunFolder
		} else {
			// Multiple groups or no explicit group selection - create folder path
			runFolder = hcpo.createGroupRunFolder(baseIterationFolder, group.Name, totalGroups)
		}

		// Check if folder exists (to determine if we need cleanup)
		fullRunFolderPath := fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), runFolder)
		isNewFolder := true
		if exists := hcpo.workspaceFileExists(ctx, fullRunFolderPath); exists {
			isNewFolder = false
		}

		// Ensure run folder exists
		if err := hcpo.createRunFolderStructure(ctx, fullRunFolderPath); err != nil {
			hcpo.GetLogger().Error(fmt.Sprintf("❌ Failed to create run folder for group %s: %v", group.Name, err), nil)
			result.FailedGroups++
			result.FailedGroupNames = append(result.FailedGroupNames, group.Name)
			continue
		}
		if err := hcpo.markRunMetadataStarted(ctx, runFolder); err != nil {
			return nil, fmt.Errorf("bind run %s to executable plan revision: %w", runFolder, err)
		}

		// Use ExecutionManager to prepare and apply cleanup for this group
		// Pass isFirstGroup=true only for the first group (groupIndex == 0)
		// This ensures resume step only applies to first group, subsequent groups start from beginning
		isFirstGroup := groupIndex == 0
		groupSetup, err := execManager.PrepareForBatchGroup(
			ctx,
			group.Name,
			runFolder,
			len(breakdownSteps),
			group.Values,
			isNewFolder,
			execCtx,      // Inherit base execution context settings
			isFirstGroup, // Only first group can use resume step
		)
		if err != nil {
			hcpo.GetLogger().Error(fmt.Sprintf("❌ Failed to prepare execution for group %s: %v", group.Name, err), nil)
			result.FailedGroups++
			result.FailedGroupNames = append(result.FailedGroupNames, group.Name)
			continue
		}

		// Apply cleanup (deletes old artifacts, initializes fresh progress)
		if err := execManager.ApplyCleanup(ctx, groupSetup); err != nil {
			hcpo.GetLogger().Error(fmt.Sprintf("❌ Failed to apply cleanup for group %s: %v", group.Name, err), nil)
			result.FailedGroups++
			result.FailedGroupNames = append(result.FailedGroupNames, group.Name)
			continue
		}

		previousSessionID := hcpo.getSessionID()

		// CRITICAL FIX: Close entire previous session before starting new group
		// This ensures the new session ID gets fresh connections with the correct Downloads path
		// Close the entire session to ensure all connections are released.
		if groupIndex > 0 {
			if previousSessionID != "" {
				hcpo.GetLogger().Info(fmt.Sprintf("🔗 Closing entire previous session before starting group %s (session: %s)", group.Name, previousSessionID))
				mcpagent.CloseSession(previousSessionID)
			}
		}

		// Update batch context for the current group
		hcpo.currentGroupName = group.Name
		hcpo.currentGroupIdx = groupIndex
		hcpo.totalGroups = totalGroups

		// Also set batch context on context-aware bridge so ALL events get batch info in metadata
		if bridge := hcpo.GetContextAwareBridge(); bridge != nil {
			if batchBridge, ok := bridge.(interface {
				SetBatchContext(groupName string, groupIndex int, totalGroups int)
			}); ok {
				batchBridge.SetBatchContext(group.Name, groupIndex, totalGroups)
			}
		}

		groupStartTime := time.Now()

		err = hcpo.executePreparedRun(ctx, breakdownSteps, iteration, groupSetup)

		groupDuration := time.Since(groupStartTime)
		remainingGroups := totalGroups - groupIndex - 1

		if err != nil {
			hcpo.GetLogger().Error(fmt.Sprintf("❌ Batch execution: group %s failed: %v", group.Name, err), nil)
			result.FailedGroups++
			result.FailedGroupNames = append(result.FailedGroupNames, group.Name)
			if result.Error == "" {
				result.Error = err.Error() // Capture first failure reason
			}
			hcpo.finalizeRunMetadata(ctx, runFolder, "failed", groupStartTime, groupStartTime.Add(groupDuration))

			// Check if we should stop on first failure
			// For now, continue with other groups
			continue
		}

		hcpo.GetLogger().Info(fmt.Sprintf("✅ Batch execution: group %s completed successfully", group.Name))
		result.CompletedGroups++
		result.CompletedGroupNames = append(result.CompletedGroupNames, group.Name)
		// The target execution is complete now. Finalize its authoritative time
		// boundary so reviewers read status=completed with a completed_at.
		hcpo.finalizeRunMetadata(ctx, runFolder, "completed", groupStartTime, time.Now())

		// Report generation is no longer a post-group step — the dynamic report
		// (design doc §2) is a live frontend view, produced on demand by the user
		// opening the report panel.
		// Drain token persistence once the group is done, so cost records land
		// before the next group starts.
		if tokenErr := hcpo.waitForTokenPersistence(); tokenErr != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("Group finished, but cost persistence did not: %v", tokenErr))
		}

		// If single step mode was active, stop batch execution after this group
		// Single step mode should only run one group, not continue to additional groups
		if groupSetup.Context.RunSingleStepOnly && (hcpo.executionOptions == nil || hcpo.executionOptions.WebhookStepID == "") {
			hcpo.GetLogger().Info(fmt.Sprintf("🎯 Single step mode was active - stopping batch execution after group %s (skipping remaining %d group(s))", group.Name, remainingGroups))
			break
		}
	}

	result.Duration = time.Since(startTime)
	result.Success = result.FailedGroups == 0 && result.CanceledGroups == 0

	hcpo.ApplyWorkflowLogContext(hcpo.GetWorkspacePath(), "")

	// Clear batch context on context-aware bridge (cleanup after batch ends)
	if bridge := hcpo.GetContextAwareBridge(); bridge != nil {
		if batchBridge, ok := bridge.(interface {
			ClearBatchContext()
		}); ok {
			batchBridge.ClearBatchContext()
		}
	}

	if result.Success {
		hcpo.GetLogger().Info(fmt.Sprintf("✅ Batch execution completed: %d/%d groups succeeded in %v", result.CompletedGroups, totalGroups, result.Duration))
	} else {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Batch execution completed with issues: %d completed, %d failed, %d canceled", result.CompletedGroups, result.FailedGroups, result.CanceledGroups))
	}

	return result, nil
}

func (hcpo *StepBasedWorkflowOrchestrator) waitForTokenPersistence() error {
	cab, ok := hcpo.GetContextAwareBridge().(*orchestrator.ContextAwareEventBridge)
	if !ok {
		return nil
	}
	flushCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	return cab.WaitForTokenPersistence(flushCtx)
}

// determineBaseIterationFolder returns the current live slot. Rotation, when
// needed, happens before batch execution; callers never choose an iteration.
func (hcpo *StepBasedWorkflowOrchestrator) determineBaseIterationFolder() string {
	if opts := hcpo.GetExecutionOptions(); opts != nil && (opts.WebhookInputFile != "" || (opts.RunKind == "schedule" || opts.RunKind == "slack")) {
		return opts.SelectedRunFolder
	}
	return currentWorkflowRunFolder
}

// sanitizeNameForFolder sanitizes a group name for use in folder paths
// - Removes/replaces special characters that aren't safe for filesystem
// - Normalizes whitespace and converts to lowercase
// - Removes multiple consecutive dashes
// - Falls back to empty string if result is invalid
func (hcpo *StepBasedWorkflowOrchestrator) sanitizeNameForFolder(name string) string {
	if name == "" {
		return ""
	}

	sanitized := strings.TrimSpace(name)

	// Replace spaces with dashes
	sanitized = strings.ReplaceAll(sanitized, " ", "-")

	// Remove or replace special characters that aren't safe for folder names
	// Keep: letters, numbers, dashes, underscores
	// Remove: colons, slashes, backslashes, pipes, etc.
	specialCharPattern := regexp.MustCompile(`[^a-zA-Z0-9\-_]`)
	sanitized = specialCharPattern.ReplaceAllString(sanitized, "-")

	// Normalize multiple consecutive dashes to single dash
	multiDashPattern := regexp.MustCompile(`-+`)
	sanitized = multiDashPattern.ReplaceAllString(sanitized, "-")

	// Remove leading/trailing dashes
	sanitized = strings.Trim(sanitized, "-")

	// Convert to lowercase for consistency
	sanitized = strings.ToLower(sanitized)

	if sanitized == "" {
		return ""
	}

	return sanitized
}

// sanitizeDisplayNameForFolder is an alias for backward compatibility
func (hcpo *StepBasedWorkflowOrchestrator) sanitizeDisplayNameForFolder(name string) string {
	return hcpo.sanitizeNameForFolder(name)
}

// createGroupRunFolder creates the run folder path for a specific group
// Uses sanitized group name as folder name
// Named input sets retain iteration-X/group; an ungrouped invocation uses iteration-X.
func (hcpo *StepBasedWorkflowOrchestrator) createGroupRunFolder(baseIterationFolder, groupName string, totalGroups int) string {
	if groupName == "" {
		return baseIterationFolder
	}
	folderName := hcpo.sanitizeNameForFolder(groupName)
	if folderName == "" {
		folderName = groupName // fallback to raw name
	}
	return fmt.Sprintf("%s/%s", baseIterationFolder, folderName)
}

// Cancellation remains a user-visible terminal event; routine batch/group
// wrapper lifecycle events are not emitted.

func (hcpo *StepBasedWorkflowOrchestrator) emitBatchExecutionCanceledEvent(ctx context.Context, totalGroups, completedGroups int, canceledGroupName string, remainingGroupNames []string, reason string) {
	bridge := hcpo.GetContextAwareBridge()
	if bridge == nil {
		return
	}

	event := events.NewBatchExecutionCanceledEvent(totalGroups, completedGroups, canceledGroupName, remainingGroupNames, reason)
	bridge.HandleEvent(ctx, &baseevents.AgentEvent{
		Type:      events.BatchExecutionCanceled,
		Timestamp: time.Now(),
		Data:      event,
	})
}
