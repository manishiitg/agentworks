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

// elicitationFamily is one kind of pending question the client may be asked:
// the tool that reads it, the tool that answers it, and which argument names
// the call, operation or session the question belongs to.
type elicitationFamily struct {
	get, reply             string
	getAction, replyAction string
	poll                   bool
	idField                string // call_id, operation_id or session_id
	workflow               bool   // the tools are workflow-scoped
}

func (f elicitationFamily) available(h FunctionElicitationHost, reply bool) bool {
	name, action := f.get, f.getAction
	if reply {
		name, action = f.reply, f.replyAction
	}
	if action != "" {
		name += "." + action
	}
	return h.Available(name)
}

type functionElicitationState struct {
	Target     string `json:"target"`
	CallID     string `json:"call_id"`
	RequestID  string `json:"request_id"`
	WorkflowID string `json:"workflow_id,omitempty"`
	CrewID     string `json:"crew_id,omitempty"`
	Action     string `json:"action,omitempty"`
}

// functionCallStatus reads a status response from any family. Function calls
// name a question request_id/message; Builder and Run chats name it
// unique_id/message_for_user.
type functionCallStatus struct {
	CallID      string `json:"call_id"`
	OperationID string `json:"operation_id"`
	SessionID   string `json:"session_id"`
	CanReply    *bool  `json:"can_reply,omitempty"`
	// Pending is every pending question, whichever names it used.
	PendingInputs []pendingInput `json:"pending_inputs"`
}

type pendingInput struct {
	RequestID      string   `json:"request_id"`
	UniqueID       string   `json:"unique_id"`
	Message        string   `json:"message"`
	MessageForUser string   `json:"message_for_user"`
	Options        []string `json:"options"`
	AllowFeedback  bool     `json:"allow_feedback"`
}

func (p pendingInput) id() string {
	if p.RequestID != "" {
		return p.RequestID
	}
	return p.UniqueID
}

func (p pendingInput) text() string {
	if strings.TrimSpace(p.Message) != "" {
		return p.Message
	}
	return p.MessageForUser
}

// id is the call, operation or session ID the family's tools take.
func (s functionCallStatus) idFor(f elicitationFamily) string {
	switch f.idField {
	case "operation_id":
		return s.OperationID
	case "session_id":
		return s.SessionID
	}
	return s.CallID
}

func elicitationFamilyFor(target string, inputs ...map[string]any) (elicitationFamily, bool) {
	var args map[string]any
	if len(inputs) > 0 {
		args = inputs[0]
	}
	action, _ := args["action"].(string)
	if target == "functions" && (action == "call" || action == "status") {
		return elicitationFamily{get: target, reply: target, getAction: "status", replyAction: "reply", idField: "call_id", poll: action == "status"}, true
	}
	if target == "sessions" && (action == "builder_chat" || action == "builder_status") {
		return elicitationFamily{get: target, reply: target, getAction: "builder_status", replyAction: "builder_reply_input", idField: "operation_id", workflow: true, poll: action == "builder_status"}, true
	}
	if target == "runs" && action == "status" {
		return elicitationFamily{get: target, reply: target, getAction: "status", replyAction: "reply_input", idField: "session_id", workflow: true, poll: true}, true
	}
	// Private backend identities are used by hosted dispatch and stored Tasks.
	// They are never public aliases on the MCP transport.
	switch target {
	case "project_functions_call", "project_functions_get_call":
		return elicitationFamily{get: "project_functions_get_call", reply: "project_functions_reply", idField: "call_id"}, true
	case "builder_status":
		return elicitationFamily{get: "builder_status", reply: "builder_reply_input", idField: "operation_id", workflow: true}, true
	case "run_status":
		return elicitationFamily{get: "run_status", reply: "run_reply_input", idField: "session_id", workflow: true}, true
	}
	return elicitationFamily{}, false
}

// pollArgs and replyArgs build the arguments of the family's tools.
func (f elicitationFamily) pollArgs(state functionElicitationState) map[string]any {
	args := map[string]any{f.idField: state.CallID}
	if f.workflow {
		args["workflow_id"] = state.WorkflowID
	}
	if state.CrewID != "" {
		args["crew_id"] = state.CrewID
	}
	if f.getAction != "" {
		args["action"] = f.getAction
	}
	if f.get == "run_status" || f.get == "runs" {
		args["compact"] = true
	}
	return args
}

func (f elicitationFamily) replyArgs(state functionElicitationState, answer string) map[string]any {
	args := f.pollArgs(state)
	delete(args, "compact")
	args["request_id"] = state.RequestID
	args["response"] = answer
	if f.replyAction != "" {
		args["action"] = f.replyAction
	}
	return args
}

// ClientCanElicit reports whether this request's client accepts form
// elicitation. Modern (2026-07-28+) requests carry capabilities per request.
func (h FunctionElicitationHost) ClientCanElicit(ctx context.Context) bool {
	capability := h.clientElicitation(ctx)
	// An empty object is the protocol's shorthand for form support. A
	// URL-only client must never receive a form.
	return capability != nil && (capability.Form != nil || capability.URL == nil)
}

