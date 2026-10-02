package agentworksclient

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type bridgeElicitationState struct {
	Target     string `json:"target"`
	CallID     string `json:"call_id"`
	RequestID  string `json:"request_id"`
	WorkflowID string `json:"workflow_id,omitempty"`
}

type bridgeFunctionStatus struct {
	CallID        string `json:"call_id"`
	CanReply      *bool  `json:"can_reply,omitempty"`
	PendingInputs []struct {
		RequestID     string   `json:"request_id"`
		Message       string   `json:"message"`
		Options       []string `json:"options"`
		AllowFeedback bool     `json:"allow_feedback"`
	} `json:"pending_inputs"`
}

func bridgeFunctionTools(target string) (get, reply string) {
	switch target {
	case "ask_crew", "call_crew_function", "get_crew_function_call":
		return "get_crew_function_call", "reply_crew_function_call"
	case "call_workflow_function", "get_workflow_function_call":
		return "get_workflow_function_call", "reply_workflow_function_call"
	default:
		return "", ""
	}
}

func bridgeClientCanElicit(ctx context.Context) bool {
	if info := server.RequestProtocolInfoFromContext(ctx); info != nil && info.Modern {
		return info.ClientCapabilities != nil && info.ClientCapabilities.Elicitation != nil
	}
	if session := server.ClientSessionFromContext(ctx); session != nil {
		if capable, ok := session.(interface{ GetClientCapabilities() mcp.ClientCapabilities }); ok {
			return capable.GetClientCapabilities().Elicitation != nil
		}
	}
	return false
}

func bridgeMaybeElicit(ctx context.Context, target string, args map[string]any, available map[string]bool, raw json.RawMessage) *mcp.CallToolResult {
	get, reply := bridgeFunctionTools(target)
	if !bridgeClientCanElicit(ctx) || get == "" || !available[get] || !available[reply] {
		return nil
	}
	var status bridgeFunctionStatus
	if err := json.Unmarshal(raw, &status); err != nil || status.CallID == "" || len(status.PendingInputs) == 0 {
		return nil
	}
	if status.CanReply != nil && !*status.CanReply {
		return nil
	}
	input := status.PendingInputs[0]
	if input.RequestID == "" || strings.TrimSpace(input.Message) == "" || bridgeSecretQuestion(input.Message) {
		return nil
	}
	state := bridgeElicitationState{Target: target, CallID: status.CallID, RequestID: input.RequestID}
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
		Mode: mcp.ElicitationModeForm, Message: message,
		RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{"response": answer},
			"required": []string{"response"}, "additionalProperties": false},
	}).ToolResult()
}

func bridgeSecretQuestion(message string) bool {
	lower := strings.ToLower(message)
	for _, term := range []string{"password", "passcode", "one-time", "otp", "secret", "api key", "access token", "verification code", "2fa", "credential", "private key"} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

func callMCPBridgeTool(ctx context.Context, client ToolCaller, target string, args map[string]any, available map[string]bool, request mcp.CallToolRequest) *mcp.CallToolResult {
	if request.Params.RequestState != "" {
		return resumeMCPBridgeElicitation(ctx, client, target, args, available, request)
	}
	if len(request.Params.InputResponses) > 0 {
		return mcp.NewToolResultError("invalid_request_state: input responses need the matching requestState")
	}
	raw, err := client.Call(ctx, target, args)
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	if result := bridgeMaybeElicit(ctx, target, args, available, raw); result != nil {
		return result
	}
	return mcp.NewToolResultStructured(json.RawMessage(raw), string(raw))
}

func resumeMCPBridgeElicitation(ctx context.Context, client ToolCaller, target string, args map[string]any, available map[string]bool, request mcp.CallToolRequest) *mcp.CallToolResult {
	if !bridgeClientCanElicit(ctx) {
		return mcp.NewToolResultError("elicitation_not_supported: use call-ID polling and reply tools")
	}
	var state bridgeElicitationState
	if err := json.Unmarshal([]byte(request.Params.RequestState), &state); err != nil || state.Target != target || state.CallID == "" || state.RequestID == "" {
		return mcp.NewToolResultError("invalid_request_state: the elicitation does not match this tool call")
	}
	get, reply := bridgeFunctionTools(target)
	if get == "" || !available[get] {
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
		return mcp.NewToolResultError("invalid_input_response: expected the answer for this request_id")
	}
	if response.Action == mcp.ElicitationResponseActionAccept {
		if !available[reply] {
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
		if _, err := client.Call(ctx, reply, replyArgs); err != nil {
			return mcp.NewToolResultError(err.Error())
		}
	} else if response.Action != mcp.ElicitationResponseActionDecline && response.Action != mcp.ElicitationResponseActionCancel {
		return mcp.NewToolResultError("invalid_input_response: unknown elicitation action")
	}
	getArgs := map[string]any{"call_id": state.CallID}
	if state.WorkflowID != "" {
		getArgs["workflow_id"] = state.WorkflowID
	}
	raw, err := client.Call(ctx, get, getArgs)
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	if response.Action == mcp.ElicitationResponseActionAccept {
		if next := bridgeMaybeElicit(ctx, target, args, available, raw); next != nil {
			return next
		}
	}
	return mcp.NewToolResultStructured(json.RawMessage(raw), string(raw))
}
