// parallel_tool_execution.go
//
// This file implements parallel (concurrent) execution of multiple tool calls
// returned by the LLM in a single response. When EnableParallelToolExecution is true
// and the LLM returns >1 tool calls, they execute concurrently using a fork-join pattern.
// Results are collected in pre-allocated indexed slots and assembled in deterministic order.
//
// Exported:
//   - executeToolCallsParallel

package mcpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/toolerr"
	"github.com/manishiitg/mcpagent/toolguard"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/mark3labs/mcp-go/mcp"
)

// toolExecutionPlan contains all pre-processed data needed to execute a single tool call.
// Built sequentially in Phase 1 before goroutines are launched.
type toolExecutionPlan struct {
	index      int
	toolCall   llmtypes.ToolCall
	args       map[string]interface{}
	client     mcpclient.ClientInterface
	serverName string

	isCustomTool bool
	isVirtual    bool
	directTool   registeredTool

	toolTimeout  time.Duration
	hasNoTimeout bool
	toolType     string // "MCP", "virtual", "custom"

	// If true, skip execution — a pre-error message is already set
	skipExecution   bool
	preErrorMessage *llmtypes.MessageContent
}

// toolExecutionResult holds the output of a single tool execution goroutine.
// Written by exactly one goroutine into its indexed slot.
type toolExecutionResult struct {
	// messages to append (tool response)
	messages []llmtypes.MessageContent

	// Raw result from tool execution (for event emission and loop detection)
	result     *mcp.CallToolResult
	resultText string
	duration   time.Duration
	toolErr    error

	// If set, the entire conversation should return this error
	fatalError error
}

