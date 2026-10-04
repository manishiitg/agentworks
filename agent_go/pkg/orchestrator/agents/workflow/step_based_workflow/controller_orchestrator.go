package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	orchestratoragents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"

	baseevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func (hcpo *StepBasedWorkflowOrchestrator) getOrchestratorExecutionWorkspacePath() string {
	baseWorkspacePath := hcpo.GetWorkspacePath()
	if hcpo.selectedRunFolder != "" {
		return filepath.Join(baseWorkspacePath, "runs", hcpo.selectedRunFolder, "execution")
	}
	return filepath.Join(baseWorkspacePath, "execution")
}

func (hcpo *StepBasedWorkflowOrchestrator) getOrchestratorStepExecutionPath(stepID, stepPath string) string {
	return getExecutionFolderPath(hcpo.getOrchestratorExecutionWorkspacePath(), stepID, stepPath)
}

// setupOrchestratorFolderGuard is used for both enforcement and prompt text.
func (hcpo *StepBasedWorkflowOrchestrator) setupOrchestratorFolderGuard(step PlanStepInterface, stepPath string) (readPaths, writePaths []string) {
	baseWorkspacePath := hcpo.GetWorkspacePath()
	executionWorkspacePath := hcpo.getOrchestratorExecutionWorkspacePath()
	skillStepConfig := getAgentConfigs(step)
	kbAccessForGuard := resolveKnowledgebaseAccess(skillStepConfig, hcpo.UseKnowledgebase())
	learningsAccessForGuard := resolveExecutionLearningsAccess(skillStepConfig, step)

	// READ: current group's execution folder + DB documentation/assets, plus KB/learnings only when
	// the step config grants those stores. WRITE: current group's execution
	// folder + DB assets, plus KB notes only for direct KB writes.
	// Do not grant the workflow root here. That would expose workflow.json, variables/,
	// planning/, and sibling groups to a nested todo_task orchestrator whose job is
	// to coordinate work inside the current run, not inspect global workflow state.
	parentRoot := filepath.Join(executionWorkspacePath, parentAgentArtifactRoot(step.GetID(), stepPath))
	sharedPath := filepath.Join(parentRoot, "shared")
	downloadsPath := filepath.Join(executionWorkspacePath, "Downloads")
	readPaths = []string{executionWorkspacePath, parentRoot, sharedPath, downloadsPath, filepath.Join(baseWorkspacePath, "soul", "soul.md")}
	// The bridge writes oversized tool results here; agents need read access to retrieve them.
	readPaths = append(readPaths, filepath.Join(baseWorkspacePath, "tool_output_folder"))
	writePaths = []string{parentRoot, sharedPath, downloadsPath}
	readPaths, writePaths = appendManagedDBFileAccess(baseWorkspacePath, readPaths, writePaths)
	if learningsAccessForGuard != LearningsAccessNone {
		globalLearningsPath := filepath.Join(baseWorkspacePath, "learnings", GlobalLearningID)
		readPaths = append(readPaths, globalLearningsPath)
		// Unlike regular steps (which write learnings in a dedicated post-step turn),
		// the orchestrator writes its stores directly via the folder guard — same as it
		// does for knowledgebase/notes below. Grant learnings write when access is read-write
		// and the step's single access mode permits writes.
		if learningsAccessForGuard == LearningsAccessReadWrite {
			writePaths = append(writePaths, globalLearningsPath)
		}
	}
	if kbAccessAllowsRead(kbAccessForGuard) {
		readPaths = append(readPaths, getKnowledgebasePath(baseWorkspacePath))
	}
	if kbAccessAllowsWrite(kbAccessForGuard) {
		writePaths = append(writePaths, filepath.Join(getKnowledgebasePath(baseWorkspacePath), "notes"))
		// A "write" knowledgebase_source grants the same notes/-only write,
		// just in a resolved external workflow's KB instead of this one's own.
		writePaths = append(writePaths, writableExternalKBNotesPaths(baseWorkspacePath)...)
	}
	readPaths = appendAdditionalWorkflowReadPaths(readPaths, baseWorkspacePath, skillStepConfig)
	readPaths = common.DeduplicateStrings(readPaths)

	// Add skill folder paths to read paths (skills are read-only)
	effectiveSkills := GetEffectiveSkills(skillStepConfig, hcpo.BaseOrchestrator)
	if len(effectiveSkills) > 0 {
		skillReadPaths, _ := BuildSkillFolderGuardPaths(effectiveSkills)
		readPaths = append(readPaths, skillReadPaths...)
		hcpo.GetLogger().Info(fmt.Sprintf("🎯 Added skill folder paths to todo task folder guard: %v", skillReadPaths))
	}

	readPaths, writePaths = hcpo.appendCDPHostDownloadsPaths(readPaths, writePaths)
	return common.DeduplicateStrings(readPaths), common.DeduplicateStrings(writePaths)
}

