package step_based_workflow

import (
	"context"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowkb"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Whole-KB maintenance orchestration: the reorganize and consolidate phases invoked
// by their builder tools. Both are serialized through kbUpdateQueue so they can't
// race each other on knowledgebase/notes/ writes.
//
// The per-step post-step KB update agent that also used this queue is retired — see
// the agent-mode retirement. Step-level KB writes happen inline in the step
// agent's own turn under direct mode.

// runKBReorganizePhase is invoked by the reorganize_knowledgebase builder tool via
// kbUpdateQueue. Returns the agent's final summary line.
func (hcpo *StepBasedWorkflowOrchestrator) runKBReorganizePhase(ctx context.Context, instruction string) (string, error) {
	if shared, _ := workflowkb.SharedConfig(GetPromptDocsRoot(), hcpo.GetWorkspacePath()); shared {
		return "", fmt.Errorf("shared Knowledge Base maintenance must use its MCP tools")
	}
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return "", fmt.Errorf("instruction is required")
	}

	hcpo.GetLogger().Info(fmt.Sprintf("🧹 Starting KB reorganize (instruction=%d chars)", len(instruction)))

	nano := time.Now().UnixNano()
	agentSessionID := fmt.Sprintf("kb-reorganize-%d", nano)
	ctx = context.WithValue(ctx, orchestratorevents.AgentSessionIDKey, agentSessionID)
	ctx = context.WithValue(ctx, orchestratorevents.ForceCorrelationIDKey, agentSessionID)
	ctx = context.WithValue(ctx, orchestratorevents.IsSubAgentContextKey, true)

	docsRoot := GetPromptDocsRoot()
	baseWorkspacePath := hcpo.GetWorkspacePath()
	notesFolderPath := filepath.Join(docsRoot, baseWorkspacePath, KnowledgebaseFolderName, KBNotesFolderName)
	notesIndexPath := filepath.Join(notesFolderPath, KBNotesIndexFileName)

	agentName := fmt.Sprintf("kb-reorganize-%d", nano)
	agent, err := hcpo.createKBReorganizeAgent(ctx, "kb_reorganize", agentName, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create KB reorganize agent: %w", err)
	}

	templateVars := map[string]string{
		"Instruction":     instruction,
		"NotesFolderPath": notesFolderPath,
		"NotesIndexPath":  notesIndexPath,
	}

	result, _, err := agent.Execute(ctx, templateVars, []llmtypes.MessageContent{})
	if err != nil {
		return "", fmt.Errorf("KB reorganize agent execution failed: %w", err)
	}
	summary := lastNonEmptyLine(result)
	if summary != "" {
		hcpo.GetLogger().Info(fmt.Sprintf("🧹 %s", summary))
	}
	return summary, nil
}

// RunKBReorganize enqueues a reorganize job through kbUpdateQueue and blocks until the
// worker finishes. Serializing against kbUpdateQueue prevents races with live post-step
// updates. Returns early if ctx is canceled (e.g. workshop session closed).
//
// Wraps the job in a tracked WorkshopStepExecution so the workshop UI surfaces it
// alongside learning, harden, replan, etc. — same pattern as maybeEnqueueKBUpdate.
func (hcpo *StepBasedWorkflowOrchestrator) RunKBReorganize(ctx context.Context, instruction string) (string, error) {
	type reorgResult struct {
		summary string
		err     error
	}

	execLabel := "KB Reorganize"
	execID := fmt.Sprintf("kb-reorganize-%05d", time.Now().UnixNano()%100000)
	jobCtx, cancel := context.WithCancel(context.Background())
	if hcpo.workshopStepRegistry != nil {
		exec := &WorkshopStepExecution{
			ID:             execID,
			StepID:         execLabel,
			AgentSessionID: "",
			Status:         WorkshopStepRunning,
			cancel:         cancel,
		}
		hcpo.workshopStepRegistry.Register(exec)
	}
	if hcpo.workshopExecutionNotifier != nil {
		hcpo.workshopExecutionNotifier.OnExecutionStart(WorkshopExecutionStart{
			ID:                execID,
			ParentExecutionID: currentWorkshopParentExecutionID(ctx),
			Name:              execLabel,
			Cancel:            cancel,
		})
	}

	done := make(chan reorgResult, 1)
	enqueueKBUpdateJob(func() {
		summary, err := hcpo.runKBReorganizePhase(jobCtx, instruction)
		done <- reorgResult{summary: summary, err: err}
	})

	finalize := func(summary string, err error) {
		cancel()
		if hcpo.workshopExecutionNotifier != nil {
			resultMsg := summary
			if err != nil {
				resultMsg = fmt.Sprintf("KB reorganize failed: %v", err)
			} else if resultMsg == "" {
				resultMsg = "KB reorganize completed"
			}
			hcpo.workshopExecutionNotifier.OnExecutionComplete(execID, execLabel, resultMsg, nil, err)
		}
	}

	select {
	case r := <-done:
		finalize(r.summary, r.err)
		return r.summary, r.err
	case <-ctx.Done():
		finalize("", ctx.Err())
		return "", ctx.Err()
	}
}

