package server

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

const trackedExecutionSourceConversationTurn = "conversation_turn"

// withConversationTurnExecutionID makes the exact message/continuation that
// owns a coding-agent turn available to every tool call made by that turn.
// Live input reuses the original agent context, so this identity must be
// installed when the stream starts rather than reconstructed from session
// activity later.
func withConversationTurnExecutionID(ctx context.Context, executionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	executionID = strings.TrimSpace(executionID)
	if executionID == "" {
		return ctx
	}
	ctx = virtualtools.WithBackgroundAgentID(ctx, executionID)
	return context.WithValue(ctx, orchestratorevents.ParentExecutionIDKey, executionID)
}

// conversationTurnTreeState is the canonical lifecycle projection for one
// message sent into a conversation. The query id returned by /api/query is the
// root execution id; background agents and tracked child executions attach to
// it through parent_execution_id.
type conversationTurnTreeState struct {
	RootFound       bool
	RootStatus      string
	RootError       string
	RunningChildren int
	LastProgressAt  time.Time
}

func (state conversationTurnTreeState) terminal() bool {
	return state.RootFound && state.RootStatus != trackedExecutionStatusRunning && state.RunningChildren == 0
}

func (api *StreamingAPI) trackConversationTurnStart(queryID, sessionID string, req QueryRequest) {
	if api == nil || strings.TrimSpace(queryID) == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	metadata := map[string]string{
		"parent_execution_id": "session:" + strings.TrimSpace(sessionID),
	}
	if req.ExecutionOptions != nil {
		if mode := normalizeChatHistoryWorkshopMode(req.ExecutionOptions.WorkshopMode); mode != "" {
			metadata["workshop_mode"] = mode
		}
	}
	kind := "conversation_turn"
	phaseID := strings.TrimSpace(req.PhaseID)
	phaseName := "Conversation"
	if phaseID != "" {
		kind = "workflow_builder_task"
		phaseName = "Workflow Builder"
	}
	api.trackExecutionStart(&TrackedWorkflowExecution{
		ExecutionID:   strings.TrimSpace(queryID),
		SessionID:     strings.TrimSpace(sessionID),
		Source:        trackedExecutionSourceConversationTurn,
		Kind:          kind,
		Name:          strings.TrimSpace(req.Query),
		Query:         strings.TrimSpace(req.Query),
		PresetQueryID: strings.TrimSpace(req.PresetQueryID),
		WorkspacePath: strings.TrimSpace(req.SelectedFolder),
		PhaseID:       phaseID,
		PhaseName:     phaseName,
		Status:        trackedExecutionStatusRunning,
		UserID:        strings.TrimSpace(req.userID),
		Title:         strings.TrimSpace(req.SessionTitle),
		TriggeredBy:   strings.TrimSpace(req.TriggeredBy),
		StartedAt:     time.Now().UTC(),
		Metadata:      metadata,
	})
}

func (api *StreamingAPI) trackSyntheticConversationTurnStart(executionID, sessionID, parentExecutionID, message string) {
	if api == nil || strings.TrimSpace(executionID) == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	metadata := map[string]string{}
	if parentExecutionID = strings.TrimSpace(parentExecutionID); parentExecutionID != "" {
		metadata["parent_execution_id"] = parentExecutionID
	}
	api.trackExecutionStart(&TrackedWorkflowExecution{
		ExecutionID: strings.TrimSpace(executionID),
		SessionID:   strings.TrimSpace(sessionID),
		Source:      trackedExecutionSourceConversationTurn,
		Kind:        "synthetic_turn",
		Name:        "Process background result",
		Query:       strings.TrimSpace(message),
		Status:      trackedExecutionStatusRunning,
		TriggeredBy: "auto_notification",
		StartedAt:   time.Now().UTC(),
		Metadata:    metadata,
	})
}

// currentConversationTurnExecutionID returns the exact message currently
// owning foreground work in a session. It is used only as the fallback parent
// for a first-level background agent; nested agents already carry their direct
// parent explicitly.
func (api *StreamingAPI) currentConversationTurnExecutionID(sessionID string) string {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	api.trackedWorkflowExecutionsMux.RLock()
	defer api.trackedWorkflowExecutionsMux.RUnlock()
	var best *TrackedWorkflowExecution
	for _, execution := range api.trackedWorkflowExecutions {
		if execution == nil || execution.SessionID != sessionID || execution.Status != trackedExecutionStatusRunning {
			continue
		}
		if execution.Source != trackedExecutionSourceConversationTurn {
			continue
		}
		if best == nil || execution.StartedAt.After(best.StartedAt) {
			best = execution
		}
	}
	if best == nil {
		return ""
	}
	return best.ExecutionID
}

