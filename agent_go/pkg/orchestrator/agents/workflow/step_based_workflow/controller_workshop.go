package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// WorkshopExecuteOptions holds per-call overrides for ExecuteStepForWorkshop.
// When GroupName is set, the controller resolves the run folder and variable values
// for that group, making each execute_step call self-contained.
type WorkshopExecuteOptions struct {
	GroupID                string                 // Deprecated: use GroupName instead. Kept for backward compat; mapped to GroupName internally.
	GroupName              string                 // e.g., "production" — overrides session-level group
	Iteration              string                 // e.g., "iteration-3" — combined with group folder name to form RunFolder
	RunFolder              string                 // e.g., "iteration-3/xtech" — auto-calculated from Iteration + group, or set directly
	SavedScriptOnly        bool                   // If true, run only the saved learnings/{step-id}/main.py fast path with no LLM fallback
	AllowScriptRepair      bool                   // Builder execute_step only: an LLM may author or repair the step's script (PLAT-436)
	Instructions           string                 // Optional orchestrator instructions for inner steps — appended to step description as "## Orchestrator Instructions"
	HumanInput             string                 // Optional human input for top-level steps — injected as critical feedback in PreviousStepsSummary
	Tier                   int                    // Optional LLM tier override (1=high, 2=medium, 3=low). 0 means no override.
	MessageSequenceRestart bool                   // If true, archive any existing message_sequence session and replay the configured item queue.
	ScriptParameters       map[string]interface{} // Validated named inputs for a scripted step, exposed through STEP_PARAMS_JSON.
	ScriptParametersSet    bool                   // Distinguishes an omitted field from an explicitly supplied empty object.
	ExecutionID            string                 // Server-generated identity for this direct step execution.
}

// cleanupWorkshopExecutionPath removes a specific workshop execution folder and archives
// its matching logs folder. This is used for inner-step workshop runs where we need
// targeted cleanup without touching sibling or parent step artifacts.
func (hcpo *StepBasedWorkflowOrchestrator) cleanupWorkshopExecutionPath(ctx context.Context, stepPath string, stepID string, includeMessageSequence bool) error {
	_ = includeMessageSequence
	return hcpo.cleanupExecutionArtifactsForStepPath(ctx, stepPath, stepID)
}

