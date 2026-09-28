package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// bindCrewRunner attaches the session's crew runner to the controller's
// execution options, creating minimal options when the session set none.
// Minimal options are behavior-neutral: every other options consumer
// nil-guards and matches on specific fields.
func bindCrewRunner(controller *StepBasedWorkflowOrchestrator, runner CrewStepRunner) {
	if controller == nil || runner == nil {
		return
	}
	if opts := controller.GetExecutionOptions(); opts != nil {
		opts.CrewRunner = runner
		return
	}
	controller.SetExecutionOptions(&ExecutionOptions{CrewRunner: runner})
}

// executeCrewStep invokes the Crew trigger named by a crew plan step and
// waits for the Crew run's final response. Declared context dependencies
// are read from the workflow run and sent as trigger input; the final
// response becomes the step result for downstream steps. The response file
// and run metadata are persisted by the caller-facing step-6 flow.
func (hcpo *StepBasedWorkflowOrchestrator) executeCrewStep(
	ctx context.Context,
	step PlanStepInterface,
	stepIndex int,
	progress *StepProgress,
	previousContextFiles []string,
	execCtx *ExecutionContext,
	allSteps []PlanStepInterface,
) (CrewStepResult, []string, error) {
	updatedContextFiles := previousContextFiles
	crewStep, ok := step.(*CrewPlanStep)
	if !ok {
		return CrewStepResult{}, updatedContextFiles, fmt.Errorf("step %d is not a CrewPlanStep", stepIndex+1)
	}
	if err := validateCrewStepFieldsTyped(crewStep); err != nil {
		return CrewStepResult{}, updatedContextFiles, err
	}
	opts := hcpo.GetExecutionOptions()
	if opts == nil || opts.CrewRunner == nil {
		return CrewStepResult{}, updatedContextFiles, fmt.Errorf(
			"crew step %q cannot run here: the current run path does not bind a Crew runner (crew steps need an authenticated run with crew access)", crewStep.GetID())
	}

	stepPath := fmt.Sprintf("step-%d", stepIndex+1)
	if execCtx.StepPathOverride != "" && execCtx.RunSingleStepOnly && stepIndex == execCtx.SingleStepTarget {
		stepPath = execCtx.StepPathOverride
	}
	hcpo.emitStepStartedEvent(ctx, step, stepIndex, stepPath)

	runWorkspacePath := fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
	executionWorkspacePath := fmt.Sprintf("%s/execution", runWorkspacePath)
	stepExecutionPath := getExecutionFolderPath(executionWorkspacePath, step.GetID(), stepPath)
	if err := hcpo.ensureStepExecutionFolderExists(ctx, stepExecutionPath); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to ensure crew step execution folder exists: %v (continuing - folder will be created when files are written)", err))
	}

	resolvedContextOutput := crewStep.GetContextOutput().String()
	if strings.TrimSpace(resolvedContextOutput) == "" {
		resolvedContextOutput = "response.md"
	}
	resolvedContextOutput = ResolveVariables(resolvedContextOutput, hcpo.variableValues)

	instruction := ResolveVariables(crewStep.Instruction, hcpo.variableValues)
	inputs, err := hcpo.readCrewStepInputs(ctx, crewStep, stepIndex, stepPath, allSteps, executionWorkspacePath)
	if err != nil {
		return CrewStepResult{}, updatedContextFiles, err
	}

	timeoutSeconds := crewStep.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = DefaultCrewStepTimeoutSeconds
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	hcpo.GetLogger().Info(fmt.Sprintf("📡 Crew step %d (%q) invoking trigger %q on project %q",
		stepIndex+1, crewStep.GetID(), crewStep.TriggerID, crewStep.CrewProjectID))
	result, err := opts.CrewRunner.RunCrewStep(runCtx, CrewStepRequest{
		WorkflowID:        hcpo.callerWorkflowID(ctx),
		WorkflowRunFolder: hcpo.selectedRunFolder,
		ExecutionID:       hcpo.bridgeExecutionID(),
		StepID:            crewStep.GetID(),
		Group:             hcpo.currentGroupName,
		ProfileID:         crewStep.CrewProfileID,
		ProjectID:         crewStep.CrewProjectID,
		TriggerID:         crewStep.TriggerID,
		Instruction:       instruction,
		Inputs:            inputs,
		TimeoutSeconds:    timeoutSeconds,
	})
	// The Crew run spent tokens even when the step failed, so attribute
	// usage whenever the run reports it. Cost persistence never fails the
	// step; a warning preserves the evidence without hiding the outcome.
	if persistErr := hcpo.persistCrewStepTokenUsage(ctx, hcpo.selectedRunFolder, crewStep.GetID(), stepIndex, hcpo.bridgeExecutionID(), result.Usage); persistErr != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to persist crew step token usage: %v", persistErr))
	}
	if err != nil {
		hcpo.emitStepFailedEvent(ctx, step, stepIndex, stepPath, err.Error())
		if recordErr := hcpo.writeCrewStepRunRecord(ctx, stepExecutionPath, crewStep, result); recordErr != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to write crew step run record: %v", recordErr))
		}
		return CrewStepResult{}, updatedContextFiles, fmt.Errorf("crew step %q failed: %w", crewStep.GetID(), err)
	}

	responseFilePath := filepath.Join(stepExecutionPath, resolvedContextOutput)
	if err := hcpo.WriteWorkspaceFile(ctx, responseFilePath, result.FinalResponse); err != nil {
		return CrewStepResult{}, updatedContextFiles, fmt.Errorf("crew step %q cannot save its response: %w", crewStep.GetID(), err)
	}
	if err := hcpo.writeCrewStepRunRecord(ctx, stepExecutionPath, crewStep, result); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to write crew step run record: %v", err))
	}

	// Crew responses are written directly by the runner, so they do not pass
	// through the regular step executor's pre-validation gate. Apply the same
	// declared schema here before the output is made available to consumers.
	if schema := crewStep.GetValidationSchema(); schema != nil && (len(schema.Files) > 0 || len(schema.DB) > 0) {
		validation, validationErr := RunPreValidation(ctx, schema, stepExecutionPath, hcpo.BaseOrchestrator)
		if validationErr != nil {
			validation = &WorkspaceVerificationResult{
				OverallPass: false,
				Summary: ValidationSummary{
					FailedChecks: 1,
					Errors:       []ValidationError{{CheckType: "pre_validation_error", Message: validationErr.Error()}},
				},
			}
		}
		hcpo.emitPreValidationCompletedEvent(ctx, step, stepIndex, stepPath, false, validation)
		SavePreValidationLog(ctx, hcpo.BaseOrchestrator, runWorkspacePath, crewStep.GetID(), stepPath,
			validation, schema, hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, hcpo.currentGroupName,
			PreValidationAttempt{ExecutionMode: "crew", ValidationPhase: "final-gate", ExecutionAttempt: 1, ValidationAttempt: 1})
		if !validation.OverallPass {
			reason := formatWorkspaceResults(validation)
			hcpo.emitStepFailedEvent(ctx, step, stepIndex, stepPath, reason)
			return CrewStepResult{}, updatedContextFiles, fmt.Errorf("crew step %q output failed validation: %s", crewStep.GetID(), reason)
		}
	}

	updatedContextFiles = append(updatedContextFiles, resolvedContextOutput)
	hcpo.emitStepFinishedEvent(ctx, step, stepIndex, stepPath)
	hcpo.addCompletedStepIndex(progress, stepIndex)
	if err := hcpo.saveStepProgress(ctx, progress); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to save step progress: %v", err))
	} else {
		hcpo.GetLogger().Info(fmt.Sprintf("💾 Saved progress: crew step %d marked as completed", stepIndex+1))
	}
	return result, updatedContextFiles, nil
}

