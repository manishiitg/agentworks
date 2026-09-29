package agentworksclient

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Function-call elicitation is shared by the hosted MCP endpoint and the
// local stdio bridge, so the security checks exist once. A function runs
// independently of the MCP request that started it. When a caller polls
// while one of its questions is pending, a client that declares the
// elicitation capability receives the question as an MCP form. requestState
// is only a routing hint: the answer and the follow-up poll both go through
// Host.Call, which re-authenticates and rechecks call ownership and access.

// FunctionElicitationHost is how one MCP surface reaches AgentWorks tools.
type FunctionElicitationHost struct {
	// Available reports whether this connection's catalog includes a tool.
	Available func(tool string) bool
	// Call runs a tool as the current caller. A non-nil result is the error
	// to return to the client unchanged.
	Call func(ctx context.Context, tool string, args map[string]any) (json.RawMessage, *mcp.CallToolResult)
	// Render turns a successful tool result into an MCP result.
	Render func(raw json.RawMessage) *mcp.CallToolResult
	// AllowLegacySession accepts the capability from an initialized
	// stateful session (stdio). A stateless endpoint cannot deliver the
	// legacy server-initiated elicitation/create exchange.
	AllowLegacySession bool
}

type functionElicitationState struct {
	Target     string `json:"target"`
	CallID     string `json:"call_id"`
	RequestID  string `json:"request_id"`
	WorkflowID string `json:"workflow_id,omitempty"`
}

type functionCallStatus struct {
	CallID        string `json:"call_id"`
	CanReply      *bool  `json:"can_reply,omitempty"`
	PendingInputs []struct {
		RequestID     string   `json:"request_id"`
		Message       string   `json:"message"`
		Options       []string `json:"options"`
		AllowFeedback bool     `json:"allow_feedback"`
	} `json:"pending_inputs"`
}

func functionElicitationTools(target string) (get, reply string) {
	switch target {
	case "ask_crew", "call_crew_function", "get_crew_function_call":
		return "get_crew_function_call", "reply_crew_function_call"
	case "call_workflow_function", "get_workflow_function_call":
		return "get_workflow_function_call", "reply_workflow_function_call"
	default:
		return "", ""
	}
}

// ClientCanElicit reports whether this request's client accepts form
// elicitation. Modern (2026-07-28+) requests carry capabilities per request.
func (h FunctionElicitationHost) ClientCanElicit(ctx context.Context) bool {
	if info := server.RequestProtocolInfoFromContext(ctx); info != nil && info.Modern {
		return info.ClientCapabilities != nil && info.ClientCapabilities.Elicitation != nil
	}
	if !h.AllowLegacySession {
		return false
	}
	if session := server.ClientSessionFromContext(ctx); session != nil {
		if capable, ok := session.(interface{ GetClientCapabilities() mcp.ClientCapabilities }); ok {
			return capable.GetClientCapabilities().Elicitation != nil
		}
	}
	return false
}

