package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type messageSequenceFolderGuardOverrideKey struct{}
type messageSequenceRuntimeSessionOverrideKey struct{}

type messageSequenceFolderGuardOverride struct {
	ReadPaths  []string
	WritePaths []string
	OutputPath string
}

type messageSequenceRuntimeSessionOverride struct {
	SessionID string
	KeepAlive bool
}

type messageSequenceSession struct {
	SessionID           string                    `json:"session_id"`
	StepID              string                    `json:"step_id"`
	RunFolder           string                    `json:"run_folder"`
	Status              string                    `json:"status"`
	CreatedAt           time.Time                 `json:"created_at"`
	UpdatedAt           time.Time                 `json:"updated_at"`
	ConversationHistory []llmtypes.MessageContent `json:"conversation_history"`
	LastRuntimeContext  string                    `json:"last_runtime_context,omitempty"`
	RuntimeSessionID    string                    `json:"runtime_session_id,omitempty"`
	ExecutionTurnCount  int                       `json:"execution_turn_count,omitempty"`
	Entries             []messageSequenceEntry    `json:"entries,omitempty"`

	runtime *messageSequenceRuntime
	// delegation is set when this sequence is a todo_task orchestrator running on
	// the shared executor. Never serialized: it holds live runtime hooks.
	delegation   *messageSequenceDelegation
	scriptedPlan *PlanningResponse
	// referencedGuides is the PLAT-556 "Referenced guides" block, built once
	// per sequence so every turn carries the same system prompt.
	referencedGuides *string
}

type messageSequenceRuntime struct {
	Agent     agents.OrchestratorAgent
	SessionID string
	Provider  string
}

