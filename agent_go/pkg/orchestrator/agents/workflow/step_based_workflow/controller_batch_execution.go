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

// getEnabledGroupsForExecution returns the list of groups to execute.
// Group selection is mandatory. Priority: ExecutionOptions.EnabledGroupNames > manifest enabled groups.
func (hcpo *StepBasedWorkflowOrchestrator) getEnabledGroupsForExecution() []VariableGroup {
	if hcpo.variablesManifest == nil {
		hcpo.GetLogger().Error("❌ [GROUP SELECTION] variablesManifest is nil - group configuration is required", nil)
		return nil
	}

	// Log available groups in manifest for debugging
	availableGroupNames := make([]string, len(hcpo.variablesManifest.Groups))
	for i, g := range hcpo.variablesManifest.Groups {
		availableGroupNames[i] = g.Name
	}
	hcpo.GetLogger().Info(fmt.Sprintf("🔍 [GROUP SELECTION] Available groups in manifest: %v", availableGroupNames))

	// Check if ExecutionOptions specifies specific group names
	if hcpo.executionOptions != nil && len(hcpo.executionOptions.EnabledGroupNames) > 0 {
		// Use specified group names from ExecutionOptions
		hcpo.GetLogger().Info(fmt.Sprintf("🔍 [GROUP SELECTION] Requested group names from execution options: %v", hcpo.executionOptions.EnabledGroupNames))
		var groups []VariableGroup
		var foundGroupNames []string
		var missingGroupNames []string

		for _, groupName := range hcpo.executionOptions.EnabledGroupNames {
			found := false
			for _, g := range hcpo.variablesManifest.Groups {
				if g.Name == groupName {
					groups = append(groups, g)
					foundGroupNames = append(foundGroupNames, groupName)
					hcpo.GetLogger().Info(fmt.Sprintf("✅ [GROUP SELECTION] Found group %s in manifest (enabled: %v)", groupName, g.Enabled))
					found = true
					break
				}
			}
			if !found {
				missingGroupNames = append(missingGroupNames, groupName)
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [GROUP SELECTION] Requested group %s not found in manifest", groupName))
			}
		}

		if len(missingGroupNames) > 0 {
			hcpo.GetLogger().Error(fmt.Sprintf("❌ [GROUP SELECTION] Some requested groups not found: %v (found: %v)", missingGroupNames, foundGroupNames), nil)
		}

		if len(groups) > 0 {
			returnedGroupNames := make([]string, len(groups))
			for i, g := range groups {
				returnedGroupNames[i] = g.Name
			}
			hcpo.GetLogger().Info(fmt.Sprintf("✅ [GROUP SELECTION] Returning %d groups: %v", len(groups), returnedGroupNames))
		} else {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [GROUP SELECTION] No groups found matching requested IDs"))
		}

		// If we found at least some groups, return them (even if some were missing)
		if len(groups) > 0 {
			return groups
		}
	}

	// Fall back to manifest's enabled groups only when the manifest defines real groups.
	if !hcpo.variablesManifest.HasGroups() {
		hcpo.GetLogger().Error("❌ [GROUP SELECTION] No groups defined in variables manifest - group configuration is required", nil)
		return nil
	}

	hcpo.GetLogger().Info(fmt.Sprintf("🔍 [GROUP SELECTION] No execution options or no matches found, using manifest's enabled groups"))
	enabledGroups := hcpo.variablesManifest.GetEnabledGroups()
	enabledGroupNames := make([]string, len(enabledGroups))
	for i, g := range enabledGroups {
		enabledGroupNames[i] = g.Name
	}
	hcpo.GetLogger().Info(fmt.Sprintf("✅ [GROUP SELECTION] Returning %d enabled groups from manifest: %v", len(enabledGroups), enabledGroupNames))
	return enabledGroups
}

// runBatchExecution executes the workflow for multiple variable groups sequentially
// Uses ExecutionManager for centralized cleanup and progress management
func (hcpo *StepBasedWorkflowOrchestrator) runBatchExecution(
	ctx context.Context,
	breakdownSteps []PlanStepInterface,
	iteration int,
	execCtx *ExecutionContext,
) (*BatchExecutionResult, error) {
	enabledGroups := hcpo.getEnabledGroupsForExecution()
	if opts := hcpo.GetExecutionOptions(); opts != nil && (opts.WebhookInputFile != "" || len(opts.WebhookVariables) > 0) {
		enabledGroups = applyWebhookVariableOverrides(enabledGroups, opts.WebhookVariables)
	}
	totalGroups := len(enabledGroups)

	if totalGroups == 0 {
		return nil, fmt.Errorf("no enabled variable groups found for batch execution")
	}

	// Validate that all groups have valid Names
	for i, group := range enabledGroups {
		if group.Name == "" {
			// PANIC for debugging: name is required for session ID and folder structure
			panic(fmt.Sprintf("CRITICAL: Group at index %d has empty Name in runBatchExecution() - all groups must have valid names. Group values: %v", i, group.Values))
		}
	}

	// Validate that returned groups match requested groups (if specified)
	if hcpo.executionOptions != nil && len(hcpo.executionOptions.EnabledGroupNames) > 0 {
		returnedGroupNames := make([]string, len(enabledGroups))
		for i, g := range enabledGroups {
			returnedGroupNames[i] = g.Name
		}
		requestedGroupNames := hcpo.executionOptions.EnabledGroupNames

		// Check if all requested groups are present
		requestedSet := make(map[string]bool)
		for _, id := range requestedGroupNames {
			requestedSet[id] = false
		}
		for _, id := range returnedGroupNames {
			if _, exists := requestedSet[id]; exists {
				requestedSet[id] = true
			}
		}

		missing := make([]string, 0)
		for id, found := range requestedSet {
			if !found {
				missing = append(missing, id)
			}
		}

		if len(missing) > 0 {
			hcpo.GetLogger().Error(fmt.Sprintf("❌ [BATCH EXECUTION] Requested groups not found in execution: %v (returned: %v)", missing, returnedGroupNames), nil)
		} else {
			hcpo.GetLogger().Info(fmt.Sprintf("✅ [BATCH EXECUTION] All requested groups found: %v", returnedGroupNames))
		}

		// Check for unexpected groups
		returnedSet := make(map[string]bool)
		for _, id := range returnedGroupNames {
			returnedSet[id] = true
		}
		unexpected := make([]string, 0)
		for _, id := range returnedGroupNames {
			found := false
			for _, reqID := range requestedGroupNames {
				if id == reqID {
					found = true
					break
				}
			}
			if !found {
				unexpected = append(unexpected, id)
			}
		}

		if len(unexpected) > 0 {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [BATCH EXECUTION] Unexpected groups in execution (not requested): %v", unexpected))
		}
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
// ALWAYS uses nested structure (iteration-X/group) regardless of number of groups
func (hcpo *StepBasedWorkflowOrchestrator) createGroupRunFolder(baseIterationFolder, groupName string, totalGroups int) string {
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
