package virtualtools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// GetSubAgentToolCategory returns the category name for sub-agent tools
func GetSubAgentToolCategory() string {
	return "sub_agent_tools"
}

// Context keys for sub-agent tool execution
type subAgentContextKey string

const (
	// ExecutePredefinedSubAgentKey is the context key for the predefined sub-agent execution function
	ExecutePredefinedSubAgentKey subAgentContextKey = "execute_predefined_sub_agent"
	// ExecuteGenericAgentKey is the context key for the generic agent execution function
	ExecuteGenericAgentKey subAgentContextKey = "execute_generic_agent"
	// PredefinedRoutesKey is the context key for available predefined routes
	PredefinedRoutesKey subAgentContextKey = "predefined_routes"
	// PreferredTierContextKey is the context key for preferred LLM tier override (1/2/3)
	PreferredTierContextKey subAgentContextKey = "preferred_tier"
	// TierSelectionRequiredKey is the context key that signals handlers to reject
	// sub-agent calls missing a valid preferred_tier. Set by the orchestrator wrapper
	// when dynamic tier selection is active.
	TierSelectionRequiredKey subAgentContextKey = "tier_selection_required"
	// SubAgentLLMContextKey is the context key for direct LLM override for sub-agents (works in both tiered and manual modes)
	SubAgentLLMContextKey subAgentContextKey = "sub_agent_llm"
	// SubAgentAgentRestartKey is the context key for forcing an agent route to start fresh.
	SubAgentAgentRestartKey subAgentContextKey = "agent_restart"
	// SubAgentParametersKey carries typed, per-call inputs for a predefined
	// scripted route. The controller validates these values against the route's
	// declared script_parameters before starting the child.
	SubAgentParametersKey subAgentContextKey = "sub_agent_parameters"
	// ScriptedSubAgentInvocationKey distinguishes the dedicated scripted-route
	// tool from the conversational predefined-route tool at the shared executor.
	ScriptedSubAgentInvocationKey subAgentContextKey = "scripted_sub_agent_invocation"
	// GenericAgentAgentKey carries optional ordered follow-up turns for
	// call_generic_agent. The runtime reuses one agent/session for every turn.
	GenericAgentAgentKey subAgentContextKey = "generic_agent_agent"
	// GetSubAgentConversationKey is the context key for the get_sub_agent_conversation function
	GetSubAgentConversationKey subAgentContextKey = "get_sub_agent_conversation"
	// QuerySubAgentKey is the context key for querying one parent-owned sub-agent execution.
	QuerySubAgentKey subAgentContextKey = "query_sub_agent"
	// StopSubAgentKey is the context key for canceling one parent-owned sub-agent execution.
	StopSubAgentKey subAgentContextKey = "stop_sub_agent"
	// RouteDescriptionsKey is the context key for a map of route_id -> description string
	RouteDescriptionsKey subAgentContextKey = "route_descriptions"
)

// GetSubAgentConversationFunc is the function signature for retrieving sub-agent conversation history.
// executionID identifies the exact sub-agent call, fromLastX is how many entries to return,
// offsetLastX skips that many entries from the tail before applying fromLastX (for paging).
type GetSubAgentConversationFunc func(ctx context.Context, executionID string, fromLastX, offsetLastX int) (string, error)

// QuerySubAgentFunc returns the current state of one execution owned by this orchestrator.
type QuerySubAgentFunc func(ctx context.Context, executionID string) (string, error)

// StopSubAgentFunc requests cancellation of one execution owned by this orchestrator.
type StopSubAgentFunc func(ctx context.Context, executionID string) (string, error)

// SubAgentResult represents the result of a sub-agent execution
type SubAgentResult struct {
	Success       bool      `json:"success"`
	TodoID        string    `json:"task_id"`
	RouteID       string    `json:"route_id,omitempty"`
	AgentType     string    `json:"agent_type"` // "predefined" or "generic"
	Result        string    `json:"result"`
	Error         string    `json:"error,omitempty"`
	Hint          string    `json:"hint,omitempty"`
	RetryAttempts int       `json:"retry_attempts"` // Number of attempts the sub-agent made (including retries)
	ExecutionTime string    `json:"execution_time"`
	CompletedAt   time.Time `json:"completed_at"`
}