type messageSequenceEntry struct {
	EntryID   string    `json:"entry_id"`
	ItemID    string    `json:"item_id,omitempty"`
	ItemType  string    `json:"item_type,omitempty"`
	Source    string    `json:"source"`
	Status    string    `json:"status"`
	Summary   string    `json:"summary,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

type messageSequenceCallOptions struct {
	Source              string
	ReentryMessage      string
	ContinuationMessage string
	Restart             bool
	// Delegation turns this sequence into a todo_task orchestrator: the agent owns
	// predefined routes and the sub-agent tools, every conversational turn is
	// followed by a reconcile of the async children it launched, and the step
	// cannot finish while a child is still running. nil for a plain sequence.
	Delegation *messageSequenceDelegation
}

// messageSequenceDelegation is the seam that lets a todo_task orchestrator run on
// the message_sequence executor instead of its own turn loop. The executor owns
// the item queue, prevalidation repair loop, final validation gate, closing
// reflection turn, session log, and stop/halt handling; the delegation hooks own
// what is orchestrator-specific: which agent runs (the orchestrator agent type
// with sub-agent tools — PLAT-027's progress bridge keys on that type), the
// narrower folder guard, the orchestrator's own prompt variables, and the
// todo_task execution-log shape ExecutionLogsPopup reads.
type messageSequenceDelegation struct {
	// ExecCtx owns the async children. Sub-agent tools exist only when it is set.
	ExecCtx *SubAgentExecutionContext
	// ReadPaths/WritePaths replace setupMessageSequenceFolderGuard for the
	// orchestrator: execution folder + db, never the workflow root.
	ReadPaths  []string
	WritePaths []string
	// CreateAgent builds the orchestrator agent once; the executor reuses it for
	// every turn of the sequence.
	CreateAgent func(ctx context.Context) (agents.OrchestratorAgent, error)
	// TurnVars returns the orchestrator's template variables for one turn. The
	// durable description remains in the common system prompt; every item is
	// carried as a user message.
	TurnVars func(item MessageSequenceItem, message string, opening bool) map[string]string
	// LogTurn persists one turn in the todo_task execution-log shape. turnNumber
	// is 1-based.
	LogTurn func(ctx context.Context, turnNumber int, executionLLM string, history []llmtypes.MessageContent, toolCalls []orchestrator.ToolCallEntry, llmCalls []orchestrator.LLMCallEntry, startedAt, completedAt time.Time)
}

// messageSequenceDelegationItemAllowed reports whether an item kind may run on an
// delegating agent sequence. Inline code/file items remain forbidden, while a
// declared scripted batch is safe because it can only invoke validated saved
// regular-script definitions and never creates an agent session.
func messageSequenceDelegationItemAllowed(item MessageSequenceItem) bool {
	switch strings.TrimSpace(item.Type) {
	case "", "user_message", "foreach", "prevalidation", "scripted":
		return true
	}
	return false
}

// lastAssistantText returns the full text of the most recent assistant message,
// or "" when there is none. Unlike latestAssistantExecutionSummary it is not
// truncated, so STATUS lines emitted after a completion batch are preserved.
func lastAssistantText(history []llmtypes.MessageContent) string {
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
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
			return strings.Join(parts, "\n")
		}
	}
	return ""
}

func messageSequenceContinuationMessage(opts messageSequenceCallOptions) string {
	if msg := strings.TrimSpace(opts.ContinuationMessage); msg != "" {
		return msg
	}
	return strings.TrimSpace(opts.ReentryMessage)
}

const normalizedRegularSequenceItemID = "execute-and-verify"

// normalizeRegularStepToMessageSequence routes persisted non-scripted regular
// steps through the canonical conversational runtime. New plans author these as
// message_sequence directly; this normalization keeps existing workflows
// executable without reviving the removed direct regular-agent path.
func normalizeRegularStepToMessageSequence(step *RegularPlanStep) *MessageSequencePlanStep {
	if step == nil {
		return nil
	}
	return &MessageSequencePlanStep{
		Type:             StepTypeMessageSeq,
		CommonStepFields: step.CommonStepFields,
		NextStepID:       step.NextStepID,
		Items: []MessageSequenceItem{{
			ID:   normalizedRegularSequenceItemID,
			Type: "user_message",
			Kind: "execution",
			Message: "Complete the step described above. Re-open the produced evidence, verify the result against every stated requirement, " +
				"repair any gap you find, and finish only when the step is complete.",
		}},
		AgentConfigs: step.AgentConfigs,
	}
}

// shouldNormalizeRegularStepToMessageSequence is the transitional shim: only a
// regular step whose unstripped step_config.json still declares the retired
// agentic mode runs as a message_sequence. Every other regular step is
// scripted (PLAT-287). Retire together with AgentConfigs.legacyDeclaredMode.
func shouldNormalizeRegularStepToMessageSequence(step PlanStepInterface) bool {
	return isLegacyAgenticRegularStep(step, getAgentConfigs(step))
}

// normalizeMessageSequenceStepToRegular is normalizeRegularStepToMessageSequence's
// mirror: a message_sequence step whose step_config already declares scripted
// mode (PLAT-280 — a checked-in learnings/{step-id}/main.py that needs the real
// scripted executor's $DB_PATH/STEP_OUTPUT_DIR injection, which the
// message_sequence runtime does not reliably provide) has no in-place way to
// become a true `regular` plan step. Items carry no information a scripted step
// can use — its work lives entirely in the checked-in script — so they are
// dropped, not migrated.
func normalizeMessageSequenceStepToRegular(step *MessageSequencePlanStep) *RegularPlanStep {
	if step == nil {
		return nil
	}
	return &RegularPlanStep{
		Type:             StepTypeRegular,
		CommonStepFields: step.CommonStepFields,
		NextStepID:       step.NextStepID,
		AgentConfigs:     step.AgentConfigs,
	}
}

// effectiveRuntimeStepType reports the execution model users actually see.
// Persisted non-scripted regular steps run through message_sequence for
// compatibility, so exposing them as "regular" in terminal metadata is
// misleading even though the saved plan still has the legacy type.
func effectiveRuntimeStepType(step PlanStepInterface) string {
	if shouldNormalizeRegularStepToMessageSequence(step) {
		return string(StepTypeMessageSeq)
	}
	if step == nil {
		return ""
	}
	return string(step.StepType())
}

// messageSequenceClosingItems builds synthetic trailing items so a standalone
// message_sequence honors its step-level learning_objective and
// knowledgebase_contribution — the same post-step learnings/KB a regular step
// runs (a message_sequence otherwise skips the learning/KB phase entirely). Each
// is a user_message turn carrying the matching write access; the item machinery
// already grants learnings/_global or notes/ from kind + write_access.
func (hcpo *StepBasedWorkflowOrchestrator) messageSequenceClosingItems(ctx context.Context, seq *MessageSequencePlanStep, stepIndex int) []MessageSequenceItem {
	cfg := seq.AgentConfigs
	if cfg == nil || !hcpo.platformStoresEnabled() {
		return nil
	}
	var items []MessageSequenceItem
	stepID := seq.GetID()
	desc := seq.GetDescription()

	// PLAT-055. One reflection item, not a learnings item followed by a
	// knowledgebase item. Appending them separately reproduced the same defect
	// the regular-step path had: the agent chose a destination based on which
	// turn it happened to be in rather than on which store owns the content.
	learningsDue := hcpo.shouldDirectWriteLearnings(cfg, seq)
	kbAccess := resolveKnowledgebaseAccess(cfg, hcpo.UseKnowledgebase())
	kbContribution := strings.TrimSpace(kbContributionForPrompt(cfg))
	// Under write_method=agent the constraint layer strips KB from every item's
	// guard, so a KB item here would be a guaranteed-denied write; agent-mode
	// contributions are handled by maybeEnqueueKBUpdate after the sequence.
	kbDue := kbContribution != "" && kbAccessAllowsWrite(kbAccess)

	input := StepReflectionTurnInput{
		StepID:              stepID,
		StepDescription:     desc,
		LearningsTargetPath: hcpo.directLearningsPromptTargetPath(),
		HasBrowserAccess:    hcpo.HasBrowserCapability(),
		DBTableNames:        hcpo.reflectionDBTableNames(ctx),
	}
	if learningsDue {
		input.LearningObjective = strings.TrimSpace(cfg.LearningObjective)
		input.SkillIndexLines = hcpo.reflectionSkillIndexLines(ctx)
	}
	if kbDue {
		input.KBAccess = kbAccess
		input.KBContribution = kbContribution
	}

	if msg := BuildStepReflectionTurn(input); msg != "" {
		items = append(items, MessageSequenceItem{
			ID:          fmt.Sprintf("%s-reflection", stepID),
			Type:        "user_message",
			Kind:        "learning",
			Title:       "Reflection",
			Message:     msg,
			WriteAccess: MessageSequenceWriteAccess{Learnings: learningsDue, Knowledgebase: kbDue},
		})
	}
	return items
}

// Jump-repeat limits for next_step_id navigation, per step kind. LLM-driven
// steps get a tight cap because every extra cycle burns tokens; human_input
// loops are inherently self-limiting (each pass blocks on a human response),
// so their cap is only a failsafe against auto-responders. Routing passes 0:
// it has its own per-route evaluation guard.
const (
	maxLLMJumpRepeats   = 5
	maxHumanJumpRepeats = 25
)

// navigateToNextStepID advances the execution loop to the step whose ID matches
// nextStepID, mirroring routing's navigation so any route can converge to a
// shared downstream step (sibling steps between here and the target are skipped).
// sourceStepID identifies the jumping step for loop accounting; maxRepeats > 0
// bounds how many times the same source→target jump may fire within one run
// (the counter is in-memory for the run, like RoutingEvaluationCounts). Pass 0
// to disable the guard when the caller has its own loop protection.
// Returns: "end" (nextStepID=="end" — caller should break the loop), "jump" (i was
// repointed to land on the target next iteration; caller should continue), or
// "none" (empty/unknown id — caller falls through to the next sequential step),
// plus a non-nil error when the jump-repeat limit is exceeded — the workflow
// should terminate rather than keep cycling.
func (hcpo *StepBasedWorkflowOrchestrator) navigateToNextStepID(ctx context.Context, sourceStepID, nextStepID string, breakdownSteps []PlanStepInterface, progress *StepProgress, i *int, startFromStep *int, maxRepeats int) (string, error) {
	nextStepID = strings.TrimSpace(nextStepID)
	if nextStepID == "" {
		return "none", nil
	}
	if nextStepID == "end" {
		return "end", nil
	}
	targetStepIndex := -1
	for idx, s := range breakdownSteps {
		if s.GetID() == nextStepID {
			targetStepIndex = idx
			break
		}
	}
	if targetStepIndex < 0 {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ next_step_id %q not found in plan - falling through to next sequential step", nextStepID))
		return "none", nil
	}
	if maxRepeats > 0 {
		if progress.JumpCounts == nil {
			progress.JumpCounts = make(map[string]int)
		}
		jumpKey := sourceStepID + "->" + nextStepID
		progress.JumpCounts[jumpKey]++
		if count := progress.JumpCounts[jumpKey]; count > maxRepeats {
			errMsg := fmt.Sprintf("infinite loop detected: step %q has jumped to next_step_id %q %d times in this run (limit %d)", sourceStepID, nextStepID, count, maxRepeats)
			hcpo.GetLogger().Error(errMsg, nil)
			hcpo.EmitOrchestratorAgentError(ctx, "workflow", "next-step-id-loop-detection", fmt.Sprintf("Jump from step %s", sourceStepID), errMsg, *i, 0)
			return "none", fmt.Errorf("workflow error: %s", errMsg)
		}
	}
	hcpo.GetLogger().Info(fmt.Sprintf("🔗 Jumping to step %d (ID: %s) as specified by next_step_id", targetStepIndex+1, nextStepID))
	if err := hcpo.cleanupProgressFromStep(ctx, targetStepIndex, progress); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to cleanup progress from step %d: %v (continuing anyway)", targetStepIndex+1, err))
	}
	runNumber := hcpo.getNextArchivalRunNumber(ctx, progress, targetStepIndex+1)
	for stepNum := targetStepIndex + 1; stepNum <= len(breakdownSteps); stepNum++ {
		if err := hcpo.archiveStepExecutionFolder(ctx, stepNum, runNumber); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to archive execution folder for step %d: %v", stepNum, err))
		}
	}
	if err := hcpo.saveStepProgress(ctx, progress); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to save progress after next_step_id navigation: %v", err))
	}
	if targetStepIndex < *startFromStep {
		*startFromStep = targetStepIndex
	}
	*i = targetStepIndex - 1
	return "jump", nil
}

func (hcpo *StepBasedWorkflowOrchestrator) executeMessageSequenceStep(
	ctx context.Context,
	step PlanStepInterface,
	stepIndex int,
	stepPath string,
	progress *StepProgress,
	execCtx *ExecutionContext,
	allSteps []PlanStepInterface,
	opts messageSequenceCallOptions,
) (string, []llmtypes.MessageContent, error) {
	_ = progress
	_ = allSteps
	ctx = withParentStepIndex(ctx, stepIndex)

	sequenceStep, ok := step.(*MessageSequencePlanStep)
	if !ok {
		return "", nil, fmt.Errorf("step %q is not a message_sequence step", step.GetID())
	}
	if stepPath == "" {
		stepPath = fmt.Sprintf("step-%d", stepIndex+1)
	}

	// How a message_sequence behaves depends on how it is invoked:
	//   - ROUTE (Source=="orchestrator_reentry"): the todo_task orchestrator re-enters the
	//     same specialist across calls within one run. The conversation is kept in an
	//     in-memory cache on this orchestrator so it remembers prior calls. This memory is
	//     NEVER read back from disk — it lives only for the lifetime of the run.
	//   - STANDALONE (top-level step / workshop): a fixed item queue that runs once. No
	//     memory and no re-entry — re-running simply re-runs the queue.
	// session.json is intentionally a one-way observability log, not a resume checkpoint.
	// If the backend or workflow run is interrupted, that run is abandoned and a fresh run
	// starts the workflow from the beginning with a new execution ID. Mid-sequence recovery
	// is deliberately unsupported; steps that produce external side effects must follow the
	// same idempotency/deduplication contract as every other rerunnable workflow step.
	isRoute := opts.Source == "orchestrator_reentry"
	if opts.Delegation != nil {
		if isRoute {
			return "", nil, fmt.Errorf("step %q: a delegating sequence cannot be a route re-entry", step.GetID())
		}
		for _, item := range sequenceStep.Items {
			if !messageSequenceDelegationItemAllowed(item) {
				return "", nil, fmt.Errorf("delegating agent step %q item %q has unsupported type %q; use user_message, foreach, prevalidation, or a declared scripted batch", step.GetID(), item.ID, item.Type)
			}
		}
		if opts.Delegation.ExecCtx != nil {
			// The step must never return while a child it owns is still running,
			// whatever the exit path (PLAT-082's failure-path backstop).
			defer func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := opts.Delegation.ExecCtx.cancelOutstandingAndWait(cleanupCtx); err != nil {
					hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Orchestrator step %s could not fully stop owned sub-agents during cleanup: %v", step.GetID(), err))
				}
			}()
		}
	}
	routeKey := hcpo.msgSeqRouteKey(stepPath, sequenceStep.GetID())
	sessionRelPath := hcpo.messageSequenceSessionPath(stepPath, sequenceStep.GetID())
	if isRoute {
		unlockRoute := hcpo.lockMsgSeqRoute(routeKey)
		defer unlockRoute()
	}

	if isRoute && opts.Restart {
		hcpo.clearMsgSeqRouteSession(routeKey)
		if err := hcpo.cleanupMessageSequenceRuntime(ctx, stepPath, sequenceStep.GetID()); err != nil {
			return "", nil, err
		}
	}

	var existing *messageSequenceSession
	var hasExisting bool
	if isRoute && !opts.Restart {
		existing, hasExisting = hcpo.loadMsgSeqRouteSession(routeKey)
	}

	var session *messageSequenceSession
	var plannedItems []MessageSequenceItem
	source := opts.Source
	if source == "" {
		source = "configured_queue"
	}
	if hasExisting {
		// Route re-entry: continue the in-memory conversation with one new user message.
		session = existing
		// The opening route description is already in this conversation. Send only
		// the new instruction supplied by the parent orchestrator; replaying the full
		// durable description wastes context and duplicates instructions every call.
		msg := messageSequenceContinuationMessage(opts)
		if msg == "" {
			return "", session.ConversationHistory, fmt.Errorf("message_sequence route %q already has an active conversation; provide a re-entry message or restart", sequenceStep.GetID())
		}
		plannedItems = appendMessageSequenceFinalValidation([]MessageSequenceItem{{
			ID:      fmt.Sprintf("reentry-%d", len(session.Entries)),
			Type:    "user_message",
			Kind:    "execution",
			Message: msg,
		}}, sequenceStep.ValidationSchema)
		if source == "configured_queue" {
			source = "builder_resume"
		}
	} else {
		// First route call, or any standalone run: run the configured queue.
		session = &messageSequenceSession{
			SessionID: sequenceStep.GetID(),
			StepID:    sequenceStep.GetID(),
			RunFolder: hcpo.selectedRunFolder,
			Status:    "running",
			CreatedAt: time.Now(),
		}
		// Run the configured queue, automatically enforce the step-level final
		// validation contract, then append synthetic learnings/KB contribution
		// turns so a standalone message_sequence honors its step-level
		// learning_objective / knowledgebase_contribution — the same post-step
		// learnings/KB a regular step runs. (Copy first so we never mutate the plan's
		// Items slice.)
		configuredItems := append([]MessageSequenceItem(nil), sequenceStep.Items...)
		// Live caller/operator input is conversational state, not part of the
		// durable step charter. Put it in the user-message queue before authored
		// items so the system description remains stable across calls.
		initialInstruction := ""
		if isRoute {
			initialInstruction = strings.TrimSpace(opts.ContinuationMessage)
		}
		if initialInstruction == "" && execCtx != nil {
			initialInstruction = strings.TrimSpace(execCtx.WorkshopHumanInput)
		}
		if initialInstruction != "" && !sequenceStep.AuthoredPrompt {
			configuredItems = append([]MessageSequenceItem{{
				ID:      sequenceStep.GetID() + "-initial-instruction",
				Type:    "user_message",
				Kind:    "execution",
				Message: initialInstruction,
			}}, configuredItems...)
		}
		if len(configuredItems) == 0 && !sequenceStep.AuthoredPrompt {
			configuredItems = []MessageSequenceItem{{
				ID:      sequenceStep.GetID() + "-execute",
				Type:    "user_message",
				Kind:    "execution",
				Message: "Execute the step charter now and verify that its requirements are satisfied.",
			}}
		}
		if sequenceStep.AuthoredPrompt {
			plannedItems = configuredItems
		} else {
			plannedItems = appendMessageSequenceFinalValidation(configuredItems, sequenceStep.ValidationSchema)
			plannedItems = append(plannedItems, hcpo.messageSequenceClosingItems(ctx, sequenceStep, stepIndex)...)
		}
		session.delegation = opts.Delegation
		source = "configured_queue"
	}

	if !isRoute {
		defer hcpo.closeMessageSequenceRuntime(session, "standalone message_sequence completed")
	}
	// Resolve a single plan/config snapshot before starting any work. Script items
	// never acquire the orchestrator's agentic delegation tools or child sessions.
	for _, item := range plannedItems {
		if item.Type != "scripted" {
			continue
		}
		plan, err := hcpo.ReadCurrentPlan(ctx)
		if err != nil {
			return "", session.ConversationHistory, fmt.Errorf("load sequence script definitions: %w", err)
		}
		configs, err := hcpo.ReadStepConfigs(ctx)
		if err != nil {
			return "", session.ConversationHistory, fmt.Errorf("load sequence script configuration: %w", err)
		}
		// Copy the definitions rather than mutating a shared plan snapshot.
		planCopy := *plan
		planCopy.OrphanSteps = append([]PlanStepInterface(nil), plan.OrphanSteps...)
		for i, orphan := range planCopy.OrphanSteps {
			if script, ok := orphan.(*RegularPlanStep); ok {
				copy := *script
				for _, config := range configs {
					if config.ID == script.ID {
						copy.AgentConfigs = config.AgentConfigs
						break
					}
				}
				planCopy.OrphanSteps[i] = &copy
			}
		}
		session.scriptedPlan = &planCopy
		break
	}

	for itemIndex, item := range plannedItems {
		// Stop means stop. Every layer below is expected to surface a canceled
		// context as an item error, and the queue halts on any error — but that
		// makes halting depend on a lower layer noticing, and a coding-CLI turn
		// whose pane is torn down can return a plausible-looking result instead
		// of an error. A canceled run must never START another item, whatever
		// the previous one reported (PLAT-130).
		if haltErr := messageSequenceHaltedBeforeItem(ctx, sequenceStep.GetID(), item.ID); haltErr != nil {
			// The line a live reverify greps for: it names the item that was NOT
			// sent, which is the whole claim Stop makes (PLAT-130).
			if hcpo.GetLogger() != nil {
				hcpo.GetLogger().Info(fmt.Sprintf("[STOP] message_sequence step %q: refusing to start item %q (%d of %d) — run is canceled: %v",
					sequenceStep.GetID(), item.ID, len(session.Entries)+1, len(plannedItems), ctx.Err()))
			}
			session.Status = "failed"
			session.UpdatedAt = time.Now()
			if isRoute {
				hcpo.storeMsgSeqRouteSession(routeKey, session)
			}
			_ = hcpo.saveMessageSequenceSession(context.WithoutCancel(ctx), sessionRelPath, session)
			return "", session.ConversationHistory, haltErr
		}
		started := time.Now()
		notificationID, notificationName, notificationMeta, notifyItem := hcpo.startMessageSequenceItemNotification(ctx, sequenceStep, item, stepIndex, stepPath, source, started)
		summary, err := hcpo.executeMessageSequenceItem(ctx, sequenceStep, item, stepIndex, stepPath, session, isRoute)
		if err == nil && sequenceStep.AuthoredPrompt && itemIndex == len(plannedItems)-1 {
			_, err = normalizeAuthoredJSONResult(summary)
		}
		ended := time.Now()
		entry := messageSequenceEntry{
			EntryID:   fmt.Sprintf("%s-%d", item.ID, started.UnixNano()),
			ItemID:    item.ID,
			ItemType:  item.Type,
			Source:    source,
			Status:    "completed",
			Summary:   summary,
			StartedAt: started,
			EndedAt:   ended,
		}
		terminalErr := err
		if err != nil {
			entry.Status = "failed"
			entry.Summary = err.Error()
			session.Status = "failed"
			session.Entries = append(session.Entries, entry)
			session.UpdatedAt = time.Now()
			if isRoute {
				hcpo.storeMsgSeqRouteSession(routeKey, session)
			}
			_ = hcpo.saveMessageSequenceSession(ctx, sessionRelPath, session)
			hcpo.completeMessageSequenceItemNotification(ctx, notificationID, notificationName, entry.Summary, notificationMeta, notifyItem, terminalErr)
			return "", session.ConversationHistory, terminalErr
		}
		// A user_message/foreach turn that self-reported STATUS: FAILED is a terminal
		// item failure — stop the queue here instead of running the remaining items.
		if itemType := item.Type; itemType == "" || itemType == "user_message" || itemType == "foreach" {
			if reason, failedStatus := messageSequenceItemReportedFailure(summary); failedStatus {
				entry.Status = "failed"
				session.Status = "failed"
				terminalErr = fmt.Errorf("message_sequence step %q item %q reported STATUS: FAILED: %s", sequenceStep.GetID(), item.ID, reason)
				session.Entries = append(session.Entries, entry)
				session.UpdatedAt = time.Now()
				if isRoute {
					hcpo.storeMsgSeqRouteSession(routeKey, session)
				}
				_ = hcpo.saveMessageSequenceSession(ctx, sessionRelPath, session)
				hcpo.completeMessageSequenceItemNotification(ctx, notificationID, notificationName, summary, notificationMeta, notifyItem, terminalErr)
				return "", session.ConversationHistory, terminalErr
			}
		}
		session.Entries = append(session.Entries, entry)
		session.UpdatedAt = time.Now()
		_ = hcpo.saveMessageSequenceSession(ctx, sessionRelPath, session)
		hcpo.completeMessageSequenceItemNotification(ctx, notificationID, notificationName, summary, notificationMeta, notifyItem, nil)
	}
	if sequenceStep.AuthoredPrompt {
		if len(session.Entries) == 0 {
			return "", session.ConversationHistory, fmt.Errorf("message_sequence step %q has no JSON result", sequenceStep.ID)
		}
		resultJSON, resultErr := normalizeAuthoredJSONResult(session.Entries[len(session.Entries)-1].Summary)
		if resultErr != nil {
			session.Status = "failed"
			session.UpdatedAt = time.Now()
			_ = hcpo.saveMessageSequenceSession(ctx, sessionRelPath, session)
			return "", session.ConversationHistory, fmt.Errorf("message_sequence step %q: %w", sequenceStep.ID, resultErr)
		}
		resultPath := filepath.Join(hcpo.messageSequenceExecutionRelPath(stepPath, sequenceStep.ID), "result.json")
		if err := hcpo.WriteWorkspaceFile(ctx, resultPath, resultJSON); err != nil {
			return "", session.ConversationHistory, fmt.Errorf("save authored JSON result: %w", err)
		}
	}

	session.Status = "completed"
	session.UpdatedAt = time.Now()
	if isRoute {
		hcpo.storeMsgSeqRouteSession(routeKey, session)
	}
	_ = hcpo.saveMessageSequenceSession(ctx, sessionRelPath, session)
	finalSummary := hcpo.summarizeMessageSequenceSession(session)
	if sequenceStep.AuthoredPrompt && len(session.Entries) > 0 {
		finalSummary = session.Entries[len(session.Entries)-1].Summary
	}
	// Item summaries stay in the retained message-sequence session. Pulse reads
	// that evidence directly; do not parse prose into observations here.
	if err := hcpo.saveFinalExecutionSummary(sequenceStep.GetID(), stepPath, finalSummary); err != nil {
		hcpo.recordRunPersistenceError(context.Background(), sequenceStep.GetID(), err)
	}
	return finalSummary, session.ConversationHistory, nil
}

// appendMessageSequenceFinalValidation makes a message_sequence obey the same
// step-level validation contract as a regular step. Explicit prevalidation items
// remain useful as intermediate gates, but authors do not need to duplicate the
// top-level schema as the final configured item. The synthetic gate deliberately
// runs before learnings/KB closing turns, so those bookkeeping turns cannot make
// an otherwise invalid work result look successful.
func appendMessageSequenceFinalValidation(items []MessageSequenceItem, schema *ValidationSchema) []MessageSequenceItem {
	planned := append([]MessageSequenceItem(nil), items...)
	if schema == nil {
		return planned
	}

	if len(planned) > 0 {
		last := planned[len(planned)-1]
		if strings.TrimSpace(last.Type) == "prevalidation" {
			lastSchema := last.ValidationSchema
			if lastSchema == nil {
				lastSchema = last.Prevalidation
			}
			if lastSchema == nil {
				lastSchema = schema
			}
			if equalValidationSchemas(lastSchema, schema) {
				return planned
			}
		}
	}

	id := "__automatic_final_validation__"
	usedIDs := make(map[string]struct{}, len(planned))
	for _, item := range planned {
		usedIDs[item.ID] = struct{}{}
	}
	for suffix := 2; ; suffix++ {
		if _, exists := usedIDs[id]; !exists {
			break
		}
		id = fmt.Sprintf("__automatic_final_validation_%d__", suffix)
	}

	return append(planned, MessageSequenceItem{
		ID:               id,
		Type:             "prevalidation",
		Title:            "Final validation",
		ValidationSchema: schema,
		Synthetic:        true,
	})
}

func (hcpo *StepBasedWorkflowOrchestrator) startMessageSequenceItemNotification(ctx context.Context, step *MessageSequencePlanStep, item MessageSequenceItem, stepIndex int, stepPath string, source string, started time.Time) (string, string, map[string]string, bool) {
	if hcpo == nil || step == nil {
		return "", "", nil, false
	}
	// The runtime's own final validation gate used to skip its auto-notification:
	// on the happy path it is a deterministic file/DB check in Go (RunPreValidation
	// — no model turn, nothing the agent did not already report), and announcing
	// it cost a full synthetic LLM turn plus a second near-identical "step
	// finished" message for one step. Re-enabled: the builder wants to hear about
	// a pre-validation failure as soon as it happens (so it can check whether a
	// schedule/setup issue or a genuine mistake is to blame) rather than only
	// after it eventually surfaces via the step's own completion notification —
	// worth the extra turn/message on the failure path. Author-declared
	// prevalidation items always got notifications; this just stops treating the
	// appended gate item differently from them.
	// Fail loud, not silent: a nil notifier means this execution path forgot to
	// wire SetWorkshopExecutionNotifier, so the main agent gets NO notification
	// for this message_sequence item. That used to be an invisible no-op (a whole
	// step ran with the main agent never told). Log it so the wiring gap is
	// obvious instead of silently swallowed.
	if hcpo.workshopExecutionNotifier == nil {
		if hcpo.GetLogger() != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ message_sequence item %q/%q: workshopExecutionNotifier is nil — auto-notification skipped (notifier not wired for this execution path)", step.GetID(), item.ID))
		}
		return "", "", nil, false
	}
	execID := messageSequenceItemExecutionID(step.GetID(), item.ID, started)
	name := messageSequenceItemExecutionName(step, item)
	meta := hcpo.messageSequenceItemNotificationMeta(step, item, stepIndex, stepPath, source)
	hcpo.workshopExecutionNotifier.OnExecutionStart(WorkshopExecutionStart{
		ID:                execID,
		ParentExecutionID: currentWorkshopParentExecutionID(ctx),
		Name:              name,
		Kind:              "message_sequence_item",
		Metadata:          meta,
	})
	return execID, name, meta, true
}

func (hcpo *StepBasedWorkflowOrchestrator) completeMessageSequenceItemNotification(_ context.Context, execID string, name string, summary string, meta map[string]string, active bool, err error) {
	if hcpo == nil || hcpo.workshopExecutionNotifier == nil || !active {
		return
	}
	result := strings.TrimSpace(summary)
	if result == "" && err != nil {
		result = err.Error()
	}
	if result == "" {
		result = "message sequence item completed"
	}
	hcpo.workshopExecutionNotifier.OnExecutionComplete(execID, name, result, meta, err)
}

func (hcpo *StepBasedWorkflowOrchestrator) messageSequenceItemNotificationMeta(step *MessageSequencePlanStep, item MessageSequenceItem, stepIndex int, stepPath string, source string) map[string]string {
	itemType := strings.TrimSpace(item.Type)
	if itemType == "" {
		itemType = "user_message"
	}
	meta := map[string]string{
		"execution_type": "message-sequence-item",
		"step_id":        step.GetID(),
		"step_index":     fmt.Sprintf("%d", stepIndex),
		"step_path":      stepPath,
		"item_id":        item.ID,
		"item_type":      itemType,
		"source":         source,
	}
	if title := strings.TrimSpace(step.GetTitle()); title != "" {
		meta["step_title"] = title
	}
	if kind := strings.TrimSpace(item.Kind); kind != "" {
		meta["item_kind"] = kind
	}
	if runFolder := strings.TrimSpace(hcpo.selectedRunFolder); runFolder != "" {
		meta["run_folder"] = runFolder
		meta["iteration"] = runFolder
	}
	if groupName := strings.TrimSpace(hcpo.currentGroupName); groupName != "" {
		meta["group_name"] = groupName
	}
	return meta
}

func messageSequenceItemExecutionName(step *MessageSequencePlanStep, item MessageSequenceItem) string {
	stepLabel := strings.TrimSpace(step.GetTitle())
	if stepLabel == "" {
		stepLabel = step.GetID()
	}
	itemType := strings.TrimSpace(item.Type)
	if itemType == "" {
		itemType = "user_message"
	}
	itemID := strings.TrimSpace(item.ID)
	if itemID == "" {
		itemID = "item"
	}
	return fmt.Sprintf("Message sequence item -> %s / %s (%s)", stepLabel, itemID, itemType)
}

func messageSequenceItemExecutionID(stepID string, itemID string, started time.Time) string {
	return fmt.Sprintf("msgseq-%s-%s-%d", sanitizeMessageSequenceExecutionIDPart(stepID), sanitizeMessageSequenceExecutionIDPart(itemID), started.UnixNano())
}

func sanitizeMessageSequenceExecutionIDPart(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "item"
	}
	s = regexp.MustCompile(`[^a-z0-9_-]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-_")
	if s == "" {
		return "item"
	}
	if len(s) > 48 {
		return s[:48]
	}
	return s
}

