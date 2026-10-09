package server

import (
	"log"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

const (
	trackedExecutionSourceWorkflowRun        = "workflow_run"
	trackedExecutionSourceWorkshopBackground = "workshop_background"

	trackedExecutionStatusRunning   = "running"
	trackedExecutionStatusCompleted = "completed"
	trackedExecutionStatusFailed    = "failed"
	trackedExecutionStatusCanceled  = "canceled"
)

const trackedExecutionRetention = 24 * time.Hour

// trackedExecutionRunningMaxAge caps how long an entry may sit in "running".
//
// The registry is in-memory and an entry only leaves "running" when a completion
// path fires. If one is ever missed — a dropped notifier call, a killed agent
// process, a context torn down before finalize — the entry is immortal: it keeps
// the global monitor showing a live spinner and makes every new workflow-builder
// chat fail with 409 workflow_busy, until the server is restarted. That has bitten
// this workflow repeatedly.
//
// The cap is deliberately far above any legitimate run. The scheduler's own
// inactivity timeouts are 10 minutes for a normal Pulse step and 30 for Goal
// Advisor, and observed full runs are ~1.5 hours, so 12 hours cannot reap live
// work — it only clears entries that are certainly dead.
const trackedExecutionRunningMaxAge = 12 * time.Hour

// TrackedWorkflowExecution is the backend's unified execution record.
// It keeps top-level workflow runs and workflow-builder background work in one place.
type TrackedWorkflowExecution struct {
	ExecutionID   string            `json:"execution_id"`
	SessionID     string            `json:"session_id"`
	Source        string            `json:"source"`
	Kind          string            `json:"kind"`
	Name          string            `json:"name,omitempty"`
	Query         string            `json:"query,omitempty"`
	PresetQueryID string            `json:"preset_query_id,omitempty"`
	PresetName    string            `json:"preset_name,omitempty"`
	WorkspacePath string            `json:"workspace_path"`
	RunFolder     string            `json:"run_folder,omitempty"`
	PhaseID       string            `json:"phase_id,omitempty"`
	PhaseName     string            `json:"phase_name,omitempty"`
	Status        string            `json:"status"`
	UserID        string            `json:"user_id,omitempty"`
	Title         string            `json:"title,omitempty"`
	TriggeredBy   string            `json:"triggered_by,omitempty"`
	StartedAt     time.Time         `json:"started_at"`
	CompletedAt   *time.Time        `json:"completed_at,omitempty"`
	LastError     string            `json:"last_error,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`

	// Workflow-run UI state mirrored from the existing running-workflow API.
	IsMinimized      bool   `json:"is_minimized,omitempty"`
	MinimizedAt      int64  `json:"minimized_at,omitempty"`
	CurrentStepID    string `json:"current_step_id,omitempty"`
	CurrentStepTitle string `json:"current_step_title,omitempty"`
}

func normalizeTrackedWorkspacePath(workspacePath string) string {
	trimmed := strings.TrimSpace(workspacePath)
	if trimmed == "" {
		return ""
	}
	return filepath.Clean(trimmed)
}

func cloneTrackedMetadata(meta map[string]string) map[string]string {
	if len(meta) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(meta))
	for k, v := range meta {
		cloned[k] = v
	}
	return cloned
}

func normalizeTrackedExecutionKind(kind string) string {
	kind = strings.TrimSpace(strings.ToLower(kind))
	if kind == "" {
		return ""
	}
	return strings.ReplaceAll(kind, "-", "_")
}

func inferTrackedExecutionKind(source, phaseID, name string, meta map[string]string) string {
	if meta != nil {
		if executionType := normalizeTrackedExecutionKind(meta["execution_type"]); executionType != "" {
			return executionType
		}
	}

	lowerName := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.HasPrefix(lowerName, "step-"):
		return "step"
	case strings.Contains(lowerName, "full-workflow"):
		return "full_workflow"
	case strings.Contains(lowerName, "full workflow execution"):
		return "full_workflow"
	case strings.Contains(lowerName, "full evaluation"):
		return "full_evaluation"
	case strings.Contains(lowerName, "evaluation"):
		return "full_evaluation"
	}

	if source == trackedExecutionSourceWorkflowRun {
		if strings.TrimSpace(phaseID) != "" {
			return "workflow_phase"
		}
		return "workflow"
	}

	if phaseID == "workflow-builder" {
		return "workflow_builder_task"
	}

	return "background_task"
}

