package events

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	"github.com/manishiitg/mcpagent/events"
)

type ExecutionInspectionTarget struct {
	WorkflowKind  string    `json:"workflow_kind,omitempty"`
	ExecutionKind string    `json:"execution_kind,omitempty"`
	SessionID     string    `json:"session_id"`
	ExecutionID   string    `json:"execution_id"`
	WorkspacePath string    `json:"workspace_path"`
	Name          string    `json:"name"`
	StepID        string    `json:"step_id,omitempty"`
	RunFolder     string    `json:"run_folder,omitempty"`
	ItemID        string    `json:"item_id,omitempty"`
	StartedAt     time.Time `json:"started_at"`
}

type BackgroundAgentTranscript struct {
	Inspection        *ExecutionInspectionTarget `json:"inspection,omitempty"`
	SessionID         string                     `json:"session_id"`
	AgentID           string                     `json:"agent_id"`
	ParentExecutionID string                     `json:"parent_execution_id,omitempty"`
	Name              string                     `json:"name,omitempty"`
	Kind              string                     `json:"kind,omitempty"`
	ModelID           string                     `json:"model_id,omitempty"`
	Provider          string                     `json:"provider,omitempty"`
	StartedAt         time.Time                  `json:"started_at"`
	CompletedAt       time.Time                  `json:"completed_at,omitempty"`
	// Status is "running" until MarkTerminal is called with "completed",
	// "failed", or "canceled". A transcript left at "running" forever is
	// itself a real signal, same as background_agent_log (PLAT-114).
	Status          string                           `json:"status"`
	Error           string                           `json:"error,omitempty"`
	Events          []BackgroundAgentTranscriptEvent `json:"events"`
	NativeChildren  []NativeChildExecution           `json:"native_children,omitempty"`
	NativeUpdatedAt time.Time                        `json:"native_updated_at,omitempty"`
}

// BackgroundAgentTranscriptEvent is one normalized, provider-agnostic
// record. The Type discriminates which fields are populated; consumers
// should not need to special-case a coding-CLI transport vs an API provider
// — both funnel through the same typed events before reaching here.
type BackgroundAgentTranscriptEvent struct {
	Timestamp        time.Time `json:"timestamp"`
	TurnID           string    `json:"turn_id,omitempty"`
	RuntimeSessionID string    `json:"runtime_session_id,omitempty"`
	Type             string    `json:"type"` // "user_message" | "assistant_message" | "tool_call"
	Role             string    `json:"role,omitempty"`
	Text             string    `json:"text,omitempty"`
	// Preserve carrier identity so the view can join a lifecycle prompt/result
	// with its provider message without removing intentional repeated messages.
	SourceType string `json:"source_type,omitempty"`
	// ToolCall reuses mcpagent/events' own ToolCallRecord contract — the same
	// one every other tool-call consumer in this codebase already reads —
	// rather than inventing a second shape for tool calls.
	ToolCall *events.ToolCallRecord `json:"tool_call,omitempty"`
}

const (
	backgroundTranscriptEventUserMessage      = "user_message"
	backgroundTranscriptEventAssistantMessage = "assistant_message"
	backgroundTranscriptEventToolCall         = "tool_call"
)

// BackgroundAgentTranscriptPath resolves the durable transcript path for one
// background agent. It sits below the same builder/conversation storage root
// workflowBuilderConversationLogPath already uses for chat history, in a
// "background" subtree keyed by session then agent, rather than inventing a
// new top-level storage convention (correcting this ticket's own original
// proposal of a "sessions/" root, which has no other usage anywhere in this
// codebase).
func BackgroundAgentTranscriptPath(workspacePath, sessionID, agentID string) string {
	cleanWorkspacePath := strings.Trim(strings.TrimSpace(workspacePath), "/")
	cleanSessionID := strings.TrimSpace(sessionID)
	cleanAgentID := strings.TrimSpace(agentID)
	return path.Join(cleanWorkspacePath, "builder", "conversation", "background", cleanSessionID, cleanAgentID+".json")
}