// ExecuteStepForWorkshop executes a single step by its ID for the interactive workshop phase.
// It reuses the standard execution pipeline (PrepareExecution → ApplyCleanup → runExecutionPhase)
// so that step execution behaves identically to the normal "run single step" UI action.
//
// If opts is non-nil and opts.GroupName is set, the controller resolves the run folder and
// variable values for that group before execution. This allows each execute_step call to
// target a different group without changing session state.
func (hcpo *StepBasedWorkflowOrchestrator) ExecuteStepForWorkshop(
	ctx context.Context,
	stepID string,
	opts *WorkshopExecuteOptions,
) (string, error) {
	hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] ExecuteStepForWorkshop: stepID=%s, runFolder=%s, codeExec=%v",
		stepID, hcpo.selectedRunFolder, hcpo.GetUseCodeExecutionMode()))
	releaseGroupSession := func() {}
	defer func() { releaseGroupSession() }()

	// 1. Apply per-call overrides (group name, run_folder, human_input)
	if opts != nil {
		// Backward compat: map deprecated GroupID to GroupName
		if opts.GroupName == "" && opts.GroupID != "" {
			opts.GroupName = opts.GroupID
		}
		hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Per-call options: groupName=%s, runFolder=%s, humanInput=%d chars, savedScriptOnly=%v", opts.GroupName, opts.RunFolder, len(opts.HumanInput), opts.SavedScriptOnly))
		releaseFn, err := hcpo.applyWorkshopExecuteOptions(ctx, opts)
		if err != nil {
			releaseFn()
			return "", fmt.Errorf("failed to apply execute options: %w", err)
		}
		releaseGroupSession = releaseFn
	}
	if hcpo.selectedRunFolder == "" {
		return "", fmt.Errorf("no run folder selected; cannot execute step %q — pass group_name or run_folder in execute_step, or select a group first", stepID)
	}

	// 1b. Ensure variable values are loaded (same as normal workflow's Execute method).
	// If group_name was passed, applyWorkshopExecuteOptions already loaded group values.
	// Otherwise, fall back to LoadVariableValues (reads from variables.json).
	if hcpo.variableValues == nil {
		if hcpo.variablesManifest != nil && len(hcpo.variablesManifest.Groups) == 1 {
			// Single group — merge group overrides on top of manifest defaults
			g := hcpo.variablesManifest.Groups[0]
			merged := MergeGroupWithDefaults(hcpo.variablesManifest, g.Values)
			hcpo.variableValues = merged
			SyncVariablesToWorkspaceEnv(hcpo.BaseOrchestrator, merged)
			hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Auto-loaded variable values from single group %q (%d vars, %d after merge with defaults)", g.Name, len(g.Values), len(merged)))
		} else {
			vals, loadErr := LoadVariableValues(ctx, hcpo.BaseOrchestrator, hcpo.GetWorkspacePath(), hcpo.GetWorkspacePath())
			if loadErr != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("[WORKSHOP] Failed to load variable values: %v", loadErr))
			} else if vals != nil {
				hcpo.variableValues = vals
				SyncVariablesToWorkspaceEnv(hcpo.BaseOrchestrator, vals)
				hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Loaded %d variable values via fallback", len(vals)))
			}
		}
	}

	// 2. Re-read plan.json + populate runtime fields from step_config.json
	plan, err := hcpo.ReadCurrentPlan(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to load plan: %w", err)
	}
	ctx = withExecutionPlan(ctx, plan)
	stepConfigs, err := hcpo.ReadStepConfigs(ctx)
	if err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("[WORKSHOP] Failed to read step_config.json: %v (using defaults)", err))
		stepConfigs = []StepConfig{}
	}
	// Populate runtime fields for ALL steps (top-level + inner)
	allStepInfos := collectAllSteps(plan.Steps)
	for _, info := range allStepInfos {
		if err := populateRuntimeFields(info.Step, stepConfigs); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("[WORKSHOP] Failed to populate runtime fields: %v", err))
		}
	}

	// 3. Resolve step ID → find in top-level or inner steps
	stepInfo := findWorkshopStepByID(plan.Steps, stepID)
	if stepInfo == nil {
		allIDs := make([]string, 0, len(allStepInfos))
		for _, info := range allStepInfos {
			allIDs = append(allIDs, info.Step.GetID())
		}
		hcpo.GetLogger().Warn(fmt.Sprintf("[WORKSHOP] Step %q not found. Valid IDs: %v", stepID, allIDs))
		return "", fmt.Errorf("step with ID %q not found in plan", stepID)
	}
	ctx, err = applyWorkshopScriptParameters(ctx, stepInfo.Step, opts)
	if err != nil {
		return "", fmt.Errorf("invalid script_parameters for step %q: %w", stepID, err)
	}
	if opts != nil && opts.SavedScriptOnly {
		agentCfgs := getAgentConfigs(stepInfo.Step)
		if !isScriptedStep(stepInfo.Step, agentCfgs) {
			return "", fmt.Errorf("step %q is not in scripted code mode, so there is no saved main.py fast path to run", stepID)
		}
	}

	isInnerStep := stepInfo.TopIndex < 0
	isMessageSequence := isMessageSequenceStep(stepInfo.Step)
	isMessageSequenceResume := isMessageSequence && opts != nil && strings.TrimSpace(opts.HumanInput) != "" && !opts.MessageSequenceRestart
	isMessageSequenceStart := isMessageSequence && !isMessageSequenceResume
	if isInnerStep {
		hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Executing INNER step %q (parent=%s, location=%s, runFolder=%s)",
			stepID, stepInfo.ParentID, stepInfo.NestedLocation, hcpo.selectedRunFolder))
	}

	// For inner steps, we execute them standalone as a single-step plan.
	// For top-level steps, we use the normal execution pipeline.
	var breakdownSteps []PlanStepInterface
	var targetIndex int
	if isInnerStep {
		step := stepInfo.Step
		// Append orchestrator instructions to inner step description (simulates what the
		// parent todo_task/orchestration agent would provide via call_sub_agent).
		if opts != nil && opts.Instructions != "" {
			step = appendInstructionsToStep(step, opts.Instructions)
			hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Appended orchestrator instructions to inner step %q (%d chars)", stepID, len(opts.Instructions)))
		}
		breakdownSteps = []PlanStepInterface{step}
		targetIndex = 0
	} else {
		breakdownSteps = plan.Steps
		targetIndex = stepInfo.TopIndex - 1 // convert 1-based to 0-based
	}
	totalSteps := len(breakdownSteps)
	hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Executing step %q (index=%d, total=%d, inner=%v, runFolder=%s)",
		stepID, targetIndex, totalSteps, isInnerStep, hcpo.selectedRunFolder))

	// 4. Ensure run folder exists
	fullRunFolderPath := fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
	if err := hcpo.createRunFolderStructure(ctx, fullRunFolderPath); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("[WORKSHOP] Failed to create run folder structure: %v (continuing)", err))
	}
	if executionOpts := hcpo.GetExecutionOptions(); executionOpts != nil && (executionOpts.RunKind == "schedule" || executionOpts.RunKind == "slack") {
		executionID := ""
		if opts != nil {
			executionID = opts.ExecutionID
		}
		if err := hcpo.markRunMetadataStartedForExecution(ctx, hcpo.selectedRunFolder, executionID); err != nil {
			return "", fmt.Errorf("bind scheduled step run identity: %w", err)
		}
	}

	// 5. Standard execution pipeline — same as normal "run single step" UI action
	execOpts := &ExecutionOptions{
		ExecutionStrategy: ExecutionStrategyRunSingleStep,
		ResumeFromStep:    targetIndex + 1, // 1-based
	}

	execManager := NewExecutionManager(hcpo)

	// Load or initialize progress
	progress, _ := hcpo.loadStepProgress(ctx)
	if progress == nil {
		progress = &StepProgress{
			TotalSteps:           totalSteps,
			CompletedStepIndices: []int{},
		}
	}

	setup, err := execManager.PrepareExecution(ctx, execOpts, progress, totalSteps, hcpo.selectedRunFolder)
	if err != nil {
		return "", fmt.Errorf("failed to prepare execution: %w", err)
	}
	if opts != nil && opts.SavedScriptOnly {
		setup.Context.SavedScriptOnly = true
	}
	// A scheduled, Slack or webhook invocation is a run even when it reaches a
	// step through execute_step: it never repairs scripts.
	if opts != nil && opts.AllowScriptRepair && !opts.SavedScriptOnly {
		if runOpts := hcpo.GetExecutionOptions(); runOpts == nil || runOpts.RunKind == "" {
			setup.Context.AllowScriptRepair = true
		}
	}

	// For inner steps: skip top-level cleanup and route execution into the
	// owning Agent's nested artifact subtree.
	if isInnerStep {
		hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP-INNER] Original cleanup scope for inner step %q: CleanAll=%v, CleanFrom=%d, CleanSpecific=%d",
			stepID, setup.Cleanup.CleanAllSteps, setup.Cleanup.CleanFromStep, setup.Cleanup.CleanSpecificStep))
		setup.Cleanup = CleanupScope{} // No cleanup — don't delete other steps' outputs
		innerStepPath := resolveInnerStepPath(plan.Steps, stepInfo)
		setup.Context.StepPathOverride = innerStepPath
		if !isMessageSequenceResume {
			cleanMessageSequenceRuntime := isMessageSequence && (opts == nil || !opts.MessageSequenceRestart)
			if err := hcpo.cleanupWorkshopExecutionPath(ctx, innerStepPath, stepInfo.Step.GetID(), cleanMessageSequenceRuntime); err != nil {
				return "", fmt.Errorf("failed to cleanup inner workshop step %q: %w", stepID, err)
			}
		} else {
			hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP-INNER] Message sequence %q resume: preserving existing session/output folder", stepID))
		}
		hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP-INNER] Inner step %q: skipping cleanup, using step path %q (target=%d, singleStep=%v)",
			stepID, innerStepPath, setup.Context.SingleStepTarget, setup.Context.RunSingleStepOnly))
	} else {
		if isMessageSequenceResume {
			setup.Cleanup = CleanupScope{}
			hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Message sequence %q resume: preserving existing session/output folder", stepID))
		}
		hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Top-level step %q: cleanup scope CleanAll=%v, CleanFrom=%d, CleanSpecific=%d",
			stepID, setup.Cleanup.CleanAllSteps, setup.Cleanup.CleanFromStep, setup.Cleanup.CleanSpecificStep))
	}

	if err := execManager.ApplyCleanup(ctx, setup); err != nil {
		return "", fmt.Errorf("failed to apply cleanup: %w", err)
	}

	execManager.ApplyExecutionContext(setup)

	// Thread builder-supplied human_input into the execution context so the
	// human_input controller can substitute it for the UI prompt without relying
	// on session-scoped state.
	if opts != nil && opts.HumanInput != "" {
		setup.Context.WorkshopHumanInput = opts.HumanInput
	}
	if opts != nil && opts.MessageSequenceRestart {
		setup.Context.MessageSequenceRestart = true
	} else if isMessageSequenceStart {
		// A plain execute_step on a message_sequence means "start from beginning".
		// Resume requires human_input so we do not accidentally replay or append.
		setup.Context.MessageSequenceRestart = true
	}

	// Reload progress after cleanup
	progress, err = hcpo.loadStepProgress(ctx)
	if err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("[WORKSHOP] Failed to reload progress after cleanup, using in-memory: %v", err))
		progress = &StepProgress{
			CompletedStepIndices: make([]int, 0),
			TotalSteps:           totalSteps,
		}
	}

	// 6. Run via the standard execution pipeline.
	retryStarted := time.Now().UTC()
	retryFolder, retryExecutionID, retryRevision := hcpo.selectedRunFolder, "", ""
	if !isInnerStep {
		if raw, err := hcpo.ReadWorkspaceFile(ctx, workflowRunMetadataPath(retryFolder)); err == nil {
			var meta map[string]interface{}
			if json.Unmarshal([]byte(raw), &meta) == nil && meta["status"] == "failed" {
				retryExecutionID, _ = meta["execution_id"].(string)
				if retryExecutionID != "" {
					retryRevision, _ = hcpo.ensureExecutablePlanRevision(ctx)
				}
			}
		}
	}
	var lastOutcome LastExecutedStepOutcome
	execErr := hcpo.runExecutionPhase(ctx, breakdownSteps, 1, progress, setup.StartFromStep, setup.Context, &lastOutcome)
	if execErr == nil && lastOutcome.Found && lastOutcome.StepID == stepID && hcpo.selectedRunFolder == retryFolder {
		if err := hcpo.recordWorkshopRetryRecovery(ctx, retryFolder, retryExecutionID, retryRevision, stepID, totalSteps, retryStarted); err != nil {
			execErr = fmt.Errorf("step %q succeeded but run recovery evidence could not be persisted: %w", stepID, err)
		}
	}

	// 7. Prefer the exact result this call just produced over a log-folder
	// re-read: a re-read cannot distinguish this call's own file from one an
	// unrelated, concurrent dispatch of the same step (a different chat
	// session or a scheduled run — nothing serializes them against this
	// path) wrote to the same folder in the meantime (PLAT-182 review). The
	// log-based paths remain as a fallback for step kinds that never
	// populate an in-memory result this way (routing, todo_task) and for
	// the inner-step-override addressing scheme, which this change leaves
	// untouched.
	result := ""
	if execErr == nil {
		loaded := false
		if isInnerStep && setup.Context.StepPathOverride != "" {
			// For inner steps, read from the overridden step path
			if r, ok := hcpo.loadStepResultFromLogsByPath(ctx, stepInfo.Step.GetID(), setup.Context.StepPathOverride); ok {
				result = r
				loaded = true
			}
		} else if lastOutcome.Found && lastOutcome.StepIndex == targetIndex {
			result = lastOutcome.ExecutionResult
			loaded = true
		}
		if !loaded {
			if r, ok := hcpo.loadSingleStepResultFromLogs(ctx, targetIndex+1); ok {
				result = r
			} else {
				result = fmt.Sprintf("Step %q completed successfully", stepID)
			}
		}
	}

	if execErr != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("[WORKSHOP] Step %q failed: %v", stepID, execErr))
	} else {
		hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Step %q completed (result len=%d)", stepID, len(result)))
	}

	// Reset single step mode so subsequent calls don't inherit it.
	// WorkshopHumanInput is scoped via ExecutionContext, so no session-level reset needed.
	hcpo.SetRunSingleStepMode(false, -1)

	return result, execErr
}