func (h FunctionElicitationHost) clientElicitation(ctx context.Context) *mcp.ElicitationCapability {
	if info := server.RequestProtocolInfoFromContext(ctx); info != nil && info.Modern {
		if info.ClientCapabilities != nil {
			return info.ClientCapabilities.Elicitation
		}
		return nil
	}
	if !h.AllowLegacySession {
		return nil
	}
	if session := server.ClientSessionFromContext(ctx); session != nil {
		if capable, ok := session.(interface{ GetClientCapabilities() mcp.ClientCapabilities }); ok {
			return capable.GetClientCapabilities().Elicitation
		}
	}
	return nil
}

func ClientURLCapability(ctx context.Context, legacy bool) bool {
	capability := (FunctionElicitationHost{AllowLegacySession: legacy}).clientElicitation(ctx)
	return capability != nil && capability.URL != nil
}

// MaybeElicit returns an input_required result for the first pending,
// answerable, non-sensitive question in a successful function-call result,
// or nil to return the ordinary result.
func (h FunctionElicitationHost) MaybeElicit(ctx context.Context, target string, args map[string]any, raw json.RawMessage) *mcp.CallToolResult {
	if target == "workspace" {
		return h.workspaceMaybeElicit(ctx, args, raw)
	}
	family, ok := elicitationFamilyFor(target, args)
	if !ok || !h.ClientCanElicit(ctx) || !family.available(h, false) || !family.available(h, true) {
		return nil
	}
	var status functionCallStatus
	if err := json.Unmarshal(raw, &status); err != nil || status.idFor(family) == "" || len(status.PendingInputs) == 0 {
		return nil
	}
	// A workflow reader may see questions but cannot answer them.
	if status.CanReply != nil && !*status.CanReply {
		return nil
	}
	input := status.PendingInputs[0]
	if input.id() == "" || strings.TrimSpace(input.text()) == "" || SecretQuestion(input.text()) {
		return nil
	}
	state := functionElicitationState{Target: target, CallID: status.idFor(family), RequestID: input.id()}
	state.CrewID, _ = args["crew_id"].(string)
	state.Action, _ = args["action"].(string)
	if family.workflow {
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
	message := input.text()
	if len(input.Options) > 0 {
		if input.AllowFeedback {
			message += " Suggested choices: " + strings.Join(input.Options, ", ") + ". You may also enter another answer."
		} else {
			answer["enum"] = input.Options
		}
	}
	return server.NewInputRequestBuilder(string(encoded)).Elicit(input.id(), mcp.ElicitationParams{
		Mode: mcp.ElicitationModeForm, Message: message,
		RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{"response": answer},
			"required": []string{"response"}, "additionalProperties": false},
	}).ToolResult()
}

// Resume handles a retried call that carries requestState and
// inputResponses. It never re-runs the original tool (a retried
// project_functions_call must not start a second call): an accepted answer goes
// to the call-scoped reply tool, then the call is polled.
func (h FunctionElicitationHost) Resume(ctx context.Context, target string, args map[string]any, request mcp.CallToolRequest) *mcp.CallToolResult {
	if !h.ClientCanElicit(ctx) {
		return mcp.NewToolResultError("elicitation_not_supported: use call-ID polling and reply tools")
	}
	if target == "workspace" {
		return h.workspaceResume(ctx, args, request)
	}
	var state functionElicitationState
	action, _ := args["action"].(string)
	if err := json.Unmarshal([]byte(request.Params.RequestState), &state); err != nil || state.Target != target || state.Action != action || state.CallID == "" || state.RequestID == "" {
		return mcp.NewToolResultError("invalid_request_state: the elicitation does not match this tool call")
	}
	family, known := elicitationFamilyFor(target, args)
	if !known || !family.available(h, false) {
		return mcp.NewToolResultError("insufficient_scope: function-call polling is unavailable")
	}
	// A retried poll must name the same call, operation or session; a retried
	// chat has no such argument yet (its session was created by the first call).
	if family.poll || strings.HasPrefix(target, "get_") || target == "builder_status" || target == "run_status" {
		if id, _ := args[family.idField].(string); id != state.CallID {
			return mcp.NewToolResultError("invalid_request_state: " + family.idField + " changed during elicitation")
		}
	}
	crewID, _ := args["crew_id"].(string)
	if crewID != state.CrewID {
		return mcp.NewToolResultError("invalid_request_state: crew_id changed during elicitation")
	}
	if family.workflow {
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
		if !family.available(h, true) {
			return mcp.NewToolResultError("insufficient_scope: answering this question is unavailable")
		}
		content, ok := response.Content.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("invalid_input_response: expected a response field")
		}
		answer, ok := content["response"].(string)
		if !ok || strings.TrimSpace(answer) == "" {
			return mcp.NewToolResultError("invalid_input_response: response must be nonempty text")
		}
		if _, failure := h.Call(ctx, family.reply, family.replyArgs(state, answer)); failure != nil {
			return failure
		}
	case mcp.ElicitationResponseActionDecline, mcp.ElicitationResponseActionCancel:
		// The question stays pending for another answer route.
	default:
		return mcp.NewToolResultError("invalid_input_response: unknown elicitation action")
	}
	raw, failure := h.Call(ctx, family.get, family.pollArgs(state))
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
	if input, ok := client.(MCPInputService); ok && ClientURLCapability(ctx, true) && MCPURLSignInCandidate(target, args) {
		return input.MCPInputCall(ctx, target, args, request, (FunctionElicitationHost{AllowLegacySession: true}).clientElicitation(ctx))
	}
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