func (hcpo *StepBasedWorkflowOrchestrator) executeMessageSequenceItem(ctx context.Context, step *MessageSequencePlanStep, item MessageSequenceItem, stepIndex int, stepPath string, session *messageSequenceSession, isNestedExecution bool) (string, error) {
	switch item.Type {
	case "user_message", "":
		return hcpo.executeMessageSequenceUserMessage(ctx, step, item, stepIndex, stepPath, session)
	case "foreach":
		return hcpo.executeMessageSequenceForeachItem(ctx, step, item, stepIndex, stepPath, session)
	case "scripted":
		return hcpo.executeMessageSequenceScripts(ctx, step, item, stepIndex, stepPath, session)
	case "code":
		return "", fmt.Errorf("message_sequence step %q item %q uses removed type \"code\"; upgrade the workflow to contract v1.0.10 before running it", step.ID, item.ID)
	case "prevalidation":
		schema := item.ValidationSchema
		if schema == nil {
			schema = item.Prevalidation
		}
		if schema == nil {
			schema = step.ValidationSchema
		}
		if schema == nil {
			return "prevalidation skipped: no schema", nil
		}
		// Prevalidation is a self-validation gate with a repair loop, mirroring the
		// retry-with-feedback behavior of regular steps: on failure we send the
		// concrete validation errors back to the SAME conversation as a fix-it turn
		// (the session continues, so the agent keeps full context and can re-create
		// or correct the required output files), then re-run the same checks. Only
		// after maxMessageSequencePrevalidationRepairs failed repair turns does the
		// gate fail the sequence. A prevalidation that cannot even RUN (err != nil)
		// is a terminal infrastructure failure, not retried.
		const maxMessageSequencePrevalidationRepairs = 3
		for attempt := 0; ; attempt++ {
			results, err := RunPreValidation(ctx, schema, hcpo.messageSequenceExecutionRelPath(stepPath, step.GetID()), hcpo.BaseOrchestrator)
			if err != nil {
				results = &WorkspaceVerificationResult{
					OverallPass:  false,
					FilesChecked: []FileCheckResult{},
					Summary: ValidationSummary{
						TotalChecks:  0,
						PassedChecks: 0,
						FailedChecks: 1,
						Errors: []ValidationError{{
							CheckType: "pre_validation_error",
							Expected:  "pre-validation to run successfully",
							Actual:    "error occurred",
							Message:   fmt.Sprintf("Pre-validation failed to run for message sequence item %q: %v", item.ID, err),
						}},
						SchemaWarnings: []ValidationError{},
					},
				}
				hcpo.emitPreValidationCompletedEvent(ctx, step, stepIndex, stepPath, isNestedExecution, results)
				hcpo.saveMessageSequencePreValidationLog(ctx, step, stepPath, item.ID, attempt, results, schema)
				return "", err
			}
			if results == nil {
				results = &WorkspaceVerificationResult{
					OverallPass:  false,
					FilesChecked: []FileCheckResult{},
					Summary: ValidationSummary{
						TotalChecks:  0,
						PassedChecks: 0,
						FailedChecks: 1,
						Errors: []ValidationError{{
							CheckType: "pre_validation_error",
							Expected:  "pre-validation to return a result",
							Actual:    "no result returned",
							Message:   fmt.Sprintf("Pre-validation returned no result for message sequence item %q", item.ID),
						}},
						SchemaWarnings: []ValidationError{},
					},
				}
			}
			hcpo.emitPreValidationCompletedEvent(ctx, step, stepIndex, stepPath, isNestedExecution, results)
			hcpo.saveMessageSequencePreValidationLog(ctx, step, stepPath, item.ID, attempt, results, schema)
			if results.OverallPass {
				if attempt == 0 {
					return "prevalidation passed", nil
				}
				return fmt.Sprintf("prevalidation passed after %d repair turn(s)", attempt), nil
			}
			if attempt >= maxMessageSequencePrevalidationRepairs {
				return "", fmt.Errorf("message sequence prevalidation failed for item %q after %d repair attempt(s): %s",
					item.ID, maxMessageSequencePrevalidationRepairs, summarizeMessageSequencePrevalidationErrors(results))
			}
			// Send the failure back as a fix-it turn on the same conversation, then loop to re-validate.
			feedback := formatMessageSequencePrevalidationFeedback(item.ID, results)
			repairItem := MessageSequenceItem{
				ID:      fmt.Sprintf("%s-repair-%d", item.ID, attempt+1),
				Type:    "user_message",
				Kind:    "execution",
				Message: feedback,
			}
			hcpo.GetLogger().Info(fmt.Sprintf("🔁 message_sequence prevalidation %q failed — sending repair turn %d/%d", item.ID, attempt+1, maxMessageSequencePrevalidationRepairs))
			if _, rerr := hcpo.executeMessageSequenceUserMessage(ctx, step, repairItem, stepIndex, stepPath, session); rerr != nil {
				return "", fmt.Errorf("message sequence prevalidation %q repair turn %d failed: %w", item.ID, attempt+1, rerr)
			}
		}
	default:
		return "", fmt.Errorf("unsupported message_sequence item type %q", item.Type)
	}
}

