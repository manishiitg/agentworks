package agentworksclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Crew Ask is a reusable conversation, not a task or a function call. Its
// clarification belongs to one query inside one tab. Both MCP transports use
// this adapter; every read and answer goes back through workspace permissions.
type workspaceInputState struct {
	Action, Workspace, OriginalWorkspace, Chat, Query, Request string
	After                                                      int
}

type workspaceQuestion struct {
	ID, Header, Question string
	Options              []struct{ Label, Description string }
	MultiSelect          bool `json:"multi_select"`
	AllowOther           bool `json:"allow_other"`
	MinSelections        int  `json:"min_selections"`
	MaxSelections        int  `json:"max_selections"`
}

type workspaceInputStatus struct {
	Workspace string `json:"workspace_id"`
	Chat      string `json:"chat_id"`
	Query     string `json:"query_id"`
	After     int    `json:"next_after"`
	CanReply  bool   `json:"can_reply"`
	Pending   []struct {
		Request   string              `json:"request_id"`
		Questions []workspaceQuestion `json:"questions"`
	} `json:"pending_inputs"`
}

func workspaceQuestionSensitive(q workspaceQuestion) bool {
	text := q.Question + " " + q.Header
	for _, option := range q.Options {
		text += " " + option.Label + " " + option.Description
	}
	return SecretQuestion(text)
}

func (h FunctionElicitationHost) workspaceMaybeElicit(ctx context.Context, args map[string]any, raw json.RawMessage) *mcp.CallToolResult {
	action, _ := args["action"].(string)
	if action != "ask" && action != "read_chat" || !h.ClientCanElicit(ctx) || !h.Available("workspace") {
		return nil
	}
	var status workspaceInputStatus
	if json.Unmarshal(raw, &status) != nil || !status.CanReply || status.Workspace == "" || status.Chat == "" || status.Query == "" || len(status.Pending) != 1 {
		return nil
	}
	input := status.Pending[0]
	if input.Request == "" || len(input.Questions) == 0 {
		return nil
	}
	properties := map[string]any{}
	required := []string{}
	for i, q := range input.Questions {
		if q.ID == "" || len(q.Options) == 0 || workspaceQuestionSensitive(q) {
			return nil
		}
		key := fmt.Sprintf("q%d", i)
		labels := []string{}
		descriptions := []string{}
		for _, option := range q.Options {
			labels = append(labels, option.Label)
			if option.Description != "" {
				descriptions = append(descriptions, option.Label+": "+option.Description)
			}
		}
		field := map[string]any{"type": "string", "title": q.Question, "enum": labels}
		if q.MultiSelect {
			min, max := q.MinSelections, q.MaxSelections
			if min == 0 {
				min = 1
			}
			if max == 0 {
				max = len(labels)
				if q.AllowOther {
					max++
				}
			}
			if q.AllowOther {
				min = 0
			}
			field = map[string]any{"type": "array", "title": q.Question, "items": map[string]any{"type": "string", "enum": labels}, "minItems": min, "maxItems": max}
		}
		if len(descriptions) > 0 {
			field["description"] = strings.Join(descriptions, "; ")
		}
		properties[key] = field
		if q.AllowOther {
			properties[key+"_other"] = map[string]any{"type": "string", "title": q.Question + " — another answer", "maxLength": 2000}
		} else {
			required = append(required, key)
		}
	}
	original, _ := args["workspace_id"].(string)
	state, _ := json.Marshal(workspaceInputState{Action: action, Workspace: status.Workspace, OriginalWorkspace: original, Chat: status.Chat, Query: status.Query, Request: input.Request, After: status.After})
	return server.NewInputRequestBuilder(string(state)).Elicit(input.Request, mcp.ElicitationParams{
		Mode: mcp.ElicitationModeForm, Message: "Please answer these questions to continue your Crew chat. Choose an option or enter another answer where offered.",
		RequestedSchema: map[string]any{"type": "object", "properties": properties, "required": required},
	}).ToolResult()
}