// executeOrchestratorStep executes a todo task step by:
//  1. The orchestrator LLM delegates to sub-agents and/or executes directly
//  2. Processing tool calls:
//     - call_sub_agent / call_scripted_sub_agent: Delegate to predefined agent or scripted routes
//     - call_generic_agent: Delegate to generic agent (no learning/prevalidation)
//  3. Pre-validation checks output files after execution
//  4. Retry with feedback on pre-validation failure (up to 3 attempts)
//  5. Return success status and next step ID
//
// Returns: (successCriteriaMet bool, nextStepID string, error)
func (hcpo *StepBasedWorkflowOrchestrator) executeOrchestratorStep(
	ctx context.Context,
	step PlanStepInterface,
	stepIndex int,
	progress *StepProgress,
	previousContextFiles []string,
	previousExecutionResults []string,
	iteration int,
	execCtx *ExecutionContext,
	allSteps []PlanStepInterface,
	stepPath string,
) (bool, string, error) {
	// Cast to OrchestratorPlanStep
	orchestratorStep, ok := step.(*OrchestratorPlanStep)
	if !ok {
		return false, "", fmt.Errorf("step is not a OrchestratorPlanStep")
	}

	hcpo.GetLogger().Info(fmt.Sprintf("🎯 Executing todo task step %d: %s", stepIndex+1, step.GetTitle()))

	// Use provided stepPath or generate from stepIndex
	orchestratorStepPath := stepPath
	if orchestratorStepPath == "" {
		orchestratorStepPath = fmt.Sprintf("step-%d", stepIndex+1)
	}

	// Setup folder guard for todo task orchestrator agent
	// The orchestrator needs to read/write output files and access workspace files
	stepID := step.GetID()
	if stepID == "" {
		stepID = fmt.Sprintf("step-%d", stepIndex+1)
	}

	// Build paths for folder guard
	// All paths should include the workspace prefix (e.g., Workflow/codeanalysis/...)

	stepExecutionPath := hcpo.getOrchestratorStepExecutionPath(stepID, orchestratorStepPath)
	readPaths, writePaths := hcpo.setupOrchestratorFolderGuard(step, orchestratorStepPath)

	// Snapshot the shared orchestrator-level guard and restore it on exit. The
	// todo_task orchestrator still enforces via this shared guard (unlike
	// execution-only agents, which carry per-agent config guards precisely to
	// avoid sharing this state). Without the restore, a nested todo_task route
	// leaves its narrower guard in place when it returns, and the parent
	// orchestrator's remaining turns — including the learnings/KB contribution
	// turns — get validated against the nested step's paths.
	prevGuardRead, prevGuardWrite := hcpo.GetFolderGuardPaths()
	defer hcpo.SetWorkspacePathForFolderGuard(prevGuardRead, prevGuardWrite)
	hcpo.SetWorkspacePathForFolderGuard(readPaths, writePaths)
	hcpo.GetLogger().Info(fmt.Sprintf("🔒 Setting folder guard for todo task orchestrator agent - Read paths: %v, Write paths: %v", readPaths, writePaths))

	// Ensure step execution folder exists before agent starts
	if err := hcpo.ensureStepExecutionFolderExists(ctx, stepExecutionPath); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to ensure step execution folder exists: %v (continuing - folder will be created when files are written)", err))
	}

	// Emit step_started event
	hcpo.emitStepStartedEvent(ctx, step, stepIndex, orchestratorStepPath)

	// Keep only the latest iteration conversation history in-memory.
	// Todo-task state should come from current files (outputs, tool results),
	// not from replaying previous assistant narration across loop iterations.
	var conversationHistory []llmtypes.MessageContent
	defer func() {
		if execCtx != nil && execCtx.ConversationHistoryCapture != nil {
			historyCopy := append([]llmtypes.MessageContent(nil), conversationHistory...)
			*execCtx.ConversationHistoryCapture = historyCopy
		}
	}()

	stepConfig := getAgentConfigs(orchestratorStep)

	// Orchestrator steps have no scripted fast path. A fast_path_only request for one is a
	// caller error rather than something to silently run through the LLM orchestrator.
	if execCtx != nil && execCtx.SavedScriptOnly {
		return false, "", fmt.Errorf("orchestrator step %q does not support scripted execution; put deterministic delegation in a regular scripted step that calls its routes", stepID)
	}

	// Learnings read gate — default-on unless learnings_access="none" or routing/eval.
	// Todo-task agents benefit from seeing _global/SKILL.md to reuse cross-step knowledge.
	isLearningDisabled := !canReadLearnings(stepConfig, orchestratorStep)
	select {
	case <-ctx.Done():
		return false, "", fmt.Errorf("todo task execution canceled: %w", ctx.Err())
	default:
	}
	var orchestratorLearningHistory string
	if !isLearningDisabled {
		orchestratorLearningHistory, _ = hcpo.readGlobalLearningHistory(ctx)
	}

	// The orchestrator's own prompt variables: routes catalog, dependencies, stores,
	// folder guard, validation schema, human input. Built once; every turn of the
	// sequence reuses them (follow-up turns only swap in their message).
	templateVars := hcpo.buildOrchestratorTemplateVars(
		ctx,
		orchestratorStep,
		stepIndex,
		orchestratorStepPath,
		previousContextFiles,
		previousExecutionResults,
		allSteps,
		orchestratorLearningHistory,
		execCtx,
	)

	agentName := orchestratorStep.Title
	if agentName == "" {
		agentName = fmt.Sprintf("todo-task-orchestrator-step-%d", stepIndex+1)
	}
	llmConfig := hcpo.selectOrchestratorLLM(ctx, stepConfig, stepID, orchestratorStepPath)
	if llmConfig == nil {
		return false, "", fmt.Errorf("no valid LLM configuration found for todo task orchestrator")
	}
	executionLLM := ""
	if llmConfig.Primary.ModelID != "" {
		executionLLM = fmt.Sprintf("%s/%s", llmConfig.Primary.Provider, llmConfig.Primary.ModelID)
	}

	// Sub-agent execution context for tool-based delegation. The workshop
	// correlation ID tags sub-agent events with the workshop step's ID so the
	// frontend can auto-notify on them.
	workshopCorrelationID := ""
	if forcedID, ok := ctx.Value(events.ForceCorrelationIDKey).(string); ok {
		workshopCorrelationID = forcedID
	}
	var humanInputs map[string]string
	if execCtx != nil {
		humanInputs = execCtx.HumanInputs
	}
	subAgentExecCtx := &SubAgentExecutionContext{
		OrchestratorStep:      orchestratorStep,
		StepIndex:             stepIndex,
		StepPath:              orchestratorStepPath,
		AllSteps:              allSteps,
		Progress:              progress,
		StepConfig:            stepConfig, // Pass step config for sub_agent_llm override
		WorkshopCorrelationID: workshopCorrelationID,
		ParentContext:         ctx,
		AsyncEnabled:          true,
		HumanInputs:           humanInputs,
	}

	createAgent := func(agentCtx context.Context) (orchestratoragents.OrchestratorAgent, error) {
		agent, err := hcpo.createOrchestratorAgent(
			agentCtx,
			"todo_task", // phase
			stepIndex,   // step
			0,           // iteration
			stepID,
			orchestratorStepPath,
			agentName,
			stepConfig,
			llmConfig,
			subAgentExecCtx,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create todo task orchestrator agent: %w", err)
		}
		// Sync template vars with the actual agent config — the factory may have
		// enabled code execution mode for CLI providers after the vars were built.
		if cfg := agent.GetConfig(); cfg != nil {
			if agentConfigUseCodeExecutionMode(cfg) {
				templateVars["IsCodeExecutionMode"] = "true"
			}
			// The tools reference section is for CLI providers NOT in code execution
			// mode; in code-exec mode the {{TOOL_STRUCTURE}} JSON is authoritative.
			if isCliProviderForPrompt(agentConfigProvider(cfg)) && !agentConfigUseCodeExecutionMode(cfg) {
				templateVars["ShowToolsSection"] = "true"
			}
		}
		// Pre-save prompts.json so get_step_prompts works during execution, not just after.
		if executionAgent, ok := agent.(*WorkflowExecutionOnlyAgent); ok {
			preSystemPrompt := executionAgent.executionOnlySystemPromptProcessor(templateVars)
			preUserMessage := executionAgent.executionOnlyUserMessageProcessor(templateVars)
			hcpo.preSavePromptsJSON(stepIndex, stepID, orchestratorStepPath, "todo_task_orchestrator", preSystemPrompt, preUserMessage, executionLLM, "todo-task-prompts.json")
		}
		return agent, nil
	}
	turnVars := func(item MessageSequenceItem, message string, opening bool) map[string]string {
		vars := make(map[string]string, len(templateVars)+1)
		for k, v := range templateVars {
			vars[k] = v
		}
		// The description is the system-level step charter. Every authored item,
		// including the first one, is an actual user message.
		vars["FollowUpMessage"] = message
		return vars
	}
	// The todo_task execution-log shape (SubAgentCallRecords, todo_task_response) is
	// what ExecutionLogsPopup renders as the route-selection view and what keeps
	// stepLogs.todo_task populated; keep writing it. Turn N lands as iteration N-1 so
	// the opening turn keeps today's file name.
	logTurn := func(logCtx context.Context, turnNumber int, turnLLM string, history []llmtypes.MessageContent, toolCalls []orchestrator.ToolCallEntry, llmCalls []orchestrator.LLMCallEntry, startedAt, completedAt time.Time) {
		hcpo.saveOrchestratorExecutionLog(logCtx, stepID, orchestratorStepPath, 1, turnNumber-1, turnLLM, history, toolCalls, llmCalls, startedAt, completedAt, completedAt.Sub(startedAt), "")
	}

	// Run the orchestrator on the message_sequence executor: the step description is
	// rendered as the system charter; authored messages are user turns, followed by
	// the synthetic final validation gate and the
	// closing reflection turn. Repairs happen in place with full memory (the
	// prevalidation repair loop) instead of restarting the whole step.
	items := make([]MessageSequenceItem, 0, len(orchestratorStep.Messages))
	for _, m := range orchestratorStep.Messages {
		if t := strings.TrimSpace(m.Type); t == "message" {
			m.Type = "user_message"
		}
		items = append(items, m)
	}
	sequenceStep := &MessageSequencePlanStep{
		Type:             StepTypeMessageSeq,
		CommonStepFields: orchestratorStep.CommonStepFields,
		Items:            items,
		NextStepID:       orchestratorStep.NextStepID,
		AgentConfigs:     orchestratorStep.AgentConfigs,
	}
	opts := messageSequenceCallOptions{
		Source: "configured_queue",
		Delegation: &messageSequenceDelegation{
			ExecCtx:     subAgentExecCtx,
			ReadPaths:   readPaths,
			WritePaths:  writePaths,
			CreateAgent: createAgent,
			TurnVars:    turnVars,
			LogTurn:     logTurn,
		},
	}
	_, history, err := hcpo.executeMessageSequenceStep(ctx, sequenceStep, stepIndex, orchestratorStepPath, progress, execCtx, allSteps, opts)
	conversationHistory = history
	if err != nil {
		if ctx.Err() != nil {
			return false, "", fmt.Errorf("todo task execution canceled: %w", ctx.Err())
		}
		return false, "", fmt.Errorf("todo task orchestrator failed: %w", err)
	}

	hcpo.GetLogger().Info("✅ Todo task step complete")
	hcpo.emitOrchestratorStepCompletedEvent(ctx, step, stepIndex, orchestratorStepPath, 1, "Execution completed", orchestratorStep.NextStepID)
	hcpo.emitStepFinishedEvent(ctx, step, stepIndex, orchestratorStepPath)
	return true, orchestratorStep.NextStepID, nil
}