// saveMessageSequencePreValidationLog preserves the latest gate result and the
// individual repair attempts. ItemID belongs in the phase so two validation
// gates in one message-sequence step cannot overwrite each other.
func (hcpo *StepBasedWorkflowOrchestrator) saveMessageSequencePreValidationLog(
	ctx context.Context,
	step *MessageSequencePlanStep,
	stepPath string,
	itemID string,
	attempt int,
	results *WorkspaceVerificationResult,
	schema *ValidationSchema,
) {
	if hcpo == nil || step == nil || results == nil || strings.TrimSpace(hcpo.selectedRunFolder) == "" {
		return
	}
	preValidationLogPath := fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
	SavePreValidationLog(ctx, hcpo.BaseOrchestrator, preValidationLogPath, step.GetID(), stepPath, results, schema, hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, hcpo.currentGroupName,
		PreValidationAttempt{ExecutionMode: "message_sequence", ValidationPhase: "message-sequence-" + itemID, ExecutionAttempt: 1, ValidationAttempt: attempt + 1})
}

// summarizeMessageSequencePrevalidationErrors renders a one-line, comma-joined
// summary of a failed prevalidation result for inclusion in the terminal error.
func summarizeMessageSequencePrevalidationErrors(results *WorkspaceVerificationResult) string {
	if results == nil {
		return "no validation result"
	}
	parts := make([]string, 0, len(results.Summary.Errors))
	for _, e := range results.Summary.Errors {
		msg := strings.TrimSpace(e.Message)
		if msg == "" {
			msg = strings.TrimSpace(fmt.Sprintf("%s check failed (expected %s, got %s)", e.CheckType, e.Expected, e.Actual))
		}
		loc := strings.TrimSpace(strings.TrimSpace(e.File) + " " + strings.TrimSpace(e.Path))
		if loc != "" {
			parts = append(parts, loc+": "+msg)
		} else {
			parts = append(parts, msg)
		}
	}
	if len(parts) == 0 {
		return "validation did not pass (no specific errors reported)"
	}
	return strings.Join(parts, "; ")
}

// formatMessageSequencePrevalidationFeedback builds the fix-it instruction sent
// back to the agent when a prevalidation gate fails. It mirrors the regular-step
// "## Pre-Validation Failed" feedback: name the failing checks concretely and
// instruct the agent to actually correct/recreate the output files (not merely
// re-report success), since the same checks run again afterward.
func formatMessageSequencePrevalidationFeedback(itemID string, results *WorkspaceVerificationResult) string {
	var b strings.Builder
	b.WriteString("## Pre-Validation Failed (Previous Attempt)\n\n")
	b.WriteString(fmt.Sprintf("The output validation gate %q did not pass. Fix the issues below by inspecting and correcting the required output files (create missing files, set every required field to the correct value/type), then finish. The exact same validation will run again immediately after this turn.\n\n", itemID))
	b.WriteString("Failing checks:\n")
	wrote := false
	if results != nil {
		for _, fc := range results.FilesChecked {
			if !fc.Exists {
				b.WriteString(fmt.Sprintf("- Missing file: %s — create it.\n", fc.FileName))
				wrote = true
				continue
			}
			for _, jc := range fc.JSONChecks {
				if jc.Passed {
					continue
				}
				detail := strings.TrimSpace(jc.ErrorMsg)
				if detail == "" {
					detail = fmt.Sprintf("%s check failed (expected %v, got %v)", jc.CheckType, jc.Expected, jc.Actual)
				}
				b.WriteString(fmt.Sprintf("- %s @ %s: %s\n", fc.FileName, jc.Path, detail))
				wrote = true
			}
		}
		if !wrote {
			for _, e := range results.Summary.Errors {
				msg := strings.TrimSpace(e.Message)
				if msg == "" {
					msg = fmt.Sprintf("%s check failed (expected %s, got %s)", e.CheckType, e.Expected, e.Actual)
				}
				loc := strings.TrimSpace(strings.TrimSpace(e.File) + " " + strings.TrimSpace(e.Path))
				if loc != "" {
					b.WriteString(fmt.Sprintf("- %s: %s\n", loc, msg))
				} else {
					b.WriteString(fmt.Sprintf("- %s\n", msg))
				}
				wrote = true
			}
		}
	}
	if !wrote {
		b.WriteString("- Validation did not pass; no specific error detail was reported. Re-check that every required output file exists with the correct structure.\n")
	}
	b.WriteString("\nDo NOT just reply that it is done — actually read the files, correct the data, and write them so every required field is present with the correct type and value.")
	return b.String()
}