// executeToolCallsParallel orchestrates concurrent execution of multiple tool calls.
//
// Phase 1 (sequential): Prepare all tool calls — parse args, resolve clients, emit start events.
// Phase 2 (parallel):   Launch goroutines, each writes result to its indexed slot.
// Phase 3 (sequential): Assemble messages in order, emit end/error events, run loop detection.
func executeToolCallsParallel(
	ctx context.Context,
	a *Agent,
	toolCalls []llmtypes.ToolCall,
	messages []llmtypes.MessageContent,
	turn int,
	traceID string,
	conversationStartTime time.Time,
	lastUserMessage string,
	loopDetector *ToolLoopDetector,
	agentCtx context.Context,
) ([]llmtypes.MessageContent, error) {

	v2Logger := a.logger

	// ─── Phase 1: Sequential preparation ───────────────────────────────────

	plans := make([]toolExecutionPlan, len(toolCalls))
	for i, tc := range toolCalls {
		plan := prepareToolExecution(ctx, a, tc, i, turn, traceID, conversationStartTime, agentCtx)
		plans[i] = plan

		// Emit tool call start event (sequential to keep hierarchy sane)
		if !plan.skipExecution {
			toolStartEvent := events.NewToolCallStartEventWithCorrelation(turn+1, tc.FunctionCall.Name, events.ToolParams{
				Arguments: tc.FunctionCall.Arguments,
			}, plan.serverName, traceID, traceID)
			toolStartEvent.IsParallel = true
			toolStartEvent.ToolCallID = tc.ID
			a.emitTypedEvent(ctx, toolStartEvent)
		}
	}

	// Check for context cancellation before launching goroutines
	if agentCtx.Err() != nil {
		v2Logger.Debug("Context cancelled before parallel tool execution",
			loggerv2.Int("turn", turn+1),
			loggerv2.Error(agentCtx.Err()))
		cancellationEvent := events.NewContextCancelledEvent(
			turn+1,
			"cancelled before parallel tool execution",
			time.Since(conversationStartTime),
		)
		a.emitTypedEvent(ctx, cancellationEvent)
		return messages, fmt.Errorf("conversation cancelled before parallel tool execution: %w", agentCtx.Err())
	}

	// ─── Phase 2: Parallel execution ───────────────────────────────────────

	results := make([]toolExecutionResult, len(plans))
	var wg sync.WaitGroup

	for i, plan := range plans {
		if plan.skipExecution {
			// Pre-error: already have the message, no goroutine needed
			if plan.preErrorMessage != nil {
				results[i] = toolExecutionResult{
					messages: []llmtypes.MessageContent{*plan.preErrorMessage},
				}
			}
			continue
		}

		wg.Add(1)
		go func(idx int, p toolExecutionPlan) {
			defer wg.Done()
			results[idx] = executeToolCall(ctx, a, p, turn, conversationStartTime, agentCtx)
		}(i, plan)
	}

	wg.Wait()

	// ─── Phase 3: Sequential assembly ──────────────────────────────────────

	for i, plan := range plans {
		res := results[i]
		tc := plan.toolCall

		// Check for fatal errors (should abort entire conversation)
		if res.fatalError != nil {
			return messages, res.fatalError
		}

		// Append messages in order
		messages = append(messages, res.messages...)

		// Emit end/error events
		if plan.skipExecution {
			// Pre-error plans already have their error events emitted in prepareToolExecution
			// or the error message was built without needing an end event
			continue
		}

		if res.toolErr != nil {
			// Durable record alongside the event. See conversation.go: events are
			// pruned and lazily fetched, logs are not.
			v2Logger.Error(toolerr.Marker+" tool failed", res.toolErr,
				loggerv2.String("layer", "agent_tool_loop_parallel"),
				loggerv2.String("tool", tc.FunctionCall.Name),
				loggerv2.String("server", plan.serverName),
				loggerv2.String("session_id", a.sessionID),
				loggerv2.String("duration", res.duration.String()),
				loggerv2.String("args", toolerr.TruncateArgsForLog(tc.FunctionCall.Arguments)))

			// Tool execution error — emit error event
			toolErrorEvent := events.NewToolCallErrorEvent(turn+1, tc.FunctionCall.Name, res.toolErr.Error(), plan.serverName, res.duration)
			toolErrorEvent.ToolCallID = tc.ID
			a.emitTypedEvent(ctx, toolErrorEvent)
		} else if res.result == nil || !res.result.IsError {
			if signal, suspicious := toolerr.SuspiciousForTool(tc.FunctionCall.Name, res.resultText); suspicious {
				v2Logger.Error(toolerr.SuspectMarker+" tool reported success but the result reads like a failure", nil,
					loggerv2.String("layer", "agent_tool_loop_parallel"),
					loggerv2.String("tool", tc.FunctionCall.Name),
					loggerv2.String("server", plan.serverName),
					loggerv2.String("session_id", a.sessionID),
					loggerv2.String("signal", signal),
					loggerv2.String("duration", res.duration.String()),
					loggerv2.String("result", toolerr.TruncateForLog(res.resultText)),
					loggerv2.String("args", toolerr.TruncateArgsForLog(tc.FunctionCall.Arguments)))
			}
			// Success — emit tool call end event
			_, _, _, _, _, _, _, _, _, _, _, _, contextUsagePercent := a.getTokenUsageWithPricing()
			a.tokenTrackingMutex.RLock()
			modelContextWindow := a.modelContextWindow
			contextWindowUsage := a.currentContextWindowUsage
			a.tokenTrackingMutex.RUnlock()

			toolEndEvent := events.NewToolCallEndEventWithTokenUsageAndModel(turn+1, tc.FunctionCall.Name, res.resultText, plan.serverName, res.duration, "", contextUsagePercent, modelContextWindow, contextWindowUsage, a.modelID)
			toolEndEvent.ToolCallID = tc.ID
			a.emitTypedEvent(ctx, toolEndEvent)
		} else if res.result != nil && res.result.IsError {
			v2Logger.Error(toolerr.Marker+" tool returned an error result", nil,
				loggerv2.String("layer", "agent_tool_loop_parallel"),
				loggerv2.String("tool", tc.FunctionCall.Name),
				loggerv2.String("server", plan.serverName),
				loggerv2.String("session_id", a.sessionID),
				loggerv2.String("duration", res.duration.String()),
				loggerv2.String("result", toolerr.TruncateForLog(res.resultText)),
				loggerv2.String("args", toolerr.TruncateArgsForLog(tc.FunctionCall.Arguments)))

			// Tool returned error in result
			toolErrorEvent := events.NewToolCallErrorEvent(turn+1, tc.FunctionCall.Name, res.resultText, plan.serverName, res.duration)
			toolErrorEvent.ToolCallID = tc.ID
			a.emitTypedEvent(ctx, toolErrorEvent)
		}

		// Loop detection (sequential)
		if tc.FunctionCall != nil && res.resultText != "" {
			loopResult := loopDetector.CheckAndHandleLoop(tc.FunctionCall.Name, tc.FunctionCall.Arguments, res.resultText)
			if loopResult.Detected {
				HandleLoopDetection(a, ctx, loopResult, lastUserMessage, turn+1, conversationStartTime, &messages, v2Logger)
			}
		}

	}

	return messages, nil
}