// delegatingMessageSequenceAsOrchestrator adapts the unified public agent step
// to the existing delegation runtime. OrchestratorPlanStep remains a legacy
// compatibility shape; new plans can put predefined_routes directly on a
// message_sequence and the sequence agent decides whether and when to call them.
func delegatingMessageSequenceAsOrchestrator(step *MessageSequencePlanStep) *OrchestratorPlanStep {
	if step == nil {
		return nil
	}
	return &OrchestratorPlanStep{
		Type:             StepTypeOrchestrator,
		CommonStepFields: step.CommonStepFields,
		PredefinedRoutes: step.PredefinedRoutes,
		NextStepID:       step.NextStepID,
		Messages:         step.Items,
		AgentConfigs:     step.AgentConfigs,
	}
}

func formatMessageSequenceRoutePromptBlock(step PlanStepInterface) string {
	if !isMessageSequenceStep(step) {
		return ""
	}
	return strings.TrimSpace(`Step type: message_sequence
Conversation: route-scoped session resumes within this orchestrator run
First call: starts the sequence and sends the configured item queue
Initial instructions: call_sub_agent instructions are added as initial context before the configured queue
Re-entry: later call_sub_agent instructions are sent as the next user message in the existing conversation
Start fresh: set message_sequence_restart=true to archive the existing route session and replay the configured queue`)
}

// buildOrchestratorTemplateVars builds template variables for the orchestrator agent
func (hcpo *StepBasedWorkflowOrchestrator) buildOrchestratorTemplateVars(
	ctx context.Context,
	step *OrchestratorPlanStep,
	stepIndex int,
	stepPath string,
	previousContextFiles []string,
	previousExecutionResults []string,
	allSteps []PlanStepInterface,
	orchestratorLearningHistory string, // Persisted learnings from previous runs
	execCtx *ExecutionContext,
) map[string]string {
	// Build predefined routes list (title + ID only — use get_route_description tool for details)
	var routesBuilder strings.Builder
	for i, route := range step.PredefinedRoutes {
		if i > 0 {
			routesBuilder.WriteString("\n")
		}
		fmt.Fprintf(&routesBuilder, "- **%s** (`%s`) — %s", ResolveVariables(route.RouteName, hcpo.variableValues), route.RouteID, routeStepTypeSummary(route.SubAgentStep))
		if route.SubAgentStep != nil {
			scripted := isScriptedStep(route.SubAgentStep, getAgentConfigs(route.SubAgentStep))
			subStepPath := todoSubAgentArtifactFolderName(step.GetID(), stepPath, route.RouteID, "<call-id>", scripted)
			subExecRelPath := getExecutionFolderPath(hcpo.getOrchestratorExecutionWorkspacePath(), "", subStepPath)
			subExecAbsPath := filepath.Join(GetPromptDocsRoot(), subExecRelPath)
			contextOutput := strings.TrimSpace(ResolveVariables(route.SubAgentStep.GetContextOutput().String(), hcpo.variableValues))
			if contextOutput != "" {
				fmt.Fprintf(&routesBuilder, " → output: `%s` | folder: `%s/`", contextOutput, subExecAbsPath)
			} else {
				fmt.Fprintf(&routesBuilder, " → folder: `%s/`", subExecAbsPath)
			}
			if isMessageSequenceStep(route.SubAgentStep) {
				fmt.Fprintf(&routesBuilder, " | type: `%s` | repeated calls resume; `message_sequence_restart=true` starts fresh", StepTypeMessageSeq)
			}
			if definitions := scriptedParameterDefinitions(route.SubAgentStep); len(definitions) > 0 {
				fmt.Fprintf(&routesBuilder, " | scripted params: `%s` (use get_route_description before calling)", strings.Join(sortedScriptParameterNames(definitions), "`, `"))
			}
		}
	}

	baseWorkspacePath := hcpo.GetWorkspacePath()
	stepID := step.GetID()
	if stepID == "" {
		stepID = fmt.Sprintf("step-%d", stepIndex+1)
	}
	executionPath := hcpo.getOrchestratorStepExecutionPath(stepID, stepPath)
	shellWorkingDirectory := filepath.Join(GetPromptDocsRoot(), executionPath)

	// Get step config for code execution mode: step config > workflow/preset default
	stepConfig := getAgentConfigs(step)
	isCodeExecutionMode := hcpo.getCodeExecutionMode(stepConfig)
	dbAccessForGuard := resolveDBAccess(stepConfig)

	// Resolve KB access mode for this step (explicit step config > preset default).
	kbAccess := resolveKnowledgebaseAccess(stepConfig, hcpo.UseKnowledgebase())
	learningsAccess := resolveExecutionLearningsAccess(stepConfig, step)
	useKnowledgebase := kbAccess != KBAccessNone

	// Build folder guard paths for prompt (same logic as executeOrchestratorStep setup)
	docsRoot := GetPromptDocsRoot()
	fgExecPath := hcpo.getOrchestratorExecutionWorkspacePath()
	fgGlobalLearningsPath := filepath.Join(baseWorkspacePath, "learnings", GlobalLearningID)
	fgKnowledgebasePath := getKnowledgebasePath(baseWorkspacePath)
	fgDBPath := getDBPath(baseWorkspacePath)
	fgReadPaths, fgWritePaths := hcpo.setupOrchestratorFolderGuard(step, stepPath)

	templateVars := map[string]string{
		// Resolve variables in step metadata
		"StepTitle":           ResolveVariables(step.GetTitle(), hcpo.variableValues),
		"StepDescription":     ResolveVariables(step.GetDescription(), hcpo.variableValues),
		"StepSuccessCriteria": "",
		"StepContextDependencies": func() string {
			resolvedDeps := hcpo.resolveDependencyPathsWithWorkspace(
				ctx,
				ResolveVariablesArray(previousContextFiles, hcpo.variableValues),
				stepIndex, stepPath, allSteps, fgExecPath, docsRoot, hcpo.variableValues,
			)
			formatted, err := hcpo.formatContextDependenciesWithContent(ctx, resolvedDeps, docsRoot)
			if err != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to inline context deps for todo task step: %v", err))
				return strings.Join(resolvedDeps, ", ")
			}
			return formatted
		}(),
		"WorkspacePath":         filepath.Join(GetPromptDocsRoot(), hcpo.GetWorkspacePath()),
		"ExecutionFolderPath":   filepath.Join(docsRoot, fgExecPath),
		"DownloadsPath":         filepath.Join(docsRoot, fgExecPath, "Downloads"),
		"StepNumber":            fmt.Sprintf("step-%d", stepIndex+1),
		"StepExecutionPath":     filepath.Join(docsRoot, executionPath),
		"ShellWorkingDirectory": shellWorkingDirectory,
		"PredefinedRoutes":      routesBuilder.String(),
		"HasBrowserAccess":      fmt.Sprintf("%t", hcpo.HasBrowserCapability()),
		// Add code execution mode and knowledgebase flags
		"IsCodeExecutionMode":       fmt.Sprintf("%v", isCodeExecutionMode),
		"UseKnowledgebase":          fmt.Sprintf("%v", useKnowledgebase), // deprecated, retained for back-compat in template
		"KbAccess":                  kbAccess,
		"KbAccessLabel":             kbAccessLabel(kbAccess),
		"LearningsAccess":           learningsAccess,
		"KnowledgebaseContribution": kbContributionForPrompt(stepConfig),
		"KBGuidanceBlock":           BuildStepKBGuidanceWithTarget(kbAccess, kbContributionForPrompt(stepConfig), filepath.Join(docsRoot, fgKnowledgebasePath, KBNotesFolderName)),
		// Workspace paths and folder guard (consistent with execution agent)
		"FolderGuardReadPaths":  strings.Join(toAbsPaths(docsRoot, fgReadPaths), ", "),
		"FolderGuardWritePaths": strings.Join(toAbsPaths(docsRoot, fgWritePaths), ", "),
		"KnowledgebasePath":     filepath.Join(docsRoot, fgKnowledgebasePath),
		"DBPath":                filepath.Join(docsRoot, fgDBPath),
		"DBAccess":              dbAccessForGuard,
		"DBDirectAccess":        "false", // orchestrator steps are never scripted (PLAT-287)
		"WorkflowRoot":          filepath.Join(docsRoot, baseWorkspacePath),
		"LearningsPath":         filepath.Join(docsRoot, fgGlobalLearningsPath),
	}

	// Build previous steps summary (includes descriptions, output files, and execution results like human_input responses)
	previousStepsSummary := hcpo.buildPreviousStepsSummary(allSteps, stepIndex, previousContextFiles, previousExecutionResults)

	templateVars["PreviousStepsSummary"] = previousStepsSummary
	if execCtx != nil && execCtx.WorkshopHumanInput != "" {
		templateVars["WorkshopHumanInput"] = execCtx.WorkshopHumanInput
		hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Injecting human_input into todo_task step %q prompt (%d chars)", step.GetID(), len(execCtx.WorkshopHumanInput)))
	} else {
		templateVars["WorkshopHumanInput"] = ""
	}

	// Add variable names and values (like orchestration step)
	if variableNames := FormatVariableNames(hcpo.variablesManifest); variableNames != "" {
		templateVars["VariableNames"] = variableNames
	}
	if variableValues := FormatVariableValues(hcpo.variablesManifest, hcpo.variableValues); variableValues != "" {
		templateVars["VariableValues"] = variableValues
	}

	// Add orchestrator learning history if available
	if orchestratorLearningHistory != "" {
		templateVars["LearningHistory"] = orchestratorLearningHistory
	}

	// Surface the pre-validation schema in the prompt so the orchestrator knows
	// which output files must exist on the first attempt — otherwise it only
	// learns the requirements via ValidationFeedback after a failed attempt.
	if validationSchema := step.GetValidationSchema(); validationSchema != nil {
		if schemaJSON, err := json.MarshalIndent(validationSchema, "", "  "); err == nil {
			templateVars["ValidationSchema"] = string(schemaJSON)
		} else {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to marshal validation schema for todo task step %d: %v", stepIndex+1, err))
		}
	}

	return templateVars
}