func (hcpo *StepBasedWorkflowOrchestrator) executeMessageSequenceUserMessage(ctx context.Context, step *MessageSequencePlanStep, item MessageSequenceItem, stepIndex int, stepPath string, session *messageSequenceSession) (string, error) {
	delegation := session.delegation
	writeAccess := hcpo.resolveMessageSequenceItemWriteAccess(getAgentConfigs(step), item)
	var readPaths, writePaths []string
	if delegation != nil {
		readPaths, writePaths = delegation.ReadPaths, delegation.WritePaths
	} else {
		readPaths, writePaths = hcpo.setupMessageSequenceFolderGuard(stepPath, step.GetID(), getAgentConfigs(step), writeAccess)
		// PLAT-556: what the description names under Inputs/Guides is readable (never writable).
		readPaths = common.DeduplicateStrings(appendDescriptionReferenceReadPaths(readPaths, hcpo.GetWorkspacePath(), step.GetDescription()))
	}
	runtime, agentCtx, err := hcpo.getMessageSequenceRuntime(ctx, step, stepPath, session, readPaths, writePaths)
	if err != nil {
		return "", err
	}
	if writeAccess.Learnings {
		restoreDirectLearningTurn := hcpo.prepareDirectLearningTurn(runtime.Agent, []string{filepath.Join(hcpo.GetWorkspacePath(), LearningsFolderName, GlobalLearningID)})
		defer restoreDirectLearningTurn()
	}
	// The dedicated learnings closing turn diff_patches the SHARED
	// learnings/_global files. Serialize it against every other direct-learnings
	// writer (regular steps hold the same mutex across their learnings turn —
	// see controller_execution.go — and learnings_direct.go documents the
	// contract). Without this, parallel message_sequence steps race on SKILL.md.
	// Scoped to Kind=="learning" only: locking every learnings-writable work
	// turn would serialize whole parallel sequences.
	// Not for a delegating orchestrator: its reflection turn may launch children
	// whose own learnings turns take this same mutex, and the parent waits for
	// them inside the turn (reconcile) — holding the lock here would deadlock.
	// The orchestrator never held it before the executor merge either.
	if strings.TrimSpace(item.Kind) == "learning" && writeAccess.Learnings && delegation == nil {
		learningsGlobalFileMutex.Lock()
		defer learningsGlobalFileMutex.Unlock()
	}
	learningRefBefore := ""
	if strings.TrimSpace(item.Kind) == "learning" && writeAccess.Learnings {
		learningRefBefore = hcpo.snapshotCanonicalArtifactRef(ctx, filepath.Join(hcpo.GetWorkspacePath(), LearningsFolderName, GlobalLearningID))
	}

	message := item.Message
	if !step.AuthoredPrompt {
		message = strings.TrimSpace(message)
	}
	if step.AuthoredPrompt {
		message, err = hcpo.renderAuthoredPrompt(ctx, message)
		if err != nil {
			return "", fmt.Errorf("message_sequence step %q item %q: %w", step.ID, item.ID, err)
		}
	}
	if session.LastRuntimeContext != "" && !step.AuthoredPrompt {
		message = session.LastRuntimeContext + "\n\n## Next instruction\n" + message
	}
	var templateVars map[string]string
	if delegation != nil {
		templateVars = delegation.TurnVars(item, message, session.ExecutionTurnCount == 0)
	} else {
		templateVars = hcpo.buildMessageSequenceTemplateVars(step, item, stepIndex, stepPath, message, readPaths, writePaths, writeAccess)
	}
	if session.referencedGuides == nil {
		guides := hcpo.referencedGuidesForStep(ctx, step.GetID(), step.GetDescription())
		session.referencedGuides = &guides
	}
	templateVars["ReferencedGuides"] = *session.referencedGuides
	common.SetSessionShellEnv(runtime.SessionID, map[string]string{"SHARED_KB_STEP_ACCESS": messageSequencePromptKBAccess(resolveKnowledgebaseAccess(getAgentConfigs(step), hcpo.UseKnowledgebase()), writeAccess)})
	if step.AuthoredPrompt {
		systemPrompt, promptErr := hcpo.renderAuthoredPrompt(ctx, step.SystemPrompt)
		if promptErr != nil {
			return "", fmt.Errorf("message_sequence step %q system_prompt: %w", step.ID, promptErr)
		}
		if delegation != nil && delegation.ExecCtx != nil && delegation.ExecCtx.OrchestratorStep != nil {
			systemPrompt += authoredRoutesPromptBlock(delegation.ExecCtx.OrchestratorStep.PredefinedRoutes, delegation.ExecCtx.ScriptToolNames)
		}
		templateVars["AuthoredSystemPrompt"] = systemPrompt
		templateVars["AuthoredUserMessage"] = message
	}

	turnCtx := agentCtx
	if cab, ok := hcpo.GetContextAwareBridge().(*orchestrator.ContextAwareEventBridge); ok {
		turnCtx = cab.StartTimingCaptureFor(turnCtx)
	}
	session.ExecutionTurnCount++
	turnNumber := session.ExecutionTurnCount
	// PLAT-167: tag this item's cost-ledger entries with its own identity, so
	// Cost Analysis can break a message_sequence step's spend out per item
	// instead of merging every item into one combined row. Reuses PLAT-166's
	// generic phase mechanism verbatim (Entry.Phase / ExecutionAggregate.ByPhase
	// already accept any string) — no new ledger plumbing. runtime.Agent is
	// created once and reused for every item in this step
	// (getMessageSequenceRuntime), so — exactly like the reflection turn —
	// this must toggle the already-attached observer's phase rather than
	// attaching a new one.
	if ba := runtime.Agent.GetBaseAgent(); ba != nil {
		for _, observer := range ba.Observers() {
			if costObserver, ok := observer.(*costobserver.Observer); ok {
				costObserver.SetPhase("item:" + item.ID)
				// Resets to "" (not costobserver.PhaseExecutionOnly): see
				// Observer.SetPhase's doc — an untagged turn writes no
				// by_phase entry at all, instead of a named-but-redundant one.
				defer costObserver.SetPhase("")
			}
		}
	}
	attemptStartedAt := time.Now().UTC()
	result, history, err := hcpo.withWorkshopMessageTarget(turnCtx, step.GetID(), "message-sequence:"+item.ID, runtime.Agent, func() (string, []llmtypes.MessageContent, error) {
		return runtime.Agent.Execute(turnCtx, templateVars, session.ConversationHistory)
	})
	attemptCompletedAt := time.Now().UTC()
	attemptDuration := attemptCompletedAt.Sub(attemptStartedAt)

	loggedHistory := history
	if len(loggedHistory) == 0 {
		loggedHistory = session.ConversationHistory
	}
	loggedResult := formatMessageSequenceTurnLogResult(item, result, err)
	executionLLM := agentConfigModelLabel(runtime.Agent.GetConfig())

	// An orchestrator turn is not finished when the LLM call returns: children it
	// launched are still running. Wait for them, feed the completion batch back
	// into the same conversation, and let the orchestrator continue — on the
	// error path too, so a failed turn never leaves children unaccounted for.
	if delegation != nil && delegation.ExecCtx != nil {
		reconcileHistory := history
		if len(reconcileHistory) == 0 {
			reconcileHistory = session.ConversationHistory
		}
		if rerr := hcpo.reconcileAsyncSubAgentCalls(ctx, step.GetID(), runtime.Agent, delegation.ExecCtx, &reconcileHistory); rerr != nil {
			session.ConversationHistory = reconcileHistory
			if err == nil {
				err = fmt.Errorf("sub-agent reconciliation after item %q: %w", item.ID, rerr)
			}
		}
		history = reconcileHistory
		loggedHistory = history
		if err == nil {
			// The turn's outcome is whatever the orchestrator said after reading the
			// completion batch, not the acknowledgement it emitted before it.
			if final := lastAssistantText(history); final != "" {
				result = final
			}
			loggedResult = formatMessageSequenceTurnLogResult(item, result, nil)
		}
	}

	var toolCalls []orchestrator.ToolCallEntry
	var llmCalls []orchestrator.LLMCallEntry
	if cab, ok := hcpo.GetContextAwareBridge().(*orchestrator.ContextAwareEventBridge); ok {
		timingCapture := cab.DrainTimingCaptureFor(turnCtx)
		toolCalls, llmCalls = timingCapture.ToolCalls, timingCapture.LLMCalls
	}
	if delegation != nil && delegation.LogTurn != nil {
		delegation.LogTurn(ctx, turnNumber, executionLLM, loggedHistory, toolCalls, llmCalls, attemptStartedAt, attemptCompletedAt)
	} else if logErr := hcpo.saveExecutionConversationLogs(
		stepIndex, step.GetID(), stepPath, 1, turnNumber,
		loggedResult, executionLLM, loggedHistory, runtime.Agent,
		toolCalls, llmCalls,
		attemptStartedAt, attemptCompletedAt, attemptDuration,
	); logErr != nil {
		hcpo.recordRunPersistenceError(context.Background(), step.GetID(), logErr)
	}

	if err != nil {
		if len(history) > 0 {
			session.ConversationHistory = history
		}
		return "", err
	}
	session.ConversationHistory = history
	session.LastRuntimeContext = ""
	trimmedResult := strings.TrimSpace(result)
	// Freshness: message_sequence is the primary execution path, so its synthetic
	// learnings/KB closing turns are where store confirmation is recorded (the
	// regular-step hooks in controller_execution.go rarely fire now). Best-effort.
	learningChanged := false
	if strings.TrimSpace(item.Kind) == "learning" && writeAccess.Learnings {
		learningRefAfter := hcpo.snapshotCanonicalArtifactRef(context.Background(), filepath.Join(hcpo.GetWorkspacePath(), LearningsFolderName, GlobalLearningID))
		learningChanged = learningRefBefore != learningRefAfter
	}
	hcpo.recordMessageSequenceStoreFreshness(item, step.GetID(), trimmedResult, learningChanged)
	return trimmedResult, nil
}

// recordMessageSequenceStoreFreshness stamps the code-owned freshness ledger when
// a learnings/KB contribution closing turn completes: a run reviewed the store and
// left it current this run. Learnings distinguishes updated vs reviewed-unchanged
// from the turn result; KB records a review. Never fails the run.
func (hcpo *StepBasedWorkflowOrchestrator) recordMessageSequenceStoreFreshness(item MessageSequenceItem, stepID, result string, learningChanged bool) {
	// Do not record a confirmation for a turn that self-reported failure. The
	// sequence driver treats STATUS: FAILED as a terminal item failure (see
	// messageSequenceItemReportedFailure in the driver loop), and this hook runs
	// on the raw item result BEFORE that check — so a failed contribution must not
	// mark the store reviewed or updated.
	if _, failed := messageSequenceItemReportedFailure(result); failed {
		return
	}
	switch strings.TrimSpace(item.Kind) {
	case "learning":
		if err := hcpo.recordLearningsConfirmation(context.Background(), hcpo.selectedRunFolder, stepID, learningChanged); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to record learnings freshness for message_sequence step %s: %v", stepID, err))
		}
	case "knowledgebase":
		if err := hcpo.recordKnowledgebaseConfirmation(context.Background(), hcpo.selectedRunFolder, stepID); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to record knowledgebase freshness for message_sequence step %s: %v", stepID, err))
		}
	}
}

func formatMessageSequenceTurnLogResult(item MessageSequenceItem, result string, err error) string {
	itemType := strings.TrimSpace(item.Type)
	if itemType == "" {
		itemType = "user_message"
	}
	header := fmt.Sprintf("Message sequence item: %s (%s)", item.ID, itemType)
	trimmedResult := strings.TrimSpace(result)
	if err != nil {
		if trimmedResult == "" {
			return fmt.Sprintf("%s\nSTATUS: FAILED\n%s", header, err.Error())
		}
		return fmt.Sprintf("%s\nSTATUS: FAILED\n%s\n\n%s", header, err.Error(), trimmedResult)
	}
	if trimmedResult == "" {
		return header
	}
	return header + "\n" + trimmedResult
}

// executeMessageSequenceForeachItem expands a foreach item into one user_message turn per row
// of its db source and runs each through the same conversation (auto-summarization keeps the
// growing context bounded). Each row's templated text is sent as an ordinary user_message,
// inheriting the foreach item's kind / write_access.
func (hcpo *StepBasedWorkflowOrchestrator) executeMessageSequenceForeachItem(ctx context.Context, step *MessageSequencePlanStep, item MessageSequenceItem, stepIndex int, stepPath string, session *messageSequenceSession) (string, error) {
	messages, err := hcpo.expandForeach(ctx, item.SourceSQL, item.Message, item.MaxIterations)
	if err != nil {
		return "", fmt.Errorf("foreach item %q: %w", item.ID, err)
	}
	if len(messages) == 0 {
		return fmt.Sprintf("foreach %s: 0 rows, nothing to process", item.ID), nil
	}
	for idx, msg := range messages {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("foreach item %q canceled: %w", item.ID, ctx.Err())
		default:
		}
		synth := MessageSequenceItem{
			ID:          fmt.Sprintf("%s-%d", item.ID, idx),
			Type:        "user_message",
			Kind:        item.Kind,
			Message:     msg,
			WriteAccess: item.WriteAccess,
		}
		if _, err := hcpo.executeMessageSequenceUserMessage(ctx, step, synth, stepIndex, stepPath, session); err != nil {
			return "", fmt.Errorf("foreach %s row %d: %w", item.ID, idx, err)
		}
	}
	return fmt.Sprintf("foreach %s: processed %d row(s)", item.ID, len(messages)), nil
}