// callerWorkflowID returns the workflow manifest ID for crew caller stamps.
// Triggers are bound to the manifest ID at creation; the controller's own
// workflowID is a random per-construction value used for human-feedback
// correlation and never matches the binding. Falls back to it only when
// the manifest is unreadable (tests, ad-hoc runs without a workflow).
func (hcpo *StepBasedWorkflowOrchestrator) callerWorkflowID(ctx context.Context) string {
	if content, err := hcpo.ReadWorkspaceFile(ctx, "workflow.json"); err == nil {
		var manifest struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(content), &manifest) == nil && strings.TrimSpace(manifest.ID) != "" {
			return strings.TrimSpace(manifest.ID)
		}
	}
	return hcpo.getWorkflowID()
}

// readCrewStepInputs resolves the step's context dependencies to run files
// and parses each into a trigger input value, keyed by file base name
// without extension. JSON files decode to structured values; anything else
// passes through as a string. A declared dependency that cannot be read
// fails the step: inputs are required, not best-effort.
func (hcpo *StepBasedWorkflowOrchestrator) readCrewStepInputs(
	ctx context.Context,
	crewStep *CrewPlanStep,
	stepIndex int,
	stepPath string,
	allSteps []PlanStepInterface,
	executionWorkspacePath string,
) (map[string]interface{}, error) {
	inputs := map[string]interface{}{}
	if len(crewStep.GetContextDependencies()) == 0 {
		return inputs, nil
	}
	resolvedDeps := ResolveVariablesArray(crewStep.GetContextDependencies(), hcpo.variableValues)
	docsRoot := GetPromptDocsRoot()
	resolved := hcpo.resolveDependencyPathsWithWorkspace(ctx, resolvedDeps, stepIndex, stepPath, allSteps, executionWorkspacePath, docsRoot, hcpo.variableValues)
	for i, depPath := range resolved {
		name := crewInputName(resolvedDeps[i])
		// ReadWorkspaceFile resolves both forms: producer-resolved
		// absolute paths normalize against the docs root, and relative
		// paths resolve against the workflow workspace.
		content, readErr := hcpo.ReadWorkspaceFile(ctx, depPath)
		if readErr != nil {
			return nil, fmt.Errorf(
				"crew input file not found: %s\n(produced by a prior step — check that the previous step completed successfully)", resolvedDeps[i])
		}
		var value interface{}
		if err := json.Unmarshal([]byte(content), &value); err != nil {
			value = content
		}
		inputs[name] = value
	}
	return inputs, nil
}