// maxSubAgentRetryAttempts is the number of retry attempts a sub-agent makes internally before returning failure
const maxSubAgentRetryAttempts = 3

// ExecutePredefinedSubAgentFunc is the function signature for executing predefined sub-agents
// Injected via context by the controller
type ExecutePredefinedSubAgentFunc func(ctx context.Context, routeID, todoID, instructions string) (string, error)

// SubAgentParametersFromContext returns a defensive copy of the parameters
// supplied to call_scripted_sub_agent. Keeping them in the call context avoids
// changing the shared predefined-route executor signature and prevents
// concurrent route calls from sharing mutable parameter state.
func SubAgentParametersFromContext(ctx context.Context) map[string]interface{} {
	params, _ := ctx.Value(SubAgentParametersKey).(map[string]interface{})
	result := make(map[string]interface{}, len(params))
	for key, value := range params {
		result[key] = value
	}
	return result
}

// IsScriptedSubAgentInvocation reports whether the predefined route was
// selected through call_scripted_sub_agent rather than call_sub_agent.
func IsScriptedSubAgentInvocation(ctx context.Context) bool {
	value, _ := ctx.Value(ScriptedSubAgentInvocationKey).(bool)
	return value
}

// ExecuteGenericAgentFunc is the function signature for executing generic agents
// Injected via context by the controller
type ExecuteGenericAgentFunc func(ctx context.Context, todoID, instructions string) (string, error)

// GenericAgentMessage is one follow-up user turn after call_generic_agent's
// opening instructions. ID is required for event/log diagnostics.
type GenericAgentMessage struct {
	ID      string
	Title   string
	Message string
}

// GenericAgentAgentFromContext returns a defensive copy of the
// ordered follow-up messages attached by handleCallGenericAgent.
func GenericAgentAgentFromContext(ctx context.Context) []GenericAgentMessage {
	items, _ := ctx.Value(GenericAgentAgentKey).([]GenericAgentMessage)
	return append([]GenericAgentMessage(nil), items...)
}

