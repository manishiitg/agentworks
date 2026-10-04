package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const workflowContinuationRecoveryScanLimit = 200

type WorkflowContinuationRecoverySummary struct {
	Scanned int
	Queued  int
	Skipped int
	Errors  []string
}

type workflowContinuationStateFile struct {
	Path  string
	State *WorkflowContinuationState
}

type workflowContinuationStepRuntime struct {
	Step                   PlanStepInterface
	StepIndex              int
	StepPath               string
	LearningPathIdentifier string
	TotalSteps             int
	ExecutionHistory       []llmtypes.MessageContent
	ValidationResponse     *ValidationResponse
	TurnCount              int
	ExecutionLLM           string
	IsCodeExecutionMode    bool
}

func (s *WorkshopChatSession) RecoverPendingContinuations(ctx context.Context) WorkflowContinuationRecoverySummary {
	if s == nil || s.controller == nil {
		return WorkflowContinuationRecoverySummary{}
	}
	return s.controller.recoverPendingWorkflowContinuations(ctx)
}

func (hcpo *StepBasedWorkflowOrchestrator) recoverPendingWorkflowContinuations(ctx context.Context) WorkflowContinuationRecoverySummary {
	if ctx == nil {
		ctx = context.Background()
	}
	summary := WorkflowContinuationRecoverySummary{}
	if hcpo == nil {
		return summary
	}

	stateFiles := hcpo.discoverWorkflowContinuationStateFiles(ctx)
	summary.Scanned = len(stateFiles)
	for _, stateFile := range stateFiles {
		if ctx.Err() != nil {
			summary.Errors = append(summary.Errors, ctx.Err().Error())
			return summary
		}
		queued, err := hcpo.queueWorkflowContinuationRecovery(ctx, stateFile.State)
		if err != nil {
			summary.Errors = append(summary.Errors, fmt.Sprintf("%s: %v", stateFile.Path, err))
			continue
		}
		if queued == 0 {
			summary.Skipped++
			continue
		}
		summary.Queued += queued
	}
	if summary.Queued > 0 || len(summary.Errors) > 0 {
		hcpo.GetLogger().Info(fmt.Sprintf("🔁 Workflow continuation recovery scanned=%d queued=%d skipped=%d errors=%d", summary.Scanned, summary.Queued, summary.Skipped, len(summary.Errors)))
	}
	return summary
}

func (hcpo *StepBasedWorkflowOrchestrator) discoverWorkflowContinuationStateFiles(ctx context.Context) []workflowContinuationStateFile {
	workspacePath := strings.TrimSpace(hcpo.GetWorkspacePath())
	if workspacePath == "" {
		return nil
	}
	seen := make(map[string]struct{})
	stateFiles := make([]workflowContinuationStateFile, 0)
	appendRunFolder := func(runFolder string) {
		if len(stateFiles) >= workflowContinuationRecoveryScanLimit {
			return
		}
		hcpo.appendWorkflowContinuationStateFiles(ctx, strings.TrimSpace(runFolder), seen, &stateFiles)
	}

	if strings.TrimSpace(hcpo.selectedRunFolder) != "" {
		appendRunFolder(hcpo.selectedRunFolder)
	}

	runsRoot := filepath.Join(workspacePath, "runs")
	runDirs, _ := hcpo.ListWorkspaceDirectories(ctx, runsRoot)
	sort.Strings(runDirs)
	sort.Sort(sort.Reverse(sort.StringSlice(runDirs)))
	for _, runDir := range runDirs {
		appendRunFolder(runDir)
		childRoot := filepath.Join(runsRoot, runDir)
		childDirs, _ := hcpo.ListWorkspaceDirectories(ctx, childRoot)
		sort.Strings(childDirs)
		sort.Sort(sort.Reverse(sort.StringSlice(childDirs)))
		for _, childDir := range childDirs {
			appendRunFolder(filepath.Join(runDir, childDir))
		}
	}
	return stateFiles
}