func trackedExecutionAppearsInRunningWorkflowList(exec *TrackedWorkflowExecution) bool {
	if exec == nil || exec.Status != trackedExecutionStatusRunning {
		return false
	}
	if exec.Source == trackedExecutionSourceWorkflowRun {
		return true
	}
	if exec.Source != trackedExecutionSourceWorkshopBackground && exec.Source != trackedExecutionSourceConversationTurn {
		return false
	}
	kind := normalizeTrackedExecutionKind(exec.Kind)
	if kind == "" {
		kind = inferTrackedExecutionKind(exec.Source, exec.PhaseID, exec.Name, exec.Metadata)
	}
	return kind == "full_workflow" || kind == "workflow_builder_task"
}

func trackedExecutionBlocksNewWorkflowBuilderChat(exec *TrackedWorkflowExecution) bool {
	if exec == nil || exec.Status != trackedExecutionStatusRunning {
		return false
	}
	if strings.TrimSpace(exec.PhaseID) != "workflow-builder" {
		return false
	}
	// Scheduled/background workflow work may use the workflow-builder phase for
	// implementation details such as message-sequence items. It must not block a
	// user from opening or continuing a normal builder chat for the same workflow.
	if isScheduledSessionIdentity(exec.SessionID, exec.TriggeredBy) {
		return false
	}
	if normalizeChatHistoryWorkshopMode(exec.Metadata["workshop_mode"]) == "run" {
		return false
	}
	// Work started from a Run-mode session (an MCP run, ask, a Run chat) runs steps and never edits the workflow, so it
	// does not hold the Builder lock. A step it starts carries no mode of its own; its session says Run.
	if cfg := common.GetSessionShellConfig(exec.SessionID); cfg != nil && cfg.WorkflowReadOnly {
		return false
	}
	return true
}

// workflowBusyMessage tells the person who was refused who holds the workflow's Builder lock and what to do.
func workflowBusyMessage(running *TrackedWorkflowExecution, currentUserID string) (string, string) {
	since := ""
	if running != nil && !running.StartedAt.IsZero() {
		since = " (since " + running.StartedAt.UTC().Format("15:04") + " UTC)"
	}
	holder := ""
	if running != nil {
		holder = strings.TrimSpace(running.UserID)
	}
	if holder == "" || sanitizeUserIDForPath(holder) == sanitizeUserIDForPath(currentUserID) {
		return "", "You are already using the Builder on this workflow in another chat" + since + ". Wait for it to finish, or stop it, then try again."
	}
	name := holder
	if rec := directoryUserFor(holder, "", ""); rec != nil && strings.TrimSpace(rec.Username) != "" {
		name = strings.TrimSpace(rec.Username)
	}
	return name, name + " is using the Builder on this workflow right now" + since + ", and only one person can use it at a time. Try again when " + name + " is done, or switch to Run mode to run steps and ask questions now."
}

// trackedExecutionBlocksScheduledWorkflow distinguishes producing workflow
// executions from interactive chats and Pulse/reviewer stages. A schedule has
// its own durable single-run lock, so only another actual full-workflow run
// should prevent it from starting; chats are intentionally a separate lane.
func trackedExecutionBlocksScheduledWorkflow(exec *TrackedWorkflowExecution) bool {
	if exec == nil || exec.Status != trackedExecutionStatusRunning {
		return false
	}
	kind := normalizeTrackedExecutionKind(exec.Kind)
	if kind == "" {
		kind = inferTrackedExecutionKind(exec.Source, exec.PhaseID, exec.Name, exec.Metadata)
	}
	return kind == "full_workflow"
}

func trackedExecutionToActive(exec *TrackedWorkflowExecution) ActiveWorkflowExecution {
	if exec == nil {
		return ActiveWorkflowExecution{}
	}
	kind := normalizeTrackedExecutionKind(exec.Kind)
	if kind == "" {
		kind = inferTrackedExecutionKind(exec.Source, exec.PhaseID, exec.Name, exec.Metadata)
	}
	return ActiveWorkflowExecution{
		QueryID:          exec.ExecutionID,
		SessionID:        exec.SessionID,
		Kind:             kind,
		PresetQueryID:    exec.PresetQueryID,
		PresetName:       exec.PresetName,
		WorkspacePath:    exec.WorkspacePath,
		RunFolder:        exec.RunFolder,
		PhaseID:          exec.PhaseID,
		PhaseName:        exec.PhaseName,
		Status:           exec.Status,
		UserID:           exec.UserID,
		Title:            exec.Title,
		Query:            exec.Query,
		TriggeredBy:      exec.TriggeredBy,
		StartedAt:        exec.StartedAt,
		IsMinimized:      exec.IsMinimized,
		MinimizedAt:      exec.MinimizedAt,
		CurrentStepID:    exec.CurrentStepID,
		CurrentStepTitle: exec.CurrentStepTitle,
	}
}