// CreateSubAgentTools creates the sub-agent calling virtual tools.
// preferred_tier is always a REQUIRED parameter on all sub-agent call tools — the
// orchestrator must explicitly reason about task difficulty on every delegation.
// When the workflow has no tier resolver or the step pins an ExecutionLLM, the
// tier value is informational (inherited/pinned LLM is used regardless), but the
// parameter remains required for prompt discipline.
func CreateSubAgentTools() []llmtypes.Tool {
	var tools []llmtypes.Tool

	// call_sub_agent executes conversational predefined routes. Scripted routes
	// have a separate tool so instructions and typed parameters are never an
	// ambiguous, mutually-exclusive argument pair.
	callSubAgentProperties := map[string]interface{}{
		"route_id": map[string]interface{}{
			"type":        "string",
			"description": "Parent-side selector for the predefined route to execute (from the available routes in your context). The runtime resolves it; do not repeat it inside instructions.",
		},
		"task_id": map[string]interface{}{
			"type":        "string",
			"description": "Stable label for this unit of delegated work (kebab-case, e.g. draft-intro-section). Used by the parent runtime for tracking and artifact naming; it is not child task context and should not be repeated inside instructions.",
		},
		"instructions": map[string]interface{}{
			"type":        "string",
			"description": "Required instructions for an agent or agent route. Scripted routes use call_scripted_sub_agent instead.",
		},
		"preferred_tier": map[string]interface{}{
			"type":        "integer",
			"description": "REQUIRED. LLM reasoning tier for this sub-agent. 1 = high reasoning (complex/novel tasks), 2 = medium reasoning (routine tasks), 3 = low reasoning (simple/validation tasks). You must pick a tier for every call based on the task's difficulty.",
			"enum":        []int{1, 2, 3},
		},
		"agent_restart": map[string]interface{}{
			"type":        "boolean",
			"description": "Optional. Only for agent routes. If true, archive the existing route conversation and replay the configured item queue from the beginning. Default: false, which resumes the existing route conversation and sends instructions as the re-entry message.",
		},
	}
	callSubAgentRequired := []string{"route_id", "task_id", "instructions", "preferred_tier"}
	callSubAgentTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "call_sub_agent",
			Description: "Start a predefined agent or agent route with per-call instructions. Scripted routes use call_scripted_sub_agent. The call returns an execution_id immediately; the workflow runtime waits outside this tool call and sends one authoritative completion batch back to this orchestrator. Launch independent calls together. For agent routes, repeated calls resume the route conversation unless agent_restart is true.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type":       "object",
				"properties": callSubAgentProperties,
				"required":   callSubAgentRequired,
			}),
		},
	}
	tools = append(tools, callSubAgentTool)

	callScriptedSubAgentTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "call_scripted_sub_agent",
			Description: "Start a predefined scripted route using its persisted typed parameter contract. Call get_route_description(route_id) first, then pass exactly the declared values. The runtime applies defaults and rejects unknown, missing, wrongly typed, or invalid enum values before main.py starts. This tool accepts no free-form instructions.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"route_id": map[string]interface{}{
						"type":        "string",
						"description": "Parent-side selector for the predefined scripted route. Inspect it with get_route_description before calling.",
					},
					"task_id": map[string]interface{}{
						"type":        "string",
						"description": "Stable label for this unit of scripted work (kebab-case). Used for tracking and artifact naming.",
					},
					"parameters": map[string]interface{}{
						"type":                 "object",
						"description":          "Required JSON object containing only values declared by this scripted route. Pass {} when the route declares no parameters.",
						"additionalProperties": true,
					},
					"preferred_tier": map[string]interface{}{
						"type":        "integer",
						"description": "REQUIRED. LLM tier used only if the saved script enters its repair path: 1 = high, 2 = medium, 3 = low. The saved-script fast path itself makes no LLM call.",
						"enum":        []int{1, 2, 3},
					},
				},
				"required": []string{"route_id", "task_id", "parameters", "preferred_tier"},
			}),
		},
	}
	tools = append(tools, callScriptedSubAgentTool)

	// call_generic_agent tool - Execute a generic agent for ad-hoc tasks
	callGenericAgentProperties := map[string]interface{}{
		"task_id": map[string]interface{}{
			"type":        "string",
			"description": "Stable label for this unit of delegated work (kebab-case, e.g. draft-intro-section). Used by the parent runtime for tracking and artifact naming; it is not child task context and should not be repeated inside instructions.",
		},
		"instructions": map[string]interface{}{
			"type":        "string",
			"description": "Detailed instructions for what the agent should accomplish. Be very specific since there's no predefined context.",
		},
		"agent": map[string]interface{}{
			"type":        "array",
			"minItems":    1,
			"maxItems":    12,
			"description": "Optional ordered follow-up turns sent to the same generic agent after the opening instructions turn. One conversation, MCP session, folder guard, and isolated workspace are preserved.",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":      map[string]interface{}{"type": "string"},
					"title":   map[string]interface{}{"type": "string"},
					"message": map[string]interface{}{"type": "string"},
				},
				"required": []string{"id", "message"},
			},
		},
	}
	callGenericAgentProperties["preferred_tier"] = map[string]interface{}{
		"type":        "integer",
		"description": "REQUIRED. LLM reasoning tier for this sub-agent. 1 = high reasoning (complex/novel tasks), 2 = medium reasoning (routine tasks), 3 = low reasoning (simple/validation tasks). You must pick a tier for every call based on the task's difficulty.",
		"enum":        []int{1, 2, 3},
	}
	callGenericAgentRequired := []string{"task_id", "instructions", "preferred_tier"}
	callGenericAgentTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "call_generic_agent",
			Description: "Start a generic agent for ad-hoc work that doesn't match predefined routes. The call returns an execution_id immediately; the workflow runtime waits outside this tool call and sends its terminal result back to this orchestrator. Use this sparingly and prefer predefined routes.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type":       "object",
				"properties": callGenericAgentProperties,
				"required":   callGenericAgentRequired,
			}),
		},
	}
	tools = append(tools, callGenericAgentTool)

	querySubAgentTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "query_sub_agent",
			Description: "Read the current state of one sub-agent execution returned by call_sub_agent, call_scripted_sub_agent, or call_generic_agent. This is for explicit inspection or debugging; do not poll it in a loop because the runtime automatically delivers completion results.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"execution_id": map[string]interface{}{
						"type":        "string",
						"description": "Exact execution_id returned when the sub-agent started.",
					},
				},
				"required": []string{"execution_id"},
			}),
		},
	}
	tools = append(tools, querySubAgentTool)

	stopSubAgentTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "stop_sub_agent",
			Description: "Cancel one currently running sub-agent owned by this orchestrator. Use only for an explicit stop request or a confirmed stuck/incorrect child. The runtime still waits for cancellation to finish before advancing.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"execution_id": map[string]interface{}{
						"type":        "string",
						"description": "Exact running execution_id returned when the sub-agent started.",
					},
				},
				"required": []string{"execution_id"},
			}),
		},
	}
	tools = append(tools, stopSubAgentTool)

	// get_sub_agent_conversation tool - Query the internal conversation of a previous sub-agent call
	getSubAgentConversationTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "get_sub_agent_conversation",
			Description: "Get the internal conversation of a sub-agent call — all tool calls, tool results, and reasoning steps. Returns the last 'from_last_x' entries. Use 'offset_last_x' to page backwards through the conversation.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"execution_id": map[string]interface{}{
						"type":        "string",
						"description": "Required. The exact execution_id returned by call_sub_agent, call_scripted_sub_agent, or call_generic_agent.",
					},
					"from_last_x": map[string]interface{}{
						"type":        "integer",
						"description": "Required. Number of conversation entries to return from the end.",
					},
					"offset_last_x": map[string]interface{}{
						"type":        "integer",
						"description": "Optional. Skip this many entries from the tail before applying from_last_x. Use to page backwards. Default 0.",
					},
				},
				"required": []string{"execution_id", "from_last_x"},
			}),
		},
	}
	tools = append(tools, getSubAgentConversationTool)

	// get_route_description tool - Get the full description/instructions for a predefined route
	getRouteDescriptionTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "get_route_description",
			Description: "Get details for one predefined route when its short catalog entry is not enough to decide whether or how to use it. Do not call it routinely for an already-clear route.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"route_id": map[string]interface{}{
						"type":        "string",
						"description": "The ID of the predefined route to get the description for",
					},
				},
				"required": []string{"route_id"},
			}),
		},
	}
	tools = append(tools, getRouteDescriptionTool)

	return tools
}