// prepareToolExecution extracts the pre-processing logic from the sequential loop.
// It parses arguments, resolves clients, validates the tool, and builds a plan.
// Does NOT execute the tool or emit end events.
func prepareToolExecution(
	ctx context.Context,
	a *Agent,
	tc llmtypes.ToolCall,
	index int,
	turn int,
	traceID string,
	conversationStartTime time.Time,
	agentCtx context.Context,
) toolExecutionPlan {

	v2Logger := a.logger

	plan := toolExecutionPlan{
		index:    index,
		toolCall: tc,
	}

	if tc.FunctionCall == nil {
		v2Logger.Warn("Tool call has nil FunctionCall", loggerv2.Int("tool_call_index", index+1))
		plan.skipExecution = true
		return plan
	}

	// Determine server name
	plan.serverName = a.toolToServer[tc.FunctionCall.Name]
	plan.isVirtual = isVirtualTool(tc.FunctionCall.Name)

	if plan.isVirtual {
		if tc.FunctionCall.Name == "get_api_spec" && tc.FunctionCall.Arguments != "" {
			var args map[string]interface{}
			if err := json.Unmarshal([]byte(tc.FunctionCall.Arguments), &args); err == nil {
				if srvName, ok := args["server_name"].(string); ok && srvName != "" {
					plan.serverName = srvName
				} else {
					plan.serverName = "virtual-tools"
				}
			} else {
				plan.serverName = "virtual-tools"
			}
		} else {
			plan.serverName = "virtual-tools"
		}
	}

	// Handle nil FunctionCall (already checked above, but guard for safety)
	if tc.FunctionCall == nil {
		plan.skipExecution = true
		return plan
	}

	// Empty tool name
	if tc.FunctionCall.Name == "" {
		v2Logger.Error("Empty tool name detected in tool call", nil,
			loggerv2.Int("turn", turn+1),
			loggerv2.String("arguments", toolerr.TruncateArgsForLog(tc.FunctionCall.Arguments)))

		feedbackMessage := generateEmptyToolNameFeedback(tc.FunctionCall.Arguments)
		v2Logger.Error(toolerr.Marker+" empty tool name", nil,
			loggerv2.String("layer", "agent_tool_loop_parallel"),
			loggerv2.String("session_id", a.sessionID))
		toolNameErrorEvent := events.NewToolCallErrorEvent(turn+1, "", "empty tool name", "", time.Since(conversationStartTime))
		toolNameErrorEvent.ToolCallID = tc.ID
		a.emitTypedEvent(ctx, toolNameErrorEvent)

		msg := llmtypes.MessageContent{
			Role:  llmtypes.ChatMessageTypeTool,
			Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: tc.ID, Name: tc.FunctionCall.Name, Content: feedbackMessage, IsError: true}},
		}
		plan.skipExecution = true
		plan.preErrorMessage = &msg
		return plan
	}

	// Parse arguments
	args, err := mcpclient.ParseToolArguments(tc.FunctionCall.Arguments)
	if err != nil {
		v2Logger.Error("Tool args parsing error", err)
		feedbackMessage := generateToolArgsParsingFeedback(tc.FunctionCall.Name, tc.FunctionCall.Arguments, err)
		v2Logger.Error(toolerr.Marker+" tool args parse failed", err,
			loggerv2.String("layer", "agent_tool_loop_parallel"),
			loggerv2.String("tool", tc.FunctionCall.Name),
			loggerv2.String("session_id", a.sessionID),
			loggerv2.String("args", toolerr.TruncateArgsForLog(tc.FunctionCall.Arguments)))
		toolArgsParsingErrorEvent := events.NewToolCallErrorEvent(turn+1, tc.FunctionCall.Name, fmt.Sprintf("parse tool args: %v", err), "", time.Since(conversationStartTime))
		toolArgsParsingErrorEvent.ToolCallID = tc.ID
		a.emitTypedEvent(ctx, toolArgsParsingErrorEvent)

		msg := llmtypes.MessageContent{
			Role:  llmtypes.ChatMessageTypeTool,
			Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: tc.ID, Name: tc.FunctionCall.Name, Content: feedbackMessage, IsError: true}},
		}
		plan.skipExecution = true
		plan.preErrorMessage = &msg
		return plan
	}
	plan.args = args

	plan.directTool, plan.isCustomTool = a.lookupDirectTool(tc.FunctionCall.Name)

	// Resolve only the client mapped to this tool.
	mappedServerName := ""
	hasMappedServer := false
	if a.toolToServer != nil {
		if mapped, ok := a.toolToServer[tc.FunctionCall.Name]; ok {
			if mapped == "custom" {
				// Compatibility marker only; the canonical registry above owns
				// direct-tool identity and execution.
			} else {
				plan.client, mappedServerName, hasMappedServer = a.mappedMCPClient(tc.FunctionCall.Name)
			}
		}
	}

	// Check for client requirement for non-custom, non-virtual tools
	if !plan.isCustomTool && !plan.isVirtual && plan.client == nil {
		if !hasMappedServer || mappedServerName == "" {
			feedbackMessage := a.unknownToolFeedback(tc.FunctionCall.Name)

			v2Logger.Error(toolerr.Marker+" tool not found", nil,
				loggerv2.String("layer", "agent_tool_loop_parallel"),
				loggerv2.String("tool", tc.FunctionCall.Name),
				loggerv2.String("session_id", a.sessionID),
				loggerv2.String("args", toolerr.TruncateArgsForLog(tc.FunctionCall.Arguments)))
			toolNotFoundEvent := events.NewToolCallErrorEvent(turn+1, tc.FunctionCall.Name, fmt.Sprintf("tool '%s' not found", tc.FunctionCall.Name), "", time.Since(conversationStartTime))
			toolNotFoundEvent.ToolCallID = tc.ID
			a.emitTypedEvent(ctx, toolNotFoundEvent)

			msg := llmtypes.MessageContent{
				Role:  llmtypes.ChatMessageTypeTool,
				Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: tc.ID, Name: tc.FunctionCall.Name, Content: feedbackMessage, IsError: true}},
			}
			plan.skipExecution = true
			plan.preErrorMessage = &msg
			return plan
		}

		onDemandClient, err := a.resolveOnDemandMCPClient(ctx, mappedServerName, v2Logger)
		if err == nil && onDemandClient == nil {
			err = fmt.Errorf("on-demand resolver returned no client")
		}
		if err != nil {
			v2Logger.Error("Failed to create on-demand connection",
				err,
				loggerv2.String("server", mappedServerName))
			conversationErrorEvent := events.NewConversationErrorEvent(getLastUserMessageForEvent(), fmt.Sprintf("failed to create on-demand connection for server %s: %v", mappedServerName, err), turn+1, "on_demand_connection_failed", time.Since(conversationStartTime))
			a.emitTypedEvent(ctx, conversationErrorEvent)
			plan.skipExecution = true
			msg := llmtypes.MessageContent{
				Role:  llmtypes.ChatMessageTypeTool,
				Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: tc.ID, Name: tc.FunctionCall.Name, Content: fmt.Sprintf("Error: failed to create on-demand connection for server %s: %v", mappedServerName, err), IsError: true}},
			}
			plan.preErrorMessage = &msg
			return plan
		}

		// Cache the exact mapped client so subsequent calls reuse it.
		a.clientsMu.Lock()
		if a.clients == nil {
			a.clients = make(map[string]mcpclient.ClientInterface)
		}
		a.clients[mappedServerName] = onDemandClient
		a.clientsMu.Unlock()
		plan.client = onDemandClient
	}

	// Determine tool timeout
	plan.toolTimeout = getToolExecutionTimeout(a)
	plan.hasNoTimeout = plan.toolTimeout <= 0
	if plan.isCustomTool {
		if plan.directTool.Timeout != -1 {
			if plan.directTool.Timeout == 0 {
				plan.hasNoTimeout = true
			} else if plan.directTool.Timeout > 0 {
				plan.hasNoTimeout = false
				plan.toolTimeout = plan.directTool.Timeout
			}
		}
	}

	// Determine tool type
	plan.toolType = "MCP"
	if plan.isVirtual {
		plan.toolType = "virtual"
	} else if plan.isCustomTool {
		plan.toolType = "custom"
	}

	return plan
}