func (api *StreamingAPI) pruneTrackedExecutionsLocked(now time.Time) {
	for executionID, exec := range api.trackedWorkflowExecutions {
		if exec == nil {
			delete(api.trackedWorkflowExecutions, executionID)
			continue
		}
		if exec.Status == trackedExecutionStatusRunning {
			// A running entry used to be skipped unconditionally, so a missed
			// completion left it running forever. Reap only the certainly-dead
			// ones, and say so: a silently vanished "running" execution would be
			// just as confusing as an immortal one.
			if !exec.StartedAt.IsZero() && now.Sub(exec.StartedAt) > trackedExecutionRunningMaxAge {
				log.Printf("[EXEC_TRACKER] Reaping stale running execution %s (workspace=%s, session=%s, started %s, age %s) — it never reported completion; this would otherwise block new workflow-builder chats with workflow_busy",
					executionID, exec.WorkspacePath, exec.SessionID, exec.StartedAt.Format(time.RFC3339), now.Sub(exec.StartedAt).Truncate(time.Minute))
				delete(api.trackedWorkflowExecutions, executionID)
			}
			continue
		}
		referenceTime := exec.StartedAt
		if exec.CompletedAt != nil {
			referenceTime = *exec.CompletedAt
		}
		if referenceTime.IsZero() || now.Sub(referenceTime) > trackedExecutionRetention {
			delete(api.trackedWorkflowExecutions, executionID)
		}
	}
}

func (api *StreamingAPI) trackExecutionStart(exec *TrackedWorkflowExecution) {
	if api == nil || exec == nil || strings.TrimSpace(exec.ExecutionID) == "" {
		return
	}

	exec.WorkspacePath = normalizeTrackedWorkspacePath(exec.WorkspacePath)
	if exec.StartedAt.IsZero() {
		exec.StartedAt = time.Now().UTC()
	}
	if strings.TrimSpace(exec.Status) == "" {
		exec.Status = trackedExecutionStatusRunning
	}
	if strings.TrimSpace(exec.Kind) == "" {
		exec.Kind = inferTrackedExecutionKind(exec.Source, exec.PhaseID, exec.Name, exec.Metadata)
	}
	exec.Metadata = cloneTrackedMetadata(exec.Metadata)

	api.trackedWorkflowExecutionsMux.Lock()
	if api.trackedWorkflowExecutions == nil {
		api.trackedWorkflowExecutions = make(map[string]*TrackedWorkflowExecution)
	}
	api.trackedWorkflowExecutions[exec.ExecutionID] = exec
	api.pruneTrackedExecutionsLocked(time.Now().UTC())
	api.trackedWorkflowExecutionsMux.Unlock()
	api.observeRuntimeSnapshot(exec.SessionID)
	publishSessionsChanged()
}

func (api *StreamingAPI) trackWorkflowRunStart(exec *ActiveWorkflowExecution) {
	if exec == nil || strings.TrimSpace(exec.QueryID) == "" {
		return
	}
	api.trackExecutionStart(&TrackedWorkflowExecution{
		ExecutionID:      exec.QueryID,
		SessionID:        exec.SessionID,
		Source:           trackedExecutionSourceWorkflowRun,
		Name:             exec.Title,
		Query:            exec.Query,
		PresetQueryID:    exec.PresetQueryID,
		PresetName:       exec.PresetName,
		WorkspacePath:    exec.WorkspacePath,
		RunFolder:        exec.RunFolder,
		PhaseID:          exec.PhaseID,
		PhaseName:        exec.PhaseName,
		Status:           exec.Status,
		UserID:           exec.UserID,
		Title:            exec.Title,
		TriggeredBy:      exec.TriggeredBy,
		StartedAt:        exec.StartedAt,
		IsMinimized:      exec.IsMinimized,
		MinimizedAt:      exec.MinimizedAt,
		CurrentStepID:    exec.CurrentStepID,
		CurrentStepTitle: exec.CurrentStepTitle,
	})
}