// CreateSubAgentToolExecutors creates the execution functions for sub-agent tools
// Note: These executors require context injection from the controller to work
func CreateSubAgentToolExecutors() map[string]func(ctx context.Context, args map[string]interface{}) (string, error) {
	executors := make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error))

	executors["call_sub_agent"] = handleCallSubAgent
	executors["call_scripted_sub_agent"] = handleCallScriptedSubAgent
	executors["call_generic_agent"] = handleCallGenericAgent
	executors["query_sub_agent"] = handleQuerySubAgent
	executors["stop_sub_agent"] = handleStopSubAgent
	executors["get_sub_agent_conversation"] = handleGetSubAgentConversation
	executors["get_route_description"] = handleGetRouteDescription
	return executors
}

func handleQuerySubAgent(ctx context.Context, args map[string]interface{}) (string, error) {
	executionID, _ := args["execution_id"].(string)
	executionID = strings.TrimSpace(executionID)
	if executionID == "" {
		return "", fmt.Errorf("execution_id is required")
	}
	queryFunc, ok := ctx.Value(QuerySubAgentKey).(QuerySubAgentFunc)
	if !ok || queryFunc == nil {
		return "", fmt.Errorf("query_sub_agent function not available in context - this tool can only inspect sub-agents owned by the current todo task orchestrator")
	}
	return queryFunc(ctx, executionID)
}

func handleStopSubAgent(ctx context.Context, args map[string]interface{}) (string, error) {
	executionID, _ := args["execution_id"].(string)
	executionID = strings.TrimSpace(executionID)
	if executionID == "" {
		return "", fmt.Errorf("execution_id is required")
	}
	stopFunc, ok := ctx.Value(StopSubAgentKey).(StopSubAgentFunc)
	if !ok || stopFunc == nil {
		return "", fmt.Errorf("stop_sub_agent function not available in context - this tool can only stop sub-agents owned by the current todo task orchestrator")
	}
	return stopFunc(ctx, executionID)
}

