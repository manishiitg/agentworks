package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Display is separate from the prompt/recovery projection: tools must never
// become a person's prompt or an agent reply. All rows use the same bounded,
// incremental journal window; unknown protocol rows are ignored, not guessed.
func parseNativeTerminalDisplayLine(line []byte, provider string) ([]builderConversationMessage, time.Time) {
	var rows []builderConversationMessage
	var prose builderConversationMessage
	var at time.Time
	var ok bool
	if provider == "codex-cli" {
		prose, at, ok = parseCodexTranscriptLine(line)
	} else {
		prose, at, ok = parseClaudeTranscriptLine(line)
	}
	if ok {
		rows = append(rows, prose)
	}
	add := func(id, name, phase, text string) {
		if id == "" {
			return
		}
		rows = append(rows, builderConversationMessage{Role: "tool", ToolID: fmt.Sprintf("%x", sha256.Sum256([]byte(id))), ToolName: boundedNativeLabel(name), ToolPhase: phase, Parts: []builderConversationPart{{Text: boundedNativeText(text)}}})
	}
	if provider == "codex-cli" {
		var entry codexRolloutEntry
		if json.Unmarshal(line, &entry) != nil || entry.Type != "response_item" {
			return rows, at
		}
		at, _ = time.Parse(time.RFC3339Nano, entry.Timestamp)
		var call struct {
			Type, Name       string
			CallID           string `json:"call_id"`
			Arguments, Input string
			Output           json.RawMessage
		}
		if json.Unmarshal(entry.Payload, &call) != nil {
			return rows, at
		}
		switch call.Type {
		case "function_call":
			add(call.CallID, call.Name, "call", call.Arguments)
		case "custom_tool_call":
			add(call.CallID, call.Name, "call", call.Input)
		case "function_call_output", "custom_tool_call_output":
			add(call.CallID, "", "result", nativeDisplayJSON(call.Output))
		}
		return rows, at
	}
	var entry claudeTranscriptEntry
	if json.Unmarshal(line, &entry) != nil || (entry.Type != "user" && entry.Type != "assistant") {
		return rows, at
	}
	if entry.Type == "user" && ((entry.Origin != nil && entry.Origin.Kind == "task-notification") || entry.TurnOrigin == "task_notification") {
		return rows, at
	}
	at, _ = time.Parse(time.RFC3339Nano, entry.Timestamp)
	if at.IsZero() {
		return rows, at
	}
	var msg claudeTranscriptMessage
	if json.Unmarshal(entry.Message, &msg) != nil {
		return rows, at
	}
	var blocks []struct {
		Type, ID, Name string
		ToolUseID      string `json:"tool_use_id"`
		Input, Content json.RawMessage
	}
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return rows, at
	}
	for _, block := range blocks {
		if entry.Type == "assistant" && block.Type == "tool_use" {
			add(block.ID, block.Name, "call", nativeDisplayJSON(block.Input))
		}
		if entry.Type == "user" && block.Type == "tool_result" {
			add(block.ToolUseID, "", "result", extractClaudeTranscriptText(block.Content))
		}
	}
	return rows, at
}
func nativeDisplayJSON(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	return string(raw)
}
func boundedNativeLabel(value string) string {
	value = strings.TrimSpace(value)
	if runes := []rune(value); len(runes) > 128 {
		return string(runes[:128])
	}
	return value
}
