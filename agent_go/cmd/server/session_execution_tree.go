package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"

	"github.com/gorilla/mux"
)

const (
	sessionExecutionRootKind          = "session"
	sessionExecutionMainAgentKind     = "main_agent"
	sessionExecutionSyntheticTurnKind = "synthetic_turn"

	sessionExecutionDisplayBusy    = "busy"
	sessionExecutionDisplayIdle    = "idle"
	sessionExecutionDisplayStopped = "stopped"
)

type SessionExecutionTreeNode struct {
	ExecutionID       string                                `json:"execution_id"`
	ParentExecutionID string                                `json:"parent_execution_id,omitempty"`
	SessionID         string                                `json:"session_id"`
	Source            string                                `json:"source,omitempty"`
	Kind              string                                `json:"kind"`
	Name              string                                `json:"name"`
	Status            string                                `json:"status"`
	StartedAt         time.Time                             `json:"started_at"`
	CompletedAt       *time.Time                            `json:"completed_at,omitempty"`
	Error             string                                `json:"error,omitempty"`
	Metadata          map[string]string                     `json:"metadata,omitempty"`
	Children          []*SessionExecutionTreeNode           `json:"children,omitempty"`
	Inspection        *orchEvents.ExecutionInspectionTarget `json:"inspection,omitempty"`
}

type SessionExecutionTreeSummary struct {
	SessionID                   string `json:"session_id"`
	SessionStatus               string `json:"session_status"`
	DisplayStatus               string `json:"display_status"`
	IsSessionBusy               bool   `json:"is_session_busy"`
	RunningCount                int    `json:"running_count"`
	CompletedCount              int    `json:"completed_count"`
	FailedCount                 int    `json:"failed_count"`
	CanceledCount               int    `json:"canceled_count"`
	HasRunningMainAgent         bool   `json:"has_running_main_agent"`
	HasRunningBackgroundAgents  bool   `json:"has_running_background_agents"`
	HasRunningTrackedExecutions bool   `json:"has_running_tracked_executions"`
}

type SessionExecutionTreeResponse struct {
	SessionID    string                      `json:"session_id"`
	Root         *SessionExecutionTreeNode   `json:"root"`
	Summary      SessionExecutionTreeSummary `json:"summary"`
	RuntimeState *RuntimeSnapshot            `json:"runtime_state,omitempty"`
}

func cloneSessionExecutionMetadata(meta map[string]string) map[string]string {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]string, len(meta))
	for k, v := range meta {
		out[k] = v
	}
	return out
}

func cloneTrackedExecution(exec *TrackedWorkflowExecution) *TrackedWorkflowExecution {
	if exec == nil {
		return nil
	}
	copied := *exec
	copied.Metadata = cloneTrackedMetadata(exec.Metadata)
	return &copied
}

func (api *StreamingAPI) trackedExecutionsForSession(sessionID string) []*TrackedWorkflowExecution {
	api.trackedWorkflowExecutionsMux.RLock()
	defer api.trackedWorkflowExecutionsMux.RUnlock()

	list := make([]*TrackedWorkflowExecution, 0, len(api.trackedWorkflowExecutions))
	for _, exec := range api.trackedWorkflowExecutions {
		if exec == nil || exec.SessionID != sessionID {
			continue
		}
		list = append(list, cloneTrackedExecution(exec))
	}
	return list
}

func sessionExecutionSortLess(a, b *SessionExecutionTreeNode) bool {
	if a == nil || b == nil {
		return a != nil
	}
	if a.Status == trackedExecutionStatusRunning && b.Status != trackedExecutionStatusRunning {
		return true
	}
	if a.Status != trackedExecutionStatusRunning && b.Status == trackedExecutionStatusRunning {
		return false
	}
	if !a.StartedAt.Equal(b.StartedAt) {
		return a.StartedAt.Before(b.StartedAt)
	}
	return a.ExecutionID < b.ExecutionID
}

func sortSessionExecutionTree(node *SessionExecutionTreeNode) {
	if node == nil || len(node.Children) == 0 {
		return
	}
	sort.Slice(node.Children, func(i, j int) bool {
		return sessionExecutionSortLess(node.Children[i], node.Children[j])
	})
	for _, child := range node.Children {
		sortSessionExecutionTree(child)
	}
}

