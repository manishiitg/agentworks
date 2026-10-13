package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/executioninspection"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

func inspectionIdentityPart(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, "/\\\x00")
}

// Only goal-workflow executions get the step inspector. Crew/product chats and
// Relays can share the execution recorder without advertising this workflow UI.
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
	return manifest.Kind == "" || manifest.Kind == "workflow"
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
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"transcript": transcript, "children": children, "result": result, "execution_mode": mode, "scripts": scripts, "conversation": conversation})
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
}

func inspectionTranscriptHasConversation(transcript *orchEvents.BackgroundAgentTranscript) bool {
	for _, event := range transcript.Events {
		if event.ToolCall != nil || event.Role == "system" || event.Role == "user" || event.Role == "assistant" {
			return true
		}
	}
	return false
}

func (api *StreamingAPI) inspectionChildConversation(ctx context.Context, workspace, sessionID, parentID string, children []BackgroundAgentLogEntry) *inspectionConversation {
	view := &inspectionConversation{}
	for _, child := range children {
		if child.Kind != "agent_item" || child.ParentExecutionID != parentID || !inspectionIdentityPart(child.AgentID) {
			continue
		}
		content, exists, err := readFileFromWorkspace(ctx, orchEvents.BackgroundAgentTranscriptPath(workspace, sessionID, child.AgentID))
		if err != nil || !exists {
			continue
		}
		source, err := orchEvents.ParseBackgroundAgentTranscript(content)
		if err != nil || source == nil || source.AgentID != child.AgentID || source.SessionID != sessionID || source.ParentExecutionID != parentID || !inspectionTranscriptHasConversation(source) {
			continue
		}
		source.RedactSensitiveText()
		view.SourceExecutionIDs = append(view.SourceExecutionIDs, source.AgentID)
		for _, event := range source.Events {
			view.Events = append(view.Events, inspectionConversationEvent{BackgroundAgentTranscriptEvent: event, ExecutionID: source.AgentID})
		}
	}
	if len(view.Events) == 0 {
		return nil
	}
	sort.SliceStable(view.Events, func(i, j int) bool { return view.Events[i].Timestamp.Before(view.Events[j].Timestamp) })
	return view
}
