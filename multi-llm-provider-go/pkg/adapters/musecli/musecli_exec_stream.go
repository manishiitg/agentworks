package musecli

import (
	"encoding/json"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Stream mapping for immediate assessment (thinking/tool visibility),
// decoded from live wire payloads observed 2026-09-10 against the real
// CLI: `tool.result` carries kind=tool_result with call_id, text (a JSON
// document with command/description/exit_code/output), and
// correlation_facts{tool_name, outcome}. `task.lifecycle.status` carries
// event.message progress text ("opening meta model stream attempt 1/10").

// museToolResultPayload decodes a tool.result wire payload.
type museToolResultPayload struct {
	CallID string `json:"call_id"`
	Text   string `json:"text"`
	Facts  struct {
		ToolName string `json:"tool_name"`
		Outcome  string `json:"outcome"`
	} `json:"correlation_facts"`
}

// museToolChunkText decodes the inner tool document: command run,
// description, exit code, and captured output.
type museToolChunkText struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	ExitCode    int    `json:"exit_code"`
	Output      string `json:"output"`
}

// museToolStreamChunks maps one tool.result event to the start/end chunk
// pair every other provider emits natively. The exec wire has NO
// tool-started event — the tool runs opaquely inside the CLI run and only
// tool.result surfaces — so the start is synthesized from the end's own
// identity (same call id, name, args; no result, no duration). Without the
// pair, product rows render an orphan end: no tool name in the batch
// label, no arguments retained, and the detail card opens onto the
// "did not retain" fallback. Emitted start-then-end in one step; a consumer
// that only wants completions keeps using museToolEndChunk.
func museToolStreamChunks(line json.RawMessage) []*llmtypes.StreamChunk {
	end := museToolEndChunk(line)
	if end == nil {
		return nil
	}
	start := &llmtypes.StreamChunk{
		Type:       llmtypes.StreamChunkTypeToolCallStart,
		ToolName:   end.ToolName,
		ToolCallID: end.ToolCallID,
		ToolArgs:   end.ToolArgs,
		Metadata:   map[string]interface{}{"synthetic": true, "outcome": end.Metadata["outcome"]},
	}
	return []*llmtypes.StreamChunk{start, end}
}

// museToolEndChunk maps one tool.result event to a tool_call_end chunk.
// Nil when the event carries nothing mappable (never fail a turn on
// telemetry shape drift).
func museToolEndChunk(line json.RawMessage) *llmtypes.StreamChunk {
	var envelope struct {
		Payload museToolResultPayload `json:"payload"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return nil
	}
	payload := envelope.Payload
	if strings.TrimSpace(payload.Text) == "" && strings.TrimSpace(payload.Facts.ToolName) == "" {
		return nil
	}
	var doc museToolChunkText
	args, result := "", strings.TrimSpace(payload.Text)
	if err := json.Unmarshal([]byte(payload.Text), &doc); err == nil {
		if strings.TrimSpace(doc.Command) != "" {
			args = doc.Command
		}
		if strings.TrimSpace(doc.Output) != "" {
			result = strings.TrimSpace(doc.Output)
		}
		if result == "" && strings.TrimSpace(doc.Description) != "" {
			result = strings.TrimSpace(doc.Description)
		}
	}
	return &llmtypes.StreamChunk{
		Type:       llmtypes.StreamChunkTypeToolCallEnd,
		ToolName:   payload.Facts.ToolName,
		ToolCallID: payload.CallID,
		ToolArgs:   args,
		ToolResult: result,
		Metadata:   map[string]interface{}{"outcome": payload.Facts.Outcome},
	}
}

// museTaskStatusPayload decodes a task.lifecycle.status wire payload.
type museTaskStatusPayload struct {
	Event struct {
		Message string `json:"message"`
	} `json:"event"`
}

// museStreamPlumbing reports whether a lifecycle status message describes
// transport retry plumbing rather than model progress — "opening meta
// model stream attempt 1/10" / "completed meta model stream attempt 1/10"
// are the only shapes observed live. Rendered as Thinking, they read as
// operator spam with no thinking attached, so they never become chunks.
// Matched by shape (open/complete + stream + attempt counter), not by
// exact string, so future retry counts stay filtered while genuine
// progress messages ("running tools…") still pass.
func museStreamPlumbing(message string) bool {
	lower := strings.ToLower(message)
	if !strings.Contains(lower, "attempt") || !strings.Contains(lower, "stream") {
		return false
	}
	return strings.Contains(lower, "opening") || strings.Contains(lower, "completed")
}

// museStatusChunk retains progress as a non-final update and asks product UIs
// to present it as assistant commentary. Transport plumbing
// (stream open/complete attempt counters) is dropped, never surfaced.
// Nil on shape drift.
func museStatusChunk(line json.RawMessage) *llmtypes.StreamChunk {
	var envelope struct {
		Payload museTaskStatusPayload `json:"payload"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return nil
	}
	message := strings.TrimSpace(envelope.Payload.Event.Message)
	if message == "" || museStreamPlumbing(message) {
		return nil
	}
	return &llmtypes.StreamChunk{
		Type:     llmtypes.StreamChunkTypeReasoning,
		Content:  message,
		Metadata: map[string]interface{}{"presentation": "assistant_update"},
	}
}