func (api *StreamingAPI) trackWorkshopExecutionStart(sessionID, workspacePath, presetQueryID, userID string, executionID, name, parentExecutionID string, startMetadata map[string]string) {
	metadata := cloneTrackedMetadata(startMetadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	if parentExecutionID = strings.TrimSpace(parentExecutionID); parentExecutionID != "" {
		metadata["parent_execution_id"] = parentExecutionID
	}
	runFolder := strings.TrimSpace(metadata["run_folder"])
	api.trackExecutionStart(&TrackedWorkflowExecution{
		ExecutionID:   executionID,
		SessionID:     sessionID,
		Source:        trackedExecutionSourceWorkshopBackground,
		Name:          name,
		Title:         name,
		PresetQueryID: presetQueryID,
		WorkspacePath: workspacePath,
		RunFolder:     runFolder,
		PhaseID:       "workflow-builder",
		PhaseName:     "Workflow Builder",
		Status:        trackedExecutionStatusRunning,
		UserID:        userID,
		TriggeredBy:   "workflow_builder",
		StartedAt:     time.Now().UTC(),
		Metadata:      metadata,
	})
}

// trackedExecutionFinished reports whether executionID is tracked and has
// already left "running" (completed, failed or canceled).
func (api *StreamingAPI) trackedExecutionFinished(executionID string) bool {
	executionID = strings.TrimSpace(executionID)
	if api == nil || executionID == "" {
		return false
	}
	api.trackedWorkflowExecutionsMux.Lock()
	defer api.trackedWorkflowExecutionsMux.Unlock()
	exec := api.trackedWorkflowExecutions[executionID]
	return exec != nil && exec.Status != trackedExecutionStatusRunning
}

func (api *StreamingAPI) completeTrackedExecution(executionID, status, errorMessage string, meta map[string]string) {
	if api == nil || strings.TrimSpace(executionID) == "" {
		return
	}
	if strings.TrimSpace(status) == "" {
		status = trackedExecutionStatusCompleted
	}

	api.trackedWorkflowExecutionsMux.Lock()

	exec := api.trackedWorkflowExecutions[executionID]
	if exec == nil || exec.Status != trackedExecutionStatusRunning {
		api.trackedWorkflowExecutionsMux.Unlock()
		return
	}
	sessionID := exec.SessionID

	now := time.Now().UTC()
	exec.Status = status
	exec.CompletedAt = &now
	if strings.TrimSpace(errorMessage) != "" {
		exec.LastError = errorMessage
	}
	if len(meta) > 0 {
		// Completion metadata augments registration metadata. Replacing the map
		// here would erase parent_execution_id and detach any still-running
		// grandchildren from the exact conversation-turn tree.
		if exec.Metadata == nil {
			exec.Metadata = make(map[string]string, len(meta))
		}
		for key, value := range meta {
			exec.Metadata[key] = value
		}
		if runFolder := strings.TrimSpace(meta["run_folder"]); runFolder != "" {
			exec.RunFolder = runFolder
		}
		exec.Kind = inferTrackedExecutionKind(exec.Source, exec.PhaseID, exec.Name, exec.Metadata)
	}
	settledRun := liveFeedSettledRunPath(exec)
	api.pruneTrackedExecutionsLocked(now)
	api.trackedWorkflowExecutionsMux.Unlock()
	api.observeRuntimeSnapshot(sessionID)
	publishSessionsChanged()
	publishWorkflowSettled(settledRun)
}

func (api *StreamingAPI) cancelTrackedExecutionsForSession(sessionID string) {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return
	}

	api.trackedWorkflowExecutionsMux.Lock()

	now := time.Now().UTC()
	marked := 0
	var settled []string
	for _, exec := range api.trackedWorkflowExecutions {
		if exec == nil || exec.SessionID != sessionID || exec.Status != trackedExecutionStatusRunning {
			continue
		}
		exec.Status = trackedExecutionStatusCanceled
		exec.CompletedAt = &now
		marked++
		settled = append(settled, liveFeedSettledRunPath(exec))
		// Marking a tracked execution canceled tells watchers to stop watching.
		// It does not stop the worker — that is cancelBackgroundAgents' job — so
		// log both and compare the counts when diagnosing a Stop that did not
		// take (PLAT-130).
		log.Printf("[STOP] session=%s tracked execution marked canceled: id=%s kind=%s name=%q",
			sessionID, exec.ExecutionID, exec.Kind, exec.Name)
	}
	log.Printf("[STOP] session=%s tracked executions marked canceled=%d (status only — see background agent counts for actual cancellation)", sessionID, marked)
	api.pruneTrackedExecutionsLocked(now)
	api.trackedWorkflowExecutionsMux.Unlock()
	api.observeRuntimeSnapshot(sessionID)
	if marked > 0 {
		publishSessionsChanged()
		for _, workspacePath := range settled {
			publishWorkflowSettled(workspacePath)
		}
	}
}