func routeStepTypeSummary(step PlanStepInterface) string {
	if step == nil {
		return "generic route"
	}
	switch step.StepType() {
	case StepTypeMessageSeq:
		return "type: message_sequence, stateful sequence worker"
	case StepTypeOrchestrator:
		return "type: todo_task, nested orchestrator"
	case StepTypeRegular:
		return "type: regular, stateless worker"
	case StepTypeRouting:
		return "type: routing"
	case StepTypeHumanInput:
		return "type: human_input"
	default:
		return fmt.Sprintf("type: %s", step.StepType())
	}
}

func routeStepBehaviorDetails(step PlanStepInterface) string {
	if step == nil {
		return "Generic ad-hoc route. It does not keep specialist route memory."
	}
	switch step.StepType() {
	case StepTypeMessageSeq:
		return "Stateful sequence worker. First call sends the configured item queue with the provided instructions as initial context. Later calls resume the same saved conversation and send instructions as the re-entry user message. Set message_sequence_restart=true to archive the existing route session and replay the queue from the beginning."
	case StepTypeRegular:
		return "Stateless worker. Each call executes the task as a normal one-off step."
	case StepTypeOrchestrator:
		return "Nested orchestrator. It manages its own sub-tasks and routes."
	default:
		return fmt.Sprintf("Route step type: %s.", step.StepType())
	}
}

// selectOrchestratorLLM selects the LLM config for todo task orchestrator.
//
// Priority:
//  1. step config ExecutionLLM — explicit override always wins (same knob used for
//     regular step execution; sub-agents spawned by this orchestrator inherit it too)
//  2. Tier 1 (High) — default for orchestrator (returns nil if tier resolver is unavailable)
func (hcpo *StepBasedWorkflowOrchestrator) selectOrchestratorLLM(
	ctx context.Context,
	stepConfig *AgentConfigs,
	stepID string,
	stepPath string,
) *orchestrator.LLMConfig {
	// 1. Step config ExecutionLLM always takes highest precedence — one LLM knob
	// covers regular step execution, todo-task orchestrator role, and sub-agents.
	if stepConfig != nil && stepConfig.ExecutionLLM != nil &&
		stepConfig.ExecutionLLM.Provider != "" && stepConfig.ExecutionLLM.ModelID != "" {
		hcpo.GetLogger().Info(fmt.Sprintf("🔧 [STEP OVERRIDE] Todo task orchestrator using step-config ExecutionLLM: %s/%s",
			stepConfig.ExecutionLLM.Provider, stepConfig.ExecutionLLM.ModelID))
		return &orchestrator.LLMConfig{
			Primary: orchestrator.LLMModel{
				Provider:     stepConfig.ExecutionLLM.Provider,
				ModelID:      stepConfig.ExecutionLLM.ModelID,
				Options:      stepConfig.ExecutionLLM.Options,
				ConnectionID: stepConfig.ExecutionLLM.ConnectionID,
			},
			APIKeys: hcpo.GetAPIKeys(),
		}
	}

	// 2. Tiered mode: todo task orchestrators default to Tier 1 (High), including nested
	// todo-task orchestrators. PLAT-061 removed todo_task_orchestrator_tier — it was an
	// int where every other tier is a string enum, so it bypassed tier validation and
	// PLAT-060's required-reason path. Use execution_llm to override.
	if hcpo.tierResolver == nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("selectOrchestratorLLM: tier resolver is nil for step %s — returning nil so caller surfaces a user-visible error", stepPath))
		return nil
	}
	tier := TierHigh
	llmConfig := hcpo.tierResolver.ResolveTier(tier)
	if llmConfig == nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("selectOrchestratorLLM: tier resolver returned nil for Tier %d (%s) on step %s", int(tier), TierLevelLabel(tier), stepPath))
		return nil
	}
	hcpo.GetLogger().Info(fmt.Sprintf("🏷️ [TIERED] Todo task orchestrator using Tier %d (%s): %s/%s",
		int(tier), TierLevelLabel(tier), llmConfig.Primary.Provider, llmConfig.Primary.ModelID))
	return llmConfig
}