func (hcpo *StepBasedWorkflowOrchestrator) appendWorkflowContinuationStateFiles(ctx context.Context, runFolder string, seen map[string]struct{}, out *[]workflowContinuationStateFile) {
	if runFolder == "" || len(*out) >= workflowContinuationRecoveryScanLimit {
		return
	}
	logsPath := filepath.Join(hcpo.GetWorkspacePath(), "runs", runFolder, "logs")
	stepDirs, _ := hcpo.ListWorkspaceDirectories(ctx, logsPath)
	sort.Strings(stepDirs)
	sort.Sort(sort.Reverse(sort.StringSlice(stepDirs)))
	for _, stepDir := range stepDirs {
		if len(*out) >= workflowContinuationRecoveryScanLimit {
			return
		}
		statePath := filepath.Join(logsPath, stepDir, workflowContinuationStateFilename)
		if _, ok := seen[statePath]; ok {
			continue
		}
		seen[statePath] = struct{}{}
		content, err := hcpo.ReadWorkspaceFile(ctx, statePath)
		if err != nil || strings.TrimSpace(content) == "" {
			continue
		}
		var state WorkflowContinuationState
		if err := json.Unmarshal([]byte(content), &state); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to parse workflow continuation state %s: %v", statePath, err))
			continue
		}
		if strings.TrimSpace(state.RunFolder) == "" {
			state.RunFolder = runFolder
		}
		if strings.TrimSpace(state.StepID) == "" {
			state.StepID = stepDir
		}
		*out = append(*out, workflowContinuationStateFile{Path: statePath, State: &state})
	}
}

func (hcpo *StepBasedWorkflowOrchestrator) queueWorkflowContinuationRecovery(ctx context.Context, state *WorkflowContinuationState) (int, error) {
	if state == nil {
		return 0, nil
	}
	pendingPhases := state.PendingRecoveryPhases()
	if len(pendingPhases) == 0 {
		return 0, nil
	}
	runtime, err := hcpo.buildWorkflowContinuationStepRuntime(ctx, state)
	if err != nil {
		return 0, err
	}
	queued := 0
	hcpo.withWorkflowContinuationRunFolder(state.RunFolder, func() {
		for _, phase := range pendingPhases {
			switch phase {
			case workflowContinuationPhaseLearningAgent:
				// Agent-mode post-step learning is retired (see
				// controller_execution.go). Any
				// pending learning-agent phase recovered from disk is marked
				// skipped — direct-mode learning happens inline in the step
				// agent's own post-completion turn and has its own recovery
				// path (workflowContinuationPhaseDirectLearning below).
				hcpo.recordWorkflowContinuationPhaseForRunFolder(context.Background(), state.RunFolder, state.StepID, state.StepPath, workflowContinuationOwnerStepExecution, phase, workflowContinuationStatusSkipped, "Agent-mode learning retired; direct mode handles writes", nil)
			case workflowContinuationPhaseKBUpdateAgent:
				// Agent-mode post-step KB update is retired (see
				// controller_execution.go). Any pending kb_update_agent phase
				// recovered from an older run's state file is marked skipped — direct
				// mode writes notes/ inline in the step agent's own turn and recovers
				// through workflowContinuationPhaseKBReview below.
				hcpo.recordWorkflowContinuationPhaseForRunFolder(context.Background(), state.RunFolder, state.StepID, state.StepPath, workflowContinuationOwnerStepExecution, phase, workflowContinuationStatusSkipped, "Agent-mode KB update retired; direct mode handles writes", nil)
			case workflowContinuationPhaseKBReview:
				hcpo.recordWorkflowContinuationPhaseForRunFolder(context.Background(), state.RunFolder, state.StepID, state.StepPath, workflowContinuationOwnerStepExecution, phase, workflowContinuationStatusRecoveryQueued, "", nil)
				hcpo.queueRecoveredDirectKBReview(state, runtime, workflowContinuationPhaseHandle(state, phase))
				queued++
			case workflowContinuationPhaseDirectLearning:
				hcpo.recordWorkflowContinuationPhaseForRunFolder(context.Background(), state.RunFolder, state.StepID, state.StepPath, workflowContinuationOwnerStepExecution, phase, workflowContinuationStatusRecoveryQueued, "", nil)
				hcpo.queueRecoveredDirectLearning(state, runtime, workflowContinuationPhaseHandle(state, phase))
				queued++
			}
		}
	})
	return queued, nil
}