func lastNonEmptyLine(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// runKBConsolidatePhase is invoked by the consolidate_knowledgebase builder tool via
// kbUpdateQueue. Gathers every step's knowledgebase_contribution + the list of step
// output folders from the selected run, renders them into the consolidate agent's user
// message, and runs the agent. Returns the agent's final summary line.
func (hcpo *StepBasedWorkflowOrchestrator) runKBConsolidatePhase(ctx context.Context, objective string) (string, error) {
	if shared, _ := workflowkb.SharedConfig(GetPromptDocsRoot(), hcpo.GetWorkspacePath()); shared {
		return "", fmt.Errorf("shared Knowledge Base maintenance must use its MCP tools")
	}
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return "", fmt.Errorf("objective is required")
	}

	hcpo.GetLogger().Info(fmt.Sprintf("🧩 Starting KB consolidate (objective=%d chars)", len(objective)))

	nano := time.Now().UnixNano()
	agentSessionID := fmt.Sprintf("kb-consolidate-%d", nano)
	ctx = context.WithValue(ctx, orchestratorevents.AgentSessionIDKey, agentSessionID)
	ctx = context.WithValue(ctx, orchestratorevents.ForceCorrelationIDKey, agentSessionID)
	ctx = context.WithValue(ctx, orchestratorevents.IsSubAgentContextKey, true)

	docsRoot := GetPromptDocsRoot()
	baseWorkspacePath := hcpo.GetWorkspacePath()
	notesFolderPath := filepath.Join(docsRoot, baseWorkspacePath, KnowledgebaseFolderName, KBNotesFolderName)
	notesIndexPath := filepath.Join(notesFolderPath, KBNotesIndexFileName)

	// Consolidation is a builder operation, not a continuation of whichever
	// execution last ran in this session. Read current declarations explicitly.
	plan, err := hcpo.ReadCurrentPlan(ctx)
	if err != nil {
		return "", fmt.Errorf("load current KB contribution plan: %w", err)
	}
	configs, err := hcpo.ReadStepConfigsFromSubdir(ctx, "planning")
	if err != nil {
		return "", fmt.Errorf("load current KB contribution configuration: %w", err)
	}
	for _, info := range collectAllSteps(plan.Steps) {
		if err := populateRuntimeFields(info.Step, configs); err != nil {
			return "", err
		}
	}
	contributionsBlock := hcpo.buildKBContributionsBlock(plan)
	stepOutputFoldersBlock := hcpo.buildStepOutputFoldersBlock(docsRoot, baseWorkspacePath)

	agentName := fmt.Sprintf("kb-consolidate-%d", nano)
	agent, err := hcpo.createKBConsolidateAgent(ctx, "kb_consolidate", agentName, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create KB consolidate agent: %w", err)
	}

	templateVars := map[string]string{
		"Objective":              objective,
		"ContributionsBlock":     contributionsBlock,
		"StepOutputFoldersBlock": stepOutputFoldersBlock,
		"NotesFolderPath":        notesFolderPath,
		"NotesIndexPath":         notesIndexPath,
	}

	result, _, err := agent.Execute(ctx, templateVars, []llmtypes.MessageContent{})
	if err != nil {
		return "", fmt.Errorf("KB consolidate agent execution failed: %w", err)
	}
	summary := lastNonEmptyLine(result)
	if summary != "" {
		hcpo.GetLogger().Info(fmt.Sprintf("🧩 %s", summary))
	}
	return summary, nil
}