// applyWorkshopExecuteOptions applies per-call group/run_folder overrides.
// If GroupName is set, it resolves the run folder from the group and loads
// the group's variable values. If only RunFolder is set, it uses that directly.
func (hcpo *StepBasedWorkflowOrchestrator) applyWorkshopExecuteOptions(ctx context.Context, opts *WorkshopExecuteOptions) (func(), error) {
	releaseGroupSession := func() {}

	// Backward compat: map deprecated GroupID to GroupName
	if opts.GroupName == "" && opts.GroupID != "" {
		opts.GroupName = opts.GroupID
	}
	if opts.GroupName != "" {
		hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] applyWorkshopExecuteOptions: groupName=%s, manifestLoaded=%v", opts.GroupName, hcpo.variablesManifest != nil))
		resolvedGroupName := opts.GroupName

		// Reload manifest from disk if not loaded (can happen on cached sessions)
		if hcpo.variablesManifest == nil {
			variablesPath := fmt.Sprintf("%s/variables/variables.json", hcpo.GetWorkspacePath())
			_, manifest, err := hcpo.variableManager.checkExistingVariables(ctx, variablesPath)
			if err != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("[WORKSHOP] Failed to reload variables manifest: %v", err))
			} else if manifest != nil {
				hcpo.variablesManifest = manifest
				hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Reloaded variables manifest from disk (%d groups, %d vars)", len(manifest.Groups), len(manifest.Variables)))
			}
		}

		// Resolve variable values for this group.
		// Try direct group name match first, then fall back to matching by sanitized name
		// (agents often pass the folder name which is derived from the group name).
		if hcpo.variablesManifest != nil {
			groupValues := hcpo.variablesManifest.GetVariableValues(opts.GroupName)
			if groupValues == nil {
				// Try matching by sanitized name (folder name derivation)
				for _, g := range hcpo.variablesManifest.Groups {
					if g.Name != "" {
						sanitized := hcpo.sanitizeDisplayNameForFolder(g.Name)
						if sanitized == opts.GroupName {
							groupValues = g.Values
							resolvedGroupName = g.Name
							hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Resolved group %q by sanitized name to %q", opts.GroupName, resolvedGroupName))
							break
						}
					}
				}
			}
			if groupValues != nil {
				merged := MergeGroupWithDefaults(hcpo.variablesManifest, groupValues)
				hcpo.variableValues = merged
				SyncVariablesToWorkspaceEnv(hcpo.BaseOrchestrator, merged)
				hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Loaded %d variable values for group %s (resolved=%s, %d after merge with defaults): %v", len(groupValues), opts.GroupName, resolvedGroupName, len(merged), merged))
			} else {
				// Group not found — return a clear error so the agent asks the user for the correct group name.
				var groupDescs []string
				for _, g := range hcpo.variablesManifest.Groups {
					groupDescs = append(groupDescs, g.Name)
				}
				return releaseGroupSession, fmt.Errorf("group %q not found. Available groups: %s — ask the user which group to use", opts.GroupName, strings.Join(groupDescs, ", "))
			}
		} else {
			hcpo.GetLogger().Warn(fmt.Sprintf("[WORKSHOP] Cannot resolve group %q — variables manifest is nil even after reload attempt", opts.GroupName))
		}

		releaseFn, err := hcpo.switchWorkshopGroupSession(resolvedGroupName)
		if err != nil {
			return releaseGroupSession, fmt.Errorf("failed to switch workshop MCP session for group %q: %w", resolvedGroupName, err)
		}
		releaseGroupSession = releaseFn
		hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Active MCP session for group %s: %s", resolvedGroupName, hcpo.GetMCPSessionID()))

		// Propagate the group into the run context. Crew steps key their
		// delivery idempotency on the current group; without this every
		// workshop execute_step shares one group-less key, so a second
		// group's step adopts the first group's success instead of
		// invoking its trigger fresh. Restored on release so group
		// context never leaks between execute_step calls.
		prevGroupName := hcpo.currentGroupName
		hcpo.currentGroupName = resolvedGroupName
		prevRelease := releaseGroupSession
		releaseGroupSession = func() {
			hcpo.currentGroupName = prevGroupName
			prevRelease()
		}

		// Resolve run folder if not explicitly provided
		if opts.RunFolder == "" {
			// Build run folder from group: use latest iteration + group folder name
			runFolder, err := hcpo.resolveGroupRunFolder(ctx, opts.GroupName)
			if err != nil {
				return releaseGroupSession, fmt.Errorf("failed to resolve run folder for group %q: %w", opts.GroupName, err)
			}
			opts.RunFolder = runFolder
		}
	}

	if opts.RunFolder != "" {
		hcpo.SetSelectedRunFolder(opts.RunFolder)
		hcpo.GetLogger().Debug(fmt.Sprintf("[WORKSHOP_DEBUG] Set run folder to %s", opts.RunFolder))
	}

	return releaseGroupSession, nil
}