func trackedExecutionParentID(execution *TrackedWorkflowExecution) string {
	if execution == nil || execution.Metadata == nil {
		return ""
	}
	return strings.TrimSpace(execution.Metadata["parent_execution_id"])
}

// conversationTurnTreeSnapshot projects the root message and all recursively
// attached descendants from the same stores used by the execution-tree API and
// Global Monitor runtime snapshot. Unrelated work in the same session is not
// allowed to hold this turn open or complete it.
func (api *StreamingAPI) conversationTurnTreeSnapshot(rootExecutionID string, ignoreRunningIDs ...string) conversationTurnTreeState {
	state := conversationTurnTreeState{}
	if api == nil || strings.TrimSpace(rootExecutionID) == "" {
		return state
	}

	api.trackedWorkflowExecutionsMux.RLock()
	root := cloneTrackedExecution(api.trackedWorkflowExecutions[rootExecutionID])
	tracked := make([]*TrackedWorkflowExecution, 0, len(api.trackedWorkflowExecutions))
	if root != nil {
		for _, execution := range api.trackedWorkflowExecutions {
			if execution != nil && execution.SessionID == root.SessionID {
				tracked = append(tracked, cloneTrackedExecution(execution))
			}
		}
	}
	api.trackedWorkflowExecutionsMux.RUnlock()
	if root == nil {
		return state
	}

	state.RootFound = true
	state.RootStatus = root.Status
	state.RootError = root.LastError
	state.LastProgressAt = root.StartedAt
	if root.CompletedAt != nil && root.CompletedAt.After(state.LastProgressAt) {
		state.LastProgressAt = *root.CompletedAt
	}

	now := time.Now()
	parents := map[string][]string{}
	statusByID := map[string]string{root.ExecutionID: root.Status}
	progressByID := map[string]time.Time{root.ExecutionID: state.LastProgressAt}
	// Per-step workflow progress records are a UI mirror, not work, and must
	// never decide whether this turn may finish (PLAT-117). See
	// BackgroundAgentSnapshot.IsProgressMirror.
	progressOnly := map[string]bool{}
	for _, execution := range tracked {
		if execution == nil || execution.ExecutionID == root.ExecutionID {
			continue
		}
		parentID := trackedExecutionParentID(execution)
		if parentID == "" {
			continue
		}
		parents[parentID] = append(parents[parentID], execution.ExecutionID)
		statusByID[execution.ExecutionID] = execution.Status
		progress := execution.StartedAt
		if execution.CompletedAt != nil && execution.CompletedAt.After(progress) {
			progress = *execution.CompletedAt
		}
		progressByID[execution.ExecutionID] = progress
	}

	if api.bgAgentRegistry != nil {
		ownedSession := api.sessionCompletionsOwned(root.SessionID)
		for _, agent := range api.bgAgentRegistry.GetAll(root.SessionID) {
			if agent == nil {
				continue
			}
			snapshot := agent.GetSnapshot()
			parentID := strings.TrimSpace(snapshot.ParentExecutionID)
			if parentID == "" {
				continue
			}
			parents[parentID] = append(parents[parentID], snapshot.ID)
			if snapshot.IsProgressMirror() {
				progressOnly[snapshot.ID] = true
			}
			status := string(snapshot.Status)
			// A completed child is not finished from the parent's perspective until
			// its completion has either launched a synthetic continuation or been
			// explicitly suppressed. Holding it in running closes the otherwise tiny
			// race between child completion and continuation registration.
			//
			// The hold is BOUNDED (PLAT-117). Unbounded, it makes turn completion
			// depend on notification bookkeeping being perfect, and every path that
			// leaves a terminal child unnotified becomes a permanent hang rather
			// than a delay: the orphan settle that caused this ticket, and also the
			// deliberate discard when a session goes unreachable with completions
			// still pending. Delivery retries every 5s and re-arms itself while work
			// remains, so the legitimate hand-off is orders of magnitude inside this
			// window — and it only ever matters when nothing else in the tree is
			// running, since a busy session keeps the tree alive on its own.
			suppressNotification := snapshot.Metadata != nil && snapshot.Metadata["suppress_auto_notification"] == "true"
			// A session owned by a runner (a scheduled run) gets its results
			// from that runner right after this turn, so a finished child is
			// finished; holding it would only delay every round.
			if ownedSession {
				suppressNotification = true
			}
			if (snapshot.Status == BGAgentCompleted || snapshot.Status == BGAgentFailed) &&
				!suppressNotification && !snapshot.CompletionNotified &&
				withinContinuationHandoffGrace(snapshot, now) {
				status = trackedExecutionStatusRunning
			}
			statusByID[snapshot.ID] = status
			progress := snapshot.CreatedAt
			if snapshot.CompletedAt != nil && snapshot.CompletedAt.After(progress) {
				progress = *snapshot.CompletedAt
			}
			progressByID[snapshot.ID] = progress
		}
	}

	ignored := make(map[string]bool, len(ignoreRunningIDs))
	for _, id := range ignoreRunningIDs {
		ignored[id] = true
	}
	seen := map[string]bool{root.ExecutionID: true}
	queue := []string{root.ExecutionID}
	for len(queue) > 0 {
		parentID := queue[0]
		queue = queue[1:]
		for _, childID := range parents[parentID] {
			if seen[childID] {
				continue
			}
			seen[childID] = true
			queue = append(queue, childID)
			if statusByID[childID] == trackedExecutionStatusRunning && !progressOnly[childID] && !ignored[childID] {
				state.RunningChildren++
			}
			if progressByID[childID].After(state.LastProgressAt) {
				state.LastProgressAt = progressByID[childID]
			}
		}
	}

	// Terminal and tool activity are already consolidated into the canonical
	// runtime snapshot used by Global Monitor. It may advance LastProgressAt,
	// but it cannot declare this exact tree complete.
	if runtime, ok := api.authoritativeRuntimeSnapshot(root.SessionID); ok && runtime.LastProgressAt.After(state.LastProgressAt) {
		state.LastProgressAt = runtime.LastProgressAt
	}
	return state
}

