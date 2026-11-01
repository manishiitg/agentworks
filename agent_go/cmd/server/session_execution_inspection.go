package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/executioninspection"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

func inspectionIdentityPart(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, "/\\\x00")
}

// Workflows and Crew-owned programs expose their saved step records. Ordinary
// Crew chats and standalone programs keep their existing conversation surfaces.
func workflowStepInspectionWorkspace(ctx context.Context, workspace string) bool {
	if !strings.HasPrefix(strings.Trim(workspace, "/"), "Workflow/") {
		return false
	}
	content, exists, err := readFileFromWorkspace(ctx, strings.Trim(workspace, "/")+"/workflow.json")
	if err != nil {
		return false
	}
	if !exists {
		return true
	} // Legacy goal workflow without a manifest.
	var manifest struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal([]byte(content), &manifest) != nil {
		return false
	}
	if manifest.Kind == "" || manifest.Kind == "workflow" {
		return true
	}
	if manifest.Kind == "relay" {
		binding, err := findCrewExecutable(workspace)
		return err == nil && binding != nil && binding.Kind == "relay"
	}
	return false
}

func workflowStepInspectionKind(kind string) bool {
	switch kind {
	case "workflow_step", "agent", "scripted_step", "agent_item":
		return true
	}
	return false
}

func (api *StreamingAPI) executionInspectionTarget(sessionID, executionID string) *orchEvents.ExecutionInspectionTarget {
	workspace := api.backgroundAgentLogWorkspacePath(sessionID)
	if workspace == "" || !inspectionIdentityPart(sessionID) || !inspectionIdentityPart(executionID) || api.bgAgentRegistry == nil {
		return nil
	}
	agent := api.bgAgentRegistry.Get(sessionID, executionID)
	if agent == nil {
		return nil
	}
	snap := agent.GetSnapshot()
	if !workflowStepInspectionKind(snap.Kind) || strings.TrimSpace(snap.Metadata["step_id"]) == "" || !workflowStepInspectionWorkspace(context.Background(), workspace) {
		return nil
	}
	target := &orchEvents.ExecutionInspectionTarget{SessionID: sessionID, ExecutionID: executionID, WorkspacePath: workspace, Name: snap.Name, StartedAt: snap.CreatedAt, WorkflowKind: "workflow", ExecutionKind: snap.Kind}
	target.StepID = snap.Metadata["step_id"]
	target.ItemID = snap.Metadata["item_id"]
	target.RunFolder = strings.TrimSpace(snap.Metadata["run_folder"])
	return target
}