// resolveGroupRunFolder finds the run folder for a given group name.
// It looks for existing iteration folders that contain a subfolder matching the group.
// Falls back to creating a path using the latest iteration + sanitized group name.
func (hcpo *StepBasedWorkflowOrchestrator) resolveGroupRunFolder(ctx context.Context, groupName string) (string, error) {
	workspacePath := hcpo.GetWorkspacePath()
	runsPath := fmt.Sprintf("%s/runs", workspacePath)

	// Derive folder name from group name
	groupFolderName := groupName
	if hcpo.variablesManifest != nil {
		for _, g := range hcpo.variablesManifest.Groups {
			if g.Name == groupName {
				sanitized := hcpo.sanitizeDisplayNameForFolder(g.Name)
				if sanitized != "" {
					groupFolderName = sanitized
				}
				break
			}
		}
	}

	// List existing iteration folders and find the latest one that contains this group
	existingFolders, err := hcpo.listRunFolders(ctx, runsPath)
	if err != nil || len(existingFolders) == 0 {
		// No runs exist — use iteration-1
		runFolder := fmt.Sprintf("iteration-1/%s", groupFolderName)
		hcpo.GetLogger().Debug(fmt.Sprintf("[WORKSHOP_DEBUG] resolveGroupRunFolder: no existing runs, using %s", runFolder))
		return runFolder, nil
	}

	// Check existing folders in reverse order (latest first) for a matching group subfolder
	for i := len(existingFolders) - 1; i >= 0; i-- {
		iterFolder := existingFolders[i]
		candidatePath := fmt.Sprintf("%s/%s/%s", runsPath, iterFolder, groupFolderName)
		if hcpo.workspaceFileExists(ctx, candidatePath) {
			runFolder := fmt.Sprintf("%s/%s", iterFolder, groupFolderName)
			hcpo.GetLogger().Debug(fmt.Sprintf("[WORKSHOP_DEBUG] resolveGroupRunFolder: found existing %s", runFolder))
			return runFolder, nil
		}
	}

	// No existing group folder found — use the latest iteration
	latestIter := existingFolders[len(existingFolders)-1]
	runFolder := fmt.Sprintf("%s/%s", latestIter, groupFolderName)
	hcpo.GetLogger().Debug(fmt.Sprintf("[WORKSHOP_DEBUG] resolveGroupRunFolder: using latest iteration %s", runFolder))
	return runFolder, nil
}