// executeGenericAgent executes a generic task using the standard execution agent
// This uses the same execution infrastructure as other steps but with:
// - Learning DISABLED (no learnings accumulated)
// - Validation DISABLED (no validation schema required)
// - Full MCP server access (same as predefined sub-agents)
// All task input comes from response (tool parameters), not from files
func (hcpo *StepBasedWorkflowOrchestrator) executeGenericAgent(
	ctx context.Context,
	step *OrchestratorPlanStep,
	stepIndex int,
	stepPath string,
	response *OrchestratorDecision,
	allSteps []PlanStepInterface,
	progress *StepProgress,
) (string, []llmtypes.MessageContent, error) {
	ctx = withParentStepIndex(ctx, stepIndex)
	// Use todoID as the task title
	// All actual task content comes from response.InstructionsToSubAgent
	taskTitle := response.TodoIDToExecute
	parentTodoTitle := step.GetTitle()
	if parentTodoTitle == "" {
		parentTodoTitle = stepPath
	}

	hcpo.GetLogger().Info(fmt.Sprintf("🤖 Executing generic agent for task: %s", taskTitle))

	useCodeExecutionMode := boolPtr(false)
	todoIDPart := workflowSafeIDPart(response.TodoIDToExecute, "todo")

	// Create a synthetic RegularPlanStep for the generic execution
	// Use the orchestrator's instructions and success criteria
	genericStepID := fmt.Sprintf("generic-%s-%s", stepPath, todoIDPart)
	genericStep := &RegularPlanStep{
		Type: StepTypeRegular,
		CommonStepFields: CommonStepFields{
			ID:            genericStepID,
			Title:         taskTitle,
			Description:   response.InstructionsToSubAgent,
			ContextOutput: FlexibleContextOutput(fmt.Sprintf("%s-result.json", todoIDPart)),
		},
		HasLoop: false,
		// Generic agents do not contribute learnings and skip pre-validation, but
		// they still inherit execution-mode settings from the parent step.
		AgentConfigs: func() *AgentConfigs {
			// Inherit parallel tool execution setting from parent step
			var disableParallelToolExec *bool
			if parentConfig := getAgentConfigs(step); parentConfig != nil {
				disableParallelToolExec = parentConfig.DisableParallelToolExecution
			}
			return &AgentConfigs{
				// Learning is off by default (LearningObjective empty) — generic agents
				// don't generate persistent learnings by design.
				UseCodeExecutionMode:         useCodeExecutionMode,
				DisableParallelToolExecution: disableParallelToolExec, // inherit from parent
			}
		}(),
	}

	callID := subAgentCallID(asyncSubAgentExecutionID(ctx), response.TodoIDToExecute, time.Now().UnixNano())
	// Generic agents are a named route beneath their owning Agent. Every call is
	// isolated while remaining inside the parent's shared artifact subtree.
	genericStepPath := genericAgentArtifactFolderName(step.GetID(), stepPath, callID)

	if err := hcpo.cleanupExecutionArtifactsForStepPath(ctx, genericStepPath, ""); err != nil {
		return "", nil, fmt.Errorf("failed to cleanup generic sub-agent output %q: %w", genericStepPath, err)
	}

	// Build execution context
	var capturedHistory []llmtypes.MessageContent
	execCtx := &ExecutionContext{
		SkipHumanInput:             true, // Generic agents don't request human feedback
		RunSingleStepOnly:          false,
		SingleStepTarget:           -1,
		ArtifactFolderNameOverride: genericStepPath,
		ConversationHistoryCapture: &capturedHistory,
	}

	// Notify sub-agent start
	agentID := asyncSubAgentExecutionID(ctx)
	if agentID == "" {
		agentID = fmt.Sprintf("todo-generic-%s-%s-%d", stepPath, todoIDPart, time.Now().UnixNano())
	}
	agentName := fmt.Sprintf("%s -> Generic (%s)", parentTodoTitle, taskTitle)
	subAgentCtx, subAgentCancel := context.WithCancel(ctx)
	defer subAgentCancel()
	parentExecutionID := subAgentParentExecutionID(ctx)
	if hcpo.subAgentNotifier != nil {
		hcpo.subAgentNotifier.OnSubAgentStart(WorkshopExecutionStart{
			ID:                agentID,
			ParentExecutionID: parentExecutionID,
			Name:              agentName,
			Kind:              "workflow_generic_agent",
			Metadata: map[string]string{
				"async_parent_reconciles":    fmt.Sprintf("%t", asyncSubAgentExecutionID(ctx) != ""),
				"suppress_auto_notification": fmt.Sprintf("%t", asyncSubAgentExecutionID(ctx) != ""),
			},
			Cancel: subAgentCancel,
		})
	}
	subAgentCtx = virtualtools.WithBackgroundAgentID(subAgentCtx, agentID)
	subAgentCtx = context.WithValue(subAgentCtx, events.ParentExecutionIDKey, agentID)

	// Keep child event identity on its goroutine-local context. Parallel agents
	// cannot safely push/pop one shared bridge stack.
	subAgentCtx = orchestrator.WithEventContextOverride(
		subAgentCtx,
		"execution",
		stepIndex,
		genericStep.GetID(),
		genericStep.GetTitle(),
		orchestrator.RichStepContext{
			StepName:     genericStep.GetTitle(),
			StepType:     effectiveRuntimeStepType(genericStep),
			StepIndex:    stepIndex + 1,
			ParentStepID: step.GetID(),
			TriggeredBy:  "todo_task",
		},
	)

	var executionResult string
	var err error
	messageSequence := virtualtools.GenericAgentMessageSequenceFromContext(subAgentCtx)
	if len(messageSequence) > 0 {
		items := make([]MessageSequenceItem, 0, 1+len(messageSequence))
		items = append(items, MessageSequenceItem{
			ID:      "opening",
			Type:    "user_message",
			Title:   "Opening",
			Message: response.InstructionsToSubAgent,
		})
		for _, message := range messageSequence {
			items = append(items, MessageSequenceItem{
				ID:      message.ID,
				Type:    "user_message",
				Title:   message.Title,
				Message: message.Message,
			})
		}
		sequenceStep := &MessageSequencePlanStep{
			Type: StepTypeMessageSeq,
			CommonStepFields: CommonStepFields{
				ID:            genericStepID,
				Title:         taskTitle,
				ContextOutput: FlexibleContextOutput(fmt.Sprintf("%s-result.json", todoIDPart)),
			},
			Items:        items,
			AgentConfigs: genericStep.AgentConfigs,
		}
		var history []llmtypes.MessageContent
		executionResult, history, err = hcpo.executeMessageSequenceStep(
			subAgentCtx,
			sequenceStep,
			stepIndex,
			genericStepPath,
			progress,
			execCtx,
			allSteps,
			messageSequenceCallOptions{Source: "generic_agent_sequence"},
		)
		capturedHistory = append([]llmtypes.MessageContent(nil), history...)
	} else {
		// Execute using executeSingleStep (reuses standard execution infrastructure)
		executionResult, _, err = hcpo.executeSingleStep(
			subAgentCtx,
			genericStep,
			stepIndex,       // Use parent step index for context
			genericStepPath, // stepPath
			1,               // totalSteps = 1 for single generic task
			0,               // iteration
			[]string{},      // previousContextFiles - empty for generic tasks
			progress,        // progress
			true,            // nested execution: parent todo_task owns top-level progress
			execCtx,         // execCtx
			allSteps,        // allSteps
			true,            // isSubAgent = true (sub-agents never request human feedback)
			[]string{response.InstructionsToSubAgent}, // previousExecutionResults - pass instructions
			nil, // orchestrationRoutes - none for generic agent
		)
	}

	// Notify sub-agent completion
	if hcpo.subAgentNotifier != nil {
		resultStr := fmt.Sprintf("Generic agent completed: %s", executionResult)
		if err != nil {
			resultStr = fmt.Sprintf("Generic agent failed: %v", err)
		}
		hcpo.subAgentNotifier.OnSubAgentComplete(agentID, agentName, resultStr, err)
	}

	if err != nil {
		return fmt.Sprintf("Generic agent failed: %v", err), capturedHistory, err
	}

	result := fmt.Sprintf("Generic agent completed: %s", executionResult)
	return result, capturedHistory, nil
}

// boolPtr returns a pointer to a bool value
func boolPtr(b bool) *bool {
	return &b
}

func appendDelegationInstructions(originalDescription, instructions string) string {
	if instructions == "" {
		return originalDescription
	}
	if originalDescription == "" {
		return instructions
	}
	return fmt.Sprintf("%s\n\n## Orchestrator Instructions\n\n%s", originalDescription, instructions)
}

func applyDelegationOverridesToCommonFields(fields *CommonStepFields, instructions string) {
	if fields == nil {
		return
	}
	fields.Description = appendDelegationInstructions(fields.Description, instructions)
}

func cloneStepWithDelegationOverrides(
	step PlanStepInterface,
	instructions string,
) (PlanStepInterface, error) {
	switch s := step.(type) {
	case *RegularPlanStep:
		stepCopy := *s
		applyDelegationOverridesToCommonFields(&stepCopy.CommonStepFields, instructions)
		return &stepCopy, nil
	case *RoutingPlanStep:
		stepCopy := *s
		applyDelegationOverridesToCommonFields(&stepCopy.CommonStepFields, instructions)
		return &stepCopy, nil
	case *BranchPlanStep:
		stepCopy := *s
		applyDelegationOverridesToCommonFields(&stepCopy.CommonStepFields, instructions)
		return &stepCopy, nil
	case *HumanInputPlanStep:
		stepCopy := *s
		applyDelegationOverridesToCommonFields(&stepCopy.CommonStepFields, instructions)
		return &stepCopy, nil
	case *MessageSequencePlanStep:
		stepCopy := *s
		// The saved description is the durable system charter. Dynamic parent
		// instructions are queued as user messages by executeRoutedSubAgentStep.
		return &stepCopy, nil
	case *OrchestratorPlanStep:
		stepCopy := *s
		// Keep dynamic delegation out of the system charter; it is a user turn.
		return &stepCopy, nil
	default:
		return step, nil
	}
}