// crewStepRunRecord is the execution record for one crew step invocation,
// persisted as crew-run.json next to the response file.
type crewStepRunRecord struct {
	StepID        string                           `json:"step_id"`
	Group         string                           `json:"group,omitempty"`
	CrewProfileID string                           `json:"crew_profile_id"`
	CrewProjectID string                           `json:"crew_project_id"`
	TriggerID     string                           `json:"trigger_id"`
	CrewRunID     string                           `json:"crew_run_id"`
	SessionID     string                           `json:"session_id,omitempty"`
	Status        string                           `json:"status"`
	Error         string                           `json:"error,omitempty"`
	StartedAt     time.Time                        `json:"started_at"`
	CompletedAt   *time.Time                       `json:"completed_at,omitempty"`
	Usage         *workflowtypes.CrewRunTokenUsage `json:"usage,omitempty"`
	Timeline      []CrewPollTransition             `json:"timeline,omitempty"`
}

// persistCrewStepTokenUsage attributes the Crew run's token spend to the
// crew step in the workflow run's cost breakdown. Each model lands in the
// step's own execution_only bucket, exactly like a local step's spend; the
// store reprices from the rate card, so dollar amounts stay consistent
// with the rest of the breakdown while token counts stay exact. A nil or
// empty usage is a no-op: cost recording may lag or be disabled.
// bridgeExecutionID returns the run's immutable cost identity, or "" when
// no event bridge is bound (tests and ad-hoc runs fall back to the legacy
// run-folder bucket, which the same cost readers still include).
func (hcpo *StepBasedWorkflowOrchestrator) bridgeExecutionID() string {
	if cab, ok := hcpo.GetContextAwareBridge().(*orchestrator.ContextAwareEventBridge); ok && cab != nil {
		return cab.ExecutionID()
	}
	return ""
}

