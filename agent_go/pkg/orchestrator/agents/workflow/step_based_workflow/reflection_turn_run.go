package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-055. Runs the merged reflection turn that replaced the KB turn followed
// by the direct-learnings turn.
//
// Everything the two turns did is preserved: the shared-file mutex, the
// continuation-phase bookkeeping, both freshness ledgers, the canonical
// artifact-change log, and learning metadata. What changes is that they now
// happen once, with every store the agent must route to reachable at the same
// moment.

// reflectionCostPhase is the phase the reflection turn pushes onto the
// ContextAwareEventBridge, producing `reflection:<step-id>` keys next to the
// step's own `execution_only:<step-id>` in the older per-step token-usage-file
// system. It is the SAME string as costobserver.PhaseReflection (used below
// to attribute this turn in the SQLite cost ledger — a separate system, see
// PLAT-166) but not the same constant; the two are kept deliberately aligned
// rather than merged, since unifying the two attribution systems is a bigger
// change than either ticket that touched this file justified.
const reflectionCostPhase = "reflection"

type stepReflectionTurnResult struct {
	Summary  string
	History  []llmtypes.MessageContent
	Executed bool
}

// runStepReflectionTurn fires the merged post-completion turn for a step.
//
// Best-effort by contract, like the turns it replaces: a step that did its work
// and passed validation is never failed because reflection failed. Errors are
// reported through the returned summary and the continuation ledger.
func (hcpo *StepBasedWorkflowOrchestrator) runStepReflectionTurn(
	ctx context.Context,
	step PlanStepInterface,
	stepIndex int,
	stepPath string,
	artifactStepID string,
	artifactStepPath string,
	stepConfig *AgentConfigs,
	executionAgent agents.OrchestratorAgent,
	history []llmtypes.MessageContent,
	isScriptedMode bool,
	turnCount int,
	executionLLM string,
) stepReflectionTurnResult {
	result := stepReflectionTurnResult{History: history}
	if executionAgent == nil || !hcpo.platformStoresEnabled() {
		return result
	}

	writesLearnings := hcpo.shouldDirectWriteLearnings(stepConfig, step)
	kbAccess := resolveKnowledgebaseAccess(stepConfig, hcpo.UseKnowledgebase())
	kbContribution := kbContributionForPrompt(stepConfig)
	writesKB := kbAccessAllowsWrite(kbAccess) && strings.TrimSpace(kbContribution) != ""

	effectiveLearnings := writesLearnings

	baseWorkspace := hcpo.GetWorkspacePath()
	globalLearningsPath := fmt.Sprintf("%s/learnings/%s", baseWorkspace, GlobalLearningID)
	kbNotesPath := fmt.Sprintf("%s/%s/%s", baseWorkspace, KnowledgebaseFolderName, KBNotesFolderName)

	learnObjective := ""
	if stepConfig != nil {
		learnObjective = stepConfig.LearningObjective
	}
	if !effectiveLearnings {
		learnObjective = ""
	}

	input := StepReflectionTurnInput{
		StepID:              step.GetID(),
		StepDescription:     step.GetDescription(),
		LearningObjective:   learnObjective,
		LearningsTargetPath: filepath.Join(GetPromptDocsRoot(), baseWorkspace, "learnings", GlobalLearningID),
		KBNotesTargetPath:   filepath.Join(GetPromptDocsRoot(), baseWorkspace, KnowledgebaseFolderName, KBNotesFolderName),
		HasBrowserAccess:    hcpo.HasBrowserCapability(),
		DBTableNames:        hcpo.reflectionDBTableNames(ctx),
	}
	if writesKB {
		input.KBAccess = kbAccess
		input.KBContribution = kbContribution
	}
	if effectiveLearnings {
		input.SkillIndexLines = hcpo.reflectionSkillIndexLines(ctx)
	}

	message := BuildStepReflectionTurn(input)
	if strings.TrimSpace(message) == "" {
		return result
	}

	// Widen only for this turn. Main execution had write access to none of
	// these paths, and the outer step defer restores session shell config.
	addedPaths := []string{globalLearningsPath}
	if writesKB {
		addedPaths = append(addedPaths, kbNotesPath)
	}

	hcpo.recordWorkflowContinuationPhase(ctx, artifactStepID, artifactStepPath,
		workflowContinuationOwnerStepExecution, workflowContinuationPhaseDirectLearning,
		workflowContinuationStatusWaitingForLock, "", executionAgent)

	func() {
		restore := hcpo.prepareDirectLearningTurn(executionAgent, addedPaths)
		defer restore()

		if cfg := executionAgent.GetConfig(); cfg != nil && strings.TrimSpace(cfg.MCPSessionID) != "" {
			hcpo.GetLogger().Info(fmt.Sprintf("🔓 [REFLECT] Widened sub-agent session %s for reflection turn on step %s: +%v",
				strings.TrimSpace(cfg.MCPSessionID), step.GetID(), addedPaths))
		}

		// _global/SKILL.md is shared across steps, so parallel reflection turns
		// would race on diff_patch without this.
		learningsGlobalFileMutex.Lock()
		defer learningsGlobalFileMutex.Unlock()

		beforeRef := hcpo.snapshotCanonicalArtifactRef(ctx, globalLearningsPath)
		hcpo.recordWorkflowContinuationPhase(ctx, artifactStepID, artifactStepPath,
			workflowContinuationOwnerStepExecution, workflowContinuationPhaseDirectLearning,
			workflowContinuationStatusRunning, "", executionAgent)
		hcpo.GetLogger().Info(fmt.Sprintf("🧠 Reflection turn for step %d (learnings=%t kb=%t objective_len=%d)",
			stepIndex+1, effectiveLearnings, writesKB, len(learnObjective)))

		ba := executionAgent.GetBaseAgent()
		if ba == nil {
			return
		}
		// Reflection was the one phase with no timing log, so its cost was
		// invisible in runs/.../logs and every "why is this step slow" answer
		// silently excluded it. It is not small — reflection has measured around
		// a fifth of total LLM time on a real workflow — so an execution-only
		// breakdown understates a step's true elapsed time. Capture it the same
		// way execution does, through the same bridge, so the numbers are
		// directly comparable rather than a second, differently-derived metric.
		timingCaptureCtx := ctx
		reflectionAgentName := ""
		if ba != nil {
			reflectionAgentName = ba.GetName()
		}
		if cab, ok := hcpo.GetContextAwareBridge().(*orchestrator.ContextAwareEventBridge); ok {
			timingCaptureCtx = cab.StartTimingCaptureFor(timingCaptureCtx)
			// Cost attribution. The reflection turn reuses the step's own agent and
			// session, so without pushing a phase its tokens land in the step's
			// `execution_only:<step>` bucket — counted, but impossible to separate
			// from the step's real work. That is why no `reflection` key has ever
			// appeared in the cost ledger despite reflection running on nearly
			// every step. Pushing here gives it `reflection:<step>` alongside the
			// step's own bucket, so "what did reflection cost" becomes answerable
			// without changing what is billed. The bridge already supports
			// arbitrary phases, so this is a labeling change, not new plumbing.
			cab.PushContext(reflectionCostPhase, stepIndex, step.GetID(), reflectionAgentName)
			// Deferred rather than popped inline so an early return or panic in the
			// turn can never leave the bridge's context stack unbalanced, which
			// would misattribute every later step in the run.
			defer cab.PopContext()
		}
		// PLAT-166. The bridge push above only affects the older per-step
		// token-usage-file system; the SQLite cost ledger (what the Cost
		// Analysis UI actually reads) is fed by a completely separate
		// AgentEventListener — the cost observer attached to this same agent
		// back when its execution turn started. Toggle its phase here too, or
		// this turn's cost silently lands untagged in the ledger even though
		// the bridge correctly tagged it `reflection`. Deferred for the same
		// reason as cab.PopContext above. Resets to "" (not
		// PhaseExecutionOnly): see SetPhase's doc for why an untagged default
		// beats a named-but-redundant one.
		for _, observer := range ba.Observers() {
			if costObserver, ok := observer.(*costobserver.Observer); ok {
				costObserver.SetPhase(costobserver.PhaseReflection)
				defer costObserver.SetPhase("")
			}
		}
		reflectionStartedAt := time.Now().UTC()
		turnResult, updatedHistory, turnErr := hcpo.withWorkshopMessageTarget(timingCaptureCtx, step.GetID(), "reflection", executionAgent,
			func() (string, []llmtypes.MessageContent, error) {
				return ba.Execute(timingCaptureCtx, message, history, "", false)
			})
		reflectionCompletedAt := time.Now().UTC()
		var reflectionToolCalls []orchestrator.ToolCallEntry
		var reflectionLLMCalls []orchestrator.LLMCallEntry
		if cab, ok := hcpo.GetContextAwareBridge().(*orchestrator.ContextAwareEventBridge); ok {
			timingCapture := cab.DrainTimingCaptureFor(timingCaptureCtx)
			reflectionToolCalls = timingCapture.ToolCalls
			reflectionLLMCalls = timingCapture.LLMCalls
		}
		// Written before the turnErr check below: a reflection turn that failed
		// still consumed real time, and excluding it would bias the totals in
		// exactly the cases worth investigating.
		hcpo.saveReflectionTimingLog(stepIndex, artifactStepID, artifactStepPath, executionLLM, executionAgent,
			reflectionToolCalls, reflectionLLMCalls, reflectionStartedAt, reflectionCompletedAt,
			effectiveLearnings, writesKB, turnErr)

		afterRef := hcpo.snapshotCanonicalArtifactRef(context.Background(), globalLearningsPath)
		if afterRef != beforeRef {
			LogCanonicalArtifactChange(context.Background(), hcpo.GetWorkspacePath(), "runtime_learning_update",
				"Step post-completion turn changed reusable runtime guidance.",
				[]PlanFieldChange{{StepID: step.GetID(), Field: "artifact_tree", OldValue: beforeRef, NewValue: afterRef}},
				hcpo.ReadWorkspaceFile, hcpo.WriteWorkspaceFile, hcpo.GetLogger(),
				"", nil, nil)
		}

		if turnErr != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Reflection turn failed for step %d: %v (accepting step anyway)", stepIndex+1, turnErr))
			result.Summary = fmt.Sprintf("CONCERNS: step reflection turn failed for this run: %v\nSTATUS: COMPLETED", turnErr)
			hcpo.recordWorkflowContinuationPhase(context.Background(), artifactStepID, artifactStepPath,
				workflowContinuationOwnerStepExecution, workflowContinuationPhaseDirectLearning,
				workflowContinuationStatusFailed, turnErr.Error(), executionAgent)
			return
		}

		result.Summary = summarizeExecutionResultForNotification(turnResult)
		result.History = updatedHistory
		result.Executed = true
		hcpo.recordWorkflowContinuationPhase(context.Background(), artifactStepID, artifactStepPath,
			workflowContinuationOwnerStepExecution, workflowContinuationPhaseDirectLearning,
			workflowContinuationStatusCompleted, "", executionAgent)

		// Freshness ledgers are code-owned and best-effort. Both stores were
		// genuinely reviewed in this turn when the step writes to them, so both
		// are confirmed here — previously each turn confirmed its own.
		hasNewLearning, reasoning, confidence := learningArtifactChange(beforeRef, afterRef)
		if writesKB {
			if err := hcpo.recordKnowledgebaseConfirmation(context.Background(), hcpo.selectedRunFolder, step.GetID()); err != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to record knowledgebase freshness for step %s: %v", step.GetID(), err))
			}
		}
		if effectiveLearnings {
			if err := hcpo.recordLearningsConfirmation(context.Background(), hcpo.selectedRunFolder, step.GetID(), hasNewLearning); err != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to record learnings freshness for step %s: %v", step.GetID(), err))
			}
			pathIdentifier := getEffectiveLearningPathIdentifier(step.GetID(), stepPath, stepConfig)
			if err := hcpo.updateLearningMetadataWithTurnCount(
				ctx, stepIndex, stepPath, pathIdentifier,
				hasNewLearning, reasoning, confidence, turnCount, step,
				true, // pre-validation already passed
				executionLLM, executionLLM,
			); err != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to update reflection learning metadata for step %s: %v", step.GetID(), err))
			}
		}
	}()

	return result
}