func (hcpo *StepBasedWorkflowOrchestrator) executeRoutedSubAgentStep(
	ctx context.Context,
	stepToExecute PlanStepInterface,
	delegationInstructions string,
	stepIndex int,
	subAgentStepPath string,
	progress *StepProgress,
	execCtx *ExecutionContext,
	allSteps []PlanStepInterface,
	orchestrationRoutesForSubAgent []OrchestrationRoute,
) (string, []llmtypes.MessageContent, error) {
	var capturedHistory []llmtypes.MessageContent
	localExecCtx := execCtx
	if execCtx != nil {
		execCtxCopy := *execCtx
		execCtxCopy.ConversationHistoryCapture = &capturedHistory
		localExecCtx = &execCtxCopy
	}

	delegatingStep := stepToExecute
	delegatingSequence := false
	if sequence, ok := stepToExecute.(*MessageSequencePlanStep); ok && len(sequence.PredefinedRoutes) > 0 {
		delegatingStep = delegatingMessageSequenceAsOrchestrator(sequence)
		delegatingSequence = true
	}
	if isOrchestratorStep(stepToExecute) || delegatingSequence {
		if orchestratorStep, ok := delegatingStep.(*OrchestratorPlanStep); ok {
			stepCopy := *orchestratorStep
			stepCopy.Messages = append([]MessageSequenceItem(nil), orchestratorStep.Messages...)
			if instruction := strings.TrimSpace(delegationInstructions); instruction != "" {
				stepCopy.Messages = append([]MessageSequenceItem{{
					ID:      stepCopy.GetID() + "-initial-instruction",
					Type:    "user_message",
					Kind:    "execution",
					Message: instruction,
				}}, stepCopy.Messages...)
			}
			delegatingStep = &stepCopy
		}
		successCriteriaMet, _, err := hcpo.executeOrchestratorStep(
			ctx,
			delegatingStep,
			stepIndex,
			progress,
			[]string{},
			[]string{},
			0,
			localExecCtx,
			allSteps,
			subAgentStepPath,
		)
		if err != nil {
			return "", capturedHistory, err
		}
		if !successCriteriaMet {
			return "", capturedHistory, fmt.Errorf("nested todo task step did not complete successfully")
		}

		if orchestratorStep, ok := stepToExecute.(*OrchestratorPlanStep); ok && orchestratorStep.OrchestratorDecision != nil {
			if orchestratorStep.OrchestratorDecision.CompletionReason != "" {
				return orchestratorStep.OrchestratorDecision.CompletionReason, capturedHistory, nil
			}
			if orchestratorStep.OrchestratorDecision.ProgressSummary != "" {
				return orchestratorStep.OrchestratorDecision.ProgressSummary, capturedHistory, nil
			}
		}

		return "Nested todo task completed successfully", capturedHistory, nil
	}

	sequenceExecutionStep := stepToExecute
	if shouldNormalizeRegularStepToMessageSequence(stepToExecute) {
		sequenceExecutionStep = normalizeRegularStepToMessageSequence(stepToExecute.(*RegularPlanStep))
		hcpo.GetLogger().Info(fmt.Sprintf("💬 Normalizing routed non-scripted regular step %q to the message-sequence runtime", stepToExecute.GetID()))
	}
	if isMessageSequenceStep(sequenceExecutionStep) {
		messageSequenceRestart, _ := ctx.Value(virtualtools.SubAgentMessageSequenceRestartKey).(bool)
		// See the matching comment in controller_execution.go: mint this step
		// its own "exec-<step>-<timestamp>" id so message-sequence item
		// notifications and this step's own tool-call events agree on the
		// same owner, instead of both falling back to whatever ambient
		// full-run id was set once at the top of the run.
		stepExecID := fmt.Sprintf("exec-%s-%d", sequenceExecutionStep.GetID(), time.Now().UnixNano())
		stepScopedCtx := virtualtools.WithBackgroundAgentID(ctx, stepExecID)
		stepScopedCtx = context.WithValue(stepScopedCtx, events.ParentExecutionIDKey, stepExecID)
		executionResult, capturedHistory, err := hcpo.executeMessageSequenceStep(
			stepScopedCtx,
			sequenceExecutionStep,
			stepIndex,
			subAgentStepPath,
			progress,
			localExecCtx,
			allSteps,
			messageSequenceCallOptions{
				Source:              "orchestrator_reentry",
				ContinuationMessage: strings.TrimSpace(delegationInstructions),
				Restart:             messageSequenceRestart,
			},
		)
		return executionResult, capturedHistory, err
	}

	executionResult, _, err := hcpo.executeSingleStep(
		ctx,
		stepToExecute,
		stepIndex,
		subAgentStepPath,
		1,
		0,
		[]string{},
		progress,
		true,
		localExecCtx,
		allSteps,
		true,
		[]string{},
		orchestrationRoutesForSubAgent,
	)
	return executionResult, capturedHistory, err
}

// executePredefinedSubAgent executes a predefined sub-agent for a todo task
// This uses the same execution pattern as orchestration steps (with learning/prevalidation)
func (hcpo *StepBasedWorkflowOrchestrator) executePredefinedSubAgent(
	ctx context.Context,
	step *OrchestratorPlanStep,
	stepIndex int,
	stepPath string,
	response *OrchestratorDecision,
	allSteps []PlanStepInterface,
	progress *StepProgress,
	humanInputs map[string]string,
) (string, []llmtypes.MessageContent, error) {
	ctx = withParentStepIndex(ctx, stepIndex)
	// Find the route
	var route *PlanOrchestrationRoute
	for i, r := range step.PredefinedRoutes {
		if r.RouteID == response.SelectedRouteID {
			route = &step.PredefinedRoutes[i]
			break
		}
	}
	if route == nil {
		return "", nil, fmt.Errorf("route %s not found in predefined routes", response.SelectedRouteID)
	}

	if route.SubAgentStep == nil {
		return "", nil, fmt.Errorf("route %s has no sub_agent_step defined", response.SelectedRouteID)
	}

	hcpo.GetLogger().Info(fmt.Sprintf("🤖 Executing predefined sub-agent: %s (%s)", route.RouteName, route.RouteID))
	parentTodoTitle := step.GetTitle()
	if parentTodoTitle == "" {
		parentTodoTitle = stepPath
	}

	// Use the sub-agent step from the route
	// CRITICAL: Create a COPY of the step to avoid modifying the original plan in memory
	// This keeps delegated instructions isolated from the original approved plan object.
	stepToExecute, err := cloneStepWithDelegationOverrides(
		route.SubAgentStep,
		response.InstructionsToSubAgent,
	)
	if err != nil {
		return "", nil, fmt.Errorf("failed to clone delegated sub-agent step: %w", err)
	}
	if err := validateOrchestratorNestingDepth(stepToExecute, nestedArtifactDelegationDepth(stepPath)+1); err != nil {
		return "", nil, fmt.Errorf("route %s exceeds supported todo_task nesting depth: %w", response.SelectedRouteID, err)
	}

	// The route owns one persistent session and every invocation owns a separate
	// calls/<call-id>/ output folder. Scripted routes live under scripts/routes;
	// conversational routes live under agents.
	callID := subAgentCallID(asyncSubAgentExecutionID(ctx), response.TodoIDToExecute, time.Now().UnixNano())
	isScriptedRoute := isScriptedStep(route.SubAgentStep, getAgentConfigs(route.SubAgentStep))
	subAgentStepPath := todoSubAgentArtifactFolderName(step.GetID(), stepPath, route.RouteID, callID, isScriptedRoute)
	messageSequenceRestart, _ := ctx.Value(virtualtools.SubAgentMessageSequenceRestartKey).(bool)
	if !isMessageSequenceStep(stepToExecute) || messageSequenceRestart {
		if err := hcpo.cleanupExecutionArtifactsForStepPath(ctx, subAgentStepPath, ""); err != nil {
			return "", nil, fmt.Errorf("failed to cleanup sub-agent output %q: %w", subAgentStepPath, err)
		}
	}
	// Build orchestration routes for sub-agent (so it knows about other agents)
	var orchestrationRoutesForSubAgent []OrchestrationRoute
	for _, r := range step.PredefinedRoutes {
		orchestrationRoutesForSubAgent = append(orchestrationRoutesForSubAgent, OrchestrationRoute{
			RouteID:      r.RouteID,
			RouteName:    r.RouteName,
			Condition:    r.Condition,
			SubAgentStep: r.SubAgentStep,
		})
	}

	// Execute the sub-agent step using executeSingleStep
	// This will include learning and prevalidation like regular orchestration sub-agents
	var capturedHistory []llmtypes.MessageContent
	// A run_full_workflow human_inputs entry keyed by this route's own step ID
	// (as shown in the plan JSON's sub_agent_step.id) reaches this turn's
	// prompt the same way it would for a top-level step of the same name.
	// Reusing executionContextForStep (rather than a second inline lookup)
	// keeps the scoping rule — and its test coverage — single-sourced.
	// Carrying the whole map forward, not just this one lookup, also means a
	// route that is itself a nested todo_task can resolve ITS OWN routes'
	// entries the identical way — nesting depth already caps how far this goes
	// (validateOrchestratorNestingDepth).
	execCtx := executionContextForStep(&ExecutionContext{HumanInputs: humanInputs}, stepToExecute.GetID())
	execCtx.SkipHumanInput = true // Sub-agents don't request human feedback
	execCtx.RunSingleStepOnly = false
	execCtx.SingleStepTarget = -1
	execCtx.ArtifactFolderNameOverride = subAgentStepPath
	execCtx.ConversationHistoryCapture = &capturedHistory
	if execCtx.WorkshopHumanInput != "" {
		hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Injecting human_input into route %q (step %q) prompt (%d chars)", route.RouteID, stepToExecute.GetID(), len(execCtx.WorkshopHumanInput)))
	}

	// Notify sub-agent start
	subAgentNotifID := asyncSubAgentExecutionID(ctx)
	if subAgentNotifID == "" {
		subAgentNotifID = fmt.Sprintf("todo-sub-%s-%d", subAgentStepPath, time.Now().UnixNano())
	}
	subAgentNotifName := fmt.Sprintf("%s -> %s (%s)", parentTodoTitle, route.RouteName, response.TodoIDToExecute)
	subAgentCtx, subAgentCancel := context.WithCancel(ctx)
	defer subAgentCancel()
	parentExecutionID := subAgentParentExecutionID(ctx)
	if hcpo.subAgentNotifier != nil {
		hcpo.subAgentNotifier.OnSubAgentStart(WorkshopExecutionStart{
			ID:                subAgentNotifID,
			ParentExecutionID: parentExecutionID,
			Name:              subAgentNotifName,
			Kind:              "workflow_sub_agent",
			Metadata: map[string]string{
				"async_parent_reconciles":    fmt.Sprintf("%t", asyncSubAgentExecutionID(ctx) != ""),
				"suppress_auto_notification": fmt.Sprintf("%t", asyncSubAgentExecutionID(ctx) != ""),
			},
			Cancel: subAgentCancel,
		})
	}
	subAgentCtx = virtualtools.WithBackgroundAgentID(subAgentCtx, subAgentNotifID)
	subAgentCtx = context.WithValue(subAgentCtx, events.ParentExecutionIDKey, subAgentNotifID)
	// A saved scripted route does not consume the augmented step description:
	// its fast path goes directly from main.py to the shell. Carry the dynamic
	// delegation separately so both that fast path and any authoring/repair
	// agent receive the exact per-call instructions without changing the stable
	// positional dependency arguments.
	subAgentCtx = withScriptedDelegationContext(
		subAgentCtx,
		route.RouteID,
		response.TodoIDToExecute,
		response.InstructionsToSubAgent,
		response.ScriptParameters,
	)

	// Bind this route's event identity to its own execution context. This also
	// composes correctly when the route itself is a nested todo_task.
	subAgentCtx = orchestrator.WithEventContextOverride(
		subAgentCtx,
		"execution",
		stepIndex,
		stepToExecute.GetID(),
		stepToExecute.GetTitle(),
		orchestrator.RichStepContext{
			StepName:     stepToExecute.GetTitle(),
			StepType:     effectiveRuntimeStepType(stepToExecute),
			StepIndex:    stepIndex + 1,
			ParentStepID: step.GetID(),
			TriggeredBy:  "todo_task_route",
		},
	)

	executionResult, capturedHistory, err := hcpo.executeRoutedSubAgentStep(
		subAgentCtx,
		stepToExecute,
		response.InstructionsToSubAgent,
		stepIndex,
		subAgentStepPath,
		progress,
		execCtx,
		allSteps,
		orchestrationRoutesForSubAgent,
	)

	// Notify sub-agent completion
	if hcpo.subAgentNotifier != nil {
		resultStr := fmt.Sprintf("Sub-agent %s completed: %s", route.RouteName, executionResult)
		if err != nil {
			resultStr = fmt.Sprintf("Sub-agent %s failed: %v", route.RouteName, err)
		}
		hcpo.subAgentNotifier.OnSubAgentComplete(subAgentNotifID, subAgentNotifName, resultStr, err)
	}

	if err != nil {
		return fmt.Sprintf("Sub-agent %s failed: %v", route.RouteName, err), capturedHistory, err
	}

	// A scripted route returns a value by writing route_result.json in its
	// output folder (PLAT-432). The caller gets that JSON instead of the run
	// summary; an unusable file is logged and the summary is kept.
	if isScriptedRoute {
		resultJSON, readErr := readScriptedRouteResult(hcpo.scriptedRouteOutputDir(subAgentStepPath))
		if readErr != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Route %s: %s ignored: %v", route.RouteID, ScriptedRouteResultFile, readErr))
			executionResult = fmt.Sprintf("%s\n(%s was ignored: %v)", executionResult, ScriptedRouteResultFile, readErr)
		}
		if resultJSON != "" {
			executionResult = resultJSON
			if capture, ok := ctx.Value(scriptedRouteResultCaptureKey{}).(*scriptedRouteResultCapture); ok && capture != nil {
				capture.json = resultJSON
			}
		}
	}

	result := fmt.Sprintf("Sub-agent %s completed: %s", route.RouteName, executionResult)
	return result, capturedHistory, nil
}