// NewBackgroundAgentTranscript creates the initial "running" transcript
// record. Callers write this before the child's first provider turn so a
// child that fails during setup still leaves a terminal diagnostic record
// once MarkTerminal is later applied to it (requirement 1).
func NewBackgroundAgentTranscript(sessionID, agentID, parentExecutionID, name, kind string, startedAt time.Time) *BackgroundAgentTranscript {
	return &BackgroundAgentTranscript{
		SessionID:         strings.TrimSpace(sessionID),
		AgentID:           strings.TrimSpace(agentID),
		ParentExecutionID: strings.TrimSpace(parentExecutionID),
		Name:              strings.TrimSpace(name),
		Kind:              strings.TrimSpace(kind),
		StartedAt:         startedAt,
		Status:            "running",
		Events:            []BackgroundAgentTranscriptEvent{},
	}
}

// ParseBackgroundAgentTranscript parses an existing transcript file's
// content. Empty content is not an error — it means no transcript exists yet.
func ParseBackgroundAgentTranscript(content string) (*BackgroundAgentTranscript, error) {
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}
	var transcript BackgroundAgentTranscript
	if err := json.Unmarshal([]byte(content), &transcript); err != nil {
		return nil, fmt.Errorf("parse background agent transcript: %w", err)
	}
	return &transcript, nil
}

// Marshal serializes the transcript for storage.
func (t *BackgroundAgentTranscript) Marshal() (string, error) {
	if t == nil {
		return "", fmt.Errorf("nil background agent transcript")
	}
	// Never mutate the provider's/live caller's event while saving diagnostics.
	copy := *t
	copy.NativeChildren = append([]NativeChildExecution(nil), t.NativeChildren...)
	copy.Events = make([]BackgroundAgentTranscriptEvent, len(t.Events))
	copyEvents := t.Events
	for index := range copyEvents {
		copy.Events[index] = copyEvents[index]
	}
	copy.RedactSensitiveText()
	data, err := json.MarshalIndent(&copy, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal background agent transcript: %w", err)
	}
	return string(data), nil
}

// RedactSensitiveText also protects reads of transcripts saved by older builds.
// IDs and paths remain untouched so evidence stays bound to its execution.
func (t *BackgroundAgentTranscript) RedactSensitiveText() {
	if t == nil {
		return
	}
	if t.Events == nil {
		t.Events = []BackgroundAgentTranscriptEvent{}
	}
	for index := range t.NativeChildren {
		t.NativeChildren[index].Name = redaction.Text(t.NativeChildren[index].Name)
	}
	t.Name = redaction.Text(t.Name)
	t.Error = redaction.Text(t.Error)
	if t.Inspection != nil {
		copy := *t.Inspection
		copy.Name = redaction.Text(copy.Name)
		t.Inspection = &copy
	}
	for index := range t.Events {
		event := &t.Events[index]
		event.Text = redaction.Text(event.Text)
		if event.ToolCall != nil {
			copy := *event.ToolCall
			copy.Arguments = redaction.Text(copy.Arguments)
			copy.Result = redaction.Text(copy.Result)
			copy.Error = redaction.Text(copy.Error)
			event.ToolCall = &copy
		}
	}
}

// AppendEvent appends one normalized event. Ordering is append-only; callers
// hold the file-level lock across read-modify-write, so this itself does no
// locking of its own.
func (t *BackgroundAgentTranscript) AppendEvent(evt BackgroundAgentTranscriptEvent) {
	if t == nil {
		return
	}
	t.Events = append(t.Events, evt)
}

// MarkTerminal atomically transitions the transcript to a terminal status.
// Calling it more than once is idempotent in effect (last write wins) but
// callers should still only call it exactly once per agent, at the point the
// agent's own lifecycle (background_agent_log) reaches a terminal state.
func (t *BackgroundAgentTranscript) MarkTerminal(status, errMsg string, completedAt time.Time) {
	if t == nil {
		return
	}
	t.Status = status
	t.Error = strings.TrimSpace(errMsg)
	t.CompletedAt = completedAt
}