func (hcpo *StepBasedWorkflowOrchestrator) getMessageSequenceRuntime(ctx context.Context, step *MessageSequencePlanStep, stepPath string, session *messageSequenceSession, readPaths, writePaths []string) (*messageSequenceRuntime, context.Context, error) {
	if session == nil {
		return nil, ctx, fmt.Errorf("message_sequence session is nil")
	}

	if session.delegation != nil {
		// The orchestrator agent carries its own MCP session, tool registry, and
		// shared folder guard (set by executeOrchestratorStep); none of the sequence's
		// per-session overrides apply to it.
		if session.runtime != nil && session.runtime.Agent != nil {
			return session.runtime, ctx, nil
		}
		if session.delegation.CreateAgent == nil {
			return nil, ctx, fmt.Errorf("message_sequence delegation for step %q has no agent factory", step.GetID())
		}
		agent, err := session.delegation.CreateAgent(ctx)
		if err != nil {
			return nil, ctx, err
		}
		session.runtime = &messageSequenceRuntime{Agent: agent}
		return session.runtime, ctx, nil
	}

	sessionID := hcpo.messageSequenceRuntimeSessionID(session, stepPath, step.GetID())
	session.RuntimeSessionID = sessionID
	ready := session.runtime != nil && session.runtime.Agent != nil
	if !ready {
		// Own the allocation before registering any capabilities. A failed
		// setup must retire this attempt, never a serialized history ID.
		session.runtime = &messageSequenceRuntime{SessionID: sessionID}
		defer func() {
			if !ready {
				hcpo.closeMessageSequenceRuntime(session, "message_sequence setup failed")
			}
		}()
	}
	if err := hcpo.materializeWorkflowGuardPaths(readPaths, writePaths); err != nil {
		return nil, ctx, err
	}
	hcpo.configureSubAgentSessionGuard(sessionID, "message-sequence", step.GetID(), readPaths, writePaths)
	hcpo.setMessageSequenceShellEnv(sessionID, stepPath, step.GetID())

	// The message_sequence execution agent is created ONCE and reused for every
	// item, and the workspace-write tools (diff_patch_workspace_file) enforce a
	// folder-guard snapshot FROZEN into the agent at creation (createExecutionOnlyAgent
	// -> config.FolderGuardWritePaths, which the wrapper captures by value and never
	// re-reads). So the snapshot must cover every store the sequence's synthetic
	// closing turns will write — the learnings and KB contribution turns — not just
	// the first item's scope, or those later turns are denied their granted access
	// (their diff_patch to learnings/_global fails even though the step is configured
	// for it). Widen ONLY the frozen tool snapshot to the step's full granted write
	// scope; shell writes stay per-item via the session guard set above. This mirrors
	// what regular steps get, since a regular step's learnings turn enforces through
	// the per-item session guard rather than a reused frozen snapshot.
	_, snapshotWrite := hcpo.setupMessageSequenceFolderGuard(stepPath, step.GetID(), getAgentConfigs(step), hcpo.messageSequenceStepFullWriteAccess(step))
	outputPath := filepath.Join(hcpo.GetWorkspacePath(), hcpo.messageSequenceExecutionRelPath(stepPath, step.GetID()))
	folderOverride := &messageSequenceFolderGuardOverride{ReadPaths: readPaths, WritePaths: snapshotWrite, OutputPath: outputPath}
	sessionOverride := &messageSequenceRuntimeSessionOverride{SessionID: sessionID, KeepAlive: true}
	agentCtx := context.WithValue(ctx, messageSequenceFolderGuardOverrideKey{}, folderOverride)
	agentCtx = context.WithValue(agentCtx, messageSequenceRuntimeSessionOverrideKey{}, sessionOverride)

	if session.runtime != nil && session.runtime.Agent != nil {
		return session.runtime, agentCtx, nil
	}

	agentName := fmt.Sprintf("message-sequence-%s", step.GetID())
	agent, err := hcpo.createExecutionOnlyAgent(agentCtx, "execution_only", stepPath, agentName, step.AgentConfigs, step, step.GetID(), "")
	if err != nil {
		return nil, agentCtx, err
	}
	session.runtime.Agent = agent
	if cfg := agent.GetConfig(); cfg != nil {
		session.runtime.Provider = cfg.LLMConfig.Primary.Provider
		if actualID := strings.TrimSpace(cfg.MCPSessionID); actualID != sessionID {
			return nil, agentCtx, fmt.Errorf("message_sequence agent session %q does not match allocated runtime %q", actualID, sessionID)
		}
	}
	// Agent creation applies its full-step guard; restore this item's scope.
	hcpo.configureSubAgentSessionGuard(sessionID, "message-sequence", step.GetID(), readPaths, writePaths)
	hcpo.setMessageSequenceShellEnv(sessionID, stepPath, step.GetID())
	ready = true
	return session.runtime, agentCtx, nil
}

// setMessageSequenceShellEnv exports the resolved workflow runtime environment
// plus per-step paths onto the session. This gives server-side bridge shell calls
// (api-bridge.execute_shell_command) parity with the in-process executor for
// VAR_*, SECRET_*, DB_PATH, STEP_OUTPUT_DIR, and STEP_EXECUTION_DIR.
func (hcpo *StepBasedWorkflowOrchestrator) setMessageSequenceShellEnv(sessionID, stepPath, stepID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	stepOutputAbs := hcpo.messageSequenceAbsPath(hcpo.messageSequenceExecutionRelPath(stepPath, stepID))
	registerStepSessionShellEnv(
		sessionID,
		stepOutputAbs,
		stepExecutionScopeAbsPath(stepOutputAbs),
		"",
		hcpo.selectedRunFolder,
		hcpo.snapshotWorkspaceEnv(),
	)
	configureWorkflowDBSession(sessionID, hcpo.GetWorkspacePath(), hcpo.resolveDBAccess(nil), false)
}

// Reuse only the live runtime owned by this sequence. Serialized session IDs
// describe prior executions; they must not revive a stopped runtime or collide
// with another execution using the same workflow/run/group/step coordinates.
// In particular, cancellation cleanup may finish after a replacement starts.
func (hcpo *StepBasedWorkflowOrchestrator) messageSequenceRuntimeSessionID(session *messageSequenceSession, stepPath string, stepID string) string {
	if session != nil && session.runtime != nil && strings.TrimSpace(session.runtime.SessionID) != "" {
		return strings.TrimSpace(session.runtime.SessionID)
	}
	parts := []string{"msgseq"}
	if strings.TrimSpace(hcpo.selectedRunFolder) != "" {
		parts = append(parts, sanitizeMessageSequenceExecutionIDPart(hcpo.selectedRunFolder))
	}
	if strings.TrimSpace(hcpo.currentGroupName) != "" {
		parts = append(parts, sanitizeMessageSequenceExecutionIDPart(hcpo.currentGroupName))
	}
	parts = append(parts, sanitizeMessageSequenceExecutionIDPart(stepPath), sanitizeMessageSequenceExecutionIDPart(stepID))
	parts = append(parts, uuid.NewString())
	return strings.Join(parts, "-")
}

func (hcpo *StepBasedWorkflowOrchestrator) closeMessageSequenceRuntime(session *messageSequenceSession, reason string) {
	if session == nil || session.runtime == nil {
		return
	}
	runtime := session.runtime
	session.runtime = nil
	if strings.TrimSpace(runtime.SessionID) != "" {
		// Prevent connection recovery from recreating a session while it is
		// being retired. Shared browser/group sessions have different owners.
		mcpagent.MarkSessionsStopped([]string{runtime.SessionID})
	}
	if runtime.Agent != nil {
		_ = runtime.Agent.Close()
	}
	if strings.TrimSpace(runtime.SessionID) == "" {
		// A delegating (orchestrator) runtime has no sequence-owned session.
		return
	}
	closeMessageSequenceCodingSession(runtime.Provider, runtime.SessionID, reason)
	common.ClearSessionShellConfig(runtime.SessionID)
	virtualtools.DeleteSessionNotificationDestination(runtime.SessionID)
	// Agent.Close preserves MCP connections between turns. At true sequence
	// termination, retire that registry and its isolated workspace as well.
	mcpagent.CloseSession(runtime.SessionID)
	// Give back this sequence's tab in the workflow's shared browser. When it
	// was the browser's only user, the page is handed to the next step.
	browser.ReleaseSessionTabOwner(runtime.SessionID, browser.NewClient(getWorkspaceAPIURL()))
}

func closeMessageSequenceCodingSession(provider string, ownerSessionID string, reason string) {
	if strings.TrimSpace(ownerSessionID) == "" {
		return
	}
	switch strings.TrimSpace(provider) {
	case string(llmproviders.ProviderClaudeCode):
		llmproviders.CloseClaudeCodeInteractiveSessionForOwner(ownerSessionID, reason)
	case string(llmproviders.ProviderCodexCLI):
		llmproviders.CloseCodexCLIInteractiveSessionForOwner(ownerSessionID, reason)
	case string(llmproviders.ProviderCursorCLI):
		llmproviders.CloseCursorCLIInteractiveSessionForOwner(ownerSessionID, reason)
	case string(llmproviders.ProviderPiCLI):
		llmproviders.ClosePiCLIInteractiveSessionForOwner(ownerSessionID, reason)
	}
}

// messageSequenceItemReportedFailure reports whether an LLM turn ended with the
// agent's terminal STATUS: FAILED marker (the execution_only Completion
// contract). When a turn self-reports failure there's no point running the rest
// of the queue — especially a following prevalidation gate, which would just
// re-confirm the failure — so the sequence short-circuits and fails the step
// with the reported reason.
func messageSequenceItemReportedFailure(summary string) (reason string, failed bool) {
	for _, line := range strings.Split(summary, "\n") {
		trimmed := strings.TrimSpace(line)
		compact := strings.ToUpper(strings.Join(strings.Fields(trimmed), " "))
		if !strings.HasPrefix(compact, "STATUS: FAILED") && !strings.HasPrefix(compact, "STATUS:FAILED") {
			continue
		}
		if idx := strings.Index(strings.ToUpper(trimmed), "FAILED"); idx >= 0 {
			reason = strings.TrimSpace(strings.TrimLeft(trimmed[idx+len("FAILED"):], " —-:"))
		}
		if reason == "" {
			reason = trimmed
		}
		return reason, true
	}
	return "", false
}

func requestedMessageSequenceItemWriteAccess(item MessageSequenceItem) (MessageSequenceWriteAccess, bool) {
	if item.WriteAccess != (MessageSequenceWriteAccess{}) {
		return item.WriteAccess, true
	}
	var access MessageSequenceWriteAccess
	switch item.Kind {
	case "learning":
		access.Learnings = true
	case "knowledgebase":
		access.Knowledgebase = true
	case "db":
		access.DB = true
	default:
		return MessageSequenceWriteAccess{}, false
	}
	return access, true
}

// resolveMessageSequenceItemWriteAccess applies the same step-level store
// permissions as a regular execution step. A non-empty item write_access (or
// kind) is an optional narrowing override; it can never escalate beyond the
// step's configured permissions. This prevents a plain sequence turn from being
// silently read-only when the step itself is configured to write.
func (hcpo *StepBasedWorkflowOrchestrator) resolveMessageSequenceItemWriteAccess(stepConfig *AgentConfigs, item MessageSequenceItem) MessageSequenceWriteAccess {
	requested, hasItemOverride := requestedMessageSequenceItemWriteAccess(item)
	var resolved MessageSequenceWriteAccess
	if hasItemOverride {
		resolved = hcpo.constrainMessageSequenceWriteAccess(stepConfig, requested)
	} else {
		kbAccess := resolveKnowledgebaseAccess(stepConfig, hcpo.UseKnowledgebase())
		resolved = MessageSequenceWriteAccess{
			DB:            hcpo.resolveDBAccess(stepConfig) == DBAccessReadWrite,
			Knowledgebase: kbAccessAllowsWrite(kbAccess),
			Learnings:     hcpo.resolveLearningsAccess(stepConfig) == LearningsAccessReadWrite,
		}
	}
	return resolved
}