func eventDerivedExecutionName(event internalevents.Event, payload map[string]interface{}) string {
	metadata := mapValue(payload["metadata"])
	if name := stringValue(payload["name"]); name != "" {
		return name
	}
	if instruction := stringValue(payload["instruction"]); instruction != "" {
		return instruction
	}
	if agentType := stringValue(payload["agent_type"]); agentType != "" {
		return agentType
	}
	if stepID := firstSessionExecutionString(
		stringValue(metadata["current_step_id"]),
		stringValue(metadata["orchestrator_step_id"]),
		stringValue(metadata["step_id"]),
		stringValue(payload["step_id"]),
		stringValue(payload["workflow_step_id"]),
		stringValue(payload["route_id"]),
	); stepID != "" {
		return stepID
	}
	switch event.ExecutionKind {
	case "main_agent":
		return "Main Agent"
	case "delegation":
		return "Delegation"
	case "agent":
		return "Agent"
	case "workflow_step":
		return "Workflow Step"
	case "workflow":
		return "Workflow"
	default:
		return "Execution"
	}
}

func eventPayloadMap(event internalevents.Event) map[string]interface{} {
	if event.Data == nil || event.Data.Data == nil {
		return nil
	}
	raw, err := json.Marshal(event.Data.Data)
	if err != nil {
		return nil
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	if nested := mapValue(payload["data"]); nested != nil {
		for key, value := range nested {
			if _, exists := payload[key]; !exists {
				payload[key] = value
			}
		}
	}
	return payload
}

func stringValue(value interface{}) string {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func mapValue(value interface{}) map[string]interface{} {
	if nested, ok := value.(map[string]interface{}); ok {
		return nested
	}
	return nil
}

func firstSessionExecutionString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func eventDerivedExecutionStatus(event internalevents.Event, payload map[string]interface{}) (status string, completed bool, failed bool) {
	switch event.Type {
	case "agent_end", "conversation_end", "unified_completion", "todo_task_step_completed":
		if stringValue(payload["error"]) != "" || event.Error != "" {
			return trackedExecutionStatusFailed, true, true
		}
		if success, ok := payload["success"].(bool); ok && !success {
			return trackedExecutionStatusFailed, true, true
		}
		return trackedExecutionStatusCompleted, true, false
	case "agent_error", "conversation_error", "workflow_error":
		return trackedExecutionStatusFailed, true, true
	case "delegation_end", "orchestrator_agent_end", "background_agent_completed":
		if stringValue(payload["error"]) != "" || event.Error != "" {
			return trackedExecutionStatusFailed, true, true
		}
		if success, ok := payload["success"].(bool); ok && !success {
			return trackedExecutionStatusFailed, true, true
		}
		return trackedExecutionStatusCompleted, true, false
	case "orchestrator_agent_error":
		return trackedExecutionStatusFailed, true, true
	case "background_agent_terminated", "background_agent_canceled", "batch_execution_canceled", "context_cancelled":
		return trackedExecutionStatusCanceled, true, false
	default:
		return trackedExecutionStatusRunning, false, false
	}
}

func eventDerivedMainFallbackStatus(sessionStatus string) (string, bool) {
	switch strings.TrimSpace(sessionStatus) {
	case "completed":
		return trackedExecutionStatusCompleted, true
	case "error":
		return trackedExecutionStatusFailed, true
	case "stopped", "inactive", "dismissed":
		return trackedExecutionStatusCanceled, true
	default:
		return "", false
	}
}

func finalizeEventDerivedRunningNodes(sessionStatus string, completedAt time.Time, nodes map[string]*SessionExecutionTreeNode) {
	status, terminal := eventDerivedMainFallbackStatus(sessionStatus)
	if !terminal {
		return
	}
	for _, node := range nodes {
		if node == nil || node.Source != "event_stream" || node.Status != trackedExecutionStatusRunning {
			continue
		}
		node.Status = status
		if node.CompletedAt == nil {
			finishedAt := completedAt
			if finishedAt.IsZero() || finishedAt.Before(node.StartedAt) {
				finishedAt = node.StartedAt
			}
			node.CompletedAt = &finishedAt
		}
	}
}

func synthesizeSessionInfoFromEvents(sessionID string, rawEvents []internalevents.Event) *ActiveSessionInfo {
	if strings.TrimSpace(sessionID) == "" || len(rawEvents) == 0 {
		return nil
	}

	createdAt := rawEvents[0].Timestamp
	lastActivity := rawEvents[0].Timestamp
	status := trackedExecutionStatusRunning
	query := ""

	for _, event := range rawEvents {
		if event.Timestamp.Before(createdAt) {
			createdAt = event.Timestamp
		}
		if event.Timestamp.After(lastActivity) {
			lastActivity = event.Timestamp
		}
		if query == "" && event.Type == "user_message" {
			payload := eventPayloadMap(event)
			query = firstSessionExecutionString(stringValue(payload["content"]), stringValue(payload["message"]))
		}
		payload := eventPayloadMap(event)
		eventStatus, completed, failed := eventDerivedExecutionStatus(event, payload)
		if completed {
			status = eventStatus
			if failed {
				break
			}
		}
	}

	return &ActiveSessionInfo{
		SessionID:    sessionID,
		AgentMode:    "restored",
		Status:       status,
		CreatedAt:    createdAt,
		LastActivity: lastActivity,
		Query:        query,
	}
}

func (api *StreamingAPI) addEventDerivedExecutionNodes(sessionID, rootID, sessionStatus string, nodes map[string]*SessionExecutionTreeNode) {
	if api == nil || api.eventStore == nil {
		return
	}
	var lastMainEventAt *time.Time
	correlationStartNodes := map[string]string{}
	for _, event := range api.eventStore.GetAllEventsRaw(sessionID) {
		if event.Type == "streaming_start" || event.Type == "streaming_chunk" || event.Type == "streaming_end" {
			continue
		}
		executionID := strings.TrimSpace(event.ExecutionID)
		if executionID == "" || executionID == rootID {
			continue
		}
		payload := eventPayloadMap(event)
		parentID := strings.TrimSpace(event.ParentExecutionID)
		if parentID == "" {
			parentID = "main:" + sessionID
		}
		kind := strings.TrimSpace(event.ExecutionKind)
		if kind == "" {
			kind = "execution"
		}

		node := nodes[executionID]
		if node == nil {
			node = &SessionExecutionTreeNode{
				ExecutionID:       executionID,
				ParentExecutionID: parentID,
				SessionID:         sessionID,
				Source:            "event_stream",
				Kind:              kind,
				Name:              eventDerivedExecutionName(event, payload),
				Status:            trackedExecutionStatusRunning,
				StartedAt:         event.Timestamp,
				Metadata: map[string]string{
					"first_event_type": event.Type,
				},
			}
			nodes[executionID] = node
		}
		if node.ParentExecutionID == "" {
			node.ParentExecutionID = parentID
		}
		if node.Kind == "" {
			node.Kind = kind
		}
		if node.Name == "" || node.Name == "Execution" {
			node.Name = eventDerivedExecutionName(event, payload)
		}
		if event.Timestamp.Before(node.StartedAt) {
			node.StartedAt = event.Timestamp
		}
		if executionID == "main:"+sessionID {
			timestamp := event.Timestamp
			lastMainEventAt = &timestamp
		}

		status, completed, failed := eventDerivedExecutionStatus(event, payload)
		correlationID := firstSessionExecutionString(
			stringValue(payload["correlation_id"]),
			stringValue(payload["workflow_step_id"]),
		)
		if correlationID != "" && !completed {
			if _, exists := correlationStartNodes[correlationID]; !exists {
				correlationStartNodes[correlationID] = executionID
			}
		}
		if completed {
			node.Status = status
			completedAt := event.Timestamp
			node.CompletedAt = &completedAt
			if failed {
				if errText := firstSessionExecutionString(stringValue(payload["error"]), event.Error); errText != "" {
					node.Error = errText
				}
			}
			if correlationID != "" {
				if startExecutionID := correlationStartNodes[correlationID]; startExecutionID != "" && startExecutionID != executionID {
					if startNode := nodes[startExecutionID]; startNode != nil && startNode.Source == "event_stream" && startNode.Status == trackedExecutionStatusRunning {
						startNode.Status = status
						startNode.CompletedAt = &completedAt
						if failed {
							if errText := firstSessionExecutionString(stringValue(payload["error"]), event.Error); errText != "" {
								startNode.Error = errText
							}
						}
					}
				}
			}
		}
	}
	mainNode := nodes["main:"+sessionID]
	if mainNode != nil && mainNode.Source == "event_stream" && mainNode.Status == trackedExecutionStatusRunning {
		if status, ok := eventDerivedMainFallbackStatus(sessionStatus); ok {
			mainNode.Status = status
			if lastMainEventAt != nil {
				completedAt := *lastMainEventAt
				mainNode.CompletedAt = &completedAt
			}
		}
	}
}

func (api *StreamingAPI) buildSessionExecutionTree(session *ActiveSessionInfo) *SessionExecutionTreeResponse {
	if api == nil || session == nil || strings.TrimSpace(session.SessionID) == "" {
		return nil
	}

	rootID := "session:" + session.SessionID
	root := &SessionExecutionTreeNode{
		ExecutionID: rootID,
		SessionID:   session.SessionID,
		Source:      "session",
		Kind:        sessionExecutionRootKind,
		Name:        strings.TrimSpace(session.Query),
		Status:      session.Status,
		StartedAt:   session.CreatedAt,
		Metadata:    map[string]string{"agent_mode": session.AgentMode},
	}
	if root.Name == "" {
		root.Name = "Session"
	}

	nodes := map[string]*SessionExecutionTreeNode{
		rootID: root,
	}

	sessionBusy := api.isSessionBusy(session.SessionID)
	mainAgentRunning := api.canSteerSession(session.SessionID) || api.isSyntheticTurn(session.SessionID) || sessionBusy
	if mainAgentRunning || session.Status == "running" {
		kind := sessionExecutionMainAgentKind
		name := "Main Agent"
		if api.isSyntheticTurn(session.SessionID) || session.IsSyntheticTurn {
			kind = sessionExecutionSyntheticTurnKind
			name = "Main Agent (Auto-notification)"
		}
		nodes["main:"+session.SessionID] = &SessionExecutionTreeNode{
			ExecutionID:       "main:" + session.SessionID,
			ParentExecutionID: rootID,
			SessionID:         session.SessionID,
			Source:            "session",
			Kind:              kind,
			Name:              name,
			Status:            trackedExecutionStatusRunning,
			StartedAt:         session.LastActivity,
			Metadata:          map[string]string{"agent_mode": session.AgentMode},
		}
	}

	for _, exec := range api.trackedExecutionsForSession(session.SessionID) {
		if exec == nil {
			continue
		}
		parentID := rootID
		if exec.Metadata != nil {
			if candidate := strings.TrimSpace(exec.Metadata["parent_execution_id"]); candidate != "" {
				parentID = candidate
			}
		}
		name := strings.TrimSpace(exec.Title)
		if name == "" {
			name = strings.TrimSpace(exec.Name)
		}
		if name == "" {
			name = "Workflow Execution"
		}
		nodes[exec.ExecutionID] = &SessionExecutionTreeNode{
			ExecutionID:       exec.ExecutionID,
			ParentExecutionID: parentID,
			SessionID:         exec.SessionID,
			Source:            exec.Source,
			Kind:              exec.Kind,
			Name:              name,
			Status:            exec.Status,
			StartedAt:         exec.StartedAt,
			CompletedAt:       exec.CompletedAt,
			Error:             exec.LastError,
			Metadata:          cloneSessionExecutionMetadata(exec.Metadata),
		}
	}

	for _, agent := range api.bgAgentRegistry.GetAll(session.SessionID) {
		if agent == nil {
			continue
		}
		snap := agent.GetSnapshot()
		parentID := rootID
		if candidate := strings.TrimSpace(snap.ParentExecutionID); candidate != "" {
			parentID = candidate
		}
		kind := normalizeTrackedExecutionKind(snap.Kind)
		if kind == "" && snap.Metadata != nil {
			kind = normalizeTrackedExecutionKind(snap.Metadata["type"])
		}
		if kind == "" {
			kind = "background_agent"
		}
		name := strings.TrimSpace(snap.Name)
		if name == "" {
			name = "Background Agent"
		}
		metadata := cloneSessionExecutionMetadata(snap.Metadata)
		if existing := nodes[snap.ID]; existing != nil && len(metadata) == 0 {
			metadata = cloneSessionExecutionMetadata(existing.Metadata)
		}
		nodes[snap.ID] = &SessionExecutionTreeNode{
			ExecutionID:       snap.ID,
			ParentExecutionID: parentID,
			SessionID:         snap.SessionID,
			Source:            "background_agent_registry",
			Kind:              kind,
			Name:              name,
			Status:            string(snap.Status),
			StartedAt:         snap.CreatedAt,
			CompletedAt:       snap.CompletedAt,
			Error:             snap.Error,
			Metadata:          metadata,
		}
	}

	api.addEventDerivedExecutionNodes(session.SessionID, rootID, session.Status, nodes)
	finalizeEventDerivedRunningNodes(session.Status, session.LastActivity, nodes)

	summary := SessionExecutionTreeSummary{
		SessionID:     session.SessionID,
		SessionStatus: session.Status,
		IsSessionBusy: sessionBusy,
		DisplayStatus: sessionExecutionDisplayIdle,
	}

	for nodeID, node := range nodes {
		if node == nil || nodeID == rootID {
			continue
		}
		parent := nodes[node.ParentExecutionID]
		if parent == nil {
			parent = root
			node.ParentExecutionID = rootID
		}
		parent.Children = append(parent.Children, node)

		switch node.Status {
		case trackedExecutionStatusRunning:
			summary.RunningCount++
		case trackedExecutionStatusCompleted:
			summary.CompletedCount++
		case trackedExecutionStatusFailed:
			summary.FailedCount++
		case trackedExecutionStatusCanceled:
			summary.CanceledCount++
		}

		if node.Status == trackedExecutionStatusRunning {
			switch node.Source {
			case "session":
				summary.HasRunningMainAgent = true
			case "background_agent_registry":
				summary.HasRunningBackgroundAgents = true
			default:
				summary.HasRunningTrackedExecutions = true
			}
		}
	}

	runtimeState, _ := api.authoritativeRuntimeSnapshotForSession(session)
	runtimeStatus := sessionDisplayStatusFromRuntime(runtimeState)
	summary.DisplayStatus = runtimeStatus.Status
	summary.HasRunningBackgroundAgents = runtimeStatus.HasRunningBackgroundAgents

	sortSessionExecutionTree(root)

	return &SessionExecutionTreeResponse{
		SessionID:    session.SessionID,
		Root:         root,
		Summary:      summary,
		RuntimeState: &runtimeState,
	}
}

// handleGetSessionExecutionTree returns a backend-owned execution tree for one session.
// GET /api/sessions/{session_id}/execution-tree
func (api *StreamingAPI) handleGetSessionExecutionTree(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	sessionID := mux.Vars(r)["session_id"]
	if sessionID == "" {
		http.Error(w, "Session ID is required", http.StatusBadRequest)
		return
	}

	currentUserID := GetUserIDFromContext(r.Context())
	activeSession, exists := api.getActiveSession(sessionID)
	var sessionCopy ActiveSessionInfo
	if exists {
		if activeSession.UserID != "" && activeSession.UserID != currentUserID {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		sessionCopy = *activeSession
	} else {
		if api.eventStore == nil {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		if owner := api.eventStore.GetSessionOwner(sessionID); owner != "" && owner != currentUserID {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		} else if owner == "" && currentUserID != GetDefaultUserID() {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		sessionFromEvents := synthesizeSessionInfoFromEvents(sessionID, api.eventStore.GetAllEventsRaw(sessionID))
		if sessionFromEvents == nil {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		sessionCopy = *sessionFromEvents
	}

	tree := api.buildSessionExecutionTree(&sessionCopy)
	if tree == nil {
		http.Error(w, "Execution tree not available", http.StatusNotFound)
		return
	}
	// Navigation receipts are added only at this authenticated HTTP boundary,
	// after the inspector's current workspace and parent ownership checks.
	// Generic execution-tree consumers must not acquire inspection authority.
	workspace, valid := cleanWorkspaceReadPath(sessionCopy.WorkspacePath, true)
	if valid && api.ownsChatSession(r, sessionID) &&
		workspaceReadAllowed(r.Context(), GetUserFromContext(r.Context()), workspace, logicalPathIsDefaultUser, false) &&
		workflowStepInspectionWorkspace(r.Context(), workspace) {
		var addInspections func(*SessionExecutionTreeNode)
		addInspections = func(node *SessionExecutionTreeNode) {
			if node.SessionID == sessionID && node.Source == "background_agent_registry" && workflowStepInspectionKind(node.Kind) {
				node.Inspection = api.executionInspectionTarget(sessionID, node.ExecutionID)
			}
			for _, child := range node.Children {
				addInspections(child)
			}
		}
		addInspections(tree.Root)
	}

	if err := json.NewEncoder(w).Encode(tree); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}
