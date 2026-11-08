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
	Inputs              map[string]string
	ResolvedInputs      map[string]string
	GroupName           string                 // e.g., "production" — overrides session-level group
	Iteration           string                 // e.g., "iteration-3" — combined with group folder name to form RunFolder
	RunFolder           string                 // e.g., "iteration-3/xtech" — auto-calculated from Iteration + group, or set directly
	Instructions        string                 // Optional orchestrator instructions for inner steps — appended to step description as "## Orchestrator Instructions"
	HumanInput          string                 // Optional human input for top-level steps — injected as critical feedback in PreviousStepsSummary
	Tier                int                    // Optional LLM tier override (1=high, 2=medium, 3=low). 0 means no override.
	AgentRestart        bool                   // If true, archive any existing agent session and replay the configured item queue.
	ScriptParameters    map[string]interface{} // Validated named inputs for a scripted step, exposed through STEP_PARAMS_JSON.
	ScriptParametersSet bool                   // Distinguishes an omitted field from an explicitly supplied empty object.
	ExecutionID         string                 // Server-generated identity for this direct step execution.
}

// cleanupWorkshopExecutionPath removes a specific workshop execution folder and archives
// its matching logs folder. This is used for inner-step workshop runs where we need
// targeted cleanup without touching sibling or parent step artifacts.
func (hcpo *StepBasedWorkflowOrchestrator) cleanupWorkshopExecutionPath(ctx context.Context, stepPath string, stepID string, includeAgent bool) error {
	_ = includeAgent
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
	hcpo.observeRunReferences(stepID)
	releaseGroupSession := func() {}
	defer func() { releaseGroupSession() }()

	if err := hcpo.loadCodeLayout(ctx); err != nil {
		return "", err
	}
	if hcpo.isRelayExecution() && opts == nil {
		opts = &WorkshopExecuteOptions{}
	}

	// 1. Apply per-call overrides (group name, run_folder, human_input)
	if opts != nil {
		hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Per-call options: groupName=%s, runFolder=%s, humanInput=%d chars", opts.GroupName, opts.RunFolder, len(opts.HumanInput)))
		releaseFn, err := hcpo.applyWorkshopExecuteOptions(ctx, opts)
		if err != nil {
			releaseFn()
			return "", fmt.Errorf("failed to apply execute options: %w", err)
		}
		releaseGroupSession = releaseFn
	}
	if hcpo.selectedRunFolder == "" {
		return "", fmt.Errorf("no run folder selected for step %q; start the step again to create a run", stepID)
	}

	var execErr error

	// 1b. Ensure variable values are loaded (same as normal workflow's Execute method).
	// If group_name was passed, applyWorkshopExecuteOptions already loaded group values.
	// Otherwise, fall back to LoadVariableValues (reads from variables.json).
	if hcpo.isRelayExecution() {
		manifest, err := hcpo.inputDefinitions(ctx)
		if err != nil {
			return "", fmt.Errorf("load Flow configuration: %w", err)
		}
		hcpo.variablesManifest = manifest
		values, err := hcpo.relayVariableValues()
		if err != nil {
			return "", err
		}
		hcpo.variableValues = values
		SyncExactVariablesToWorkspaceEnv(hcpo.BaseOrchestrator, values)
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

	isInnerStep := stepInfo.TopIndex < 0
	isAgent := isAgentStep(stepInfo.Step)
	isAgentResume := isAgent && opts != nil && strings.TrimSpace(opts.HumanInput) != "" && !opts.AgentRestart
	isAgentStart := isAgent && !isAgentResume
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

	start := setup.StartFromStep
	if setup.Context.RunSingleStepOnly {
		start = setup.Context.SingleStepTarget
	}
	if err := validateRequiredLaunchVariables(breakdownSteps, hcpo.variableValues, hcpo.requiredLaunchRouteSelections(), start, setup.Context.RunSingleStepOnly); err != nil {
		return "", err
	}

	// For inner steps: skip top-level cleanup and route execution into the
	// owning Agent's nested artifact subtree.
	if isInnerStep {
		hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP-INNER] Original cleanup scope for inner step %q: CleanAll=%v, CleanFrom=%d, CleanSpecific=%d",
			stepID, setup.Cleanup.CleanAllSteps, setup.Cleanup.CleanFromStep, setup.Cleanup.CleanSpecificStep))
		setup.Cleanup = CleanupScope{} // No cleanup — don't delete other steps' outputs
		innerStepPath := resolveInnerStepPath(plan.Steps, stepInfo)
		setup.Context.StepPathOverride = innerStepPath
		if !isAgentResume {
			cleanAgentRuntime := isAgent && (opts == nil || !opts.AgentRestart)
			if err := hcpo.cleanupWorkshopExecutionPath(ctx, innerStepPath, stepInfo.Step.GetID(), cleanAgentRuntime); err != nil {
				return "", fmt.Errorf("failed to cleanup inner workshop step %q: %w", stepID, err)
			}
		} else {
			hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP-INNER] Agent %q resume: preserving existing session/output folder", stepID))
		}
		hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP-INNER] Inner step %q: skipping cleanup, using step path %q (target=%d, singleStep=%v)",
			stepID, innerStepPath, setup.Context.SingleStepTarget, setup.Context.RunSingleStepOnly))
	} else {
		if isAgentResume {
			setup.Cleanup = CleanupScope{}
			hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Agent %q resume: preserving existing session/output folder", stepID))
		}
		hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Top-level step %q: cleanup scope CleanAll=%v, CleanFrom=%d, CleanSpecific=%d",
			stepID, setup.Cleanup.CleanAllSteps, setup.Cleanup.CleanFromStep, setup.Cleanup.CleanSpecificStep))
	}

	if err := execManager.ApplyCleanup(ctx, setup); err != nil {
		return "", fmt.Errorf("failed to apply cleanup: %w", err)
	}

	execManager.ApplyExecutionContext(setup)

	// Thread builder-supplied human_input into the execution context so the
	// human branch can substitute it for the UI prompt without relying
	// on session-scoped state.
	if opts != nil && opts.HumanInput != "" {
		setup.Context.WorkshopHumanInput = opts.HumanInput
	}
	if opts != nil && opts.AgentRestart {
		setup.Context.AgentRestart = true
	} else if isAgentStart {
		// A plain execute_step on a agent means "start from beginning".
		// Resume requires human_input so we do not accidentally replay or append.
		setup.Context.AgentRestart = true
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
	execErr = hcpo.runExecutionPhase(ctx, breakdownSteps, 1, progress, setup.StartFromStep, setup.Context, &lastOutcome)
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

// applyWorkshopExecuteOptions pins the admitted inputs and execution identity.
func (hcpo *StepBasedWorkflowOrchestrator) applyWorkshopExecuteOptions(ctx context.Context, opts *WorkshopExecuteOptions) (func(), error) {
	releaseGroupSession := func() {}
	if hcpo.isRelayExecution() {
		if opts.GroupName != "" {
			return releaseGroupSession, fmt.Errorf("Flows do not accept variable groups")
		}
		if opts.RunFolder == "" {
			opts.RunFolder = "relay-" + workflowExecutionIDToken()
		}
		if err := validateRelayWorkshopRunFolder(opts.RunFolder); err != nil {
			return releaseGroupSession, err
		}
		hcpo.currentGroupName = ""
		hcpo.enabledGroupNames = nil
		hcpo.SetSelectedRunFolder(opts.RunFolder)
		return releaseGroupSession, nil
	}

	manifest, err := hcpo.inputDefinitions(ctx)
	if err != nil {
		return releaseGroupSession, err
	}
	hcpo.variablesManifest = manifest
	if opts.RunFolder == "" {
		opts.RunFolder = "run-" + workflowExecutionIDToken()
	}
	if err := ValidateWorkflowRunFolder(opts.RunFolder); err != nil {
		return releaseGroupSession, err
	}
	inputs := cloneWorkflowStringMap(opts.ResolvedInputs)
	if inputs == nil {
		inputs, err = ResolveWorkflowInputs(manifest, opts.GroupName, opts.Inputs)
	}
	if err != nil {
		return releaseGroupSession, err
	}
	hcpo.variableValues, err = hcpo.resolveRunInputs(ctx, opts.RunFolder, opts.GroupName, inputs)
	if err != nil {
		return releaseGroupSession, err
	}
	SyncExactVariablesToWorkspaceEnv(hcpo.BaseOrchestrator, hcpo.variableValues)
	hcpo.currentGroupName = ""
	hcpo.SetSelectedRunFolder(opts.RunFolder)
	// MCP/browser sessions follow the run identity, never the saved input label.
	return hcpo.switchWorkshopGroupSession(opts.RunFolder)
}

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
	case strings.HasPrefix(location, "route:"):
		routeID := strings.TrimPrefix(location, "route:")
		return todoSubAgentArtifactFolderName(parentID, parentRoot, routeID, "workshop", isScriptedStep(info.Step))
	default:
		return filepath.Join(parentRoot, "agents", workflowSafeIDPart(info.Step.GetID(), "nested"), "calls", "workshop")
	}
}