func (hcpo *StepBasedWorkflowOrchestrator) buildWorkflowContinuationStepRuntime(ctx context.Context, state *WorkflowContinuationState) (*workflowContinuationStepRuntime, error) {
	plan, err := hcpo.readRunPlanSnapshot(context.WithoutCancel(ctx), state.RunFolder)
	if err != nil {
		return nil, fmt.Errorf("read continuation run plan: %w", err)
	}
	stepInfo := findWorkshopStepByID(plan.Steps, state.StepID)
	if stepInfo == nil || stepInfo.Step == nil {
		return nil, fmt.Errorf("step %q not found in plan", state.StepID)
	}
	stepIndex := stepInfo.TopIndex - 1
	if stepIndex < 0 {
		parsed := parseStepPath(state.StepPath)
		stepIndex = parsed.ParentStepNumber - 1
	}
	if stepIndex < 0 {
		stepIndex = 0
	}
	step := stepInfo.Step
	history := hcpo.loadLatestWorkflowContinuationHistory(ctx, state)
	executionLLM := "recovered"
	turnCount := len(history)
	runtimeStepPath := firstNonEmpty(strings.TrimSpace(state.StepPath), resolveInnerStepPath(plan.Steps, stepInfo))
	agentConfigs := getAgentConfigs(step)
	return &workflowContinuationStepRuntime{
		Step:                   step,
		StepIndex:              stepIndex,
		StepPath:               runtimeStepPath,
		LearningPathIdentifier: getEffectiveLearningPathIdentifier(step.GetID(), runtimeStepPath, agentConfigs),
		TotalSteps:             len(plan.Steps),
		ExecutionHistory:       history,
		ValidationResponse: &ValidationResponse{
			IsSuccessCriteriaMet: true,
			ExecutionStatus:      "COMPLETED",
			Reasoning:            "Recovered from continuation_state.json after main execution and pre-validation completed.",
		},
		TurnCount:           turnCount,
		ExecutionLLM:        executionLLM,
		IsCodeExecutionMode: hcpo.getCodeExecutionMode(agentConfigs),
	}, nil
}

func (hcpo *StepBasedWorkflowOrchestrator) loadLatestWorkflowContinuationHistory(ctx context.Context, state *WorkflowContinuationState) []llmtypes.MessageContent {
	if state == nil {
		return nil
	}
	runWorkspacePath := hcpo.GetWorkspacePath()
	if strings.TrimSpace(state.RunFolder) != "" {
		runWorkspacePath = filepath.Join(runWorkspacePath, "runs", state.RunFolder)
	}
	logDir := getExecutionFolderPathForLogs(runWorkspacePath, state.StepID, state.StepPath)
	files, err := hcpo.ListWorkspaceFiles(ctx, logDir)
	if err != nil {
		return nil
	}
	sort.Strings(files)
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	for _, file := range files {
		if !strings.HasSuffix(file, "-conversation.json") {
			continue
		}
		content, err := hcpo.ReadWorkspaceFile(ctx, filepath.Join(logDir, file))
		if err != nil || strings.TrimSpace(content) == "" {
			continue
		}
		var payload struct {
			ConversationHistory []llmtypes.MessageContent `json:"conversation_history"`
			Model               string                    `json:"model"`
		}
		if err := json.Unmarshal([]byte(content), &payload); err != nil {
			continue
		}
		if len(payload.ConversationHistory) > 0 {
			return payload.ConversationHistory
		}
	}
	return nil
}

func (hcpo *StepBasedWorkflowOrchestrator) queueRecoveredDirectKBReview(state *WorkflowContinuationState, runtime *workflowContinuationStepRuntime, handle *mcpagent.AgentSessionHandle) {
	if state == nil || runtime == nil {
		return
	}
	stepCfg := getAgentConfigs(runtime.Step)
	reviewMsg := BuildKBContributionReviewMessageWithTarget(
		resolveKnowledgebaseAccess(stepCfg, hcpo.UseKnowledgebase()),
		kbContributionForPrompt(stepCfg),
		filepath.Join(GetPromptDocsRoot(), hcpo.GetWorkspacePath(), KnowledgebaseFolderName, KBNotesFolderName),
	)
	if strings.TrimSpace(reviewMsg) == "" {
		hcpo.recordWorkflowContinuationPhaseForRunFolder(context.Background(), state.RunFolder, state.StepID, state.StepPath, workflowContinuationOwnerStepExecution, workflowContinuationPhaseKBReview, workflowContinuationStatusSkipped, "no direct KB review message", nil)
		return
	}
	hcpo.startRecoveredDirectContinuation(state, runtime, workflowContinuationPhaseKBReview, "Recovered KB Review", func(execCtx context.Context, agent agents.OrchestratorAgent) (string, error) {
		if handle != nil && !handle.Empty() {
			if base := agent.GetBaseAgent(); base != nil {
				if err := base.Resume(execCtx, handle); err != nil {
					return "", fmt.Errorf("resume KB review agent: %w", err)
				}
			}
		}
		base := agent.GetBaseAgent()
		if base == nil {
			return "", fmt.Errorf("execution agent base is nil")
		}
		result, _, err := hcpo.withWorkshopMessageTarget(execCtx, state.StepID, "recovered-knowledgebase-review", agent, func() (string, []llmtypes.MessageContent, error) {
			return base.Execute(execCtx, reviewMsg, runtime.ExecutionHistory, "", false)
		})
		return summarizeExecutionResultForNotification(result), err
	})
}