// Inspect reads one durable execution. It never starts/restores an agent or
// accepts a file path. Both current workflow access and the chat's authorship
// are checked before reading any transcript, including after a server restart.
func (api *StreamingAPI) handleInspectSessionExecution(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID, executionID := vars["session_id"], vars["execution_id"]
	workspace, valid := cleanWorkspaceReadPath(r.URL.Query().Get("workspace_path"), true)
	if !valid || !inspectionIdentityPart(sessionID) || !inspectionIdentityPart(executionID) ||
		!workspaceReadAllowed(r.Context(), GetUserFromContext(r.Context()), workspace, logicalPathIsDefaultUser, false) ||
		!workflowStepInspectionWorkspace(r.Context(), workspace) {
		http.Error(w, "Execution not found", http.StatusNotFound)
		return
	}
	if session, exists := api.getActiveSession(sessionID); exists && session != nil {
		if strings.Trim(session.WorkspacePath, "/") != workspace || !api.ownsChatSession(r, sessionID) {
			http.Error(w, "Execution not found", http.StatusNotFound)
			return
		}
	} else {
		userID := GetUserIDFromContext(r.Context())
		if userID == "" {
			userID = GetDefaultUserID()
		}
		content, err := ReadChatHistoryConversation(userID, sessionID, workspace)
		ownerID, _ := chatHistoryConversationIdentity(content)
		if err != nil || !chatHistoryCanResume(ownerID, userID, false) {
			http.Error(w, "Execution not found", http.StatusNotFound)
			return
		}
	}
	content, exists, err := readFileFromWorkspace(r.Context(), orchEvents.BackgroundAgentTranscriptPath(workspace, sessionID, executionID))
	if err != nil {
		http.Error(w, "Execution records temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	if !exists {
		http.Error(w, "Execution records have not been saved yet", http.StatusNotFound)
		return
	}
	transcript, err := orchEvents.ParseBackgroundAgentTranscript(content)
	if err != nil || transcript == nil || transcript.SessionID != sessionID || transcript.AgentID != executionID {
		http.Error(w, "Execution records unavailable", http.StatusConflict)
		return
	}
	transcript.RedactSensitiveText()
	// The durable record wins over volatile registry state and later selections.
	result := ""
	children := []BackgroundAgentLogEntry{}
	if records, err := backgroundAgentLogForSession(r.Context(), workspace, sessionID); err == nil {
		for _, record := range records {
			if record.AgentID == executionID {
				result = redaction.Text(record.Result)
			}
			if record.ParentExecutionID == executionID {
				record.Name = redaction.Text(record.Name)
				record.Result = redaction.Text(record.Result)
				record.Error = redaction.Text(record.Error)
				record.TranscriptStatus = redaction.Text(record.TranscriptStatus)
				children = append(children, record)
			}
		}
	}
	// Native children use the same saved transcript and navigation contract.
	appendNativeChildren := func(source *orchEvents.BackgroundAgentTranscript) {
		for _, ref := range source.NativeChildren {
			duplicate := false
			for _, existing := range children {
				if existing.AgentID == ref.AgentID {
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
			children = append(children, BackgroundAgentLogEntry{WorkspacePath: workspace, SessionID: sessionID, AgentID: ref.AgentID, ParentExecutionID: ref.ParentExecutionID, Name: redaction.Text(ref.Name), Kind: "sub_agent", Status: ref.Status, StartedAt: nativeInspectionTime(ref.StartedAt), CompletedAt: nativeInspectionTime(ref.UpdatedAt), TranscriptPath: orchEvents.BackgroundAgentTranscriptPath(workspace, sessionID, ref.AgentID), TranscriptStatus: "ok"})
		}
	}
	appendNativeChildren(transcript)
	mode, scripts := executioninspection.Records(transcript)
	if mode == "" && transcript.Kind == "workflow_step" {
		for _, child := range children {
			if child.Kind == "agent_item" {
				mode = "agent"
				break
			}
		}
	}
	var conversation *inspectionConversation
	if mode != "script" && !inspectionTranscriptHasConversation(transcript) {
		conversation = api.inspectionChildConversation(r.Context(), workspace, sessionID, executionID, children)
		if conversation != nil {
			mode = "agent"
		}
	}
	var linkedModelEvents []orchEvents.BackgroundAgentTranscriptEvent
	if conversation != nil {
		for _, event := range conversation.Events {
			linkedModelEvents = append(linkedModelEvents, event.BackgroundAgentTranscriptEvent)
		}
	}
	var promptEvents []executioninspection.PromptEvent
	for _, event := range transcript.Events {
		promptEvents = append(promptEvents, executioninspection.PromptEvent{Event: event, ExecutionID: transcript.AgentID})
	}
	if conversation != nil {
		for _, event := range conversation.Events {
			promptEvents = append(promptEvents, executioninspection.PromptEvent{Event: event.BackgroundAgentTranscriptEvent, ExecutionID: event.ExecutionID})
		}
	}
	systemPrompts := executioninspection.PromptVersions(promptEvents)
	model := executioninspection.Model(transcript, linkedModelEvents)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"transcript": transcript, "children": children, "result": result, "execution_mode": mode, "scripts": scripts, "conversation": conversation, "model": model, "system_prompts": systemPrompts})
}

type inspectionConversationEvent struct {
	orchEvents.BackgroundAgentTranscriptEvent
	ExecutionID string `json:"execution_id"`
}

// A read-only conversation view across the recorded turns of this execution.
// The original transcript and each source record remain unchanged.
type inspectionConversation struct {
	SourceExecutionIDs []string                      `json:"source_execution_ids"`
	Events             []inspectionConversationEvent `json:"events"`
	Truncated          bool                          `json:"truncated,omitempty"`
}

const (
	maxInspectionConversationChildren = 50
	maxInspectionConversationEvents   = 5000
	maxInspectionConversationBytes    = 8 << 20
)

func inspectionTranscriptHasConversation(transcript *orchEvents.BackgroundAgentTranscript) bool {
	for _, event := range transcript.Events {
		if event.ToolCall != nil || event.Role == "system" || event.Role == "user" || event.Role == "assistant" {
			return true
		}
	}
	return false
}

func (api *StreamingAPI) inspectionChildConversation(ctx context.Context, workspace, sessionID, parentID string, children []BackgroundAgentLogEntry) *inspectionConversation {
	view := &inspectionConversation{Events: []inspectionConversationEvent{}, SourceExecutionIDs: []string{}}
	readChildren, readBytes := 0, 0
	for _, child := range children {
		if child.Kind != "agent_item" || child.ParentExecutionID != parentID || !inspectionIdentityPart(child.AgentID) {
			continue
		}
		if ctx.Err() != nil || readChildren >= maxInspectionConversationChildren || len(view.Events) >= maxInspectionConversationEvents {
			view.Truncated = true
			break
		}
		readChildren++
		remainingBytes := maxInspectionConversationBytes - readBytes
		if remainingBytes <= 0 {
			view.Truncated = true
			break
		}
		content, exists, err := readFileFromWorkspaceQueryWithLimit(ctx, orchEvents.BackgroundAgentTranscriptPath(workspace, sessionID, child.AgentID), "", int64(remainingBytes))
		if errors.Is(err, errWorkspaceReadLimit) {
			view.Truncated = true
			continue
		}
		if err != nil || !exists {
			continue
		}
		// Bound decoding and the combined response without changing saved evidence.
		if len(content) > maxInspectionConversationBytes-readBytes {
			view.Truncated = true
			continue
		}
		readBytes += len(content)
		source, err := orchEvents.ParseBackgroundAgentTranscript(content)
		if err != nil || source == nil || source.AgentID != child.AgentID || source.SessionID != sessionID || source.ParentExecutionID != parentID || !inspectionTranscriptHasConversation(source) {
			continue
		}
		source.RedactSensitiveText()
		view.SourceExecutionIDs = append(view.SourceExecutionIDs, source.AgentID)
		for _, event := range source.Events {
			if len(view.Events) >= maxInspectionConversationEvents {
				view.Truncated = true
				break
			}
			view.Events = append(view.Events, inspectionConversationEvent{BackgroundAgentTranscriptEvent: event, ExecutionID: source.AgentID})
		}
	}
	if len(view.Events) == 0 && !view.Truncated {
		return nil
	}
	sort.SliceStable(view.Events, func(i, j int) bool { return view.Events[i].Timestamp.Before(view.Events[j].Timestamp) })
	return view
}

func nativeInspectionTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

// inspectionParentSession uses the same current-project and exact-chat-owner
// checks as Inspect. History remains authoritative after live sessions expire.
func (api *StreamingAPI) inspectionParentSession(r *http.Request, sessionID, workspace string) (*ActiveSessionInfo, bool) {
	if !inspectionIdentityPart(sessionID) || !workspaceReadAllowed(r.Context(), GetUserFromContext(r.Context()), workspace, logicalPathIsDefaultUser, false) || !workflowStepInspectionWorkspace(r.Context(), workspace) {
		return nil, false
	}
	return api.inspectionParentSessionInAuthorizedWorkspace(r, sessionID, workspace)
}

// The caller must check the current product/Goal ACL before using this helper.
// A workspace listing does that once per request; exact parent ownership is
// still checked (and only memoized within that request) for every session.
func (api *StreamingAPI) inspectionParentSessionInAuthorizedWorkspace(r *http.Request, sessionID, workspace string) (*ActiveSessionInfo, bool) {
	if !inspectionIdentityPart(sessionID) {
		return nil, false
	}
	if session, exists := api.getActiveSession(sessionID); exists && session != nil {
		if strings.Trim(session.WorkspacePath, "/") != workspace || !api.ownsChatSession(r, sessionID) {
			return nil, false
		}
		copy := *session
		return &copy, true
	}
	userID := GetUserIDFromContext(r.Context())
	if userID == "" {
		userID = GetDefaultUserID()
	}
	content, err := ReadChatHistoryConversation(userID, sessionID, workspace)
	owner, _ := chatHistoryConversationIdentity(content)
	if err != nil || !chatHistoryCanResume(owner, userID, false) {
		return nil, false
	}
	runtime, runtimeErr := chatHistoryRuntimeFromJSON(content)
	if runtimeErr != nil {
		return nil, false
	}
	if runtime != nil && runtime.WorkspacePath != "" {
		if canonicalChatHistoryWorkspacePath(userID, runtime.WorkspacePath) != canonicalChatHistoryWorkspacePath(userID, workspace) {
			return nil, false
		}
	} else {
		// Older conversations have no runtime binding. They must actually be
		// stored under this workflow, not merely found in the account index.
		_, found, readErr := readWorkflowScopedChatHistoryConversationDirect(sessionID, workspace)
		if readErr != nil {
			return nil, false
		}
		if !found {
			_, found, readErr = readWorkflowScopedChatHistoryConversationFromWorkspace(sessionID, workspace)
		}
		if readErr != nil || !found {
			return nil, false
		}
	}
	var source struct {
		BotPlatform string `json:"bot_platform"`
		TriggeredBy string `json:"triggered_by"`
	}
	_ = json.Unmarshal(content, &source)
	return &ActiveSessionInfo{SessionID: sessionID, UserID: userID, WorkspacePath: workspace, Status: "completed", BotPlatform: source.BotPlatform, TriggeredBy: source.TriggeredBy}, true
}

// Reuse explicit saved receipts; never reconstruct a target from a step name
// or path. The enclosing HTTP handler has already authorized the exact parent.
func appendDurableStepInspections(ctx context.Context, tree *SessionExecutionTreeResponse, workspace, sessionID string) {
	records, err := backgroundAgentLogForSession(ctx, workspace, sessionID)
	if err != nil || tree == nil || tree.Root == nil {
		return
	}
	nodes := map[string]*SessionExecutionTreeNode{}
	var visit func(*SessionExecutionTreeNode)
	visit = func(node *SessionExecutionTreeNode) {
		if node == nil || len(nodes) >= 1000 {
			return
		}
		if _, seen := nodes[node.ExecutionID]; seen {
			return
		}
		nodes[node.ExecutionID] = node
		for _, child := range node.Children {
			visit(child)
		}
	}
	visit(tree.Root)
	byID := make(map[string]BackgroundAgentLogEntry, len(records))
	for _, record := range records {
		byID[record.AgentID] = record
	}
	inspected := 0
	for _, record := range records {
		if (record.Kind != "" && !workflowStepInspectionKind(record.Kind)) || record.Kind == "agent_item" || !inspectionIdentityPart(record.AgentID) {
			continue
		}
		nested := false
		parentID := record.ParentExecutionID
		for visited := 0; parentID != "" && visited < 1000; visited++ {
			if node := nodes[parentID]; node != nil && node.Inspection != nil {
				nested = true
				break
			}
			parent, found := byID[parentID]
			if !found {
				break
			}
			if parentID == record.AgentID || workflowStepInspectionKind(parent.Kind) {
				nested = true
				break
			}
			parentID = parent.ParentExecutionID
		}
		if nested {
			continue
		}
		if node := nodes[record.AgentID]; node != nil && node.Inspection != nil {
			continue
		}
		key := inspectionReceiptCacheKey{Store: getWorkspaceAPIURL(), Workspace: workspace, Session: sessionID, Execution: record.AgentID, UpdatedAt: record.UpdatedAt, Status: record.Status}
		target, cached := cachedInspectionReceipt(key)
		if !cached {
			if inspected >= 100 {
				break
			}
			inspected++
			target = readDurableInspectionReceipt(ctx, key)
		}
		if target == nil {
			continue
		}
		node := nodes[target.ExecutionID]
		if node == nil {
			node = &SessionExecutionTreeNode{SessionID: sessionID, ExecutionID: target.ExecutionID, ParentExecutionID: tree.Root.ExecutionID, Source: "background_agent_log", Kind: target.ExecutionKind, Name: target.Name, Status: record.Status, StartedAt: target.StartedAt}
			tree.Root.Children = append(tree.Root.Children, node)
			nodes[target.ExecutionID] = node
		}
		node.Inspection = target
	}
	sortSessionExecutionTree(tree.Root)
}