// handleCallSubAgent executes a predefined sub-agent
func handleCallSubAgent(ctx context.Context, args map[string]interface{}) (string, error) {
	// Extract arguments
	routeID, ok := args["route_id"].(string)
	if !ok || routeID == "" {
		return "", fmt.Errorf("route_id is required")
	}

	todoID, ok := args["task_id"].(string)
	if !ok || todoID == "" {
		return "", fmt.Errorf("task_id is required")
	}

	instructions, ok := args["instructions"].(string)
	if !ok || strings.TrimSpace(instructions) == "" {
		return "", fmt.Errorf("instructions are required; scripted routes use call_scripted_sub_agent")
	}
	if _, supplied := args["parameters"]; supplied {
		return "", fmt.Errorf("parameters are not accepted by call_sub_agent; use call_scripted_sub_agent for scripted routes")
	}

	tierRequired, _ := ctx.Value(TierSelectionRequiredKey).(bool)
	preferredTierF, hasTier := args["preferred_tier"].(float64)
	validTier := hasTier && int(preferredTierF) >= 1 && int(preferredTierF) <= 3
	if tierRequired && !validTier {
		return "", fmt.Errorf("preferred_tier is required and must be 1, 2, or 3 (1=high reasoning, 2=medium, 3=low) — pick a tier based on task difficulty")
	}
	if validTier {
		ctx = context.WithValue(ctx, PreferredTierContextKey, int(preferredTierF))
	}

	if restart, ok := args["agent_restart"].(bool); ok && restart {
		ctx = context.WithValue(ctx, SubAgentAgentRestartKey, true)
	}

	return executePredefinedSubAgentCall(ctx, routeID, todoID, instructions)
}

// handleCallScriptedSubAgent executes one predefined scripted route with typed
// parameters and no conversational instructions.
func handleCallScriptedSubAgent(ctx context.Context, args map[string]interface{}) (string, error) {
	routeID, ok := args["route_id"].(string)
	if !ok || strings.TrimSpace(routeID) == "" {
		return "", fmt.Errorf("route_id is required")
	}
	todoID, ok := args["task_id"].(string)
	if !ok || strings.TrimSpace(todoID) == "" {
		return "", fmt.Errorf("task_id is required")
	}
	rawParameters, supplied := args["parameters"]
	parameters, ok := rawParameters.(map[string]interface{})
	if !supplied || !ok {
		return "", fmt.Errorf("parameters is required and must be an object; pass {} when the scripted route declares no parameters")
	}
	if _, supplied := args["instructions"]; supplied {
		return "", fmt.Errorf("instructions are not accepted by call_scripted_sub_agent; declare runtime variation in script_parameters")
	}
	copied := make(map[string]interface{}, len(parameters))
	for key, value := range parameters {
		copied[key] = value
	}
	ctx = context.WithValue(ctx, SubAgentParametersKey, copied)
	ctx = context.WithValue(ctx, ScriptedSubAgentInvocationKey, true)

	tierRequired, _ := ctx.Value(TierSelectionRequiredKey).(bool)
	preferredTierF, hasTier := args["preferred_tier"].(float64)
	validTier := hasTier && int(preferredTierF) >= 1 && int(preferredTierF) <= 3
	if tierRequired && !validTier {
		return "", fmt.Errorf("preferred_tier is required and must be 1, 2, or 3 (1=high reasoning, 2=medium, 3=low) — pick a tier for possible repair difficulty")
	}
	if validTier {
		ctx = context.WithValue(ctx, PreferredTierContextKey, int(preferredTierF))
	}

	return executePredefinedSubAgentCall(ctx, routeID, todoID, "")
}