func (hcpo *StepBasedWorkflowOrchestrator) persistCrewStepTokenUsage(ctx context.Context, runFolder, stepID string, stepIndex int, executionID string, usage *workflowtypes.CrewRunTokenUsage) error {
	if usage == nil || usage.Empty() {
		return nil
	}
	modelIDs := make([]string, 0, len(usage.ByModel))
	for modelID := range usage.ByModel {
		modelIDs = append(modelIDs, modelID)
	}
	sort.Strings(modelIDs)
	if len(modelIDs) == 0 {
		modelIDs = []string{"unknown"}
	}
	for _, modelID := range modelIDs {
		model := usage.ByModel[modelID]
		provider := ""
		prompt, completion, reasoning, cacheRead, cacheWrite, calls := 0, 0, 0, 0, 0, 0
		if model == nil {
			prompt, completion, reasoning, cacheRead, cacheWrite, calls =
				usage.PromptTokens, usage.CompletionTokens, usage.ReasoningTokens,
				usage.CacheReadTokens, usage.CacheWriteTokens, usage.LLMCallCount
		} else {
			provider = model.Provider
			prompt, completion, reasoning, cacheRead, cacheWrite, calls =
				model.PromptTokens, model.CompletionTokens, model.ReasoningTokens,
				model.CacheReadTokens, model.CacheWriteTokens, model.LLMCallCount
		}
		stepData := &orchestrator.StepTokenData{
			Phase: "execution_only", Step: stepIndex + 1, StepID: stepID,
			InputTokens: prompt, OutputTokens: completion,
			CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite,
			ReasoningTokens: reasoning, LLMCallCount: calls,
			ExecutionID: executionID,
		}
		modelData := &orchestrator.ModelTokenData{
			ModelID: modelID, Provider: provider,
			InputTokens: prompt, OutputTokens: completion,
			CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite,
			ReasoningTokens: reasoning, LLMCallCount: calls,
		}
		if err := hcpo.PersistTokenUsage(ctx, runFolder, stepData, modelData); err != nil {
			return err
		}
	}
	return nil
}

// writeCrewStepRunRecord persists the operational metadata for a crew step
// invocation. It is a no-op when no Crew run exists (for example, access
// was revoked before dispatch), so failed pre-dispatch checks leave no
// misleading record behind.
func (hcpo *StepBasedWorkflowOrchestrator) writeCrewStepRunRecord(ctx context.Context, stepExecutionPath string, crewStep *CrewPlanStep, result CrewStepResult) error {
	if strings.TrimSpace(result.CrewRunID) == "" {
		return nil
	}
	record := crewStepRunRecord{
		StepID: crewStep.GetID(), Group: hcpo.currentGroupName,
		CrewProfileID: crewStep.CrewProfileID, CrewProjectID: crewStep.CrewProjectID,
		TriggerID: crewStep.TriggerID, CrewRunID: result.CrewRunID, SessionID: result.SessionID,
		Status: result.Status, Error: result.Error,
		StartedAt: result.StartedAt, CompletedAt: result.CompletedAt,
		Usage: result.Usage, Timeline: result.Timeline,
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode crew run record: %w", err)
	}
	return hcpo.WriteWorkspaceFile(ctx, filepath.Join(stepExecutionPath, "crew-run.json"), string(encoded))
}

// crewInputName keys a dependency by file base name without extension, so
// "prepare-review/pr.json" becomes the "pr" trigger input.
func crewInputName(dep string) string {
	base := filepath.Base(strings.TrimSpace(dep))
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if base == "" {
		return strings.TrimSpace(dep)
	}
	return base
}
