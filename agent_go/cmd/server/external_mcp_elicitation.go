package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// A function runs independently of the MCP request that started it. When a
// caller polls while one of its questions is pending, modern MCP clients can
// answer it as a multi-round-trip form. requestState is only a routing hint:
// both the answer and the subsequent poll go through the authenticated REST
// dispatcher again, which rechecks the current token, target and call access.
type externalMCPElicitationState struct {
	Target     string `json:"target"`
	CallID     string `json:"call_id"`
	RequestID  string `json:"request_id"`
	WorkflowID string `json:"workflow_id,omitempty"`
}

type externalMCPFunctionStatus struct {
	CallID        string `json:"call_id"`
	CanReply      *bool  `json:"can_reply,omitempty"`
	PendingInputs []struct {
		RequestID     string   `json:"request_id"`
		Message       string   `json:"message"`
		Options       []string `json:"options"`
		AllowFeedback bool     `json:"allow_feedback"`
	} `json:"pending_inputs"`
}

func externalMCPElicitationTools(target string) (get, reply string) {
	switch target {
	case "ask_crew", "call_crew_function", "get_crew_function_call":
		return "get_crew_function_call", "reply_crew_function_call"
	case "call_workflow_function", "get_workflow_function_call":
		return "get_workflow_function_call", "reply_workflow_function_call"
	default:
		return "", ""
	}
}

func externalMCPClientCanElicit(ctx context.Context) bool {
	info := server.RequestProtocolInfoFromContext(ctx)
	// This endpoint builds an independent stateless MCP server per HTTP
	// request. Legacy server-initiated elicitation requires a retained session
	// to receive the client's second POST, which this endpoint does not have.
	// Modern input_required is stateless and carries capabilities per request.
	return info != nil && info.Modern && info.ClientCapabilities != nil && info.ClientCapabilities.Elicitation != nil
}

func externalMCPMaybeElicit(ctx context.Context, target string, args map[string]any, allowed map[string]externalTool, rec *externalMCPRecorder) *mcp.CallToolResult {
	if !externalMCPClientCanElicit(ctx) || rec.status < 200 || rec.status >= 300 {
		return nil
	}
	get, reply := externalMCPElicitationTools(target)
	if get == "" {
		return nil
	}
	if _, ok := allowed[reply]; !ok {
		return nil
	}
	var status externalMCPFunctionStatus
	if err := json.Unmarshal(rec.body.Bytes(), &status); err != nil || status.CallID == "" || len(status.PendingInputs) == 0 {
		return nil
	}
	if status.CanReply != nil && !*status.CanReply {
		return nil
	}
	input := status.PendingInputs[0]
	if input.RequestID == "" || strings.TrimSpace(input.Message) == "" || externalMCPSecretQuestion(input.Message) {
		return nil
	}
	state := externalMCPElicitationState{Target: target, CallID: status.CallID, RequestID: input.RequestID}
	if get == "get_workflow_function_call" {
		state.WorkflowID, _ = args["workflow_id"].(string)
		if state.WorkflowID == "" {
			return nil
		}
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return nil
	}
	answer := map[string]any{"type": "string", "minLength": 1, "maxLength": 4000}
	message := input.Message
	if len(input.Options) > 0 {
		if input.AllowFeedback {
			message += " Suggested choices: " + strings.Join(input.Options, ", ") + ". You may also enter another answer."
		} else {
			answer["enum"] = input.Options
		}
	}
	return server.NewInputRequestBuilder(string(encoded)).Elicit(input.RequestID, mcp.ElicitationParams{
		Mode:    mcp.ElicitationModeForm,
		Message: message,
		RequestedSchema: map[string]any{
			"type": "object", "properties": map[string]any{"response": answer},
			"required": []string{"response"}, "additionalProperties": false,
		},
	}).ToolResult()
}

// Form elicitation is for ordinary questions, never credentials. Questions
// with obvious secret language stay on the explicit poll/reply path.
func externalMCPSecretQuestion(message string) bool {
	lower := strings.ToLower(message)
	for _, term := range []string{"password", "passcode", "one-time", "otp", "secret", "api key", "access token", "verification code", "2fa", "credential", "private key"} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

func (api *StreamingAPI) externalMCPResumeElicitation(ctx context.Context, r *http.Request, target string, args map[string]any, allowed map[string]externalTool, request mcp.CallToolRequest) *mcp.CallToolResult {
	if !externalMCPClientCanElicit(ctx) {
		return mcp.NewToolResultError("elicitation_not_supported: use call-ID polling and reply tools")
	}
	var state externalMCPElicitationState
	if err := json.Unmarshal([]byte(request.Params.RequestState), &state); err != nil || state.Target != target || state.CallID == "" || state.RequestID == "" {
		return mcp.NewToolResultError("invalid_request_state: the elicitation does not match this tool call")
	}
	get, reply := externalMCPElicitationTools(target)
	if get == "" {
		return mcp.NewToolResultError("invalid_request_state: this tool does not accept elicitation replies")
	}
	if _, ok := allowed[get]; !ok {
		return mcp.NewToolResultError("insufficient_scope: function-call polling is unavailable")
	}
	if strings.HasPrefix(target, "get_") {
		if callID, _ := args["call_id"].(string); callID != state.CallID {
			return mcp.NewToolResultError("invalid_request_state: call_id changed during elicitation")
		}
	}
	if get == "get_workflow_function_call" {
		workflowID, _ := args["workflow_id"].(string)
		if workflowID == "" || workflowID != state.WorkflowID {
			return mcp.NewToolResultError("invalid_request_state: workflow_id changed during elicitation")
		}
	}
	response := server.ElicitationResponse(request.Params.InputResponses, state.RequestID)
	if response == nil {
		return mcp.NewToolResultError("invalid_input_response: expected the elicitation answer for this request_id")
	}
	if response.Action == mcp.ElicitationResponseActionAccept {
		if _, ok := allowed[reply]; !ok {
			return mcp.NewToolResultError("insufficient_scope: answering this function call is unavailable")
		}
		content, ok := response.Content.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid_input_response: expected a response field")
		}
		answer, ok := content["response"].(string)
		if !ok || strings.TrimSpace(answer) == "" {
			return mcp.NewToolResultError("invalid_input_response: response must be nonempty text")
		}
		replyArgs := map[string]any{"call_id": state.CallID, "request_id": state.RequestID, "response": answer}
		if state.WorkflowID != "" {
			replyArgs["workflow_id"] = state.WorkflowID
		}
		rec, err := api.externalMCPInvoke(ctx, r, reply, replyArgs)
		if err != nil {
			return mcp.NewToolResultError("failed to encode elicitation answer: " + err.Error())
		}
		if rec.status < 200 || rec.status >= 300 {
			return externalMCPDispatchResult(rec)
		}
	} else if response.Action != mcp.ElicitationResponseActionDecline && response.Action != mcp.ElicitationResponseActionCancel {
		return mcp.NewToolResultError(fmt.Sprintf("invalid_input_response: unknown elicitation action %q", response.Action))
	}
	getArgs := map[string]any{"call_id": state.CallID}
	if state.WorkflowID != "" {
		getArgs["workflow_id"] = state.WorkflowID
	}
	rec, err := api.externalMCPInvoke(ctx, r, get, getArgs)
	if err != nil {
		return mcp.NewToolResultError("failed to poll function call: " + err.Error())
	}
	if response.Action == mcp.ElicitationResponseActionAccept {
		if next := externalMCPMaybeElicit(ctx, target, args, allowed, rec); next != nil {
			return next
		}
	}
	return externalMCPDispatchResult(rec)
}