func (hcpo *StepBasedWorkflowOrchestrator) queueRecoveredDirectLearning(state *WorkflowContinuationState, runtime *workflowContinuationStepRuntime, handle *mcpagent.AgentSessionHandle) {
	if state == nil || runtime == nil {
		return
	}
	stepCfg := getAgentConfigs(runtime.Step)
	if !hcpo.shouldDirectWriteLearnings(stepCfg, runtime.Step) {
		hcpo.recordWorkflowContinuationPhaseForRunFolder(context.Background(), state.RunFolder, state.StepID, state.StepPath, workflowContinuationOwnerStepExecution, workflowContinuationPhaseDirectLearning, workflowContinuationStatusSkipped, "direct learning gates disabled", nil)
		return
	}
	learnObjective := ""
	if stepCfg != nil {
		learnObjective = stepCfg.LearningObjective
	}
	learnMsg := hcpo.buildLearningsContributionTurn(runtime.Step.GetID(), runtime.Step.GetDescription(), learnObjective, isScriptedStep(runtime.Step, stepCfg))
	if strings.TrimSpace(learnMsg) == "" {
		hcpo.recordWorkflowContinuationPhaseForRunFolder(context.Background(), state.RunFolder, state.StepID, state.StepPath, workflowContinuationOwnerStepExecution, workflowContinuationPhaseDirectLearning, workflowContinuationStatusSkipped, "no direct learning message", nil)
		return
	}
	hcpo.startRecoveredDirectContinuation(state, runtime, workflowContinuationPhaseDirectLearning, "Recovered Direct Learning", func(execCtx context.Context, agent agents.OrchestratorAgent) (string, error) {
		if handle != nil && !handle.Empty() {
			if base := agent.GetBaseAgent(); base != nil {
				if err := base.Resume(execCtx, handle); err != nil {
					return "", fmt.Errorf("resume learning agent: %w", err)
				}
			}
		}
		if cfg := agent.GetConfig(); cfg != nil {
			globalLearningsPath := fmt.Sprintf("%s/learnings/%s", hcpo.GetWorkspacePath(), GlobalLearningID)
			restoreDirectLearningTurn := hcpo.prepareDirectLearningTurn(agent, []string{globalLearningsPath})
			defer restoreDirectLearningTurn()
		}
		base := agent.GetBaseAgent()
		if base == nil {
			return "", fmt.Errorf("execution agent base is nil")
		}
		hcpo.recordWorkflowContinuationPhaseForRunFolder(execCtx, state.RunFolder, state.StepID, state.StepPath, workflowContinuationOwnerStepExecution, workflowContinuationPhaseDirectLearning, workflowContinuationStatusWaitingForLock, "", agent)
		learningsGlobalFileMutex.Lock()
		defer learningsGlobalFileMutex.Unlock()
		globalLearningsPath := fmt.Sprintf("%s/learnings/%s", hcpo.GetWorkspacePath(), GlobalLearningID)
		beforeRef := hcpo.snapshotCanonicalArtifactRef(execCtx, globalLearningsPath)
		result, _, err := hcpo.withWorkshopMessageTarget(execCtx, state.StepID, "recovered-learnings", agent, func() (string, []llmtypes.MessageContent, error) {
			return base.Execute(execCtx, learnMsg, runtime.ExecutionHistory, "", false)
		})
		if err != nil {
			return "", err
		}
		afterRef := hcpo.snapshotCanonicalArtifactRef(execCtx, globalLearningsPath)
		hasNewLearning, reasoning, confidence := learningArtifactChange(beforeRef, afterRef)
		if metadataErr := hcpo.updateLearningMetadataWithTurnCount(
			execCtx,
			runtime.StepIndex,
			runtime.StepPath,
			runtime.LearningPathIdentifier,
			hasNewLearning,
			reasoning,
			confidence,
			runtime.TurnCount,
			runtime.Step,
			true,
			runtime.ExecutionLLM,
			runtime.ExecutionLLM,
		); metadataErr != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to update recovered direct-learning metadata for %s: %v", state.StepID, metadataErr))
		}
		return summarizeExecutionResultForNotification(result), nil
	})
}