// RunKBConsolidate enqueues a consolidate job through kbUpdateQueue and blocks until the
// worker finishes. Serialized against per-step KB updates AND reorganize so graph/notes
// writes can't interleave. Returns early if ctx is canceled (e.g. workshop session closed).
//
// Wraps the job in a tracked WorkshopStepExecution so the workshop UI surfaces it
// alongside learning, harden, reorganize, etc.
func (hcpo *StepBasedWorkflowOrchestrator) RunKBConsolidate(ctx context.Context, objective string) (string, error) {
	type consolidateResult struct {
		summary string
		err     error
	}

	execLabel := "KB Consolidate"
	execID := fmt.Sprintf("kb-consolidate-%05d", time.Now().UnixNano()%100000)
	jobCtx, cancel := context.WithCancel(context.Background())
	if hcpo.workshopStepRegistry != nil {
		exec := &WorkshopStepExecution{
			ID:             execID,
			StepID:         execLabel,
			AgentSessionID: "",
			Status:         WorkshopStepRunning,
			cancel:         cancel,
		}
		hcpo.workshopStepRegistry.Register(exec)
	}
	if hcpo.workshopExecutionNotifier != nil {
		hcpo.workshopExecutionNotifier.OnExecutionStart(WorkshopExecutionStart{
			ID:                execID,
			ParentExecutionID: currentWorkshopParentExecutionID(ctx),
			Name:              execLabel,
			Cancel:            cancel,
		})
	}

	done := make(chan consolidateResult, 1)
	enqueueKBUpdateJob(func() {
		summary, err := hcpo.runKBConsolidatePhase(jobCtx, objective)
		done <- consolidateResult{summary: summary, err: err}
	})

	finalize := func(summary string, err error) {
		cancel()
		if hcpo.workshopExecutionNotifier != nil {
			resultMsg := summary
			if err != nil {
				resultMsg = fmt.Sprintf("KB consolidate failed: %v", err)
			} else if resultMsg == "" {
				resultMsg = "KB consolidate completed"
			}
			hcpo.workshopExecutionNotifier.OnExecutionComplete(execID, execLabel, resultMsg, nil, err)
		}
	}

	select {
	case r := <-done:
		finalize(r.summary, r.err)
		return r.summary, r.err
	case <-ctx.Done():
		finalize("", ctx.Err())
		return "", ctx.Err()
	}
}

// buildKBContributionsBlock produces a deterministic markdown block listing every step's
// declared knowledgebase_contribution. This is what the consolidate agent uses as the
// "declared schema" across the workflow — any type-name / property-name drift between
// these strings is what it should reconcile.
func (hcpo *StepBasedWorkflowOrchestrator) buildKBContributionsBlock(plan *PlanningResponse) string {
	if plan == nil || len(plan.Steps) == 0 {
		return "_No plan loaded — cannot enumerate step contributions._"
	}
	var sb strings.Builder
	count := 0
	for _, s := range plan.Steps {
		if s == nil {
			continue
		}
		cfg := getAgentConfigs(s)
		if cfg == nil {
			continue
		}
		contrib := strings.TrimSpace(cfg.KnowledgebaseContribution)
		if contrib == "" {
			continue
		}
		count++
		access := strings.TrimSpace(cfg.KnowledgebaseAccess)
		if access == "" {
			access = "none"
		}
		sb.WriteString(fmt.Sprintf("### step: %s — %s\n", s.GetID(), s.GetTitle()))
		sb.WriteString(fmt.Sprintf("- access: `%s`\n", access))
		sb.WriteString("- contribution:\n")
		for _, line := range strings.Split(contrib, "\n") {
			sb.WriteString("  > ")
			sb.WriteString(line)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	if count == 0 {
		return "_No steps in this workflow have a non-empty `knowledgebase_contribution`. Consolidation has nothing to reconcile from declared schema — fall back to reading graph.json + notes directly._"
	}
	header := fmt.Sprintf("_%d step(s) declare a knowledgebase_contribution. Review for type-name / property-name drift._\n\n", count)
	return header + sb.String()
}

// buildStepOutputFoldersBlock lists the step output folders under the selected run.
// These are ABSOLUTE paths the agent can `cat` into, but it MUST pick targeted files —
// don't glob the lot. We list folders not files so the agent doesn't pre-commit to
// reading everything; it discovers files on demand.
func (hcpo *StepBasedWorkflowOrchestrator) buildStepOutputFoldersBlock(docsRoot, baseWorkspacePath string) string {
	runFolder := hcpo.selectedRunFolder
	if runFolder == "" {
		return "_No run folder selected — consolidate without step output access. Read graph.json + notes directly and rely on the contributions block for cross-step patterns._"
	}
	executionPath := filepath.Join(docsRoot, baseWorkspacePath, "runs", runFolder, "execution")
	entries, err := os.ReadDir(executionPath)
	if err != nil {
		return fmt.Sprintf("_Selected run `%s` has no execution folder at `%s` (%v). Read notes/_index.json + notes/ directly._", runFolder, executionPath, err)
	}
	var folders []string
	for _, e := range entries {
		if e.IsDir() {
			folders = append(folders, e.Name())
		}
	}
	sort.Strings(folders)
	if len(folders) == 0 {
		return fmt.Sprintf("_Run `%s` execution folder is empty (`%s`)._", runFolder, executionPath)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Run folder: `%s`\nExecution root: `%s`\n\nStep output folders (cat specific files inside as needed; DO NOT glob-read):\n", runFolder, executionPath))
	for _, name := range folders {
		sb.WriteString(fmt.Sprintf("- `%s/%s/`\n", executionPath, name))
	}
	return sb.String()
}
