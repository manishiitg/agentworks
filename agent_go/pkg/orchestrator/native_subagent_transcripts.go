package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/backgroundtranscript"
	orchevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/toolerr"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func nativeChildExecutionID(owner, provider, nativeID string) string {
	sum := sha256.Sum256([]byte(owner + "\x00" + provider + "\x00" + nativeID))
	return "native-" + hex.EncodeToString(sum[:16])
}

// PersistNativeSubagentTranscripts uses the existing chat deletion fence and
// execution transcript directory. A registered parent must exist first.
func (bo *BaseOrchestrator) PersistNativeSubagentTranscripts(ctx context.Context, sessionID, ownerID string, snapshots []llmtypes.NativeSubagentTranscript) error {
	workspace := bo.GetWorkspacePath()
	if workspace == "" || sessionID == "" || ownerID == "" {
		return nil
	}
	unlock, deleted := backgroundtranscript.Lock(workspace, sessionID)
	defer unlock()
	if deleted {
		return nil
	}
	parentPath := orchevents.BackgroundAgentTranscriptPath(workspace, sessionID, ownerID)
	content, err := bo.ReadWorkspaceFile(ctx, parentPath)
	if err != nil {
		return err
	}
	parent, err := orchevents.ParseBackgroundAgentTranscript(content)
	if err != nil || parent == nil || parent.AgentID != ownerID || parent.SessionID != sessionID {
		return fmt.Errorf("native child has no registered parent transcript")
	}
	// Internal sequence items share their native session; fold native children
	// into the owning step so later turns update one child rather than duplicate it.
	if parent.Kind == "agent_item" && parent.ParentExecutionID != "" {
		containerPath := orchevents.BackgroundAgentTranscriptPath(workspace, sessionID, parent.ParentExecutionID)
		if raw, readErr := bo.ReadWorkspaceFile(ctx, containerPath); readErr == nil {
			if container, parseErr := orchevents.ParseBackgroundAgentTranscript(raw); parseErr == nil && container != nil && container.SessionID == sessionID && container.AgentID == parent.ParentExecutionID {
				parent, ownerID, parentPath = container, container.AgentID, containerPath
			}
		}
	}
	filtered := make([]llmtypes.NativeSubagentTranscript, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if !snapshot.UpdatedAt.Before(parent.StartedAt) {
			filtered = append(filtered, snapshot)
		}
	}
	snapshots = filtered
	ids := map[string]string{}
	for _, snapshot := range snapshots {
		if snapshot.ID != "" {
			ids[snapshot.ID] = nativeChildExecutionID(ownerID, snapshot.Provider, snapshot.ID)
		}
	}
	refs := map[string][]orchevents.NativeChildExecution{}
	for _, snapshot := range snapshots {
		id := ids[snapshot.ID]
		if id == "" {
			continue
		}
		parentID := ids[snapshot.ParentID]
		if parentID == "" {
			parentID = ownerID
		}
		refs[parentID] = append(refs[parentID], orchevents.NativeChildExecution{AgentID: id, ParentExecutionID: parentID, Name: snapshot.Name, Status: snapshot.Status, StartedAt: snapshot.StartedAt, UpdatedAt: snapshot.UpdatedAt})
	}
	for _, snapshot := range snapshots {
		id := ids[snapshot.ID]
		if id == "" {
			continue
		}
		parentID := ids[snapshot.ParentID]
		if parentID == "" {
			parentID = ownerID
		}
		filePath := orchevents.BackgroundAgentTranscriptPath(workspace, sessionID, id)
		if oldContent, readErr := bo.ReadWorkspaceFile(ctx, filePath); readErr == nil {
			if old, parseErr := orchevents.ParseBackgroundAgentTranscript(oldContent); parseErr == nil && old != nil && old.NativeUpdatedAt.After(snapshot.UpdatedAt) {
				continue
			}
		}
		child := orchevents.NewBackgroundAgentTranscript(sessionID, id, parentID, snapshot.Name, "sub_agent", snapshot.StartedAt)
		child.Provider = snapshot.Provider
		child.Status = snapshot.Status
		child.NativeUpdatedAt = snapshot.UpdatedAt
		child.NativeChildren = refs[id]
		if snapshot.Status != "running" {
			child.CompletedAt = snapshot.UpdatedAt
		}
		child.Events = nativeTranscriptEvents(snapshot)
		encoded, marshalErr := child.Marshal()
		if marshalErr != nil {
			return marshalErr
		}
		if writeErr := bo.WriteWorkspaceFile(ctx, filePath, encoded); writeErr != nil {
			return writeErr
		}
	}
	for _, ref := range refs[ownerID] {
		found := false
		for i, old := range parent.NativeChildren {
			if old.AgentID == ref.AgentID {
				found = true
				if !old.UpdatedAt.After(ref.UpdatedAt) {
					parent.NativeChildren[i] = ref
				}
				break
			}
		}
		if !found {
			parent.NativeChildren = append(parent.NativeChildren, ref)
		}
	}
	encoded, err := parent.Marshal()
	if err != nil {
		return err
	}
	return bo.WriteWorkspaceFile(ctx, parentPath, encoded)
}

func nativeTranscriptEvents(snapshot llmtypes.NativeSubagentTranscript) []orchevents.BackgroundAgentTranscriptEvent {
	var out []orchevents.BackgroundAgentTranscriptEvent
	calls := map[string]*events.ToolCallRecord{}
	for _, message := range snapshot.Messages {
		role := "assistant"
		if message.Role == llmtypes.ChatMessageTypeHuman {
			role = "user"
		}
		if message.Role == llmtypes.ChatMessageTypeSystem {
			role = "system"
		}
		for _, part := range message.Parts {
			switch p := part.(type) {
			case llmtypes.TextContent:
				if strings.TrimSpace(p.Text) != "" {
					out = append(out, orchevents.BackgroundAgentTranscriptEvent{Timestamp: snapshot.UpdatedAt, Type: role + "_message", Role: role, Text: p.Text, SourceType: "native_transcript"})
				}
			case llmtypes.ToolCall:
				if p.FunctionCall == nil {
					continue
				}
				call := &events.ToolCallRecord{ToolCallID: p.ID, ToolName: p.FunctionCall.Name, Arguments: p.FunctionCall.Arguments, Status: "running", ServerName: snapshot.Provider}
				calls[p.ID] = call
				out = append(out, orchevents.BackgroundAgentTranscriptEvent{Timestamp: snapshot.UpdatedAt, Type: "tool_call", ToolCall: call, SourceType: "native_transcript"})
			case llmtypes.ToolCallResponse:
				call := calls[p.ToolCallID]
				if call == nil {
					call = &events.ToolCallRecord{ToolCallID: p.ToolCallID, ToolName: p.Name, ServerName: snapshot.Provider}
					calls[p.ToolCallID] = call
					out = append(out, orchevents.BackgroundAgentTranscriptEvent{Timestamp: snapshot.UpdatedAt, Type: "tool_call", ToolCall: call, SourceType: "native_transcript"})
				}
				call.Result = p.Content
				call.Status = "completed"
				if p.IsError {
					call.Status = "failed"
					call.Error = p.Content
				} else if signal, failed := toolerr.CanonicalFailureForTool(call.ToolName, p.Content); failed {
					call.Status = "failed"
					call.Error = signal
				}
			}
		}
	}
	return out
}