func (hcpo *StepBasedWorkflowOrchestrator) startRecoveredDirectContinuation(
	state *WorkflowContinuationState,
	runtime *workflowContinuationStepRuntime,
	phase string,
	labelPrefix string,
	run func(context.Context, agents.OrchestratorAgent) (string, error),
) {
	stepLabel := strings.TrimSpace(runtime.Step.GetTitle())
	if stepLabel == "" {
		stepLabel = state.StepID
	}
	execLabel := fmt.Sprintf("%s: %s", labelPrefix, stepLabel)
	execID := fmt.Sprintf("recover-%s-%s-%05d", phase, state.StepID, time.Now().UnixNano()%100000)
	baseCtx := hcpo.workshopSessionCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	execCtx, cancel := context.WithCancel(baseCtx)
	agentSessionID := fmt.Sprintf("workshop-recovery-%s-%d", state.StepID, time.Now().UnixNano())
	execCtx = context.WithValue(execCtx, orchestratorevents.AgentSessionIDKey, agentSessionID)
	execCtx = context.WithValue(execCtx, orchestratorevents.ForceCorrelationIDKey, agentSessionID)
	execCtx = context.WithValue(execCtx, orchestratorevents.IsSubAgentContextKey, true)

	exec := &WorkshopStepExecution{
		ID:             execID,
		StepID:         execLabel,
		AgentSessionID: agentSessionID,
		Status:         WorkshopStepRunning,
		cancel:         cancel,
	}
	if hcpo.workshopStepRegistry != nil {
		hcpo.workshopStepRegistry.Register(exec)
	}
	if hcpo.workshopExecutionNotifier != nil {
		hcpo.workshopExecutionNotifier.OnExecutionStart(WorkshopExecutionStart{
			ID:                execID,
			ParentExecutionID: currentWorkshopParentExecutionID(execCtx),
			Name:              execLabel,
			Cancel:            cancel,
		})
	}
	go func() {
		hcpo.withWorkflowContinuationRunFolder(state.RunFolder, func() {
			var result string
			var execErr error
			defer func() {
				cancel()
				skipNotify := finalizeExecStatus(exec, execCtx, &result, &execErr)
				status := workflowContinuationStatusCompleted
				errText := ""
				if execErr != nil {
					status = workflowContinuationStatusFailed
					errText = execErr.Error()
				}
				hcpo.recordWorkflowContinuationPhaseForRunFolder(context.Background(), state.RunFolder, state.StepID, state.StepPath, workflowContinuationOwnerStepExecution, phase, status, errText, nil)
				if !skipNotify && hcpo.workshopExecutionNotifier != nil {
					hcpo.workshopExecutionNotifier.OnExecutionComplete(execID, execLabel, result, nil, execErr)
				}
			}()
			hcpo.recordWorkflowContinuationPhaseForRunFolder(execCtx, state.RunFolder, state.StepID, state.StepPath, workflowContinuationOwnerStepExecution, phase, workflowContinuationStatusRunning, "", nil)
			agentName := fmt.Sprintf("%s-recovery-%s", state.StepID, phase)
			agent, err := hcpo.createExecutionOnlyAgent(execCtx, "execution_only", runtime.StepPath, agentName, getAgentConfigs(runtime.Step), runtime.Step, state.StepID, "")
			if err != nil {
				execErr = err
				result = fmt.Sprintf("%s failed for %s: %v", labelPrefix, stepLabel, err)
				return
			}
			result, execErr = run(execCtx, agent)
			if execErr != nil {
				result = fmt.Sprintf("%s failed for %s: %v", labelPrefix, stepLabel, execErr)
			} else if strings.TrimSpace(result) == "" {
				result = fmt.Sprintf("%s completed for %s", labelPrefix, stepLabel)
			}
		})
	}()
}