// saveReflectionTimingLog persists the reflection turn's cost next to the
// execution attempts it belongs to, under a `reflection-timing.json` sibling.
//
// The schema deliberately matches the execution timing file (same
// buildTimingTrace / normalize* helpers, same `agent`/`llm`/`tools`/`breakdown`
// shape) so a reader can sum execution and reflection without reconciling two
// formats. `phase` is what distinguishes them.
//
// Best-effort, like every other side effect of the reflection turn: a failure to
// write timing never affects the step's outcome.
func (hcpo *StepBasedWorkflowOrchestrator) saveReflectionTimingLog(
	stepIndex int, stepID string, stepPath string, executionLLM string,
	executionAgent agents.OrchestratorAgent,
	toolCalls []orchestrator.ToolCallEntry,
	llmCalls []orchestrator.LLMCallEntry,
	startedAt time.Time, completedAt time.Time,
	wroteLearnings bool, wroteKB bool, turnErr error,
) {
	saveCtx := context.Background()
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	duration := completedAt.Sub(startedAt)

	toolTiming := normalizeToolTimingEntries(toolCalls, startedAt)
	llmTiming := normalizeLLMTimingEntries(llmCalls, startedAt)
	agentName := ""
	if executionAgent != nil && executionAgent.GetBaseAgent() != nil {
		agentName = executionAgent.GetBaseAgent().GetName()
	}
	traceSpans, breakdown := buildTimingTrace(stepID, agentName, executionLLM, startedAt, completedAt, duration, llmTiming, toolTiming)

	timingData := map[string]interface{}{
		"schema_version": 2,
		"phase":          "reflection",
		"step_index":     stepIndex + 1,
		"step_id":        stepID,
		"step_path":      stepPath,
		"run_folder":     hcpo.selectedRunFolder,
		// What the turn was actually asked to do. A reflection turn that wrote
		// neither store is a different cost question from one that wrote both.
		"wrote_learnings": wroteLearnings,
		"wrote_kb":        wroteKB,
		"failed":          turnErr != nil,
		"agent": map[string]interface{}{
			"name":                          agentName,
			"model":                         executionLLM,
			"started_at":                    formatRFC3339UTC(startedAt),
			"completed_at":                  formatRFC3339UTC(completedAt),
			"duration_ns":                   int64(duration),
			"duration_ms":                   durationToMillis(duration),
			"llm_call_count":                llmTiming.Count,
			"llm_duration_ms":               llmTiming.TotalDurationMs,
			"llm_time_to_first_response_ms": llmTiming.TimeToFirstResponseMs,
		},
		"llm":         llmTiming,
		"tools":       toolTiming,
		"trace_spans": traceSpans,
		"breakdown":   breakdown,
	}
	if turnErr != nil {
		timingData["error"] = turnErr.Error()
	}

	var validationWorkspacePath string
	if hcpo.selectedRunFolder != "" {
		validationWorkspacePath = fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
	} else {
		validationWorkspacePath = hcpo.GetWorkspacePath()
	}
	logDir := getExecutionFolderPathForLogs(validationWorkspacePath, stepID, stepPath)
	timingPath := fmt.Sprintf("%s/reflection-timing.json", logDir)
	timingJSON, err := json.MarshalIndent(timingData, "", "  ")
	if err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to marshal reflection timing for step %s: %v", stepID, err))
		return
	}
	if err := hcpo.WriteWorkspaceFile(saveCtx, timingPath, string(timingJSON)); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to write reflection timing to %s: %v", timingPath, err))
		return
	}
	hcpo.GetLogger().Info(fmt.Sprintf("⏱️ Reflection turn for step %s took %dms (llm %dms, %d tool calls)",
		stepID, durationToMillis(duration), llmTiming.TotalDurationMs, toolTiming.Count))
}