// continuationHandoffGrace bounds how long a finished-but-unnotified child may
// hold its turn open while its continuation is handed off (PLAT-117).
//
// The race it protects is milliseconds wide: a child finishes, and a synthetic
// turn or steered message is registered immediately afterwards. Delivery is
// retried every 5s and re-arms while work remains, so two minutes is orders of
// magnitude more than any legitimate hand-off needs — and the hold only has any
// effect once nothing else in the tree is running, because a busy session keeps
// the tree alive by itself.
//
// What the bound buys is that no stuck notification flag can make a turn
// permanently unfinishable. Before it, "terminal but never notified" was a
// permanent hang, which is what this ticket's orphaned progress mirrors caused,
// and what a discard-on-unreachable-session would cause too.
const continuationHandoffGrace = 2 * time.Minute

const (
	durableWorkflowFailureProbeDelay    = 30 * time.Second
	durableWorkflowFailureProbeInterval = 5 * time.Second
)

// withinContinuationHandoffGrace reports whether a terminal child finished
// recently enough that its continuation may still legitimately be in flight.
//
// A terminal child always carries CompletedAt (both SetResult and SetError
// stamp it), so the nil case is malformed rather than expected; it holds, which
// preserves the pre-bound behavior rather than risking a turn ending early.
func withinContinuationHandoffGrace(snapshot BackgroundAgentSnapshot, now time.Time) bool {
	if snapshot.CompletedAt == nil {
		return true
	}
	return now.Sub(*snapshot.CompletedAt) < continuationHandoffGrace
}

// durableFailedWorkflowDescendant checks the run's own terminal record when a
// scheduled conversation turn is still being held open by a linked full-run
// container. The durable workflow result is more authoritative than an
// in-memory completion callback that may never arrive. Only explicit failure is
// actionable here: a successful full run may still legitimately need its
// completion notification to resume the main agent for follow-up work.
func (api *StreamingAPI) durableFailedWorkflowDescendant(ctx context.Context, rootExecutionID string) (string, string, bool) {
	if api == nil || strings.TrimSpace(rootExecutionID) == "" {
		return "", "", false
	}

	api.trackedWorkflowExecutionsMux.RLock()
	root := cloneTrackedExecution(api.trackedWorkflowExecutions[rootExecutionID])
	executions := make(map[string]*TrackedWorkflowExecution)
	if root != nil {
		for id, execution := range api.trackedWorkflowExecutions {
			if execution != nil && execution.SessionID == root.SessionID {
				executions[id] = cloneTrackedExecution(execution)
			}
		}
	}
	api.trackedWorkflowExecutionsMux.RUnlock()
	if root == nil {
		return "", "", false
	}

	children := make(map[string][]string)
	for id, execution := range executions {
		if id == rootExecutionID {
			continue
		}
		if parentID := trackedExecutionParentID(execution); parentID != "" {
			children[parentID] = append(children[parentID], id)
		}
	}
	seen := map[string]bool{rootExecutionID: true}
	queue := []string{rootExecutionID}
	for len(queue) > 0 {
		parentID := queue[0]
		queue = queue[1:]
		for _, childID := range children[parentID] {
			if seen[childID] {
				continue
			}
			seen[childID] = true
			queue = append(queue, childID)
			execution := executions[childID]
			if execution == nil || execution.Status != trackedExecutionStatusRunning {
				continue
			}
			executionType := normalizeTrackedExecutionKind(execution.Metadata["execution_type"])
			if normalizeTrackedExecutionKind(execution.Kind) != normalizeTrackedExecutionKind(string(orchestratorevents.ExecutionKindFullRun)) && executionType != "full_workflow" {
				continue
			}
			runFolder := strings.TrimSpace(execution.RunFolder)
			if runFolder == "" {
				runFolder = strings.TrimSpace(execution.Metadata["run_folder"])
			}
			workspacePath := strings.TrimSpace(execution.WorkspacePath)
			if workspacePath == "" || runFolder == "" {
				continue
			}
			metadataPath := filepath.Join(workspacePath, "runs", runFolder, "run_metadata.json")
			metadata, err := readRunMetadata(ctx, metadataPath)
			if err != nil || metadata == nil || !strings.EqualFold(strings.TrimSpace(metadata.Status), "failed") {
				continue
			}
			return childID, runFolder, true
		}
	}
	return "", "", false
}