func (api *StreamingAPI) finalizeTrackedExecutionIfRunning(executionID, status, errorMessage string) {
	if api == nil || strings.TrimSpace(executionID) == "" {
		return
	}

	api.trackedWorkflowExecutionsMux.Lock()

	exec := api.trackedWorkflowExecutions[executionID]
	if exec == nil || exec.Status != trackedExecutionStatusRunning {
		api.trackedWorkflowExecutionsMux.Unlock()
		return
	}
	sessionID := exec.SessionID

	now := time.Now().UTC()
	exec.Status = status
	exec.CompletedAt = &now
	if strings.TrimSpace(errorMessage) != "" {
		exec.LastError = errorMessage
	}
	settledRun := liveFeedSettledRunPath(exec)
	api.pruneTrackedExecutionsLocked(now)
	api.trackedWorkflowExecutionsMux.Unlock()
	defer publishWorkflowSettled(settledRun)
	defer publishSessionsChanged()
	api.observeRuntimeSnapshot(sessionID)
}

// runningWorkflowExecutionBySessionLocked returns the latest running top-level workflow execution.
// api.trackedWorkflowExecutionsMux must already be held.
func (api *StreamingAPI) runningWorkflowExecutionBySessionLocked(sessionID string) *TrackedWorkflowExecution {
	var best *TrackedWorkflowExecution
	for _, exec := range api.trackedWorkflowExecutions {
		if exec == nil || exec.Source != trackedExecutionSourceWorkflowRun || exec.Status != trackedExecutionStatusRunning || exec.SessionID != sessionID {
			continue
		}
		if best == nil || exec.StartedAt.After(best.StartedAt) {
			best = exec
		}
	}
	return best
}

// runningTrackedExecutionBySessionLocked returns the latest running tracked execution
// for a session, including workflow-builder background work. api.trackedWorkflowExecutionsMux
// must already be held.
func (api *StreamingAPI) runningTrackedExecutionBySessionLocked(sessionID string) *TrackedWorkflowExecution {
	var best *TrackedWorkflowExecution
	for _, exec := range api.trackedWorkflowExecutions {
		if exec == nil || exec.Status != trackedExecutionStatusRunning || exec.SessionID != sessionID {
			continue
		}
		if best == nil || exec.StartedAt.After(best.StartedAt) {
			best = exec
		}
	}
	return best
}

// runningWorkflowListExecutionBySessionLocked returns the latest running
// execution for a session that is eligible for /api/workflow/running. This
// deliberately differs from runningTrackedExecutionBySessionLocked, which can
// return internal workflow steps/sub-agents that should not be top-level rows.
// api.trackedWorkflowExecutionsMux must already be held.
func (api *StreamingAPI) runningWorkflowListExecutionBySessionLocked(sessionID string) *TrackedWorkflowExecution {
	var best *TrackedWorkflowExecution
	for _, exec := range api.trackedWorkflowExecutions {
		if exec == nil || exec.SessionID != sessionID || !trackedExecutionAppearsInRunningWorkflowList(exec) {
			continue
		}
		if best == nil || exec.StartedAt.After(best.StartedAt) {
			best = exec
		}
	}
	return best
}

func (api *StreamingAPI) listRunningWorkflowExecutions(userID string) []ActiveWorkflowExecution {
	api.trackedWorkflowExecutionsMux.RLock()
	list := make([]ActiveWorkflowExecution, 0, len(api.trackedWorkflowExecutions))
	for _, exec := range api.trackedWorkflowExecutions {
		if !trackedExecutionAppearsInRunningWorkflowList(exec) {
			continue
		}
		if userID != "" && exec.UserID != "" && exec.UserID != userID {
			continue
		}
		list = append(list, trackedExecutionToActive(exec))
	}
	api.trackedWorkflowExecutionsMux.RUnlock()

	for i := range list {
		api.enrichActiveWorkflowExecutionLifecycle(&list[i])
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].StartedAt.After(list[j].StartedAt)
	})
	return list
}