// NormalizeBackgroundTranscriptEvent maps one mcpagent AgentEvent into the
// transcript's normalized contract. ok is false for event types the
// transcript intentionally does not record (token usage, streaming deltas,
// MCP connection chatter, etc.) — the transcript is a forensic record of
// what was said and done, not a full replay of every internal signal.
func NormalizeBackgroundTranscriptEvent(evt *events.AgentEvent) (normalized BackgroundAgentTranscriptEvent, ok bool) {
	if evt == nil || evt.Data == nil {
		return BackgroundAgentTranscriptEvent{}, false
	}
	defer func() {
		if ok {
			normalized.SourceType = string(evt.Type)
			normalized.TurnID = evt.TurnID
		}
	}()
	switch data := evt.Data.(type) {
	case *events.SystemPromptEvent:
		if strings.TrimSpace(data.Content) == "" {
			return BackgroundAgentTranscriptEvent{}, false
		}
		return BackgroundAgentTranscriptEvent{Timestamp: eventTimestamp(evt, data.Timestamp), Type: "system_message", Role: "system", Text: data.Content}, true
	case *events.ConversationStartEvent:
		if strings.TrimSpace(data.SystemPrompt) == "" {
			return BackgroundAgentTranscriptEvent{}, false
		}
		return BackgroundAgentTranscriptEvent{Timestamp: eventTimestamp(evt, data.Timestamp), Type: "system_message", Role: "system", Text: data.SystemPrompt}, true
	case *events.LLMGenerationEndEvent:
		if strings.TrimSpace(data.Content) == "" {
			return BackgroundAgentTranscriptEvent{}, false
		}
		return BackgroundAgentTranscriptEvent{Timestamp: eventTimestamp(evt, data.Timestamp), Type: backgroundTranscriptEventAssistantMessage, Role: "assistant", Text: data.Content}, true
	case *events.UserMessageEvent:
		text := strings.TrimSpace(data.Content)
		if text == "" {
			return BackgroundAgentTranscriptEvent{}, false
		}
		role := data.Role
		if role == "" {
			role = "user"
		}
		return BackgroundAgentTranscriptEvent{
			Timestamp: eventTimestamp(evt, data.Timestamp),
			Type:      backgroundTranscriptEventUserMessage,
			Role:      role,
			Text:      text,
		}, true
	case *OrchestratorAgentStartEvent:
		text := strings.TrimSpace(data.UserMessage)
		if text == "" {
			return BackgroundAgentTranscriptEvent{}, false
		}
		return BackgroundAgentTranscriptEvent{
			Timestamp: eventTimestamp(evt, data.Timestamp),
			Type:      backgroundTranscriptEventUserMessage,
			Role:      "user",
			Text:      text,
		}, true
	case *OrchestratorAgentEndEvent:
		text := strings.TrimSpace(data.Result)
		if text == "" && strings.TrimSpace(data.Error) == "" {
			return BackgroundAgentTranscriptEvent{}, false
		}
		if text == "" {
			text = strings.TrimSpace(data.Error)
		}
		return BackgroundAgentTranscriptEvent{
			Timestamp: eventTimestamp(evt, data.Timestamp),
			Type:      backgroundTranscriptEventAssistantMessage,
			Role:      "assistant",
			Text:      text,
		}, true
	default:
		if record, ok := events.ToolCallRecordFromEvent(evt.Data); ok {
			return BackgroundAgentTranscriptEvent{
				Timestamp: evt.Timestamp,
				Type:      backgroundTranscriptEventToolCall,
				ToolCall:  &record,
			}, true
		}
	}
	return BackgroundAgentTranscriptEvent{}, false
}

func eventTimestamp(evt *events.AgentEvent, dataTimestamp time.Time) time.Time {
	if !dataTimestamp.IsZero() {
		return dataTimestamp
	}
	return evt.Timestamp
}

// NativeChildExecution links a provider child to the same durable execution
// transcript contract used by managed delegates.
type NativeChildExecution struct {
	AgentID           string    `json:"agent_id"`
	ParentExecutionID string    `json:"parent_execution_id"`
	Name              string    `json:"name"`
	Status            string    `json:"status"`
	StartedAt         time.Time `json:"started_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}