// emitOrchestratorRouteSelectedEvent emits an event when the todo task orchestrator selects a route/sub-agent
func (hcpo *StepBasedWorkflowOrchestrator) emitOrchestratorRouteSelectedEvent(
	ctx context.Context,
	step PlanStepInterface,
	stepIndex int,
	stepPath string,
	iteration int,
	response *OrchestratorDecision,
	executionLLM string,
) {
	bridge := hcpo.GetContextAwareBridge()
	if bridge == nil {
		return
	}

	// The todo list this event once summarized no longer exists; the task label
	// itself is the only title available. Field names stay for wire compatibility.
	todoTitle := response.TodoIDToExecute

	// Get route name if predefined route selected
	var selectedRouteName string
	if response.SelectedRouteID != "" {
		orchestratorStep, ok := step.(*OrchestratorPlanStep)
		if ok {
			for _, route := range orchestratorStep.PredefinedRoutes {
				if route.RouteID == response.SelectedRouteID {
					selectedRouteName = route.RouteName
					break
				}
			}
		}
	}

	// Extract preferred tier from context (set by call_sub_agent,
	// call_scripted_sub_agent, or call_generic_agent).
	var preferredTier int
	var preferredTierLabel string
	if tier, ok := ctx.Value(virtualtools.PreferredTierContextKey).(int); ok && tier >= 1 && tier <= 3 {
		preferredTier = tier
		preferredTierLabel = TierLevelLabel(TierLevel(tier))
	}

	event := &OrchestratorRouteSelectedEvent{
		BaseEventData: baseevents.BaseEventData{
			Timestamp: time.Now(),
			Component: "orchestrator",
		},
		StepIndex:              stepIndex,
		StepPath:               stepPath,
		StepID:                 step.GetID(),
		StepTitle:              step.GetTitle(),
		Iteration:              iteration + 1, // 1-based for display
		NextAction:             response.NextAction,
		SelectedRouteID:        response.SelectedRouteID,
		SelectedRouteName:      selectedRouteName,
		UseGenericAgent:        response.UseGenericAgent,
		TodoIDToExecute:        response.TodoIDToExecute,
		TodoTitle:              todoTitle,
		InstructionsToSubAgent: response.InstructionsToSubAgent,
		SelectionReasoning:     response.ProgressSummary, // Use progress summary as reasoning
		AllTasksComplete:       response.AllTasksComplete,
		ProgressSummary:        response.ProgressSummary,
		Model:                  executionLLM,
		PreferredTier:          preferredTier,
		PreferredTierLabel:     preferredTierLabel,
	}

	agentEvent := &baseevents.AgentEvent{
		Type:      events.OrchestratorRouteSelected,
		Timestamp: time.Now(),
		Data:      event,
	}

	if err := bridge.HandleEvent(ctx, agentEvent); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to emit todo task route selected event: %v", err))
	} else {
		hcpo.GetLogger().Info(fmt.Sprintf("📢 Emitted todo task route selected event: action=%s, route=%s, todo=%s",
			response.NextAction, response.SelectedRouteID, response.TodoIDToExecute))
	}
}

// emitOrchestratorStepCompletedEvent emits an event when the entire todo task step is completed
func (hcpo *StepBasedWorkflowOrchestrator) emitOrchestratorStepCompletedEvent(
	ctx context.Context,
	step PlanStepInterface,
	stepIndex int,
	stepPath string,
	totalIterations int,
	completionReason string,
	nextStepID string,
) {
	bridge := hcpo.GetContextAwareBridge()
	if bridge == nil {
		return
	}

	// No todo list exists any more; counts stay in the event for wire compatibility.
	totalTodos := 0
	completedCount := 0

	event := &OrchestratorStepCompletedEvent{
		BaseEventData: baseevents.BaseEventData{
			Timestamp: time.Now(),
			Component: "orchestrator",
		},
		StepIndex:        stepIndex,
		StepPath:         stepPath,
		StepID:           step.GetID(),
		StepTitle:        step.GetTitle(),
		TotalIterations:  totalIterations,
		TotalTodosCount:  totalTodos,
		CompletedCount:   completedCount,
		CompletionReason: completionReason,
		NextStepID:       nextStepID,
	}

	agentEvent := &baseevents.AgentEvent{
		Type:      events.OrchestratorStepCompleted,
		Timestamp: time.Now(),
		Data:      event,
	}

	if err := bridge.HandleEvent(ctx, agentEvent); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to emit todo task step completed event: %v", err))
	} else {
		hcpo.GetLogger().Info(fmt.Sprintf("📢 Emitted todo task step completed event: step=%s, iterations=%d, todos=%d/%d",
			stepPath, totalIterations, completedCount, totalTodos))
	}
}