func (api *StreamingAPI) listRunningWorkflowExecutionsForWorkspace(workspacePath string) []ActiveWorkflowExecution {
	normalizedWorkspace := normalizeTrackedWorkspacePath(workspacePath)

	api.trackedWorkflowExecutionsMux.RLock()
	list := make([]ActiveWorkflowExecution, 0, len(api.trackedWorkflowExecutions))
	for _, exec := range api.trackedWorkflowExecutions {
		if !trackedExecutionAppearsInRunningWorkflowList(exec) {
			continue
		}
		if normalizedWorkspace != "" && exec.WorkspacePath != normalizedWorkspace {
			continue
		}
		list = append(list, trackedExecutionToActive(exec))
	}
	api.trackedWorkflowExecutionsMux.RUnlock()

	for i := range list {
		api.enrichActiveWorkflowExecutionLifecycle(&list[i])
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].StartedAt.After(list[j].StartedAt)
	})
	return list
}

// The external run API needs step IDs as well as the top-level workflow rows
// shown by the UI. Its stop_step operation accepts IDs from this list.
func (api *StreamingAPI) listRunningExternalExecutionsForWorkspace(workspacePath string) []ActiveWorkflowExecution {
	normalizedWorkspace := normalizeTrackedWorkspacePath(workspacePath)
	api.trackedWorkflowExecutionsMux.RLock()
	list := make([]ActiveWorkflowExecution, 0, len(api.trackedWorkflowExecutions))
	for _, exec := range api.trackedWorkflowExecutions {
		if exec == nil || exec.Status != trackedExecutionStatusRunning || normalizeTrackedWorkspacePath(exec.WorkspacePath) != normalizedWorkspace {
			continue
		}
		if !trackedExecutionAppearsInRunningWorkflowList(exec) && exec.Source != trackedExecutionSourceWorkshopBackground {
			continue
		}
		list = append(list, trackedExecutionToActive(exec))
	}
	api.trackedWorkflowExecutionsMux.RUnlock()
	for i := range list {
		api.enrichActiveWorkflowExecutionLifecycle(&list[i])
	}
	sort.Slice(list, func(i, j int) bool { return list[i].StartedAt.After(list[j].StartedAt) })
	return list
}

// enrichActiveWorkflowExecutionLifecycle is the sole projection used by both
// Global Monitor and workspace-scoped activity. It reads the same canonical
// runtime snapshot whose tracked query-root records drive scheduled-message
// completion, so UI status and scheduler sequencing cannot disagree about
// whether a turn is still active.
func (api *StreamingAPI) enrichActiveWorkflowExecutionLifecycle(execution *ActiveWorkflowExecution) {
	if api == nil || execution == nil {
		return
	}
	needsInput, _, waitingSince, waitingMessage := api.deriveSessionUserInputState(execution.SessionID)
	execution.NeedsUserInput = needsInput
	execution.WaitingMessage = waitingMessage
	execution.WaitingSince = waitingSince
	if snapshot, ok := api.authoritativeRuntimeSnapshot(execution.SessionID); ok {
		execution.RuntimeState = &snapshot
		execution.DisplayStatus = sessionDisplayStatusFromRuntime(snapshot).Status
	}
}

func (api *StreamingAPI) findRunningTrackedExecutionForWorkspaceWhere(
	workspacePath string,
	matches func(*TrackedWorkflowExecution) bool,
) *TrackedWorkflowExecution {
	normalizedWorkspace := normalizeTrackedWorkspacePath(workspacePath)
	if normalizedWorkspace == "" {
		return nil
	}

	api.trackedWorkflowExecutionsMux.RLock()
	defer api.trackedWorkflowExecutionsMux.RUnlock()

	var best *TrackedWorkflowExecution
	for _, exec := range api.trackedWorkflowExecutions {
		if exec == nil || exec.Status != trackedExecutionStatusRunning || exec.WorkspacePath != normalizedWorkspace {
			continue
		}
		if matches != nil && !matches(exec) {
			continue
		}
		if best == nil || exec.StartedAt.After(best.StartedAt) {
			copyExec := *exec
			best = &copyExec
		}
	}
	return best
}