func executePredefinedSubAgentCall(ctx context.Context, routeID, todoID, instructions string) (string, error) {
	// Get the execution function from context
	executeFunc, ok := ctx.Value(ExecutePredefinedSubAgentKey).(ExecutePredefinedSubAgentFunc)
	if !ok || executeFunc == nil {
		return "", fmt.Errorf("predefined sub-agent execution function not available in context - this tool can only be used within a todo task orchestrator")
	}

	startTime := time.Now()

	// Execute the sub-agent
	result, err := executeFunc(ctx, routeID, todoID, instructions)
	if err == nil && isAsyncSubAgentStart(result) {
		return result, nil
	}

	executionTime := time.Since(startTime)

	// Build result
	// Determine success: err must be nil AND result must not contain failure indicators
	// (execution failures can be auto-approved by sub-agent flow, returning nil error with empty/failed result)
	isSuccess := err == nil && !isSubAgentResultFailure(result)

	subAgentResult := SubAgentResult{
		Success:       isSuccess,
		TodoID:        todoID,
		RouteID:       routeID,
		AgentType:     "predefined",
		Result:        result,
		RetryAttempts: maxSubAgentRetryAttempts,
		ExecutionTime: executionTime.String(),
		CompletedAt:   time.Now(),
	}

	if !isSuccess {
		if err != nil {
			subAgentResult.Error = err.Error()
		} else {
			// No Go error, but result contains failure details (e.g. "ERROR: agent execution failed: ...")
			subAgentResult.Error = result
		}
		subAgentResult.Hint = fmt.Sprintf("Sub-agent failed after %d internal retry attempts. Consider using get_sub_agent_conversation to inspect what went wrong, review LEARNING HISTORY, and execute the task yourself — you have a higher-reasoning LLM and the same tool access.", maxSubAgentRetryAttempts)
	}

	resultJSON, _ := json.MarshalIndent(subAgentResult, "", "  ")
	return string(resultJSON), nil
}

// handleCallGenericAgent executes a generic agent
func handleCallGenericAgent(ctx context.Context, args map[string]interface{}) (string, error) {
	// Extract arguments
	todoID, ok := args["task_id"].(string)
	if !ok || todoID == "" {
		return "", fmt.Errorf("task_id is required")
	}

	instructions, ok := args["instructions"].(string)
	if !ok || instructions == "" {
		return "", fmt.Errorf("instructions are required")
	}
	agentSequence, err := parseGenericAgentAgent(args["agent"])
	if err != nil {
		return "", err
	}
	if len(agentSequence) > 0 {
		ctx = context.WithValue(ctx, GenericAgentAgentKey, agentSequence)
	}

	tierRequired, _ := ctx.Value(TierSelectionRequiredKey).(bool)
	preferredTierF, hasTier := args["preferred_tier"].(float64)
	validTier := hasTier && int(preferredTierF) >= 1 && int(preferredTierF) <= 3
	if tierRequired && !validTier {
		return "", fmt.Errorf("preferred_tier is required and must be 1, 2, or 3 (1=high reasoning, 2=medium, 3=low) — pick a tier based on task difficulty")
	}
	if validTier {
		ctx = context.WithValue(ctx, PreferredTierContextKey, int(preferredTierF))
	}

	// Get the execution function from context
	executeFunc, ok := ctx.Value(ExecuteGenericAgentKey).(ExecuteGenericAgentFunc)
	if !ok || executeFunc == nil {
		return "", fmt.Errorf("generic agent execution function not available in context - this tool can only be used within a todo task orchestrator")
	}

	startTime := time.Now()

	// Execute the generic agent
	result, err := executeFunc(ctx, todoID, instructions)
	if err == nil && isAsyncSubAgentStart(result) {
		return result, nil
	}

	executionTime := time.Since(startTime)

	// Build result
	// Determine success: err must be nil AND result must not contain failure indicators
	isSuccess := err == nil && !isSubAgentResultFailure(result)

	subAgentResult := SubAgentResult{
		Success:       isSuccess,
		TodoID:        todoID,
		AgentType:     "generic",
		Result:        result,
		RetryAttempts: maxSubAgentRetryAttempts,
		ExecutionTime: executionTime.String(),
		CompletedAt:   time.Now(),
	}

	if !isSuccess {
		if err != nil {
			subAgentResult.Error = err.Error()
		} else {
			// No Go error, but result contains failure details (e.g. "ERROR: agent execution failed: ...")
			subAgentResult.Error = result
		}
		subAgentResult.Hint = fmt.Sprintf("Sub-agent failed after %d internal retry attempts. Consider using get_sub_agent_conversation to inspect what went wrong, review LEARNING HISTORY, and execute the task yourself — you have a higher-reasoning LLM and the same tool access.", maxSubAgentRetryAttempts)
	}

	resultJSON, _ := json.MarshalIndent(subAgentResult, "", "  ")
	return string(resultJSON), nil
}