// appendInstructionsToStep creates a shallow copy of the step with orchestrator instructions
// appended to its description. This mirrors what executePredefinedSubAgent does when the
// todo_task orchestrator delegates to a sub-agent via call_sub_agent.
func appendInstructionsToStep(step PlanStepInterface, instructions string) PlanStepInterface {
	if instructions == "" {
		return step
	}

	updatedStep, err := cloneStepWithDelegationOverrides(step, instructions)
	if err != nil {
		return step
	}
	return updatedStep
}

// loadStepResultFromLogsByPath reads the latest execution result using the same
// stable step identity as the runtime writer. stepPath remains structural context.
func (hcpo *StepBasedWorkflowOrchestrator) loadStepResultFromLogsByPath(ctx context.Context, stepID, stepPath string) (string, bool) {
	var validationWorkspacePath string
	if hcpo.selectedRunFolder != "" {
		validationWorkspacePath = fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
	} else {
		validationWorkspacePath = hcpo.GetWorkspacePath()
	}

	executionLogsFolderPath := getExecutionFolderPathForLogs(validationWorkspacePath, stepID, stepPath)
	var latestResult string
	var latestAttempt, latestIteration int

	for attempt := 1; attempt <= 10; attempt++ {
		for iteration := 0; iteration <= 10; iteration++ {
			filePath := fmt.Sprintf("%s/execution-attempt-%d-iteration-%d.json", executionLogsFolderPath, attempt, iteration)
			content, err := hcpo.ReadWorkspaceFile(ctx, filePath)
			if err != nil {
				continue
			}
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(content), &data); err != nil {
				continue
			}
			if execResult, ok := data["execution_result"].(string); ok {
				if attempt > latestAttempt || (attempt == latestAttempt && iteration > latestIteration) {
					latestResult = execResult
					latestAttempt = attempt
					latestIteration = iteration
				}
			}
		}
	}

	if latestResult != "" {
		hcpo.GetLogger().Info(fmt.Sprintf("Loaded execution result from logs for %s (attempt %d, iteration %d)", stepPath, latestAttempt, latestIteration))
		return latestResult, true
	}

	logsRoot := fmt.Sprintf("%s/logs", validationWorkspacePath)
	logFolders, err := hcpo.BaseOrchestrator.ListWorkspaceFiles(ctx, logsRoot)
	if err == nil {
		for _, folderName := range logFolders {
			for attempt := 1; attempt <= 10; attempt++ {
				for iteration := 0; iteration <= 10; iteration++ {
					filePath := fmt.Sprintf("%s/%s/execution/execution-attempt-%d-iteration-%d.json", logsRoot, folderName, attempt, iteration)
					content, readErr := hcpo.ReadWorkspaceFile(ctx, filePath)
					if readErr != nil {
						continue
					}
					var data map[string]interface{}
					if err := json.Unmarshal([]byte(content), &data); err != nil {
						continue
					}
					recordedStepPath, _ := data["step_path"].(string)
					execResult, _ := data["execution_result"].(string)
					if recordedStepPath != stepPath || execResult == "" {
						continue
					}
					if attempt > latestAttempt || (attempt == latestAttempt && iteration > latestIteration) {
						latestResult = execResult
						latestAttempt = attempt
						latestIteration = iteration
					}
				}
			}
		}
	}

	if latestResult != "" {
		hcpo.GetLogger().Info(fmt.Sprintf("Loaded execution result from scanned logs for %s (attempt %d, iteration %d)", stepPath, latestAttempt, latestIteration))
		return latestResult, true
	}
	return "", false
}

// resolveInnerStepPath computes a deterministic Workshop call directory inside
// the owning Agent subtree. Normal runtime calls use their execution id in the
// final segment; Workshop uses the reserved "workshop" call id.
func resolveInnerStepPath(topLevelSteps []PlanStepInterface, info *WorkshopStepInfo) string {
	parentID := ""
	for _, s := range topLevelSteps {
		if s.GetID() == info.ParentID {
			parentID = s.GetID()
			break
		}
	}
	if parentID == "" {
		parentID = info.ParentID
	}
	parentRoot := parentAgentArtifactRoot(parentID, parentID)

	location := info.NestedLocation
	switch {
	case location == "todo_task_step":
		return filepath.Join(parentRoot, "agents", workflowSafeIDPart(info.Step.GetID(), "nested"), "calls", "workshop")
	case strings.HasPrefix(location, "route:"):
		routeID := strings.TrimPrefix(location, "route:")
		return todoSubAgentArtifactFolderName(parentID, parentRoot, routeID, "workshop", isScriptedStep(info.Step, getAgentConfigs(info.Step)))
	default:
		return filepath.Join(parentRoot, "agents", workflowSafeIDPart(info.Step.GetID(), "nested"), "calls", "workshop")
	}
}
