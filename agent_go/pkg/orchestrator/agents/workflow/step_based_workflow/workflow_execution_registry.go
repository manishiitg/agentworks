package step_based_workflow

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
)

// WorkshopStepInfo describes a step in the plan, including its position in the hierarchy.
type WorkshopStepInfo struct {
	Step           PlanStepInterface
	ParentID       string // empty for top-level steps
	ParentType     StepType
	NestedLocation string // e.g. "route:route-id"
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
	case *AgentPlanStep:
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

// isScriptedStep derives execution from the saved plan type.
func isScriptedStep(step PlanStepInterface) bool {
	_, scripted := step.(*ScriptedPlanStep)
	return scripted
}

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
	RunFolder               string
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
	RunFolder          string
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
		RunFolder:      e.RunFolder,
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

// WorkshopExecutionNotifier is called when workshop step/background executions start and complete.
// Implemented by the server layer to register executions in bgAgentRegistry so that
// HasRunningAgents() returns true and the frontend keeps polling for events.
type WorkshopExecutionNotifier interface {
	OnExecutionStart(start WorkshopExecutionStart)
	OnExecutionComplete(execID, name, result string, meta map[string]string, err error)
	OnExecutionTerminated(execID, name string) // explicit cancellation via stop_step/stop_all
}

// ServerAgentInfo is a lightweight snapshot of a server-tracked background agent.
type ServerAgentInfo struct {
	ID     string
	Name   string
	Status string // "running", "completed", "failed", "canceled"
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

// executionModeLabel renders the derived mode for event/context tagging.
func executionModeLabel(scripted bool) string {
	if scripted {
		return StepModeScripted
	}
	return StepModeAgentic
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

// vaultSecretEnvName is the $SECRET_ name of a selected secret: a Vault secret
// VLT_<vault>__NAME is used as NAME.
func vaultSecretEnvName(name string) string {
	if strings.HasPrefix(name, "VLT_") {
		if i := strings.Index(name, "__"); i >= 0 {
			return name[i+2:]
		}
	}
	return name
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