func (s workspaceInputState) pollArgs() map[string]any {
	return map[string]any{"action": "read_chat", "workspace_id": s.Workspace, "chat_id": s.Chat, "query_id": s.Query, "after": s.After, "wait_seconds": 0}
}

func (h FunctionElicitationHost) workspaceResume(ctx context.Context, args map[string]any, request mcp.CallToolRequest) *mcp.CallToolResult {
	var state workspaceInputState
	if json.Unmarshal([]byte(request.Params.RequestState), &state) != nil || state.Workspace == "" || state.Chat == "" || state.Query == "" || state.Request == "" || (state.Action != "ask" && state.Action != "read_chat") {
		return mcp.NewToolResultError("invalid_request_state: expected the pending Crew Ask question")
	}
	workspace, _ := args["workspace_id"].(string)
	action, _ := args["action"].(string)
	chat, _ := args["chat_id"].(string)
	query, _ := args["query_id"].(string)
	if action != state.Action || workspace != state.OriginalWorkspace || chat != "" && chat != state.Chat || query != "" && query != state.Query || !h.Available("workspace") {
		return mcp.NewToolResultError("invalid_request_state: the Crew, chat or query changed")
	}
	response := server.ElicitationResponse(request.Params.InputResponses, state.Request)
	if response == nil {
		return mcp.NewToolResultError("invalid_input_response: answer the pending request_id")
	}
	// Re-read with current credentials before interpreting the form. requestState
	// supplies no question schema, identity or authority of its own.
	raw, failure := h.Call(ctx, "workspace", state.pollArgs())
	if failure != nil {
		return failure
	}
	if response.Action == mcp.ElicitationResponseActionDecline || response.Action == mcp.ElicitationResponseActionCancel {
		return h.Render(raw)
	}
	if response.Action != mcp.ElicitationResponseActionAccept {
		return mcp.NewToolResultError("invalid_input_response: unknown elicitation action")
	}
	var status workspaceInputStatus
	if json.Unmarshal(raw, &status) != nil || !status.CanReply || status.Query != state.Query || status.Chat != state.Chat || status.Workspace != state.Workspace || len(status.Pending) != 1 || status.Pending[0].Request != state.Request {
		return mcp.NewToolResultError("input_not_pending: this question is no longer pending for your Ask")
	}
	content, ok := response.Content.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid_input_response: expected question answers")
	}
	answers := []map[string]any{}
	for i, q := range status.Pending[0].Questions {
		if workspaceQuestionSensitive(q) {
			return mcp.NewToolResultError("Use account sign-in or add the secret in Vault from chat instead of a question form.")
		}
		key := fmt.Sprintf("q%d", i)
		labels := []string{}
		if value, exists := content[key]; exists {
			if q.MultiSelect {
				encoded, err := json.Marshal(value)
				if err != nil || json.Unmarshal(encoded, &labels) != nil {
					return mcp.NewToolResultError("invalid_input_response: choose options for each question")
				}
			} else {
				label, ok := value.(string)
				if !ok {
					return mcp.NewToolResultError("invalid_input_response: choose an option")
				}
				labels = append(labels, label)
			}
		}
		other := ""
		if value, exists := content[key+"_other"]; exists {
			text, ok := value.(string)
			if !ok {
				return mcp.NewToolResultError("invalid_input_response: another answer must be text")
			}
			other = strings.TrimSpace(text)
		}
		answer := map[string]any{"id": q.ID, "selected_labels": labels}
		if other != "" {
			answer["other_text"] = other
		}
		answers = append(answers, answer)
	}
	reply := state.pollArgs()
	reply["action"], reply["request_id"], reply["answers"] = "reply_input", state.Request, answers
	if _, failure = h.Call(ctx, "workspace", reply); failure != nil {
		return failure
	}
	raw, failure = h.Call(ctx, "workspace", state.pollArgs())
	if failure != nil {
		return failure
	}
	if next := h.workspaceMaybeElicit(ctx, args, raw); next != nil {
		return next
	}
	return h.Render(raw)
}
