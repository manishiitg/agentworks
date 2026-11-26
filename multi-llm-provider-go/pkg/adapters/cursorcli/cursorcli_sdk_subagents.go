package cursorcli

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// cursorSDKSubagent turns a finished SDK "task" call into a child conversation the app shows as its own
// chat. The SDK returns the child's whole run in the result (conversationSteps), so nothing is read from disk.
// It also returns the text the parent's task row shows: the child's last answer, not the raw steps.
func cursorSDKSubagent(parentID, callID, args string, raw json.RawMessage, failed bool) (llmtypes.NativeSubagentTranscript, string, bool) {
	var call struct {
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}
	var result struct {
		Status string `json:"status"`
		Value  struct {
			AgentID    string `json:"agentId"`
			DurationMS int64  `json:"durationMs"`
			Steps      []struct {
				Assistant *struct {
					Text string `json:"text"`
				} `json:"assistantMessage"`
				ToolCall map[string]json.RawMessage `json:"toolCall"`
			} `json:"conversationSteps"`
		} `json:"value"`
	}
	if json.Unmarshal([]byte(args), &call) != nil || json.Unmarshal(raw, &result) != nil || result.Status == "" {
		return llmtypes.NativeSubagentTranscript{}, "", false
	}
	id := result.Value.AgentID
	if id == "" {
		id = callID
	}
	name := strings.TrimSpace(call.Description)
	if name == "" {
		name = "Subagent"
	}
	now := time.Now()
	child := llmtypes.NativeSubagentTranscript{ID: id, ParentID: parentID, Name: name, Provider: "cursor-cli", Status: "completed", StartedAt: now.Add(-time.Duration(result.Value.DurationMS) * time.Millisecond), UpdatedAt: now}
	if failed || result.Status != "success" {
		child.Status = "failed"
	}
	if strings.TrimSpace(call.Prompt) != "" {
		child.Messages = append(child.Messages, llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, call.Prompt))
	}
	last := ""
	for _, step := range result.Value.Steps {
		if step.Assistant != nil && strings.TrimSpace(step.Assistant.Text) != "" {
			last = strings.TrimSpace(step.Assistant.Text)
			child.Messages = append(child.Messages, llmtypes.TextPart(llmtypes.ChatMessageTypeAI, last))
		}
		for kind, body := range step.ToolCall {
			if !strings.HasSuffix(kind, "ToolCall") {
				continue
			}
			var tc struct {
				Args   json.RawMessage `json:"args"`
				Result json.RawMessage `json:"result"`
			}
			if json.Unmarshal(body, &tc) != nil {
				continue
			}
			toolName, toolArgs := cursorSDKMCPToolCall(strings.TrimSuffix(kind, "ToolCall"), string(tc.Args))
			toolID := callID + ":" + strconv.Itoa(len(child.Messages))
			out := string(tc.Result)
			var success struct {
				Success json.RawMessage `json:"success"`
			}
			if json.Unmarshal(tc.Result, &success) == nil && len(success.Success) > 0 {
				if text, ok := cursorSDKMCPToolResult(json.RawMessage(`{"status":"success","value":` + string(success.Success) + `}`)); ok {
					out = text
				} else {
					var plain struct {
						Content string `json:"content"`
					}
					if json.Unmarshal(success.Success, &plain) == nil && plain.Content != "" {
						out = plain.Content
					}
				}
			}
			child.Messages = append(child.Messages,
				llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.ToolCall{ID: toolID, Type: "function", FunctionCall: &llmtypes.FunctionCall{Name: toolName, Arguments: toolArgs}}}},
				llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: toolID, Name: toolName, Content: out}}})
		}
	}
	if last == "" {
		last = "The subagent finished."
		if child.Status == "failed" {
			last = "The subagent did not finish."
		}
	}
	return child, last, true
}
