//nolint:misspell // "cancelled" is the established workshop status text and is surfaced to users.
package step_based_workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/testmode"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/contractupgrade"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	orchestrator_events "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulsemodules"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulepolicy"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowkb"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	baseevents "github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/observability"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	_ "modernc.org/sqlite"
)

const maxCDPPortsPerWorkflow = 4

const statusPollWindow = 60 * time.Second

const statusPollNextAction = `**NEXT ACTION: End the current agent turn now.**
This execution sends an automatic completion notification. Do not call query_step or list_executions again unless the user explicitly requests another status check.`

const statusPollWarning = `**POLLING WARNING:** This is a repeated status check while background work is still running. End the current agent turn now; the runtime will resume the session automatically when the execution changes or completes.`

const statusPollSuppressed = `**POLLING SUPPRESSED**
The background execution state has not changed since the previous status check. A completion notification will resume this session automatically.

**NEXT ACTION: End the current agent turn now.** Do not call query_step or list_executions again unless the user explicitly requests another status check.`

var workshopStageAgentIdentityCounter atomic.Uint64

func parseWorkshopIterationNumber(iteration string) int {
	if iteration == "" {
		return 0
	}
	trimmed := strings.TrimSpace(iteration)
	trimmed = strings.TrimPrefix(trimmed, "iteration-")
	trimmed = strings.TrimSuffix(strings.TrimSuffix(trimmed, "-hook"), "-sched")
	if n, err := strconv.Atoi(trimmed); err == nil {
		return n
	}
	return 0
}

func sanitizeWorkshopAgentIdentityPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	identity := strings.Trim(builder.String(), "-")
	if identity == "" {
		identity = "reviewer"
	}
	if len(identity) > 48 {
		identity = strings.TrimRight(identity[:48], "-")
	}
	return identity
}

func newWorkshopStageAgentIdentity(name string) string {
	// Keep every workshop stage identity stable and readable in durable
	// background-agent logs and tool-session diagnostics.
	prefix := time.Now().UTC().Format("2006-01-02T15-04-05.000Z")
	return fmt.Sprintf("%s_%s-%d", prefix, sanitizeWorkshopAgentIdentityPart(name), workshopStageAgentIdentityCounter.Add(1))
}

func stringSliceArgument(args map[string]interface{}, key string) ([]string, bool) {
	raw, supplied := args[key]
	if !supplied || raw == nil {
		return nil, supplied
	}
	if values, ok := raw.([]string); ok {
		return append([]string(nil), values...), true
	}
	values, ok := raw.([]interface{})
	if !ok {
		return nil, true
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result, true
}

// ValidatePulseReviewIdentity rejects a review identity that cannot name a
// saved review. It is exported because the review read is served by the shared
// get_pulse_state tool in the server tool pool, not by a workshop-local tool.
func ValidatePulseReviewIdentity(reviewRunID, module string) error {
	reviewRunID = strings.TrimSpace(reviewRunID)
	module = strings.TrimSpace(module)
	separator := strings.IndexByte(reviewRunID, '_')
	if separator <= 0 {
		return fmt.Errorf("review_run_id %q must start with a UTC date-time and an underscore, e.g. 2026-08-01T00-00-00.000Z_<suffix>; use the id exactly as reported by the reviewer completion notification", reviewRunID)
	}
	if _, err := time.Parse("2006-01-02T15-04-05.000Z", reviewRunID[:separator]); err != nil {
		return fmt.Errorf("review_run_id %q must start with YYYY-MM-DDTHH-MM-SS.mmmZ: %w", reviewRunID, err)
	}
	for _, r := range reviewRunID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return fmt.Errorf("review_run_id contains unsupported path characters")
		}
	}
	// Derived from the canonical registry — see pkg/pulsemodules. Only current
	// perspectives are accepted: evidence packs never get independent receipt
	// identities. Omitting a current module here silently breaks its persistence.
	valid := false
	for _, accepted := range pulsemodules.AcceptedForReviewReceipts() {
		if accepted == module {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("module %q is not a valid Pulse review module. Must be one of: %s",
			module, strings.Join(pulsemodules.AcceptedForReviewReceipts(), ", "))
	}
	return nil
}

func isMissingOrEmptyWorkspaceError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "not found") ||
		strings.Contains(errStr, "no such file") ||
		strings.Contains(errStr, "no content found")
}

// ============================================================================
// Workshop Step Hierarchy — recursive step discovery for inner steps
// ============================================================================

// WorkshopStepInfo describes a step in the plan, including its position in the hierarchy.
type WorkshopStepInfo struct {
	Step           PlanStepInterface
	ParentID       string // empty for top-level steps
	ParentType     StepType
	NestedLocation string // e.g. "route:route-id" or "todo_task_step"
	TopIndex       int    // 1-based index of the top-level step this belongs to (-1 if inner)
	IsOrphan       bool   // true for orphan steps (workshop-only, not in main execution flow)
}

// collectAllSteps returns a flat list of all steps in the plan, including inner steps
// from todo task routes and sub-agents.
func collectAllSteps(steps []PlanStepInterface) []WorkshopStepInfo {
	var result []WorkshopStepInfo
	for i, step := range steps {
		result = append(result, WorkshopStepInfo{
			Step:     step,
			TopIndex: i + 1,
		})
		result = append(result, collectInnerSteps(step)...)
	}
	return result
}

// collectInnerSteps recursively extracts inner steps from a step.
func collectInnerSteps(step PlanStepInterface) []WorkshopStepInfo {
	var result []WorkshopStepInfo
	parentID := step.GetID()
	parentType := step.StepType()

	switch s := step.(type) {
	case *OrchestratorPlanStep:
		for _, route := range s.PredefinedRoutes {
			if route.SubAgentStep != nil {
				result = append(result, WorkshopStepInfo{
					Step: route.SubAgentStep, ParentID: parentID, ParentType: parentType,
					NestedLocation: fmt.Sprintf("route:%s", route.RouteID), TopIndex: -1,
				})
				result = append(result, collectInnerSteps(route.SubAgentStep)...)
			}
		}
	}
	return result
}

// StepModeAgentic is the canonical mode name for steps where the LLM acts
// each turn with tools available (the default). Previously called
// "agentic" — the old name biased agents toward writing Python scripts
// when a tool call would do. "Agentic" describes the actual behavior:
// the LLM decides each turn what to call. Old name is still accepted on
// read for backward compatibility with persisted configs.
const StepModeAgentic = "agentic"

// StepModeScripted is the canonical mode name for steps that author a
// reusable main.py saved at learnings/{step-id}/main.py, replayed
// deterministically on future runs. Previously called "scripted".
// The on-disk directory name remains "learnings/" — the mode label and
// the directory are decoupled. Old name is still accepted on read.
const StepModeScripted = "scripted"

// canonicalDeclaredExecutionMode normalizes the declared execution mode
// string. It accepts both the new canonical names ("agentic", "scripted")
// and the legacy names ("agentic", "scripted") on input, always
// returning the canonical form. Empty input returns empty.
func canonicalDeclaredExecutionMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "code_exec", StepModeAgentic:
		return StepModeAgentic
	case "learn_code", StepModeScripted:
		return StepModeScripted
	default:
		return strings.TrimSpace(mode)
	}
}

// isScriptedStep reports whether a step runs through the scripted executor
// (a persistent learnings/{step-id}/main.py, replayed across runs). The plan
// decides (PLAT-287): a regular step IS scripted, a message_sequence is
// conversational. The one exception is transitional: a regular step whose
// step_config.json still carries the retired declared_execution_mode="agentic"
// key (not yet stripped by v1.0.39) is a legacy agentic step the runtime keeps
// running as a message_sequence.
// cfg may be nil.
func isScriptedStep(step PlanStepInterface, cfg *AgentConfigs) bool {
	switch step.(type) {
	case *RegularPlanStep:
		return cfg.legacyDeclared() != StepModeAgentic
	default:
		return false
	}
}

// workshopPlanStepIndex maps every step id in the current workflow plan
// (nested and orphan) to its step, for callers that hold step_config entries
// and need the plan type to know how each runs.
func workshopPlanStepIndex(ctx context.Context, controller *StepBasedWorkflowOrchestrator) map[string]PlanStepInterface {
	index := map[string]PlanStepInterface{}
	if controller == nil {
		return index
	}
	plan, err := controller.ReadCurrentPlan(ctx)
	if err != nil || plan == nil {
		return index
	}
	for _, info := range append(collectAllSteps(plan.Steps), collectAllSteps(plan.OrphanSteps)...) {
		if info.Step != nil {
			index[info.Step.GetID()] = info.Step
		}
	}
	return index
}

// executionModeLabel renders the derived mode for event/context tagging.
func executionModeLabel(scripted bool) string {
	if scripted {
		return StepModeScripted
	}
	return StepModeAgentic
}

// isLegacyAgenticRegularStep is the transitional shim's predicate: a regular
// step whose unstripped step_config.json still declares agentic. Such a step
// executed as a message_sequence before PLAT-287 and must keep doing so until
// the v1.0.38 migration rewrites its plan type -- which the scheduler's
// preflight runs before a scheduled workflow, but a manual run does not wait
// for.
func isLegacyAgenticRegularStep(step PlanStepInterface, cfg *AgentConfigs) bool {
	_, regular := step.(*RegularPlanStep)
	return regular && cfg.legacyDeclared() == StepModeAgentic
}

// findWorkshopStepByID searches all steps (including inner) for a matching ID.
func findWorkshopStepByID(steps []PlanStepInterface, stepID string) *WorkshopStepInfo {
	all := collectAllSteps(steps)
	for _, info := range all {
		if info.Step.GetID() == stepID {
			return &info
		}
	}
	return nil
}

// workshopStepLogFolder mirrors the runtime artifact writer: declared step IDs
// are the canonical folder names for normal and directly addressed inner steps.
func workshopStepLogFolder(stepID string) string {
	return strings.TrimSpace(stepID)
}

// ============================================================================
// Complex Step Query Enrichment — lightweight hints for todo_task/orchestration
// ============================================================================

// enrichQueryForComplexStep detects todo_task/orchestration steps and returns
// a compact hint with iteration count and log paths for deeper inspection.
// Keeps responses small — the LLM can use execute_shell_command to dig deeper.
func (iwm *InteractiveWorkshopManager) enrichQueryForComplexStep(
	ctx context.Context,
	stepID string,
) string {
	plan, err := iwm.controller.ReadCurrentPlan(ctx)
	if err != nil {
		return ""
	}
	stepInfo := findWorkshopStepByID(plan.Steps, stepID)
	if stepInfo == nil || stepInfo.TopIndex < 1 {
		return ""
	}

	stepType := stepInfo.Step.StepType()
	var logFileName, stepTypeName string
	switch stepType {
	case StepTypeOrchestrator:
		logFileName = "todo-task-execution.json"
		stepTypeName = "Todo Task"
	case StepTypeRouting, StepTypeBranch:
		logFileName = "orchestration-execution.json"
		stepTypeName = "Orchestration"
	default:
		return ""
	}

	runFolder := iwm.controller.selectedRunFolder
	if runFolder == "" {
		return ""
	}

	logFolder := workshopStepLogFolder(stepID)
	logPath := fmt.Sprintf("runs/%s/logs/%s/%s", runFolder, logFolder, logFileName)

	content, err := iwm.controller.ReadWorkspaceFile(ctx, logPath)
	if err != nil {
		return ""
	}

	// Count iterations and collect sub-agent paths + tier usage from JSONL
	lines := strings.Split(strings.TrimSpace(content), "\n")
	iterations := 0
	var subAgentPaths []string
	seen := make(map[string]bool)
	var lastEntry map[string]interface{} // retain last parsed entry for todo progress

	// Track tier usage per route/generic agent for optimization analysis
	type tierUsageEntry struct {
		RouteID   string
		RouteName string
		TodoID    string
		TodoTitle string
		Tier      int
		TierLabel string
		IsGeneric bool
	}
	var tierUsageLog []tierUsageEntry

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		iterations++

		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		lastEntry = entry

		// Extract sub-agent path from the response
		var response map[string]interface{}
		if stepType == StepTypeOrchestrator {
			response, _ = entry["todo_task_response"].(map[string]interface{})
		} else {
			response, _ = entry["orchestration_response"].(map[string]interface{})
		}
		if response == nil {
			continue
		}

		subPath, _ := response["selected_sub_agent_path"].(string)
		if subPath != "" && !seen[subPath] {
			seen[subPath] = true
			subAgentPaths = append(subAgentPaths, subPath)
		}

		// Extract tier usage data for todo_task steps
		if stepType == StepTypeOrchestrator {
			nextAction, _ := response["next_action"].(string)
			if nextAction == "delegate" {
				tierNum := 0
				if t, ok := response["preferred_tier"].(float64); ok {
					tierNum = int(t)
				}
				tierLabel, _ := response["preferred_tier_label"].(string)
				routeID, _ := response["selected_route_id"].(string)
				routeName, _ := response["selected_route_name"].(string)
				todoID, _ := response["todo_id_to_execute"].(string)
				todoTitle, _ := response["todo_title"].(string)
				isGeneric, _ := response["use_generic_agent"].(bool)

				tierUsageLog = append(tierUsageLog, tierUsageEntry{
					RouteID:   routeID,
					RouteName: routeName,
					TodoID:    todoID,
					TodoTitle: todoTitle,
					Tier:      tierNum,
					TierLabel: tierLabel,
					IsGeneric: isGeneric,
				})
			}
		}
	}

	if iterations == 0 {
		return ""
	}

	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("\n\n[%s step — %d iterations", stepTypeName, iterations))

	// Show todo progress from the last entry (already parsed in main loop)
	if stepType == StepTypeOrchestrator && lastEntry != nil {
		if todoSummary, ok := lastEntry["todo_summary"].(map[string]interface{}); ok {
			total, _ := todoSummary["total"].(float64)
			completed, _ := todoSummary["completed"].(float64)
			if total > 0 {
				summary.WriteString(fmt.Sprintf(", %.0f/%.0f tasks done", completed, total))
			}
		}
	}
	summary.WriteString("]\n")

	// Log paths for deeper inspection
	summary.WriteString(fmt.Sprintf("Routing log: %s\n", logPath))
	if len(subAgentPaths) > 0 {
		summary.WriteString("Sub-agent logs:\n")
		for _, p := range subAgentPaths {
			summary.WriteString(fmt.Sprintf("  - runs/%s/logs/%s/execution/\n", runFolder, p))
		}
	}
	summary.WriteString("Use execute_shell_command to read these files for details.")

	// Append tier usage summary for todo_task steps
	if len(tierUsageLog) > 0 {
		summary.WriteString("\n\nTier Usage per Sub-Agent:\n")
		summary.WriteString("| Route/Agent | Todo | Tier | Label |\n")
		summary.WriteString("| :--- | :--- | :--- | :--- |\n")
		for _, tu := range tierUsageLog {
			agentName := tu.RouteName
			if agentName == "" && tu.IsGeneric {
				agentName = "generic-agent"
			} else if agentName == "" {
				agentName = tu.RouteID
			}
			tierStr := "auto"
			if tu.Tier > 0 {
				tierStr = fmt.Sprintf("%d", tu.Tier)
			}
			tierLabel := tu.TierLabel
			if tierLabel == "" {
				tierLabel = "auto-selected"
			}
			summary.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", agentName, tu.TodoTitle, tierStr, tierLabel))
		}
	}

	return summary.String()
}

// ============================================================================
// Workshop Step Registry — tracks background step executions
// ============================================================================

// WorkshopStepStatus represents the status of a background step execution
type WorkshopStepStatus string

const (
	WorkshopStepRunning   WorkshopStepStatus = "running"
	WorkshopStepDone      WorkshopStepStatus = "done"
	WorkshopStepFailed    WorkshopStepStatus = "failed"
	WorkshopStepCancelled WorkshopStepStatus = "cancelled"
)

// ToolCallQueryFunc queries live tool calls associated with a workshop execution.
// Parameters: sessionID (main session), correlationID (agentSessionID for the step execution), stepID, toolCallID (empty for summary, specific ID for detail).
// Returns a formatted string summary of tool calls. Nil means the feature is unavailable.
type ToolCallQueryFunc func(sessionID, correlationID, stepID, toolCallID string) string

// TmuxLookupFunc resolves the live tmux session for a workshop step running a
// coding-CLI provider (tmux transport). Given the terminal-store session key and
// stepID, it returns the tmux session name and a freshly-captured tail of the pane
// (the latest terminal output), plus ok=true when the step is tmux-backed. paneTail
// may be empty if the live capture failed. Nil func means the feature is unavailable.
type TmuxLookupFunc func(ctx context.Context, mainSessionID, stepID string) (tmuxSession, paneTail string, ok bool)

// WorkshopStepExecution tracks a single background step execution
type WorkshopStepExecution struct {
	ID                      string
	StepID                  string
	AgentSessionID          string // correlation ID used to tag events for this execution
	Status                  WorkshopStepStatus
	CreatedAt               time.Time
	Result                  string
	Err                     error
	cancel                  context.CancelFunc
	mu                      sync.RWMutex
	messageSendMu           sync.Mutex
	messageTarget           *workshopStepMessageTarget
	messageTargetGeneration uint64
}

// WorkshopExecutionStart carries the canonical information needed to register a running execution.
type WorkshopExecutionStart struct {
	ID                string
	ParentExecutionID string
	Name              string
	Kind              string
	Metadata          map[string]string
	Cancel            context.CancelFunc
}

// WorkshopStepSnapshot is a read-only copy of a tracked execution for external callers.
type WorkshopStepSnapshot struct {
	ID                 string
	StepID             string
	AgentSessionID     string
	Status             WorkshopStepStatus
	CreatedAt          time.Time
	Result             string
	Err                error
	CanCancel          bool
	CanReceiveMessage  bool
	ActiveMessagePhase string
}

var (
	ErrWorkshopExecutionNotFound      = errors.New("workshop execution not found")
	ErrWorkshopExecutionNotCancelable = errors.New("workshop execution is not cancelable")
)

func (e *WorkshopStepExecution) Snapshot() WorkshopStepSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	snapshot := WorkshopStepSnapshot{
		ID:             e.ID,
		StepID:         e.StepID,
		AgentSessionID: e.AgentSessionID,
		Status:         e.Status,
		CreatedAt:      e.CreatedAt,
		Result:         e.Result,
		Err:            e.Err,
		CanCancel:      e.Status == WorkshopStepRunning && e.cancel != nil,
	}
	if e.Status == WorkshopStepRunning && e.messageTarget != nil {
		snapshot.CanReceiveMessage = true
		snapshot.ActiveMessagePhase = e.messageTarget.phase
	}
	return snapshot
}

// finalizeExecStatus sets the final status on a WorkshopStepExecution under lock,
// with context-cancellation detection.
//
// Returns true only when stop_step/stop_all already set the status to Cancelled
// (meaning OnExecutionTerminated was already called — skip OnExecutionComplete).
// Returns false for context-cancelled errors (status is set to Cancelled but
// OnExecutionComplete must still fire since OnExecutionTerminated was NOT called).
//
// Callers that need to distinguish cancel vs failure for display purposes should
// check execCtx.Err() != nil after this call.
//
// Usage at the top of every background execution goroutine:
//
//	var result string
//	var execErr error
//	defer func() {
//	    skipNotify := finalizeExecStatus(exec, execCtx, &result, &execErr)
//	    // ... eventBridge end event (use execCtx.Err() != nil for cancel display) ...
//	    if !skipNotify && notifier != nil {
//	        notifier.OnExecutionComplete(execID, name, result, meta, execErr)
//	    }
//	}()
func finalizeExecStatus(exec *WorkshopStepExecution, ctx context.Context, result *string, execErr *error) (skipNotify bool) {
	exec.messageSendMu.Lock()
	defer exec.messageSendMu.Unlock()
	exec.mu.Lock()
	defer exec.mu.Unlock()
	exec.messageTarget = nil
	exec.cancel = nil
	if exec.Status == WorkshopStepCancelled {
		log.Printf("[FINALIZE_EXEC] exec=%s step=%s — already cancelled by stop_step, skipNotify=true", exec.ID, exec.StepID)
		return true // stop_step already called OnExecutionTerminated
	}
	if *execErr != nil {
		errStr := strings.ToLower((*execErr).Error())
		isTimeout := errors.Is(*execErr, context.DeadlineExceeded) ||
			(ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)) ||
			strings.Contains(errStr, "timed out") ||
			strings.Contains(errStr, "timeout") ||
			strings.Contains(errStr, "deadline exceeded")

		if isTimeout {
			exec.Status = WorkshopStepFailed
			exec.Err = *execErr
			log.Printf("[FINALIZE_EXEC] exec=%s step=%s — timeout detected (err=%v), status=Failed, skipNotify=false", exec.ID, exec.StepID, *execErr)
		} else if ctx.Err() == context.Canceled || errors.Is(*execErr, context.Canceled) {
			exec.Status = WorkshopStepCancelled
			exec.Err = *execErr
			log.Printf("[FINALIZE_EXEC] exec=%s step=%s — context cancelled (err=%v), status=Cancelled, skipNotify=false (OnExecutionComplete will fire)", exec.ID, exec.StepID, *execErr)
		} else {
			exec.Status = WorkshopStepFailed
			exec.Err = *execErr
			log.Printf("[FINALIZE_EXEC] exec=%s step=%s — failed (err=%v)", exec.ID, exec.StepID, *execErr)
		}
	} else {
		exec.Status = WorkshopStepDone
		log.Printf("[FINALIZE_EXEC] exec=%s step=%s — done (result_len=%d)", exec.ID, exec.StepID, len(*result))
		exec.Result = *result
	}
	return false
}

// WorkshopStepRegistry tracks all background step executions for a workshop session
type WorkshopStepRegistry struct {
	mu         sync.RWMutex
	executions map[string]*WorkshopStepExecution

	statusPollMu              sync.Mutex
	statusPollWindowStartedAt time.Time
	statusPollLastCalledAt    time.Time
	statusPollFingerprint     string
	statusPollCount           int
}

type statusPollDecision struct {
	Count    int
	Changed  bool
	Warn     bool
	Suppress bool
}

func newWorkshopStepRegistry() *WorkshopStepRegistry {
	return &WorkshopStepRegistry{
		executions: make(map[string]*WorkshopStepExecution),
	}
}

// NewWorkshopStepRegistry creates a new empty WorkshopStepRegistry (exported for use in server.go chat mode).
func NewWorkshopStepRegistry() *WorkshopStepRegistry {
	return newWorkshopStepRegistry()
}

// observeStatusPoll applies one shared rapid-poll budget across query_step and
// list_executions. The registry is session-scoped and survives tool
// re-registration between agent turns, so alternating tools cannot bypass the
// guard. Changed state is always returned, but it does not reset the rapid-poll
// count; the next unchanged call is still suppressed.
func (r *WorkshopStepRegistry) observeStatusPoll(now time.Time, fingerprint string) statusPollDecision {
	r.statusPollMu.Lock()
	defer r.statusPollMu.Unlock()

	if r.statusPollLastCalledAt.IsZero() ||
		now.Sub(r.statusPollLastCalledAt) > statusPollWindow ||
		now.Sub(r.statusPollWindowStartedAt) > statusPollWindow {
		r.statusPollWindowStartedAt = now
		r.statusPollCount = 0
		r.statusPollFingerprint = ""
	}

	changed := r.statusPollFingerprint != "" && r.statusPollFingerprint != fingerprint
	r.statusPollCount++
	r.statusPollLastCalledAt = now
	r.statusPollFingerprint = fingerprint

	decision := statusPollDecision{
		Count:   r.statusPollCount,
		Changed: changed,
		Warn:    r.statusPollCount >= 2,
	}
	decision.Suppress = r.statusPollCount >= 3 && !changed
	return decision
}

// Register adds a new execution entry to the registry
func (r *WorkshopStepRegistry) Register(exec *WorkshopStepExecution) {
	exec.mu.Lock()
	if exec.CreatedAt.IsZero() {
		exec.CreatedAt = time.Now()
	}
	exec.mu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	r.executions[exec.ID] = exec
}

// Get retrieves an execution by ID; returns nil if not found.
// Internal callers may use this for in-place status/result updates.
func (r *WorkshopStepRegistry) Get(id string) *WorkshopStepExecution {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.executions[id]
}

// GetSnapshot retrieves a read-only snapshot of an execution by ID.
func (r *WorkshopStepRegistry) GetSnapshot(id string) (WorkshopStepSnapshot, bool) {
	r.mu.RLock()
	exec := r.executions[id]
	r.mu.RUnlock()
	if exec == nil {
		return WorkshopStepSnapshot{}, false
	}
	return exec.Snapshot(), true
}

// LatestSnapshotForStep returns the newest tracked execution for a step.
// Running executions are preferred; completed executions are used only when
// nothing is running.
func (r *WorkshopStepRegistry) LatestSnapshotForStep(stepID string) (WorkshopStepSnapshot, bool, []WorkshopStepSnapshot) {
	r.mu.RLock()
	snapshots := make([]WorkshopStepSnapshot, 0)
	for _, exec := range r.executions {
		snap := exec.Snapshot()
		if snap.StepID == stepID {
			snapshots = append(snapshots, snap)
		}
	}
	r.mu.RUnlock()
	if len(snapshots) == 0 {
		return WorkshopStepSnapshot{}, false, nil
	}

	sort.SliceStable(snapshots, func(i, j int) bool {
		leftRunning := snapshots[i].Status == WorkshopStepRunning
		rightRunning := snapshots[j].Status == WorkshopStepRunning
		if leftRunning != rightRunning {
			return leftRunning
		}
		if snapshots[i].CreatedAt.Equal(snapshots[j].CreatedAt) {
			return snapshots[i].ID > snapshots[j].ID
		}
		return snapshots[i].CreatedAt.After(snapshots[j].CreatedAt)
	})
	return snapshots[0], true, snapshots
}

// Cancel cancels an execution by ID and returns its updated snapshot.
func (r *WorkshopStepRegistry) Cancel(id string) (WorkshopStepSnapshot, error) {
	return r.cancelWithNotification(id, nil)
}

func stopWorkshopExecution(registry *WorkshopStepRegistry, notifier WorkshopExecutionNotifier, executionID string) (WorkshopStepSnapshot, error) {
	if registry == nil {
		return WorkshopStepSnapshot{}, ErrWorkshopExecutionNotFound
	}
	return registry.cancelWithNotification(executionID, func(exec WorkshopStepSnapshot) {
		if notifier != nil {
			notifier.OnExecutionTerminated(executionID, exec.StepID)
		}
	})
}

// Publish cancellation before the context unwinds so children cannot report
// that explicit stop as an unrelated runtime failure.
func (r *WorkshopStepRegistry) cancelWithNotification(id string, notify func(WorkshopStepSnapshot)) (WorkshopStepSnapshot, error) {
	r.mu.RLock()
	exec := r.executions[id]
	r.mu.RUnlock()
	if exec == nil {
		return WorkshopStepSnapshot{}, ErrWorkshopExecutionNotFound
	}

	exec.messageSendMu.Lock()
	exec.mu.Lock()
	if exec.Status != WorkshopStepRunning || exec.cancel == nil {
		exec.mu.Unlock()
		exec.messageSendMu.Unlock()
		return exec.Snapshot(), ErrWorkshopExecutionNotCancelable
	}
	cancel := exec.cancel
	exec.Status = WorkshopStepCancelled
	exec.messageTarget = nil
	exec.cancel = nil
	exec.mu.Unlock()
	exec.messageSendMu.Unlock()
	snapshot := exec.Snapshot()
	if notify != nil {
		notify(snapshot)
	}
	cancel()
	return snapshot, nil
}

// CancelAll cancels all running executions and returns their updated snapshots.
func (r *WorkshopStepRegistry) CancelAll() []WorkshopStepSnapshot {
	return r.cancelAllWithNotification(nil)
}

func (r *WorkshopStepRegistry) cancelAllWithNotification(notify func(WorkshopStepSnapshot)) []WorkshopStepSnapshot {
	r.mu.RLock()
	executions := make([]*WorkshopStepExecution, 0, len(r.executions))
	for _, exec := range r.executions {
		executions = append(executions, exec)
	}
	r.mu.RUnlock()
	cancelled := make([]WorkshopStepSnapshot, 0, len(executions))
	callbacks := make([]context.CancelFunc, 0, len(executions))
	for _, exec := range executions {
		exec.messageSendMu.Lock()
		exec.mu.Lock()
		if exec.Status != WorkshopStepRunning || exec.cancel == nil {
			exec.mu.Unlock()
			exec.messageSendMu.Unlock()
			continue
		}
		callbacks = append(callbacks, exec.cancel)
		exec.Status = WorkshopStepCancelled
		exec.messageTarget = nil
		exec.cancel = nil
		exec.mu.Unlock()
		exec.messageSendMu.Unlock()
		cancelled = append(cancelled, exec.Snapshot())
	}
	// Settle every workshop entry before publishing or unwinding any worker.
	if notify != nil {
		for _, exec := range cancelled {
			notify(exec)
		}
	}
	for _, cancel := range callbacks {
		cancel()
	}
	return cancelled
}

// ListSnapshots returns read-only snapshots of all tracked executions.
func (r *WorkshopStepRegistry) ListSnapshots() []WorkshopStepSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]WorkshopStepSnapshot, 0, len(r.executions))
	for _, e := range r.executions {
		list = append(list, e.Snapshot())
	}
	return list
}

func (iwm *InteractiveWorkshopManager) statusPollStateFingerprint() string {
	parts := make([]string, 0)
	for _, exec := range iwm.stepRegistry.ListSnapshots() {
		parts = append(parts, fmt.Sprintf("registry:%s:%s", exec.ID, exec.Status))
	}
	if iwm.listServerAgents != nil {
		for _, agent := range iwm.listServerAgents() {
			parts = append(parts, fmt.Sprintf("server:%s:%s", agent.ID, agent.Status))
		}
	}
	if iwm.hasPendingCompletions != nil && iwm.hasPendingCompletions() {
		parts = append(parts, "pending-completions:true")
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

func (iwm *InteractiveWorkshopManager) runningStatusPollGuidance() (string, bool) {
	decision := iwm.stepRegistry.observeStatusPoll(time.Now(), iwm.statusPollStateFingerprint())
	if decision.Suppress {
		return statusPollSuppressed, true
	}
	if decision.Warn {
		return statusPollWarning + "\n\n" + statusPollNextAction, false
	}
	return statusPollNextAction, false
}

// WorkshopExecutionNotifier is called when workshop step/background executions start and complete.
// Implemented by the server layer to register executions in bgAgentRegistry so that
// HasRunningAgents() returns true and the frontend keeps polling for events.
type WorkshopExecutionNotifier interface {
	OnExecutionStart(start WorkshopExecutionStart)
	OnExecutionComplete(execID, name, result string, meta map[string]string, err error)
	OnExecutionTerminated(execID, name string) // explicit cancellation via stop_step/stop_all
}

// registerWorkshopExecutionBeforeLaunch is intentionally synchronous. Async
// workshop tools must make their live execution visible before returning the
// execution_id to the parent agent; otherwise a scheduler can observe the
// parent turn as idle before the child has entered the registry.
func registerWorkshopExecutionBeforeLaunch(notifier WorkshopExecutionNotifier, start WorkshopExecutionStart) {
	if notifier != nil {
		notifier.OnExecutionStart(start)
	}
}

// ServerAgentInfo is a lightweight snapshot of a server-tracked background agent.
type ServerAgentInfo struct {
	ID     string
	Name   string
	Status string // "running", "completed", "failed", "canceled"
}

// ============================================================================
// InteractiveWorkshopManager
// ============================================================================

// InteractiveWorkshopManager manages the interactive workshop phase
type InteractiveWorkshopManager struct {
	controller     *StepBasedWorkflowOrchestrator
	workshopConfig *WorkshopConfig
	presetLLM      *AgentLLMConfig
	sessionID      string
	workflowID     string
	stepRegistry   *WorkshopStepRegistry
	// executeStartMu serializes execute_step's start checks so a test run and
	// another execution never start together (PLAT-562).
	executeStartMu sync.Mutex
	// runningTestRunID is the test run started here and not yet finished;
	// guarded by executeStartMu.
	runningTestRunID       string
	sessionCtx             context.Context                             // long-lived ctx for background goroutines
	toolCallQueryFunc      ToolCallQueryFunc                           // optional: query live tool calls for running steps
	tmuxLookupFunc         TmuxLookupFunc                              // optional: resolve live tmux session name for a coding-CLI step
	mainSessionID          string                                      // event store session ID for tool call queries
	schedulerWorkspacePath string                                      // workspace path for schedule management
	schedulerFuncs         *SchedulerCallbacks                         // schedule CRUD callbacks from server.go
	skillFuncs             *SkillCallbacks                             // skill import/delete callbacks from server.go
	llmToolsFuncs          *LLMToolsCallbacks                          // LLM management callbacks from server.go
	listAvailableSecrets   func(ctx context.Context) ([]string, error) // list all available secret names
	resolveSecretValues    func(ctx context.Context, names []string) map[string]string
	secretsAttached        func(set map[string]string, removed []string)
	executionNotifier      WorkshopExecutionNotifier // optional: notifies server when executions start/complete
	hasPendingCompletions  func() bool               // optional: true if completions are queued for delivery
	hasRunningAgents       func() bool               // optional: true if server still has running background agents
	cancelAllServerAgents  func()                    // optional: cancel all running agents in server's bgAgentRegistry
	listServerAgents       func() []ServerAgentInfo  // optional: list all agents from server's bgAgentRegistry
	workshopModeOverride   string                    // frontend-selected workshop mode (takes priority over auto-detection)
}

// isRunModeRestricted reports whether this session's current WorkshopMode is
// "run" — the single gate (PLAT-262) for mutating tools, the system prompt,
// and skills. A read-only-access identity is always pinned to "run" by the
// caller (cmd/server); anyone else genuinely in "run" mode (Bot Connector
// routes, scheduled runs, the agent-profile runtime) gets the same reduced
// tool set on purpose. See RCA #2 in docs/bugs/pulse_platform/sandbox/access/plat-262.md.
func (iwm *InteractiveWorkshopManager) isRunModeRestricted() bool {
	return canonicalWorkshopMode(iwm.workshopModeOverride) == "run"
}

func uniqueStringsPreserveOrder(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

// notificationRecipientSummary renders a configured recipient list for the
// agent-facing config read-out. An empty list is described by what it actually
// does at send time rather than as a blank, so the agent does not read a
// missing override as "this workflow emails nobody".
func notificationRecipientSummary(recipients []string) string {
	if len(recipients) == 0 {
		return "not configured (uses the account default recipient)"
	}
	return strings.Join(recipients, ", ")
}

// notificationWebhookSummary renders the Slack channels configured for one
// summary. Each entry is a webhook secret, and a webhook is one channel.
func notificationWebhookSummary(secretNames []string) string {
	if len(secretNames) == 0 {
		return "not configured (uses the workflow's single Slack webhook, if any)"
	}
	return strings.Join(secretNames, ", ")
}

// persistWorkflowConfigToManifest writes the current in-memory workflow config
// (servers, tools, skills, secrets) back to workflow.json so changes survive
// session end.
func (iwm *InteractiveWorkshopManager) persistWorkflowConfigToManifest(ctx context.Context, logger loggerv2.Logger) error {
	wsPath := iwm.controller.GetWorkspacePath()
	if wsPath == "" {
		return fmt.Errorf("workflow workspace is missing")
	}
	manifestPath := "workflow.json"

	// Read existing manifest
	content, err := iwm.controller.ReadWorkspaceFile(ctx, manifestPath)
	if err != nil {
		return fmt.Errorf("read workflow.json for config persistence: %w", err)
	}

	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(content), &manifest); err != nil {
		return fmt.Errorf("parse workflow.json: %w", err)
	}

	// Get or create capabilities object
	caps, ok := manifest["capabilities"].(map[string]interface{})
	if !ok {
		caps = make(map[string]interface{})
	}

	// Update from current controller state
	caps["selected_servers"] = iwm.controller.GetSelectedServers()
	caps["selected_tools"] = iwm.controller.GetSelectedTools()
	caps["selected_skills"] = iwm.controller.GetSelectedSkills()

	// Update secrets (names only, never values). Write to BOTH manifest fields:
	//   - selected_secrets           → used by loadSelectedSecrets to decrypt workflow/user-stored values
	//   - selected_global_secret_names → used by mergeGlobalSecrets to filter GLOBAL_SECRET_* env vars
	// Each loader path ignores names it can't resolve in its own bucket, so writing the
	// union to both fields lets user and global names each resolve via their own path.
	// Writing to only one field is the bug the builder chat hit: user secrets attached via
	// update_workflow_config never reached step runtime because selected_secrets stayed empty.
	secretNames := make([]string, 0)
	for _, s := range iwm.controller.GetSecrets() {
		if s.Name != "" {
			secretNames = append(secretNames, s.Name)
		}
	}
	caps["selected_secrets"] = secretNames
	caps["selected_global_secret_names"] = secretNames

	caps["use_code_execution_mode"] = iwm.controller.GetUseCodeExecutionMode()
	if mode := strings.ToLower(strings.TrimSpace(iwm.controller.GetBrowserMode())); mode != "" {
		caps["browser_mode"] = mode
	} else if _, exists := caps["browser_mode"]; !exists {
		caps["browser_mode"] = "none"
	}
	if ports := iwm.controller.GetCdpPorts(); len(ports) > 0 {
		caps["cdp_ports"] = ports
	} else {
		delete(caps, "cdp_ports")
	}

	// Persist the remaining workflow-level KB configuration. Remove the retired
	// workflow-wide lock whenever an older manifest is rewritten; KB write
	// ownership is now entirely step-based.
	llmCfg, _ := caps["llm_config"].(map[string]interface{})
	if llmCfg == nil {
		llmCfg = make(map[string]interface{})
	}
	delete(llmCfg, "lock_knowledgebase")
	if shape := iwm.controller.KBShape(); shape != "" {
		llmCfg["kb_shape"] = shape
	} else {
		delete(llmCfg, "kb_shape")
	}
	caps["llm_config"] = llmCfg

	manifest["capabilities"] = caps
	manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)

	// Write back
	updated, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workflow.json: %w", err)
	}

	// Go-owned structured write of the workflow's own manifest (PLAT-304):
	// background and Pulse sessions have no raw grant to workflow.json, and
	// the typed tool that led here already wrote it the managed way.
	if err := iwm.controller.writeManagedWorkflowManifest(ctx, string(updated)); err != nil {
		return fmt.Errorf("write workflow.json: %w", err)
	}

	logger.Info("✅ Persisted workflow config changes to workflow.json")
	return nil
}

// refreshVariablesManifest reloads the variables manifest from file into the controller.
// Call this before using iwm.controller.variablesManifest to avoid stale data.
func (iwm *InteractiveWorkshopManager) refreshVariablesManifest(ctx context.Context) {
	manifest, err := readVariablesFromFile(ctx, iwm.controller.GetWorkspacePath(), func(ctx context.Context, path string) (string, error) {
		return iwm.controller.ReadWorkspaceFile(ctx, path)
	})
	if err == nil && manifest != nil {
		iwm.controller.variablesManifest = manifest
		if iwm.controller.isRelayExecution() {
			values, resolveErr := iwm.controller.relayVariableValues()
			if resolveErr != nil {
				iwm.controller.GetLogger().Warn(fmt.Sprintf("Relay configuration needs migration: %v", resolveErr))
				values = nil
			}
			iwm.controller.variableValues = values
			iwm.controller.enabledGroupNames = nil
			SyncExactVariablesToWorkspaceEnv(iwm.controller.BaseOrchestrator, values)
		} else if values, groupName, ok := ResolveWorkshopVariableValues(manifest, iwm.controller.enabledGroupNames); ok {
			iwm.controller.variableValues = values
			iwm.controller.enabledGroupNames = []string{groupName}
			SyncExactVariablesToWorkspaceEnv(iwm.controller.BaseOrchestrator, values)
		} else {
			// Do not retain values from a deleted group or from a previously
			// unambiguous manifest after a second group makes selection necessary.
			iwm.controller.variableValues = nil
			SyncExactVariablesToWorkspaceEnv(iwm.controller.BaseOrchestrator, nil)
		}
	}
}

// workshopSubAgentNotifier implements SubAgentNotifier by registering todo task
// sub-agents into the workshop stepRegistry so query_step can find them.
type workshopSubAgentNotifier struct {
	registry *WorkshopStepRegistry
}

func (n *workshopSubAgentNotifier) OnSubAgentStart(start WorkshopExecutionStart) {
	exec := &WorkshopStepExecution{
		ID:     start.ID,
		StepID: start.Name,
		Status: WorkshopStepRunning,
		cancel: start.Cancel,
	}
	n.registry.Register(exec)
}

func (n *workshopSubAgentNotifier) OnSubAgentComplete(agentID, name, result string, err error) {
	exec := n.registry.Get(agentID)
	if exec == nil {
		return
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	if err != nil {
		exec.Status = WorkshopStepFailed
		exec.Err = err
	} else {
		exec.Status = WorkshopStepDone
		exec.Result = result
	}
}

// NewInteractiveWorkshopManager creates a new InteractiveWorkshopManager
func NewInteractiveWorkshopManager(
	controller *StepBasedWorkflowOrchestrator,
	presetLLM *AgentLLMConfig,
	sessionID string,
	workflowID string,
) *InteractiveWorkshopManager {
	registry := newWorkshopStepRegistry()
	// Wire sub-agent notifier so todo task sub-agents appear in stepRegistry
	// and are queryable via query_step
	controller.SetSubAgentNotifier(&workshopSubAgentNotifier{registry: registry})
	return &InteractiveWorkshopManager{
		controller:   controller,
		presetLLM:    presetLLM,
		sessionID:    sessionID,
		workflowID:   workflowID,
		stepRegistry: registry,
	}
}

// SetToolCallQuery configures the live tool call query capability.
// mainSessionID is the event store session ID; queryFunc queries tool calls by correlation ID.
func (iwm *InteractiveWorkshopManager) SetToolCallQuery(mainSessionID string, queryFunc ToolCallQueryFunc) {
	iwm.mainSessionID = mainSessionID
	iwm.toolCallQueryFunc = queryFunc
}

// GetToolsForWorkshopMode returns the legacy mode-specific tool-name projection.
// It is retained for compatibility checks and is not an authorization boundary;
// production authority must be enforced by agent construction and executors.
//
// Tools are grouped into categories:
//   - System tools: always included (shell, workspace, human interaction/notification, virtual tools)
//   - Workshop execution tools: execute_step, query_step, send_step_message, stop, list, run_in_background
//   - Step config/tools: update_step_config, review_step_code
//   - Plan modification tools: add/update/delete steps and routes
//   - Variable/config tools: update_variable, groups, workflow config
//   - Schedule tools: list/create/update/delete schedules
//   - Skill tools: list/search/install/uninstall skills
func GetToolsForWorkshopMode(mode string) []string {
	// System tools — always available regardless of mode.
	// Includes workspace, shell, virtual tools, and human interaction/notification.
	system := []string{
		"record_goal_observations",
		// Workspace advanced tools. Basic workspace file tools are intentionally
		// not in the central workspace registry; use the active shell/diff/text/search tools.
		"execute_shell_command", "diff_patch_workspace_file",
		"generate_text_llm",
		"query_workflow_db", "mutate_workflow_db", "apply_workflow_db_migration", "create_workflow_database_snapshot",
		// PLAT-184. This workflow's own per-workspace cost ledger.
		"query_workflow_costs",
		// Secret management tools. Managed globals require server admin; the
		// workflow encrypted store is writable when the corresponding tools
		// are registered.
		"list_secrets", "set_workflow_secret", "delete_workflow_secret", "manage_user_access", "manage_workflow_webhook",
		// Product chat tools, including human tools, are appended below from
		// AgentWorks product.yaml so mode admission has one source of truth.
		// Browser (if registered)
		"agent_browser",
		// mcpagent virtual tools (get_api_spec, get_prompt, get_resource)
		"get_api_spec", "get_prompt", "get_resource",
		// Read-only LLM capability discovery. The prompts instruct agents to check
		// capabilities before pinning a provider/model, and llm-selection.md leans on
		// it, but the allow-list never granted it — so a real, registered tool was
		// rejected as "not available in the current workshop mode". Registration and
		// this list live in different files, which is how they drifted.
		"list_llm_capabilities",
		// Sub-agent execution tools — used by execution agents running inside steps.
		// These must always be allowed because SetToolAllowList also gates the code
		// execution registry (HTTP calls), which blocks execution agents from calling
		// sub-agents even though the restriction is intended only for the phase agent LLM.
		"call_sub_agent", "call_scripted_sub_agent", "get_sub_agent_conversation", "get_route_description",
	}
	// Read-only info tools — safe in all modes
	readOnly := []string{
		"get_goal_metrics",
		"get_step_prompts", "get_workflow_config", "request_workflow_folder_access", "get_llm_config", "get_cost_summary",
	}

	// Workshop execution tools
	execution := []string{
		"execute_step", "query_step", "send_step_message", "stop_step", "stop_all_executions",
		"list_executions",
	}

	// Step config & analysis tools
	stepConfig := []string{
		"update_step_config", "review_step_code",
	}

	// LLM config tools — inspect published/available models and save tiered LLM
	// configuration directly to workflow.json capabilities.llm_config.
	llmConfig := []string{
		"list_published_llms", "list_provider_models", "test_llm", "set_workflow_llm_config",
	}

	// Plan modification tools
	planMod := []string{
		"create_plan",
		"validate_plan_change",
		"migrate_message_sequence_code_items", "migrate_orchestrator_step_type", "migrate_declared_execution_mode", "strip_declared_execution_mode",
		"add_scripted_step", "add_message_sequence_step", "add_routing_step", "add_branch_step",
		"add_human_input_step", "add_todo_task_step", "add_todo_task_route",
		"add_orchestrator_step", "add_orchestrator_route", "update_orchestrator_step", "update_orchestrator_route", "delete_orchestrator_route",
		"add_crew_step", "update_crew_step",
		"update_scripted_step", "update_message_sequence_step", "update_routing_step", "update_branch_step",
		"update_human_input_step", "update_todo_task_step", "update_todo_task_route",
		"delete_todo_task_route", "delete_plan_steps", "cleanup_orphan_step_configs",
		"update_validation_schema",
		"change_step_type",
		"record_plan_drift_review",
		"check_plan_no_loss", "restore_step_from_changelog",
	}

	// Variable & config tools
	variableConfig := []string{
		"configure_goal_metrics",
		"update_variable", "add_group", "update_group", "delete_group",
		"update_workflow_config", "set_workflow_contract_version",
	}

	// Schedule tools
	schedule := []string{
		"list_schedules", "create_schedule", "create_calendar_schedule", "update_schedule",
		"delete_schedule", "trigger_schedule", "get_schedule_runs", "get_contract_upgrades",
	}

	// Skill tools
	skills := []string{
		"list_skills", "search_skills", "install_skill", "uninstall_skill", "import_skill",
	}

	// Report tools — validate the workflow-owned db/reports/index.html UI.
	// Available in Workshop mode. Run mode may read report data for answers,
	// but does not author report pages.
	report := []string{
		"validate_report_html",
	}

	// Guided workflow command tools.
	autoImprovement := []string{
		"get_workflow_command_guidance", // canonical slash-command prose; see guidance package.
	}

	// Pulse state tools are only for scheduled/manual Pulse maintenance in
	// workshop mode. Run mode can inspect outcomes, but should not mutate the
	// dynamic Pulse worklist/result state.
	pulseState := []string{
		"get_pulse_state",
		"record_pulse_finding",
		"merge_pulse_issues",
		"record_pulse_worklist",
		"record_pulse_result",
		"resolve_run_concern",
		"mark_changelog_artifact_reviewed",
		"record_pulse_next_run",
		"record_pulse_fast_request",
		"record_pulse_goal_work",
		"record_pulse_goal_check",
		"record_pulse_recommendation",
		"record_pulse_decision_outcome",
		"record_pulse_goal_memory",
		// Phase 4: the Pulse's focus areas and its QA sub-agent requests.
		"record_pulse_focus_area",
		"record_pulse_qa_request",
		"search_platform",
		"ask_platform_crew",
		"read_crew_calls",
	}

	var tools []string
	tools = append(tools, system...)
	tools = append(tools, readOnly...)

	// Normalize every legacy alias before selecting a tool surface. Production
	// callers normally do this earlier, but direct callers must not fall through
	// to an unintended tool set.
	if canonical := canonicalWorkshopMode(mode); canonical != "" {
		mode = canonical
	}

	switch mode {
	case "workshop":
		tools = append(tools, "get_plan_prompt_health")
		// WORKSHOP: full toolkit for designing,
		// running, reviewing, and evolving a workflow. The agent
		// derives the current "phase" from workspace state (does a plan
		// exist? are there successful runs?) and uses the appropriate tools.
		// Tools that only make sense post-runs (Bug Review
		// and plan-edit tools are present here; their
		// downstream agents check evidence and refuse
		// when state isn't ready.
		tools = append(tools, execution...)
		tools = append(tools, stepConfig...)
		tools = append(tools, planMod...)
		tools = append(tools, variableConfig...)
		tools = append(tools, schedule...)
		tools = append(tools, skills...)
		tools = append(tools, llmConfig...)
		tools = append(tools, "debug_step")
		tools = append(tools, "run_full_workflow")
		tools = append(tools, "review_workflow_timing")
		tools = append(tools, "review_workflow_costs")
		tools = append(tools, report...)
		tools = append(tools, autoImprovement...)
		tools = append(tools, pulseState...)
		tools = append(tools, "ask_pulse", "ask_goal_lead")
		// The Pulse asks the workflow's Builder chat; the tool refuses any
		// other session (PLAT-697).
		tools = append(tools, "ask_builder")

	case "run":
		// RUN: deployed/user-facing runtime for workflow-backed work, Slack, WhatsApp,
		// and direct operational requests. It can answer directly from workflow state,
		// run individual steps including orphan utility steps, or run the full workflow.
		// No plan changes, no optimization, and no config changes — those belong to
		// Workshop.
		// The only framework mutation allowed here is capture_context after user
		// confirmation, so context learned during a run is not lost.
		// Read-only review tools stay available for outcome inspection.
		tools = append(tools, execution...)
		tools = append(tools, "run_full_workflow")
		tools = append(tools, "debug_step")
		tools = append(tools, "review_workflow_timing")
		tools = append(tools, "review_workflow_costs")
		tools = append(tools, "get_workflow_command_guidance") // /review-* commands need this even in run mode
		// Chats and steps working on the goal may ask the workflow's Pulse
		// for a recommendation (PLAT-697 phase 4).
		tools = append(tools, "ask_pulse", "ask_goal_lead")

	default:
		// Unknown mode — allow everything (no restriction)
		tools = append(tools, execution...)
		tools = append(tools, stepConfig...)
		tools = append(tools, planMod...)
		tools = append(tools, variableConfig...)
		tools = append(tools, schedule...)
		tools = append(tools, skills...)
		tools = append(tools, llmConfig...)
		tools = append(tools, report...)
		tools = append(tools, "debug_step")
	}

	productMode := "builder"
	if mode == "run" {
		productMode = "run"
	}
	humanImplementations := map[string]bool{}
	for _, name := range virtualtools.HumanToolImplementationNames() {
		humanImplementations[name] = true
	}
	for _, name := range agentworksproduct.ChatTools(productMode) {
		if humanImplementations[name] {
			tools = append(tools, name)
		}
	}

	projected := []string{}
	for _, name := range tools {
		if name != "review_plan" {
			projected = append(projected, ConsolidatedPlanToolName(name))
		}
	}
	return uniqueStringsPreserveOrder(projected)
}

func (iwm *InteractiveWorkshopManager) registerMarkChangelogArtifactReviewedTool(mcpAgent DefinitionToolRegistrar, workspacePath string, logger loggerv2.Logger) error {
	if mcpAgent == nil {
		return fmt.Errorf("nil mcp agent")
	}
	return mcpAgent.RegisterCustomTool(
		"mark_changelog_artifact_reviewed",
		"Mark planning/changelog entries as fully inspected after every dependency surface has an evidence-backed disposition. Generic read-only reviewers must only propose exact marks and must not call this tool. This is the only supported way to set artifact_review.done=true; do not edit changelog JSON directly.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"marks": map[string]interface{}{
					"type":        "array",
					"description": "Changelog files and zero-based entry indexes to mark reviewed.",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"file": map[string]interface{}{
								"type":        "string",
								"description": "The changelog filename, e.g. changelog-2026-07-02-06-12-46.json. A planning/changelog/... path is accepted; only the basename is used.",
							},
							"entry_indexes": map[string]interface{}{
								"type":        "array",
								"description": "Zero-based indexes in the file's entries array that were fully inspected or cursor-backfilled.",
								"items":       map[string]interface{}{"type": "integer"},
							},
							"surface_reviews": planDependencySurfaceReviewSchema(),
						},
						"required": []string{"file", "entry_indexes"},
					},
				},
				"result": map[string]interface{}{
					"type":        "string",
					"enum":        []interface{}{"clean", "findings", "cursor-backfill"},
					"description": "Review result to stamp on all marked entries.",
				},
				"report_entry_id": map[string]interface{}{
					"type":        "string",
					"description": "Optional reference ID of the structured Pulse review record that records this review.",
				},
				"reviewed_at": map[string]interface{}{
					"type":        "string",
					"description": "Optional RFC3339 UTC timestamp. Defaults to now.",
				},
			},
			"required": []string{"marks", "result"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			return iwm.markChangelogArtifactReviewed(ctx, workspacePath, args, logger)
		},
		"workflow",
	)
}

// registerWorkshopAgentTools is the single registration path for tools that are
// available only to a workshop/Pulse agent. Keep both the exported CLI wrapper
// and the legacy in-process workshop executor on this path so their actual tool
// registries cannot drift from one another.
func registerWorkshopAgentTools(iwm *InteractiveWorkshopManager, mcpAgent DefinitionRegistrar, workspacePath string, logger loggerv2.Logger) {
	registerInteractiveWorkshopTools(iwm, mcpAgent, logger)
	if !iwm.isRunModeRestricted() && iwm.workshopConfig != nil && iwm.workshopConfig.KnowledgebaseProjectTool != nil {
		for _, def := range knowledgebase.ProjectToolDefinitions() {
			def := def
			params := def.InputSchema
			if err := mcpAgent.RegisterCustomTool(def.Name, def.Description, params, func(ctx context.Context, args map[string]interface{}) (string, error) {
				return iwm.workshopConfig.KnowledgebaseProjectTool(ctx, def.Name, args)
			}, "knowledgebase"); err != nil {
				logger.Warn(fmt.Sprintf("Failed to register knowledge project tool: %v", err))
			}
		}
	}
	if iwm.isRunModeRestricted() {
		// PLAT-262: skip changelog artifact-review marker registration for read-only access
	} else if err := iwm.registerMarkChangelogArtifactReviewedTool(mcpAgent, workspacePath, logger); err != nil {
		logger.Warn(fmt.Sprintf("Failed to register changelog artifact-review marker tool: %v", err))
	}
}

func (iwm *InteractiveWorkshopManager) markChangelogArtifactReviewed(ctx context.Context, workspacePath string, args map[string]interface{}, logger loggerv2.Logger) (string, error) {
	rawMarks, ok := args["marks"].([]interface{})
	if !ok || len(rawMarks) == 0 {
		return "marks is required", nil
	}
	result := strings.TrimSpace(asString(args["result"]))
	switch result {
	case "clean", "findings", "cursor-backfill":
	default:
		return `result must be one of "clean", "findings", or "cursor-backfill"`, nil
	}
	reviewedAt := strings.TrimSpace(asString(args["reviewed_at"]))
	if reviewedAt == "" {
		reviewedAt = time.Now().UTC().Format(time.RFC3339)
	}
	reportEntryID := strings.TrimSpace(asString(args["report_entry_id"]))

	marksByFile := map[string]map[int]bool{}
	reviewsByFile := map[string]map[int]map[string]PlanDependencySurfaceReview{}
	for _, raw := range rawMarks {
		obj, ok := raw.(map[string]interface{})
		if !ok {
			return "each marks item must be an object", nil
		}
		file := filepath.Base(strings.TrimSpace(asString(obj["file"])))
		if file == "." || file == "" || !strings.HasPrefix(file, "changelog-") || !strings.HasSuffix(strings.ToLower(file), ".json") {
			return fmt.Sprintf("invalid changelog file %q", asString(obj["file"])), nil
		}
		rawIndexes, ok := obj["entry_indexes"].([]interface{})
		if !ok || len(rawIndexes) == 0 {
			return fmt.Sprintf("entry_indexes is required for %s", file), nil
		}
		if marksByFile[file] == nil {
			marksByFile[file] = map[int]bool{}
		}
		var surfaceReviews map[string]PlanDependencySurfaceReview
		if result == "cursor-backfill" && obj["surface_reviews"] == nil {
			surfaceReviews = cursorBackfillSurfaceReviews()
		} else {
			var reviewErr error
			surfaceReviews, reviewErr = parsePlanDependencySurfaceReviews(obj["surface_reviews"])
			if reviewErr != nil {
				return fmt.Sprintf("invalid dependency review for %s: %v", file, reviewErr), nil
			}
		}
		if reviewsByFile[file] == nil {
			reviewsByFile[file] = map[int]map[string]PlanDependencySurfaceReview{}
		}
		for _, rawIndex := range rawIndexes {
			var idx int
			switch v := rawIndex.(type) {
			case float64:
				idx = int(v)
				if float64(idx) != v {
					return fmt.Sprintf("entry index %v for %s is not an integer", rawIndex, file), nil
				}
			case int:
				idx = v
			default:
				return fmt.Sprintf("entry index %v for %s is not an integer", rawIndex, file), nil
			}
			if idx < 0 {
				return fmt.Sprintf("entry index %d for %s is negative", idx, file), nil
			}
			marksByFile[file][idx] = true
			reviewsByFile[file][idx] = surfaceReviews
		}
	}

	totalMarked := 0
	touchedFiles := make([]string, 0, len(marksByFile))
	for file, indexSet := range marksByFile {
		changelogPath := strings.Trim(strings.TrimSpace(workspacePath), "/") + "/planning/changelog/" + file
		if strings.HasPrefix(changelogPath, "/") {
			changelogPath = strings.TrimPrefix(changelogPath, "/")
		}
		content, err := iwm.controller.ReadWorkspaceFile(ctx, changelogPath)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", changelogPath, err)
		}
		var changelog PlanChangelog
		if err := json.Unmarshal([]byte(content), &changelog); err != nil {
			return "", fmt.Errorf("parse %s: %w", changelogPath, err)
		}
		for idx := range indexSet {
			if idx >= len(changelog.Entries) {
				return fmt.Sprintf("entry index %d out of range for %s (entries=%d)", idx, file, len(changelog.Entries)), nil
			}
		}
		for idx := range indexSet {
			changelog.Entries[idx].ArtifactReview = &PlanChangelogArtifactReview{
				Done:          true,
				ReviewedAt:    reviewedAt,
				ReviewedBy:    "workflow_builder",
				Result:        result,
				ReportEntryID: reportEntryID,
				Surfaces:      reviewsByFile[file][idx],
			}
			totalMarked++
		}
		data, err := json.MarshalIndent(changelog, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal %s: %w", changelogPath, err)
		}
		if err := iwm.controller.writeManagedPlanningFile(ctx, filepath.Join("changelog", file), string(data)); err != nil {
			return "", fmt.Errorf("write %s: %w", changelogPath, err)
		}
		touchedFiles = append(touchedFiles, file)
		logger.Info(fmt.Sprintf("📝 Artifact Review: marked %d changelog entries reviewed in %s", len(indexSet), file))
	}
	sort.Strings(touchedFiles)
	entryWord := "entry"
	if totalMarked != 1 {
		entryWord = "entries"
	}
	return fmt.Sprintf("Marked %d changelog %s artifact_review.done=true across %d file(s): %s", totalMarked, entryWord, len(touchedFiles), strings.Join(touchedFiles, ", ")), nil
}

// canonicalWorkshopMode keeps this package aligned with server-side chat-history
// normalization. Legacy editable-mode names all mean the unified Workshop mode.
func canonicalWorkshopMode(mode string) string {
	trimmed := strings.ToLower(strings.TrimSpace(mode))
	if trimmed == "" {
		return ""
	}
	switch trimmed {
	case "run", "ask", "debugger", "runner":
		return "run"
	default:
		return "workshop"
	}
}

// detectWorkshopMode returns the editable Workshop default when the frontend
// has not provided an explicit Run override.
func detectWorkshopMode(plan *PlanningResponse, stepConfigs []StepConfig) string {
	return "workshop"
}

func (iwm *InteractiveWorkshopManager) currentWorkshopModeFromConfigs(stepConfigs []StepConfig) string {
	if iwm == nil {
		return "workshop"
	}
	if mode := canonicalWorkshopMode(iwm.workshopModeOverride); mode != "" {
		return mode
	}
	mode := detectWorkshopMode(nil, stepConfigs)
	return mode
}

// newExecContext creates a child context for a step execution goroutine.
// Returns (nil, errSessionStopped) if the session was already canceled (user pressed stop),
// preventing orphaned CLI processes from being spawned. All step execution tool handlers
// MUST use this instead of calling context.WithCancel(iwm.sessionCtx) directly.
//
// Background: When the user stops a session, WorkshopChatSession.Close() cancels
// iwm.sessionCtx. But step execution goroutines may already be queued or in-flight.
// Without this check, they would create a derived context from a canceled parent,
// spawn a CLI process, and that process would either fail immediately or, in edge
// cases, run indefinitely if the cancel signal is not delivered quickly.
func (iwm *InteractiveWorkshopManager) newExecContext(launchCtx context.Context) (context.Context, context.CancelFunc, error) {
	if iwm.sessionCtx.Err() != nil {
		return nil, nil, fmt.Errorf("session was stopped")
	}
	ctx, cancel := context.WithCancel(iwm.sessionCtx)
	ctx = withWorkshopExecutionParent(ctx, launchCtx)
	return ctx, cancel, nil
}

// workshopWritePaths returns the explicit per-subfolder write allow-list shared
// by the workshop main agent and every workshop sub-agent (review agents,
// maintenance background tasks). Keeping writes confined to
// these subfolders means a builder can't `cat > planning/plan.json` via shell
// or a shell write and bypass the plan-mod tools that validate
// schemas and emit events. Workspace-root config files (workflow.json,
// mcp_config.json) are mutated through dedicated tools that go via the
// workspace API and bypass this sandbox altogether, so they don't appear here.
func workshopWritePaths(workspacePath string) []string {
	return []string{
		fmt.Sprintf("%s/code", workspacePath),
		fmt.Sprintf("%s/learnings", workspacePath),
		fmt.Sprintf("%s/code", workspacePath),
		fmt.Sprintf("%s/knowledgebase", workspacePath),
		fmt.Sprintf("%s/runs", workspacePath),
		fmt.Sprintf("%s/evaluation", workspacePath),
		fmt.Sprintf("%s/reports", workspacePath),
		fmt.Sprintf("%s/db", workspacePath),
		fmt.Sprintf("%s/memory", workspacePath),
		fmt.Sprintf("%s/execution", workspacePath),
		fmt.Sprintf("%s/variables", workspacePath),
		fmt.Sprintf("%s/builder", workspacePath), // workshop conversation and changelog artifacts
	}
}

func (iwm *InteractiveWorkshopManager) setupWorkshopToolAgentSession(agentKind string, readPaths []string, writePaths []string) string {
	sessionID := newWorkshopStageAgentIdentity(agentKind)
	workspacePath := strings.TrimSpace(iwm.controller.GetWorkspacePath())

	common.SetSessionFolderGuard(sessionID, readPaths, writePaths)
	configureWorkshopToolAgentBridgeSession(sessionID)
	dbAccess := DBAccessRead
	if dbWritePathGranted(writePaths, workspacePath) {
		dbAccess = DBAccessReadWrite
	}
	configureWorkflowDBSession(sessionID, workspacePath, dbAccess, false)
	iwm.controller.grantSessionCDPHostDownloadsReadWrite(sessionID)
	if workspacePath != "" {
		common.SetSessionWorkingDir(sessionID, workspacePath)
	}

	iwm.controller.GetLogger().Info(fmt.Sprintf(
		"🔒 Workshop tool-agent session %q (%s) — cwd=%q Read=%v Write=%v",
		sessionID, agentKind, workspacePath, readPaths, writePaths,
	))
	return sessionID
}

// configureWorkshopToolAgentBridgeSession makes shell-originated bridge calls
// use the same child MCP session as direct tool calls. Without this override,
// execute_shell_command inherits the long-lived workshop client's MCP URL. A
// nested curl to mutate_workflow_db is then attributed to the parent session,
// even though the child owns the folder guard and DB capability.
func configureWorkshopToolAgentBridgeSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	env := map[string]string{"MCP_SESSION_ID": sessionID}
	if scopedURL := buildSessionScopedMCPAPIURL(sessionID); scopedURL != "" {
		env["MCP_API_URL"] = scopedURL
	}
	common.PopulateMCPBridgeShortEnv(env)
	common.SetSessionShellEnv(sessionID, env)
}

func (iwm *InteractiveWorkshopManager) configureWorkshopToolAgentSession(config *agents.OrchestratorAgentConfig, agentKind string, readPaths []string, writePaths []string) func() {
	_, cleanup := iwm.configureWorkshopToolAgentSessionWithID(config, agentKind, readPaths, writePaths)
	return cleanup
}

// configureWorkshopToolAgentSessionWithID also returns the MCP session id the
// child's tool calls will actually carry. Callers that must key something on
// that identity — Pulse write authority is keyed by session id — need the real
// value, which is derived here and is not the agentKind passed in.
func (iwm *InteractiveWorkshopManager) configureWorkshopToolAgentSessionWithID(config *agents.OrchestratorAgentConfig, agentKind string, readPaths []string, writePaths []string) (string, func()) {
	readPaths, writePaths, readOnlyPaths, folderEnv := appendWorkflowFolderAccess(iwm.controller.GetWorkspacePath(), readPaths, writePaths)
	toolAgentSessionID := iwm.setupWorkshopToolAgentSession(agentKind, readPaths, writePaths)
	configureWorkflowFolderAccessSession(toolAgentSessionID, iwm.controller.GetWorkspacePath(), readOnlyPaths, folderEnv)
	config.MCPSessionID = toolAgentSessionID
	config.FolderGuardReadPaths = readPaths
	config.FolderGuardWritePaths = writePaths
	// Trusted owner of this tool session for server-side tools that act for
	// a workflow (workshop_tool_sessions.go).
	unregister := RegisterWorkshopToolSession(toolAgentSessionID, iwm.controller.GetWorkspacePath(), iwm.mainSessionID)
	return toolAgentSessionID, func() {
		unregister()
		common.ClearSessionShellConfig(toolAgentSessionID)
	}
}

func absPromptWorkspacePath(workspacePath string) string {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return ""
	}
	return filepath.Join(GetPromptDocsRoot(), workspacePath)
}

// Prompt sources and shared includes are selected by AgentWorks product.yaml.
var interactiveWorkshopSystemTemplate = MustRegisterTemplate("interactiveWorkshopSystem", agentworksproduct.ChatPromptTemplate("builder"))
var interactiveRunSystemTemplate = MustRegisterTemplate("interactiveRunSystem", agentworksproduct.ChatPromptTemplate("run"))

// ============================================================================
// Custom Workshop Tools
// ============================================================================

// resolveWorkshopStepID resolves a user-provided step reference to an actual step ID from the plan.
// Accepts exact IDs, 1-based positions ("1", "step-1", "step1"), and falls back with suggestions.
// The caller supplies the snapshot appropriate to the query or execution.
func resolveWorkshopStepID(plan *PlanningResponse, inputID string) (string, error) {
	if plan == nil || len(plan.Steps) == 0 {
		return "", fmt.Errorf("no plan loaded")
	}

	// 1. Exact match (top-level + inner steps)
	if info := findWorkshopStepByID(plan.Steps, inputID); info != nil {
		return inputID, nil
	}

	// 2. Positional match: extract number from "1", "step-1", "step1", "step 1"
	numStr := inputID
	for _, prefix := range []string{"step-", "step ", "step"} {
		if strings.HasPrefix(strings.ToLower(inputID), prefix) {
			numStr = inputID[len(prefix):]
			break
		}
	}
	var pos int
	if _, err := fmt.Sscanf(strings.TrimSpace(numStr), "%d", &pos); err == nil && pos >= 1 && pos <= len(plan.Steps) {
		return plan.Steps[pos-1].GetID(), nil
	}

	// 3. Not found — return valid IDs for helpful error message
	allSteps := collectAllSteps(plan.Steps)
	ids := make([]string, 0, len(allSteps))
	for _, info := range allSteps {
		label := fmt.Sprintf("%q (step %d)", info.Step.GetID(), info.TopIndex)
		if info.TopIndex <= 0 {
			label = fmt.Sprintf("%q (inner, parent=%s, location=%s)", info.Step.GetID(), info.ParentID, info.NestedLocation)
		}
		ids = append(ids, label)
	}
	return "", fmt.Errorf("step %q not found in plan. Valid IDs: %s", inputID, strings.Join(ids, ", "))
}

func resolveWorkshopStepConfigTarget(ctx context.Context, controller interface {
	ReadCurrentPlan(context.Context) (*PlanningResponse, error)
}, inputID string) (resolvedID string, err error) {
	plan, loadErr := controller.ReadCurrentPlan(ctx)
	if loadErr != nil {
		return "", fmt.Errorf("step %q: cannot read planning/plan.json: %w", inputID, loadErr)
	}
	if id, resolveErr := resolveWorkshopStepID(plan, inputID); resolveErr == nil {
		return id, nil
	}
	return "", fmt.Errorf("step %q not found in planning/plan.json", inputID)
}

// registerInteractiveWorkshopTools registers the custom workshop tools on the agent.
func registerGetCostSummaryTool(iwm *InteractiveWorkshopManager, mcpAgent DefinitionToolRegistrar, logger loggerv2.Logger) {
	if err := mcpAgent.RegisterCustomTool(
		"get_cost_summary",
		"Show token usage and cost breakdown for one execution run. Builder/Pulse usage is shown separately as cumulative reference only and is never part of that run's total.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"run_folder": map[string]interface{}{
					"type":        "string",
					"description": "Optional run folder such as 'iteration-0/group-1'. If omitted, uses the currently selected run folder.",
				},
			},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			runFolder, _ := args["run_folder"].(string)
			runFolder = strings.TrimSpace(runFolder)
			if runFolder == "" {
				runFolder = strings.TrimSpace(iwm.controller.selectedRunFolder)
			}
			if runFolder == "" {
				return "no run folder selected; pass run_folder like 'iteration-0/group-1'", nil
			}

			tokenFile := iwm.controller.GetRunTokenUsageFile(ctx, runFolder)
			runMissingReason := ""
			if tokenFile == nil || len(tokenFile.ByModel) == 0 && len(tokenFile.ByStepAndModel) == 0 {
				if runMissingReason == "" {
					runMissingReason = fmt.Sprintf("missing evidence: no run token usage data found for %s", runFolder)
				}
			}

			tok := func(s string) string {
				if s == "" {
					return "0"
				}
				return s
			}

			var result strings.Builder
			result.WriteString(fmt.Sprintf("## Cost Summary — %s\n\n", runFolder))
			result.WriteString("Run totals below cover only this execution run. Builder/Pulse totals, when present, are cumulative workflow-wide reference and must not be added to this run.\n\n")

			if runMissingReason != "" {
				result.WriteString("### Run Cost\n\n")
				result.WriteString(runMissingReason)
				result.WriteString("\n\n")
			}

			if tokenFile != nil && len(tokenFile.ByStepAndModel) > 0 {
				result.WriteString("### Per-Step Breakdown\n\n")
				result.WriteString("| Step | Model | Input | Output | Cache | Cost |\n")
				result.WriteString("|------|-------|-------|--------|-------|------|\n")

				stepKeys := make([]string, 0, len(tokenFile.ByStepAndModel))
				for k := range tokenFile.ByStepAndModel {
					stepKeys = append(stepKeys, k)
				}
				sort.Strings(stepKeys)

				grandTotalCost := 0.0
				for _, stepKey := range stepKeys {
					models := tokenFile.ByStepAndModel[stepKey]
					modelKeys := make([]string, 0, len(models))
					for k := range models {
						modelKeys = append(modelKeys, k)
					}
					sort.Strings(modelKeys)
					for _, modelID := range modelKeys {
						usage := models[modelID]
						grandTotalCost += usage.TotalCost
						result.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | $%.4f |\n",
							stepKey, modelID,
							tok(usage.InputTokensM), tok(usage.OutputTokensM), tok(usage.CacheTokensM),
							usage.TotalCost,
						))
					}
				}
				result.WriteString(fmt.Sprintf("\n**Grand total: $%.4f**\n\n", grandTotalCost))
			}

			if tokenFile != nil && len(tokenFile.ByModel) > 0 {
				result.WriteString("### Per-Model Totals\n\n")
				result.WriteString("| Model | Input | Output | Cache R/W | Reasoning | Calls | Cost |\n")
				result.WriteString("|-------|-------|--------|-----------|-----------|-------|------|\n")

				modelKeys := make([]string, 0, len(tokenFile.ByModel))
				for k := range tokenFile.ByModel {
					modelKeys = append(modelKeys, k)
				}
				sort.Strings(modelKeys)

				totalCost := 0.0
				for _, modelID := range modelKeys {
					usage := tokenFile.ByModel[modelID]
					totalCost += usage.TotalCost
					cacheRW := fmt.Sprintf("%s / %s", tok(usage.CacheReadTokensM), tok(usage.CacheWriteTokensM))
					result.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %d | $%.4f |\n",
						modelID,
						tok(usage.InputTokensM), tok(usage.OutputTokensM), cacheRW,
						tok(usage.ReasoningTokensM), usage.LLMCallCount,
						usage.TotalCost,
					))
				}
				result.WriteString(fmt.Sprintf("\n**Total: $%.4f**\n", totalCost))
			}

			phaseTokenPath := orchestrator.ResolvePhaseTokenUsagePath("")
			phaseContent, phaseErr := iwm.controller.ReadWorkspaceFile(ctx, phaseTokenPath)
			if phaseErr != nil {
				result.WriteString("\n### Cumulative Builder/Pulse Reference (not part of this run)\n\n")
				result.WriteString(fmt.Sprintf("missing evidence: no phase token usage data found at %s\n", phaseTokenPath))
			} else {
				var phaseFile orchestrator.PhaseTokenUsageFile
				if err := json.Unmarshal([]byte(phaseContent), &phaseFile); err != nil {
					result.WriteString("\n### Cumulative Builder/Pulse Reference (not part of this run)\n\n")
					result.WriteString(fmt.Sprintf("missing evidence: failed to parse %s: %v\n", phaseTokenPath, err))
				} else if len(phaseFile.ByPhaseAndModel) == 0 {
					result.WriteString("\n### Cumulative Builder/Pulse Reference (not part of this run)\n\n")
					result.WriteString(fmt.Sprintf("missing evidence: no by_phase_and_model entries in %s\n", phaseTokenPath))
				} else {
					result.WriteString("\n### Cumulative Builder/Pulse Reference (not part of this run)\n\n")
					result.WriteString("| Phase | Model | Input | Output | Cost |\n")
					result.WriteString("|-------|-------|-------|--------|------|\n")

					phaseKeys := make([]string, 0, len(phaseFile.ByPhaseAndModel))
					for k := range phaseFile.ByPhaseAndModel {
						phaseKeys = append(phaseKeys, k)
					}
					sort.Strings(phaseKeys)

					phaseTotalCost := 0.0
					for _, phase := range phaseKeys {
						models := phaseFile.ByPhaseAndModel[phase]
						pModelKeys := make([]string, 0, len(models))
						for k := range models {
							pModelKeys = append(pModelKeys, k)
						}
						sort.Strings(pModelKeys)
						for _, modelID := range pModelKeys {
							usage := models[modelID]
							phaseTotalCost += usage.TotalCost
							result.WriteString(fmt.Sprintf("| %s | %s | %s | %s | $%.4f |\n",
								phase, modelID,
								tok(usage.InputTokensM), tok(usage.OutputTokensM),
								usage.TotalCost,
							))
						}
					}
					result.WriteString(fmt.Sprintf("\n**Cumulative builder/Pulse reference: $%.4f (not included in run total)**\n", phaseTotalCost))
				}
			}

			return result.String(), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register get_cost_summary tool: %v", err))
	}
}

func registerInteractiveWorkshopTools(iwm *InteractiveWorkshopManager, mcpAgent DefinitionRegistrar, logger loggerv2.Logger) {
	if iwm.controller.isRelayExecution() {
		mcpAgent = relayDefinitionRegistrar{DefinitionRegistrar: mcpAgent}
	}
	capture := newConsolidatedPlanRegistrar(mcpAgent, iwm.controller.GetWorkspacePath(), func(ctx context.Context, p string) (string, error) { return iwm.controller.ReadWorkspaceFile(ctx, p) })
	mcpAgent = consolidatedWorkshopRegistrar{mcpAgent, capture}
	defer func() {
		if err := capture.flush(); err != nil {
			logger.Warn(fmt.Sprintf("Failed to register consolidated workshop tools: %v", err))
		}
	}()

	// Tool 1: execute_step — start step in background
	if err := mcpAgent.RegisterCustomTool(
		"execute_step",
		"Start a workflow step in the background, including a normal plan step, nested route step, or plan-local orphan utility step. Returns an execution_id immediately. You will be automatically notified when it completes. Idempotent while running: if the step already has a running execution, this returns that existing execution_id instead of starting a duplicate — use send_step_message with the returned execution_id to steer its currently active agent turn. For a scripted step that declares script_parameters, pass matching values in script_parameters; they are validated before main.py starts and exposed through STEP_PARAMS_JSON. Learnings follow the step's persistent config (`learnings_access`, `learning_objective`). When learning writes are enabled, SKILL.md updates run as the step agent's direct post-completion continuation before the step is fully finalized. Workshop mode only: set fast_path_only=true to run ONLY the saved main.py (code/{step-id}/main.py for code_layout_version=1, otherwise learnings/{step-id}/main.py) script with no LLM fallback when testing scripted patches.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"step_id": map[string]interface{}{
					"type":        "string",
					"description": "The step ID from plan.json or orphan_steps[] (e.g., 'step-create-report', 'validate-environment') or positional reference (e.g., '1', 'step-1', 'step1')",
				},
				"group_name": map[string]interface{}{
					"type":        "string",
					"description": "Optional variable group ID. Omit it when this product has one default execution group; otherwise read variables/variables.json and provide a group name.",
				},
				"instructions": map[string]interface{}{
					"type":        "string",
					"description": "Optional orchestrator instructions for inner steps (sub-agents from todo_task/orchestration routes). Appended to the step description as '## Orchestrator Instructions'. Simulates what the parent orchestrator would provide when delegating. Ignored for top-level steps.",
				},
				"human_input": map[string]interface{}{
					"type":        "string",
					"description": "Optional human input/custom instructions for this run. For a standalone message_sequence step, this is opening context and the configured queue still runs from the beginning; standalone sessions are not resumed across execute_step calls. For human_input steps, it is used as the response. For other executable steps, it is injected as high-priority context.",
				},
				"message_sequence_restart": map[string]interface{}{
					"type":        "boolean",
					"description": "Message_sequence steps only. Request a clean run and clear route-local runtime artifacts before replaying the configured queue. Standalone execute_step calls already start a fresh queue and do not provide durable conversation resume. In-memory route re-entry exists only while a todo-task workflow run is still active.",
				},
				"tier": map[string]interface{}{
					"type":        "string",
					"enum":        []interface{}{"high", "medium", "low"},
					"description": "Optional LLM tier override for this execution. 'high' = Tier 1 (most capable), 'medium' = Tier 2, 'low' = Tier 3 (fastest/cheapest). Overrides the default maturity-based tier selection. Only works in tiered mode.",
				},
				"fast_path_only": map[string]interface{}{
					"type":        "boolean",
					"description": "Workshop mode only. If true, run ONLY the saved main.py (code/{step-id}/main.py for code_layout_version=1, otherwise learnings/{step-id}/main.py) script with no LLM fallback. Fails if no saved script exists, the step is not in scripted mode, or the current workshop mode is Run. Use this to quickly test scripted main.py patches.",
				},
				"script_parameters": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": true,
					"description":          "Named runtime inputs for a scripted step. Values must match that step's declared script_parameters contract and are passed to main.py as STEP_PARAMS_JSON. Omit for non-scripted steps.",
				},
				"source_run": map[string]interface{}{
					"type":        "string",
					"description": "Only with test_mode: the real run to read upstream step outputs from, as <iteration>/<group> (for example iteration-26-sched/daily-bid). Omit to use the most complete of the newest real runs of the group; the test receipt names the run used.",
				},
				"test_mode": map[string]interface{}{
					"type":        "boolean",
					"description": "Run the step once WITHOUT real-world side effects, to verify a change before it counts as done. Reads stay real (DB, files, read-only MCP tools, browser navigation and snapshots). Everything with an external effect is NOT performed but recorded as 'would have run': MCP tools not annotated read-only (send, post, submit, spend), browser clicks/typing/forms/eval, notifications and human-input requests, Crew calls, KB and goal-metric writes. The workflow DB is a copy and files go to runs/test-<id>/; learnings are not written. The shell's outbound network is NOT blocked, so a step that sends through curl in a script is not contained. The result lists every stubbed action. Cannot run while another step of this workshop runs.",
				},
			},
			"required": []string{"step_id"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			stepIDRaw, ok := args["step_id"]
			if !ok || stepIDRaw == nil {
				return "step_id is required", nil
			}
			stepID, ok := stepIDRaw.(string)
			if !ok || stepID == "" {
				return "step_id must be a non-empty string", nil
			}

			// Extract group_name and other options
			groupNameRaw, _ := args["group_name"]
			groupName, _ := groupNameRaw.(string)

			// Fallback to session-level group from toolbar selection
			if !iwm.controller.isRelayExecution() && groupName == "" && len(iwm.controller.enabledGroupNames) > 0 {
				groupName = iwm.controller.enabledGroupNames[0]
			}

			// Resolve an execution group when the workspace defines them. Products
			// with no user-visible variables may omit group_name and supply only a
			// run folder; this preserves a stable execution scope without forcing a
			// user to choose workflow machinery.
			if !iwm.controller.isRelayExecution() && groupName == "" {
				iwm.refreshVariablesManifest(ctx)
				if iwm.controller.variablesManifest != nil && len(iwm.controller.variablesManifest.Groups) > 0 {
					// Auto-select the first available group.
					groupName = iwm.controller.variablesManifest.Groups[0].Name
				}
			}

			iteration := "iteration-0"
			if iwm.workshopConfig != nil && iwm.workshopConfig.ScheduleInvocation != nil {
				iteration = iwm.workshopConfig.ScheduleInvocation.BoundRunFolder()
			}

			// Build run_folder from iteration + group folder name
			// Refresh manifest from file to avoid stale group data
			iwm.refreshVariablesManifest(ctx)
			// Resolve group folder name from name (uses sanitized name)
			groupFolderName := groupName
			resolvedGroupName := ""
			if iwm.controller.variablesManifest != nil && groupName != "" {
				for _, g := range iwm.controller.variablesManifest.Groups {
					if g.Name == groupName || iwm.controller.sanitizeDisplayNameForFolder(g.Name) == groupName {
						if g.Name != "" {
							resolvedGroupName = g.Name
							sanitized := iwm.controller.sanitizeDisplayNameForFolder(g.Name)
							if sanitized != "" {
								groupFolderName = sanitized
							}
						}
						break
					}
				}
			}
			runFolder := fmt.Sprintf("%s/%s", iteration, groupFolderName)
			if iwm.controller.isRelayExecution() {
				runFolder, _ = args["run_folder"].(string)
				if runFolder == "" {
					runFolder = "relay-" + workflowExecutionIDToken()
				}
				if err := validateRelayWorkshopRunFolder(runFolder); err != nil {
					return "", err
				}
				iteration = runFolder
				if _, err := iwm.controller.relayVariableValues(); err != nil {
					return "", err
				}
			}

			// Optional orchestrator instructions for inner steps
			instructions, _ := args["instructions"].(string)

			// Optional human input for any step type
			humanInput, _ := args["human_input"].(string)

			// Optional tier override (high=1, medium=2, low=3)
			tierValue := 0
			if tierStr, ok := args["tier"].(string); ok && tierStr != "" {
				switch tierStr {
				case "high":
					tierValue = 1
				case "medium":
					tierValue = 2
				case "low":
					tierValue = 3
				}
			}

			// fast_path_only: run saved script only, no LLM fallback
			fastPathOnly := false
			if val, ok := args["fast_path_only"]; ok && val != nil {
				if b, ok := val.(bool); ok {
					fastPathOnly = b
				}
			}
			messageSequenceRestart := false
			if val, ok := args["message_sequence_restart"]; ok && val != nil {
				if b, ok := val.(bool); ok {
					messageSequenceRestart = b
				}
			}
			var scriptParameters map[string]interface{}
			scriptParametersSet := false
			if raw, present := args["script_parameters"]; present {
				scriptParametersSet = true
				if raw == nil {
					return "script_parameters must be an object", nil
				}
				var ok bool
				scriptParameters, ok = raw.(map[string]interface{})
				if !ok {
					return "script_parameters must be an object", nil
				}
			}
			if err := validateWorkshopFastPathRequest(iwm.currentWorkshopModeFromConfigs(nil), fastPathOnly); err != nil {
				return "", err
			}
			testMode, _ := args["test_mode"].(bool)

			execOpts := &WorkshopExecuteOptions{
				GroupName:       resolvedGroupName,
				Iteration:       iteration,
				RunFolder:       runFolder,
				SavedScriptOnly: fastPathOnly,
				// The Builder running a step itself may author or repair its
				// script; a scheduled session never does (PLAT-436).
				AllowScriptRepair: !fastPathOnly && iwm.currentWorkshopModeFromConfigs(nil) == "workshop" &&
					(iwm.workshopConfig == nil || iwm.workshopConfig.ScheduleInvocation == nil),
				Instructions:           instructions,
				HumanInput:             humanInput,
				Tier:                   tierValue,
				MessageSequenceRestart: messageSequenceRestart,
				ScriptParameters:       scriptParameters,
				ScriptParametersSet:    scriptParametersSet,
			}
			if testMode {
				// PLAT-562: the test run gets its own folder under runs/ and
				// reads upstream outputs copied from the real run folder.
				execOpts.TestRunID = fmt.Sprintf("%s%d", testmode.FolderPrefix, time.Now().UnixNano())
				execOpts.RealRunFolder = runFolder
				execOpts.TestSourceRun, _ = args["source_run"].(string)
				execOpts.RunFolder = fmt.Sprintf("%s/%s", execOpts.TestRunID, groupFolderName)
				if iwm.controller.isRelayExecution() {
					execOpts.RunFolder = execOpts.TestRunID
				}
				execOpts.AllowScriptRepair = false
			}

			// Resolve flexible step ID (handles "1", "step-1", "step1" etc.)
			plan, err := iwm.controller.ReadCurrentPlan(ctx)
			if err != nil {
				return fmt.Sprintf("Failed to load plan: %v. Cannot resolve step ID.", err), nil
			}
			resolvedID, resolveErr := resolveWorkshopStepID(plan, stepID)
			if resolveErr != nil {
				return resolveErr.Error(), nil
			}
			stepID = resolvedID

			// Guard against duplicate concurrent runs of the SAME step. If an
			// execution for this step is already running (e.g. the agent
			// re-issued execute_step, or any double-trigger), return the live
			// execution instead of forking a second concurrent run. Two runs of
			// one step race on shared state (db/ rows, the browser/CDP session)
			// and can double-act — e.g. post the same tweet twice.
			// A test run shares this workshop's controller and its group tool
			// session, so it never overlaps another execution here (PLAT-562).
			iwm.executeStartMu.Lock()
			defer iwm.executeStartMu.Unlock()
			if running := iwm.runningTestRunID; running != "" {
				return fmt.Sprintf("A test-mode run (%s) is in progress in this workshop; wait for it to finish before starting %q.", running, stepID), nil
			}
			if testMode {
				for _, snap := range iwm.stepRegistry.ListSnapshots() {
					if snap.Status == WorkshopStepRunning {
						return fmt.Sprintf("Step %q was NOT started in test mode: step %q is running in this workshop (execution_id %q). Test mode runs only when nothing else runs here; wait for it to finish.", stepID, snap.StepID, snap.ID), nil
					}
				}
			}
			if existing, found, _ := iwm.stepRegistry.LatestSnapshotForStep(stepID); found && existing.Status == WorkshopStepRunning {
				logger.Info(fmt.Sprintf("⏭️ Workshop: execute_step(%q) skipped — already running (execution_id=%q)", stepID, existing.ID))
				return fmt.Sprintf("Step %q is ALREADY RUNNING (execution_id: %q) — not starting a duplicate. You'll be notified when it completes. End the current agent turn instead of polling; use query_step(step_id=%q) only if the user explicitly requests a live status check, or stop that execution first if the user wants a fresh run. (Concurrent runs of the same step race on shared state and can double-act.)", stepID, existing.ID, stepID), nil
			}
			if iwm.controller.isRelayExecution() {
				for _, snap := range iwm.stepRegistry.ListSnapshots() {
					if snap.Status == WorkshopStepRunning {
						return fmt.Sprintf("Step %q was not started: execution %q is running in this Builder. Wait for it to finish before starting another step test.", stepID, snap.ID), nil
					}
				}
			}

			// Pulse runs alongside the workflow's own schedules; it must not run
			// a step the workflow is running right now, or the step acts twice.
			if check := iwm.pulseStepBusyCheck(); check != nil {
				if busy := check(ctx, iwm.controller.GetWorkspacePath(), stepID); busy != "" {
					logger.Info(fmt.Sprintf("⏭️ Workshop: execute_step(%q) held — %s", stepID, busy))
					return fmt.Sprintf("Step %q is being run right now by the workflow's own run (%s), so it was NOT started again: two runs of one step can act twice (send or post twice). Check that run's result when it finishes (query_step or the run history) and verify from it; re-run the step later only if that result shows it is still needed.", stepID, busy), nil
				}
			}

			// The plan type decides (PLAT-287); step_config.json is consulted
			// only for the transitional legacy-agentic shim.
			scriptedStep := false
			var resolvedStep PlanStepInterface
			if plan != nil {
				if stepInfo := findWorkshopStepByID(plan.Steps, stepID); stepInfo != nil {
					resolvedStep = stepInfo.Step
					cfg := getAgentConfigs(stepInfo.Step)
					if cfg.legacyDeclared() == "" {
						if configs, err := iwm.controller.ReadStepConfigs(ctx); err == nil {
							if sc := MatchStepConfigByID(stepID, configs); sc.legacyDeclared() != "" {
								cfg = sc
							}
						}
					}
					scriptedStep = isScriptedStep(stepInfo.Step, cfg)
				}
			}
			if scriptParametersSet && !scriptedStep {
				return fmt.Sprintf("step %q is not scripted; script_parameters is only supported for scripted steps", stepID), nil
			}
			if scriptedStep && resolvedStep != nil {
				resolvedParameters, parameterErr := validateAndResolveScriptParameters(resolvedStep, scriptParameters)
				if parameterErr != nil {
					return fmt.Sprintf("invalid script_parameters for step %q: %v", stepID, parameterErr), nil
				}
				execOpts.ScriptParameters = resolvedParameters
			}

			execID := fmt.Sprintf("exec-%s-%d", stepID, time.Now().UnixNano())
			execOpts.ExecutionID = execID
			execCtx, cancel, ctxErr := iwm.newExecContext(ctx)
			if ctxErr != nil {
				return "Session was stopped — execution skipped", nil
			}

			// Inject correlation IDs so step execution events are tagged as sub-agent
			// events. ForceCorrelationIDKey survives child agent context overwrites
			// (child agents overwrite AgentSessionIDKey but not ForceCorrelationIDKey),
			// so all nested events share the same correlation_id as the
			// orchestrator_agent_start event, enabling frontend grouping.
			agentSessionID := fmt.Sprintf("workshop-step-%s-%d", stepID, time.Now().UnixNano())
			execCtx = context.WithValue(execCtx, orchestrator_events.AgentSessionIDKey, agentSessionID)
			execCtx = context.WithValue(execCtx, orchestrator_events.ForceCorrelationIDKey, agentSessionID)
			execCtx = context.WithValue(execCtx, orchestrator_events.IsSubAgentContextKey, true)

			exec := &WorkshopStepExecution{
				ID:             execID,
				StepID:         stepID,
				AgentSessionID: agentSessionID,
				Status:         WorkshopStepRunning,
				cancel:         cancel,
			}
			iwm.stepRegistry.Register(exec)
			workflowSessionID := ""
			if iwm.mainSessionID != "" {
				parentGroupName := resolvedGroupName
				if parentGroupName == "" {
					parentGroupName = groupName
				}
				parentChat := &virtualtools.ParentChatContext{
					SessionID:    iwm.mainSessionID,
					WorkflowPath: iwm.controller.GetWorkspacePath(),
					GroupName:    parentGroupName,
					AgentID:      execID,
				}
				if workflowSessionID = iwm.controller.GetMCPSessionID(); workflowSessionID != "" {
					virtualtools.RegisterParentChat(workflowSessionID, &virtualtools.ParentChatContext{
						SessionID:    parentChat.SessionID,
						WorkflowPath: parentChat.WorkflowPath,
						GroupName:    parentChat.GroupName,
						AgentID:      parentChat.AgentID,
					})
				}
				virtualtools.RegisterParentChat(agentSessionID, parentChat)
			}

			// Register the background execution before execute_step returns. The
			// scheduler waits on the server-side execution registry after each
			// scheduled workshop turn; registering from inside the goroutine left
			// a race where the parent turn could go idle first and the scheduler
			// would advance (or mark the whole schedule successful) while this
			// step was still starting.
			stepDisplayName := stepID
			if plan != nil {
				if stepInfo := findWorkshopStepByID(plan.Steps, stepID); stepInfo != nil {
					stepDisplayName = stepInfo.Step.GetTitle()
				}
			}
			if resolvedGroupName != "" {
				stepDisplayName = fmt.Sprintf("%s [%s]", stepDisplayName, resolvedGroupName)
			} else if groupName != "" {
				stepDisplayName = fmt.Sprintf("%s [%s]", stepDisplayName, groupName)
			}
			parentExecutionID := currentWorkshopParentExecutionID(execCtx)
			startMetadata := map[string]string{
				"execution_type": "workflow-step",
			}
			if execOpts.TestRunID != "" {
				startMetadata["test_run_id"] = execOpts.TestRunID
			}
			registerWorkshopExecutionBeforeLaunch(iwm.executionNotifier, WorkshopExecutionStart{
				ID:                execID,
				ParentExecutionID: parentExecutionID,
				Name:              stepDisplayName,
				// This is a real workflow step, even when a schedule invokes it
				// directly instead of through run_full_workflow. The scheduler uses
				// the declared kind as invocation evidence for Pulse.
				Kind:     string(orchestrator_events.ExecutionKindWorkflowStep),
				Metadata: startMetadata,
				Cancel:   cancel,
			})
			execCtx = virtualtools.WithBackgroundAgentID(execCtx, execID)
			execCtx = context.WithValue(execCtx, orchestrator_events.ParentExecutionIDKey, execID)
			releaseRunningStep := RegisterRunningWorkflowStep(iwm.controller.GetWorkspacePath(), stepID, execID, iwm.mainSessionID)

			if execOpts.TestRunID != "" {
				iwm.runningTestRunID = execOpts.TestRunID
			}
			go func() {
				if execOpts.TestRunID != "" {
					defer func() {
						iwm.executeStartMu.Lock()
						if iwm.runningTestRunID == execOpts.TestRunID {
							iwm.runningTestRunID = ""
						}
						iwm.executeStartMu.Unlock()
					}()
				}
				defer releaseRunningStep()
				if workflowSessionID != "" {
					defer virtualtools.UnregisterParentChat(workflowSessionID)
				}
				defer virtualtools.UnregisterParentChat(agentSessionID)

				var result string
				var execErr error

				// Inject tier override into context if specified (concurrent-safe via context, not shared field)
				if execOpts.Tier >= 1 && execOpts.Tier <= 3 {
					execCtx = context.WithValue(execCtx, WorkshopTierOverrideKey, execOpts.Tier)
				}

				// Variables captured after execution for metadata
				var isLockCode bool
				var lockCodeConsecutiveFailures int
				var lockCodeNeedsReview bool
				var workshopModeForMeta string

				eventBridge := iwm.controller.GetContextAwareBridge()
				defer func() {
					skipNotify := finalizeExecStatus(exec, execCtx, &result, &execErr)
					if !skipNotify && iwm.executionNotifier != nil {
						execMeta := map[string]string{
							"iteration":  iteration,
							"group_name": groupName,
						}
						if iwm.controller.isRelayExecution() {
							delete(execMeta, "group_name")
							execMeta["run_folder"] = execOpts.RunFolder
						}
						if isLockCode {
							execMeta["lock_code"] = "true"
						}
						if lockCodeConsecutiveFailures > 0 {
							execMeta["lock_code_consecutive_failures"] = fmt.Sprintf("%d", lockCodeConsecutiveFailures)
						}
						if lockCodeNeedsReview {
							execMeta["lock_code_needs_review"] = "true"
						}
						// Use frontend-selected mode if available, else fall back to auto-detection
						if iwm.workshopModeOverride != "" {
							execMeta["workshop_mode"] = iwm.workshopModeOverride
						} else if workshopModeForMeta != "" {
							execMeta["workshop_mode"] = workshopModeForMeta
						}
						iwm.executionNotifier.OnExecutionComplete(execID, stepDisplayName, result, execMeta, execErr)
					}
				}()

				// Emit orchestrator_agent_start so the frontend creates a grouping card
				if eventBridge != nil {
					inputData := map[string]string{}
					if execOpts != nil {
						if execOpts.GroupName != "" {
							inputData["group_name"] = execOpts.GroupName
						}
						if execOpts.Iteration != "" {
							inputData["iteration"] = execOpts.Iteration
						}
						if execOpts.RunFolder != "" {
							inputData["run_folder"] = execOpts.RunFolder
						}
					}
					if scriptedStep {
						inputData["workshop_mode"] = "scripted"
						inputData["IsScriptedMode"] = "true"
					}
					startEvent := &orchestrator_events.OrchestratorAgentStartEvent{
						BaseEventData:        baseevents.BaseEventData{Timestamp: time.Now(), Component: "orchestrator"},
						AgentType:            "workshop-step-execution",
						AgentName:            fmt.Sprintf("Step: %s", stepDisplayName),
						InputData:            inputData,
						Iteration:            parseWorkshopIterationNumber(execOpts.Iteration),
						UseCodeExecutionMode: true,
						UseScriptedMode:      scriptedStep,
					}
					eventBridge.HandleEvent(execCtx, &baseevents.AgentEvent{
						Type:          orchestrator_events.OrchestratorAgentStart,
						Timestamp:     time.Now(),
						Data:          startEvent,
						CorrelationID: agentSessionID,
					})
				}

				result, execErr = iwm.controller.ExecuteStepForWorkshop(execCtx, stepID, execOpts)

				// Capture step lock flags so the auto-notification can tailor
				// recovery guidance (e.g. fast-path failure on a locked step has only two
				// recovery paths: fix main.py after unlocking, or rerun with fast_path_only=false).
				if configs, configErr := iwm.controller.ReadStepConfigs(execCtx); configErr == nil {
					for _, sc := range configs {
						if sc.ID == stepID && sc.AgentConfigs != nil {
							if sc.AgentConfigs.LockCode != nil && *sc.AgentConfigs.LockCode {
								isLockCode = true
							}
							break
						}
					}
					workshopModeForMeta = detectWorkshopMode(nil, configs)
				}

				// If the step is locked, surface its locked-script run history so the auto-
				// notification can flag a "this frozen script keeps failing" pattern to the
				// builder rather than letting it accumulate silently in script_metadata.json.
				if isLockCode {
					if meta := iwm.controller.readScriptedMetadataAPI(execCtx, stepID); meta != nil && meta.LockCodeStats != nil {
						lockCodeConsecutiveFailures = meta.LockCodeStats.ConsecutiveFailures
						lockCodeNeedsReview = meta.LockCodeStats.NeedsReview
					}
				}
			}()

			groupInfo := ""
			if groupName != "" {
				groupInfo = fmt.Sprintf(", group=%q", groupName)
			}
			logger.Info(fmt.Sprintf("🚀 Workshop: step %q started in background, execution_id=%q%s, fast_path_only=%v", stepID, execID, groupInfo, fastPathOnly))
			// Surface the resolved run_folder/group explicitly. group_name is
			// optional on this tool and silently falls back to the first
			// enabled/manifest group when omitted — without this line, an
			// omitted or mistyped group_name silently lands the run in a
			// folder the caller never confirmed, discoverable only by reading
			// runs/ directly (found live: a step run this way was invisible
			// in the Execution Logs UI because its dropdown never listed the
			// folder the run actually used).
			displayGroupName := resolvedGroupName
			if displayGroupName == "" {
				displayGroupName = groupName
			}
			runFolderNotice := fmt.Sprintf("\nrun_folder: %q", runFolder)
			if displayGroupName == "" && !iwm.controller.isRelayExecution() {
				runFolderNotice += " (no group resolved — this workspace may have no defined variable groups)"
			}
			return fmt.Sprintf("Step %q started in background.\nexecution_id: %q%s\nYou will be automatically notified when it completes. End the current agent turn now instead of polling. Use query_step(step_id=%q) only if the user explicitly requests a live status check. Use send_step_message(execution_id=%q, message=...) only for a necessary live correction while an agent turn is active.", stepID, execID, runFolderNotice, stepID, execID), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register execute_step tool: %v", err))
	}

	// Tool 2: query_step — execution status + structured MCP tool call visibility
	// When running: shows status + captured structured tool calls (auto-enriched)
	// When done/failed/cancelled: shows result
	if err := mcpAgent.RegisterCustomTool(
		"query_step",
		"One-off live status check for a tracked execution. For a workflow step, pass step_id and the backend resolves its latest execution automatically. For a background task, pass the execution_id from its start notification; step_id is not required. When running, shows registry status and structured MCP tool calls captured so far. Important: coding CLI providers can show terminal/TUI activity before structured tool_call events exist, so 'no tool calls observed yet' does NOT mean the execution failed to start or is stuck. When a coding-CLI provider runs in tmux, query_step also captures the latest terminal lines and the tmux session name. After one running-status check, end the current agent turn; automatic completion notification will resume the session. Never alternate query_step and list_executions as a polling loop. Do not stop or re-run solely because no tool calls are listed. Pass tool_call_id to get full input/output for a specific tool call. Use debug_step for workflow-step file insights.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"step_id": map[string]interface{}{
					"type":        "string",
					"description": "Workflow step ID to inspect. Required unless execution_id is supplied. The backend resolves the latest execution for this step automatically.",
				},
				"execution_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact tracked execution ID. Use this without step_id for a background task; it can also disambiguate workflow-step executions.",
				},
				"tool_call_id": map[string]interface{}{
					"type":        "string",
					"description": "Optional: a specific tool_call_id from a previous query_step summary to get full input/output details for that call",
				},
			},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			stepID := ""
			if stepIDRaw, ok := args["step_id"]; ok && stepIDRaw != nil {
				value, valueOK := stepIDRaw.(string)
				if !valueOK {
					return "step_id must be a string", nil
				}
				stepID = strings.TrimSpace(value)
			}

			// Optional: specific tool_call_id for detailed view
			toolCallID := ""
			if val, ok := args["tool_call_id"]; ok && val != nil {
				if s, ok := val.(string); ok {
					toolCallID = s
				}
			}

			execID := ""
			if execIDRaw, ok := args["execution_id"]; ok && execIDRaw != nil {
				if s, ok := execIDRaw.(string); ok {
					execID = strings.TrimSpace(s)
				}
			}
			if stepID == "" && execID == "" {
				return "step_id or execution_id is required", nil
			}
			if stepID != "" {
				if plan, err := iwm.controller.ReadCurrentPlan(ctx); err == nil {
					if resolvedID, resolveErr := resolveWorkshopStepID(plan, stepID); resolveErr == nil {
						stepID = resolvedID
					}
				}
			}

			var exec WorkshopStepSnapshot
			var found bool
			if execID != "" {
				exec, found = iwm.stepRegistry.GetSnapshot(execID)
				if !found {
					return fmt.Sprintf("execution %q not found", execID), nil
				}
				if stepID == "" {
					stepID = exec.StepID
				} else if exec.StepID != stepID {
					return fmt.Sprintf("execution %q belongs to step %q, not step %q", execID, exec.StepID, stepID), nil
				}
			} else {
				var matches []WorkshopStepSnapshot
				exec, found, matches = iwm.stepRegistry.LatestSnapshotForStep(stepID)
				if !found {
					if iwm.controller.isRelayExecution() {
						return fmt.Sprintf("No tracked execution found for step %q. Start it with execute_step(step_id=%q).", stepID, stepID), nil
					}
					return fmt.Sprintf("No tracked execution found for step %q. Start it with execute_step(step_id=%q, group_name=...).", stepID, stepID), nil
				}
				execID = exec.ID
				runningCount := 0
				for _, match := range matches {
					if match.Status == WorkshopStepRunning {
						runningCount++
					}
				}
				if runningCount > 1 {
					var ids []string
					for _, match := range matches {
						if match.Status == WorkshopStepRunning {
							ids = append(ids, match.ID)
						}
					}
					return fmt.Sprintf("Step %q has multiple running executions: %s. Use list_executions to inspect them or stop the duplicates.", stepID, strings.Join(ids, ", ")), nil
				}
			}

			status := exec.Status
			result := exec.Result
			execErr := exec.Err
			agentSessID := exec.AgentSessionID
			isGenericAgent := strings.HasPrefix(execID, "generic-agent-")
			isPulseReviewer := strings.HasPrefix(execID, "pulse-review-")
			executionLabel := fmt.Sprintf("Step %q", stepID)
			if isGenericAgent {
				executionLabel = fmt.Sprintf("Generic agent %q", strings.TrimPrefix(stepID, "generic-agent:"))
			} else if isPulseReviewer {
				executionLabel = fmt.Sprintf("Pulse reviewer %q", strings.TrimPrefix(stepID, "pulse-review:"))
			}

			switch status {
			case WorkshopStepRunning:
				pollGuidance := ""
				if toolCallID == "" {
					var suppressed bool
					pollGuidance, suppressed = iwm.runningStatusPollGuidance()
					if suppressed {
						return pollGuidance, nil
					}
				}
				messageHint := "\n\n**Live steering:** no active agent turn is currently available; the execution may be validating or running script-only work. Wait for the next phase or completion notification."
				if exec.CanReceiveMessage {
					messageHint = fmt.Sprintf("\n\n**Live steering:** send_step_message(execution_id=%q, message=...) can steer the active %s phase.", execID, exec.ActiveMessagePhase)
				}
				mainSessID := iwm.mainSessionID
				if mainSessID == "" {
					mainSessID = iwm.sessionID
				}

				// Auto-enrich with live tool calls when running
				var toolCallInfo string
				if iwm.toolCallQueryFunc != nil {
					summary := iwm.toolCallQueryFunc(mainSessID, agentSessID, stepID, toolCallID)
					if toolCallID != "" && summary != "" {
						return fmt.Sprintf("%s (execution_id: %s) — tool call detail:\n%s", executionLabel, execID, summary), nil
					}
					if summary != "" {
						toolCallInfo = fmt.Sprintf("\n\n**Structured MCP tool calls:**\n%s", summary)
					}
				}

				// If the step runs a coding CLI in tmux, surface the live tmux session
				// (the same name shown in the UI terminal panes) so the builder can
				// inspect the actual terminal pane via execute_shell_command. Coding-CLI
				// progress is terminal/TUI output that does NOT show up as MCP tool calls.
				var tmuxInfo string
				if iwm.tmuxLookupFunc != nil {
					if tmuxSession, paneTail, ok := iwm.tmuxLookupFunc(ctx, mainSessID, stepID); ok && tmuxSession != "" {
						tmuxInfo = fmt.Sprintf("\n\n**Live tmux session:** `%s` (coding CLI in tmux).", tmuxSession)
						if strings.TrimSpace(paneTail) != "" {
							tmuxInfo += fmt.Sprintf(" Latest terminal output:\n```\n%s\n```", paneTail)
						}
						tmuxInfo += fmt.Sprintf("\nFor more history, run `tmux capture-pane -pt %s -S -200` via execute_shell_command (read-only; do NOT use `tmux attach`, it blocks).", tmuxSession)
					}
				}

				// Detect execution type from ID prefix and add context
				isAnalysisAgent := strings.HasPrefix(execID, "learn-") || strings.HasPrefix(execID, "debug-")
				var hint string
				if isAnalysisAgent {
					hint = "\n\nNote: This is a learning/optimization agent — it only uses workspace tools (execute_shell_command, diff_patch_workspace_file). For richer insights, use debug_step(step_id) instead."
				}

				if toolCallInfo == "" {
					return fmt.Sprintf("%s is registered and running.\nexecution_id: %s\n\nNo structured MCP tool calls have been captured for this execution yet. This is normal for coding CLI providers while they are booting, thinking, using terminal/TUI output, or before they make their first api-bridge call. It does not mean the execution failed to start or is stuck.\n\nDo not stop or re-run this execution solely because no tool calls are listed; wait for the automatic completion notification unless the user explicitly asks you to stop/retry.%s%s%s\n\n%s", executionLabel, execID, messageHint, tmuxInfo, hint, pollGuidance), nil
				}
				return fmt.Sprintf("%s is still running.\nexecution_id: %s%s%s%s%s\n\n%s", executionLabel, execID, messageHint, toolCallInfo, tmuxInfo, hint, pollGuidance), nil

			case WorkshopStepDone:
				if isGenericAgent || isPulseReviewer {
					return fmt.Sprintf("%s completed.\nexecution_id: %s\n\n%s", executionLabel, execID, result), nil
				}
				// Background tasks get a generic completion response (no step-specific hints)
				if strings.HasPrefix(execID, "bg-") {
					return fmt.Sprintf("Background task %q completed.\n\n%s", stepID, result), nil
				}
				return fmt.Sprintf("Step %q completed.\nexecution_id: %s\n\n%s", stepID, execID, result), nil
			case WorkshopStepFailed:
				if isGenericAgent || isPulseReviewer {
					return fmt.Sprintf("%s failed.\nexecution_id: %s\nerror: %v", executionLabel, execID, execErr), nil
				}
				if strings.HasPrefix(execID, "bg-") {
					return fmt.Sprintf("Background task %q failed: %v", stepID, execErr), nil
				}
				return fmt.Sprintf("Step %q failed.\nexecution_id: %s\nerror: %v", stepID, execID, execErr), nil
			case WorkshopStepCancelled:
				return fmt.Sprintf("%s was cancelled.\nexecution_id: %s", executionLabel, execID), nil
			default:
				return fmt.Sprintf("Step %q has unknown status: %s\nexecution_id: %s", stepID, status, execID), nil
			}
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register query_step tool: %v", err))
	}

	// Tool 2a: send_step_message — steer the exact child agent currently active
	// inside a running execution. This never starts or resumes an execution.
	if err := mcpAgent.RegisterCustomTool(
		"send_step_message",
		"Send a follow-up message to the active agent turn inside one exact running workflow execution. Use the execution_id returned by execute_step or run_full_workflow. This is live steering only: it never starts, restarts, or resumes a completed step. Coding CLI agents receive provider-native live input; API agents queue the message for the next safe turn boundary. If the execution is currently validating or running script-only work, the result is no_active_agent and you should wait rather than retry in a loop.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"execution_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact running execution_id returned by execute_step or run_full_workflow.",
				},
				"message": map[string]interface{}{
					"type":        "string",
					"description": "Follow-up instruction or correction to deliver to the currently active child agent.",
				},
			},
			"required": []string{"execution_id", "message"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			executionID, _ := args["execution_id"].(string)
			message, _ := args["message"].(string)
			result := iwm.stepRegistry.SendMessage(ctx, executionID, message)
			payload, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return "", err
			}
			return string(payload), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register send_step_message tool: %v", err))
	}

	// Tool 2b: debug_step — rich insights about a step's execution
	if iwm.isRunModeRestricted() {
		// PLAT-262: skip debug_step registration for read-only access
	} else if err := mcpAgent.RegisterCustomTool(
		"debug_step",
		"Get detailed insights about a step's execution: learning status, validation result, iteration details for complex steps (todo_task/orchestration), and log paths. Use after a step completes or to inspect a running complex step's progress.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"log_offset": map[string]interface{}{
					"type": "integer", "minimum": 0,
					"description": "Character offset into the saved scripted execution log; use next_log_offset to read more.",
				},
				"iteration": map[string]interface{}{
					"type": "string", "pattern": "^iteration-[0-9]+(?:-(?:hook|sched|slack-[a-f0-9]+))?$",
					"description": "Run iteration to inspect, default iteration-0.",
				},
				"step_id": map[string]interface{}{
					"type":        "string",
					"description": "The step ID from plan.json (e.g., 'step-create-report') or positional reference (e.g., '1', 'step-1')",
				},
				"group_name": map[string]interface{}{
					"type":        "string",
					"description": "Variable group ID (e.g., 'group-1', 'saurabh'). Required. Read variables/variables.json to see available groups.",
				},
			},
			"required": []string{"step_id", "group_name"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			stepIDRaw, ok := args["step_id"]
			if !ok || stepIDRaw == nil {
				return "step_id is required", nil
			}
			stepID, ok := stepIDRaw.(string)
			if !ok || stepID == "" {
				return "step_id must be a non-empty string", nil
			}

			// Extract group_name
			groupName, _ := args["group_name"].(string)

			iteration := "iteration-0"
			if value, ok := args["iteration"].(string); ok && value != "" {
				if !regexp.MustCompile(`^iteration-[0-9]+(?:-(?:hook|sched|slack-[a-f0-9]+))?$`).MatchString(value) {
					return "iteration must be iteration-<number>", nil
				}
				iteration = value
			}
			if groupName == "" && !iwm.controller.isRelayExecution() {
				return "group_name is required (e.g., 'group-1'). Read variables/variables.json to see available groups.", nil
			}

			// Refresh manifest from file to avoid stale group data
			iwm.refreshVariablesManifest(ctx)
			// Resolve group folder name and build run folder
			groupFolderName := groupName
			if iwm.controller.variablesManifest != nil {
				for _, g := range iwm.controller.variablesManifest.Groups {
					if g.Name == groupName || iwm.controller.sanitizeDisplayNameForFolder(g.Name) == groupName {
						if g.Name != "" {
							sanitized := iwm.controller.sanitizeDisplayNameForFolder(g.Name)
							if sanitized != "" {
								groupFolderName = sanitized
							}
						}
						break
					}
				}
			}
			runFolder := fmt.Sprintf("%s/%s", iteration, groupFolderName)
			if iwm.controller.isRelayExecution() {
				runFolder, _ = args["run_folder"].(string)
				if runFolder == "" {
					runFolder = iwm.controller.selectedRunFolder
				}
				if err := validateRelayWorkshopRunFolder(runFolder); err != nil {
					return "", err
				}
			}
			iwm.controller.SetSelectedRunFolder(runFolder)

			// Resolve step ID
			plan, err := iwm.controller.ReadCurrentPlan(ctx)
			if err != nil {
				return fmt.Sprintf("Failed to load plan: %v", err), nil
			}
			resolvedID, resolveErr := resolveWorkshopStepID(plan, stepID)
			if resolveErr != nil {
				return resolveErr.Error(), nil
			}

			stepInfo := findWorkshopStepByID(plan.Steps, resolvedID)
			if stepInfo == nil {
				return fmt.Sprintf("step %q not found in plan", stepID), nil
			}

			var result strings.Builder
			result.WriteString(fmt.Sprintf("## Debug: %s (%s)\n\n", stepInfo.Step.GetTitle(), resolvedID))

			// Section 1: Learning status
			learningsPath := getLearningFolderPathByStepID("", resolvedID, "")
			learningFiles, _ := iwm.controller.readStepLearningFiles(ctx, learningsPath)

			stepConfigs, _ := iwm.controller.ReadStepConfigs(ctx)
			learningAccess := LearningsAccessRead
			for _, sc := range stepConfigs {
				if sc.ID == resolvedID && sc.AgentConfigs != nil {
					learningAccess = resolveLearningsAccess(sc.AgentConfigs)
					break
				}
			}

			result.WriteString("### Learnings\n")
			if len(learningFiles) > 0 {
				fileNames := make([]string, 0, len(learningFiles))
				for f := range learningFiles {
					fileNames = append(fileNames, f)
				}
				sort.Strings(fileNames)
				result.WriteString(fmt.Sprintf("Files: %d | Access: %s | Path: %s\n", len(learningFiles), learningAccess, learningsPath))
				for _, f := range fileNames {
					result.WriteString(fmt.Sprintf("  - %s\n", f))
				}
			} else {
				result.WriteString(fmt.Sprintf("No learnings yet | Access: %s | Path: %s\n", learningAccess, learningsPath))
			}
			result.WriteString("\n")

			// Section 2: Validation result
			logFolder := workshopStepLogFolder(resolvedID)
			validationLogDir := fmt.Sprintf("runs/%s/logs/%s", runFolder, logFolder)
			logPath := validationLogDir + "/execution/scripted_fast_path.json"
			if content, err := iwm.controller.ReadWorkspaceFile(ctx, logPath); err == nil {
				offset := 0
				if value, ok := args["log_offset"].(float64); ok && value > 0 {
					offset = int(value)
				}
				result.WriteString("### Scripted execution\n" + logPath + "\n")
				result.WriteString(scriptedLogPage(content, offset))
			}

			result.WriteString("### Validation\n")
			foundValidation := false
			for i := 5; i >= 2; i-- {
				vPath := fmt.Sprintf("%s/validation-%d.json", validationLogDir, i)
				if content, err := iwm.controller.ReadWorkspaceFile(ctx, vPath); err == nil {
					result.WriteString(content)
					result.WriteString("\n")
					foundValidation = true
					break
				}
			}
			if !foundValidation {
				if content, err := iwm.controller.ReadWorkspaceFile(ctx, fmt.Sprintf("%s/validation.json", validationLogDir)); err == nil {
					result.WriteString(content)
					result.WriteString("\n")
				} else {
					result.WriteString("No validation result found.\n")
				}
			}
			result.WriteString("\n")

			// Section 3: Complex step details (todo_task/orchestration)
			complexSummary := iwm.enrichQueryForComplexStep(ctx, resolvedID)
			if complexSummary != "" {
				result.WriteString("### Execution Details")
				result.WriteString(complexSummary)
				result.WriteString("\n")
			}

			// Section 4: Log paths for manual inspection
			result.WriteString("### Log Paths\n")
			result.WriteString(fmt.Sprintf("Execution logs: runs/%s/logs/%s/execution/\n", runFolder, logFolder))
			result.WriteString(fmt.Sprintf("Validation: %s/\n", validationLogDir))
			result.WriteString(fmt.Sprintf("Learnings: %s/\n", learningsPath))
			result.WriteString("Use execute_shell_command to read these files for details.\n")

			return result.String(), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register debug_step tool: %v", err))
	}

	// Tool 2b: list_executions — list all tracked background executions
	if err := mcpAgent.RegisterCustomTool(
		"list_executions",
		"One-off execution lookup for finding an execution_id or resolving ambiguity. Shows execution_id, step_id, status (running/done/failed/cancelled), and whether a running execution currently accepts send_step_message. Do not use it as a progress poll. After a result shows running work, end the current agent turn; automatic completion notification will resume the session. Never alternate list_executions and query_step as a polling loop.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"status_filter": map[string]interface{}{
					"type":        "string",
					"description": "Optional filter: 'running', 'done', 'failed', 'cancelled'. If omitted, returns all.",
				},
			},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			statusFilter, _ := args["status_filter"].(string)

			allExecs := iwm.stepRegistry.ListSnapshots()

			// Sort by creation time for chronological order.
			sort.Slice(allExecs, func(i, j int) bool {
				if allExecs[i].CreatedAt.Equal(allExecs[j].CreatedAt) {
					return allExecs[i].ID < allExecs[j].ID
				}
				return allExecs[i].CreatedAt.Before(allExecs[j].CreatedAt)
			})

			// Build set of registry IDs for dedup against server agents
			registryIDs := make(map[string]struct{}, len(allExecs))
			for _, exec := range allExecs {
				registryIDs[exec.ID] = struct{}{}
			}

			var sb strings.Builder
			count := 0
			hasRunning := false
			for _, exec := range allExecs {
				status := string(exec.Status)
				execErr := exec.Err

				if statusFilter != "" && status != statusFilter {
					continue
				}

				count++
				if status == "running" {
					hasRunning = true
				}
				sb.WriteString(fmt.Sprintf("- **%s** | step: %s | status: %s", exec.ID, exec.StepID, status))
				if exec.CanReceiveMessage {
					sb.WriteString(fmt.Sprintf(" | messageable: %s", exec.ActiveMessagePhase))
				}
				if status == "failed" && execErr != nil {
					sb.WriteString(fmt.Sprintf(" | error: %v", execErr))
				}
				sb.WriteString("\n")
			}

			// Merge server-tracked agents not in stepRegistry
			if iwm.listServerAgents != nil {
				for _, agent := range iwm.listServerAgents() {
					if _, exists := registryIDs[agent.ID]; exists {
						continue
					}
					if statusFilter != "" && agent.Status != statusFilter {
						continue
					}
					count++
					if agent.Status == "running" {
						hasRunning = true
					}
					sb.WriteString(fmt.Sprintf("- **%s** | step: %s | status: %s (server)\n", agent.ID, agent.Name, agent.Status))
				}
			}

			hasPending := iwm.hasPendingCompletions != nil && iwm.hasPendingCompletions()
			if hasPending {
				sb.WriteString("\n⚠️ Completions pending delivery (agents finished while session was busy).\n")
			}

			pollGuidance := ""
			if (statusFilter == "" || statusFilter == "running") && (hasRunning || hasPending) {
				var suppressed bool
				pollGuidance, suppressed = iwm.runningStatusPollGuidance()
				if suppressed {
					return pollGuidance, nil
				}
			}

			if count == 0 && hasPending {
				return "No running executions, but **completions are pending delivery** — results will arrive shortly.\nDo NOT report \"all clear\".\n\n" + pollGuidance, nil
			}

			if count == 0 {
				if statusFilter != "" {
					return fmt.Sprintf("No executions with status %q. Total tracked: %d.", statusFilter, len(allExecs)), nil
				}
				if len(allExecs) == 0 {
					return "No background executions tracked in this session.", nil
				}
				return "No background executions tracked.", nil
			}

			return fmt.Sprintf("**%d execution(s)**%s:\n%s\n%s", count, func() string {
				if statusFilter != "" {
					return fmt.Sprintf(" (filter: %s)", statusFilter)
				}
				return ""
			}(), sb.String(), pollGuidance), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register list_executions tool: %v", err))
	}

	// Tool 3: stop_step — cancel a running step
	if err := mcpAgent.RegisterCustomTool(
		"stop_step",
		"Cancel a tracked workflow step or background task only after query_step currently reports that exact execution_id as running. A wait timeout does not prove the execution is still running because completion notifications can be delayed. Never use this as cleanup for completed, failed, or already-cancelled work; those states are rejected.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"execution_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact execution_id returned by a start call or shown in a background-task start notification.",
				},
			},
			"required": []string{"execution_id"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			execIDRaw, ok := args["execution_id"]
			if !ok || execIDRaw == nil {
				return "execution_id is required", nil
			}
			execID, ok := execIDRaw.(string)
			if !ok || execID == "" {
				return "execution_id must be a non-empty string", nil
			}

			exec, err := stopWorkshopExecution(iwm.stepRegistry, iwm.executionNotifier, execID)
			if errors.Is(err, ErrWorkshopExecutionNotFound) {
				return fmt.Sprintf("execution %q not found", execID), nil
			}
			if errors.Is(err, ErrWorkshopExecutionNotCancelable) {
				logger.Warn(fmt.Sprintf("⚠️ Workshop: step %q (execution_id=%q) has no cancel function", exec.StepID, execID))
				return fmt.Sprintf("Step %q (execution_id=%q) is tracked but cannot be cancelled individually.", exec.StepID, execID), nil
			}
			if err != nil {
				return "", err
			}

			logger.Info(fmt.Sprintf("🛑 Workshop: step %q (execution_id=%q) cancelled", exec.StepID, execID))
			return fmt.Sprintf("Step %q (execution_id=%q) has been canceled.", exec.StepID, execID), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register stop_step tool: %v", err))
	}

	// Tool 3b: stop_all_executions — cancel all running background executions at once
	if err := mcpAgent.RegisterCustomTool(
		"stop_all_executions",
		"Cancel ALL running background executions (steps, learnings, optimizations, background agents). Use this when the user asks to stop, cancel, or abort everything.",
		map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			// Cancel in stepRegistry first
			cancelledExecs := iwm.stepRegistry.cancelAllWithNotification(func(exec WorkshopStepSnapshot) {
				if iwm.executionNotifier != nil {
					iwm.executionNotifier.OnExecutionTerminated(exec.ID, exec.StepID)
				}
			})

			// Also cancel through server's bgAgentRegistry (catches anything stepRegistry missed)
			if iwm.cancelAllServerAgents != nil {
				iwm.cancelAllServerAgents()
			}

			if len(cancelledExecs) == 0 {
				return "No running executions found to cancel.", nil
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("Cancelled %d running execution(s):\n", len(cancelledExecs)))
			for _, exec := range cancelledExecs {
				sb.WriteString(fmt.Sprintf("- %s (step: %s)\n", exec.ID, exec.StepID))
			}
			logger.Info(fmt.Sprintf("🛑 Workshop: cancelled all %d running executions", len(cancelledExecs)))
			return sb.String(), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register stop_all_executions tool: %v", err))
	}

	// === Builder tools: config, optimization, learning ===

	// Tool 4: update_step_config shares its schema and executor with external clients.
	if iwm.isRunModeRestricted() {
		// PLAT-262: skip update_step_config registration for read-only access.
	} else if err := mcpAgent.RegisterCustomTool("update_step_config", updateStepConfigToolDescription,
		getUpdateStepConfigParameters(), createUpdateStepConfigExecutor(iwm.controller, logger), "workflow"); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register update_step_config tool: %v", err))
	}

	if iwm.currentWorkshopModeFromConfigs(nil) != "run" {
		// Tool 5: get_plan_prompt_health — compact, deterministic size/duplication
		// metrics for authored step descriptions. This avoids pasting a whole plan into
		// a reviewer prompt just to establish whether prompt-contract bloat exists.
		if err := mcpAgent.RegisterCustomTool(
			"get_plan_prompt_health",
			"Measure authored plan-description health without dumping the plan: per-step character counts, 5k/10k/20k thresholds, long verbatim duplicate paragraphs, and the PLAT-556 budget per step (size vs this plan's median with OVER budget above 3x the median, floor 12,000 chars; dated/incident text; 300+ characters repeated verbatim across steps; missing ## Goal/Inputs/Output/Rules/Done when/Guides sections). consolidation_due_steps are the steps that make Architecture (prompt_design) due. reference_map (PLAT-561) lists references that no longer resolve: dependencies no step produces or stages, step folders and retired step ids, missing workflow files and eval routes named by steps, evals, KB notes, soul.md and learnings guides. Budgets and breaks are reports, never gates: they never block a run or an edit.",
			map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			func(ctx context.Context, _ map[string]interface{}) (string, error) {
				report, err := iwm.controller.currentPlanPromptHealth(ctx)
				if err != nil {
					return "", fmt.Errorf("load plan for prompt-health review: %w", err)
				}
				encoded, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return "", fmt.Errorf("encode prompt-health report: %w", err)
				}
				return string(encoded), nil
			},
			"workflow",
		); err != nil {
			logger.Warn(fmt.Sprintf("⚠️ Failed to register get_plan_prompt_health tool: %v", err))
		}
	}

	// Tool 6: get_step_prompts — read saved system prompt + user message for a step run
	if err := mcpAgent.RegisterCustomTool(
		"get_step_prompts",
		"Get the system prompt and user message for a step. Works both during execution (prompts saved at start) and after completion. Useful for debugging what instructions the agent received. For sub-agent steps, pass the inner step ID directly (e.g., 'step-icici-login') or use route_id with the parent step.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"step_id": map[string]interface{}{
					"type":        "string",
					"description": "The step ID from plan.json (e.g., 'step-create-report') or inner step ID (e.g., 'step-icici-login')",
				},
				"route_id": map[string]interface{}{
					"type":        "string",
					"description": "Optional route ID for a conversational specialist. Pair it with call_id and the owning parent Agent step_id.",
				},
				"call_id": map[string]interface{}{
					"type":        "string",
					"description": "Exact specialist invocation ID beneath agents/{route-id}/calls/{call-id}. Required with route_id.",
				},
				"attempt": map[string]interface{}{
					"type":        "integer",
					"description": "Retry attempt number (default: 1)",
				},
				"iteration": map[string]interface{}{
					"type":        "integer",
					"description": "Loop iteration number (default: 0)",
				},
			},
			"required": []string{"step_id"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			stepIDRaw, ok := args["step_id"]
			if !ok || stepIDRaw == nil {
				return "step_id is required", nil
			}
			stepID, ok := stepIDRaw.(string)
			if !ok || stepID == "" {
				return "step_id must be a non-empty string", nil
			}

			routeID, _ := args["route_id"].(string)
			callID, _ := args["call_id"].(string)

			// Resolve historical positions only against the selected run's revision.
			// Exact stable IDs remain usable when old runs predate plan revisions.
			if strings.ContainsAny(stepID, `/\`) || stepID == "." || stepID == ".." || strings.ContainsAny(routeID, `/\`) || strings.ContainsAny(callID, `/\`) {
				return "", fmt.Errorf("step_id, route_id, and call_id must be IDs, not file paths")
			}
			if routeID != "" && strings.TrimSpace(callID) == "" {
				return "", fmt.Errorf("call_id is required with route_id; routed logs are isolated per invocation")
			}
			resolvedForPrompts := stepID
			var stepInfo *WorkshopStepInfo
			plan, planErr := iwm.controller.readRunPlanSnapshot(ctx, iwm.controller.selectedRunFolder)
			if planErr == nil {
				resolved, resolveErr := resolveWorkshopStepID(plan, stepID)
				if resolveErr != nil {
					return "", resolveErr
				}
				resolvedForPrompts = resolved
				stepInfo = findWorkshopStepByID(plan.Steps, resolved)
			} else if routeID != "" {
				return "", fmt.Errorf("cannot resolve historical step position: %w; use an exact step ID without route_id", planErr)
			}

			attempt := 1
			if v, ok := args["attempt"]; ok && v != nil {
				if f, ok := v.(float64); ok {
					attempt = int(f)
				}
			}
			iteration := 0
			if v, ok := args["iteration"]; ok && v != nil {
				if f, ok := v.(float64); ok {
					iteration = int(f)
				}
			}

			runFolder := iwm.controller.selectedRunFolder
			if runFolder == "" {
				return "no run folder selected", nil
			}

			// Determine the correct log path based on step type
			var stepPath string
			if routeID != "" && stepInfo != nil && stepInfo.TopIndex > 0 {
				stepPath = todoSubAgentArtifactFolderName(stepInfo.Step.GetID(), stepInfo.Step.GetID(), routeID, callID, false)
			} else {
				stepPath = workshopStepLogFolder(resolvedForPrompts)
			}
			logDir := fmt.Sprintf("runs/%s/logs/%s/execution", runFolder, stepPath)
			filenameBase := fmt.Sprintf("execution-attempt-%d-iteration-%d", attempt, iteration)

			var result strings.Builder
			hasUserMessage := false // Track if user message was already included from prompts.json

			// Read system prompt and user message from prompts.json (saved pre-execution and updated post-execution).
			// Todo-task orchestrators use a stable non-attempt filename.
			promptsPath := fmt.Sprintf("%s/%s-prompts.json", logDir, filenameBase)
			promptsContent, err := iwm.controller.ReadWorkspaceFile(ctx, promptsPath)
			if err != nil {
				altPath := fmt.Sprintf("%s/todo-task-prompts.json", logDir)
				if tc, te := iwm.controller.ReadWorkspaceFile(ctx, altPath); te == nil {
					promptsContent = tc
					err = nil
					promptsPath = altPath
				}
			}
			if err != nil {
				// A legacy ID can itself be "step-1". Accept its actual artifact,
				// but never translate that reference using today's plan positions.
				if planErr != nil && isPositionalStepReference(stepID) {
					return "", fmt.Errorf("no exact prompt artifact and cannot resolve historical position: %w; use the run's exact step ID", planErr)
				}
				result.WriteString(fmt.Sprintf("⚠️ Prompts file not found (%s).\nNote: only available for runs after this feature was added.\n\n", promptsPath))
			} else {
				var promptsData map[string]interface{}
				if jsonErr := workshopJSONUnmarshal([]byte(promptsContent), &promptsData); jsonErr == nil {
					// Show when the prompt was saved (pre_execution = still running, post_execution = completed)
					if savedAt, ok := promptsData["saved_at"].(string); ok {
						result.WriteString(fmt.Sprintf("**Prompt saved at**: %s\n", savedAt))
					}
					if model, ok := promptsData["model"].(string); ok && model != "" {
						result.WriteString(fmt.Sprintf("**Model**: %s\n\n", model))
					}
					result.WriteString("## System Prompt\n\n")
					if sp, ok := promptsData["system_prompt"].(string); ok {
						result.WriteString(sp)
					} else {
						result.WriteString("(system_prompt field missing)")
					}
					result.WriteString("\n\n")
					// User message from prompts.json (available from pre-execution save)
					if um, ok := promptsData["user_message"].(string); ok && um != "" {
						result.WriteString("## User Message\n\n")
						result.WriteString(um)
						result.WriteString("\n\n")
						hasUserMessage = true
					}
				} else {
					result.WriteString("## System Prompt\n\n")
					result.WriteString(promptsContent)
					result.WriteString("\n\n")
				}
			}

			// Read user message from conversation.json (first human message in history)
			// Skip if user message was already included from prompts.json
			if hasUserMessage {
				return result.String(), nil
			}
			convPath := fmt.Sprintf("%s/%s-conversation.json", logDir, filenameBase)
			convContent, err := iwm.controller.ReadWorkspaceFile(ctx, convPath)
			if err != nil {
				result.WriteString(fmt.Sprintf("⚠️ Conversation file not found (%s): %v\n", convPath, err))
			} else {
				result.WriteString("## User Message (first turn)\n\n")
				var convData map[string]interface{}
				if jsonErr := workshopJSONUnmarshal([]byte(convContent), &convData); jsonErr == nil {
					if history, ok := convData["conversation_history"].([]interface{}); ok && len(history) > 0 {
						// Find the first human message (skip system message at index 0).
						// Role field is PascalCase ("Role") since MessageContent has no JSON tags.
						var humanMsg map[string]interface{}
						for _, msg := range history {
							m, ok := msg.(map[string]interface{})
							if !ok {
								continue
							}
							role, _ := m["Role"].(string)
							if role == "" {
								role, _ = m["role"].(string)
							}
							if role == "human" || role == "Human" || role == "user" {
								humanMsg = m
								break
							}
						}
						if humanMsg != nil {
							parts, ok := humanMsg["Parts"].([]interface{})
							if !ok {
								parts, ok = humanMsg["parts"].([]interface{})
							}
							if ok {
								for _, part := range parts {
									if partMap, ok := part.(map[string]interface{}); ok {
										text, found := partMap["Text"].(string)
										if !found {
											text, found = partMap["text"].(string)
										}
										if found {
											result.WriteString(text)
											break
										}
									}
								}
							}
						} else {
							result.WriteString("(no human message found in conversation history)")
						}
					} else {
						result.WriteString("(conversation history empty or unparseable)")
					}
				} else {
					result.WriteString(fmt.Sprintf("(could not parse conversation JSON: %v)", jsonErr))
				}
			}

			return result.String(), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register get_step_prompts tool: %v", err))
	}

	// === Tools: analyze, learn, optimize, background tasks ===

	const registerRetiredCostTimingReviewers = false
	if registerRetiredCostTimingReviewers {
		// Tool 7f3: review_workflow_timing — retired in favor of /pulse-review
		if err := mcpAgent.RegisterCustomTool(
			"review_workflow_timing",
			"Start a background agent that reviews workflow runtime and step timing from actual run evidence, identifies the main latency bottlenecks, and recommends how to make the workflow faster without compromising the objective or success criteria. Read-only. Returns execution_id immediately — you will be automatically notified when it completes.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"focus": map[string]interface{}{
						"type":        "string",
						"description": "Optional focus for the review, e.g., 'slowest step', 'tool latency', 'too many steps', 'merge opportunities', 'description ambiguity'.",
					},
					"iteration": map[string]interface{}{
						"type":        "string",
						"description": "Optional iteration folder (e.g., 'iteration-3'). If omitted, the selected run folder is used when present; otherwise the reviewer finds the latest meaningful run evidence.",
					},
					"group_name": map[string]interface{}{
						"type":        "string",
						"description": "Optional group/user subfolder within the iteration (e.g., 'saurabh'). Use together with iteration for grouped workflows.",
					},
				},
			},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				focus := ""
				if val, ok := args["focus"]; ok && val != nil {
					if s, ok := val.(string); ok {
						focus = s
					}
				}
				targetRunFolder := ""
				if iter, ok := args["iteration"]; ok && iter != nil {
					if s, ok := iter.(string); ok && strings.TrimSpace(s) != "" {
						targetRunFolder = strings.TrimSpace(s)
						if gid, ok := args["group_name"]; ok && gid != nil {
							if g, ok := gid.(string); ok && strings.TrimSpace(g) != "" {
								targetRunFolder += "/" + strings.TrimSpace(g)
							}
						}
					}
				}
				if targetRunFolder == "" {
					targetRunFolder = strings.TrimSpace(iwm.controller.selectedRunFolder)
				}

				execID := fmt.Sprintf("review-timing-%05d", time.Now().UnixNano()%100000)
				execCtx, cancel, ctxErr := iwm.newExecContext(ctx)
				if ctxErr != nil {
					return "Session was stopped — execution skipped", nil
				}

				agentSessionID := fmt.Sprintf("workshop-review-timing-%d", time.Now().UnixNano())
				execCtx = context.WithValue(execCtx, orchestrator_events.AgentSessionIDKey, agentSessionID)
				execCtx = context.WithValue(execCtx, orchestrator_events.ForceCorrelationIDKey, agentSessionID)
				execCtx = context.WithValue(execCtx, orchestrator_events.IsSubAgentContextKey, true)

				exec := &WorkshopStepExecution{
					ID:             execID,
					StepID:         "review-workflow-timing",
					AgentSessionID: agentSessionID,
					Status:         WorkshopStepRunning,
					cancel:         cancel,
				}
				iwm.stepRegistry.Register(exec)

				if iwm.executionNotifier != nil {
					iwm.executionNotifier.OnExecutionStart(WorkshopExecutionStart{
						ID:                execID,
						ParentExecutionID: currentWorkshopParentExecutionID(execCtx),
						Name:              "Review Workflow Timing",
						Kind:              string(orchestrator_events.ExecutionKindSubAgent),
						Cancel:            cancel,
					})
				}
				execCtx = virtualtools.WithBackgroundAgentID(execCtx, execID)
				execCtx = context.WithValue(execCtx, orchestrator_events.ParentExecutionIDKey, execID)

				go func() {
					var result string
					var execErr error
					eventBridge := iwm.controller.GetContextAwareBridge()
					defer func() {
						skipNotify := finalizeExecStatus(exec, execCtx, &result, &execErr)
						if eventBridge != nil {
							isCancelled := skipNotify || execCtx.Err() != nil
							endEvent := &orchestrator_events.OrchestratorAgentEndEvent{
								BaseEventData: baseevents.BaseEventData{Timestamp: time.Now(), Component: "orchestrator"},
								AgentType:     "workshop-review-timing",
								AgentName:     "Review Workflow Timing",
								Success:       execErr == nil,
							}
							if execErr != nil {
								if isCancelled {
									endEvent.Result = fmt.Sprintf("Cancelled: %v", execErr)
								} else {
									endEvent.Result = fmt.Sprintf("Failed: %v", execErr)
								}
							} else {
								endEvent.Result = result
							}
							eventBridge.HandleEvent(execCtx, &baseevents.AgentEvent{
								Type: orchestrator_events.OrchestratorAgentEnd, Timestamp: time.Now(),
								Data: endEvent, CorrelationID: agentSessionID,
							})
						}
						if !skipNotify && iwm.executionNotifier != nil {
							iwm.executionNotifier.OnExecutionComplete(execID, "Review Workflow Timing", result, nil, execErr)
						}
					}()

					if eventBridge != nil {
						startEvent := &orchestrator_events.OrchestratorAgentStartEvent{
							BaseEventData: baseevents.BaseEventData{Timestamp: time.Now(), Component: "orchestrator"},
							AgentType:     "workshop-review-timing",
							AgentName:     "Review Workflow Timing",
						}
						eventBridge.HandleEvent(execCtx, &baseevents.AgentEvent{
							Type: orchestrator_events.OrchestratorAgentStart, Timestamp: time.Now(),
							Data: startEvent, CorrelationID: agentSessionID,
						})
					}

					result, execErr = iwm.runReviewWorkflowTimingAgent(execCtx, targetRunFolder, focus)
				}()

				focusInfo := ""
				if focus != "" {
					focusInfo = fmt.Sprintf("\nFocus: %s", focus)
				}
				runInfo := ""
				if targetRunFolder != "" {
					runInfo = fmt.Sprintf("\nTarget run folder: %s", targetRunFolder)
				}
				logger.Info(fmt.Sprintf("⏱️ Workshop: review_workflow_timing agent started in background, execution_id=%q, target_run_folder=%q", execID, targetRunFolder))
				return fmt.Sprintf("Workflow timing review agent started in background.\nexecution_id: %q%s%s\nYou will be automatically notified when it completes.", execID, runInfo, focusInfo), nil
			},
			"workflow",
		); err != nil {
			logger.Warn(fmt.Sprintf("⚠️ Failed to register review_workflow_timing tool: %v", err))
		}

		// Tool 7f4: review_workflow_costs — retired in favor of /pulse-review
		if err := mcpAgent.RegisterCustomTool(
			"review_workflow_costs",
			"Start a background agent that reviews workflow token/cost data from actual run evidence, identifies the biggest cost drivers, and recommends how to reduce cost without compromising the objective or success criteria. Read-only. Returns execution_id immediately — you will be automatically notified when it completes.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"focus": map[string]interface{}{
						"type":        "string",
						"description": "Optional focus for the review, e.g., 'expensive step', 'model tier', 'retry waste', 'evaluation cost', 'merge opportunities'.",
					},
					"iteration": map[string]interface{}{
						"type":        "string",
						"description": "Optional iteration folder (e.g., 'iteration-3'). If omitted, the selected run folder is used when present; otherwise the reviewer finds the latest meaningful cost/run evidence.",
					},
					"group_name": map[string]interface{}{
						"type":        "string",
						"description": "Optional group/user subfolder within the iteration (e.g., 'saurabh'). Use together with iteration for grouped workflows.",
					},
				},
			},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				focus := ""
				if val, ok := args["focus"]; ok && val != nil {
					if s, ok := val.(string); ok {
						focus = s
					}
				}
				targetRunFolder := ""
				if iter, ok := args["iteration"]; ok && iter != nil {
					if s, ok := iter.(string); ok && strings.TrimSpace(s) != "" {
						targetRunFolder = strings.TrimSpace(s)
						if gid, ok := args["group_name"]; ok && gid != nil {
							if g, ok := gid.(string); ok && strings.TrimSpace(g) != "" {
								targetRunFolder += "/" + strings.TrimSpace(g)
							}
						}
					}
				}
				if targetRunFolder == "" {
					targetRunFolder = strings.TrimSpace(iwm.controller.selectedRunFolder)
				}

				execID := fmt.Sprintf("review-costs-%05d", time.Now().UnixNano()%100000)
				execCtx, cancel, ctxErr := iwm.newExecContext(ctx)
				if ctxErr != nil {
					return "Session was stopped — execution skipped", nil
				}

				agentSessionID := fmt.Sprintf("workshop-review-costs-%d", time.Now().UnixNano())
				execCtx = context.WithValue(execCtx, orchestrator_events.AgentSessionIDKey, agentSessionID)
				execCtx = context.WithValue(execCtx, orchestrator_events.ForceCorrelationIDKey, agentSessionID)
				execCtx = context.WithValue(execCtx, orchestrator_events.IsSubAgentContextKey, true)

				exec := &WorkshopStepExecution{
					ID:             execID,
					StepID:         "review-workflow-costs",
					AgentSessionID: agentSessionID,
					Status:         WorkshopStepRunning,
					cancel:         cancel,
				}
				iwm.stepRegistry.Register(exec)

				if iwm.executionNotifier != nil {
					iwm.executionNotifier.OnExecutionStart(WorkshopExecutionStart{
						ID:                execID,
						ParentExecutionID: currentWorkshopParentExecutionID(execCtx),
						Name:              "Review Workflow Costs",
						Kind:              string(orchestrator_events.ExecutionKindSubAgent),
						Cancel:            cancel,
					})
				}
				execCtx = virtualtools.WithBackgroundAgentID(execCtx, execID)
				execCtx = context.WithValue(execCtx, orchestrator_events.ParentExecutionIDKey, execID)

				go func() {
					var result string
					var execErr error
					eventBridge := iwm.controller.GetContextAwareBridge()
					defer func() {
						skipNotify := finalizeExecStatus(exec, execCtx, &result, &execErr)
						if eventBridge != nil {
							isCancelled := skipNotify || execCtx.Err() != nil
							endEvent := &orchestrator_events.OrchestratorAgentEndEvent{
								BaseEventData: baseevents.BaseEventData{Timestamp: time.Now(), Component: "orchestrator"},
								AgentType:     "workshop-review-costs",
								AgentName:     "Review Workflow Costs",
								Success:       execErr == nil,
							}
							if execErr != nil {
								if isCancelled {
									endEvent.Result = fmt.Sprintf("Cancelled: %v", execErr)
								} else {
									endEvent.Result = fmt.Sprintf("Failed: %v", execErr)
								}
							} else {
								endEvent.Result = result
							}
							eventBridge.HandleEvent(execCtx, &baseevents.AgentEvent{
								Type: orchestrator_events.OrchestratorAgentEnd, Timestamp: time.Now(),
								Data: endEvent, CorrelationID: agentSessionID,
							})
						}
						if !skipNotify && iwm.executionNotifier != nil {
							iwm.executionNotifier.OnExecutionComplete(execID, "Review Workflow Costs", result, nil, execErr)
						}
					}()

					if eventBridge != nil {
						startEvent := &orchestrator_events.OrchestratorAgentStartEvent{
							BaseEventData: baseevents.BaseEventData{Timestamp: time.Now(), Component: "orchestrator"},
							AgentType:     "workshop-review-costs",
							AgentName:     "Review Workflow Costs",
						}
						eventBridge.HandleEvent(execCtx, &baseevents.AgentEvent{
							Type: orchestrator_events.OrchestratorAgentStart, Timestamp: time.Now(),
							Data: startEvent, CorrelationID: agentSessionID,
						})
					}

					result, execErr = iwm.runReviewWorkflowCostsAgent(execCtx, targetRunFolder, focus)
				}()

				focusInfo := ""
				if focus != "" {
					focusInfo = fmt.Sprintf("\nFocus: %s", focus)
				}
				runInfo := ""
				if targetRunFolder != "" {
					runInfo = fmt.Sprintf("\nTarget run folder: %s", targetRunFolder)
				}
				logger.Info(fmt.Sprintf("💸 Workshop: review_workflow_costs agent started in background, execution_id=%q, target_run_folder=%q", execID, targetRunFolder))
				return fmt.Sprintf("Workflow cost review agent started in background.\nexecution_id: %q%s%s\nYou will be automatically notified when it completes.", execID, runInfo, focusInfo), nil
			},
			"workflow",
		); err != nil {
			logger.Warn(fmt.Sprintf("⚠️ Failed to register review_workflow_costs tool: %v", err))
		}
	}

	// Tool: review_step_code — background agent that checks if saved scripts match step descriptions
	if iwm.isRunModeRestricted() {
		// PLAT-262: skip review_step_code registration for read-only access
	} else if err := mcpAgent.RegisterCustomTool(
		"review_step_code",
		"Start a background agent that compares saved main.py scripts with current descriptions to detect drift. Reviews workflow steps and evaluation steps. Over time, descriptions get updated but scripts don't — this tool finds where they've gone out of sync. Read-only. Returns execution_id immediately — you will be automatically notified when it completes.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"step_id": map[string]interface{}{
					"type":        "string",
					"description": "Optional workflow step ID or evaluation step ID to review. If omitted, reviews all saved main.py scripts across workflow and evaluation code.",
				},
				"focus": map[string]interface{}{
					"type":        "string",
					"description": "Optional focus for the review, e.g., 'output format', 'missing features', 'hardcoded values', 'portability'.",
				},
			},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			stepID := ""
			if val, ok := args["step_id"]; ok && val != nil {
				if s, ok := val.(string); ok {
					stepID = strings.TrimSpace(s)
				}
			}
			focus := ""
			if val, ok := args["focus"]; ok && val != nil {
				if s, ok := val.(string); ok {
					focus = s
				}
			}

			execID := fmt.Sprintf("review-step-code-%05d", time.Now().UnixNano()%100000)
			execCtx, cancel, ctxErr := iwm.newExecContext(ctx)
			if ctxErr != nil {
				return "Session was stopped — execution skipped", nil
			}

			agentSessionID := fmt.Sprintf("workshop-review-step-code-%d", time.Now().UnixNano())
			execCtx = context.WithValue(execCtx, orchestrator_events.AgentSessionIDKey, agentSessionID)
			execCtx = context.WithValue(execCtx, orchestrator_events.ForceCorrelationIDKey, agentSessionID)
			execCtx = context.WithValue(execCtx, orchestrator_events.IsSubAgentContextKey, true)

			exec := &WorkshopStepExecution{
				ID:             execID,
				StepID:         "review-step-code",
				AgentSessionID: agentSessionID,
				Status:         WorkshopStepRunning,
				cancel:         cancel,
			}
			iwm.stepRegistry.Register(exec)

			if iwm.executionNotifier != nil {
				iwm.executionNotifier.OnExecutionStart(WorkshopExecutionStart{
					ID:                execID,
					ParentExecutionID: currentWorkshopParentExecutionID(execCtx),
					Name:              "Review Step Code",
					Kind:              string(orchestrator_events.ExecutionKindSubAgent),
					Cancel:            cancel,
				})
			}
			execCtx = virtualtools.WithBackgroundAgentID(execCtx, execID)
			execCtx = context.WithValue(execCtx, orchestrator_events.ParentExecutionIDKey, execID)

			go func() {
				var result string
				var execErr error
				eventBridge := iwm.controller.GetContextAwareBridge()
				defer func() {
					skipNotify := finalizeExecStatus(exec, execCtx, &result, &execErr)
					if eventBridge != nil {
						isCancelled := skipNotify || execCtx.Err() != nil
						endEvent := &orchestrator_events.OrchestratorAgentEndEvent{
							BaseEventData: baseevents.BaseEventData{Timestamp: time.Now(), Component: "orchestrator"},
							AgentType:     "workshop-review-step-code",
							AgentName:     "Review Step Code",
							Success:       execErr == nil,
						}
						if execErr != nil {
							if isCancelled {
								endEvent.Result = fmt.Sprintf("Cancelled: %v", execErr)
							} else {
								endEvent.Result = fmt.Sprintf("Failed: %v", execErr)
							}
						} else {
							endEvent.Result = result
						}
						eventBridge.HandleEvent(execCtx, &baseevents.AgentEvent{
							Type: orchestrator_events.OrchestratorAgentEnd, Timestamp: time.Now(),
							Data: endEvent, CorrelationID: agentSessionID,
						})
					}
					if !skipNotify && iwm.executionNotifier != nil {
						iwm.executionNotifier.OnExecutionComplete(execID, "Review Step Code", result, nil, execErr)
					}
				}()

				if eventBridge != nil {
					startEvent := &orchestrator_events.OrchestratorAgentStartEvent{
						BaseEventData: baseevents.BaseEventData{Timestamp: time.Now(), Component: "orchestrator"},
						AgentType:     "workshop-review-step-code",
						AgentName:     "Review Step Code",
					}
					eventBridge.HandleEvent(execCtx, &baseevents.AgentEvent{
						Type: orchestrator_events.OrchestratorAgentStart, Timestamp: time.Now(),
						Data: startEvent, CorrelationID: agentSessionID,
					})
				}

				result, execErr = iwm.runReviewStepCodeAgent(execCtx, stepID, focus)
			}()

			stepInfo := ""
			if stepID != "" {
				stepInfo = fmt.Sprintf("\nStep: %s", stepID)
			} else {
				stepInfo = "\nScope: all saved main.py scripts across workflow, evaluation, and scoring"
			}
			focusInfo := ""
			if focus != "" {
				focusInfo = fmt.Sprintf("\nFocus: %s", focus)
			}
			logger.Info(fmt.Sprintf("🔍 Workshop: review_step_code agent started in background, execution_id=%q, step_id=%q", execID, stepID))
			return fmt.Sprintf("Step code review agent started in background.\nexecution_id: %q%s%s\nYou will be automatically notified when it completes.", execID, stepInfo, focusInfo), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register review_step_code tool: %v", err))
	}

	// Tool 8: get_cost_summary — parse token_usage.json and show formatted cost breakdown
	registerGetCostSummaryTool(iwm, mcpAgent, logger)

	// === Tools: background tasks, LLM config, workflow config ===

	// Tool: get_llm_config — show current LLM configuration (read-only)
	if err := mcpAgent.RegisterCustomTool(
		"get_llm_config",
		"Show every effective workflow LLM role (Builder, execution high/medium/low, and Pulse), including provider/model, reasoning, inheritance source, override status, and per-step overrides.",
		map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			manifest, manifestErr := iwm.controller.ReadWorkspaceFile(ctx, "workflow.json")
			if manifestErr != nil {
				return "", fmt.Errorf("read workflow LLM configuration: %w", manifestErr)
			}
			resolved, resolveErr := renderResolvedWorkflowLLMRoles(manifest)
			if resolveErr != nil {
				return "", fmt.Errorf("resolve workflow LLM configuration: %w", resolveErr)
			}
			var sb strings.Builder
			sb.WriteString(resolved)

			// Show per-step overrides from step_config.json
			stepConfigs, err := iwm.controller.ReadStepConfigs(ctx)
			if err != nil {
				sb.WriteString("\n### Per-Step LLM Overrides\nCould not read step_config.json.\n")
			} else {
				hasOverrides := false
				for _, sc := range stepConfigs {
					if sc.AgentConfigs == nil {
						continue
					}
					ac := sc.AgentConfigs
					if ac.ExecutionLLM == nil {
						continue
					}
					if !hasOverrides {
						sb.WriteString("\n### Per-Step LLM Overrides\n")
						hasOverrides = true
					}
					sb.WriteString(fmt.Sprintf("\n**%s**:\n", sc.ID))
					if ac.ExecutionLLM != nil {
						sb.WriteString(fmt.Sprintf("  - execution: %s/%s\n", ac.ExecutionLLM.Provider, ac.ExecutionLLM.ModelID))
					}
				}
				if !hasOverrides {
					sb.WriteString("\n### Per-Step LLM Overrides\nNone — all steps use workflow defaults.\n")
				}
			}

			return sb.String(), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register get_llm_config tool: %v", err))
	}

	// Tool: update_variable — add, update, or delete variables
	updateVariableSchema := getUpdateVariableSchema()
	updateVariableParams, parseErr := parseSchemaForToolParameters(updateVariableSchema)
	if parseErr != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to parse update_variable schema: %v", parseErr))
	} else if iwm.isRunModeRestricted() {
		// PLAT-262: skip update_variable registration for read-only access
	} else if err := mcpAgent.RegisterCustomTool(
		"update_variable",
		"Update, add, or delete variables in variables/variables.json. Provide action (required: 'update', 'add', or 'delete'), existing_variable_name (required for update/delete), and fields to update (name, value, description). The variables/variables.json file is updated immediately.",
		updateVariableParams,
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			executor := createUpdateVariableExecutor(iwm.controller.GetWorkspacePath(), logger,
				func(ctx context.Context, path string) (string, error) {
					return iwm.controller.ReadWorkspaceFile(ctx, path)
				},
				func(ctx context.Context, path string, content string) error {
					return iwm.controller.WriteWorkspaceFile(ctx, path, content)
				}, iwm.controller.isRelayExecution())
			result, execErr := executor(ctx, args)
			if execErr == nil {
				iwm.refreshVariablesManifest(ctx)
			}
			return result, execErr
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register update_variable tool: %v", err))
	}

	// Tool: add_group — create a new variable group
	if iwm.isRunModeRestricted() {
		// PLAT-262: skip add_group registration for read-only access
	} else if err := mcpAgent.RegisterCustomTool(
		"add_group",
		"Create a new variable group. Optionally provide a name and initial values. The new group will have all defined variables with empty values by default.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Name for the group (e.g., 'Production', 'Staging'). If not provided, an auto-generated name is used.",
				},
				"values": map[string]interface{}{
					"type":        "object",
					"description": "Optional initial variable values as key-value pairs (e.g., {\"API_URL\": \"https://prod.example.com\"}). Variables not specified will have empty values.",
					"additionalProperties": map[string]interface{}{
						"type": "string",
					},
				},
			},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			readFile := func(ctx context.Context, path string) (string, error) {
				return iwm.controller.ReadWorkspaceFile(ctx, path)
			}
			writeFile := func(ctx context.Context, path string, content string) error {
				return iwm.controller.WriteWorkspaceFile(ctx, path, content)
			}
			workspacePath := iwm.controller.GetWorkspacePath()

			manifest, err := readVariablesFromFile(ctx, workspacePath, readFile)
			if err != nil {
				// Create new manifest if none exists
				manifest = &VariablesManifest{
					Variables:      []Variable{},
					Groups:         []VariableGroup{},
					ExtractionDate: time.Now().Format(time.RFC3339),
				}
			}

			newGroup := manifest.AddGroup()

			// Set name if provided
			if name, ok := args["name"].(string); ok && name != "" {
				newGroup.Name = name
			}

			// Set initial values if provided
			if values, ok := args["values"].(map[string]interface{}); ok {
				for k, v := range values {
					if strVal, ok := v.(string); ok {
						newGroup.Values[k] = strVal
					}
				}
			}

			if err := writeVariablesToFile(ctx, workspacePath, manifest, readFile, writeFile, logger); err != nil {
				return "", fmt.Errorf("failed to write variables: %w", err)
			}
			iwm.refreshVariablesManifest(ctx)

			return fmt.Sprintf("Created new group: %s with %d variables", newGroup.Name, len(newGroup.Values)), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register add_group tool: %v", err))
	}

	// Tool: update_group — update an existing variable group's name, values, or enabled status
	if iwm.isRunModeRestricted() {
		// PLAT-262: skip update_group registration for read-only access
	} else if err := mcpAgent.RegisterCustomTool(
		"update_group",
		"Update a variable group. Provide name (required) and fields to change: new_name, values (key-value map), enabled (true/false). Only provided fields are updated.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "The name of the group to update (e.g., 'group-1')",
				},
				"new_name": map[string]interface{}{
					"type":        "string",
					"description": "New name for the group",
				},
				"values": map[string]interface{}{
					"type":        "object",
					"description": "Variable values to set or update as key-value pairs. Only specified variables are updated; others remain unchanged.",
					"additionalProperties": map[string]interface{}{
						"type": "string",
					},
				},
				"enabled": map[string]interface{}{
					"type":        "boolean",
					"description": "Enable or disable the group for execution",
				},
			},
			"required": []interface{}{"name"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			groupName, ok := args["name"].(string)
			if !ok || groupName == "" {
				return "", fmt.Errorf("name is required")
			}

			readFile := func(ctx context.Context, path string) (string, error) {
				return iwm.controller.ReadWorkspaceFile(ctx, path)
			}
			writeFile := func(ctx context.Context, path string, content string) error {
				return iwm.controller.WriteWorkspaceFile(ctx, path, content)
			}
			workspacePath := iwm.controller.GetWorkspacePath()

			manifest, err := readVariablesFromFile(ctx, workspacePath, readFile)
			if err != nil {
				return "", fmt.Errorf("failed to read variables: %w", err)
			}

			// Find the group
			groupIdx := -1
			for i := range manifest.Groups {
				if manifest.Groups[i].Name == groupName {
					groupIdx = i
					break
				}
			}
			if groupIdx == -1 {
				return "", fmt.Errorf("group %s not found", groupName)
			}

			changes := []string{}

			// Update name
			if newName, ok := args["new_name"].(string); ok {
				manifest.Groups[groupIdx].Name = newName
				changes = append(changes, fmt.Sprintf("name=%s", newName))
			}

			// Update enabled
			if enabled, ok := args["enabled"].(bool); ok {
				manifest.Groups[groupIdx].Enabled = enabled
				changes = append(changes, fmt.Sprintf("enabled=%v", enabled))
			}

			// Update values (merge, don't replace)
			if values, ok := args["values"].(map[string]interface{}); ok {
				if manifest.Groups[groupIdx].Values == nil {
					manifest.Groups[groupIdx].Values = make(map[string]string)
				}
				for k, v := range values {
					if strVal, ok := v.(string); ok {
						manifest.Groups[groupIdx].Values[k] = strVal
						changes = append(changes, fmt.Sprintf("%s=%s", k, strVal))
					}
				}
			}

			if len(changes) == 0 {
				return "No changes specified", nil
			}

			if err := writeVariablesToFile(ctx, workspacePath, manifest, readFile, writeFile, logger); err != nil {
				return "", fmt.Errorf("failed to write variables: %w", err)
			}
			if newName, renamed := args["new_name"].(string); renamed && newName != "" &&
				len(iwm.controller.enabledGroupNames) == 1 && iwm.controller.enabledGroupNames[0] == groupName {
				iwm.controller.enabledGroupNames = []string{newName}
			}
			iwm.refreshVariablesManifest(ctx)

			return fmt.Sprintf("Updated group %s: %s", groupName, strings.Join(changes, ", ")), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register update_group tool: %v", err))
	}

	// Tool: delete_group — remove a variable group
	if iwm.isRunModeRestricted() {
		// PLAT-262: skip delete_group registration for read-only access
	} else if err := mcpAgent.RegisterCustomTool(
		"delete_group",
		"Delete a variable group by name. Cannot delete the last remaining group.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "The name of the group to delete (e.g., 'group-2')",
				},
			},
			"required": []interface{}{"name"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			groupName, ok := args["name"].(string)
			if !ok || groupName == "" {
				return "", fmt.Errorf("name is required")
			}

			readFile := func(ctx context.Context, path string) (string, error) {
				return iwm.controller.ReadWorkspaceFile(ctx, path)
			}
			writeFile := func(ctx context.Context, path string, content string) error {
				return iwm.controller.WriteWorkspaceFile(ctx, path, content)
			}
			workspacePath := iwm.controller.GetWorkspacePath()

			manifest, err := readVariablesFromFile(ctx, workspacePath, readFile)
			if err != nil {
				return "", fmt.Errorf("failed to read variables: %w", err)
			}

			if len(manifest.Groups) <= 1 {
				return "", fmt.Errorf("cannot delete the last remaining group")
			}

			if !manifest.DeleteGroup(groupName) {
				return "", fmt.Errorf("group %s not found", groupName)
			}

			if err := writeVariablesToFile(ctx, workspacePath, manifest, readFile, writeFile, logger); err != nil {
				return "", fmt.Errorf("failed to write variables: %w", err)
			}
			iwm.refreshVariablesManifest(ctx)

			return fmt.Sprintf("Deleted group %s. Remaining groups: %d", groupName, len(manifest.Groups)), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register delete_group tool: %v", err))
	}

	// Tool: get_workflow_config — read-only view of workflow-level settings (MCP servers, skills, secrets, LLM config)
	if err := mcpAgent.RegisterCustomTool(
		"get_workflow_config",
		"Show current workflow configuration: selected workflow MCP servers, selected workflow skills, secrets (names only, no values), approved external folder access, workflow-scoped notification content instructions and one-way destinations, active owner-approved advisor specialization, run retention, LLM config (tiered allocation, preset defaults), and schedules.",
		map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			ctrl := iwm.controller
			refreshWorkflowFolderAccessSession(ctx, ctrl.GetWorkspacePath())
			selected := ctrl.GetSelectedServers()
			var sb strings.Builder
			sb.WriteString("## Workflow Configuration\n\n")
			sb.WriteString(workflowKnowledgebaseSourcesPrompt(ctrl.GetWorkspacePath()) + "\n\n")
			if candidates, err := workflowkb.Candidates(GetPromptDocsRoot(), ctrl.GetWorkspacePath()); err == nil {
				sb.WriteString("### Available knowledge source workflows\n")
				for _, source := range candidates {
					sb.WriteString(fmt.Sprintf("- %s: %s\n", source.WorkflowID, source.Label))
				}
			}
			if content, err := ctrl.ReadWorkspaceFile(ctx, "workflow.json"); err == nil {
				var manifest struct {
					Access *struct {
						AllowedKBWriters []string `json:"allowed_kb_writers"`
					} `json:"access"`
					CrewAttachments []workflowtypes.CrewAttachment `json:"crew_attachments"`
				}
				if json.Unmarshal([]byte(content), &manifest) == nil {
					sb.WriteString("\n### KB write grants (this workflow's own knowledgebase)\n")
					if manifest.Access == nil || len(manifest.Access.AllowedKBWriters) == 0 {
						sb.WriteString("No other workflow may write into this workflow's knowledgebase/notes/. Grant with update_workflow_config(kb_write_grants=[...]).\n")
					} else {
						sb.WriteString(fmt.Sprintf("Permitted writers: %s. Change with update_workflow_config(kb_write_grants=[...]).\n", strings.Join(manifest.Access.AllowedKBWriters, ", ")))
					}
					sb.WriteString("\n### Attached Crews (read-only)\n")
					if len(manifest.CrewAttachments) == 0 {
						sb.WriteString("No Crew projects are attached. Crew steps require their project attached; manage with manage_crew_attachment.\n")
					} else {
						for _, attachment := range manifest.CrewAttachments {
							sb.WriteString(fmt.Sprintf("- **%s** → crew project %s (profile %s); read files as `%s/<path>`\n", attachment.Alias, attachment.CrewProjectID, attachment.CrewProfileID, attachment.Alias))
						}
					}
				}
			}

			// --- MCP Servers ---
			sb.WriteString("### Selected MCP Servers\n")
			if len(selected) == 0 {
				sb.WriteString("No MCP servers selected for this workflow.\n")
			} else {
				for _, s := range selected {
					sb.WriteString(fmt.Sprintf("- %s\n", s))
				}
			}

			// --- Skills ---
			selectedSkills := ctrl.GetSelectedSkills()

			sb.WriteString("\n### Selected Skills\n")
			if len(selectedSkills) == 0 {
				sb.WriteString("No skills selected for this workflow.\n")
			} else {
				for _, sk := range selectedSkills {
					sb.WriteString(fmt.Sprintf("- **%s** — instructions at `skills/%s/SKILL.md`\n", sk, sk))
				}
			}

			// --- Owner-approved external folders ---
			sb.WriteString(workflowtypes.FolderGrantsPrompt(workflowFolderAccess(ctrl.GetWorkspacePath()), "WORKFLOW_FOLDER_", "No external folders are attached. Use Workflow toolbar → Attached folders; an agent cannot approve a host path for itself.\n"))
			if requests := workflowFolderAccessRequests(ctrl.GetWorkspacePath()); len(requests) > 0 {
				sb.WriteString("Pending requests:\n")
				for _, request := range requests {
					sb.WriteString(fmt.Sprintf("- **%s** (`%s`) — %s — %s\n", request.Alias, request.ID, request.Access, request.Reason))
				}
			}

			// --- Browser ---
			sb.WriteString("\n### Browser\n")
			mode := strings.ToLower(strings.TrimSpace(ctrl.GetBrowserMode()))
			if mode == "" {
				mode = "none"
			}
			sb.WriteString(fmt.Sprintf("- browser_mode: %s\n", mode))
			if ports := ctrl.GetCdpPorts(); len(ports) > 0 {
				sb.WriteString(fmt.Sprintf("- cdp_ports: %v (independent Chrome profiles/login identities)\n", ports))
			} else {
				sb.WriteString("- cdp_ports: (default single port when CDP is selected)\n")
			}

			// --- Secrets (names only) ---
			secrets := ctrl.GetSecrets()
			sb.WriteString("\n### Secrets\n")
			if len(secrets) == 0 {
				sb.WriteString("No secrets configured for this workflow.\n")
			} else {
				sb.WriteString("**Selected** (values hidden):\n")
				for _, s := range secrets {
					sb.WriteString(fmt.Sprintf("- **%s**\n", s.Name))
				}
			}

			// --- One-way notifications (secret references only) ---
			sb.WriteString("\n### Notifications\n")
			var slackWebhookSecretName string
			var runNotificationInstructions string
			var pulseNotificationInstructions string
			var runNotificationChannels []string
			var pulseNotificationChannels []string
			var notificationExcludeChannels []string
			var notificationBlockRecipients []string
			var notificationGmailConnectionID string
			var runNotificationRecipients []string
			var pulseNotificationRecipients []string
			var runNotificationWebhooks []string
			var pulseNotificationWebhooks []string
			if content, readErr := ctrl.ReadWorkspaceFile(ctx, "workflow.json"); readErr == nil {
				var manifest struct {
					Capabilities struct {
						Notifications struct {
							SlackWebhookSecretName   string   `json:"slack_webhook_secret_name"`
							RunSummaryInstructions   string   `json:"run_summary_instructions"`
							PulseSummaryInstructions string   `json:"pulse_summary_instructions"`
							RunSummaryChannels       []string `json:"run_summary_channels"`
							PulseSummaryChannels     []string `json:"pulse_summary_channels"`
							Instructions             string   `json:"instructions"`
							ExcludeChannels          []string `json:"exclude_channels"`
							BlockRecipients          []string `json:"block_recipients"`
							GmailConnectionID        string   `json:"gmail_connection_id"`
							RunSummaryRecipients     []string `json:"run_summary_recipients"`
							PulseSummaryRecipients   []string `json:"pulse_summary_recipients"`

							RunSummarySlackWebhookSecretNames   []string `json:"run_summary_slack_webhook_secret_names"`
							PulseSummarySlackWebhookSecretNames []string `json:"pulse_summary_slack_webhook_secret_names"`
						} `json:"notifications"`
					} `json:"capabilities"`
				}
				if json.Unmarshal([]byte(content), &manifest) == nil {
					slackWebhookSecretName = strings.TrimSpace(manifest.Capabilities.Notifications.SlackWebhookSecretName)
					runNotificationInstructions = strings.TrimSpace(manifest.Capabilities.Notifications.RunSummaryInstructions)
					pulseNotificationInstructions = strings.TrimSpace(manifest.Capabilities.Notifications.PulseSummaryInstructions)
					legacyInstructions := strings.TrimSpace(manifest.Capabilities.Notifications.Instructions)
					if runNotificationInstructions == "" {
						runNotificationInstructions = legacyInstructions
					}
					if pulseNotificationInstructions == "" {
						pulseNotificationInstructions = legacyInstructions
					}
					notificationExcludeChannels = uniqueStringsPreserveOrder(manifest.Capabilities.Notifications.ExcludeChannels)
					notificationBlockRecipients = uniqueStringsPreserveOrder(manifest.Capabilities.Notifications.BlockRecipients)
					notificationGmailConnectionID = strings.TrimSpace(manifest.Capabilities.Notifications.GmailConnectionID)
					runNotificationRecipients = uniqueStringsPreserveOrder(manifest.Capabilities.Notifications.RunSummaryRecipients)
					pulseNotificationRecipients = uniqueStringsPreserveOrder(manifest.Capabilities.Notifications.PulseSummaryRecipients)
					runNotificationWebhooks = uniqueStringsPreserveOrder(manifest.Capabilities.Notifications.RunSummarySlackWebhookSecretNames)
					pulseNotificationWebhooks = uniqueStringsPreserveOrder(manifest.Capabilities.Notifications.PulseSummarySlackWebhookSecretNames)
					runNotificationChannels = uniqueStringsPreserveOrder(manifest.Capabilities.Notifications.RunSummaryChannels)
					pulseNotificationChannels = uniqueStringsPreserveOrder(manifest.Capabilities.Notifications.PulseSummaryChannels)
				}
			}
			if slackWebhookSecretName == "" {
				sb.WriteString("- Slack Incoming Webhook: not configured\n")
			} else {
				sb.WriteString(fmt.Sprintf("- Slack Incoming Webhook: configured via encrypted secret **%s** (one-way notify_user delivery; URL hidden)\n", slackWebhookSecretName))
			}
			if runNotificationInstructions == "" {
				sb.WriteString("- Workflow run summary instructions: not configured\n")
			} else {
				sb.WriteString("- Workflow run summary instructions:\n  " + strings.ReplaceAll(runNotificationInstructions, "\n", "\n  ") + "\n")
			}
			if pulseNotificationInstructions == "" {
				sb.WriteString("- Pulse review summary instructions: not configured\n")
			} else {
				sb.WriteString("- Pulse review summary instructions:\n  " + strings.ReplaceAll(pulseNotificationInstructions, "\n", "\n  ") + "\n")
			}
			if len(runNotificationChannels) > 0 {
				sb.WriteString(fmt.Sprintf("- Workflow run summary channels: %s\n", strings.Join(runNotificationChannels, ", ")))
			}
			if len(pulseNotificationChannels) > 0 {
				sb.WriteString(fmt.Sprintf("- Pulse review summary channels: %s\n", strings.Join(pulseNotificationChannels, ", ")))
			}
			if len(notificationExcludeChannels) > 0 {
				sb.WriteString(fmt.Sprintf("- Excluded channels: %s\n", strings.Join(notificationExcludeChannels, ", ")))
			}
			if len(notificationBlockRecipients) > 0 {
				sb.WriteString(fmt.Sprintf("- Blocked recipients: %s\n", strings.Join(notificationBlockRecipients, ", ")))
			}
			// Show the saved sender and enabled choices so Builder can configure it.
			if gmailSvc := services.GetGmailService(); gmailSvc != nil {
				connections := gmailSvc.ListConnections()
				if notificationGmailConnectionID == "" {
					sb.WriteString("- Gmail sender: not set — inherits the account default connection\n")
				} else {
					label := notificationGmailConnectionID
					for _, conn := range connections {
						if conn.ID == notificationGmailConnectionID {
							label = fmt.Sprintf("%s (%s)", conn.ID, conn.DisplayName)
							break
						}
					}
					sb.WriteString(fmt.Sprintf("- Gmail sender: %s\n", label))
				}
				if len(connections) > 0 {
					available := make([]string, 0, len(connections))
					for _, conn := range connections {
						if !conn.Enabled {
							continue
						}
						entry := conn.ID
						if conn.DisplayName != "" {
							entry += " — " + conn.DisplayName
						}
						if st, ok := gmailSvc.AuthStatusForConnection(conn.ID); ok && st.Email != "" {
							entry += " <" + st.Email + ">"
						}
						available = append(available, entry)
					}
					if len(available) > 0 {
						sb.WriteString(fmt.Sprintf("- Available Gmail senders (set `notification_gmail_connection_id` with update_workflow_config to choose): %s\n",
							strings.Join(available, "; ")))
					}
				}
			} else if notificationGmailConnectionID == "" {
				sb.WriteString("- Gmail sender: not set — inherits the account default connection\n")
			} else {
				sb.WriteString(fmt.Sprintf("- Gmail sender connection ID: %s (connection status unavailable)\n", notificationGmailConnectionID))
			}
			// State both recipient lists unconditionally. "Not configured" is a real
			// answer here — it means mail goes to the account default — and leaving
			// the line out entirely reads as "no email is sent", which is wrong.
			sb.WriteString(fmt.Sprintf("- Workflow run summary recipients: %s\n", notificationRecipientSummary(runNotificationRecipients)))
			sb.WriteString(fmt.Sprintf("- Pulse review summary recipients: %s\n", notificationRecipientSummary(pulseNotificationRecipients)))
			if len(runNotificationWebhooks) > 0 || len(pulseNotificationWebhooks) > 0 {
				sb.WriteString(fmt.Sprintf("- Workflow run summary Slack channels: %s\n", notificationWebhookSummary(runNotificationWebhooks)))
				sb.WriteString(fmt.Sprintf("- Pulse review summary Slack channels: %s\n", notificationWebhookSummary(pulseNotificationWebhooks)))
			}

			// Show available secrets that can be added
			if iwm.listAvailableSecrets != nil {
				allSecretNames, listErr := iwm.listAvailableSecrets(ctx)
				if listErr == nil && len(allSecretNames) > 0 {
					selectedSet := make(map[string]bool, len(secrets))
					for _, s := range secrets {
						selectedSet[s.Name] = true
					}
					var available []string
					for _, name := range allSecretNames {
						if !selectedSet[name] {
							available = append(available, name)
						}
					}
					if len(available) > 0 {
						sb.WriteString("\n**Available to add** (use update_workflow_config with add_secrets):\n")
						for _, name := range available {
							sb.WriteString(fmt.Sprintf("- %s\n", name))
						}
					}
				}
			}

			// --- LLM Configuration ---
			sb.WriteString("\n### LLM Configuration\n")

			// Tiered config
			if ctrl.tierResolver != nil && ctrl.tierResolver.config != nil {
				tc := ctrl.tierResolver.config
				sb.WriteString("\n**Tiered Allocation (active)**:\n")
				writeTierEntry := func(label string, cfg *AgentLLMConfig) {
					if cfg == nil {
						return
					}
					sb.WriteString(fmt.Sprintf("- **%s**: %s/%s", label, cfg.Provider, cfg.ModelID))

					sb.WriteString("\n")
				}
				writeTierEntry("Tier 1 (high)", tc.Tier1)
				writeTierEntry("Tier 2 (medium)", tc.Tier2)
				writeTierEntry("Tier 3 (low)", tc.Tier3)
			}

			// Knowledgebase state. Write ownership is configured per step through
			// knowledgebase_access + knowledgebase_contribution.
			sb.WriteString("\n**Knowledgebase**:\n")
			sb.WriteString(fmt.Sprintf("- use_knowledgebase: %v\n", ctrl.UseKnowledgebase()))
			sb.WriteString(fmt.Sprintf("- kb_shape: %s", workflowtypes.ResolveKBShape(ctrl.KBShape())))
			if ctrl.KBShape() == "" {
				sb.WriteString(" (default — not explicitly set)")
			}
			sb.WriteString("\n")
			runRetentionCount := defaultRunRetentionCount
			runRetentionNote := " (default)"
			if content, err := ctrl.ReadWorkspaceFile(ctx, "workflow.json"); err == nil {
				var manifest struct {
					RunRetentionCount *int `json:"run_retention_count"`
				}
				if json.Unmarshal([]byte(content), &manifest) == nil && manifest.RunRetentionCount != nil {
					if *manifest.RunRetentionCount >= 1 && *manifest.RunRetentionCount <= maxRunRetentionCount {
						runRetentionCount = *manifest.RunRetentionCount
						runRetentionNote = ""
					} else {
						runRetentionNote = fmt.Sprintf(" (invalid; runtime uses default %d)", defaultRunRetentionCount)
					}
				}
			}
			sb.WriteString(fmt.Sprintf("- run_retention_count: %d%s\n", runRetentionCount, runRetentionNote))

			// --- Owner-approved advisor specialization ---
			sb.WriteString("\n### Advisor Specialization\n")
			if content, err := ctrl.ReadWorkspaceFile(ctx, "workflow.json"); err == nil {
				specialization, parseErr := readWorkflowAdvisorSpecialization(content)
				if parseErr == nil && specialization != nil {
					sb.WriteString(fmt.Sprintf("- version: %d\n- approved decision: %s\n", specialization.Version, specialization.ApprovedInputID))
					sb.WriteString("- Strategy Auditor:\n  " + strings.ReplaceAll(strings.TrimSpace(specialization.StrategyAuditor), "\n", "\n  ") + "\n")
					sb.WriteString("- Goal Advisor:\n  " + strings.ReplaceAll(strings.TrimSpace(specialization.GoalAdvisor), "\n", "\n  ") + "\n")
				} else {
					sb.WriteString("No workflow-specific advisor specialization is active.\n")
				}
			} else {
				sb.WriteString("Could not read workflow.json.\n")
			}

			// Preset-level defaults
			sb.WriteString("\n**Preset Defaults**:\n")
			writeLLMDefault := func(label string, llm *AgentLLMConfig) {
				if llm != nil {
					sb.WriteString(fmt.Sprintf("- **%s**: %s/%s", label, llm.Provider, llm.ModelID))

					sb.WriteString("\n")
				} else {
					sb.WriteString(fmt.Sprintf("- **%s**: (not set — uses LLM config default)\n", label))
				}
			}
			writeLLMDefault("Builder", ctrl.presetPhaseLLM)
			writeLLMDefault("Pulse", ctrl.presetPulseLLM)

			// --- Schedules ---
			if iwm.schedulerFuncs != nil && iwm.schedulerWorkspacePath != "" {
				sb.WriteString("\n### Schedules\n")
				scheduleList, schedErr := iwm.schedulerFuncs.ListSchedules(ctx, iwm.schedulerWorkspacePath)
				if schedErr != nil {
					sb.WriteString(fmt.Sprintf("_Could not load schedules: %v_\n", schedErr))
				} else if strings.TrimSpace(scheduleList) == "" {
					sb.WriteString("No schedules configured.\n")
				} else {
					sb.WriteString(scheduleList)
					sb.WriteString("\n")
				}
			}

			return sb.String(), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register get_workflow_config tool: %v", err))
	}

	if err := mcpAgent.RegisterCustomTool(
		"request_workflow_folder_access",
		"Create a pending attached-folder request in the Workflow toolbar. This never grants access. If the user explicitly supplied an exact absolute host folder path, preserve it in path so the user can approve or deny that exact request; never infer a path. Without a supplied path, the user chooses one with the native picker. Use get_workflow_config afterward to confirm the approved grant.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"alias":  map[string]interface{}{"type": "string", "description": "Suggested short alias, for example rts-source."},
				"path":   map[string]interface{}{"type": "string", "description": "Optional exact absolute folder path explicitly supplied by the user. Never infer or invent this value."},
				"access": map[string]interface{}{"type": "string", "enum": []string{"read_only", "read_write"}},
				"reason": map[string]interface{}{"type": "string", "description": "Why the workflow needs this folder and what it will do there."},
			},
			"required": []string{"alias", "access", "reason"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			alias := strings.TrimSpace(fmt.Sprint(args["alias"]))
			access := strings.TrimSpace(fmt.Sprint(args["access"]))
			reason := strings.TrimSpace(fmt.Sprint(args["reason"]))
			requestedPath := ""
			if rawPath, ok := args["path"]; ok && rawPath != nil {
				requestedPath = strings.TrimSpace(fmt.Sprint(rawPath))
			}
			if alias == "" || reason == "" || (access != workflowtypes.FolderAccessReadOnly && access != workflowtypes.FolderAccessReadWrite) {
				return "Error: alias, reason, and access (read_only or read_write) are required.", nil
			}
			if requestedPath != "" {
				if strings.ContainsRune(requestedPath, '\x00') || !filepath.IsAbs(requestedPath) {
					return "Error: path must be an exact absolute folder path supplied by the user.", nil
				}
				requestedPath = filepath.Clean(requestedPath)
				if requestedPath == filepath.VolumeName(requestedPath)+string(filepath.Separator) {
					return "Error: a filesystem root cannot be requested.", nil
				}
			}
			content, readErr := iwm.controller.ReadWorkspaceFile(ctx, "workflow.json")
			if readErr != nil {
				return fmt.Sprintf("Failed to read workflow.json: %v", readErr), nil
			}
			now := time.Now().UTC()
			request := workflowtypes.WorkflowFolderAccessRequest{
				ID:            fmt.Sprintf("folder-request-%d", now.UnixNano()),
				Alias:         alias,
				RequestedPath: requestedPath,
				Access:        access,
				Reason:        reason,
				RequestedAt:   now.Format(time.RFC3339Nano),
			}
			updated, existing, updateErr := upsertWorkflowFolderAccessRequest([]byte(content), request)
			if updateErr != nil {
				return fmt.Sprintf("Unable to create folder request: %v", updateErr), nil
			}
			if !existing {
				if writeErr := iwm.controller.writeManagedWorkflowManifest(ctx, string(updated)); writeErr != nil {
					return fmt.Sprintf("Unable to save folder request: %v", writeErr), nil
				}
			}
			if requestedPath != "" {
				return fmt.Sprintf("Pending folder request %q for %q is now visible in Workflow toolbar → Attached folders. No access has been granted yet. The user can approve or deny the displayed %s permission. After approval, call get_workflow_config to confirm the grant.", alias, requestedPath, access), nil
			}
			return fmt.Sprintf("Pending folder request %q is now visible in Workflow toolbar → Attached folders. No access has been granted yet. The user must choose the folder and approve %s access. After approval, call get_workflow_config to confirm the grant.", alias, access), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register request_workflow_folder_access tool: %v", err))
	}

	// === Tool: update_workflow_config ===
	// Tool: update_workflow_config — add/remove MCP servers, skills, secrets, and workflow-level knobs
	browserModeValues := []interface{}{"none", "auto", "headless", "cdp"}
	browserModeDescription := "Workflow-level browser automation mode. 'none' disables browser capability; 'auto' uses the operator's shared Chrome through CDP when reachable and otherwise headless agent_browser; 'headless' forces isolated agent_browser; 'cdp' requires the operator's shared Chrome. For steps that actually drive the browser, also set update_step_config(enabled_custom_tools=[...]) and enabled_skills=['agent-browser']."
	cdpPortsDescription := "Optional specialized CDP browser ports (maximum 4). Use multiple ports only when one workflow needs independent Chrome profiles/login identities, such as testing two accounts on the same site. Each port must be launched with a distinct --user-data-dir. Normal concurrent workflows should share the default single CDP browser."
	if !browser.CDPEnabled() {
		browserModeValues = []interface{}{"none", "auto", "headless"}
		browserModeDescription = "Workflow-level browser automation mode for this server deployment. CDP is disabled: 'auto' and 'headless' both use managed headless Chromium; 'none' disables browser capability. The 'cdp' mode is unavailable and cannot be forced."
		cdpPortsDescription = "Unavailable on this server deployment because CDP is disabled. Omit this field."
	}
	if iwm.isRunModeRestricted() {
		// PLAT-262: skip update_workflow_config registration for read-only access
	} else if err := mcpAgent.RegisterCustomTool(
		"update_workflow_config",
		"Update workflow configuration: add/remove MCP servers, workflow-level tool allowlist entries, skills and secrets; save workflow-scoped notification content instructions and one Gmail notification sender; configure a one-way Slack Incoming Webhook by encrypted secret name; set browser mode and optional specialized multi-profile CDP ports; set run retention; disable selected Pulse reviewers; or activate an owner-approved Strategy Auditor + Goal Advisor specialization. Use get_workflow_config to inspect current workflow settings and list_skills to discover installed skill folder names. Most changes take effect immediately for subsequent steps; changing cdp_ports, Pulse reviewer selection, or Slack webhook configuration takes effect on the next workflow execution.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"knowledgebase_sources": map[string]interface{}{
					"type": "array", "maxItems": 20, "description": "Replace the complete shared KB source list this workflow attaches. Use stable workflow IDs and unique lowercase aliases. Empty list detaches all. A \"read\" source only needs the source to be readable by every owner/reader of this workflow. A \"write\" source ALSO requires the source workflow's own owner to have explicitly named this workflow's ID in ITS kb_write_grants -- set that from the source workflow's own config, not from here; a write source that isn't explicitly granted resolves as unavailable. \"write\" implies read too (see notes/_index.json before contributing). Inspect workflow.json via shell or workflow discovery for IDs; never use guessed paths. Builder, eligible steps and reviewers get WORKFLOW_KB_<ALIAS> shell access; write access is additionally scoped to steps whose own knowledgebase write access is granted, and confined to the source's notes/ folder only.",
					"items": map[string]interface{}{"type": "object", "additionalProperties": false, "properties": map[string]interface{}{"workflow_id": map[string]interface{}{"type": "string"}, "alias": map[string]interface{}{"type": "string"}, "access": map[string]interface{}{"type": "string", "enum": []string{"read", "write"}}}, "required": []string{"workflow_id", "alias", "access"}}},

				"kb_write_grants": map[string]interface{}{
					"type": "array", "maxItems": 50, "description": "Replace the complete list of other workflows' IDs permitted to write into THIS workflow's knowledgebase/notes/ via their own \"write\" knowledgebase_source. This is the consent step a consumer workflow's write source depends on -- granting here does nothing until the named workflow actually attaches this workflow with access:\"write\". Empty list revokes all external write access. Does not affect read sharing, which is governed by ordinary owner/reader overlap, not this list.",
					"items": map[string]interface{}{"type": "string"}},

				"add_servers": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "MCP server names to add to the workflow",
				},
				"remove_servers": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "MCP server names to remove from the workflow",
				},
				"add_tools": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Workflow-level MCP tool allowlist entries to add. Format: 'server:tool_name' or 'server:*'. The server must already be selected with add_servers. This is for MCP servers, not workspace custom tools; use update_step_config(enabled_custom_tools=...) for workspace_advanced/workspace_browser/human_tools.",
				},
				"remove_tools": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Workflow-level MCP tool allowlist entries to remove. Format: 'server:tool_name' or 'server:*'.",
				},
				"add_skills": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Skill folder names to add to the workflow (use list_skills to see installed skills)",
				},
				"remove_skills": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Skill folder names to remove from the workflow",
				},
				"add_secrets": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Secret names to attach to the workflow. Each name MUST already have a stored value — either a GLOBAL_SECRET_* env var or a workflow secret (store via set_workflow_secret) — otherwise the request is rejected. Attaching only wires the name: runtime injects $SECRET_<NAME> with the looked-up value. Use list_secrets to see what's available.",
				},
				"remove_secrets": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Secret names to remove/disable from the workflow",
				},
				"slack_webhook_secret_name": map[string]interface{}{
					"type":        "string",
					"description": "Name of an existing encrypted secret containing a complete Slack Incoming Webhook URL. This converts the credential into backend-only notification configuration and removes it from selected_secrets, selected_global_secret_names, and SECRET_* injection. notify_user sends a rich Block Kit card to it automatically and applies the change in the current builder turn; human_feedback never does because webhooks are one-way. Pass an empty string to disable workflow webhook delivery. Never put the URL itself in workflow.json or post to it directly.",
				},
				"run_notification_instructions": map[string]interface{}{
					"type":        "string",
					"maxLength":   2000,
					"description": "Owner-approved guidance for the workflow execution section of notifications: what happened, outputs, failures, goal movement, metrics, detail, tone, and emphasis. Stored in workflow.json capabilities.notifications.run_summary_instructions. Pass an empty string to clear it. It cannot change recipients, channels, permissions, schedules, plan behavior, or delivery configuration.",
				},
				"pulse_notification_instructions": map[string]interface{}{
					"type":        "string",
					"maxLength":   2000,
					"description": "Owner-approved guidance for the Pulse section of notifications: reviews, bugs, fixes, recommendations, decisions, backup/publish state, and next actions. Stored in workflow.json capabilities.notifications.pulse_summary_instructions. Pass an empty string to clear it. It cannot change recipients, channels, permissions, schedules, plan behavior, or delivery configuration.",
				},
				"run_notification_channels": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string", "enum": []string{"gmail", "slack", "whatsapp"}},
					"minItems":    1,
					"description": "Channels for the workflow run summary only. The backend enforces this allowlist. Omit to retain the current/default routing.",
				},
				"pulse_notification_channels": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string", "enum": []string{"gmail", "slack", "whatsapp"}},
					"minItems":    1,
					"description": "Channels for the Pulse review summary only. The backend enforces this allowlist. Omit to retain the current/default routing.",
				},
				"run_notification_slack_webhooks": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Which Slack CHANNEL(S) the workflow run summary posts to, as names of existing encrypted secrets that each hold a complete Slack Incoming Webhook URL. A webhook is bound to one channel when it is created and cannot be retargeted, so a different channel means a different webhook secret — store each with set_workflow_secret, then list its name here. Listing several posts the run summary to several channels. Stored in workflow.json capabilities.notifications.run_summary_slack_webhook_secret_names. Omit to leave unchanged; pass an empty array to fall back to the workflow's single slack_webhook_secret_name.",
				},
				"pulse_notification_slack_webhooks": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Which Slack CHANNEL(S) the Pulse review summary posts to, as encrypted-secret names holding Slack Incoming Webhook URLs. Use when Pulse activity belongs in a different channel than the run outcome. Stored in workflow.json capabilities.notifications.pulse_summary_slack_webhook_secret_names. Omit to leave unchanged; pass an empty array to fall back to the workflow's single slack_webhook_secret_name.",
				},
				"run_notification_recipients": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Email addresses the workflow run summary is sent TO, stored in workflow.json capabilities.notifications.run_summary_recipients and applied automatically to every run_summary send. This is the positive 'send here' list — set it when the user names who should receive this workflow's run emails. Omit to leave unchanged; pass an empty array to clear it and fall back to the account default recipient. Never blocks anything: the account-wide and per-workflow denylists are still applied on top, so a blocked address listed here is skipped rather than unblocked.",
				},
				"pulse_notification_recipients": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Email addresses the Pulse review summary is sent TO, stored in workflow.json capabilities.notifications.pulse_summary_recipients and applied automatically to every pulse_summary send. Use when Pulse findings should reach different people than the run outcome. Omit to leave unchanged; pass an empty array to clear it and fall back to the account default recipient. Denylists still apply on top.",
				},
				"pulse_pace": map[string]interface{}{
					"type":        "string",
					"enum":        []interface{}{"calm", "steady", "aggressive"},
					"description": "How hard Pulse pushes on the goal, only when the owner asks: calm (checks every 1-7 days, failures wait), steady (default, 6h-3d), aggressive (1-24h, failures wake it at once).",
				},
				"pulse_enabled": map[string]interface{}{
					"type":        "boolean",
					"description": "Turn the workflow's Pulse on or off, only when the owner asks. On: an agent owns the goal in soul/soul.md (needs that file). Off: the owner manages the workflow.",
				},
				"after_manual_run": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": false,
					"description":          "What happens after a full run started from a chat (not a schedule): backup saves workflow state when it changed, publish refreshes the published report when it changed, notify sends the run summary. Each schedule has its own after_run (create_schedule/update_schedule).",
					"properties": map[string]interface{}{
						"backup":  map[string]interface{}{"type": "boolean"},
						"publish": map[string]interface{}{"type": "boolean"},
						"notify":  map[string]interface{}{"type": "boolean"},
					},
				},
				"notification_gmail_connection_id": map[string]interface{}{
					"type":        "string",
					"description": "One enabled Gmail connection ID to send this workflow's notify_user emails FROM, for both run and Pulse summaries. Inspect available IDs with get_workflow_config. Stored in workflow.json capabilities.notifications.gmail_connection_id; clears legacy per-summary sender overrides. Omit to leave unchanged; pass an empty string to inherit the account default Gmail sender. This does not change recipients or the Gmail connection_id chosen by ordinary workflow and Builder email actions.",
				},

				"browser_mode": map[string]interface{}{
					"type":        "string",
					"enum":        browserModeValues,
					"description": browserModeDescription,
				},
				"cdp_ports": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 65535},
					"maxItems":    maxCDPPortsPerWorkflow,
					"uniqueItems": true,
					"description": cdpPortsDescription,
				},
				"run_retention_count": map[string]interface{}{
					"type":        "integer",
					"minimum":     1,
					"maximum":     maxRunRetentionCount,
					"description": "Number of completed run/eval folders to keep independently for plain Builder archives, saved-schedule -sched runs, and webhook -hook runs, excluding active iteration-0. Defaults to 10 when omitted. Raise this for workflows whose Pulse or Goal Advisor reviews need a wider evidence window.",
				},
				"pulse_disabled_review_modules": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string", "enum": []string{"technical_review", "architecture_review", "strategic_review"}},
					"uniqueItems": true,
					"description": "Replace the complete list of Pulse reviewers disabled by the workflow owner. Disabled reviewers are skipped in future full Pulse runs while their history remains visible. Use technical_review for Health, architecture_review for Architecture, and strategic_review for Strategy. Pass [] to enable all three. This does not turn Pulse itself off or change per-schedule off/basic/full policy.",
				},
				"disable_parallel_tool_execution": map[string]interface{}{
					"type":        "boolean",
					"description": "Workflow-level default (stored in execution_defaults, applies to EVERY step unless a step overrides it) forcing one tool call per turn. Use only when most steps in this workflow need strictly sequential tool calls (e.g. a workflow that is browser-driven throughout); prefer per-step tuning via update_step_config when only some steps need it. Pass null to clear the workflow-level default.",
				},
				"enabled_custom_tools": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Workflow-level optional workspace/custom tools (stored in execution_defaults, applies to EVERY step unless a step sets its own enabled_custom_tools). Execution agents already receive workspace_advanced plus human_feedback, notify_user, and create_human_input_request. Add only capabilities nearly every step needs and name additional human tools explicitly. Legacy human_tools:* is normalized to the same three-tool baseline. Prefer per-step tuning otherwise. Pass an empty array to clear the workflow-level additions.",
				},
				"advisor_specialization_approval_input_id": map[string]interface{}{
					"type":        "string",
					"description": "Activate the exact Strategy Auditor and Goal Advisor specialization text in an answered report_human_inputs decision whose id starts advisor-specialization- and whose selected option is activate. The two texts are resolved from the approved decision and written atomically to workflow.json; arbitrary replacement text is not accepted here.",
				},
			},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			var sb strings.Builder
			anyChanged := false

			// Helper to extract string array from args
			extractStringArray := func(key string) []string {
				val, ok := args[key]
				if !ok || val == nil {
					return nil
				}
				arr, ok := val.([]interface{})
				if !ok {
					return nil
				}
				result := make([]string, 0, len(arr))
				for _, v := range arr {
					if s, ok := v.(string); ok && s != "" {
						result = append(result, s)
					}
				}
				return result
			}

			// --- MCP Servers ---
			addServers := extractStringArray("add_servers")
			removeServers := extractStringArray("remove_servers")
			if len(addServers) > 0 || len(removeServers) > 0 {
				servers := iwm.controller.GetSelectedServers()
				result := make([]string, len(servers))
				copy(result, servers)
				changed := false

				if len(addServers) > 0 {
					existSet := make(map[string]bool, len(result))
					for _, s := range result {
						existSet[s] = true
					}
					for _, s := range addServers {
						if !existSet[s] {
							result = append(result, s)
							existSet[s] = true
							changed = true
						}
					}
				}

				if len(removeServers) > 0 {
					removeSet := make(map[string]bool, len(removeServers))
					for _, s := range removeServers {
						removeSet[s] = true
					}
					filtered := result[:0]
					for _, s := range result {
						if !removeSet[s] {
							filtered = append(filtered, s)
						} else {
							changed = true
						}
					}
					result = filtered
				}

				if changed {
					iwm.controller.SetSelectedServers(result)
					anyChanged = true
					sb.WriteString("### MCP Servers (updated)\n")
					if len(result) == 0 {
						sb.WriteString("No MCP servers configured.\n")
					} else {
						for _, s := range result {
							sb.WriteString(fmt.Sprintf("- %s\n", s))
						}
					}
					if len(removeServers) > 0 {
						removeSet := make(map[string]bool, len(removeServers))
						for _, s := range removeServers {
							removeSet[s] = true
						}
						selectedTools := iwm.controller.GetSelectedTools()
						filteredTools := selectedTools[:0]
						for _, tool := range selectedTools {
							serverName := tool
							if idx := strings.Index(tool, ":"); idx >= 0 {
								serverName = tool[:idx]
							}
							if !removeSet[serverName] {
								filteredTools = append(filteredTools, tool)
							}
						}
						if len(filteredTools) != len(selectedTools) {
							iwm.controller.SetSelectedTools(filteredTools)
							sb.WriteString("\nRemoved workflow-level tool allowlist entries for removed server(s).\n")
						}
					}
					logger.Info(fmt.Sprintf("Updated workflow MCP servers: %v", result))
				}
			}

			// --- Workflow-level MCP tool allowlist ---
			addTools := extractStringArray("add_tools")
			removeTools := extractStringArray("remove_tools")
			if len(addTools) > 0 || len(removeTools) > 0 {
				selectedServers := iwm.controller.GetSelectedServers()
				serverSet := make(map[string]bool, len(selectedServers))
				for _, s := range selectedServers {
					serverSet[s] = true
				}
				var invalidTools []string
				var missingServers []string
				for _, tool := range addTools {
					idx := strings.Index(tool, ":")
					if idx <= 0 || idx == len(tool)-1 {
						invalidTools = append(invalidTools, tool)
						continue
					}
					serverName := tool[:idx]
					if !serverSet[serverName] {
						missingServers = append(missingServers, serverName)
					}
				}
				if len(invalidTools) > 0 {
					return fmt.Sprintf("Error: selected tool entries must use 'server:tool_name' or 'server:*'. Invalid entries: %v.", invalidTools), nil
				}
				if len(missingServers) > 0 {
					return fmt.Sprintf("Error: selected tool entries reference server(s) not selected at workflow level: %v. Add the server first with update_workflow_config(add_servers=[...]).", uniqueStringsPreserveOrder(missingServers)), nil
				}

				tools := iwm.controller.GetSelectedTools()
				result := make([]string, len(tools))
				copy(result, tools)
				changed := false

				if len(addTools) > 0 {
					existSet := make(map[string]bool, len(result))
					for _, t := range result {
						existSet[t] = true
					}
					for _, t := range addTools {
						if !existSet[t] {
							result = append(result, t)
							existSet[t] = true
							changed = true
						}
					}
				}

				if len(removeTools) > 0 {
					removeSet := make(map[string]bool, len(removeTools))
					for _, t := range removeTools {
						removeSet[t] = true
					}
					filtered := result[:0]
					for _, t := range result {
						if !removeSet[t] {
							filtered = append(filtered, t)
						} else {
							changed = true
						}
					}
					result = filtered
				}

				if changed {
					iwm.controller.SetSelectedTools(result)
					anyChanged = true
					sb.WriteString("\n### MCP Tools (updated)\n")
					if len(result) == 0 {
						sb.WriteString("No workflow-level tool allowlist configured; selected servers expose all their tools unless step-level tools override them.\n")
					} else {
						for _, t := range result {
							sb.WriteString(fmt.Sprintf("- %s\n", t))
						}
					}
					logger.Info(fmt.Sprintf("Updated workflow MCP tools: %v", result))
				}
			}

			// --- Skills ---
			addSkills := extractStringArray("add_skills")
			removeSkills := extractStringArray("remove_skills")
			if len(addSkills) > 0 || len(removeSkills) > 0 {
				currentSkills := iwm.controller.GetSelectedSkills()
				result := make([]string, len(currentSkills))
				copy(result, currentSkills)
				changed := false

				if len(addSkills) > 0 {
					existSet := make(map[string]bool, len(result))
					for _, s := range result {
						existSet[s] = true
					}
					for _, s := range addSkills {
						if !existSet[s] {
							result = append(result, s)
							existSet[s] = true
							changed = true
						}
					}
				}

				if len(removeSkills) > 0 {
					removeSet := make(map[string]bool, len(removeSkills))
					for _, s := range removeSkills {
						removeSet[s] = true
					}
					filtered := result[:0]
					for _, s := range result {
						if !removeSet[s] {
							filtered = append(filtered, s)
						} else {
							changed = true
						}
					}
					result = filtered
				}

				if changed {
					iwm.controller.SetSelectedSkills(result)
					anyChanged = true
					sb.WriteString("\n### Skills (updated)\n")
					if len(result) == 0 {
						sb.WriteString("No skills configured.\n")
					} else {
						for _, s := range result {
							sb.WriteString(fmt.Sprintf("- %s\n", s))
						}
					}
					logger.Info(fmt.Sprintf("Updated workflow skills: %v", result))
				}
			}

			// --- Secrets ---
			addSecrets := extractStringArray("add_secrets")
			removeSecrets := extractStringArray("remove_secrets")
			if len(addSecrets) > 0 || len(removeSecrets) > 0 {
				currentSecrets := iwm.controller.GetSecrets()
				changed := false

				if len(addSecrets) > 0 {
					// Attaching a name without a stored value is a silent foot-gun: runtime
					// drops the empty entry and $SECRET_<NAME> ends up unset, masking the
					// real "no value stored" error with a downstream graceful-fallback.
					// Reject up front if any requested name has no value in workflow store,
					// user store, or global env. The caller must store the value first.
					availableNames := map[string]bool{}
					if iwm.listAvailableSecrets != nil {
						if names, listErr := iwm.listAvailableSecrets(ctx); listErr == nil {
							for _, n := range names {
								availableNames[n] = true
							}
						} else {
							logger.Warn(fmt.Sprintf("Could not list available secrets for validation: %v", listErr))
						}
					}
					var missing []string
					if len(availableNames) > 0 {
						for _, name := range addSecrets {
							if !availableNames[name] {
								missing = append(missing, name)
							}
						}
					}
					if len(missing) > 0 {
						return fmt.Sprintf(
							"Error: cannot attach secret(s) with no stored value: %v.\n\n"+
								"These names have no value in the workflow secret store or matching GLOBAL_SECRET_* env var. "+
								"Attaching them would set $SECRET_<NAME> to an empty string at runtime and silently break any step that reads them.\n\n"+
								"Fix:\n"+
								"  1. Store the value first: set_workflow_secret(name=\"%s\", value=\"<plaintext>\") for this workflow.\n"+
								"  2. Then re-run update_workflow_config(add_secrets=[...]).",
							missing, missing[0],
						), nil
					}

					existSet := make(map[string]bool, len(currentSecrets))
					for _, s := range currentSecrets {
						existSet[s.Name] = true
					}
					for _, name := range addSecrets {
						if !existSet[name] {
							currentSecrets = append(currentSecrets, orchestrator.SecretEntry{Name: name, Value: ""})
							existSet[name] = true
							changed = true
						}
					}
				}

				if len(removeSecrets) > 0 {
					removeSet := make(map[string]bool, len(removeSecrets))
					for _, s := range removeSecrets {
						removeSet[s] = true
					}
					filtered := currentSecrets[:0]
					for _, s := range currentSecrets {
						if !removeSet[s.Name] {
							filtered = append(filtered, s)
						} else {
							changed = true
						}
					}
					currentSecrets = filtered
				}

				if changed {
					// Resolve plaintext values for every attached name so the workshop shell's
					// SECRET_* env vars stay in sync with workflow.json mid-session. Without
					// this, add_secrets would silently give you an empty $SECRET_<NAME> in
					// the workshop's own execute_shell_command (step runs still see values
					// because their env is rebuilt per-step, but builder shell does not).
					if iwm.resolveSecretValues != nil {
						names := make([]string, 0, len(currentSecrets))
						for _, s := range currentSecrets {
							names = append(names, s.Name)
						}
						values := iwm.resolveSecretValues(ctx, names)
						for i, s := range currentSecrets {
							if v, ok := values[s.Name]; ok {
								currentSecrets[i].Value = v
							}
						}
					}

					iwm.controller.SetSecrets(currentSecrets)
					if iwm.workshopConfig != nil {
						cloned := make([]orchestrator.SecretEntry, len(currentSecrets))
						copy(cloned, currentSecrets)
						iwm.workshopConfig.Secrets = cloned
					}

					// Sync SECRET_* into the workshop shell's persistent env map, so
					// `execute_shell_command` from the builder agent picks up new keys
					// without a session restart. Also strips removed secrets.
					if envRef := iwm.controller.GetWorkspaceEnvRef(); envRef != nil {
						nameSet := make(map[string]bool, len(currentSecrets))
						iwm.controller.LockWorkspaceEnv()
						for _, s := range currentSecrets {
							if s.Name == "" {
								continue
							}
							nameSet[s.Name] = true
							envRef["SECRET_"+s.Name] = s.Value
						}
						for k := range envRef {
							if strings.HasPrefix(k, "SECRET_") && !nameSet[strings.TrimPrefix(k, "SECRET_")] {
								delete(envRef, k)
							}
						}
						iwm.controller.UnlockWorkspaceEnv()
						logger.Info(fmt.Sprintf("Refreshed workshop shell env with %d SECRET_* entries", len(currentSecrets)))
					}
					// The builder chat's own shell captured its environment at turn
					// start; push the change there too so the next shell command in
					// this same turn sees it (server A 2026-09-25: an attached
					// SENTRY_AUTH_TOKEN stayed unset until the user sent another message).
					notifySecretsAttached(iwm.secretsAttached, currentSecrets, removeSecrets)

					anyChanged = true
					sb.WriteString("\n### Secrets (updated)\n")
					if len(currentSecrets) == 0 {
						sb.WriteString("No secrets configured.\n")
					} else {
						for _, s := range currentSecrets {
							sb.WriteString(fmt.Sprintf("- %s\n", s.Name))
						}
					}
					logger.Info(fmt.Sprintf("Updated workflow secrets: %d entries", len(currentSecrets)))
				}
			}

			// --- Workflow-scoped notification content instructions ---
			runRaw, runProvided := args["run_notification_instructions"]
			pulseRaw, pulseProvided := args["pulse_notification_instructions"]
			runChannelsRaw, runChannelsProvided := args["run_notification_channels"]
			pulseChannelsRaw, pulseChannelsProvided := args["pulse_notification_channels"]
			runRecipientsRaw, runRecipientsProvided := args["run_notification_recipients"]
			pulseRecipientsRaw, pulseRecipientsProvided := args["pulse_notification_recipients"]
			senderRaw, senderProvided := args["notification_gmail_connection_id"]
			if (runProvided && runRaw != nil) || (pulseProvided && pulseRaw != nil) ||
				(runChannelsProvided && runChannelsRaw != nil) || (pulseChannelsProvided && pulseChannelsRaw != nil) ||
				(runRecipientsProvided && runRecipientsRaw != nil) || (pulseRecipientsProvided && pulseRecipientsRaw != nil) ||
				(senderProvided && senderRaw != nil) {
				parseInstructions := func(name string, raw interface{}, provided bool) (string, error) {
					if !provided || raw == nil {
						return "", nil
					}
					value, ok := raw.(string)
					if !ok {
						return "", fmt.Errorf("%s must be a string", name)
					}
					value = strings.TrimSpace(value)
					if len([]rune(value)) > 2000 {
						return "", fmt.Errorf("%s must be at most 2000 characters", name)
					}
					return value, nil
				}
				runInstructions, parseErr := parseInstructions("run_notification_instructions", runRaw, runProvided)
				if parseErr != nil {
					return "Error: " + parseErr.Error() + ".", nil
				}
				pulseInstructions, parseErr := parseInstructions("pulse_notification_instructions", pulseRaw, pulseProvided)
				if parseErr != nil {
					return "Error: " + parseErr.Error() + ".", nil
				}

				content, readErr := iwm.controller.ReadWorkspaceFile(ctx, "workflow.json")
				if readErr != nil {
					return fmt.Sprintf("Failed to read workflow.json: %v", readErr), nil
				}
				var manifest map[string]interface{}
				if parseErr := json.Unmarshal([]byte(content), &manifest); parseErr != nil {
					return fmt.Sprintf("Failed to parse workflow.json: %v", parseErr), nil
				}
				caps, _ := manifest["capabilities"].(map[string]interface{})
				if caps == nil {
					caps = make(map[string]interface{})
				}
				notifications, _ := caps["notifications"].(map[string]interface{})
				if notifications == nil {
					notifications = make(map[string]interface{})
				}
				legacyInstructions, _ := notifications["instructions"].(string)
				if !runProvided {
					runInstructions, _ = notifications["run_summary_instructions"].(string)
					if strings.TrimSpace(runInstructions) == "" {
						runInstructions = legacyInstructions
					}
				}
				if !pulseProvided {
					pulseInstructions, _ = notifications["pulse_summary_instructions"].(string)
					if strings.TrimSpace(pulseInstructions) == "" {
						pulseInstructions = legacyInstructions
					}
				}
				parseChannels := func(name string, raw interface{}, provided bool) ([]string, error) {
					if !provided || raw == nil {
						return nil, nil
					}
					values, ok := raw.([]interface{})
					if !ok {
						return nil, fmt.Errorf("%s must be an array", name)
					}
					allowed := map[string]bool{"gmail": true, "slack": true, "whatsapp": true}
					channels := make([]string, 0, len(values))
					seen := map[string]bool{}
					for _, rawValue := range values {
						value, ok := rawValue.(string)
						if !ok || !allowed[strings.ToLower(strings.TrimSpace(value))] {
							return nil, fmt.Errorf("%s contains an unsupported channel", name)
						}
						value = strings.ToLower(strings.TrimSpace(value))
						if !seen[value] {
							channels = append(channels, value)
							seen[value] = true
						}
					}
					if len(channels) == 0 {
						return nil, fmt.Errorf("%s must contain at least one channel", name)
					}
					return channels, nil
				}
				runChannels, parseErr := parseChannels("run_notification_channels", runChannelsRaw, runChannelsProvided)
				if parseErr != nil {
					return "Error: " + parseErr.Error() + ".", nil
				}
				pulseChannels, parseErr := parseChannels("pulse_notification_channels", pulseChannelsRaw, pulseChannelsProvided)
				if parseErr != nil {
					return "Error: " + parseErr.Error() + ".", nil
				}
				// Recipient lists are stored as-given minus obvious junk. An empty
				// array is a legitimate value here — it clears the list back to the
				// account default — so unlike channels it is not an error.
				parseRecipients := func(name string, raw interface{}, provided bool) ([]string, error) {
					if !provided || raw == nil {
						return nil, nil
					}
					values, ok := raw.([]interface{})
					if !ok {
						return nil, fmt.Errorf("%s must be an array of email addresses", name)
					}
					recipients := make([]string, 0, len(values))
					seen := map[string]bool{}
					for _, rawValue := range values {
						value, ok := rawValue.(string)
						if !ok {
							return nil, fmt.Errorf("%s must contain only email addresses", name)
						}
						for _, part := range strings.FieldsFunc(value, func(r rune) bool {
							return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
						}) {
							email := strings.ToLower(strings.TrimSpace(part))
							if email == "" || seen[email] {
								continue
							}
							if !strings.Contains(email, "@") {
								return nil, fmt.Errorf("%s contains %q, which is not an email address", name, part)
							}
							seen[email] = true
							recipients = append(recipients, email)
						}
					}
					return recipients, nil
				}
				runRecipients, parseErr := parseRecipients("run_notification_recipients", runRecipientsRaw, runRecipientsProvided)
				if parseErr != nil {
					return "Error: " + parseErr.Error() + ".", nil
				}
				pulseRecipients, parseErr := parseRecipients("pulse_notification_recipients", pulseRecipientsRaw, pulseRecipientsProvided)
				if parseErr != nil {
					return "Error: " + parseErr.Error() + ".", nil
				}
				var senderID string
				if senderProvided && senderRaw != nil {
					var ok bool
					senderID, ok = senderRaw.(string)
					if !ok {
						return "Error: notification_gmail_connection_id must be a string.", nil
					}
					senderID = strings.TrimSpace(senderID)
					if senderID != "" {
						gmailSvc := services.GetGmailService()
						if gmailSvc == nil {
							return "Error: Gmail connections are unavailable; cannot validate notification sender.", nil
						}
						conn, found := gmailSvc.GetConnection(senderID)
						if !found || !conn.Enabled {
							return "Error: Notify sender must be an enabled Gmail connection.", nil
						}
					}
				}
				delete(notifications, "instructions")
				for key, value := range map[string]string{
					"run_summary_instructions":   strings.TrimSpace(runInstructions),
					"pulse_summary_instructions": strings.TrimSpace(pulseInstructions),
				} {
					if value == "" {
						delete(notifications, key)
					} else {
						notifications[key] = value
					}
				}
				if runChannelsProvided {
					notifications["run_summary_channels"] = runChannels
				}
				if pulseChannelsProvided {
					notifications["pulse_summary_channels"] = pulseChannels
				}
				// An explicitly emptied list is stored as "no override" by removing
				// the key, which is what falls back to the account default.
				if runRecipientsProvided {
					if len(runRecipients) == 0 {
						delete(notifications, "run_summary_recipients")
					} else {
						notifications["run_summary_recipients"] = runRecipients
					}
				}
				if pulseRecipientsProvided {
					if len(pulseRecipients) == 0 {
						delete(notifications, "pulse_summary_recipients")
					} else {
						notifications["pulse_summary_recipients"] = pulseRecipients
					}
				}
				if senderProvided && senderRaw != nil {
					if senderID == "" {
						delete(notifications, "gmail_connection_id")
					} else {
						notifications["gmail_connection_id"] = senderID
					}
					delete(notifications, "run_summary_gmail_connection_ids")
					delete(notifications, "pulse_summary_gmail_connection_ids")
				}
				if len(notifications) == 0 {
					delete(caps, "notifications")
				} else {
					caps["notifications"] = notifications
				}
				manifest["capabilities"] = caps
				manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
				updated, marshalErr := json.MarshalIndent(manifest, "", "  ")
				if marshalErr != nil {
					return fmt.Sprintf("Failed to marshal workflow.json: %v", marshalErr), nil
				}
				if writeErr := iwm.controller.writeManagedWorkflowManifest(ctx, string(updated)); writeErr != nil {
					return fmt.Sprintf("Failed to write workflow.json: %v", writeErr), nil
				}

				anyChanged = true
				sb.WriteString("\n### Notification Instructions (updated)\nSaved separate workflow run and Pulse review content preferences in workflow.json.\n")
				if runRecipientsProvided {
					sb.WriteString(fmt.Sprintf("- Run summary recipients: %s\n", notificationRecipientSummary(runRecipients)))
				}
				if pulseRecipientsProvided {
					sb.WriteString(fmt.Sprintf("- Pulse summary recipients: %s\n", notificationRecipientSummary(pulseRecipients)))
				}
				if senderProvided && senderRaw != nil {
					if senderID == "" {
						sb.WriteString("- Gmail notification sender: account default\n")
					} else {
						sb.WriteString(fmt.Sprintf("- Gmail notification sender connection ID: %s\n", senderID))
					}
				}
				logger.Info(fmt.Sprintf("Updated workflow notification instructions: run_configured=%v pulse_configured=%v run_recipients=%d pulse_recipients=%d", strings.TrimSpace(runInstructions) != "", strings.TrimSpace(pulseInstructions) != "", len(runRecipients), len(pulseRecipients)))
			}

			// --- Pulse pace (owner, 2026-10-08) ---
			if raw, provided := args["pulse_pace"]; provided && raw != nil {
				pace, ok := raw.(string)
				pace = strings.ToLower(strings.TrimSpace(pace))
				if !ok || (pace != "calm" && pace != "steady" && pace != "aggressive") {
					return "Error: pulse_pace must be calm, steady or aggressive.", nil
				}
				content, readErr := iwm.controller.ReadWorkspaceFile(ctx, "workflow.json")
				if readErr != nil {
					return fmt.Sprintf("Failed to read workflow.json: %v", readErr), nil
				}
				var manifest map[string]interface{}
				if parseErr := json.Unmarshal([]byte(content), &manifest); parseErr != nil {
					return fmt.Sprintf("Failed to parse workflow.json: %v", parseErr), nil
				}
				pulse, _ := manifest["pulse"].(map[string]interface{})
				if pulse == nil {
					pulse = map[string]interface{}{}
				}
				pulse["pace"] = pace
				manifest["pulse"] = pulse
				manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
				updated, marshalErr := json.MarshalIndent(manifest, "", "  ")
				if marshalErr != nil {
					return fmt.Sprintf("Failed to marshal workflow.json: %v", marshalErr), nil
				}
				if writeErr := iwm.controller.writeManagedWorkflowManifest(ctx, string(updated)); writeErr != nil {
					return fmt.Sprintf("Failed to write workflow.json: %v", writeErr), nil
				}
				anyChanged = true
				sb.WriteString(fmt.Sprintf("\n### Pulse pace (updated)\nPulse pace is %s.\n", pace))
			}

			// --- Pulse on or off (owner, 2026-10-08) ---
			if raw, provided := args["pulse_enabled"]; provided && raw != nil {
				enabled, ok := raw.(bool)
				if !ok {
					return "Error: pulse_enabled must be true or false.", nil
				}
				if enabled {
					if soul, err := iwm.controller.ReadWorkspaceFile(ctx, "soul/soul.md"); err != nil || strings.TrimSpace(soul) == "" {
						return "Error: Pulse needs the goal in soul/soul.md first. Help the owner write it (objective, the number that shows progress, a target and date), then turn Pulse on.", nil
					}
				}
				content, readErr := iwm.controller.ReadWorkspaceFile(ctx, "workflow.json")
				if readErr != nil {
					return fmt.Sprintf("Failed to read workflow.json: %v", readErr), nil
				}
				var manifest map[string]interface{}
				if parseErr := json.Unmarshal([]byte(content), &manifest); parseErr != nil {
					return fmt.Sprintf("Failed to parse workflow.json: %v", parseErr), nil
				}
				pulse, _ := manifest["pulse"].(map[string]interface{})
				if pulse == nil {
					pulse = map[string]interface{}{}
				}
				pulse["enabled"] = enabled
				manifest["pulse"] = pulse
				manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
				updated, marshalErr := json.MarshalIndent(manifest, "", "  ")
				if marshalErr != nil {
					return fmt.Sprintf("Failed to marshal workflow.json: %v", marshalErr), nil
				}
				if writeErr := iwm.controller.writeManagedWorkflowManifest(ctx, string(updated)); writeErr != nil {
					return fmt.Sprintf("Failed to write workflow.json: %v", writeErr), nil
				}
				anyChanged = true
				if enabled {
					sb.WriteString("\n### Pulse (updated)\nPulse is on: it owns the goal in soul/soul.md and starts with a goal check.\n")
				} else {
					sb.WriteString("\n### Pulse (updated)\nPulse is off: the owner manages the workflow; Pulse's history and memory are kept.\n")
				}
			}

			// --- After a manual run: backup, publish, notify (PLAT-697 phase 0) ---
			if raw, provided := args["after_manual_run"]; provided && raw != nil {
				opts, ok := raw.(map[string]interface{})
				if !ok {
					return "Error: after_manual_run must be an object {backup, publish, notify}.", nil
				}
				content, readErr := iwm.controller.ReadWorkspaceFile(ctx, "workflow.json")
				if readErr != nil {
					return fmt.Sprintf("Failed to read workflow.json: %v", readErr), nil
				}
				var manifest map[string]interface{}
				if parseErr := json.Unmarshal([]byte(content), &manifest); parseErr != nil {
					return fmt.Sprintf("Failed to parse workflow.json: %v", parseErr), nil
				}
				current, _ := manifest["after_manual_run"].(map[string]interface{})
				next := map[string]interface{}{"backup": false, "publish": false, "notify": false}
				for key := range next {
					if v, ok := current[key].(bool); ok {
						next[key] = v
					}
					if v, given := opts[key]; given {
						b, isBool := v.(bool)
						if !isBool {
							return fmt.Sprintf("Error: after_manual_run.%s must be true or false.", key), nil
						}
						next[key] = b
					}
				}
				manifest["after_manual_run"] = next
				manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
				updated, marshalErr := json.MarshalIndent(manifest, "", "  ")
				if marshalErr != nil {
					return fmt.Sprintf("Failed to marshal workflow.json: %v", marshalErr), nil
				}
				if writeErr := iwm.controller.writeManagedWorkflowManifest(ctx, string(updated)); writeErr != nil {
					return fmt.Sprintf("Failed to write workflow.json: %v", writeErr), nil
				}
				anyChanged = true
				sb.WriteString(fmt.Sprintf("\n### After a manual run (updated)\nbackup=%v publish=%v notify=%v\n", next["backup"], next["publish"], next["notify"]))
			}

			// --- Workflow-scoped one-way Slack webhook ---
			if raw, provided := args["slack_webhook_secret_name"]; provided && raw != nil {
				secretName, isString := raw.(string)
				if !isString {
					return "Error: slack_webhook_secret_name must be a string.", nil
				}
				secretName = strings.TrimSpace(secretName)
				var secretValue string
				if secretName != "" {
					if iwm.resolveSecretValues != nil {
						secretValue = iwm.resolveSecretValues(ctx, []string{secretName})[secretName]
					}
					if strings.TrimSpace(secretValue) == "" {
						for _, secret := range iwm.controller.GetSecrets() {
							if secret.Name == secretName {
								secretValue = secret.Value
								break
							}
						}
					}
					if strings.TrimSpace(secretValue) == "" {
						return fmt.Sprintf("Error: Slack webhook secret %q has no stored value. Save the full Incoming Webhook URL with set_workflow_secret first.", secretName), nil
					}
					if validateErr := services.ValidateSlackIncomingWebhookURL(secretValue); validateErr != nil {
						return fmt.Sprintf("Error: secret %q is not a valid Slack Incoming Webhook URL: %v", secretName, validateErr), nil
					}
				}

				content, readErr := iwm.controller.ReadWorkspaceFile(ctx, "workflow.json")
				if readErr != nil {
					return fmt.Sprintf("Failed to read workflow.json: %v", readErr), nil
				}
				var manifest map[string]interface{}
				if parseErr := json.Unmarshal([]byte(content), &manifest); parseErr != nil {
					return fmt.Sprintf("Failed to parse workflow.json: %v", parseErr), nil
				}
				caps, _ := manifest["capabilities"].(map[string]interface{})
				if caps == nil {
					caps = make(map[string]interface{})
				}
				if secretName == "" {
					if notifications, ok := caps["notifications"].(map[string]interface{}); ok {
						delete(notifications, "slack_webhook_secret_name")
						if len(notifications) == 0 {
							delete(caps, "notifications")
						} else {
							caps["notifications"] = notifications
						}
					}
				} else {
					notifications, _ := caps["notifications"].(map[string]interface{})
					if notifications == nil {
						notifications = make(map[string]interface{})
					}
					notifications["slack_webhook_secret_name"] = secretName
					caps["notifications"] = notifications
				}
				// A webhook referenced by Notifications is backend-only. Remove any
				// legacy/auto-attached copies from both secret selection lists so
				// future builders and workflow steps never receive SECRET_<NAME>.
				if secretName != "" {
					for _, key := range []string{"selected_secrets", "selected_global_secret_names"} {
						if rawSelected, ok := caps[key].([]interface{}); ok {
							filtered := make([]interface{}, 0, len(rawSelected))
							for _, entry := range rawSelected {
								if name, _ := entry.(string); strings.TrimSpace(name) != secretName {
									filtered = append(filtered, entry)
								}
							}
							caps[key] = filtered
						}
					}
				}
				manifest["capabilities"] = caps
				manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
				updated, marshalErr := json.MarshalIndent(manifest, "", "  ")
				if marshalErr != nil {
					return fmt.Sprintf("Failed to marshal workflow.json: %v", marshalErr), nil
				}
				if writeErr := iwm.controller.writeManagedWorkflowManifest(ctx, string(updated)); writeErr != nil {
					return fmt.Sprintf("Failed to write workflow.json: %v", writeErr), nil
				}

				if secretName != "" {
					currentSecrets := iwm.controller.GetSecrets()
					filtered := make([]orchestrator.SecretEntry, 0, len(currentSecrets))
					for _, secret := range currentSecrets {
						if strings.TrimSpace(secret.Name) != secretName {
							filtered = append(filtered, secret)
						}
					}
					iwm.controller.SetSecrets(filtered)
					if iwm.workshopConfig != nil {
						iwm.workshopConfig.Secrets = append([]orchestrator.SecretEntry(nil), filtered...)
					}
					if envRef := iwm.controller.GetWorkspaceEnvRef(); envRef != nil {
						iwm.controller.LockWorkspaceEnv()
						delete(envRef, "SECRET_"+secretName)
						iwm.controller.UnlockWorkspaceEnv()
					}
				}
				// Refresh the current builder turn immediately. The agent still never
				// sees the URL; only notify_user's destination context receives it.
				if destination, ok := ctx.Value(virtualtools.BotNotificationDestinationKey).(*services.NotificationDestination); ok && destination != nil {
					if secretName == "" {
						destination.SlackWebhook = nil
					} else {
						destination.SlackWebhook = &services.SlackWebhookDest{SecretName: secretName, URL: secretValue}
					}
				}
				var currentWebhook *services.SlackWebhookDest
				if secretName != "" {
					currentWebhook = &services.SlackWebhookDest{SecretName: secretName, URL: secretValue}
				}
				virtualtools.UpdateSessionSlackWebhook(iwm.sessionID, currentWebhook)

				anyChanged = true
				if secretName == "" {
					sb.WriteString("\n### Slack Incoming Webhook (disabled)\nWorkflow notify_user calls no longer send to a dedicated Slack webhook. Interactive Slack bot behavior is unchanged.\n")
				} else {
					sb.WriteString(fmt.Sprintf("\n### Slack Incoming Webhook (configured)\n- Encrypted backend-only secret: %s\n- Applies immediately to notify_user in this builder turn and to future workflow runs. notify_user renders rich Block Kit by default; the agent never receives the URL. It is one-way and is not used for human_feedback.\n", secretName))
				}
				logger.Info(fmt.Sprintf("Updated workflow Slack webhook secret reference: configured=%v", secretName != ""))
			}

			// --- Per-summary Slack channels (one webhook == one channel) ---
			runWebhooksRaw, runWebhooksProvided := args["run_notification_slack_webhooks"]
			pulseWebhooksRaw, pulseWebhooksProvided := args["pulse_notification_slack_webhooks"]
			if (runWebhooksProvided && runWebhooksRaw != nil) || (pulseWebhooksProvided && pulseWebhooksRaw != nil) {
				// Validate before writing anything: a name with no stored value, or
				// one holding something that is not a webhook URL, would persist as a
				// channel that silently never receives a message.
				resolveWebhookSecret := func(name string) (string, error) {
					var value string
					if iwm.resolveSecretValues != nil {
						value = iwm.resolveSecretValues(ctx, []string{name})[name]
					}
					if strings.TrimSpace(value) == "" {
						for _, secret := range iwm.controller.GetSecrets() {
							if secret.Name == name {
								value = secret.Value
								break
							}
						}
					}
					if strings.TrimSpace(value) == "" {
						return "", fmt.Errorf("Slack webhook secret %q has no stored value. Save the full Incoming Webhook URL with set_workflow_secret first", name)
					}
					if validateErr := services.ValidateSlackIncomingWebhookURL(value); validateErr != nil {
						return "", fmt.Errorf("secret %q is not a valid Slack Incoming Webhook URL: %w", name, validateErr)
					}
					return value, nil
				}
				parseWebhooks := func(argName string, raw interface{}, provided bool) ([]string, map[string]string, error) {
					if !provided || raw == nil {
						return nil, nil, nil
					}
					values, ok := raw.([]interface{})
					if !ok {
						return nil, nil, fmt.Errorf("%s must be an array of secret names", argName)
					}
					names := make([]string, 0, len(values))
					resolved := map[string]string{}
					seen := map[string]bool{}
					for _, rawValue := range values {
						name, ok := rawValue.(string)
						if !ok {
							return nil, nil, fmt.Errorf("%s must contain only secret names", argName)
						}
						name = strings.TrimSpace(name)
						if name == "" || seen[name] {
							continue
						}
						value, resolveErr := resolveWebhookSecret(name)
						if resolveErr != nil {
							return nil, nil, resolveErr
						}
						seen[name] = true
						names = append(names, name)
						resolved[name] = value
					}
					return names, resolved, nil
				}
				runWebhookNames, runWebhookValues, parseErr := parseWebhooks("run_notification_slack_webhooks", runWebhooksRaw, runWebhooksProvided)
				if parseErr != nil {
					return "Error: " + parseErr.Error() + ".", nil
				}
				pulseWebhookNames, pulseWebhookValues, parseErr := parseWebhooks("pulse_notification_slack_webhooks", pulseWebhooksRaw, pulseWebhooksProvided)
				if parseErr != nil {
					return "Error: " + parseErr.Error() + ".", nil
				}

				content, readErr := iwm.controller.ReadWorkspaceFile(ctx, "workflow.json")
				if readErr != nil {
					return fmt.Sprintf("Failed to read workflow.json: %v", readErr), nil
				}
				var manifest map[string]interface{}
				if parseErr := json.Unmarshal([]byte(content), &manifest); parseErr != nil {
					return fmt.Sprintf("Failed to parse workflow.json: %v", parseErr), nil
				}
				caps, _ := manifest["capabilities"].(map[string]interface{})
				if caps == nil {
					caps = make(map[string]interface{})
				}
				notifications, _ := caps["notifications"].(map[string]interface{})
				if notifications == nil {
					notifications = make(map[string]interface{})
				}
				applyWebhookNames := func(key string, names []string, provided bool) {
					if !provided {
						return
					}
					if len(names) == 0 {
						delete(notifications, key)
						return
					}
					notifications[key] = names
				}
				applyWebhookNames("run_summary_slack_webhook_secret_names", runWebhookNames, runWebhooksProvided)
				applyWebhookNames("pulse_summary_slack_webhook_secret_names", pulseWebhookNames, pulseWebhooksProvided)
				if len(notifications) == 0 {
					delete(caps, "notifications")
				} else {
					caps["notifications"] = notifications
				}

				// Same backend-only guarantee the single webhook gets: a credential
				// referenced by notifications must never reach the agent as SECRET_*.
				backendOnly := append(append([]string{}, runWebhookNames...), pulseWebhookNames...)
				if len(backendOnly) > 0 {
					blocked := map[string]bool{}
					for _, name := range backendOnly {
						blocked[name] = true
					}
					for _, key := range []string{"selected_secrets", "selected_global_secret_names"} {
						if rawSelected, ok := caps[key].([]interface{}); ok {
							filtered := make([]interface{}, 0, len(rawSelected))
							for _, entry := range rawSelected {
								if name, _ := entry.(string); !blocked[strings.TrimSpace(name)] {
									filtered = append(filtered, entry)
								}
							}
							caps[key] = filtered
						}
					}
				}
				manifest["capabilities"] = caps
				manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
				updated, marshalErr := json.MarshalIndent(manifest, "", "  ")
				if marshalErr != nil {
					return fmt.Sprintf("Failed to marshal workflow.json: %v", marshalErr), nil
				}
				if writeErr := iwm.controller.writeManagedWorkflowManifest(ctx, string(updated)); writeErr != nil {
					return fmt.Sprintf("Failed to write workflow.json: %v", writeErr), nil
				}

				if len(backendOnly) > 0 {
					blocked := map[string]bool{}
					for _, name := range backendOnly {
						blocked[name] = true
					}
					currentSecrets := iwm.controller.GetSecrets()
					filtered := make([]orchestrator.SecretEntry, 0, len(currentSecrets))
					for _, secret := range currentSecrets {
						if !blocked[strings.TrimSpace(secret.Name)] {
							filtered = append(filtered, secret)
						}
					}
					iwm.controller.SetSecrets(filtered)
					if iwm.workshopConfig != nil {
						iwm.workshopConfig.Secrets = append([]orchestrator.SecretEntry(nil), filtered...)
					}
					if envRef := iwm.controller.GetWorkspaceEnvRef(); envRef != nil {
						iwm.controller.LockWorkspaceEnv()
						for name := range blocked {
							delete(envRef, "SECRET_"+name)
						}
						iwm.controller.UnlockWorkspaceEnv()
					}
				}

				// Apply to the live builder turn so a test notify_user right after
				// this call already posts to the newly chosen channels.
				toWebhookDests := func(names []string, values map[string]string) []services.SlackWebhookDest {
					var dests []services.SlackWebhookDest
					for _, name := range names {
						dests = append(dests, services.SlackWebhookDest{SecretName: name, URL: values[name]})
					}
					return dests
				}
				if destination, ok := ctx.Value(virtualtools.BotNotificationDestinationKey).(*services.NotificationDestination); ok && destination != nil {
					if runWebhooksProvided {
						destination.RunSummaryWebhooks = toWebhookDests(runWebhookNames, runWebhookValues)
					}
					if pulseWebhooksProvided {
						destination.PulseSummaryWebhooks = toWebhookDests(pulseWebhookNames, pulseWebhookValues)
					}
				}

				anyChanged = true
				sb.WriteString("\n### Slack channels per summary (updated)\n")
				if runWebhooksProvided {
					sb.WriteString(fmt.Sprintf("- Workflow run summary channels: %s\n", notificationWebhookSummary(runWebhookNames)))
				}
				if pulseWebhooksProvided {
					sb.WriteString(fmt.Sprintf("- Pulse review summary channels: %s\n", notificationWebhookSummary(pulseWebhookNames)))
				}
				sb.WriteString("Each webhook is one Slack channel. The agent never receives these URLs; the backend posts through them by notification_kind.\n")
				logger.Info(fmt.Sprintf("Updated per-summary Slack webhooks: run=%d pulse=%d", len(runWebhookNames), len(pulseWebhookNames)))
			}

			// --- Browser mode ---
			if raw, ok := args["browser_mode"]; ok && raw != nil {
				mode, _ := raw.(string)
				mode = strings.ToLower(strings.TrimSpace(mode))
				if mode == "cdp" && !browser.CDPEnabled() {
					return "Error: CDP is disabled for this server deployment. Use browser_mode='auto', 'headless', or 'none'.", nil
				}
				validModes := map[string]bool{"none": true, "auto": true, "headless": true, "cdp": true}
				if !validModes[mode] {
					return "Error: browser_mode must be one of: none, auto, headless, cdp.", nil
				}
				iwm.controller.SetBrowserMode(mode)
				if mode != "cdp" && mode != "auto" {
					iwm.controller.SetCdpPorts(nil)
				}
				anyChanged = true
				sb.WriteString(fmt.Sprintf("\n### Browser Mode (updated)\n- %s\n", mode))
				logger.Info(fmt.Sprintf("Updated workflow browser_mode=%s", mode))
			}

			// --- Specialized multi-profile CDP ports ---
			if raw, ok := args["cdp_ports"]; ok && raw != nil {
				if !browser.CDPEnabled() {
					return "Error: cdp_ports is unavailable because CDP is disabled for this server deployment.", nil
				}
				arr, ok := raw.([]interface{})
				if !ok {
					return "Error: cdp_ports must be an array of integer ports.", nil
				}
				if len(arr) > maxCDPPortsPerWorkflow {
					return fmt.Sprintf("Error: cdp_ports supports at most %d ports.", maxCDPPortsPerWorkflow), nil
				}
				ports := make([]int, 0, len(arr))
				seen := make(map[int]bool, len(arr))
				for _, item := range arr {
					value, ok := item.(float64)
					if !ok || value != float64(int(value)) || value < 1 || value > 65535 {
						return "Error: every cdp_ports entry must be an integer between 1 and 65535.", nil
					}
					port := int(value)
					if seen[port] {
						return fmt.Sprintf("Error: duplicate cdp_ports entry %d.", port), nil
					}
					seen[port] = true
					ports = append(ports, port)
				}
				mode := strings.ToLower(strings.TrimSpace(iwm.controller.GetBrowserMode()))
				if len(ports) > 0 && mode != "cdp" && mode != "auto" {
					return "Error: non-empty cdp_ports requires browser_mode='cdp' or browser_mode='auto'. Set both in the same update_workflow_config call if needed.", nil
				}
				iwm.controller.SetCdpPorts(ports)
				anyChanged = true
				sb.WriteString(fmt.Sprintf("\n### CDP Profiles (updated)\n- Authorized ports: %v\n- Each port must use a distinct Chrome --user-data-dir. The change applies to the next workflow execution.\n", ports))
				logger.Info(fmt.Sprintf("Updated workflow cdp_ports=%v", ports))
			}

			// --- Run Retention ---
			if raw, ok := args["run_retention_count"]; ok && raw != nil {
				parseRetention := func(raw interface{}) (int, bool) {
					switch v := raw.(type) {
					case int:
						return v, true
					case int64:
						return int(v), true
					case float64:
						if v == float64(int(v)) {
							return int(v), true
						}
					case json.Number:
						if n, err := v.Int64(); err == nil {
							return int(n), true
						}
					}
					return 0, false
				}

				count, ok := parseRetention(raw)
				if !ok || count < 1 || count > maxRunRetentionCount {
					return fmt.Sprintf("Error: run_retention_count must be an integer between 1 and %d.", maxRunRetentionCount), nil
				}

				content, err := iwm.controller.ReadWorkspaceFile(ctx, "workflow.json")
				if err != nil {
					return fmt.Sprintf("Failed to read workflow.json: %v", err), nil
				}
				var manifest map[string]interface{}
				if err := json.Unmarshal([]byte(content), &manifest); err != nil {
					return fmt.Sprintf("Failed to parse workflow.json: %v", err), nil
				}
				manifest["run_retention_count"] = count
				manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)

				out, err := json.MarshalIndent(manifest, "", "  ")
				if err != nil {
					return fmt.Sprintf("Failed to marshal workflow.json: %v", err), nil
				}
				if err := iwm.controller.writeManagedWorkflowManifest(ctx, string(out)); err != nil {
					return fmt.Sprintf("Failed to write workflow.json: %v", err), nil
				}

				anyChanged = true
				sb.WriteString(fmt.Sprintf("\n### Run Retention (updated)\nKeeping %d completed run/eval folder(s) independently for Builder archives, saved schedules, and webhooks, excluding active iteration-0.\n", count))
				logger.Info(fmt.Sprintf("Updated workflow run_retention_count=%d", count))
			}

			// --- Execution Defaults (workflow-level overrides applied to every step) ---
			_, hasDisableParallel := args["disable_parallel_tool_execution"]
			_, hasEnabledCustomTools := args["enabled_custom_tools"]
			if hasDisableParallel || hasEnabledCustomTools {
				content, err := iwm.controller.ReadWorkspaceFile(ctx, "workflow.json")
				if err != nil {
					return fmt.Sprintf("Failed to read workflow.json: %v", err), nil
				}
				var manifest map[string]interface{}
				if err := json.Unmarshal([]byte(content), &manifest); err != nil {
					return fmt.Sprintf("Failed to parse workflow.json: %v", err), nil
				}
				execDefaults, _ := manifest["execution_defaults"].(map[string]interface{})
				if execDefaults == nil {
					execDefaults = map[string]interface{}{}
				}

				var summary []string

				if hasDisableParallel {
					raw := args["disable_parallel_tool_execution"]
					if raw == nil {
						delete(execDefaults, "disable_parallel_tool_execution")
						summary = append(summary, "disable_parallel_tool_execution: cleared")
					} else {
						disabled, isBool := raw.(bool)
						if !isBool {
							return "Error: disable_parallel_tool_execution must be a boolean (or null to clear).", nil
						}
						execDefaults["disable_parallel_tool_execution"] = disabled
						state := "disabled — steps emit one tool call per turn"
						if !disabled {
							state = "explicitly enabled — parallel tool calls allowed"
						}
						summary = append(summary, fmt.Sprintf("disable_parallel_tool_execution: %t (%s, applies to every step)", disabled, state))
					}
				}

				if hasEnabledCustomTools {
					tools := extractStringArray("enabled_custom_tools")
					if len(tools) == 0 {
						delete(execDefaults, "enabled_custom_tools")
						summary = append(summary, "enabled_custom_tools: cleared (steps use their own/preset tools)")
					} else {
						execDefaults["enabled_custom_tools"] = tools
						summary = append(summary, fmt.Sprintf("enabled_custom_tools: %v (applies to every step)", tools))
					}
				}

				if len(execDefaults) == 0 {
					delete(manifest, "execution_defaults")
				} else {
					manifest["execution_defaults"] = execDefaults
				}
				manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)

				out, err := json.MarshalIndent(manifest, "", "  ")
				if err != nil {
					return fmt.Sprintf("Failed to marshal workflow.json: %v", err), nil
				}
				if err := iwm.controller.writeManagedWorkflowManifest(ctx, string(out)); err != nil {
					return fmt.Sprintf("Failed to write workflow.json: %v", err), nil
				}

				anyChanged = true
				sb.WriteString("\n### Execution Defaults (updated)\n")
				for _, line := range summary {
					sb.WriteString(fmt.Sprintf("- %s\n", line))
				}
				logger.Info(fmt.Sprintf("Updated workflow execution_defaults: %v", summary))
			}

			if _, provided := args["pulse_disabled_review_modules"]; provided {
				disabled := extractStringArray("pulse_disabled_review_modules")
				allowed := map[string]bool{"technical_review": true, "architecture_review": true, "strategic_review": true}
				for _, module := range disabled {
					if !allowed[module] {
						return fmt.Sprintf("Error: unsupported Pulse reviewer %q; choose technical_review, architecture_review, or strategic_review.", module), nil
					}
				}
				content, err := iwm.controller.ReadWorkspaceFile(ctx, "workflow.json")
				if err != nil {
					return "", err
				}
				var manifest map[string]interface{}
				if err := json.Unmarshal([]byte(content), &manifest); err != nil {
					return fmt.Sprintf("Failed to parse workflow.json: %v", err), nil
				}
				pulse, _ := manifest["pulse"].(map[string]interface{})
				if pulse == nil {
					pulse = map[string]interface{}{}
				}
				if len(disabled) == 0 {
					delete(pulse, "disabled_review_modules")
				} else {
					pulse["disabled_review_modules"] = disabled
				}
				manifest["pulse"] = pulse
				manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
				updated, err := json.MarshalIndent(manifest, "", "  ")
				if err != nil {
					return "", err
				}
				if err := iwm.controller.writeManagedWorkflowManifest(ctx, string(updated)); err != nil {
					return "", err
				}
				anyChanged = true
				sb.WriteString(fmt.Sprintf("\n### Pulse reviewers (updated)\n- Disabled: %v\n- The change applies to future full Pulse runs; prior review history remains available.\n", disabled))
			}

			// --- Owner-approved advisor specialization ---
			if raw, ok := args["advisor_specialization_approval_input_id"]; ok && raw != nil {
				inputID, _ := raw.(string)
				inputID = strings.TrimSpace(inputID)
				if inputID == "" {
					return "Error: advisor_specialization_approval_input_id must be a non-empty advisor-specialization-* decision id.", nil
				}
				activated, _, err := iwm.activateApprovedAdvisorSpecialization(ctx, inputID)
				if err != nil {
					return fmt.Sprintf("Error: could not activate advisor specialization: %v", err), nil
				}
				// A retry of an already-active approval is an idempotent successful
				// operation, not a generic "no changes" error.
				anyChanged = true
				sb.WriteString(fmt.Sprintf("\n### Advisor specialization (version %d)\n- Approved decision: %s\n- Strategy Auditor and Goal Advisor now receive their separate workflow-specific lenses; the canonical reviewer contracts still take precedence.\n", activated.Version, activated.ApprovedInputID))
			}

			if raw, provided := args["knowledgebase_sources"]; provided {
				if raw == nil {
					return "", fmt.Errorf("knowledgebase_sources must be an array; use [] to detach all")
				}
				encoded, err := json.Marshal(raw)
				if err != nil {
					return "", err
				}
				var sources []workflowtypes.KnowledgebaseSource
				if err = json.Unmarshal(encoded, &sources); err != nil {
					return "", err
				}
				if sources == nil {
					sources = []workflowtypes.KnowledgebaseSource{}
				}
				if err := workflowkb.Validate(GetPromptDocsRoot(), iwm.controller.GetWorkspacePath(), sources); err != nil {
					return "", err
				}
				content, err := iwm.controller.ReadWorkspaceFile(ctx, "workflow.json")
				if err != nil {
					return "", err
				}
				var manifest map[string]interface{}
				if err = json.Unmarshal([]byte(content), &manifest); err != nil {
					return "", err
				}
				manifest["knowledgebase_sources"] = sources
				manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
				updated, err := json.MarshalIndent(manifest, "", "  ")
				if err != nil {
					return "", err
				}
				if err = iwm.controller.writeManagedWorkflowManifest(ctx, string(updated)); err != nil {
					return "", err
				}
				refreshWorkflowFolderAccessSession(ctx, iwm.controller.GetWorkspacePath())
				anyChanged = true
				sb.WriteString("\n" + workflowKnowledgebaseSourcesPrompt(iwm.controller.GetWorkspacePath()) + "\n")
			}

			if raw, provided := args["kb_write_grants"]; provided {
				if raw == nil {
					return "", fmt.Errorf("kb_write_grants must be an array; use [] to revoke all")
				}
				encoded, err := json.Marshal(raw)
				if err != nil {
					return "", err
				}
				var grants []string
				if err = json.Unmarshal(encoded, &grants); err != nil {
					return "", err
				}
				if grants == nil {
					grants = []string{}
				}
				for _, id := range grants {
					if strings.TrimSpace(id) == "" {
						return "", fmt.Errorf("kb_write_grants entries must be non-empty workflow IDs")
					}
				}
				content, err := iwm.controller.ReadWorkspaceFile(ctx, "workflow.json")
				if err != nil {
					return "", err
				}
				var manifest map[string]interface{}
				if err = json.Unmarshal([]byte(content), &manifest); err != nil {
					return "", err
				}
				access, _ := manifest["access"].(map[string]interface{})
				if access == nil {
					access = map[string]interface{}{}
				}
				access["allowed_kb_writers"] = grants
				manifest["access"] = access
				manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
				updated, err := json.MarshalIndent(manifest, "", "  ")
				if err != nil {
					return "", err
				}
				if err = iwm.controller.writeManagedWorkflowManifest(ctx, string(updated)); err != nil {
					return "", err
				}
				anyChanged = true
				if len(grants) == 0 {
					sb.WriteString("\n### KB write grants\nRevoked all external write access to this workflow's knowledgebase/notes/.\n")
				} else {
					sb.WriteString(fmt.Sprintf("\n### KB write grants\nWorkflows permitted to write into this workflow's knowledgebase/notes/: %s\n", strings.Join(grants, ", ")))
				}
			}

			if !anyChanged {
				return "No changes applied. Provide at least one of: knowledgebase_sources, kb_write_grants, add_servers, remove_servers, add_tools, remove_tools, add_skills, remove_skills, add_secrets, remove_secrets, run_notification_instructions, pulse_notification_instructions, run_notification_channels, pulse_notification_channels, slack_webhook_secret_name, browser_mode, cdp_ports, run_retention_count, pulse_disabled_review_modules, advisor_specialization_approval_input_id.", nil
			}

			// Persist config changes to workflow.json manifest (file-backed)
			if err := iwm.persistWorkflowConfigToManifest(ctx, logger); err != nil {
				return "", err
			}

			return sb.String(), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register update_workflow_config tool: %v", err))
	}

	// === Tool: set_workflow_contract_version ===
	// Workflow-contract upgrades need one explicit, narrow way to acknowledge
	// that their migration work and verification completed. WriteWorkflowManifest
	// is an internal server helper, not an agent tool; exposing that helper name
	// in a prompt made agents search for a tool that could never exist.
	if iwm.isRunModeRestricted() {
		// PLAT-262: skip set_workflow_contract_version registration for read-only access
	} else if err := mcpAgent.RegisterCustomTool(
		"set_workflow_contract_version",
		"Stamp workflow.json with a completed workflow contract version after the operator started a manual upgrade in this chat and that migration's work has been verified. Use get_contract_upgrades to see what is pending, do the migration the instruction describes, then stamp that same version. Stamp one version at a time, in order; the version is the record that the migration happened, so never stamp work that was not performed. Schedules never authorize or perform migrations. This changes workflow.json.version and updated_at and appends the applied timestamp to contract_upgrade_history; it cannot change plans, schedules, capabilities, or notifications.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"version": map[string]interface{}{
					"type":        "string",
					"pattern":     `^\d+\.\d+\.\d+$`,
					"description": "The exact target contract version stated by the manual upgrade instruction, for example 1.0.21.",
				},
			},
			"required": []string{"version"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			version, _ := args["version"].(string)
			version = strings.TrimSpace(version)
			parts := strings.Split(version, ".")
			if len(parts) != 3 {
				return "Error: version must be a semantic version such as 1.0.21.", nil
			}
			for _, part := range parts {
				if part == "" {
					return "Error: version must be a semantic version such as 1.0.21.", nil
				}
				if _, err := strconv.Atoi(part); err != nil {
					return "Error: version must be a semantic version such as 1.0.21.", nil
				}
			}

			content, err := iwm.controller.ReadWorkspaceFile(ctx, "workflow.json")
			if err != nil {
				return fmt.Sprintf("Failed to read workflow.json: %v", err), nil
			}
			var manifest map[string]interface{}
			if err := json.Unmarshal([]byte(content), &manifest); err != nil {
				return fmt.Sprintf("Failed to parse workflow.json: %v", err), nil
			}
			if err := schedulepolicy.ValidatePulseStamp([]byte(content), version); err != nil {
				return fmt.Sprintf("Refused: schedule Pulse migration is incomplete: %v", err), nil
			}
			if err := validateContractVersionStampPrerequisites(version, manifest); err != nil {
				return fmt.Sprintf("Refused: nested Agent artifact migration is incomplete: %v", err), nil
			}
			if err := validateManagedDBScriptsStamp(version, filepath.Join(GetPromptDocsRoot(), iwm.controller.GetWorkspacePath())); err != nil {
				return fmt.Sprintf("Refused: the managed database script migration is incomplete: %v", err), nil
			}
			if err := validateStepDescriptionLayoutStamp(version, filepath.Join(GetPromptDocsRoot(), iwm.controller.GetWorkspacePath())); err != nil {
				return fmt.Sprintf("Refused: the step description layout migration is incomplete: %v", err), nil
			}
			if current, _ := manifest["version"].(string); strings.TrimSpace(current) == version {
				return fmt.Sprintf("Workflow contract version is already %s.", version), nil
			}

			sessionID := ""
			if iwm.controller != nil {
				sessionID = iwm.controller.httpSessionID
			}
			var nextPending func() (string, string, error)
			if iwm.schedulerFuncs != nil && iwm.schedulerFuncs.NextContractUpgrade != nil && iwm.schedulerWorkspacePath != "" {
				nextPending = func() (string, string, error) {
					return iwm.schedulerFuncs.NextContractUpgrade(ctx, iwm.schedulerWorkspacePath)
				}
			}
			if refusal, ok := authorizeContractVersionStamp(sessionID, version, nextPending); !ok {
				return refusal, nil
			}

			appliedAt := time.Now().UTC()
			manifest["version"] = version
			manifest["updated_at"] = appliedAt.Format(time.RFC3339)
			if err := appendContractUpgradeHistory(manifest, version, appliedAt); err != nil {
				contractupgrade.Restore(sessionID, version)
				return fmt.Sprintf("Failed to record contract upgrade history: %v", err), nil
			}
			updated, err := json.MarshalIndent(manifest, "", "  ")
			if err != nil {
				return fmt.Sprintf("Failed to marshal workflow.json: %v", err), nil
			}
			if err := iwm.controller.writeManagedWorkflowManifest(ctx, string(updated)); err != nil {
				// Nothing changed on disk, so the turn keeps its one stamp.
				contractupgrade.Restore(sessionID, version)
				return fmt.Sprintf("Failed to write workflow.json: %v", err), nil
			}
			return fmt.Sprintf("Workflow contract version stamped %s.", version), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register set_workflow_contract_version tool: %v", err))
	}

	// === Schedule management tools ===

	// Tool: list_schedules — List schedules for this workflow
	if err := mcpAgent.RegisterCustomTool(
		"list_schedules",
		"List all schedules for this workflow from workflow.json, including IDs, type, cron/calendar shape, timezone, enabled state, mode, workshop_mode, groups, and recent runtime state. Use this before update_schedule/delete_schedule/trigger_schedule/get_schedule_runs.",
		map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if iwm.schedulerFuncs == nil {
				return "Schedule management not available in this session.", nil
			}
			if iwm.schedulerWorkspacePath == "" {
				return "No workspace path associated with this workflow session.", nil
			}
			return iwm.schedulerFuncs.ListSchedules(ctx, iwm.schedulerWorkspacePath)
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register list_schedules tool: %v", err))
	}

	// Tool: get_contract_upgrades — show pending platform migrations
	if err := mcpAgent.RegisterCustomTool(
		"get_contract_upgrades",
		"Show this workflow's platform contract status: its current version, every migration still owed with full manual instructions, and historical failed upgrade attempts. Use when the owner asks why an interactive run or webhook is blocked, why the version is behind, or what an upgrade will change. Upgrades are started manually in this Builder chat, one at a time in order. Cron/calendar and trigger_schedule runs keep using the saved contract and never authorize, apply, or stamp migrations; direct webhooks remain fail-closed until required migrations are complete.",
		map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if iwm.schedulerFuncs == nil || iwm.schedulerFuncs.GetContractUpgrades == nil {
				return "Contract upgrade status is not available in this session.", nil
			}
			if iwm.schedulerWorkspacePath == "" {
				return "No workspace path associated with this workflow session.", nil
			}
			return iwm.schedulerFuncs.GetContractUpgrades(ctx, iwm.schedulerWorkspacePath)
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register get_contract_upgrades tool: %v", err))
	}

	// Tool: create_schedule — Create a new cron schedule
	if iwm.isRunModeRestricted() {
		// PLAT-262: skip create_schedule registration for read-only access
	} else if err := mcpAgent.RegisterCustomTool(
		"create_schedule",
		"Create a new cron schedule for this workflow. Workflow schedules use mode='workshop' with workshop_mode='workshop'; read-only access is pinned to Run by the server. Messages are optional; when omitted, the scheduler asks the Builder to execute the full workflow. Before adding, inspect existing schedules and choose review frequency using token cost and accumulated-run evidence. Set after_run (backup, publish, notify) for what runs after each run; it defaults to all three. pulse_mode is legacy. Pulse never runs after a normal run, and the Workflow Review runs before every run on its own. For the full contract (collision/dependency policy design, when direct messages vs. route_selections is correct, resume_previous tradeoffs): read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/schedules.md\"}]).",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Display name for the schedule (e.g., 'Daily morning run').",
				},
				"cron_expression": map[string]interface{}{
					"type":        "string",
					"description": "5-field cron expression (minute hour day-of-month month day-of-week). Examples: '0 9 * * *' (daily 9 AM), '*/30 * * * *' (every 30 min), '0 0 * * 1' (weekly Monday midnight).",
				},
				"timezone": map[string]interface{}{
					"type":        "string",
					"description": "Required IANA timezone (e.g. 'UTC', 'America/New_York', 'Asia/Kolkata'). Do not use abbreviations like EST, PST, or IST.",
				},
				"group_names": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Required. Variable group names to run (e.g., 'group-1', 'group-2'). Read variables/variables.json to see available groups. Do not leave empty.",
				},
				"mode": map[string]interface{}{
					"type":        "string",
					"description": "Execution mode. Only 'workshop' is supported for workflow schedules; legacy 'workflow' input is normalized to 'workshop'.",
					"enum":        []string{"workshop"},
				},
				"messages": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Optional workshop conversation turns. Prefer route_selections for durable planned behavior. Multi-message or procedure-like queues require direct_messages_reason because they lack the canonical step lifecycle.",
				},
				"route_selections": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": map[string]interface{}{"type": "string"},
					"description":          "Optional routing-step selections passed to run_full_workflow.",
				},
				"direct_messages_reason": map[string]interface{}{
					"type":        "string",
					"description": "Required for an intentional direct procedure or multi-turn conversation. Explain why it is schedule-specific rather than canonical plan work.",
				},
				"workshop_mode": map[string]interface{}{
					"type":        "string",
					"description": "Workshop is the supported value for writable scheduled sessions; read-only access is pinned to Run by the server.",
					"enum":        []string{"workshop"},
				},
				"resume_previous": map[string]interface{}{
					"type":        "boolean",
					"description": "Optional opt-in when this workflow runs on a coding-agent CLI (claude-code, cursor-cli, codex-cli, pi-cli). When true, each scheduled run resumes the previous run's thread (same CLI) instead of starting a fresh session, so the agent keeps prior context across runs. API model providers and non-resumable runs start fresh. Defaults to false; omit for fresh sessions.",
				},
				"pulse_review_only": map[string]interface{}{
					"type":        "boolean",
					"description": "Legacy compatibility field only. Do not set true for new workflows: recurring Pulse is workflow-wide workflow.json.pulse.enabled and runs on the workflow's own self-deciding Pulse schedule, never after a normal run. An enabled legacy value is migrated to that setting and is not registered as an independent cron. Omit or false for ordinary schedules.",
				},
				"after_run": map[string]interface{}{
					"type":        "object",
					"description": "What runs after each run of this schedule, as plain platform code with no Pulse pass: backup (save workflow state when it changed), publish (refresh the published report when it changed), notify (send the run summary; routine successes are recorded in the dashboard, failures and changes are sent). Normal schedules turn all three on. Replaces pulse_mode.",
					"properties": map[string]interface{}{
						"backup":  map[string]interface{}{"type": "boolean"},
						"publish": map[string]interface{}{"type": "boolean"},
						"notify":  map[string]interface{}{"type": "boolean"},
					},
				},
				"pulse_mode_reason": map[string]interface{}{"type": "string", "description": "Legacy: kept in step with after_run. Omit."},
				"pulse_mode": map[string]interface{}{
					"type": "string", "enum": []string{"off", "basic"},
					"description": "Legacy, read for one release: use after_run. off = none of backup/publish/notify, basic = all three.",
				},
				"execution_mode": map[string]interface{}{
					"type": "string", "enum": []string{"close_only"},
					"description": "Optional backend-enforced operating mode. Use close_only for a run that may manage/close existing state but must never create new entries.",
				},
				"collision_policy": map[string]interface{}{
					"type": "string", "enum": []string{"skip", "queue_latest", "retry", "coalesce"},
					"description": "What to do if this workflow is already running: skip discards; queue_latest keeps only the newest; retry preserves the first blocked occurrence; coalesce combines repeated occurrences into one catch-up run.",
				},
				"concurrency_mode": map[string]interface{}{
					"type": "string", "enum": []string{"sequential", "parallel"},
					"description": "Defaults to sequential. Set parallel only after the human explicitly accepts that shared workflow DB/KB/learnings/reports/planning/browser state may conflict or be overwritten and external actions may be duplicated.",
				},
				"parallel_risk_acknowledged": map[string]interface{}{
					"type": "boolean", "description": "Required true with concurrency_mode=parallel. Set only after explicitly disclosing the shared-state overwrite and duplicate-action risks and receiving human approval.",
				},
				"max_start_delay_minutes": map[string]interface{}{
					"type": "integer", "minimum": 1,
					"description": "Maximum age of a queued occurrence before it expires.",
				},
				"after_schedule_id": map[string]interface{}{
					"type":        "string",
					"description": "Legacy singular prerequisite schedule ID. Prefer after_schedule_ids for new schedules.",
				},
				"after_schedule_ids": map[string]interface{}{
					"type": "array", "items": map[string]interface{}{"type": "string"},
					"description": "Optional prerequisite schedule IDs. This schedule waits for every listed schedule's durable occurrence on the same local date.",
				},
				"after_terminal_status": map[string]interface{}{
					"type": "string", "enum": []string{"completed", "any_terminal"},
					"description": "Which prerequisite result releases this schedule. completed is the safe default; any_terminal also releases after failure, partial, stop, or interruption.",
				},
				"after_delay_minutes": map[string]interface{}{
					"type": "integer", "minimum": 0,
					"description": "Optional delay after the prerequisite terminal receipt before this schedule may start.",
				},
				"dependency_deadline": map[string]interface{}{
					"type":        "string",
					"description": "Optional local HH:MM deadline after which this dependent occurrence expires instead of running stale.",
				},
			},
			"required": []string{"name", "cron_expression", "timezone"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if iwm.schedulerFuncs == nil {
				return "Schedule management not available in this session.", nil
			}
			if iwm.schedulerWorkspacePath == "" {
				return "No workspace path associated with this workflow session.", nil
			}
			pulseReviewOnly, _ := args["pulse_review_only"].(bool)
			name, _ := args["name"].(string)
			cronExpr, _ := args["cron_expression"].(string)
			timezone, _ := args["timezone"].(string)
			var groupNames []string
			if raw, ok := args["group_names"]; ok && raw != nil {
				if arr, ok := raw.([]interface{}); ok {
					for _, v := range arr {
						if s, ok := v.(string); ok {
							groupNames = append(groupNames, s)
						}
					}
				}
			}
			mode, _ := args["mode"].(string)
			routeSelections := map[string]string{}
			if raw, ok := args["route_selections"].(map[string]interface{}); ok {
				for key, value := range raw {
					if routeID, ok := value.(string); ok {
						routeSelections[key] = routeID
					}
				}
			}
			var messages []string
			if raw, ok := args["messages"]; ok && raw != nil {
				if arr, ok := raw.([]interface{}); ok {
					for _, v := range arr {
						if s, ok := v.(string); ok {
							messages = append(messages, s)
						}
					}
				}
			}
			workshopMode, _ := args["workshop_mode"].(string)
			if strings.TrimSpace(workshopMode) == "" {
				workshopMode = "workshop"
			}
			directMessagesReason, _ := args["direct_messages_reason"].(string)
			var resumePrevious *bool
			if raw, ok := args["resume_previous"]; ok && raw != nil {
				if b, ok2 := raw.(bool); ok2 {
					resumePrevious = &b
				}
			}
			if name == "" {
				return "name is required.", nil
			}
			if cronExpr == "" {
				return "cron_expression is required.", nil
			}
			if err := validateWorkshopScheduleTimezone(timezone); err != nil {
				return err.Error(), nil
			}
			if !pulseReviewOnly && len(groupNames) == 0 {
				return "group_names is required. Read variables/variables.json and provide at least one explicit group_name, e.g. ['group-1'].", nil
			}
			if pulseReviewOnly && (len(groupNames) > 0 || len(routeSelections) > 0 || len(messages) > 0) {
				return "pulse_review_only does not run the workflow — it must not set group_names, route_selections, or messages.", nil
			}
			policy := ScheduleRuntimePolicy{}
			policy.PulseMode, _ = args["pulse_mode"].(string)
			policy.PulseModeReason, _ = args["pulse_mode_reason"].(string)
			policy.AfterRun, policy.SetAfterRun = scheduleAfterRunArgument(args)
			policy.ExecutionMode, _ = args["execution_mode"].(string)
			policy.CollisionPolicy, _ = args["collision_policy"].(string)
			policy.ConcurrencyMode, _ = args["concurrency_mode"].(string)
			policy.ParallelRiskAcknowledged, _ = args["parallel_risk_acknowledged"].(bool)
			policy.AfterScheduleID, _ = args["after_schedule_id"].(string)
			policy.AfterScheduleIDs, _ = stringSliceArgument(args, "after_schedule_ids")
			policy.AfterTerminalStatus, _ = args["after_terminal_status"].(string)
			policy.DependencyDeadline, _ = args["dependency_deadline"].(string)
			if value, ok := args["max_start_delay_minutes"].(float64); ok {
				policy.MaxStartDelayMinutes = int(value)
			}
			if value, ok := args["after_delay_minutes"].(float64); ok {
				policy.AfterDelayMinutes = int(value)
			}
			return iwm.schedulerFuncs.CreateSchedule(ctx, iwm.schedulerWorkspacePath, name, cronExpr, timezone, groupNames, routeSelections, mode, messages, directMessagesReason, workshopMode, resumePrevious, pulseReviewOnly, policy)
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register create_schedule tool: %v", err))
	}

	// Tool: create_calendar_schedule — Create dated one-time runs for content calendars
	if iwm.isRunModeRestricted() {
		// PLAT-262: skip create_calendar_schedule registration for read-only access
	} else if err := mcpAgent.RegisterCustomTool(
		"create_calendar_schedule",
		"Create a dated calendar schedule for this workflow, such as a full-month Instagram content calendar. Set after_run (backup, publish, notify); it defaults to all three. Use this when the user provides specific dates/times instead of a repeating cron pattern. Workflow calendar schedules use mode='workshop' and workshop_mode='workshop'; read-only access is pinned to Run by the server. For the full contract: read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/schedules.md\"}]).",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"after_run": map[string]interface{}{
					"type":        "object",
					"description": "What runs after each run of this schedule, as plain platform code with no Pulse pass: backup (save workflow state when it changed), publish (refresh the published report when it changed), notify (send the run summary; routine successes are recorded in the dashboard, failures and changes are sent). Normal schedules turn all three on. Replaces pulse_mode.",
					"properties": map[string]interface{}{
						"backup":  map[string]interface{}{"type": "boolean"},
						"publish": map[string]interface{}{"type": "boolean"},
						"notify":  map[string]interface{}{"type": "boolean"},
					},
				},
				"pulse_mode":        map[string]interface{}{"type": "string", "enum": []string{"off", "basic"}, "description": "Legacy, read for one release: use after_run."},
				"pulse_mode_reason": map[string]interface{}{"type": "string", "description": "Legacy: kept in step with after_run. Omit."},
				"name":              map[string]interface{}{"type": "string", "description": "Display name for the calendar schedule."},
				"timezone":          map[string]interface{}{"type": "string", "description": "Required IANA timezone (e.g. 'UTC', 'America/New_York', 'Asia/Kolkata')."},
				"group_names":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Required variable group names to run."},
				"calendar_items": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"date":        map[string]interface{}{"type": "string", "description": "YYYY-MM-DD in the schedule timezone."},
							"time":        map[string]interface{}{"type": "string", "description": "HH:MM in the schedule timezone."},
							"description": map[string]interface{}{"type": "string", "description": "Optional note for this item."},
							"messages":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional per-item workshop messages."},
						},
						"required": []string{"date", "time"},
					},
				},
				"direct_messages_reason":     map[string]interface{}{"type": "string", "description": "Required when default or per-item messages form a direct procedure; explain why it is schedule-specific."},
				"mode":                       map[string]interface{}{"type": "string", "description": "Execution mode. Only 'workshop' is supported for workflow schedules; legacy 'workflow' input is normalized to 'workshop'.", "enum": []string{"workshop"}},
				"messages":                   map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional default workshop messages for all items. Omit for the default full-workflow run message."},
				"workshop_mode":              map[string]interface{}{"type": "string", "description": "Workshop is the supported value for writable scheduled sessions; read-only access is pinned to Run by the server.", "enum": []string{"workshop"}},
				"collision_policy":           map[string]interface{}{"type": "string", "enum": []string{"skip", "queue_latest", "retry", "coalesce"}, "description": "What to do if the workflow is busy when a calendar item is due."},
				"concurrency_mode":           map[string]interface{}{"type": "string", "enum": []string{"sequential", "parallel"}, "description": "Defaults to sequential. Parallel requires explicit human approval after disclosing shared-state overwrite and duplicate-action risks."},
				"parallel_risk_acknowledged": map[string]interface{}{"type": "boolean", "description": "Required true with concurrency_mode=parallel; set only after explicit human approval."},
				"max_start_delay_minutes": map[string]interface{}{
					"type": "integer", "minimum": 1, "description": "Maximum age of a queued calendar occurrence before it expires.",
				},
				"after_schedule_ids": map[string]interface{}{
					"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Prerequisite schedule IDs whose same-day occurrences must all release first.",
				},
				"after_terminal_status": map[string]interface{}{"type": "string", "enum": []string{"completed", "any_terminal"}, "description": "Which prerequisite outcomes release this schedule."},
				"after_delay_minutes":   map[string]interface{}{"type": "integer", "minimum": 0, "description": "Delay after every prerequisite terminal receipt."},
				"dependency_deadline":   map[string]interface{}{"type": "string", "description": "Optional local HH:MM deadline for prerequisite release."},
			},
			"required": []string{"name", "timezone", "calendar_items", "group_names"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if iwm.schedulerFuncs == nil || iwm.schedulerFuncs.CreateCalendarSchedule == nil {
				return "Calendar schedule management not available in this session.", nil
			}
			if iwm.schedulerWorkspacePath == "" {
				return "No workspace path associated with this workflow session.", nil
			}
			name, _ := args["name"].(string)
			timezone, _ := args["timezone"].(string)
			if name == "" {
				return "name is required.", nil
			}
			if err := validateWorkshopScheduleTimezone(timezone); err != nil {
				return err.Error(), nil
			}
			rawItems, ok := args["calendar_items"]
			if !ok || rawItems == nil {
				return "calendar_items is required.", nil
			}
			calendarItemsJSON, err := json.Marshal(rawItems)
			if err != nil {
				return "", err
			}
			var groupNames []string
			if raw, ok := args["group_names"]; ok && raw != nil {
				if arr, ok := raw.([]interface{}); ok {
					for _, v := range arr {
						if s, ok := v.(string); ok {
							groupNames = append(groupNames, s)
						}
					}
				}
			}
			if len(groupNames) == 0 {
				return "group_names is required. Read variables/variables.json and provide at least one explicit group_name.", nil
			}
			mode, _ := args["mode"].(string)
			var messages []string
			if raw, ok := args["messages"]; ok && raw != nil {
				if arr, ok := raw.([]interface{}); ok {
					for _, v := range arr {
						if s, ok := v.(string); ok {
							messages = append(messages, s)
						}
					}
				}
			}
			workshopMode, _ := args["workshop_mode"].(string)
			if strings.TrimSpace(workshopMode) == "" {
				workshopMode = "workshop"
			}
			directMessagesReason, _ := args["direct_messages_reason"].(string)
			policy := ScheduleRuntimePolicy{}
			policy.PulseMode, _ = args["pulse_mode"].(string)
			policy.PulseModeReason, _ = args["pulse_mode_reason"].(string)
			policy.AfterRun, policy.SetAfterRun = scheduleAfterRunArgument(args)
			policy.CollisionPolicy, _ = args["collision_policy"].(string)
			policy.ConcurrencyMode, _ = args["concurrency_mode"].(string)
			policy.ParallelRiskAcknowledged, _ = args["parallel_risk_acknowledged"].(bool)
			policy.AfterScheduleIDs, _ = stringSliceArgument(args, "after_schedule_ids")
			policy.AfterTerminalStatus, _ = args["after_terminal_status"].(string)
			policy.DependencyDeadline, _ = args["dependency_deadline"].(string)
			if value, ok := args["max_start_delay_minutes"].(float64); ok {
				policy.MaxStartDelayMinutes = int(value)
			}
			if value, ok := args["after_delay_minutes"].(float64); ok {
				policy.AfterDelayMinutes = int(value)
			}
			return iwm.schedulerFuncs.CreateCalendarSchedule(ctx, iwm.schedulerWorkspacePath, name, timezone, groupNames, string(calendarItemsJSON), mode, messages, directMessagesReason, workshopMode, policy)
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register create_calendar_schedule tool: %v", err))
	}

	// Tool: update_schedule — Update a schedule
	if iwm.isRunModeRestricted() {
		// PLAT-262: skip update_schedule registration for read-only access
	} else if err := mcpAgent.RegisterCustomTool(
		"update_schedule",
		"Update an existing schedule. Only provided fields are changed; omitted fields keep their current values. For collision/dependency policy design and when direct messages vs. route_selections is correct: read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/schedules.md\"}]).",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"job_id": map[string]interface{}{
					"type":        "string",
					"description": "The schedule ID to update (from list_schedules).",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "New display name.",
				},
				"cron_expression": map[string]interface{}{
					"type":        "string",
					"description": "New 5-field cron expression.",
				},
				"timezone": map[string]interface{}{
					"type":        "string",
					"description": "New IANA timezone (e.g. 'UTC', 'America/New_York', 'Asia/Kolkata'). Do not use abbreviations like EST, PST, or IST.",
				},
				"group_names": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Explicit variable group names for the schedule (e.g., 'group-1', 'group-2'). Read variables/variables.json to see available groups. Omit to keep the current groups; do not pass an empty array.",
				},
				"enabled": map[string]interface{}{
					"type":        "boolean",
					"description": "Enable or disable the schedule.",
				},
				"mode": map[string]interface{}{
					"type":        "string",
					"description": "Execution mode. Only 'workshop' is supported for workflow schedules; legacy 'workflow' input is normalized to 'workshop'.",
					"enum":        []string{"workshop"},
				},
				"messages": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Replaces existing messages. Messages should reference tools with full parameters, e.g. ['Run the full workflow using run_full_workflow(group_name=\"group-1\")']. Pass [] or null to clear back to the route-based default (equivalent to omitting messages on create_schedule). Omit this field entirely to leave existing messages untouched.",
				},
				"workshop_mode": map[string]interface{}{
					"type":        "string",
					"description": "Use 'workshop'. Omit this field to preserve an existing legacy schedule value.",
					"enum":        []string{"workshop"},
				},
				"resume_previous": map[string]interface{}{
					"type":        "boolean",
					"description": "Optional opt-in when this workflow runs on a coding-agent CLI. When true, scheduled runs resume the previous thread (same CLI) instead of starting fresh. Set false to go back to fresh sessions. Omit to keep the current setting.",
				},
				"route_selections": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": map[string]interface{}{"type": "string"},
					"description":          "Replace routing-step selections; pass {} to clear.",
				},
				"direct_messages_reason": map[string]interface{}{
					"type":        "string",
					"description": "Set or clear the rationale for an intentional direct schedule sequence.",
				},
				"pulse_review_only": map[string]interface{}{
					"type":        "boolean",
					"description": "Legacy compatibility field only. Do not use it to configure Pulse: recurring Pulse is workflow-wide workflow.json.pulse.enabled and runs on the workflow's own self-deciding Pulse schedule, never after a normal run. Omit to leave any legacy value unchanged.",
				},
				"after_run": map[string]interface{}{
					"type":        "object",
					"description": "What runs after each run of this schedule, as plain platform code with no Pulse pass: backup (save workflow state when it changed), publish (refresh the published report when it changed), notify (send the run summary; routine successes are recorded in the dashboard, failures and changes are sent). Normal schedules turn all three on. Replaces pulse_mode.",
					"properties": map[string]interface{}{
						"backup":  map[string]interface{}{"type": "boolean"},
						"publish": map[string]interface{}{"type": "boolean"},
						"notify":  map[string]interface{}{"type": "boolean"},
					},
				},
				"pulse_mode_reason": map[string]interface{}{"type": "string", "description": "Legacy: kept in step with after_run. Omit."},
				"pulse_mode": map[string]interface{}{
					"type": "string", "enum": []string{"off", "basic"},
					"description": "Legacy, read for one release: use after_run. Omit to preserve the current value.",
				},
				"execution_mode": map[string]interface{}{
					"type": "string", "description": "Set close_only, or an empty string to clear the backend-enforced execution mode.",
				},
				"collision_policy": map[string]interface{}{
					"type": "string", "description": "Set skip, queue_latest, retry, or coalesce. Empty resets to the default skip behavior.",
				},
				"concurrency_mode": map[string]interface{}{
					"type": "string", "enum": []string{"sequential", "parallel"}, "description": "Set sequential (default) or parallel. Parallel requires explicit human approval after the shared-state overwrite and duplicate-action warning.",
				},
				"parallel_risk_acknowledged": map[string]interface{}{
					"type": "boolean", "description": "Set true only when the human explicitly approved parallel risk; set false when returning to sequential.",
				},
				"max_start_delay_minutes": map[string]interface{}{
					"type": "integer", "minimum": 0, "description": "Maximum queued age. Zero restores the platform default.",
				},
				"after_schedule_id": map[string]interface{}{
					"type": "string", "description": "Legacy singular prerequisite schedule ID, or an empty string to clear it.",
				},
				"after_schedule_ids": map[string]interface{}{
					"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Replace the prerequisite list. Pass [] to clear it; every listed same-day occurrence must release before this schedule starts.",
				},
				"after_terminal_status": map[string]interface{}{
					"type": "string", "description": "Set completed or any_terminal. Empty restores completed.",
				},
				"after_delay_minutes": map[string]interface{}{
					"type": "integer", "minimum": 0, "description": "Delay after prerequisite completion. Zero clears it.",
				},
				"dependency_deadline": map[string]interface{}{
					"type": "string", "description": "Local HH:MM deadline, or an empty string to clear it.",
				},
			},
			"required": []string{"job_id"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if iwm.schedulerFuncs == nil {
				return "Schedule management not available in this session.", nil
			}
			jobID, _ := args["job_id"].(string)
			if jobID == "" {
				return "job_id is required.", nil
			}
			name, _ := args["name"].(string)
			cronExpr, _ := args["cron_expression"].(string)
			timezone, _ := args["timezone"].(string)
			if timezone != "" {
				if err := validateWorkshopScheduleTimezone(timezone); err != nil {
					return err.Error(), nil
				}
			}
			var groupNames []string
			setGroupNames := false
			if raw, ok := args["group_names"]; ok && raw != nil {
				setGroupNames = true
				if arr, ok := raw.([]interface{}); ok {
					for _, v := range arr {
						if s, ok := v.(string); ok {
							groupNames = append(groupNames, s)
						}
					}
				}
			}
			if setGroupNames && len(groupNames) == 0 {
				return "group_names cannot be empty. Provide at least one explicit group_name from variables/variables.json, or omit group_names to keep the current selection.", nil
			}
			setRouteSelections := false
			var routeSelections map[string]string
			if raw, supplied := args["route_selections"]; supplied && raw != nil {
				setRouteSelections = true
				routeSelections = map[string]string{}
				if object, ok := raw.(map[string]interface{}); ok {
					for key, value := range object {
						if routeID, ok := value.(string); ok {
							routeSelections[key] = routeID
						}
					}
				}
			}
			var enabled *bool
			if raw, ok := args["enabled"]; ok && raw != nil {
				if b, ok := raw.(bool); ok {
					enabled = &b
				}
			}
			mode, _ := args["mode"].(string)
			var messages []string
			setMessages := false
			// The key being present at all — including an explicit null or an
			// empty array — means "set this", not just a non-empty array. A
			// caller clearing messages back to the route-based default (an
			// empty list) must be distinguishable from omitting the field
			// entirely, or the clear silently no-ops. See PLAT-097.
			if raw, ok := args["messages"]; ok {
				setMessages = true
				if arr, ok := raw.([]interface{}); ok {
					for _, v := range arr {
						if s, ok := v.(string); ok {
							messages = append(messages, s)
						}
					}
				}
			}
			workshopMode, _ := args["workshop_mode"].(string)
			var directMessagesReason *string
			if raw, ok := args["direct_messages_reason"]; ok && raw != nil {
				if value, ok := raw.(string); ok {
					directMessagesReason = &value
				}
			}
			var resumePrevious *bool
			if raw, ok := args["resume_previous"]; ok && raw != nil {
				if b, ok := raw.(bool); ok {
					resumePrevious = &b
				}
			}
			var pulseReviewOnly *bool
			if raw, ok := args["pulse_review_only"]; ok && raw != nil {
				if b, ok := raw.(bool); ok {
					pulseReviewOnly = &b
				}
			}
			if pulseReviewOnly != nil && *pulseReviewOnly && (setGroupNames || setRouteSelections || setMessages) {
				return "pulse_review_only=true does not run the workflow — do not combine it with group_names, route_selections, or messages in the same call.", nil
			}
			var policy *ScheduleRuntimePolicy
			if _, ok1 := args["execution_mode"]; ok1 {
				policy = &ScheduleRuntimePolicy{}
			}
			if _, ok2 := args["collision_policy"]; ok2 && policy == nil {
				policy = &ScheduleRuntimePolicy{}
			}
			if _, ok := args["concurrency_mode"]; ok && policy == nil {
				policy = &ScheduleRuntimePolicy{}
			}
			if _, ok := args["parallel_risk_acknowledged"]; ok && policy == nil {
				policy = &ScheduleRuntimePolicy{}
			}
			if _, ok3 := args["max_start_delay_minutes"]; ok3 && policy == nil {
				policy = &ScheduleRuntimePolicy{}
			}
			if _, ok4 := args["after_schedule_id"]; ok4 && policy == nil {
				policy = &ScheduleRuntimePolicy{}
			}
			if _, ok := args["after_schedule_ids"]; ok && policy == nil {
				policy = &ScheduleRuntimePolicy{}
			}
			if _, ok5 := args["after_terminal_status"]; ok5 && policy == nil {
				policy = &ScheduleRuntimePolicy{}
			}
			if _, ok6 := args["after_delay_minutes"]; ok6 && policy == nil {
				policy = &ScheduleRuntimePolicy{}
			}
			if _, ok7 := args["dependency_deadline"]; ok7 && policy == nil {
				policy = &ScheduleRuntimePolicy{}
			}
			if _, ok := args["pulse_mode_reason"]; ok && policy == nil {
				policy = &ScheduleRuntimePolicy{}
			}
			if _, ok := args["pulse_mode"]; ok && policy == nil {
				policy = &ScheduleRuntimePolicy{}
			}
			if _, ok := args["after_run"]; ok && policy == nil {
				policy = &ScheduleRuntimePolicy{}
			}
			if policy != nil {
				policy.AfterRun, policy.SetAfterRun = scheduleAfterRunArgument(args)
				_, policy.SetPulseMode = args["pulse_mode"]
				_, policy.SetPulseModeReason = args["pulse_mode_reason"]
				policy.PulseMode, _ = args["pulse_mode"].(string)
				policy.PulseModeReason, _ = args["pulse_mode_reason"].(string)
				_, policy.SetExecutionMode = args["execution_mode"]
				_, policy.SetCollisionPolicy = args["collision_policy"]
				_, policy.SetConcurrencyMode = args["concurrency_mode"]
				_, policy.SetParallelRiskAcknowledged = args["parallel_risk_acknowledged"]
				_, policy.SetMaxStartDelayMinutes = args["max_start_delay_minutes"]
				_, policy.SetAfterScheduleID = args["after_schedule_id"]
				policy.AfterScheduleIDs, policy.SetAfterScheduleIDs = stringSliceArgument(args, "after_schedule_ids")
				_, policy.SetAfterTerminalStatus = args["after_terminal_status"]
				_, policy.SetAfterDelayMinutes = args["after_delay_minutes"]
				_, policy.SetDependencyDeadline = args["dependency_deadline"]
				policy.ExecutionMode, _ = args["execution_mode"].(string)
				policy.CollisionPolicy, _ = args["collision_policy"].(string)
				policy.ConcurrencyMode, _ = args["concurrency_mode"].(string)
				policy.ParallelRiskAcknowledged, _ = args["parallel_risk_acknowledged"].(bool)
				policy.AfterScheduleID, _ = args["after_schedule_id"].(string)
				policy.AfterTerminalStatus, _ = args["after_terminal_status"].(string)
				policy.DependencyDeadline, _ = args["dependency_deadline"].(string)
				if value, ok := args["max_start_delay_minutes"].(float64); ok {
					policy.MaxStartDelayMinutes = int(value)
				}
				if value, ok := args["after_delay_minutes"].(float64); ok {
					policy.AfterDelayMinutes = int(value)
				}
			}
			return iwm.schedulerFuncs.UpdateSchedule(ctx, jobID, name, cronExpr, timezone, groupNames, setGroupNames, routeSelections, setRouteSelections, enabled, mode, messages, setMessages, directMessagesReason, workshopMode, resumePrevious, pulseReviewOnly, policy)
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register update_schedule tool: %v", err))
	}

	// Tool: delete_schedule — Delete a schedule
	if iwm.isRunModeRestricted() {
		// PLAT-262: skip delete_schedule registration for read-only access
	} else if err := mcpAgent.RegisterCustomTool(
		"delete_schedule",
		"Permanently delete a schedule. This cannot be undone.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"job_id": map[string]interface{}{
					"type":        "string",
					"description": "The schedule ID to delete (from list_schedules).",
				},
			},
			"required": []string{"job_id"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if iwm.schedulerFuncs == nil {
				return "Schedule management not available in this session.", nil
			}
			jobID, _ := args["job_id"].(string)
			if jobID == "" {
				return "job_id is required.", nil
			}
			if err := iwm.schedulerFuncs.DeleteSchedule(ctx, jobID); err != nil {
				return "", err
			}
			return fmt.Sprintf("Schedule `%s` deleted.", jobID), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register delete_schedule tool: %v", err))
	}

	// Tool: trigger_schedule — Manually trigger a schedule run
	if err := mcpAgent.RegisterCustomTool(
		"trigger_schedule",
		"Manually trigger a schedule to run immediately, outside its normal cron timing.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"job_id": map[string]interface{}{
					"type":        "string",
					"description": "The schedule ID to trigger (from list_schedules).",
				},
			},
			"required": []string{"job_id"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if iwm.schedulerFuncs == nil {
				return "Schedule management not available in this session.", nil
			}
			jobID, _ := args["job_id"].(string)
			if jobID == "" {
				return "job_id is required.", nil
			}
			return iwm.schedulerFuncs.TriggerSchedule(ctx, jobID)
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register trigger_schedule tool: %v", err))
	}

	// Tool: get_schedule_runs — Get run history for a schedule
	if err := mcpAgent.RegisterCustomTool(
		"get_schedule_runs",
		"View the execution history for a specific schedule, including status, duration, and errors. Also returns any recent occurrences the scheduler deliberately did NOT run (global pause, another schedule already owning the workflow, a queued dependency) with the real reason — these never appear in Run History since they never started a run. Before concluding a schedule 'silently skipped' or diagnosing a missing recovery mechanism, check this list first: a schedule with no runs for days is very often a scheduler correctly honoring a pause the whole time, not a defect.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"job_id": map[string]interface{}{
					"type":        "string",
					"description": "The schedule ID to get runs for (from list_schedules).",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of runs to return. Defaults to 10.",
				},
			},
			"required": []string{"job_id"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if iwm.schedulerFuncs == nil {
				return "Schedule management not available in this session.", nil
			}
			jobID, _ := args["job_id"].(string)
			if jobID == "" {
				return "job_id is required.", nil
			}
			limit := 10
			if raw, ok := args["limit"]; ok && raw != nil {
				if f, ok := raw.(float64); ok {
					limit = int(f)
				}
			}
			return iwm.schedulerFuncs.GetScheduleRuns(ctx, jobID, limit)
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register get_schedule_runs tool: %v", err))
	}

	// === Skill management tools ===

	// Tool: list_skills — List all available skills in the workspace
	if err := mcpAgent.RegisterCustomTool(
		"list_skills",
		"List all available skills in the workspace. Shows both selected skills (used by this workflow) and all discovered skills. Being listed as selected is not the same as being usable at runtime — a step only receives a skill if it's explicitly listed in that step's enabled_skills.",
		map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if iwm.skillFuncs == nil {
				return "Skill management not available in this session.", nil
			}
			return iwm.skillFuncs.ListSkills(ctx)
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register list_skills tool: %v", err))
	}

	// Tool: import_skill — Import a skill from GitHub
	if iwm.isRunModeRestricted() {
		// PLAT-262: skip import_skill registration for read-only access
	} else if err := mcpAgent.RegisterCustomTool(
		"import_skill",
		"Import a skill from GitHub into the workspace. The skill will be downloaded and available for use in workflows. Use list_skills first to see what's already available. Importing alone does not attach it anywhere: call update_workflow_config(add_skills=[\"folder-name\"]) to select it for the workflow, then update_step_config(step_id, enabled_skills=[\"folder-name\"]) on each step that should actually receive it at runtime — step execution does not inherit workflow-selected skills.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"github_url": map[string]interface{}{
					"type":        "string",
					"description": "GitHub URL of the skill to import. Can be a repository URL or a path to a specific skill folder (e.g., 'https://github.com/org/repo/tree/main/skills/my-skill').",
				},
				"token": map[string]interface{}{
					"type":        "string",
					"description": "Optional GitHub personal access token for private repositories.",
				},
			},
			"required": []string{"github_url"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if iwm.skillFuncs == nil {
				return "Skill management not available in this session.", nil
			}
			githubURL, _ := args["github_url"].(string)
			if githubURL == "" {
				return "github_url is required.", nil
			}
			token, _ := args["token"].(string)
			return iwm.skillFuncs.ImportSkill(ctx, githubURL, token)
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register import_skill tool: %v", err))
	}

	// Tool: uninstall_skill — Uninstall a skill from the workspace
	if iwm.isRunModeRestricted() {
		// PLAT-262: skip uninstall_skill registration for read-only access
	} else if err := mcpAgent.RegisterCustomTool(
		"uninstall_skill",
		"Uninstall a skill from the workspace. Removes skill files and version tracking. Use list_skills first to see available skills and their folder names. This does not remove the skill from any workflow's add_skills or a step's enabled_skills — clear those references first (update_workflow_config, update_step_config) or the workflow will point at a skill that no longer exists.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"folder_name": map[string]interface{}{
					"type":        "string",
					"description": "The folder name of the skill to uninstall (from list_skills).",
				},
			},
			"required": []string{"folder_name"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if iwm.skillFuncs == nil {
				return "Skill management not available in this session.", nil
			}
			folderName, _ := args["folder_name"].(string)
			if folderName == "" {
				return "folder_name is required.", nil
			}
			if err := iwm.skillFuncs.DeleteSkill(ctx, folderName); err != nil {
				return fmt.Sprintf("Failed to uninstall skill %q: %v", folderName, err), nil
			}
			return fmt.Sprintf("Successfully uninstalled skill %q from workspace.", folderName), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register uninstall_skill tool: %v", err))
	}

	// Tool: search_skills — Search the skills registry
	if err := mcpAgent.RegisterCustomTool(
		"search_skills",
		"Search for skills in the public skills registry. Returns matching skills with install commands. Use install_skill to install a result. Installing is not the same as attaching it to a workflow or step — see install_skill.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search query (e.g., 'social media', 'browser automation', 'data visualization').",
				},
			},
			"required": []string{"query"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if iwm.skillFuncs == nil || iwm.skillFuncs.SearchSkills == nil {
				return "Skill search not available. The skills CLI (npx) may not be installed.", nil
			}
			query, _ := args["query"].(string)
			if query == "" {
				return "query is required.", nil
			}
			return iwm.skillFuncs.SearchSkills(ctx, query)
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register search_skills tool: %v", err))
	}

	// Tool: install_skill — Install a skill via the skills CLI
	if iwm.isRunModeRestricted() {
		// PLAT-262: skip install_skill registration for read-only access
	} else if err := mcpAgent.RegisterCustomTool(
		"install_skill",
		"Install a skill from the public skills registry using owner/repo@skill-name format. Use search_skills first to find available skills. Installing alone does not attach it anywhere: call update_workflow_config(add_skills=[\"folder-name\"]) to select it for the workflow, then update_step_config(step_id, enabled_skills=[\"folder-name\"]) on each step that should actually receive it at runtime — step execution does not inherit workflow-selected skills.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"source": map[string]interface{}{
					"type":        "string",
					"description": "Skill source in owner/repo@skill-name format (e.g., 'anthropics/skills@skill-creator').",
				},
			},
			"required": []string{"source"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if iwm.skillFuncs == nil || iwm.skillFuncs.InstallSkill == nil {
				return "Skill installation not available. The skills CLI (npx) may not be installed.", nil
			}
			source, _ := args["source"].(string)
			if source == "" {
				return "source is required (e.g., 'owner/repo@skill-name').", nil
			}
			return iwm.skillFuncs.InstallSkill(ctx, source)
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register install_skill tool: %v", err))
	}

	// ── LLM management tools (only when server callbacks are available) ──
	if iwm.llmToolsFuncs != nil {
		registerWorkshopLLMTools(iwm, mcpAgent, logger)
	}

}

func workshopLLMOptionsSchema(description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": description,
		"properties": map[string]interface{}{
			"reasoning_effort": map[string]interface{}{
				"type":        "string",
				"description": "Reasoning/effort level for providers that support it. For Codex CLI this becomes model_reasoning_effort; for Claude Code this becomes --effort. Prefer values from list_provider_models.reasoning_effort_levels for the selected model.",
				"enum":        []string{"none", "minimal", "low", "medium", "high", "max", "xhigh"},
			},
			"verbosity": map[string]interface{}{
				"type":        "string",
				"description": "Response verbosity for providers that support it.",
				"enum":        []string{"low", "medium", "high"},
			},
			"thinking_level": map[string]interface{}{
				"type":        "string",
				"description": "Thinking level for providers that support a named thinking setting.",
				"enum":        []string{"low", "medium", "high"},
			},
			"thinking_budget": map[string]interface{}{
				"type":        "integer",
				"description": "Thinking budget in tokens for providers that support token-budgeted thinking.",
			},
			"top_p": map[string]interface{}{
				"type":        "number",
				"description": "Optional nucleus sampling value for providers that support it.",
			},
			"top_k": map[string]interface{}{
				"type":        "integer",
				"description": "Optional top-k sampling value for providers that support it.",
			},
			"stop_sequences": map[string]interface{}{
				"type":        "array",
				"description": "Optional stop sequences for providers that support them.",
				"items":       map[string]interface{}{"type": "string"},
			},
			"endpoint": map[string]interface{}{
				"type":        "string",
				"description": "Azure endpoint override when validating an Azure model.",
			},
			"region": map[string]interface{}{
				"type":        "string",
				"description": "Azure or Bedrock region override.",
			},
			"api_version": map[string]interface{}{
				"type":        "string",
				"description": "Azure API version override.",
			},
		},
		"additionalProperties": true,
	}
}

// registerWorkshopLLMTools registers saved model-library, provider discovery,
// validation, and workflow role configuration tools on the workshop agent.
func registerWorkshopLLMTools(iwm *InteractiveWorkshopManager, mcpAgent DefinitionToolRegistrar, logger loggerv2.Logger) {
	cb := iwm.llmToolsFuncs

	// list_published_llms
	if err := mcpAgent.RegisterCustomTool(
		"list_published_llms",
		"List all published LLMs available for selection in the workflow tier config.",
		map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return cb.ListPublishedLLMs(ctx)
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register list_published_llms tool: %v", err))
	}

	// list_provider_models
	if err := mcpAgent.RegisterCustomTool(
		"list_provider_models",
		"List the frontend-visible models for a provider. Fixed providers use shared metadata; dynamic providers use the same dynamic picker source as the UI.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"provider": map[string]interface{}{
					"type":        "string",
					"description": "Provider id such as openai, openrouter, anthropic, vertex, azure, minimax, bedrock, pi-cli, claude-code.",
				},
			},
			"required": []string{"provider"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			provider := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", args["provider"])))
			if provider == "" {
				return "provider is required.", nil
			}
			return cb.ListProviderModels(ctx, provider)
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register list_provider_models tool: %v", err))
	}

	// test_llm
	if err := mcpAgent.RegisterCustomTool(
		"test_llm",
		"Validate an LLM provider/model configuration. Uses workspace-backed provider auth by default, but temporary overrides can be provided.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"provider": map[string]interface{}{
					"type":        "string",
					"description": "Provider id such as openai, openrouter, anthropic, vertex, azure, minimax, bedrock, pi-cli, claude-code, codex-cli.",
				},
				"model_id": map[string]interface{}{
					"type":        "string",
					"description": "Optional model id to validate.",
				},
				"api_key": map[string]interface{}{
					"type":        "string",
					"description": "Optional temporary API key override. If omitted, uses workspace-backed provider auth.",
				},
				"options": workshopLLMOptionsSchema("Optional model-specific options. Use reasoning_effort for Codex CLI and Claude Code effort control, and use list_provider_models to discover supported levels."),
				"endpoint": map[string]interface{}{
					"type":        "string",
					"description": "Optional Azure endpoint override.",
				},
				"region": map[string]interface{}{
					"type":        "string",
					"description": "Optional region override for Azure or Bedrock.",
				},
				"api_version": map[string]interface{}{
					"type":        "string",
					"description": "Optional Azure API version override.",
				},
			},
			"required": []string{"provider"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			return cb.ValidateLLM(ctx, args)
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register test_llm tool: %v", err))
	}

	llmEntrySchema := func(description string) map[string]interface{} {
		return map[string]interface{}{
			"type":        "object",
			"description": description,
			"properties": map[string]interface{}{
				"published_llm_id": map[string]interface{}{"type": "string", "description": "Optional id from the published LLM set."},
				"provider":         map[string]interface{}{"type": "string", "description": "Provider id."},
				"model_id":         map[string]interface{}{"type": "string", "description": "Model id."},
				"options":          map[string]interface{}{"type": "object", "description": "Provider-specific runtime options copied from the published LLM, such as reasoning_effort.", "additionalProperties": true},
			},
		}
	}

	// set_workflow_llm_config saves either a provider profile or fully explicit
	// workflow role configuration directly to workflow.json.
	if iwm.isRunModeRestricted() {
		// PLAT-262: skip set_workflow_llm_config registration for read-only access
	} else if err := mcpAgent.RegisterCustomTool(
		"set_workflow_llm_config",
		"Save the workflow's LLM configuration to workflow.json capabilities.llm_config. Requires read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/llm-selection.md\"}]) first. In provider_profile mode, provide one coding-agent provider and its current Builder, execution-tier, and Pulse defaults resolve at runtime. In explicit mode, provide builder_llm, pulse_llm, and all three execution tiers; each entry directly pins provider, model_id, and options. Saved model-library entries are optional reusable shortcuts, not a prerequisite.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"mode": map[string]interface{}{
					"type":        "string",
					"enum":        []string{workflowtypes.LLMConfigModeProviderProfile, workflowtypes.LLMConfigModeExplicit},
					"description": "provider_profile follows the coding-agent provider defaults; explicit pins every workflow role.",
				},
				"provider":    map[string]interface{}{"type": "string", "description": "Coding-agent provider id. Required only in provider_profile mode."},
				"builder_llm": llmEntrySchema("Builder model for planning, eval design, debugging, and normal workflow-builder chat."),
				"tier_1":      llmEntrySchema("High-reasoning execution tier: first-time or difficult work."),
				"tier_2":      llmEntrySchema("Medium-reasoning execution tier: established work with useful context."),
				"tier_3":      llmEntrySchema("Low-reasoning execution tier: validation and mature routine work."),
				"pulse_llm":   llmEntrySchema("Pulse coordinator model for gate, worklist, reporting, and notification turns."),
			},
			"required": []string{"mode"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			wsPath := iwm.controller.GetWorkspacePath()
			if wsPath == "" {
				return "Cannot determine workspace path.", nil
			}

			content, err := iwm.controller.ReadWorkspaceFile(ctx, "workflow.json")
			if err != nil {
				return fmt.Sprintf("Failed to read workflow.json: %v", err), nil
			}

			var manifest map[string]interface{}
			if err := json.Unmarshal([]byte(content), &manifest); err != nil {
				return fmt.Sprintf("Failed to parse workflow.json: %v", err), nil
			}

			caps, _ := manifest["capabilities"].(map[string]interface{})
			if caps == nil {
				caps = make(map[string]interface{})
			}
			existingLLMCfg, _ := caps["llm_config"].(map[string]interface{})
			llmCfg := map[string]interface{}{
				"schema_version": workflowtypes.LLMConfigSchemaVersion,
			}
			for _, key := range []string{
				"use_knowledgebase", "kb_shape",
				"enable_context_summarization",
				"enable_image_generation", "image_gen_provider", "image_gen_model_id",
			} {
				if value, ok := existingLLMCfg[key]; ok {
					llmCfg[key] = value
				}
			}

			updated := []string{}

			buildTierEntry := func(raw interface{}) map[string]interface{} {
				m, ok := raw.(map[string]interface{})
				if !ok {
					return nil
				}
				provider, _ := m["provider"].(string)
				modelID, _ := m["model_id"].(string)
				if provider == "" || modelID == "" {
					return nil
				}
				entry := map[string]interface{}{"provider": provider, "model_id": modelID}
				if publishedLLMID, _ := m["published_llm_id"].(string); publishedLLMID != "" {
					entry["published_llm_id"] = publishedLLMID
				}
				if options, _ := m["options"].(map[string]interface{}); len(options) > 0 {
					entry["options"] = options
				}
				return entry
			}

			mode := strings.TrimSpace(fmt.Sprintf("%v", args["mode"]))
			switch mode {
			case workflowtypes.LLMConfigModeProviderProfile:
				provider := strings.TrimSpace(fmt.Sprintf("%v", args["provider"]))
				if provider == "" || provider == "<nil>" {
					return "provider is required in provider_profile mode. Use list_provider_models to inspect available coding-agent providers.", nil
				}
				llmCfg["mode"] = workflowtypes.LLMConfigModeProviderProfile
				llmCfg["provider"] = provider
				updated = append(updated, fmt.Sprintf("provider_profile=%s", provider))

			case workflowtypes.LLMConfigModeExplicit:
				llmCfg["mode"] = workflowtypes.LLMConfigModeExplicit
				missing := []string{}
				for _, key := range []string{"builder_llm", "pulse_llm"} {
					entry := buildTierEntry(args[key])
					if entry == nil {
						missing = append(missing, key)
						continue
					}
					llmCfg[key] = entry
					updated = append(updated, fmt.Sprintf("%s=%v/%v", key, entry["provider"], entry["model_id"]))
				}

				tieredConfig := map[string]interface{}{}
				for _, key := range []string{"tier_1", "tier_2", "tier_3"} {
					entry := buildTierEntry(args[key])
					if entry == nil {
						missing = append(missing, key)
						continue
					}
					tieredConfig[key] = entry
					updated = append(updated, fmt.Sprintf("%s=%v/%v", key, entry["provider"], entry["model_id"]))
				}
				if len(missing) > 0 {
					return fmt.Sprintf("Explicit mode requires complete role configuration. Missing or invalid: %s.", strings.Join(missing, ", ")), nil
				}
				llmCfg["tiered_config"] = tieredConfig

			default:
				return fmt.Sprintf("mode must be %q or %q.", workflowtypes.LLMConfigModeProviderProfile, workflowtypes.LLMConfigModeExplicit), nil
			}

			caps["llm_config"] = llmCfg
			manifest["capabilities"] = caps

			out, err := json.MarshalIndent(manifest, "", "  ")
			if err != nil {
				return fmt.Sprintf("Failed to marshal workflow.json: %v", err), nil
			}
			if err := iwm.controller.writeManagedWorkflowManifest(ctx, string(out)); err != nil {
				return fmt.Sprintf("Failed to write workflow.json: %v", err), nil
			}

			logger.Info(fmt.Sprintf("✅ Workshop: workflow LLM config updated: %s", strings.Join(updated, ", ")))
			return fmt.Sprintf("Saved to workflow.json capabilities.llm_config:\n%s\n\nChanges take effect on the next workflow run.", strings.Join(updated, "\n")), nil
		},
		"workflow",
	); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to register set_workflow_llm_config tool: %v", err))
	}
}

// workshopJSONUnmarshal is a local alias to avoid import conflicts
func workshopJSONUnmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

var reviewPlanAgentSystemTemplate = MustRegisterTemplate("reviewPlanAgentSystem", `# Workflow Plan Review Agent

You are a critical reviewer of the current workflow design. Your job is not to optimize or rewrite the plan. Your job is to challenge the decisions already made and identify where the current plan or its dependent artifacts are weak, unjustified, risky, overfit, stale, or internally inconsistent.

{{.DBGuidance}}

This is a **read-only review**:
- do not modify files
- do not invent missing evidence
- focus on findings first, not redesign first

## RULES
1. **Read-Only**: Do NOT modify any files.
2. **Findings first**: Lead with concrete problems, ordered by severity. Do not hide important issues under summaries.
3. **Be specific**: Always reference exact step IDs, and nested IDs as `+"`parent-id > sub-id`"+`.
4. **Review current decisions**: Critique the decisions that exist now: step boundaries, step types, mode declarations, context dependencies, context outputs, validation shape, portability, eval coverage, skill selection/scoping, learning configuration, KB configuration, db schema contracts, report wiring, and variables.
5. **Challenge assumptions**: If a decision appears to depend on unstated assumptions, call that out explicitly.
6. **Use evidence when available**: If a target run folder is provided, use run outputs/logs/eval reports to test whether the current workflow decisions were actually justified.
7. **Do not drift into full redesign**: You may suggest a concrete correction, but the primary task is to review and explain what is wrong with the current decision.
8. **Check portability and secrecy**: Flag plan-visible secrets, user-specific values, absolute paths, run-folder-specific values, and brittle environment assumptions.
9. **Check persistent-store discipline**: Stores survive across runs — `+"`"+`learnings/`+"`"+` (HOW to run), `+"`knowledgebase/context/`"+` (user-supplied runtime business context), `+"`"+`knowledgebase/notes/`+"`"+` (workflow-discovered durable narrative observations), `+"`"+`db/db.sqlite`+"`"+` (structured durable SQLite tables for cross-run state, read live by HTML reports via `+"`window.report.query`"+`), and `+"`db/assets/`"+` (durable media/file assets referenced by db rows or reports). Flag steps that confuse these stores: stashing durable facts in learnings or plan.json, stashing user-owned context in notes, writing report data only to run folders, embedding assets as blobs, or failing to declare `+"`"+`knowledgebase_contribution`+"`"+` / `+"`"+`db/README.md`+"`"+` contracts when steps produce persistent facts.
10. **Check learning and mode discipline**: A step should write learnings only when it has reusable HOW-to-run knowledge worth capturing across runs, and it must have a concrete `+"`"+`learning_objective`+"`"+`. Deterministic API/SDK calls, CLI commands, data fetching, known pagination, stable parsing/normalization/transforms, and mechanical persistence should be `+"`"+`scripted`+"`"+` from initial design with an authored/tested `+"`code/{step-id}/main.py` (code_layout_version=1; legacy: `learnings/{step-id}/main.py`)"+`; no run-history threshold is required to choose that mode. Browser/UI work, adaptive discovery, and judgment stay agentic. The 10+ representative-run bar applies to `+"`"+`lock_code`+"`"+`, not to declaring deterministic work scripted.
11. **Check KB discipline**: KB writes require a useful `+"`"+`knowledgebase_contribution`+"`"+` and correct read/write access. `+"`knowledgebase/context/`"+` should contain user-supplied runtime context; `+"`knowledgebase/notes/`"+` should contain workflow-discovered durable narrative observations, not execution recipes, raw rows, or volatile run state. If `+"`"+`knowledgebase/notes/_index.json`+"`"+` exists, it must point to coherent topic notes.
12. **Check db discipline**: `+"`"+`db/db.sqlite`+"`"+` should be a clean relational surface: each table documented in `+"`"+`db/README.md`+"`"+` with DDL, PRIMARY KEY, upsert rule (`+"`INSERT ... ON CONFLICT`"+`), indexes, writer ownership, group separation, report consumers (the HTML report `+"`window.report.query`"+` SQL that reads it), and correct references to durable assets under `+"`db/assets/`"+`.
13. **Check skill discipline**: Installed skills live under `+"`skills/{folder}/SKILL.md`"+` and are reusable capability instructions shared across workflows. Review workflow-selected skills and per-step `+"`enabled_skills`"+` against the actual plan. Flag missing needed skills, selected-but-unused skills, descriptions that reference skills not enabled for the execution agent, malformed skill folders, and skills that duplicate workflow-specific learnings or contain workflow-specific secrets/paths/run state. Do not assume workflow-level selected skills automatically reach step execution; verify step-level `+"`enabled_skills`"+` when runtime requires explicit scoping.
14. **Check access and code-lock consistency**: `+"`"+`lock_code`+"`"+` freezes scripted `+"`"+`code/{step-id}/main.py (version 1), otherwise learnings/{step-id}/main.py`+"`"+` against fix-loop rewrites; flag it without the scripted evidence gate. Learning and KB write eligibility are access decisions, not locks: review `+"`"+`learnings_access`+"`"+` + `+"`"+`learning_objective`+"`"+`, and `+"`"+`knowledgebase_access`+"`"+` + `+"`"+`knowledgebase_contribution`+"`"+`. If a step description has meaningfully changed since the last review, recommend clearing `+"`"+`description_reviewed`+"`"+` and re-reviewing before keeping a code lock or write grant.

## STEP BOUNDARY STANDARD

Modern agents can handle long context and many tool calls. Do not flag a step merely because it performs many actions, tool calls, screen interactions, or small transformations. A step is a durable workflow boundary: it has an output contract, validation gate, retry behavior, and persistent-store responsibilities.

Start from one large `+"`message_sequence`"+` per coherent shared-context span. Prefer the fewest durable steps that preserve real control boundaries. One agentic step may own a substantial end-to-end outcome when the work shares one objective, context, tool/security envelope, output contract, retry domain, and final validation gate. Do not require a separate scripted step for each subtask, checklist item, source, tool, proof check, double-check, or intermediate thought.

When that coherent outcome needs staged assurance, prefer one `+"`message_sequence`"+` over several regular steps. Its `+"`description`"+` is the durable system-level charter (objective, boundaries, definition of done); its `+"`items[]`"+` are the actual user turns that execute and verify that charter. Do not repeat the description as item 0. Give the first work item the whole outcome; add only decision-useful follow-up turns that inspect evidence, challenge completion, and repair gaps (for example: `+"`re-open the result, verify every success criterion, and fix anything unsupported or incomplete`"+`). Do not turn a large task into one tiny sequence item per routine action.

Treat validation as an improvement to that large step before treating it as a topology change. Require run-specific proof/provenance in the output, tighten the top-level `+"`validation_schema`"+`, and add evidence-based double-check and repair turns. Flag a separate validation/reviewer step unless it needs an independently rerunnable artifact/failure domain, different permissions/tools, or genuine clean-room independence from the maker.

Multiple large sequences are correct when their contexts should not be shared—for example different credentials/security exposure, independent durable outputs/retries, clean-room independence, human or routing boundaries, or unrelated context that would distract or contaminate the next agent. The builder should decide this intelligently from workflow semantics, and the plan should make the boundary explainable. Flag action-count splitting that has no context-boundary rationale.

Separate deterministic acquisition from agentic processing even when both serve one business outcome. Fixed API/SDK requests, CLI commands, data fetching, known pagination, stable parsing/normalization, and mechanical persistence belong in one or a few scripted regular fetcher steps, batched by source/auth/retry/output contract. Their validated DB rows or artifacts feed a large message sequence for judgment, synthesis, semantic verification, and repair. Flag LLM turns that repeatedly perform deterministic retrieval/parsing, and flag one-scripted-step-per-endpoint fragmentation.

Flag **over-merged** steps when one step mixes unrelated durable outputs, validation gates, retry/failure domains, tool/security contexts, downstream contracts, persistent stores, human approvals, or routing decisions.

Flag **over-split** steps when adjacent steps share one objective and output contract, use the same tools/security context, fail and retry together, produce only scratch/pass-through intermediates, and one validation schema could verify the result. Also flag message sequences whose items merely enumerate routine sub-actions instead of reserving follow-up turns for validation, critique, correction, a real intermediate gate, or new external input.

Boundary truth: many tool calls can belong in one step; many durable contracts should not.

## CONTEXT

- **Workspace**: {{.WorkspacePath}}
{{if .TargetRunFolder}}- **Target Run Folder**: {{.TargetRunFolder}}{{end}}

## PATH DISCIPLINE

For shell commands, use absolute workspace paths: `+"`{{.AbsWorkspacePath}}/...`"+`. For workspace file tools that expect workspace-relative paths, use `+"`{{.WorkspacePath}}/...`"+`. Do not use bare `+"`runs/...`"+`, `+"`evaluation/...`"+`, `+"`db/...`"+`, or similar paths unless a tool explicitly requires a path relative to the workflow root. Do not use host paths outside workspace-docs.

## REQUIRED SOURCE REVIEW

Read the workflow's source artifacts yourself before forming findings. The Go
controller deliberately does not paste or summarize them into this prompt:

- read `+"`soul/soul.md`"+` for the objective and success criteria; report them as
  missing only after checking the file
- read `+"`workflow.json`"+` for workflow-selected skills and capability settings
- inspect `+"`planning/plan.json`"+` with targeted `+"`jq`"+` queries, one relevant
  step/route/field set at a time; do not dump the full plan into the conversation
- call `+"`get_plan_prompt_health`"+` before raising any prompt-contract-bloat
  finding. Treat its character counts and repeated-paragraph clusters as an
  objective triage signal, not an automatic defect: inspect the affected step,
  validation, and shared references before deciding whether a safe extraction
  exists. Prefer one workflow-level consolidation finding over one finding per
  oversized step, and never rewrite a broad workflow contract from review alone.
- inspect `+"`planning/step_config.json`"+` directly for execution modes, tools,
  skills, learning/KB access, locks, and review metadata

The files are the authority. Do not infer their content from controller prose,
route labels, or prior review text.

## MEASUREMENT
Read run-scoped, evidence-backed measurements where the producing steps already store them (`+"`db/db.sqlite`"+` tables, run outputs) and recent goal observations via `+"`get_goal_metrics`"+`, using shell commands. If no measurement exists, treat missing measurement as a review finding when the workflow clearly needs measurable verification.

## DEPENDENT ARTIFACTS
Review these files/directories when present. Stay read-only:
- `+"`variables/variables.json`"+`: check whether plan-visible hardcoded values should be variables and whether required variables are declared.
- `+"`skills/{folder}/SKILL.md`"+`: read every skill selected at workflow level or enabled per step. Check whether the skill is actually needed, scoped to the right step(s), and not duplicating workflow-specific learnings.
- `+"`learnings/_global/SKILL.md`"+`: check whether HOW-to-run learnings match current step descriptions and do not duplicate task instructions.
- `+"`learnings/{step-id}/.learning_metadata.json`"+`: inspect for every step with learning writes. Check `+"`successful_runs`"+`, `+"`description_hash_runs`"+`, and latest detection history. Flag stale or unnecessary `+"`learnings_access=\"read-write\"`"+` grants that contradict the current step description/config or repeatedly produce no reusable HOW.
- `+"`code/{step-id}/main.py` (code_layout_version=1; legacy: `learnings/{step-id}/main.py`)"+` and `+"`code/{step-id}/script_metadata.json` (legacy: `learnings/{step-id}/script_metadata.json`)"+`: inspect for scripted steps. For `+"`agentic`"+` steps, verify `+"`code/{step-id}/main.py` (code_layout_version=1; legacy: `learnings/{step-id}/main.py`)"+` does NOT exist; if it does, flag it as a stale artifact that should be deleted because agentic never runs or maintains persistent main.py.
- `+"`knowledgebase/context/context.md`"+`: check whether user-supplied runtime context is present when steps appear to rely on chat memory, and verify maintenance-owned notes did not absorb user-owned rules/preferences that belong here.
- `+"`knowledgebase/notes/_index.json`"+` and relevant `+"`knowledgebase/notes/*.md`"+`: check topic registry, stale/duplicated notes, and whether steps that produce domain facts have matching KB contribution contracts.
- `+"`db/README.md`"+`, `+"`db/db.sqlite`"+`, and `+"`db/assets/`"+`: check schema/DDL documentation, table shape, primary keys, upsert rules, indexes, writer ownership, group separation, durable asset metadata/provenance, and report compatibility.
- `+"`db/reports/index.html`"+`: check whether every report view's `+"`window.report.query`"+` SQL reads durable `+"`db/db.sqlite`"+` tables (and references `+"`db/assets/`"+`/KB via `+"`window.report.get`"+`/`+"`fileUrl`"+`) rather than volatile run paths, whether referenced columns exist, and whether derived report helper tables could be collapsed into the report's query (JOIN/GROUP BY).

{{if .TargetRunFolder}}## OPTIONAL RUN EVIDENCE
If useful, read:
- execution outputs under `+"`runs/{{.TargetRunFolder}}/execution/`"+`
- logs under `+"`runs/{{.TargetRunFolder}}/logs/`"+`
- measurement rows the producing steps stored for the target run (`+"`db`"+` tables, run outputs, goal observations)
Use the run evidence to assess whether the plan decisions were justified in practice.
{{end}}

{{if .Focus}}## FOCUS
Prioritize this area: **{{.Focus}}**
{{end}}

## REVIEW CHECKS

Review the plan through these lenses:

1. **Decision justification** — Does each important design choice have a clear reason, or does it look accidental?
2. **Step boundaries** — Are steps split or merged according to the durable-boundary standard above, rather than by raw action/tool-call count?
3. **Step type choice** — Is each step using the right type for the actual job?
4. **Execution mode choice** — Does the declared mode fit the work? Deterministic API/SDK/CLI fetchers, known pagination, stable parsing/normalization/transforms, and mechanical persistence should be scripted from initial design and own an authored/tested `+"`code/{step-id}/main.py` (code_layout_version=1; legacy: `learnings/{step-id}/main.py`)"+`; they do not need 10 prior runs just to select scripted mode. The 10+ representative-run threshold applies only before `+"`lock_code=true`"+`. Judgment, adaptive discovery, and browser/UI work should remain agentic. If an agentic step has `+"`main.py`"+`, flag stale mode debt; if an obviously deterministic fetcher is agentic, flag avoidable LLM execution.
5. **Context flow** — Are dependencies/output contracts minimal, correct, and sufficient? Are there artificial file handoffs or missing dependencies?
6. **Validation & evaluation** — Is the workflow validating and evaluating the things that actually matter for success?
7. **Portability & secrecy** — Does the current plan leak secrets or overfit to one user, machine, run, or folder structure?
8. **Skill quality** — Are installed/selected skills the right reusable capabilities for the steps, scoped correctly via `+"`enabled_skills`"+` where needed, and free of workflow-specific learned state that belongs in `+"`learnings/_global/`"+`?
9. **Learning quality** — For every learning-enabled step, is there a good reason, a concrete objective, direct write method unless explicitly requested otherwise, and no stale/stretched learning scope?
10. **KB quality** — Are KB producer/consumer steps correctly declared, and do notes/index files contain durable domain knowledge rather than run logs or execution recipes?
11. **DB quality** — Are `+"`"+`db/db.sqlite`+"`"+` tables documented, keyed, upsert-safe, group-safe, connected to report consumers (the HTML report `+"`window.report.query`"+` SQL), and correctly referencing durable files under `+"`db/assets/`"+` when assets exist?
12. **Report wiring** — Are the report's `+"`window.report.query`"+` SQL statements backed by durable tables with matching columns and clear ownership? Do they push joins/aggregation into SQL where it would avoid helper tables or a `+"`step-generate-report`"+` flatten step?
13. **Operational risk** — Which current choices are most likely to fail, confuse the agent, or create maintenance burden later?

## OUTPUT FORMAT

### Findings
List only real findings, ordered by severity. If there are none, say so explicitly.

For each finding use:
- **[severity: high|medium|low] [actual-step-id or plan-wide]**: <what decision is weak or risky>
  - **Why this is a problem**: <impact on objective / success criteria / maintainability>
  - **Evidence**: <plan field, mode setting, step config, skill selection/SKILL.md, learning/KB/db/report/eval artifact, run evidence, hardcoded value, etc.>
  - **Better decision**: <what decision should likely replace it, briefly>

### Decisions That Look Sound
Call out 0-5 current decisions that appear well-justified so the builder knows what not to churn.

### Open Risks
List any important uncertainties where the plan might be fine, but the current evidence is too weak to trust the decision yet.

### Priority Rechecks
Give the top 3-5 follow-up checks or tool calls to validate the riskiest decisions next.
`)

var reviewPlanAgentUserTemplate = MustRegisterTemplate("reviewPlanAgentUser", `Critically review the current workflow design and dependent artifacts, then produce a findings-first report.{{if .TargetRunFolder}} Use run evidence from "{{.TargetRunFolder}}" where it helps test whether current decisions are justified.{{end}}{{if .Focus}} Focus especially on: {{.Focus}}{{end}}`)

var reviewWorkflowTimingAgentSystemTemplate = MustRegisterTemplate("reviewWorkflowTimingAgentSystem", `# Workflow Timing Review Agent

You are a read-only reviewer of workflow runtime performance. Your job is to determine:
1. where the workflow is actually spending time
2. which latency is necessary versus wasteful
3. how to make the workflow faster without compromising the objective or success criteria

{{.DBGuidance}}

This is a **read-only review**:
- do not modify files
- do not recommend speedups that obviously sacrifice the real goal
- separate evidence-backed bottlenecks from speculation

## RULES
1. **Read-Only**: Do NOT modify files.
2. **Evidence first**: Use run_metadata, execution summaries, timing files, and conversation logs from a real run.
3. **Read timing in the right order**: workflow wall-clock first, then step summaries, then detailed timing for the slowest steps, then conversation logs only to explain why the slow call happened.
4. **Separate bottleneck classes**:
   - LLM latency
   - tool latency
   - orchestration / validation / file IO overhead
   - too many step boundaries or handoffs
   - unclear descriptions causing extra tool calls, retries, or thinking loops
5. **Protect the objective**: A speedup is only good if it still preserves the stated objective and success criteria. Flag risky suggestions clearly.
6. **Recommend the smallest effective fix**: Prefer a tighter description over a structural plan change when the issue is prompt ambiguity. Prefer a structural change only when the current plan shape is causing waste.
7. **If no target run folder is preset**: Use shell to find the latest meaningful run evidence. If no run evidence exists, say you cannot assess real workflow speed yet.

## CONTEXT

- **Workspace**: {{.WorkspacePath}}
{{if .WorkflowObjective}}- **Workflow Objective**: {{.WorkflowObjective}}{{else}}- **Workflow Objective**: ⚠️ NOT SET — treat missing objective as a top-level finding{{end}}
{{if .WorkflowSuccessCriteria}}- **Success Criteria**: {{.WorkflowSuccessCriteria}}{{else}}- **Success Criteria**: ⚠️ NOT SET — treat missing success criteria as a top-level finding{{end}}
{{if .TargetRunFolder}}- **Target Run Folder**: {{.TargetRunFolder}}{{else}}- **Target Run Folder**: not preset — find the latest meaningful run evidence before judging speed{{end}}

## PATH DISCIPLINE

For shell commands, use absolute workspace paths: `+"`{{.AbsWorkspacePath}}/...`"+`. For workspace file tools that expect workspace-relative paths, use `+"`{{.WorkspacePath}}/...`"+`. Do not use bare `+"`runs/...`"+`, `+"`evaluation/...`"+`, or similar paths unless a tool explicitly requires a path relative to the workflow root. Do not use host paths outside workspace-docs.

{{if .PlanJSON}}## CURRENT PLAN
`+"```json\n{{.PlanJSON}}\n```"+`
{{else}}Read the plan from `+"`planning/plan.json`"+` using shell commands before starting the review.{{end}}

{{if .StepConfigSummary}}## STEP CONFIG SUMMARY
{{.StepConfigSummary}}
{{end}}

## REQUIRED EVIDENCE TO READ

1. Read `+"`runs/<target>/run_metadata.json`"+` if present.
2. Read the step execution summaries under `+"`runs/<target>/logs/<step-id>/execution/`"+` to rank the slowest step attempts using:
   - `+"`duration_ms`"+`
   - `+"`llm_duration_ms`"+`
   - `+"`tool_duration_ms`"+`
3. For the slowest step attempts, read the matching `+"`*-timing.json`"+` files and interpret:
   - `+"`agent.duration_ms`"+` as full wall-clock
   - `+"`llm.total_duration_ms`"+` as total model time
   - `+"`tools.total_duration_ms`"+` as total tool time
   - `+"`tools.calls[]`"+` as per-tool breakdown
   - `+"`phase`"+` to tell the files apart: the `+"`execution-attempt-*`"+` files are the step's own work, while `+"`reflection-timing.json`"+` is its post-completion reflection turn. A step's true elapsed cost is execution PLUS reflection — reporting execution alone understates it, and reflection has measured around a fifth of total LLM time on a real workflow. Attribute them separately: a slow execution phase is a step-instruction problem, a slow reflection phase is a learning/KB objective problem, and the two have different fixes.
   - `+"`wrote_learnings`"+` / `+"`wrote_kb`"+` on a reflection file as the yield question — a reflection turn that cost real time while writing to neither store is pure overhead, and the fix is to sharpen the objective or drop the step to `+"`learnings_access: \"read\"`"+`, not to speed the turn up.
4. Read conversation logs only after the timing files show where the time went.
5. Read the producing steps' stored measurements (and goal observations via `+"`get_goal_metrics`"+`) if they help determine whether a proposed speedup would threaten success criteria.

{{if .Focus}}## FOCUS
Prioritize this area: **{{.Focus}}**
{{end}}

## REVIEW TASKS

1. Identify the workflow-level wall-clock and whether one group or one step dominates.
2. Identify the top latency bottlenecks by class:
   - slow model calls
   - slow tools
   - orchestration/overhead gap
   - unnecessary step boundaries / file handoffs
   - ambiguous descriptions causing extra loops
3. For each major bottleneck, decide the safest improvement lever:
   - tighten a step description
   - reduce tool thrash
   - merge/remove/reorder steps
   - route work differently
   - change model/config only if the current tier is clearly overkill
4. Distinguish:
   - **safe speedups** that should preserve outcome quality
   - **risky speedups** that could harm success criteria
5. Prefer concrete recommendations over generic advice.

## OUTPUT FORMAT

### Verdict
- **Speed status**: fast enough | mixed | too slow | cannot assess
- **Main bottleneck class**: llm | tools | orchestration | plan shape | mixed
- **Confidence**: high | medium | low

### Biggest Bottlenecks
List the top 3-5 bottlenecks.

For each:
- **[workflow|group|step-id]**
  - **Where time went**: <wall-clock, llm, tools, overhead>
  - **Evidence**: <file/field values>
  - **Why it happened**: <actual cause, not vague speculation>

### Recommended Speedups
Group recommendations into:
- **Description / prompt changes**
- **Plan / step-shape changes**
- **Model / tool / config changes**

For each recommendation:
- **Change**
- **Expected impact**: high | medium | low
- **Risk to success criteria**: low | medium | high
- **Why**

### Priority Next Actions
Give the top 3-5 next actions. Prefer concrete tool calls where possible.
`)

var reviewWorkflowTimingAgentUserTemplate = MustRegisterTemplate("reviewWorkflowTimingAgentUser", `Review workflow latency and recommend how to make it faster without weakening the objective or success criteria.{{if .TargetRunFolder}} Use run evidence from "{{.TargetRunFolder}}".{{else}} Find the latest meaningful run evidence first.{{end}}{{if .Focus}} Focus especially on: {{.Focus}}{{end}}`)

var reviewWorkflowCostsAgentSystemTemplate = MustRegisterTemplate("reviewWorkflowCostsAgentSystem", `# Workflow Cost Review Agent

You are a read-only reviewer of workflow cost efficiency. Your job is to determine:
1. where the workflow is spending tokens and money
2. which spend is necessary versus wasteful
3. how to reduce cost without compromising the objective or success criteria

{{.DBGuidance}}

This is a **read-only review**:
- do not modify files
- do not recommend cheaper settings that obviously undermine the real goal
- separate necessary spend from avoidable waste

## RULES
1. **Read-Only**: Do NOT modify files.
2. **Evidence first**: Use actual cost ledgers, `+"`get_cost_summary`"+`, execution summaries, and run/eval evidence from a real run.
3. **Protect the objective**: First preserve success; then reduce cost. A cheap failure is not an optimization.
4. **Separate cost sources**:
   - expensive model usage
   - too many retries or loops
   - too many step boundaries / handoffs
   - unnecessary tool calls
   - expensive evaluation that is not measuring the real objective well
   - background learning / KB updates / fix loops that are still unlocked without enough benefit
5. **Recommend the smallest effective fix**: Prefer a tighter description or config change over a structural plan change when the issue is local. Recommend structural changes only when the plan shape itself is creating waste.
6. **If no target run folder is preset**: Use shell to find the latest meaningful cost/run evidence. If no cost evidence exists, say you cannot assess real workflow cost yet.

## CONTEXT

- **Workspace**: {{.WorkspacePath}}
{{if .WorkflowObjective}}- **Workflow Objective**: {{.WorkflowObjective}}{{else}}- **Workflow Objective**: ⚠️ NOT SET — treat missing objective as a top-level finding{{end}}
{{if .WorkflowSuccessCriteria}}- **Success Criteria**: {{.WorkflowSuccessCriteria}}{{else}}- **Success Criteria**: ⚠️ NOT SET — treat missing success criteria as a top-level finding{{end}}
{{if .TargetRunFolder}}- **Target Run Folder**: {{.TargetRunFolder}}{{else}}- **Target Run Folder**: not preset — find the latest meaningful cost/run evidence before judging cost{{end}}

## PATH DISCIPLINE

For shell commands, use absolute workspace paths: `+"`{{.AbsWorkspacePath}}/...`"+`. For workspace file tools that expect workspace-relative paths, use `+"`{{.WorkspacePath}}/...`"+`. Do not use bare `+"`runs/...`"+`, `+"`evaluation/...`"+`, `+"`costs/...`"+`, or similar paths unless a tool explicitly requires a path relative to the workflow root. Do not use host paths outside workspace-docs.

{{if .PlanJSON}}## CURRENT PLAN
`+"```json\n{{.PlanJSON}}\n```"+`
{{else}}Read the plan from `+"`planning/plan.json`"+` using shell commands before starting the review.{{end}}

{{if .StepConfigSummary}}## STEP CONFIG SUMMARY
{{.StepConfigSummary}}
{{end}}

## REQUIRED EVIDENCE TO READ

1. Call `+"`get_cost_summary(run_folder=\"<target>\")`"+` for the target run if available.
2. Read cost ledgers under:
   - `+"`costs/phase/token_usage.json`"+`
   - `+"`costs/execution/`"+`
3. Read step execution summaries under `+"`runs/<target>/logs/<step-id>/execution/`"+` to correlate cost with retries, LLM calls, and tool calls.
4. Read the producing steps' stored measurements when measurement cost might be disproportionate or misaligned to the real goal.
5. Read enough run evidence to judge whether a lower-cost recommendation would threaten success criteria.

{{if .Focus}}## FOCUS
Prioritize this area: **{{.Focus}}**
{{end}}

## REVIEW TASKS

1. Identify the biggest cost drivers by step, model, and phase.
2. Distinguish:
   - **necessary spend** that is directly supporting success
   - **avoidable waste** from retries, ambiguous descriptions, too many handoffs, or overpowered models
3. Decide the safest reduction lever for each major cost driver:
   - tighten a description to reduce retries/tool calls
   - merge/remove/reorder steps
   - reduce evaluation waste or misaligned eval breadth
   - change model/config only when success evidence suggests it is safe
   - lock mature learnings/code/knowledgebase only when the current evidence supports freezing them
4. Distinguish:
   - **safe cost reductions** that should preserve outcome quality
   - **risky cost reductions** that could hurt success criteria
5. Prefer concrete recommendations over generic advice.

## OUTPUT FORMAT

### Verdict
- **Cost status**: efficient | mixed | too expensive | cannot assess
- **Main cost driver**: model usage | retries/loops | plan shape | evaluation | mixed
- **Confidence**: high | medium | low

### Biggest Cost Drivers
List the top 3-5 cost drivers.

For each:
- **[phase|group|step-id|model]**
  - **Cost evidence**: <tool output, ledger values, call counts>
  - **Why cost is high**: <actual cause>
  - **Necessary vs wasteful**: <one sentence>

### Recommended Cost Reductions
Group recommendations into:
- **Description / prompt changes**
- **Plan / step-shape changes**
- **Model / tool / config changes**

For each recommendation:
- **Change**
- **Expected savings**: high | medium | low
- **Risk to success criteria**: low | medium | high
- **Why**

### Priority Next Actions
Give the top 3-5 next actions. Prefer concrete tool calls where possible.
`)

var reviewWorkflowCostsAgentUserTemplate = MustRegisterTemplate("reviewWorkflowCostsAgentUser", `Review workflow cost and recommend how to reduce it without weakening the objective or success criteria.{{if .TargetRunFolder}} Use run and cost evidence from "{{.TargetRunFolder}}".{{else}} Find the latest meaningful run and cost evidence first.{{end}}{{if .Focus}} Focus especially on: {{.Focus}}{{end}}`)

// --- review_step_code templates ---

var reviewStepCodeAgentSystemTemplate = MustRegisterTemplate("reviewStepCodeAgentSystem", `# Step Code Review Agent

{{.MainPyAuthoringRules}}

You are a code reviewer that checks whether saved Python scripts (main.py) still match their step descriptions AND comply with the authoring rules above. Over time, step descriptions get updated with new requirements, but the saved scripts don't get regenerated — causing silent drift. And manual patches sometimes bypass the rules.

Your job is to compare each step's **current description** with its **saved main.py** and identify:
1. **Missing functionality** — things the description requires that main.py doesn't do
2. **Extra functionality** — things main.py does that are no longer in the description
3. **Incorrect behavior** — things main.py does differently than what the description specifies
4. **Anti-patterns** — hardcoded paths, hardcoded credentials, missing env var usage, non-portable code
5. **Fabricated data** — script writes output data without actually fetching/computing it from real sources (MCP tools, APIs, input files)
6. **MCP tool usage** — incorrect server/tool names in call_mcp(), missing API discovery, guessed parameter names instead of using get_api_spec
7. **Persistent-store confusion** — script writes to the wrong store. Stores: `+"`"+`learnings/`+"`"+` (HOW to run, updated by direct step-learning turns), `+"`knowledgebase/context/`"+` (user-supplied runtime context, never step-written), `+"`"+`knowledgebase/notes/`+"`"+` (workflow-discovered durable narrative observations — written by the post-step KB update agent in agent mode, or by step agents in direct-write mode), `+"`"+`db/db.sqlite`+"`"+` (structured durable SQLite tables — step-owned; upsert via `+"`INSERT ... ON CONFLICT`"+`, never recreate a table), and `+"`db/assets/`"+` (durable media/file assets referenced by db rows/reports). Flag scripts that write directly under `+"`"+`knowledgebase/notes/`+"`"+` outside of direct-write mode, write under `+"`knowledgebase/context/`"+`, stash durable cross-run state in per-step output files when it belongs in `+"`"+`db/`+"`"+`, store large assets in the DB instead of `+"`db/assets/`"+`, or DROP/recreate `+"`"+`db/db.sqlite`+"`"+` tables instead of upserting.

{{.DBGuidance}}

This is a **read-only review** — do not modify any files.

## RULES
1. **Read-Only**: Do NOT modify any files.
2. **Be specific**: Reference exact line numbers in main.py and exact phrases from the description.
3. **Severity matters**: Flag critical drift (wrong output, missing steps) higher than cosmetic issues.
4. **Consider reusability**: The script runs across different groups/users — flag anything that would break portability.
5. **Check output contract**: Verify that main.py produces the exact output files, field names, and formats specified in the validation schema.
6. **Check data authenticity**: Verify that every value written to output files traces back to a real data source (MCP tool call, API response, or input file). Flag any hardcoded/fabricated data in output construction.
7. **Check MCP tool usage**: Verify that call_mcp() calls use consistent server/tool naming. Flag suspicious tool names that look guessed rather than discovered via get_api_spec.
8. **Check browser automation quality**: For scripts using agent_browser, verify:
   - **Browser-heavy scripted is usually the wrong fit.** If a browser-enabled step has a saved `+"`main.py`"+`, flag it unless the user explicitly requested scripted browser execution AND the script uses durable selectors, state-driven waits, fresh snapshots, and has evidence of stability across runs. Browser/UI automation should generally remain `+"`agentic`"+` so the agent can adapt to live UI state, auth, dynamic selectors, pagination, and third-party page timing.
   - **agentic must not keep main.py.** If a step is declared `+"`agentic`"+` but still has `+"`code/{step-id}/main.py` (code_layout_version=1; legacy: `learnings/{step-id}/main.py`)"+`, flag the file as stale artifact debt. Recommend deleting it and clearing `+"`lock_code`"+`; agentic does not run or maintain persistent scripts.
   - **Selectors are DURABLE, not refs.** Hardcoded string refs like `+"`'abc123'`"+` or `+"`'@e1'`"+` in main.py are BUGS — refs are session-local. The durable alternatives are (in priority order): data-testid / hand-written id / aria-label / role+name / get_by_label|placeholder|text. Flag any ref that appears as a literal string (not a variable parsed from a current-run snapshot).
   - Uses agent_browser `+"`snapshot`"+` before interacting — never clicks or types blindly
   - Ref-based interaction is acceptable ONLY when the ref value is parsed from a snapshot taken earlier in the SAME run (`+"`ref = extract_ref(snapshot, role=..., name=...)` then `browser('click', [ref])`"+`). Hardcoded refs in main.py must be flagged.
   - Does NOT use raw page JavaScript for actions when a dedicated agent_browser command exists. Read-only eval for discovery is fine.
   - Uses wait loops that check page state via agent_browser `+"`snapshot`"+`, not hardcoded time.sleep()
   - Prints diagnostic snapshots/state on failure so the fix loop can debug what went wrong
   - Avoids structural CSS selectors (nth-child chains, deep descendant paths) — flag those in favor of durable hooks

## CORRECT BROWSER AUTOMATION PATTERNS

**The invariant, independent of tool choice:** the selectors a saved `+"`main.py`"+` carries must be DETERMINISTIC across future runs — i.e. on every replay they must resolve to the same element, even across browser restarts, page rebuilds, deploys that change class names / DOM shape / React keys. Refs (`+"`'abc123'`"+`, `+"`'@e1'`"+`) are session-local — a snapshot assigns them fresh each run, so hardcoded refs are the opposite of deterministic. **Any ref value that appears as a string literal in main.py is a bug** — the replay tomorrow will click the wrong thing.

Deterministic selectors that survive future runs (in descending durability): data-testid → hand-written id/name → aria-label → role+name → get_by_label / get_by_placeholder / get_by_text. Structural CSS (`+"`nth-child`"+`, deep descendant chains, auto-generated classes like `+"`css-8xy3zb`"+`) is NOT deterministic — DOM rearrangements or style-system rebuilds break it.

Refs are fine *in-session* (parse a snapshot, use the ref for the next call in the same run). They are never fine *persisted*. Flag any saved script where the ref is a hardcoded string.

### Agent Browser (tool='agent_browser')

Direct tool call (NOT via call_mcp). Uses command + args pattern.

CDP mode: call agent_browser(command='tab', args=[], session='main') to get the selected tab hint. If none is selected, create a stable labeled tab with the tab command. Call open with URL-only args. Then include the chosen tab inline in later page actions, e.g. args=['tab', 't1', '-i'] for snapshot and args=['tab', 't1', ref] for click. Calls without an inline tab are rejected in shared-CDP mode except open, which uses the selected tab and passes only the URL to agent-browser.

**Correct patterns:**
`+"```"+`python
# CORRECT: Durable CSS selector (id / aria-label / testid) as the args target
agent_browser(command='click', args=['tab', 't1', '#panAdhaarUserId'], session='main')
agent_browser(command='fill', args=['tab', 't1', '[aria-label="Password"]', password], session='main')
agent_browser(command='tab', args=['t1'], session='main')
agent_browser(command='open', args=['https://example.com'], session='main')

# CORRECT: Snapshot+ref derived AT RUNTIME (the @e1 is parsed from the snapshot variable)
snapshot = agent_browser(command='snapshot', args=['tab', 't1', '-i'], session='main')
ref = extract_ref(snapshot, role='button', name='Continue')  # your helper returns '@eN' parsed from snapshot
agent_browser(command='click', args=['tab', 't1', ref], session='main')

# CORRECT: Wait by polling
agent_browser(command='wait', args=['tab', 't1', 'text', 'Dashboard'], session='main')
`+"```"+`

**Anti-patterns for agent_browser (WRONG):**
`+"```"+`python
# WRONG: Hardcoded @e1 / @e2 ref in main.py — ref is session-local
agent_browser(command='click', args=['@e1'], session='main')
agent_browser(command='fill', args=['@e2', 'search text'], session='main')

# WRONG: eval for actions
agent_browser(command='eval', args=['document.querySelector("button").click()'], session='main')

# WRONG: Structural CSS selector that will break on DOM shape change
agent_browser(command='click', args=['.form > div:nth-child(3)'], session='main')
`+"```"+`

## CONTEXT

- **Workspace**: {{.WorkspacePath}}
{{if .WorkflowObjective}}- **Workflow Objective**: {{.WorkflowObjective}}{{end}}

## PATH DISCIPLINE

For shell commands, use absolute workspace paths: `+"`{{.AbsWorkspacePath}}/...`"+`. For workspace file tools that expect workspace-relative paths, use `+"`{{.WorkspacePath}}/...`"+`. Do not use bare `+"`planning/...`"+`, `+"`evaluation/...`"+`, `+"`learnings/...`"+`, or similar paths unless a tool explicitly requires a path relative to the workflow root. Do not use host paths outside workspace-docs.

## STEPS TO REVIEW

{{.StepsToReview}}

## OUTPUT FORMAT

For each step reviewed, output:

### [step-id]: <step title>
- **Status**: `+"`IN_SYNC`"+` | `+"`DRIFTED`"+` | `+"`NO_SCRIPT`"+`
- **Findings** (if drifted):
  1. **[severity: high|medium|low]**: <what is mismatched>
     - **Description says**: <relevant excerpt>
     - **Script does**: <what the code actually does, with line reference>
     - **Fix needed**: <brief suggestion>

### Summary
- Total steps reviewed: N
- In sync: N
- Drifted: N
- No script: N
- **Top priority fixes**: List the 3 most critical drifts that should be addressed first.
- **Stale code-lock warning**: For every step marked `+"`"+`DRIFTED`+"`"+` whose `+"`"+`step_config`+"`"+` has `+"`"+`lock_code=true`+"`"+`, explicitly call out that the frozen main.py may no longer match the description, and the builder should `+"`"+`update_step_config(step_id, lock_code=false, description_reviewed=false)`+"`"+` before regenerating. Separately reassess whether `+"`"+`learnings_access`+"`"+` should remain `+"`"+`read-write`+"`"+`.
`)

var reviewStepCodeAgentUserTemplate = MustRegisterTemplate("reviewStepCodeAgentUser", `Review the saved main.py scripts against their step descriptions and report any drift.{{if .Focus}} Focus especially on: {{.Focus}}{{end}}

For each step listed above, carefully compare the description with the script and check:
1. Does the script do everything the description asks?
2. Does the script produce the correct output format?
3. Are there hardcoded values that should use environment variables?
4. Would the script work correctly for a different group/user?
5. Does the script actually fetch/compute data from real sources, or does it fabricate/hardcode output values?
6. Are MCP tool calls using correct server names, tool names, and parameters?
7. For browser automation: does the script use agent_browser snapshots plus fresh refs or durable selectors, or does it blindly inject JavaScript?`)

// ============================================================================
// Background Task Agent — standalone agent for run_in_background tool
// ============================================================================

var backgroundTaskAgentSystemTemplate = MustRegisterTemplate("backgroundTaskAgentSystem", `# Background Task Agent

You are a background agent spawned by the workflow builder to perform a specific task. You have access to the same workspace tools as the workflow execution agents.

**Workspace folder:** {{.WorkspacePath}}

## Path Discipline

For shell commands, use absolute workspace paths: `+"`{{.AbsWorkspacePath}}/...`"+`. For workspace file tools that expect workspace-relative paths, use `+"`{{.WorkspacePath}}/...`"+`. Do not use bare `+"`runs/...`"+`, `+"`evaluation/...`"+`, `+"`planning/...`"+`, `+"`db/...`"+`, or similar paths unless a tool explicitly requires a path relative to the workflow root. Do not use host paths outside workspace-docs.

{{.DBGuidance}}

## Instructions
Complete the task described in the user message below. Be thorough and specific in your output.
When you finish, summarize what you did and any important findings.

{{.SkillPrompt}}
{{.SecretPrompt}}
{{.BrowserPrompt}}
`)

var backgroundTaskAgentUserTemplate = MustRegisterTemplate("backgroundTaskAgentUser", `{{.Instruction}}`)

// StepCodeReviewAgent compares step descriptions with their saved main.py scripts to detect drift.
type StepCodeReviewAgent struct {
	*agents.BaseOrchestratorAgent
}

func newStepCodeReviewAgent(config *agents.OrchestratorAgentConfig, logger loggerv2.Logger, tracer observability.Tracer, eventBridge mcpagent.AgentEventListener) *StepCodeReviewAgent {
	baseAgent := agents.NewBaseOrchestratorAgentWithEventBridge(config, logger, tracer, agents.TodoPlannerExecutionQAAgentType, eventBridge)
	return &StepCodeReviewAgent{BaseOrchestratorAgent: baseAgent}
}

func (agent *StepCodeReviewAgent) Execute(ctx context.Context, templateVars map[string]string, conversationHistory []llmtypes.MessageContent) (string, []llmtypes.MessageContent, error) {
	if agent.BaseOrchestratorAgent.BaseAgent() == nil || agent.BaseOrchestratorAgent.BaseAgent().Agent() == nil {
		return "", nil, fmt.Errorf("agent not initialized")
	}
	if _, has := templateVars["MainPyAuthoringRules"]; !has {
		templateVars["MainPyAuthoringRules"] = BuildMainPyAuthoringRules() + BrowserAuthoringRulesFromTemplateVars(templateVars)
	}
	templateVars["DBGuidance"] = BuildManagedWorkflowDBGuidance(DBAccessRead)
	var systemPrompt, userMessage strings.Builder
	if err := reviewStepCodeAgentSystemTemplate.Execute(&systemPrompt, templateVars); err != nil {
		return "", nil, err
	}
	if err := reviewStepCodeAgentUserTemplate.Execute(&userMessage, templateVars); err != nil {
		return "", nil, err
	}
	inputProcessor := func(map[string]string) string { return userMessage.String() }
	result, updatedHistory, err := agent.ExecuteWithTemplateValidation(ctx, templateVars, inputProcessor, conversationHistory, struct{}{}, systemPrompt.String(), true)
	if err != nil {
		return "", nil, err
	}
	return result, updatedHistory, nil
}

// WorkflowPlanReviewAgent critically reviews current workflow design and dependent artifacts without modifying files.
type WorkflowPlanReviewAgent struct {
	*agents.BaseOrchestratorAgent
}

func (agent *WorkflowPlanReviewAgent) Execute(ctx context.Context, templateVars map[string]string, conversationHistory []llmtypes.MessageContent) (string, []llmtypes.MessageContent, error) {
	if agent.BaseOrchestratorAgent.BaseAgent() == nil || agent.BaseOrchestratorAgent.BaseAgent().Agent() == nil {
		return "", nil, fmt.Errorf("agent not initialized")
	}
	templateVars["DBGuidance"] = BuildManagedWorkflowDBGuidance(DBAccessRead)
	var systemPrompt, userMessage strings.Builder
	if err := reviewPlanAgentSystemTemplate.Execute(&systemPrompt, templateVars); err != nil {
		return "", nil, err
	}
	if err := reviewPlanAgentUserTemplate.Execute(&userMessage, templateVars); err != nil {
		return "", nil, err
	}
	inputProcessor := func(map[string]string) string { return userMessage.String() }
	result, updatedHistory, err := agent.ExecuteWithTemplateValidation(ctx, templateVars, inputProcessor, conversationHistory, struct{}{}, systemPrompt.String(), true)
	if err != nil {
		return "", nil, err
	}
	return result, updatedHistory, nil
}

// WorkflowTimingReviewAgent reviews actual run latency and speedup opportunities.
type WorkflowTimingReviewAgent struct {
	*agents.BaseOrchestratorAgent
}

func newWorkflowTimingReviewAgent(config *agents.OrchestratorAgentConfig, logger loggerv2.Logger, tracer observability.Tracer, eventBridge mcpagent.AgentEventListener) *WorkflowTimingReviewAgent {
	baseAgent := agents.NewBaseOrchestratorAgentWithEventBridge(config, logger, tracer, agents.TodoPlannerExecutionQAAgentType, eventBridge)
	return &WorkflowTimingReviewAgent{BaseOrchestratorAgent: baseAgent}
}

func (agent *WorkflowTimingReviewAgent) Execute(ctx context.Context, templateVars map[string]string, conversationHistory []llmtypes.MessageContent) (string, []llmtypes.MessageContent, error) {
	if agent.BaseOrchestratorAgent.BaseAgent() == nil || agent.BaseOrchestratorAgent.BaseAgent().Agent() == nil {
		return "", nil, fmt.Errorf("agent not initialized")
	}
	templateVars["DBGuidance"] = BuildManagedWorkflowDBGuidance(DBAccessRead)
	var systemPrompt, userMessage strings.Builder
	if err := reviewWorkflowTimingAgentSystemTemplate.Execute(&systemPrompt, templateVars); err != nil {
		return "", nil, err
	}
	if err := reviewWorkflowTimingAgentUserTemplate.Execute(&userMessage, templateVars); err != nil {
		return "", nil, err
	}
	inputProcessor := func(map[string]string) string { return userMessage.String() }
	result, updatedHistory, err := agent.ExecuteWithTemplateValidation(ctx, templateVars, inputProcessor, conversationHistory, struct{}{}, systemPrompt.String(), true)
	if err != nil {
		return "", nil, err
	}
	return result, updatedHistory, nil
}

// WorkflowCostReviewAgent reviews actual run costs and cost-reduction opportunities.
type WorkflowCostReviewAgent struct {
	*agents.BaseOrchestratorAgent
}

func newWorkflowCostReviewAgent(config *agents.OrchestratorAgentConfig, logger loggerv2.Logger, tracer observability.Tracer, eventBridge mcpagent.AgentEventListener) *WorkflowCostReviewAgent {
	baseAgent := agents.NewBaseOrchestratorAgentWithEventBridge(config, logger, tracer, agents.TodoPlannerExecutionQAAgentType, eventBridge)
	return &WorkflowCostReviewAgent{BaseOrchestratorAgent: baseAgent}
}

func (agent *WorkflowCostReviewAgent) Execute(ctx context.Context, templateVars map[string]string, conversationHistory []llmtypes.MessageContent) (string, []llmtypes.MessageContent, error) {
	if agent.BaseOrchestratorAgent.BaseAgent() == nil || agent.BaseOrchestratorAgent.BaseAgent().Agent() == nil {
		return "", nil, fmt.Errorf("agent not initialized")
	}
	templateVars["DBGuidance"] = BuildManagedWorkflowDBGuidance(DBAccessRead)
	var systemPrompt, userMessage strings.Builder
	if err := reviewWorkflowCostsAgentSystemTemplate.Execute(&systemPrompt, templateVars); err != nil {
		return "", nil, err
	}
	if err := reviewWorkflowCostsAgentUserTemplate.Execute(&userMessage, templateVars); err != nil {
		return "", nil, err
	}
	inputProcessor := func(map[string]string) string { return userMessage.String() }
	result, updatedHistory, err := agent.ExecuteWithTemplateValidation(ctx, templateVars, inputProcessor, conversationHistory, struct{}{}, systemPrompt.String(), true)
	if err != nil {
		return "", nil, err
	}
	return result, updatedHistory, nil
}

// runReviewPlanAgent performs a read-only critical review of the current workflow design and dependent artifacts.
// prepareReadOnlyBackgroundAgentTools is the common tool surface for
// read-only workshop reviewers. Every such agent can inspect workflow state
// through the managed query tool; none receives database mutation authority.
func prepareReadOnlyBackgroundAgentTools(base *orchestrator.BaseOrchestrator) ([]llmtypes.Tool, map[string]interface{}) {
	if base == nil {
		return nil, nil
	}
	enabledTools := []string{
		"workspace_advanced:execute_shell_command",
		"workflow_db:query_workflow_db",
		"workflow_costs:query_workflow_costs",
	}
	enabledTools = append(enabledTools, orchestrator.AgentWorksChatHumanToolSelections("builder")...)
	return orchestrator.FilterCustomToolsByCategory(
		base.WorkspaceTools,
		base.WorkspaceToolExecutors,
		enabledTools,
	)
}

// runReviewWorkflowTimingAgent reviews actual run latency and speedup opportunities.
func (iwm *InteractiveWorkshopManager) runReviewWorkflowTimingAgent(ctx context.Context, targetRunFolder string, focus string) (string, error) {
	workspacePath := iwm.controller.GetWorkspacePath()
	logger := iwm.controller.GetLogger()

	planJSON := ""
	if planContent, err := iwm.controller.ReadWorkspaceFile(ctx, "planning/plan.json"); err == nil {
		planJSON = planContent
	}

	stepConfigSummary := ""
	if stepConfigs, err := iwm.controller.ReadStepConfigs(ctx); err == nil && len(stepConfigs) > 0 {
		var sb strings.Builder
		planSteps := workshopPlanStepIndex(ctx, iwm.controller)
		for _, sc := range stepConfigs {
			mode := executionModeLabel(isScriptedStep(planSteps[sc.ID], sc.AgentConfigs))
			successfulRuns := 0
			learningsAccess := LearningsAccessRead
			lockCode := false
			if sc.AgentConfigs != nil {
				if sc.AgentConfigs.SuccessfulRuns != nil {
					successfulRuns = *sc.AgentConfigs.SuccessfulRuns
				}
				learningsAccess = resolveLearningsAccess(sc.AgentConfigs)
				if sc.AgentConfigs.LockCode != nil {
					lockCode = *sc.AgentConfigs.LockCode
				}
			}
			sb.WriteString(fmt.Sprintf("- %s: mode=%s, successful_runs=%d, learnings_access=%s, lock_code=%v\n", sc.ID, mode, successfulRuns, learningsAccess, lockCode))
		}
		stepConfigSummary = sb.String()
	}

	workflowObjective, workflowSuccessCriteria := iwm.controller.ResolveWorkflowObjective(ctx)

	readPaths := []string{
		workspacePath,
		fmt.Sprintf("%s/runs", workspacePath),
		fmt.Sprintf("%s/planning", workspacePath),
		fmt.Sprintf("%s/learnings", workspacePath),
		fmt.Sprintf("%s/code", workspacePath),
		fmt.Sprintf("%s/evaluation", workspacePath),
	}
	iwm.controller.SetWorkspacePathForFolderGuard(readPaths, []string{})

	llmConfigToUse := iwm.controller.selectPulseLLM("review_workflow_timing agent")
	if llmConfigToUse == nil {
		return "", fmt.Errorf("no valid LLM configuration for review_workflow_timing agent")
	}

	config := iwm.createUnattendedWorkshopAgentConfig("review-workflow-timing-agent", 60, llmConfigToUse, "review_workflow_timing agent")
	// Isolate in a fresh tmp dir; don't project CLAUDE.md/.claude into the
	// builder's live workflow folder. See improve-db-agent above.
	config.IsolateCodingAgentWorkspace = true
	config.UseCodeExecutionMode = requiresCodeExecutionForProvider(iwm.presetLLM)
	config.ServerNames = []string{mcpclient.NoServers}
	defer iwm.configureWorkshopToolAgentSession(config, "review-workflow-timing", readPaths, []string{})()

	phaseTools, phaseExecutors := prepareReadOnlyBackgroundAgentTools(iwm.controller.BaseOrchestrator)
	createAgentFunc := func(cfg *agents.OrchestratorAgentConfig, log loggerv2.Logger, tracer observability.Tracer, eventBridge mcpagent.AgentEventListener) agents.OrchestratorAgent {
		return newWorkflowTimingReviewAgent(cfg, log, tracer, eventBridge)
	}
	agent, err := iwm.controller.CreateAndSetupStandardAgentWithConfig(
		ctx, config, "review-workflow-timing", 0, 0, "review-workflow-timing",
		createAgentFunc, phaseTools, phaseExecutors, true,
	)
	if err != nil {
		return "", fmt.Errorf("failed to create review_workflow_timing agent: %w", err)
	}

	templateVars := map[string]string{
		"WorkspacePath":           workspacePath,
		"AbsWorkspacePath":        absPromptWorkspacePath(workspacePath),
		"TargetRunFolder":         targetRunFolder,
		"PlanJSON":                planJSON,
		"StepConfigSummary":       stepConfigSummary,
		"WorkflowObjective":       workflowObjective,
		"WorkflowSuccessCriteria": workflowSuccessCriteria,
		"Focus":                   focus,
		"SessionID":               iwm.sessionID,
		"WorkflowID":              iwm.workflowID,
	}

	logger.Info(fmt.Sprintf("⏱️ Running review_workflow_timing agent (target_run_folder: %q, objective: %q, success_criteria: %q, focus: %q)", targetRunFolder, workflowObjective, workflowSuccessCriteria, focus))
	result, _, err := agent.Execute(ctx, templateVars, nil)
	if err != nil {
		return "", fmt.Errorf("review_workflow_timing agent failed: %w", err)
	}
	return result, nil
}

// runReviewWorkflowCostsAgent reviews actual run costs and cost-reduction opportunities.
func (iwm *InteractiveWorkshopManager) runReviewWorkflowCostsAgent(ctx context.Context, targetRunFolder string, focus string) (string, error) {
	workspacePath := iwm.controller.GetWorkspacePath()
	logger := iwm.controller.GetLogger()

	planJSON := ""
	if planContent, err := iwm.controller.ReadWorkspaceFile(ctx, "planning/plan.json"); err == nil {
		planJSON = planContent
	}

	stepConfigSummary := ""
	if stepConfigs, err := iwm.controller.ReadStepConfigs(ctx); err == nil && len(stepConfigs) > 0 {
		var sb strings.Builder
		planSteps := workshopPlanStepIndex(ctx, iwm.controller)
		for _, sc := range stepConfigs {
			mode := executionModeLabel(isScriptedStep(planSteps[sc.ID], sc.AgentConfigs))
			successfulRuns := 0
			learningsAccess := LearningsAccessRead
			lockCode := false
			if sc.AgentConfigs != nil {
				if sc.AgentConfigs.SuccessfulRuns != nil {
					successfulRuns = *sc.AgentConfigs.SuccessfulRuns
				}
				learningsAccess = resolveLearningsAccess(sc.AgentConfigs)
				if sc.AgentConfigs.LockCode != nil {
					lockCode = *sc.AgentConfigs.LockCode
				}
			}
			sb.WriteString(fmt.Sprintf("- %s: mode=%s, successful_runs=%d, learnings_access=%s, lock_code=%v\n", sc.ID, mode, successfulRuns, learningsAccess, lockCode))
		}
		stepConfigSummary = sb.String()
	}

	workflowObjective, workflowSuccessCriteria := iwm.controller.ResolveWorkflowObjective(ctx)

	readPaths := []string{
		workspacePath,
		fmt.Sprintf("%s/runs", workspacePath),
		fmt.Sprintf("%s/planning", workspacePath),
		fmt.Sprintf("%s/learnings", workspacePath),
		fmt.Sprintf("%s/code", workspacePath),
		fmt.Sprintf("%s/evaluation", workspacePath),
		fmt.Sprintf("%s/costs", workspacePath),
	}
	iwm.controller.SetWorkspacePathForFolderGuard(readPaths, []string{})

	llmConfigToUse := iwm.controller.selectPulseLLM("review_workflow_costs agent")
	if llmConfigToUse == nil {
		return "", fmt.Errorf("no valid LLM configuration for review_workflow_costs agent")
	}

	config := iwm.createUnattendedWorkshopAgentConfig("review-workflow-costs-agent", 60, llmConfigToUse, "review_workflow_costs agent")
	// Isolate in a fresh tmp dir; don't project CLAUDE.md/.claude into the
	// builder's live workflow folder. See improve-db-agent above.
	config.IsolateCodingAgentWorkspace = true
	config.UseCodeExecutionMode = requiresCodeExecutionForProvider(iwm.presetLLM)
	config.ServerNames = []string{mcpclient.NoServers}
	defer iwm.configureWorkshopToolAgentSession(config, "review-workflow-costs", readPaths, []string{})()

	phaseTools, phaseExecutors := prepareReadOnlyBackgroundAgentTools(iwm.controller.BaseOrchestrator)
	createAgentFunc := func(cfg *agents.OrchestratorAgentConfig, log loggerv2.Logger, tracer observability.Tracer, eventBridge mcpagent.AgentEventListener) agents.OrchestratorAgent {
		return newWorkflowCostReviewAgent(cfg, log, tracer, eventBridge)
	}
	agent, err := iwm.controller.CreateAndSetupStandardAgentWithConfig(
		ctx, config, "review-workflow-costs", 0, 0, "review-workflow-costs",
		createAgentFunc, phaseTools, phaseExecutors, true,
	)
	if err != nil {
		return "", fmt.Errorf("failed to create review_workflow_costs agent: %w", err)
	}
	if agent.GetBaseAgent() != nil && agent.GetBaseAgent().Agent() != nil {
		registerGetCostSummaryTool(iwm, agent.GetBaseAgent(), logger)
	}

	templateVars := map[string]string{
		"WorkspacePath":           workspacePath,
		"AbsWorkspacePath":        absPromptWorkspacePath(workspacePath),
		"TargetRunFolder":         targetRunFolder,
		"PlanJSON":                planJSON,
		"StepConfigSummary":       stepConfigSummary,
		"WorkflowObjective":       workflowObjective,
		"WorkflowSuccessCriteria": workflowSuccessCriteria,
		"Focus":                   focus,
		"SessionID":               iwm.sessionID,
		"WorkflowID":              iwm.workflowID,
	}

	logger.Info(fmt.Sprintf("💸 Running review_workflow_costs agent (target_run_folder: %q, objective: %q, success_criteria: %q, focus: %q)", targetRunFolder, workflowObjective, workflowSuccessCriteria, focus))
	result, _, err := agent.Execute(ctx, templateVars, nil)
	if err != nil {
		return "", fmt.Errorf("review_workflow_costs agent failed: %w", err)
	}
	return result, nil
}

// runReviewStepCodeAgent compares step descriptions with saved main.py scripts to detect drift.
func (iwm *InteractiveWorkshopManager) runReviewStepCodeAgent(ctx context.Context, stepID string, focus string) (string, error) {
	workspacePath := iwm.controller.GetWorkspacePath()
	logger := iwm.controller.GetLogger()

	plan, err := iwm.controller.ReadCurrentPlan(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to load plan: %w", err)
	}
	if plan == nil {
		return "", fmt.Errorf("no approved plan found")
	}

	workflowObjective, _ := iwm.controller.ResolveWorkflowObjective(ctx)

	// Collect saved code to review — either a specific step/scoring agent or all
	// saved main.py scripts across workflow, evaluation, and scoring code.
	allSteps := collectAllSteps(plan.Steps)
	stepConfigs, _ := iwm.controller.ReadStepConfigsFromSubdir(ctx, "planning")
	workflowStepConfigMap := map[string]*StepConfig{}
	for i := range stepConfigs {
		workflowStepConfigMap[stepConfigs[i].ID] = &stepConfigs[i]
	}
	// The plan type decides whether a target is scripted (PLAT-287).
	planStepsByID := map[string]PlanStepInterface{}
	for _, info := range append(allSteps, collectAllSteps(plan.OrphanSteps)...) {
		if info.Step != nil {
			planStepsByID[info.Step.GetID()] = info.Step
		}
	}

	var stepsToReview strings.Builder
	reviewCount := 0

	appendCodeReviewTarget := func(scope string, sid string, title string, description string, validation *ValidationSchema, sc *StepConfig) {
		if stepID != "" && sid != stepID {
			return
		}
		scriptRelPath := iwm.controller.scriptedSourceDir(sid) + "/main.py"
		scriptContent, scriptErr := iwm.controller.ReadWorkspaceFile(ctx, scriptRelPath)
		hasSavedScript := scriptErr == nil && strings.TrimSpace(scriptContent) != ""
		isScriptedConfig := false
		if sc != nil {
			isScriptedConfig = isScriptedStep(planStepsByID[sid], sc.AgentConfigs)
		}

		if !hasSavedScript && !isScriptedConfig {
			if stepID != "" {
				stepsToReview.WriteString(fmt.Sprintf("### %s\n- **Scope**: %s\n- **Status**: NO_SAVED_CODE — this target is not configured for saved code and no %s exists\n\n", sid, scope, scriptRelPath))
				reviewCount++
			}
			return
		}

		validationSchema := ""
		if validation != nil {
			if schemaBytes, jsonErr := json.Marshal(validation); jsonErr == nil {
				validationSchema = string(schemaBytes)
			}
		}
		if validationSchema == "" && sc != nil && sc.ValidationSchema != nil {
			if schemaBytes, jsonErr := json.Marshal(sc.ValidationSchema); jsonErr == nil {
				validationSchema = string(schemaBytes)
			}
		}

		stepsToReview.WriteString(fmt.Sprintf("---\n### Step: %s\n", sid))
		stepsToReview.WriteString(fmt.Sprintf("**Scope**: %s\n", scope))
		stepsToReview.WriteString(fmt.Sprintf("**Title**: %s\n", title))
		stepsToReview.WriteString(fmt.Sprintf("\n**Description**:\n%s\n", description))

		if validationSchema != "" {
			stepsToReview.WriteString(fmt.Sprintf("\n**Validation Schema**:\n```json\n%s\n```\n", validationSchema))
		}

		if scriptErr != nil || strings.TrimSpace(scriptContent) == "" {
			stepsToReview.WriteString("\n**Saved Script**: ⚠️ No main.py found in learnings\n\n")
		} else {
			// Run static review too
			staticIssues := reviewMainPyScript(scriptContent)
			stepsToReview.WriteString(fmt.Sprintf("\n**Saved Script** (`%s`):\n```python\n%s\n```\n", scriptRelPath, scriptContent))
			if !isScriptedConfig {
				stepsToReview.WriteString("\n**Mode Fit Issue**: ⚠️ This step is not declared scripted but still has a saved main.py. For agentic steps, this file is stale artifact debt and should be deleted; agentic does not run or maintain persistent main.py.\n")
			}
			if len(staticIssues) > 0 {
				stepsToReview.WriteString("\n**Static Analysis Issues**:\n")
				for idx, issue := range staticIssues {
					stepsToReview.WriteString(fmt.Sprintf("%d. %s\n", idx+1, issue))
				}
			}
			stepsToReview.WriteString("\n")
		}
		reviewCount++
	}

	for _, info := range allSteps {
		sid := info.Step.GetID()
		appendCodeReviewTarget("workflow", sid, info.Step.GetTitle(), info.Step.GetDescription(), info.Step.GetValidationSchema(), workflowStepConfigMap[sid])
	}

	if reviewCount == 0 {
		return "No saved main.py scripts found to review across workflow steps.", nil
	}

	// Set up read-only folder guard
	readPaths := []string{
		workspacePath,
		fmt.Sprintf("%s/planning", workspacePath),
		fmt.Sprintf("%s/learnings", workspacePath),
		fmt.Sprintf("%s/code", workspacePath),
	}
	iwm.controller.SetWorkspacePathForFolderGuard(readPaths, []string{})

	llmConfigToUse := iwm.controller.selectPulseLLM("review_step_code agent")
	if llmConfigToUse == nil {
		return "", fmt.Errorf("no valid LLM configuration for review_step_code agent")
	}

	config := iwm.createUnattendedWorkshopAgentConfig("review-step-code-agent", 50, llmConfigToUse, "review_step_code agent")
	// Isolate in a fresh tmp dir; don't project CLAUDE.md/.claude into the
	// builder's live workflow folder. See improve-db-agent above.
	config.IsolateCodingAgentWorkspace = true
	config.UseCodeExecutionMode = requiresCodeExecutionForProvider(iwm.presetLLM)
	config.ServerNames = []string{mcpclient.NoServers}
	defer iwm.configureWorkshopToolAgentSession(config, "review-step-code", readPaths, []string{})()

	phaseTools, phaseExecutors := prepareReadOnlyBackgroundAgentTools(iwm.controller.BaseOrchestrator)
	createAgentFunc := func(cfg *agents.OrchestratorAgentConfig, log loggerv2.Logger, tracer observability.Tracer, eventBridge mcpagent.AgentEventListener) agents.OrchestratorAgent {
		return newStepCodeReviewAgent(cfg, log, tracer, eventBridge)
	}
	agent, err := iwm.controller.CreateAndSetupStandardAgentWithConfig(
		ctx, config, "review-step-code", 0, 0, "review-step-code",
		createAgentFunc, phaseTools, phaseExecutors, true,
	)
	if err != nil {
		return "", fmt.Errorf("failed to create review_step_code agent: %w", err)
	}

	templateVars := map[string]string{
		"WorkspacePath":     workspacePath,
		"AbsWorkspacePath":  absPromptWorkspacePath(workspacePath),
		"WorkflowObjective": workflowObjective,
		"StepsToReview":     stepsToReview.String(),
		"Focus":             focus,
		// Browser-authoring rules slot is populated inside the agent's Execute
		// method via BrowserAuthoringRulesFromTemplateVars; without this flag
		// the review agent is blind to the durable-selector contract it's
		// supposed to enforce on main.py drift.
		"HasBrowserAccess": fmt.Sprintf("%t", iwm.controller.HasBrowserCapability()),
	}

	logger.Info(fmt.Sprintf("🔍 Running review_step_code agent (%d steps, focus: %q)", reviewCount, focus))
	result, _, err := agent.Execute(ctx, templateVars, nil)
	if err != nil {
		return "", fmt.Errorf("review_step_code agent failed: %w", err)
	}
	return result, nil
}

func normalizeGoalAdvisorWorkspacePath(workspacePath string) (string, error) {
	cleaned := strings.Trim(filepath.ToSlash(strings.TrimSpace(workspacePath)), "/")
	if cleaned == "" {
		return "", fmt.Errorf("workspace path is required")
	}
	if filepath.IsAbs(workspacePath) || strings.Contains(cleaned, "\x00") {
		return "", fmt.Errorf("invalid workspace path %q", workspacePath)
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid workspace path %q", workspacePath)
		}
	}
	return cleaned, nil
}

type workflowAdvisorSpecialization struct {
	Version         int    `json:"version"`
	StrategyAuditor string `json:"strategy_auditor"`
	GoalAdvisor     string `json:"goal_advisor"`
	ApprovedInputID string `json:"approved_input_id,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
}

func parseAdvisorSpecializationDecisionContext(value string) (string, string, error) {
	const strategyMarker = "Strategy Auditor specialization:"
	const goalMarker = "Goal Advisor specialization:"
	strategyAt := strings.Index(value, strategyMarker)
	goalAt := strings.Index(value, goalMarker)
	if strategyAt < 0 || goalAt < 0 || goalAt <= strategyAt+len(strategyMarker) {
		return "", "", fmt.Errorf("decision context must contain Strategy Auditor specialization followed by Goal Advisor specialization")
	}
	strategy := strings.TrimSpace(value[strategyAt+len(strategyMarker) : goalAt])
	goal := strings.TrimSpace(value[goalAt+len(goalMarker):])
	if strategy == "" || goal == "" {
		return "", "", fmt.Errorf("both advisor specialization texts must be non-empty")
	}
	return strategy, goal, nil
}

func readWorkflowAdvisorSpecialization(content string) (*workflowAdvisorSpecialization, error) {
	var manifest struct {
		Pulse struct {
			AdvisorSpecialization *workflowAdvisorSpecialization `json:"advisor_specialization"`
		} `json:"pulse"`
	}
	if err := json.Unmarshal([]byte(content), &manifest); err != nil {
		return nil, err
	}
	return manifest.Pulse.AdvisorSpecialization, nil
}

func applyAdvisorSpecializationToManifest(content, inputID, strategy, goal, updatedAt string) (string, *workflowAdvisorSpecialization, bool, error) {
	current, err := readWorkflowAdvisorSpecialization(content)
	if err != nil {
		return "", nil, false, fmt.Errorf("parse workflow.json: %w", err)
	}
	strategy = strings.TrimSpace(strategy)
	goal = strings.TrimSpace(goal)
	alreadyActive := current != nil && current.ApprovedInputID == inputID &&
		strings.TrimSpace(current.StrategyAuditor) == strategy && strings.TrimSpace(current.GoalAdvisor) == goal
	if alreadyActive {
		return content, current, true, nil
	}

	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(content), &manifest); err != nil {
		return "", nil, false, fmt.Errorf("parse workflow.json: %w", err)
	}
	version := 1
	if current != nil && current.Version >= 1 {
		version = current.Version + 1
	}
	activated := &workflowAdvisorSpecialization{
		Version:         version,
		StrategyAuditor: strategy,
		GoalAdvisor:     goal,
		ApprovedInputID: inputID,
		UpdatedAt:       updatedAt,
	}
	pulse, _ := manifest["pulse"].(map[string]interface{})
	if pulse == nil {
		pulse = make(map[string]interface{})
	}
	pulse["advisor_specialization"] = activated
	manifest["pulse"] = pulse
	manifest["updated_at"] = updatedAt
	out, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", nil, false, err
	}
	return string(out), activated, false, nil
}

func advisorSpecializationPrompt(specialization *workflowAdvisorSpecialization, module string) string {
	if specialization == nil || pulsemodules.Normalize(module) != pulsemodules.StrategicReviewID {
		return ""
	}
	// The manifest retains both approved texts so existing workflows do not
	// lose owner decisions during migration. They are phase-specific lenses in
	// one Strategic Review sequence now, not separate module identities.
	parts := make([]string, 0, 2)
	if lens := strings.TrimSpace(specialization.StrategyAuditor); lens != "" {
		parts = append(parts, "Current-strategy audit lens:\n"+lens)
	}
	if lens := strings.TrimSpace(specialization.GoalAdvisor); lens != "" {
		parts = append(parts, "Independent-opportunity lens:\n"+lens)
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("OWNER-APPROVED WORKFLOW-SPECIFIC STRATEGIC REVIEW LENSES (version %d). Apply each only in its named sequence phase. The canonical contract and current soul/plan win on conflict:\n%s", specialization.Version, strings.Join(parts, "\n\n"))
}

func (iwm *InteractiveWorkshopManager) activateApprovedAdvisorSpecialization(ctx context.Context, inputID string) (*workflowAdvisorSpecialization, bool, error) {
	inputID = strings.TrimSpace(inputID)
	if !strings.HasPrefix(inputID, "advisor-specialization-") {
		return nil, false, fmt.Errorf("input id must start with advisor-specialization-")
	}
	normalized, err := normalizeGoalAdvisorWorkspacePath(iwm.controller.GetWorkspacePath())
	if err != nil {
		return nil, false, err
	}
	dbPath := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(normalized), "db", "db.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, false, err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return nil, false, err
	}

	var source, status, selected, decisionContext string
	err = db.QueryRowContext(ctx, `SELECT source, status, selected_option_id, context
		FROM report_human_inputs WHERE id=? AND workspace_path=?`, inputID, normalized).
		Scan(&source, &status, &selected, &decisionContext)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, fmt.Errorf("approved decision %q was not found", inputID)
		}
		return nil, false, err
	}
	if strings.TrimSpace(source) != "pulse" {
		return nil, false, fmt.Errorf("decision %q must have source pulse", inputID)
	}
	if status != "answered" && status != "consumed" {
		return nil, false, fmt.Errorf("decision %q must be answered before activation; current status=%q", inputID, status)
	}
	if strings.ToLower(strings.TrimSpace(selected)) != "activate" {
		return nil, false, fmt.Errorf("decision %q selected %q, not activate", inputID, selected)
	}
	strategy, goal, err := parseAdvisorSpecializationDecisionContext(decisionContext)
	if err != nil {
		return nil, false, err
	}

	content, err := iwm.controller.ReadWorkspaceFile(ctx, "workflow.json")
	if err != nil {
		return nil, false, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	updatedContent, activated, alreadyActive, err := applyAdvisorSpecializationToManifest(content, inputID, strategy, goal, now)
	if err != nil {
		return nil, false, err
	}
	if !alreadyActive {
		if err := iwm.controller.writeManagedWorkflowManifest(ctx, updatedContent); err != nil {
			return nil, false, err
		}
		oldState := "absent"
		if previous, _ := readWorkflowAdvisorSpecialization(content); previous != nil {
			oldState = "present"
		}
		LogCanonicalArtifactChange(
			ctx,
			normalized,
			"update_workflow_config",
			"Activated an owner-approved Strategy Auditor and Goal Advisor specialization.",
			[]PlanFieldChange{{
				Field:    "workflow.json.pulse.advisor_specialization",
				OldValue: oldState,
				NewValue: "changed",
			}},
			func(ctx context.Context, path string) (string, error) {
				return iwm.controller.ReadWorkspaceFile(ctx, path)
			},
			func(ctx context.Context, path, value string) error {
				return iwm.controller.WriteWorkspaceFile(ctx, path, value)
			},
			iwm.controller.GetLogger(),
			"workflow.json",
			content,
			updatedContent,
		)
	}

	if status == "answered" {
		result, err := db.ExecContext(ctx, `UPDATE report_human_inputs
			SET status='consumed', consumed_by='advisor_specialization',
			    outcome_summary=?, consumed_at=?, updated_at=?
			WHERE id=? AND workspace_path=? AND status='answered'`,
			fmt.Sprintf("Activated advisor specialization version %d in workflow.json", activated.Version), now, now, inputID, normalized)
		if err != nil {
			return nil, false, fmt.Errorf("specialization was written but decision consumption failed: %w", err)
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return nil, false, fmt.Errorf("specialization was written but decision consumption did not update exactly one row")
		}
	}
	return activated, alreadyActive, nil
}

func (iwm *InteractiveWorkshopManager) createUnattendedWorkshopAgentConfig(agentName string, maxTurns int, llmConfig *orchestrator.LLMConfig, agentKind string) *agents.OrchestratorAgentConfig {
	config := iwm.controller.CreateStandardAgentConfigWithLLM(agentName, maxTurns, agents.OutputFormatStructured, llmConfig)
	iwm.controller.applyWorkflowTransportToAgentConfig(config, nil, agentKind)
	return config
}

// WorkflowBackgroundTaskAgent is a standalone agent spawned by run_in_background.
type WorkflowBackgroundTaskAgent struct {
	*agents.BaseOrchestratorAgent
}

// Execute implements OrchestratorAgent interface for the background task agent
func (agent *WorkflowBackgroundTaskAgent) Execute(ctx context.Context, templateVars map[string]string, conversationHistory []llmtypes.MessageContent) (string, []llmtypes.MessageContent, error) {
	baseAgent := agent.BaseOrchestratorAgent.BaseAgent()
	if baseAgent == nil || baseAgent.Agent() == nil {
		return "", nil, fmt.Errorf("agent not initialized")
	}

	// Templates
	var systemPrompt, userMessage strings.Builder
	if err := backgroundTaskAgentSystemTemplate.Execute(&systemPrompt, templateVars); err != nil {
		return "", nil, err
	}
	if err := backgroundTaskAgentUserTemplate.Execute(&userMessage, templateVars); err != nil {
		return "", nil, err
	}

	systemPrompt.WriteString("\n\n" + workflowKnowledgebaseSourcesPrompt(templateVars["WorkspacePath"]))

	// Single-pass execution
	inputProcessor := func(map[string]string) string { return userMessage.String() }

	result, updatedHistory, err := agent.ExecuteWithTemplateValidation(
		ctx, templateVars, inputProcessor,
		conversationHistory, struct{}{},
		systemPrompt.String(), true,
	)
	if err != nil {
		return "", nil, err
	}

	return result, updatedHistory, nil
}

func validateWorkshopScheduleTimezone(timezone string) error {
	if timezone == "" {
		return fmt.Errorf("timezone is required; use an IANA timezone like UTC, Asia/Kolkata, or America/New_York")
	}
	if timezone != "UTC" && !strings.Contains(timezone, "/") {
		return fmt.Errorf("invalid timezone %q: use an IANA timezone like UTC, Asia/Kolkata, or America/New_York; abbreviations like EST, PST, or IST are not accepted", timezone)
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return fmt.Errorf("invalid timezone %q: use an IANA timezone like UTC, Asia/Kolkata, or America/New_York", timezone)
	}
	return nil
}

// stepLLMConfigForValidation carries the selected override for structural validation.
type stepLLMConfigForValidation struct {
	label       string
	publishedID string
	provider    string
	modelID     string
}

func collectStepLLMConfigsForValidation(cfg *AgentLLMConfig) []stepLLMConfigForValidation {
	if cfg == nil {
		return nil
	}
	out := []stepLLMConfigForValidation{{
		label:       "execution_llm",
		publishedID: cfg.PublishedLLMID,
		provider:    cfg.Provider,
		modelID:     cfg.ModelID,
	}}

	return out
}

// validateStepLLMConfig returns an error string, or "" when the shape is usable.
//
// It deliberately does not try to confirm the model exists — that needs a live
// provider call. It catches the shapes that cannot be right under any provider.
func validateStepLLMConfig(label, publishedID, provider, modelID string) string {
	publishedID = strings.TrimSpace(publishedID)
	provider = strings.TrimSpace(provider)
	modelID = strings.TrimSpace(modelID)

	if publishedID == "" && provider == "" && modelID == "" {
		return fmt.Sprintf("%s is set but empty. Give a published_llm_id, or both provider and model_id, or clear the field so the step uses the workflow default.", label)
	}
	// A published id resolves both halves on its own.
	if publishedID != "" {
		return ""
	}
	if provider == "" {
		return fmt.Sprintf("%s sets model_id %q with no provider. Use get_llm_config to see which provider serves that model.", label, modelID)
	}
	if strings.EqualFold(provider, "agy-cli") {
		if err := llmguard.RequireCodingAgentProvider(provider); err != nil {
			return fmt.Sprintf("%s: %v", label, err)
		}
	}
	if modelID == "" {
		return fmt.Sprintf("%s sets provider %q with no model_id. Use get_llm_config (or list_coding_agent_models for a CLI provider) to pick one.", label, provider)
	}
	if strings.EqualFold(provider, modelID) {
		hint := "Use get_llm_config to pick a real model for that provider."
		if common.IsCLIProvider(provider) {
			hint = fmt.Sprintf("%q is a CLI runtime, not a model. Use list_coding_agent_models to pick the model it should run (for example %q with a specific Claude model).", provider, provider)
		}
		return fmt.Sprintf("%s sets model_id %q, which is the provider name repeated in the model slot — that is never a real model and the step will fail at turn 1 with \"all LLMs failed\". %s", label, modelID, hint)
	}
	return ""
}

// notifySecretsAttached hands the attached secrets that have a value, and the
// removed names, to the live-shell sync callback.
func notifySecretsAttached(callback func(set map[string]string, removed []string), secrets []orchestrator.SecretEntry, removed []string) {
	if callback == nil {
		return
	}
	set := make(map[string]string, len(secrets))
	for _, secret := range secrets {
		if secret.Name != "" && secret.Value != "" {
			set[secret.Name] = secret.Value
		}
	}
	callback(set, removed)
}

// pulseStepBusyCheck is the Pulse workshop's check for a step the workflow is
// running right now, or nil outside Pulse.
func (iwm *InteractiveWorkshopManager) pulseStepBusyCheck() func(context.Context, string, string) string {
	if iwm == nil || iwm.workshopConfig == nil {
		return nil
	}
	return iwm.workshopConfig.StepBusyForPulse
}