// messageSequenceStepFullWriteAccess is the step's maximal granted write scope
// (the no-override resolution). The reused execution agent's frozen tool guard is
// built from this so every synthetic closing turn (learnings/KB) can write what the
// step is configured for, regardless of which item created the agent.
func (hcpo *StepBasedWorkflowOrchestrator) messageSequenceStepFullWriteAccess(step *MessageSequencePlanStep) MessageSequenceWriteAccess {
	stepConfig := getAgentConfigs(step)
	kbAccess := resolveKnowledgebaseAccess(stepConfig, hcpo.UseKnowledgebase())
	resolved := MessageSequenceWriteAccess{
		DB:            hcpo.resolveDBAccess(stepConfig) == DBAccessReadWrite,
		Knowledgebase: kbAccessAllowsWrite(kbAccess),
		Learnings:     hcpo.resolveLearningsAccess(stepConfig) == LearningsAccessReadWrite,
	}
	return resolved
}

func (hcpo *StepBasedWorkflowOrchestrator) constrainMessageSequenceWriteAccess(stepConfig *AgentConfigs, requested MessageSequenceWriteAccess) MessageSequenceWriteAccess {
	return MessageSequenceWriteAccess{
		// DB is intentionally not narrowed by an item's write_access. Every
		// workflow step and sequence turn receives the same managed DB tools.
		DB:            hcpo.resolveDBAccess(stepConfig) == DBAccessReadWrite,
		Knowledgebase: requested.Knowledgebase && kbAccessAllowsWrite(resolveKnowledgebaseAccess(stepConfig, hcpo.UseKnowledgebase())),
		Learnings:     requested.Learnings && hcpo.resolveLearningsAccess(stepConfig) == LearningsAccessReadWrite,
	}
}

func (hcpo *StepBasedWorkflowOrchestrator) setupMessageSequenceFolderGuard(stepPath string, stepID string, stepConfig *AgentConfigs, itemWriteAccess MessageSequenceWriteAccess) (readPaths, writePaths []string) {
	baseWorkspacePath := hcpo.GetWorkspacePath()
	runWorkspacePath := baseWorkspacePath
	if hcpo.selectedRunFolder != "" {
		runWorkspacePath = fmt.Sprintf("%s/runs/%s", baseWorkspacePath, hcpo.selectedRunFolder)
	}
	executionWorkspacePath := fmt.Sprintf("%s/execution", runWorkspacePath)
	// Build from executionWorkspacePath (which includes baseWorkspacePath, the
	// workflow root) so the guard's writable step folder matches downloadsPath /
	// getDBPath and the agent-facing StepExecutionPath. messageSequenceExecutionRelPath
	// is workflow-root-RELATIVE (for the workspace-file API) and must NOT be used
	// directly as a guard path or it omits the workflow root.
	stepFolderPath := filepath.Join(executionWorkspacePath, getArtifactFolderName(stepID, stepPath))
	writeRootPath := stepFolderPath
	if parentRoot := nestedArtifactParentRoot(stepPath); parentRoot != "" {
		writeRootPath = filepath.Join(executionWorkspacePath, parentRoot)
	}
	downloadsPath := fmt.Sprintf("%s/Downloads", executionWorkspacePath)

	readPaths = []string{
		executionWorkspacePath,
		fmt.Sprintf("%s/soul", baseWorkspacePath),
		// tool_output_folder is where any bridge tool result past its inline size
		// cap is spilled (MCP_TOOL_OUTPUT_DIR) -- most often a large agent_browser
		// snapshot. setupExecutionFolderGuard has granted this since PLAT-073
		// cluster F; this parallel message_sequence builder never did, so a
		// message_sequence step told "full output saved to <path>" had no legal
		// way to read it back. Confirmed live 2026-08-17 (confida-login
		// step-5-execute-browser-and-capture-apis): its read paths carried no
		// tool_output_folder, and a spilled agent_browser result came back as
		// "access denied ... outside every workspace root" with no recoverable
		// path. Read-only: the spill is written by the bridge process, never by
		// the step.
		fmt.Sprintf("%s/tool_output_folder", baseWorkspacePath),
	}
	kbAccess := resolveKnowledgebaseAccess(stepConfig, hcpo.UseKnowledgebase())
	learningsAccess := hcpo.resolveLearningsAccess(stepConfig)
	if hcpo.platformStoresEnabled() {
		readPaths, writePaths = appendManagedDBFileAccess(baseWorkspacePath, readPaths, nil)
	}
	if kbAccessAllowsRead(kbAccess) {
		readPaths = append(readPaths, getKnowledgebasePath(baseWorkspacePath))
	}
	if learningsAccess != LearningsAccessNone {
		readPaths = appendLearningReadPaths(readPaths, baseWorkspacePath, stepID)
	}
	writePaths = append(writePaths, writeRootPath, downloadsPath)
	if itemWriteAccess.Knowledgebase && kbAccessAllowsWrite(kbAccess) {
		writePaths = append(writePaths, filepath.Join(getKnowledgebasePath(baseWorkspacePath), "notes"))
		// A "write" knowledgebase_source grants the same notes/-only write,
		// just in a resolved external workflow's KB instead of this one's own.
		writePaths = append(writePaths, writableExternalKBNotesPaths(baseWorkspacePath)...)
	}
	if itemWriteAccess.Learnings && learningsAccess == LearningsAccessReadWrite {
		globalLearnings := filepath.Join(baseWorkspacePath, LearningsFolderName, GlobalLearningID)
		// The step may write here, so the folder must exist: the sandbox refuses to start over a writable folder it
		// cannot find, and a workflow that never wrote a learning has none (server A 2026-10-05: the review step could not
		// run a single shell command, PLAT-514). Creating it is idempotent.
		if err := createFolderViaAPI(context.Background(), globalLearnings); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Could not create the global learnings folder %s: %v", globalLearnings, err))
		}
		writePaths = append(writePaths, globalLearnings)
	}
	readPaths = appendAdditionalWorkflowReadPaths(readPaths, baseWorkspacePath, stepConfig)
	readPaths, writePaths = hcpo.appendCDPHostDownloadsPaths(readPaths, writePaths)
	return common.DeduplicateStrings(readPaths), common.DeduplicateStrings(writePaths)
}

// firstValidationFileName returns the file name of the first file-validation rule
// in a schema, or "" when the schema is nil or validates only on the db. A step
// with no declared context_output is prompted to create a file ONLY when the gate
// actually checks one — otherwise it produces no throwaway output file.
func firstValidationFileName(schema *ValidationSchema) string {
	if schema == nil {
		return ""
	}
	for _, f := range schema.Files {
		if name := strings.TrimSpace(f.FileName); name != "" {
			return name
		}
	}
	return ""
}

func messageSequenceValidationSchemaJSON(schema *ValidationSchema) string {
	if schema == nil {
		return ""
	}
	schemaJSON, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return ""
	}
	return string(schemaJSON)
}

func (hcpo *StepBasedWorkflowOrchestrator) buildMessageSequenceTemplateVars(step *MessageSequencePlanStep, item MessageSequenceItem, stepIndex int, stepPath string, message string, readPaths []string, writePaths []string, writeAccess MessageSequenceWriteAccess) map[string]string {
	stepExecRel := hcpo.messageSequenceExecutionRelPath(stepPath, step.GetID())
	docsRoot := GetPromptDocsRoot()
	kbAccess := messageSequencePromptKBAccess(resolveKnowledgebaseAccess(getAgentConfigs(step), hcpo.UseKnowledgebase()), writeAccess)
	dbAccess := hcpo.resolveDBAccess(getAgentConfigs(step))
	// Honor the step's declared context_output so the sequence writes the file
	// downstream steps expect (in execution/<stepID>/, the normal step folder).
	// Fall back to the generic name only when the step declares no output.
	contextOutput := strings.TrimSpace(step.GetContextOutput().String())
	if contextOutput == "" {
		// No declared output. Only tell the agent to create a file when the step is
		// actually validated on one — and name the exact file the final gate checks so
		// prompt and gate agree. Otherwise do NOT invent a throwaway JSON: leave it
		// empty so the "no output file — persist to db" prompt branch renders and the
		// step is validated on the db / real effect (validation_schema.db), not on a
		// self-written marker nothing reads.
		contextOutput = firstValidationFileName(step.GetValidationSchema())
	}
	// Synthetic learnings/KB closing turns are contribution turns: the user message
	// should be JUST the contribution instruction (IsContributionTurn), not the
	// execute-the-task / Inputs / Output / "create the output file" scaffolding — the
	// step's work and output are already done. Blank the output so neither the prompt
	// nor the completion line claims a step output must be produced this turn.
	isContributionTurn := ""
	if kind := strings.TrimSpace(item.Kind); kind == "learning" || kind == "knowledgebase" {
		isContributionTurn = "true"
		contextOutput = ""
	}
	vars := map[string]string{
		"StepTitle":                 step.GetTitle(),
		"StepDescription":           ResolveVariables(step.GetDescription(), hcpo.variableValues),
		"BaseDescription":           ResolveVariables(step.GetDescription(), hcpo.variableValues),
		"FollowUpMessage":           message,
		"ValidationSchema":          messageSequenceValidationSchemaJSON(step.GetValidationSchema()),
		"IsContributionTurn":        isContributionTurn,
		"StepContextDependencies":   strings.Join(step.GetContextDependencies(), "\n"),
		"StepContextOutput":         contextOutput,
		"WorkspacePath":             hcpo.messageSequenceAbsPath(filepath.Join("runs", hcpo.selectedRunFolder, "execution")),
		"WorkflowRoot":              hcpo.messageSequenceAbsPath(""),
		"DocsRoot":                  docsRoot,
		"StepExecutionPath":         hcpo.messageSequenceAbsPath(stepExecRel),
		"DBPath":                    hcpo.messageSequenceAbsPath(DBFolderName),
		"DBAccess":                  dbAccess,
		"DBDirectAccess":            "false",
		"KnowledgebasePath":         hcpo.messageSequenceAbsPath(KnowledgebaseFolderName),
		"FolderGuardReadPaths":      strings.Join(toAbsPaths(docsRoot, readPaths), ", "),
		"FolderGuardWritePaths":     strings.Join(toAbsPaths(docsRoot, writePaths), ", "),
		"StepNumber":                fmt.Sprintf("%d", stepIndex+1),
		"IsCodeExecutionMode":       "false",
		"UseCodeStyleRules":         "",
		"KbAccess":                  kbAccess,
		"KbAccessLabel":             kbAccessLabel(kbAccess),
		"KBGuidanceBlock":           BuildStepKBGuidanceWithTarget(kbAccess, "", hcpo.messageSequenceAbsPath(filepath.Join(KnowledgebaseFolderName, KBNotesFolderName))),
		"MessageSequenceAccessNote": buildMessageSequenceAccessNote(writeAccess, dbAccess),
		"HasLearnings":              "false",
		"CurrentDate":               promptDate(time.Now()),
		"CurrentTime":               promptTime(time.Now()),
	}
	return applySharedKBPrompt(hcpo.GetWorkspacePath(), vars)
}