// reflectionDBTableNames lists the workflow's tables so the routing rule can
// name real destinations. Best-effort: an unavailable database simply omits the
// list rather than blocking reflection.
func (hcpo *StepBasedWorkflowOrchestrator) reflectionDBTableNames(ctx context.Context) []string {
	if !hcpo.platformStoresEnabled() {
		return nil
	}
	names, err := LoadWorkflowDBTableNames(ctx, hcpo.GetWorkspacePath())
	if err != nil {
		hcpo.GetLogger().Info(fmt.Sprintf("ℹ️ Reflection turn omitting DB table list: %v", err))
		return nil
	}
	return names
}

// reflectionLearningsSizes reports the current index size and the step's own
// file, so the prompt can state the gap instead of asserting a budget nobody
// measures.
func (hcpo *StepBasedWorkflowOrchestrator) reflectionSkillIndexLines(ctx context.Context) int {
	if !hcpo.platformStoresEnabled() {
		return 0
	}
	base := fmt.Sprintf("%s/learnings/%s", hcpo.GetWorkspacePath(), GlobalLearningID)
	content, err := hcpo.ReadWorkspaceFile(ctx, base+"/SKILL.md")
	if err != nil {
		return 0
	}
	return len(strings.Split(strings.TrimRight(content, "\n"), "\n"))
}