func orchestratorExecutionLogFilename(retryAttempt, iteration int) string {
	return fmt.Sprintf("execution-attempt-%d-iteration-%d.json", retryAttempt, iteration)
}

// saveOrchestratorExecutionLog saves the execution log for a todo task iteration
// This allows the UI to show the full execution history (conversation, tool calls) for each iteration
func (hcpo *StepBasedWorkflowOrchestrator) saveOrchestratorExecutionLog(
	ctx context.Context,
	stepID string,
	stepPath string,
	retryAttempt int,
	iteration int,
	executionLLM string,
	conversationHistory []llmtypes.MessageContent,
	toolCalls []orchestrator.ToolCallEntry,
	llmCalls []orchestrator.LLMCallEntry,
	attemptStartedAt time.Time,
	attemptCompletedAt time.Time,
	attemptDuration time.Duration,
	executionSummaryOverride string,
) {
	// Use background context so logs are persisted even if execution was canceled.
	saveCtx := context.Background()

	// Get workspace path for logs folder
	var validationWorkspacePath string
	if hcpo.selectedRunFolder != "" {
		validationWorkspacePath = fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
	} else {
		validationWorkspacePath = hcpo.GetWorkspacePath()
	}

	// Get execution logs folder path
	executionLogsFolderPath := getExecutionFolderPathForLogs(validationWorkspacePath, stepID, stepPath)
	if attemptCompletedAt.IsZero() {
		attemptCompletedAt = time.Now().UTC()
	}
	if attemptStartedAt.IsZero() {
		attemptStartedAt = attemptCompletedAt.Add(-attemptDuration)
	}
	toolTiming := normalizeToolTimingEntries(toolCalls, attemptStartedAt)
	llmTiming := normalizeLLMTimingEntries(llmCalls, attemptStartedAt)
	traceSpans, timingBreakdown := buildTimingTrace(stepID, stepPath, executionLLM, attemptStartedAt, attemptCompletedAt, attemptDuration, llmTiming, toolTiming)
	timingData := map[string]interface{}{
		"schema_version": 2,
		"step_id":        stepID,
		"step_path":      stepPath,
		"run_folder":     hcpo.selectedRunFolder,
		"agent": map[string]interface{}{
			"model":                         executionLLM,
			"started_at":                    formatRFC3339UTC(attemptStartedAt),
			"completed_at":                  formatRFC3339UTC(attemptCompletedAt),
			"duration_ns":                   int64(attemptDuration),
			"duration_ms":                   durationToMillis(attemptDuration),
			"llm_call_count":                llmTiming.Count,
			"llm_duration_ms":               llmTiming.TotalDurationMs,
			"llm_time_to_first_response_ms": llmTiming.TimeToFirstResponseMs,
		},
		"llm":         llmTiming,
		"tools":       toolTiming,
		"trace_spans": traceSpans,
		"breakdown":   timingBreakdown,
	}

	filename := orchestratorExecutionLogFilename(retryAttempt, iteration)
	filePath := fmt.Sprintf("%s/%s", executionLogsFolderPath, filename)
	conversationPath := strings.TrimSuffix(filePath, ".json") + "-conversation.json"

	// Persist the latest assistant response as the compact execution result. The
	// old implementation concatenated assistant narration from the beginning of
	// the conversation and stopped at 2,000 bytes, which could omit the final
	// outcome and its CONCERNS line entirely.
	executionSummary := latestAssistantExecutionSummary(conversationHistory)
	if strings.TrimSpace(executionSummaryOverride) != "" {
		executionSummary = strings.TrimSpace(executionSummaryOverride)
	}

	// Build execution log entry
	executionLog := map[string]interface{}{
		"step_path":                     stepPath,
		"attempt":                       retryAttempt,
		"iteration":                     iteration,
		"model":                         executionLLM,
		"execution_result":              executionSummary,
		"message_count":                 len(conversationHistory),
		"started_at":                    formatRFC3339UTC(attemptStartedAt),
		"completed_at":                  formatRFC3339UTC(attemptCompletedAt),
		"duration_ms":                   durationToMillis(attemptDuration),
		"duration_ns":                   int64(attemptDuration),
		"llm_call_count":                llmTiming.Count,
		"llm_duration_ms":               llmTiming.TotalDurationMs,
		"llm_time_to_first_response_ms": llmTiming.TimeToFirstResponseMs,
		"tool_call_count":               toolTiming.Count,
		"tool_duration_ms":              toolTiming.TotalDurationMs,
		"tracked_union_duration_ms":     timingBreakdown.TrackedUnionDurationMs,
		"untracked_duration_ms":         timingBreakdown.UntrackedDurationMs,
		"total_input_tokens":            timingBreakdown.TotalInputTokens,
		"total_output_tokens":           timingBreakdown.TotalOutputTokens,
		"total_tokens":                  timingBreakdown.TotalTokens,
		"tool_args_bytes":               timingBreakdown.ToolArgsBytes,
		"tool_result_bytes":             timingBreakdown.ToolResultBytes,
		"timing":                        timingData,
		"timestamp":                     attemptCompletedAt.Format(time.RFC3339),
	}

	// Marshal to JSON
	logJSON, err := json.MarshalIndent(executionLog, "", "  ")
	if err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to marshal todo task execution log: %v", err))
		return
	}

	// Write to file
	if err := hcpo.WriteWorkspaceFile(saveCtx, filePath, string(logJSON)); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to save todo task execution log to %s: %v", filePath, err))
	} else {
		hcpo.GetLogger().Info(fmt.Sprintf("💾 Todo task execution log saved to: %s", filePath))
	}

	// Save the full conversation history and tool calls,
	// so the execution popup can open it via the inferred conversation_path.
	conversationLog := map[string]interface{}{
		"step_path":            stepPath,
		"retry_attempt":        1,
		"loop_iteration":       iteration,
		"conversation_history": conversationHistory,
		"llm_calls":            llmCalls,
		"tool_calls":           toolCalls,
		"llm_call_count":       llmTiming.Count,
		"tool_call_count":      len(toolCalls),
		"timing":               timingData,
		"timestamp":            attemptCompletedAt.Format(time.RFC3339),
	}
	conversationJSON, err := json.MarshalIndent(conversationLog, "", "  ")
	if err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to marshal todo task conversation log: %v", err))
		return
	}

	if err := hcpo.WriteWorkspaceFile(saveCtx, conversationPath, string(conversationJSON)); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to save todo task conversation log to %s: %v", conversationPath, err))
	} else {
		hcpo.GetLogger().Info(fmt.Sprintf("💬 Todo task conversation log saved to: %s", conversationPath))
	}

	timingPath := strings.TrimSuffix(filePath, ".json") + "-timing.json"
	timingJSON, err := json.MarshalIndent(timingData, "", "  ")
	if err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to marshal todo task timing log: %v", err))
		return
	}
	if err := hcpo.WriteWorkspaceFile(saveCtx, timingPath, string(timingJSON)); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to save todo task timing log to %s: %v", timingPath, err))
	} else {
		hcpo.GetLogger().Info(fmt.Sprintf("⏱️ Todo task timing log saved to: %s", timingPath))
	}
}

func latestAssistantExecutionSummary(conversationHistory []llmtypes.MessageContent) string {
	for i := len(conversationHistory) - 1; i >= 0; i-- {
		msg := conversationHistory[i]
		if msg.Role != llmtypes.ChatMessageTypeAI {
			continue
		}
		var parts []string
		for _, part := range msg.Parts {
			if textContent, ok := part.(llmtypes.TextContent); ok {
				if text := strings.TrimSpace(textContent.Text); text != "" {
					parts = append(parts, text)
				}
			}
		}
		if len(parts) > 0 {
			return summarizeExecutionResultForNotification(strings.Join(parts, "\n"))
		}
	}
	return ""
}