// MaybeElicit returns an input_required result for the first pending,
// answerable, non-sensitive question in a successful function-call result,
// or nil to return the ordinary result.
func (h FunctionElicitationHost) MaybeElicit(ctx context.Context, target string, args map[string]any, raw json.RawMessage) *mcp.CallToolResult {
	get, reply := functionElicitationTools(target)
	if get == "" || !h.ClientCanElicit(ctx) || !h.Available(get) || !h.Available(reply) {
		return nil
	}
	var status functionCallStatus
	if err := json.Unmarshal(raw, &status); err != nil || status.CallID == "" || len(status.PendingInputs) == 0 {
		return nil
	}
	// A workflow reader may see questions but cannot answer them.
	if status.CanReply != nil && !*status.CanReply {
		return nil
	}
	input := status.PendingInputs[0]
	if input.RequestID == "" || strings.TrimSpace(input.Message) == "" || SecretQuestion(input.Message) {
		return nil
	}
	state := functionElicitationState{Target: target, CallID: status.CallID, RequestID: input.RequestID}
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

// Resume handles a retried call that carries requestState and
// inputResponses. It never re-runs the original tool (a retried
// call_crew_function must not start a second call): an accepted answer goes
// to the call-scoped reply tool, then the call is polled.
func (h FunctionElicitationHost) Resume(ctx context.Context, target string, args map[string]any, request mcp.CallToolRequest) *mcp.CallToolResult {
	if !h.ClientCanElicit(ctx) {
		return mcp.NewToolResultError("elicitation_not_supported: use call-ID polling and reply tools")
	}
	var state functionElicitationState
	if err := json.Unmarshal([]byte(request.Params.RequestState), &state); err != nil || state.Target != target || state.CallID == "" || state.RequestID == "" {
		return mcp.NewToolResultError("invalid_request_state: the elicitation does not match this tool call")
	}
	get, reply := functionElicitationTools(target)
	if get == "" || !h.Available(get) {
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
	switch response.Action {
	case mcp.ElicitationResponseActionAccept:
		if !h.Available(reply) {
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
		if _, failure := h.Call(ctx, reply, replyArgs); failure != nil {
			return failure
		}
	case mcp.ElicitationResponseActionDecline, mcp.ElicitationResponseActionCancel:
		// The question stays pending for another answer route.
	default:
		return mcp.NewToolResultError("invalid_input_response: unknown elicitation action")
	}
	getArgs := map[string]any{"call_id": state.CallID}
	if state.WorkflowID != "" {
		getArgs["workflow_id"] = state.WorkflowID
	}
	raw, failure := h.Call(ctx, get, getArgs)
	if failure != nil {
		return failure
	}
	// After an answer, the next pending question (if any) is asked at once.
	// After a decline, return status so the client is not asked again.
	if response.Action == mcp.ElicitationResponseActionAccept {
		if next := h.MaybeElicit(ctx, target, args, raw); next != nil {
			return next
		}
	}
	return h.Render(raw)
}

// SecretQuestion reports wording that suggests a credential or one-time
// code. Form elicitation is for ordinary input only (MCP forbids requesting
// sensitive data through form mode), so these stay on poll/reply. False
// positives are safe: they only fall back to polling.
func SecretQuestion(message string) bool {
	lower := strings.ToLower(message)
	for _, term := range []string{
		"password", "passcode", "passphrase", "one-time", "otp", "secret", "api key", "apikey", "api_key",
		"access token", "auth token", "bearer", "verification code", "security code", "login code", "auth code",
		"2fa", "mfa", "credential", "private key", "ssh key", "pin code", "cvv", "card number", "social security",
	} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

func callMCPBridgeTool(ctx context.Context, client ToolCaller, target string, args map[string]any, available map[string]bool, request mcp.CallToolRequest) *mcp.CallToolResult {
	host := FunctionElicitationHost{
		Available: func(tool string) bool { return available[tool] },
		Call: func(ctx context.Context, tool string, args map[string]any) (json.RawMessage, *mcp.CallToolResult) {
			raw, err := client.Call(ctx, tool, args)
			if err != nil {
				return nil, mcp.NewToolResultError(err.Error())
			}
			return raw, nil
		},
		Render:             func(raw json.RawMessage) *mcp.CallToolResult { return mcp.NewToolResultStructured(raw, string(raw)) },
		AllowLegacySession: true,
	}
	if request.Params.RequestState != "" {
		return host.Resume(ctx, target, args, request)
	}
	if len(request.Params.InputResponses) > 0 {
		return mcp.NewToolResultError("invalid_request_state: input responses need the matching requestState")
	}
	raw, failure := host.Call(ctx, target, args)
	if failure != nil {
		return failure
	}
	if result := host.MaybeElicit(ctx, target, args, raw); result != nil {
		return result
	}
	return host.Render(raw)
}