// getLastUserMessageForEvent returns an empty string as a placeholder for event metadata.
// During parallel preparation, the actual lastUserMessage is not readily available,
// but it's only used for event metadata (not functional behavior).
func getLastUserMessageForEvent() string {
	return ""
}

// executeToolCall runs a single tool call and returns its result.
// This function is safe to call from a goroutine — it does NOT mutate shared state
// (messages slice, loop detector, event hierarchy). It only reads from the plan
// and calls the tool.
func executeToolCall(
	ctx context.Context,
	a *Agent,
	plan toolExecutionPlan,
	turn int,
	conversationStartTime time.Time,
	agentCtx context.Context,
) toolExecutionResult {

	v2Logger := a.logger
	tc := plan.toolCall
	result := toolExecutionResult{}

	// Create timeout context for tool execution
	var toolCtx context.Context
	var cancel context.CancelFunc
	if plan.hasNoTimeout {
		toolCtx = ctx
		cancel = func() {}
	} else {
		toolCtx, cancel = context.WithTimeout(ctx, plan.toolTimeout)
	}
	defer cancel()

	startTime := time.Now()

	// Log tool call
	argsJSON, _ := json.Marshal(plan.args)
	timeoutStr := plan.toolTimeout.String()
	if plan.hasNoTimeout {
		timeoutStr = "none (indefinite)"
	}
	v2Logger.Debug("🔧 [TOOL_CALL] Tool called (parallel)",
		loggerv2.String("tool_name", tc.FunctionCall.Name),
		loggerv2.String("tool_type", plan.toolType),
		loggerv2.String("server_name", plan.serverName),
		loggerv2.String("tool_call_id", tc.ID),
		loggerv2.Int("turn", turn+1),
		loggerv2.String("arguments", toolerr.TruncateArgsForLog(string(argsJSON))),
		loggerv2.String("timeout", timeoutStr))

	// Cache hit event
	if len(a.tracers) > 0 && plan.serverName != "" && plan.serverName != "virtual-tools" {
		connectionCacheHitEvent := events.NewCacheHitEvent(plan.serverName, fmt.Sprintf("unified_%s", plan.serverName), "unified_cache", 1, time.Duration(0))
		a.emitTypedEvent(ctx, connectionCacheHitEvent)
	}

	// Inject tool execution metadata into context
	toolCtx = context.WithValue(toolCtx, ToolExecutionAgentKey, a)
	toolCtx = context.WithValue(toolCtx, ToolExecutionTurnKey, turn+1)
	toolCtx = context.WithValue(toolCtx, ToolExecutionServerKey, plan.serverName)
	toolCtx = context.WithValue(toolCtx, ToolExecutionLLMConfigKey, a.getLLMModelConfig())

	// ─── Execute the tool ──────────────────────────────────────────────

	var mcpResult *mcp.CallToolResult
	var toolErr error
	actualToolName := a.realMCPToolName(tc.FunctionCall.Name, plan.serverName)

	if guarded := toolguard.Check(toolCtx, a.guardCall(tc.FunctionCall.Name, actualToolName, plan.serverName, plan.isCustomTool, plan.client, plan.args)); guarded.Stub {
		mcpResult = guarded.AsResult()
	} else if isVirtualTool(tc.FunctionCall.Name) {
		v2Logger.Debug("🔧 [TOOL_CALL] Executing virtual tool (parallel)",
			loggerv2.String("tool_name", tc.FunctionCall.Name))
		resultText, vtErr := a.handleVirtualTool(toolCtx, tc.FunctionCall.Name, plan.args)
		if vtErr != nil {
			mcpResult = &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: vtErr.Error()}},
			}
		} else {
			if resultText == "" {
				resultText = fmt.Sprintf("Tool '%s' executed successfully but returned no output.", tc.FunctionCall.Name)
			}
			mcpResult = &mcp.CallToolResult{
				IsError: false,
				Content: []mcp.Content{&mcp.TextContent{Text: resultText}},
			}
		}
	} else if plan.isCustomTool {
		resultText, ctErr := plan.directTool.Executor(toolCtx, plan.args)
		if ctErr != nil {
			mcpResult = &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: ctErr.Error()}}}
		} else {
			mcpResult = &mcp.CallToolResult{IsError: false, Content: []mcp.Content{&mcp.TextContent{Text: resultText}}}
		}
	} else {
		mcpResult, toolErr = callToolWithTimeoutWrapper(toolCtx, plan.client, actualToolName, plan.args, v2Logger, plan.serverName)
	}

	result.duration = time.Since(startTime)

	// Check for timeout
	if toolCtx.Err() == context.DeadlineExceeded {
		toolErr = fmt.Errorf("tool execution timed out after %s: %s", plan.toolTimeout.String(), tc.FunctionCall.Name)
	}

	// Handle tool execution errors
	if toolErr != nil {
		// Attempt error recovery
		errorRecoveryHandler := newErrorRecoveryHandler(a)
		recoveredResult, recoveredDuration, wasRecovered, recoveredErr := errorRecoveryHandler.HandleError(
			ctx, &tc, plan.serverName, toolErr, time.Now().Add(-result.duration), plan.isCustomTool, plan.isVirtual)

		if wasRecovered && recoveredErr == nil {
			mcpResult = recoveredResult
			result.duration = recoveredDuration
		} else {
			if wasRecovered {
				toolErr = recoveredErr
				result.duration = recoveredDuration
			}

			errorResultText := fmt.Sprintf("Tool execution failed - %v", toolErr)
			result.toolErr = toolErr
			result.resultText = errorResultText
			result.messages = []llmtypes.MessageContent{{
				Role:  llmtypes.ChatMessageTypeTool,
				Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: tc.ID, Name: tc.FunctionCall.Name, Content: errorResultText, IsError: true}},
			}}
			return result
		}
	}

	// Process result
	var resultText string
	if mcpResult != nil {
		resultText = mcpclient.ToolResultAsString(mcpResult)
		if _, failed := toolerr.CanonicalFailureForTool(tc.FunctionCall.Name, resultText); failed && !mcpResult.IsError {
			mcpResult.IsError = true
		}

		if resultText == "" && !mcpResult.IsError {
			resultText = fmt.Sprintf("Tool '%s' executed successfully but returned no output.", tc.FunctionCall.Name)
		}

		// Check for broken pipe in content
		if mcpclient.IsBrokenPipeInContent(resultText) {
			v2Logger.Info(fmt.Sprintf("🔧 [BROKEN PIPE DETECTED IN RESULT] Turn %d, Tool: %s, Server: %s", turn+1, tc.FunctionCall.Name, plan.serverName))
			errorRecoveryHandler := newErrorRecoveryHandler(a)
			fakeErr := fmt.Errorf("broken pipe detected in result: %s", resultText)
			recoveredResult, recoveredDuration, wasRecovered, recoveredErr := errorRecoveryHandler.HandleError(
				ctx, &tc, plan.serverName, fakeErr, time.Now().Add(-result.duration), plan.isCustomTool, plan.isVirtual)

			if wasRecovered && recoveredErr == nil {
				mcpResult = recoveredResult
				result.duration = recoveredDuration
				resultText = mcpclient.ToolResultAsString(mcpResult)
			}
		}

	} else {
		resultText = "Tool execution completed but no result returned"
	}

	result.result = mcpResult
	result.resultText = resultText
	result.messages = []llmtypes.MessageContent{{
		Role:  llmtypes.ChatMessageTypeTool,
		Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: tc.ID, Name: tc.FunctionCall.Name, Content: resultText, IsError: mcpResult != nil && mcpResult.IsError}},
	}}
	return result
}