// waitForConversationTurnTree is the single completion waiter for scheduled
// messages. Events only wake the loop; completion comes exclusively from the
// exact query-rooted execution tree.
func (api *StreamingAPI) waitForConversationTurnTree(ctx context.Context, sessionID, rootExecutionID string, maxInactivity time.Duration) error {
	if api == nil {
		return fmt.Errorf("execution lifecycle is not configured")
	}
	var eventCh <-chan internalevents.Event
	var unsubscribe func()
	if api.eventStore != nil {
		sub := api.eventStore.Subscribe(sessionID)
		eventCh = sub.Ch
		unsubscribe = func() { api.eventStore.Unsubscribe(sessionID, sub) }
	}
	if unsubscribe != nil {
		defer unsubscribe()
	}

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	waitStartedAt := time.Now()
	lastProgressAt := waitStartedAt
	lastDurableFailureProbeAt := time.Time{}
	rootMissingSince := waitStartedAt
	check := func() (bool, error) {
		state := api.conversationTurnTreeSnapshot(rootExecutionID)
		if !state.RootFound {
			if time.Since(rootMissingSince) > 5*time.Second {
				return false, fmt.Errorf("execution lifecycle record %s was not registered", rootExecutionID)
			}
			return false, nil
		}
		if state.LastProgressAt.After(lastProgressAt) {
			lastProgressAt = state.LastProgressAt
		}
		if state.RootStatus == trackedExecutionStatusCompleted && state.RunningChildren > 0 &&
			time.Since(lastProgressAt) >= durableWorkflowFailureProbeDelay &&
			(lastDurableFailureProbeAt.IsZero() || time.Since(lastDurableFailureProbeAt) >= durableWorkflowFailureProbeInterval) {
			lastDurableFailureProbeAt = time.Now()
			if childID, runFolder, failed := api.durableFailedWorkflowDescendant(ctx, rootExecutionID); failed {
				return true, fmt.Errorf("%w: linked workflow execution %s run %s recorded status failed", errWorkshopSessionFailed, childID, runFolder)
			}
		}
		if state.terminal() {
			switch state.RootStatus {
			case trackedExecutionStatusCompleted:
				return true, nil
			case trackedExecutionStatusCanceled:
				return true, fmt.Errorf("%w: execution %s was canceled", errWorkshopSequenceInterrupted, rootExecutionID)
			case trackedExecutionStatusFailed:
				reason := strings.TrimSpace(state.RootError)
				if reason == "" {
					reason = "turn failed"
				}
				return true, fmt.Errorf("%w: execution %s: %s", errWorkshopSessionFailed, rootExecutionID, reason)
			default:
				return true, fmt.Errorf("execution %s ended with unsupported status %q", rootExecutionID, state.RootStatus)
			}
		}
		if maxInactivity > 0 && time.Since(lastProgressAt) >= maxInactivity {
			if state.RunningChildren > 0 && time.Since(waitStartedAt) < schedulerWorkshopLiveChildCeiling {
				lastProgressAt = time.Now()
				return false, nil
			}
			timeoutErr := fmt.Errorf("%w: execution %s made no progress for %s (running_children=%d)", errWorkshopIdleWaitTimeout, rootExecutionID, maxInactivity, state.RunningChildren)
			return true, api.diagnoseAndCleanupStalledConversationTurn(sessionID, rootExecutionID, waitStartedAt, timeoutErr)
		}
		return false, nil
	}

	for {
		if done, err := check(); done || err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		case _, ok := <-eventCh:
			if eventCh != nil && !ok {
				eventCh = nil
			}
		}
	}
}