func parseGenericAgentAgent(raw interface{}) ([]GenericAgentMessage, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("agent must be an array")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("agent must contain at least one follow-up turn when supplied")
	}
	if len(items) > 12 {
		return nil, fmt.Errorf("agent has %d items; maximum is 12", len(items))
	}
	seen := make(map[string]struct{}, len(items))
	parsed := make([]GenericAgentMessage, 0, len(items))
	for index, rawItem := range items {
		item, ok := rawItem.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("agent[%d] must be an object", index)
		}
		id, _ := item["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("agent[%d].id is required", index)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("agent item id %q is duplicated", id)
		}
		seen[id] = struct{}{}
		message, _ := item["message"].(string)
		message = strings.TrimSpace(message)
		if message == "" {
			return nil, fmt.Errorf("agent[%d].message is required", index)
		}
		title, _ := item["title"].(string)
		parsed = append(parsed, GenericAgentMessage{ID: id, Title: strings.TrimSpace(title), Message: message})
	}
	return parsed, nil
}

func isAsyncSubAgentStart(result string) bool {
	var payload struct {
		Async       bool   `json:"async"`
		ExecutionID string `json:"execution_id"`
	}
	return json.Unmarshal([]byte(result), &payload) == nil && payload.Async && strings.TrimSpace(payload.ExecutionID) != ""
}

// isSubAgentResultFailure checks if a sub-agent result string indicates failure
// This catches cases where execution failed but was auto-approved (nil error with empty/failed result)
func isSubAgentResultFailure(result string) bool {
	if strings.TrimSpace(result) == "" {
		return true
	}
	lower := strings.ToLower(result)
	return strings.Contains(lower, "failed:") ||
		strings.Contains(lower, "failed validation after") ||
		strings.Contains(lower, "execution failed") ||
		strings.Contains(lower, "error:")
}

// handleGetRouteDescription returns the full description of a predefined route
func handleGetRouteDescription(ctx context.Context, args map[string]interface{}) (string, error) {
	routeID, ok := args["route_id"].(string)
	if !ok || routeID == "" {
		return "", fmt.Errorf("route_id is required")
	}

	// Get route descriptions map from context
	descriptionsMap, ok := ctx.Value(RouteDescriptionsKey).(map[string]string)
	if !ok || descriptionsMap == nil {
		return "", fmt.Errorf("route descriptions not available in context")
	}

	description, exists := descriptionsMap[routeID]
	if !exists {
		// List available routes for the error message
		var available []string
		for id := range descriptionsMap {
			available = append(available, id)
		}
		return "", fmt.Errorf("route '%s' not found. Available routes: %s", routeID, strings.Join(available, ", "))
	}

	result := map[string]string{
		"route_id":    routeID,
		"description": description,
	}
	resultJSON, _ := json.MarshalIndent(result, "", "  ")
	return string(resultJSON), nil
}

// handleGetSubAgentConversation retrieves the internal conversation of a previous sub-agent call
func handleGetSubAgentConversation(ctx context.Context, args map[string]interface{}) (string, error) {
	// Inspect one exact call. A task label can be retried or delegated more than once,
	// so task_id is not a unique conversation identity.
	executionID, ok := args["execution_id"].(string)
	if !ok || strings.TrimSpace(executionID) == "" {
		return "", fmt.Errorf("execution_id is required")
	}
	executionID = strings.TrimSpace(executionID)

	// Extract from_last_x (required, must be > 0)
	fromLastXRaw, ok := args["from_last_x"].(float64)
	if !ok {
		return "", fmt.Errorf("from_last_x is required and must be an integer")
	}
	fromLastX := int(fromLastXRaw)
	if fromLastX <= 0 {
		return "", fmt.Errorf("from_last_x must be greater than 0")
	}

	// Extract offset_last_x (optional, default 0)
	offsetLastX := 0
	if offsetLastXRaw, ok := args["offset_last_x"].(float64); ok {
		offsetLastX = int(offsetLastXRaw)
		if offsetLastX < 0 {
			offsetLastX = 0
		}
	}

	// Get the conversation retrieval function from context
	getConvFunc, ok := ctx.Value(GetSubAgentConversationKey).(GetSubAgentConversationFunc)
	if !ok || getConvFunc == nil {
		return "", fmt.Errorf("get_sub_agent_conversation function not available in context - this tool can only be used within a todo task orchestrator after a sub-agent has been called")
	}

	return getConvFunc(ctx, executionID, fromLastX, offsetLastX)
}