func buildMessageSequenceAccessNote(writeAccess MessageSequenceWriteAccess, dbAccess string) string {
	// No platform stores (a Relay): the folder grants are the step folder and
	// Downloads only; name no database, knowledgebase or learnings.
	if dbAccess == DBAccessNone {
		return "Writes for this item are limited to: step folder, Downloads. Readable folders are listed in Allowed READ."
	}
	grants := []string{"step folder", "Downloads", "db/assets/"}
	if writeAccess.DB {
		grants = append(grants, "database rows via mutate_workflow_db")
	}
	if writeAccess.Knowledgebase {
		grants = append(grants, "knowledgebase/notes/")
	}
	if writeAccess.Learnings {
		grants = append(grants, "learnings/_global/")
	}
	return "Database files: db/README.md is read-only; db/assets/ is readable and writable. Use query_workflow_db/mutate_workflow_db for database rows; other db/ files are not granted. Other readable folders are listed in Allowed READ. Writes for this item are limited to: " + strings.Join(grants, ", ") + "."
}

// messageSequenceAbsPath lifts a WORKFLOW-ROOT-RELATIVE path (e.g.
// "runs/<run>/execution/<stepID>", as returned by messageSequenceExecutionRelPath
// and friends) to the absolute on-disk path the step agent uses:
// <docsRoot>/<workflowRoot>/<path>. The workflow root (GetWorkspacePath, e.g.
// "Workflow/social-media") MUST be included — otherwise the agent is told to
// write to <docsRoot>/runs/... , OUTSIDE its workflow folder, where the normal
// context system can't see the file (this was the message_sequence forward-pipe
// bug). The *Rel helpers stay workflow-root-relative for the workspace-file API
// (which resolves relative to the workflow root); this is the single place that
// converts them to absolute.
func (hcpo *StepBasedWorkflowOrchestrator) messageSequenceAbsPath(workflowRel string) string {
	full := filepath.Join(hcpo.GetWorkspacePath(), workflowRel)
	docsRoot := GetPromptDocsRoot()
	if docsRoot == "" {
		return full
	}
	return filepath.Join(docsRoot, full)
}

// messageSequenceExecutionRelPath returns the step's execution folder — the SAME
// folder regular and Agent steps use (execution/<stepID>). The sequence's
// per-item artifacts and session.json live in subfolders under it, and its
// declared context_output lands directly here, so downstream context_dependencies
// resolve it exactly like any other step's output.
func (hcpo *StepBasedWorkflowOrchestrator) messageSequenceExecutionRelPath(stepPath string, stepID string) string {
	return filepath.Join("runs", hcpo.selectedRunFolder, "execution", getArtifactFolderName(stepID, stepPath))
}

func (hcpo *StepBasedWorkflowOrchestrator) renderAuthoredPrompt(ctx context.Context, prompt string) (string, error) {
	return renderAuthoredPromptWithSteps(prompt, hcpo.variableValues, func(stepID string) (string, error) {
		path := filepath.Join("runs", hcpo.selectedRunFolder, "execution", stepID, "result.json")
		return hcpo.ReadWorkspaceFile(ctx, path)
	})
}

func (hcpo *StepBasedWorkflowOrchestrator) messageSequenceSessionPath(stepPath string, stepID string) string {
	if routeRoot := messageSequenceRouteRoot(stepPath); routeRoot != "" {
		return filepath.Join("runs", hcpo.selectedRunFolder, "execution", routeRoot, "session.json")
	}
	return filepath.Join(hcpo.messageSequenceExecutionRelPath(stepPath, stepID), "session.json")
}

// cleanupMessageSequenceRuntime wipes a route's on-disk execution artifacts
// (item state, output snapshots, and session.json). Called on restart so a fresh attempt starts clean.
func (hcpo *StepBasedWorkflowOrchestrator) cleanupMessageSequenceRuntime(ctx context.Context, stepPath string, stepID string) error {
	relPath := hcpo.messageSequenceExecutionRelPath(stepPath, stepID)
	hcpo.GetLogger().Info(fmt.Sprintf("🗑️ Cleaning message_sequence runtime: %s", relPath))
	if err := hcpo.CleanupDirectory(ctx, relPath, filepath.Join("execution", getArtifactFolderName(stepID, stepPath))); err != nil {
		return fmt.Errorf("failed to cleanup message_sequence runtime %s/%s: %w", stepPath, stepID, err)
	}
	return nil
}

// msgSeqRouteKey identifies a message_sequence route's in-memory conversation within a run.
func (hcpo *StepBasedWorkflowOrchestrator) msgSeqRouteKey(stepPath, stepID string) string {
	if routeRoot := messageSequenceRouteRoot(stepPath); routeRoot != "" {
		return routeRoot
	}
	return stepPath + "/" + stepID
}

// lockMsgSeqRoute prevents concurrent calls from mutating the same stateful
// route conversation. Different routes retain full parallelism.
func (hcpo *StepBasedWorkflowOrchestrator) lockMsgSeqRoute(key string) func() {
	hcpo.msgSeqRoutesMu.Lock()
	if hcpo.msgSeqRouteLocks == nil {
		hcpo.msgSeqRouteLocks = make(map[string]*sync.Mutex)
	}
	lock := hcpo.msgSeqRouteLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		hcpo.msgSeqRouteLocks[key] = lock
	}
	hcpo.msgSeqRoutesMu.Unlock()

	lock.Lock()
	return lock.Unlock
}

// loadMsgSeqRouteSession returns a route's in-memory conversation if the orchestrator has
// already run it in this run. Route memory is never read back from disk.
func (hcpo *StepBasedWorkflowOrchestrator) loadMsgSeqRouteSession(key string) (*messageSequenceSession, bool) {
	hcpo.msgSeqRoutesMu.Lock()
	defer hcpo.msgSeqRoutesMu.Unlock()
	s, ok := hcpo.msgSeqRoutes[key]
	return s, ok
}

// storeMsgSeqRouteSession records a route's conversation so a later re-entry in the same run
// continues from where it left off.
func (hcpo *StepBasedWorkflowOrchestrator) storeMsgSeqRouteSession(key string, session *messageSequenceSession) {
	hcpo.msgSeqRoutesMu.Lock()
	defer hcpo.msgSeqRoutesMu.Unlock()
	if hcpo.msgSeqRoutes == nil {
		hcpo.msgSeqRoutes = make(map[string]*messageSequenceSession)
	}
	hcpo.msgSeqRoutes[key] = session
}

// clearMsgSeqRouteSession drops a route's in-memory conversation (used on restart).
func (hcpo *StepBasedWorkflowOrchestrator) clearMsgSeqRouteSession(key string) {
	hcpo.msgSeqRoutesMu.Lock()
	session := hcpo.msgSeqRoutes[key]
	delete(hcpo.msgSeqRoutes, key)
	hcpo.msgSeqRoutesMu.Unlock()
	hcpo.closeMessageSequenceRuntime(session, "message_sequence route restarted")
}

// clearAllMsgSeqRouteSessions drops every route's in-memory conversation and
// closes the underlying runtimes. Route memory is scoped to one execution
// phase — without this drain, a reused orchestrator instance (next iteration
// or next run) silently resumes a prior run's route conversations and the
// runtimes leak.
func (hcpo *StepBasedWorkflowOrchestrator) clearAllMsgSeqRouteSessions(reason string) {
	hcpo.msgSeqRoutesMu.Lock()
	sessions := hcpo.msgSeqRoutes
	hcpo.msgSeqRoutes = nil
	hcpo.msgSeqRouteLocks = nil
	hcpo.msgSeqRoutesMu.Unlock()
	for key, session := range sessions {
		hcpo.GetLogger().Info(fmt.Sprintf("🧹 Dropping message_sequence route session %q (%s)", key, reason))
		hcpo.closeMessageSequenceRuntime(session, reason)
	}
}

func (hcpo *StepBasedWorkflowOrchestrator) saveMessageSequenceSession(ctx context.Context, relPath string, session *messageSequenceSession) error {
	out, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	return hcpo.WriteWorkspaceFile(ctx, relPath, string(out))
}

// summarizeMessageSequenceSession is the step's result: what the parent chat's
// completion notification carries and what saveFinalExecutionSummary stores.
// It is the last completed authored item's answer, the same thing a regular
// step reports. A bare "N item(s) completed" count told the user nothing once
// message_sequence became the default step type. Prevalidation gates and
// synthetic __...__ items are bookkeeping, not answers, so they are skipped. The
// count remains the fallback when no authored item produced text.
func (hcpo *StepBasedWorkflowOrchestrator) summarizeMessageSequenceSession(session *messageSequenceSession) string {
	completed := 0
	for _, entry := range session.Entries {
		if entry.Status == "completed" {
			completed++
		}
	}
	for i := len(session.Entries) - 1; i >= 0; i-- {
		entry := session.Entries[i]
		if entry.Status != "completed" || entry.ItemType == "prevalidation" || strings.HasPrefix(entry.ItemID, "__") {
			continue
		}
		if answer := strings.TrimSpace(entry.Summary); answer != "" {
			return answer
		}
	}
	return fmt.Sprintf("Message sequence %s completed: %d item(s) completed", session.StepID, completed)
}

// messageSequenceHaltedBeforeItem reports why a message_sequence queue must not
// start another item, or nil to proceed.
//
// PLAT-130. Clicking Stop on a schedule canceled the run's context correctly,
// and the item loop already returned on any item error — yet a queued item
// still fired, because "the context was canceled" only becomes an error if
// some layer underneath converts it into one. Session teardown races that
// conversion: a coding-CLI turn whose pane is being killed can return a
// truncated-but-plausible result rather than a failure, and the queue reads
// that as success and moves on. The next item then runs its real side effect —
// a post, a write, an outbound call — after the UI, the run history and the
// schedule status all say the run stopped.
//
// Checking the context directly removes the dependency on that conversion. It
// is deliberately a pre-item gate rather than a mid-item abort: an item already
// in flight is left to unwind through its own error path, but no new work
// begins once the run is canceled.
func messageSequenceHaltedBeforeItem(ctx context.Context, stepID, itemID string) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("message_sequence step %q halted before item %q: %w", stepID, itemID, err)
	}
	return nil
}

// messageSequencePromptKBAccess is the knowledge-base access a sequence item's
// prompt advertises. It must match the folder guard (setupMessageSequenceFolderGuard):
// the prompt used to say "read" for every step, so a step with
// knowledgebase_access "none" was told to read knowledgebase/context and was
// then refused by the sandbox ("Operation not permitted"), PLAT-438.
func messageSequencePromptKBAccess(stepAccess string, writeAccess MessageSequenceWriteAccess) string {
	if !kbAccessAllowsRead(stepAccess) {
		return KBAccessNone
	}
	if writeAccess.Knowledgebase && kbAccessAllowsWrite(stepAccess) {
		return KBAccessReadWrite
	}
	return KBAccessRead
}

// authoredRoutesPromptBlock tells an authored agent (whose system prompt is used
// verbatim) which tools it has. Only tools that were actually registered are
// listed, under the name they were registered with (toolNames: route id -> name):
// an agent is never told about a tool it cannot call (PLAT-444). An authored
// agent has script tools only, never sub-agents. The author's text is not altered.
func authoredRoutesPromptBlock(routes []PlanOrchestrationRoute, toolNames map[string]string) string {
	var tools []string
	for _, route := range routes {
		name := toolNames[route.RouteID]
		if name == "" {
			continue // not registered: the agent has no such tool
		}
		condition := strings.TrimSpace(route.Condition)
		if condition == "" && route.SubAgentStep != nil {
			condition = strings.TrimSpace(route.SubAgentStep.GetDescription())
		}
		tools = append(tools, fmt.Sprintf("- `%s`: %s", name, condition))
	}
	if len(tools) == 0 {
		return ""
	}
	return "\n\n## Tools of this agent\nCall these tools when the task needs them; each runs a saved script and returns JSON:\n" +
		strings.Join(tools, "\n") +
		"\nYour final answer is still the JSON this prompt asks for."
}