// unknownToolFeedback builds the correction returned when the model calls a tool
// that does not exist.
//
// Feedback lists the tools actually available to this agent.
//
// Recovery text is not documentation. The model acts on it, so a wrong hint
// turns one failed call into several: this is the same shape as get_api_spec
// answering an unknown server by listing categories the prompt had told the
// agent were not addresses. Naming what this agent actually has makes the
// correction recoverable in one step.
func (a *Agent) unknownToolFeedback(requested string) string {
	available := make([]string, 0, len(a.filteredTools))
	for _, tool := range a.filteredTools {
		if tool.Function == nil || tool.Function.Name == "" {
			continue
		}
		if a.isToolAllowed(tool.Function.Name) {
			available = append(available, tool.Function.Name)
		}
	}
	sort.Strings(available)

	var b strings.Builder
	fmt.Fprintf(&b, "❌ Tool %q is not available in this system.\n\n", requested)
	if len(available) == 0 {
		// Worth stating plainly rather than printing an empty list: an agent with
		// no tools should report that it cannot proceed, not keep guessing names.
		b.WriteString("🔧 This agent has no callable tools. Do not retry with a different name — report that the required capability is unavailable.\n")
		return b.String()
	}
	// Bounded because a full surface can run to hundreds of names, and an
	// unreadable wall of text is its own kind of unhelpful hint.
	const maxListed = 60
	listed := available
	if len(listed) > maxListed {
		listed = listed[:maxListed]
	}
	fmt.Fprintf(&b, "🔧 Tools available to this agent (%d):\n- %s\n", len(available), strings.Join(listed, "\n- "))
	if len(available) > maxListed {
		fmt.Fprintf(&b, "…and %d more; see the tool index in the system prompt.\n", len(available)-maxListed)
	}
	b.WriteString("\n💡 Use one of the names above exactly, or report that the capability is unavailable.")
	return b.String()
}
