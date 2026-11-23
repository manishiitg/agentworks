// conversation.go
//
// This file contains the synchronous conversation logic for the Agent, including Ask, AskWithHistory, and generateContentWithRetry.
// These functions handle multi-turn LLM conversations, tool call execution, and error handling.
//
// Exported:
//   - Ask
//   - AskWithHistory
//   - generateContentWithRetry

package mcpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpcache"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/observability"
	"github.com/manishiitg/mcpagent/toolerr"
	"github.com/manishiitg/mcpagent/toolguard"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
)

// Generic tool execution context keys
// These are injected into context for all tool executions to provide metadata
// Tools can extract these values if needed (e.g., workspace tools for event emission)
// Using string keys ensures compatibility across packages
const (
	// ToolExecutionAgentKey is the context key for the agent executing the tool
	// Tools can use this to emit events or access agent functionality
	ToolExecutionAgentKey = "tool_execution_agent"
	// ToolExecutionTurnKey is the context key for the current conversation turn
	ToolExecutionTurnKey = "tool_execution_turn"
	// ToolExecutionServerKey is the context key for the server name providing the tool
	ToolExecutionServerKey = "tool_execution_server"
	// ToolExecutionLLMConfigKey is the context key for the agent's primary LLM configuration.
	// Tools like read_image use this to create a dedicated LLM client for analysis.
	// Value type: mcpagent.LLMModel (Provider, ModelID, APIKey, Region).
	ToolExecutionLLMConfigKey = "tool_execution_llm_config"
)

// getLogger returns the agent's logger (guaranteed to be non-nil)
func getLogger(a *Agent) loggerv2.Logger {
	// Agent logger is guaranteed to be non-nil in the new architecture
	return a.logger
}

// mappedMCPClient returns only the client owned by the server mapped to toolName.
func (a *Agent) mappedMCPClient(toolName string) (mcpclient.ClientInterface, string, bool) {
	if a.toolToServer == nil {
		return nil, "", false
	}

	serverName, mapped := a.toolToServer[toolName]
	if !mapped || serverName == "" || serverName == "custom" {
		return nil, serverName, mapped
	}

	a.clientsMu.RLock()
	client := a.clients[serverName]
	a.clientsMu.RUnlock()
	return client, serverName, true
}

func requireFunctionCall(tc llmtypes.ToolCall) (*llmtypes.FunctionCall, error) {
	if tc.FunctionCall == nil {
		return nil, fmt.Errorf("invalid tool call: nil function call")
	}
	return tc.FunctionCall, nil
}

func injectSteerMessages(ctx context.Context, a *Agent, messages []llmtypes.MessageContent, steerMsgs []queuedSteer, turn int, logMessage string) []llmtypes.MessageContent {
	for _, sm := range steerMsgs {
		messages = append(messages, llmtypes.MessageContent{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: sm.Text}},
		})
		// One row, recorded where the agent takes the message; a person's message carries its identity so
		// the chat matches the bubble it already shows (no second copy).
		a.emitTypedEvent(ctx, steerUserMessageEvent(a, turn+1, sm, "sent_to_cli"))
		getLogger(a).Info(logMessage, loggerv2.Int("turn", turn+1))
	}
	return messages
}

// steerUserMessageEvent is the user row of a queued message. A person's message carries the same metadata
// the server writes for a live message (message_id, client_message_id) so the chat matches its bubble.
func steerUserMessageEvent(a *Agent, turn int, sm queuedSteer, deliveryStatus string) *events.UserMessageEvent {
	event := events.NewUserMessageEvent(turn, sm.Text, "user")
	if sm.ID == "" && sm.ClientID == "" {
		return event
	}
	event.Metadata = map[string]interface{}{"source": "coding_agent_live_input", "provider": string(a.provider), "message_id": sm.ID, "delivery_status": deliveryStatus}
	if sm.ClientID != "" {
		event.Metadata["client_message_id"] = sm.ClientID
	}
	return event
}

// reportUnsentSteers settles messages still queued when a response stops. A person's message is recorded once
// as failed (its row offers resend); only unidentified ones need the separate notice.
func reportUnsentSteers(ctx context.Context, a *Agent, pending []queuedSteer) {
	var unrecorded []string
	for _, entry := range pending {
		if entry.ID == "" && entry.ClientID == "" {
			unrecorded = append(unrecorded, entry.Text)
			continue
		}
		event := steerUserMessageEvent(a, 0, entry, "failed")
		event.Metadata["confirmation"] = "failed"
		event.Metadata["delivery_error"] = "Not sent because the response stopped. Send it again."
		a.emitTypedEvent(ctx, event)
	}
	if len(unrecorded) > 0 {
		a.emitTypedEvent(ctx, events.NewConversationErrorEvent(strings.Join(unrecorded, "\n\n"), "Queued messages were not sent because this response stopped. Please send them again.", 0, "queued_input_not_sent", 0))
	}
}

func (a *Agent) annotateUnifiedCompletionEvent(event *events.UnifiedCompletionEvent) {
	if event == nil {
		return
	}
	if event.Metadata == nil {
		event.Metadata = make(map[string]interface{})
	}
	event.Metadata["provider"] = string(a.provider)
	event.Metadata["model_id"] = a.modelID
	event.SetProviderFailureDisplay()
	if isCodingCLIProvider(a.provider, a.modelID) {
		event.Metadata["coding_agent_terminal_format"] = true
	}
}

// finalAssistantTextFromCodingTrail returns the last textual assistant message
// reconstructed from a coding CLI's sidecar transcript.  For tmux providers,
// that transcript is the authoritative record of the completed turn; the pane
// capture is only a transport fallback and can be empty or contain stale UI.
//
// The same value must drive both the returned conversation result and the
// unified_completion event.  Otherwise raw terminal mode can show the coding
// CLI's final answer while formatted mode only receives an empty lifecycle
// completion card.
func finalAssistantTextFromCodingTrail(messages []llmtypes.MessageContent) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llmtypes.ChatMessageTypeAI {
			continue
		}
		var parts []string
		for _, part := range messages[i].Parts {
			switch text := part.(type) {
			case llmtypes.TextContent:
				if trimmed := strings.TrimSpace(text.Text); trimmed != "" {
					parts = append(parts, trimmed)
				}
			case *llmtypes.TextContent:
				if text != nil {
					if trimmed := strings.TrimSpace(text.Text); trimmed != "" {
						parts = append(parts, trimmed)
					}
				}
			}
		}
		if result := strings.TrimSpace(strings.Join(parts, "\n")); result != "" {
			return result
		}
	}
	return ""
}

// isVirtualTool checks if a tool name is a virtual tool
func isVirtualTool(toolName string) bool {
	// Check hardcoded virtual tools (includes all possible virtual tools)
	virtualTools := []string{
		"get_api_spec", // Code execution mode tool
	}
	for _, vt := range virtualTools {
		if vt == toolName {
			return true
		}
	}

	// Check if it's a custom tool (this will be checked in the calling function)
	return false
}

// getToolExecutionTimeout returns the tool execution timeout duration.
// A return value <= 0 means no agent-level tool timeout.
func getToolExecutionTimeout(a *Agent) time.Duration {
	// First check if agent has a specific timeout configured
	if a.toolTimeout > 0 {
		return a.toolTimeout
	}

	// Fall back to environment variable
	timeoutStr := os.Getenv("TOOL_EXECUTION_TIMEOUT")
	if timeoutStr == "" {
		return 0
	}

	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return 0
	}

	return timeout
}

// callToolWithTimeoutWrapper wraps MCP client CallTool with explicit timeout monitoring.
// This ensures timeouts work even if the underlying library doesn't properly respect context cancellation.
// The wrapper runs the call in a goroutine and monitors the context deadline explicitly.
func callToolWithTimeoutWrapper(
	ctx context.Context,
	client mcpclient.ClientInterface,
	toolName string,
	args map[string]interface{},
	logger loggerv2.Logger,
	serverName string,
) (*mcp.CallToolResult, error) {
	// Get deadline from context
	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		// No deadline, just call directly
		return client.CallTool(ctx, toolName, args)
	}

	// Calculate remaining time
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return nil, context.DeadlineExceeded
	}

	// Create result channels
	type result struct {
		res *mcp.CallToolResult
		err error
	}
	resultChan := make(chan result, 1)

	// Start the call in a goroutine with proper cleanup to prevent leaks
	// Use nested goroutine pattern to ensure this wrapper goroutine exits
	// even if client.CallTool blocks indefinitely and doesn't respect context cancellation
	go func() {
		// Create a done channel to signal when the actual call completes
		callDone := make(chan struct{})
		var res *mcp.CallToolResult
		var err error

		// Run the actual call in an inner goroutine
		go func() {
			defer close(callDone)
			res, err = client.CallTool(ctx, toolName, args)
		}()

		// Wait for either completion or context cancellation
		select {
		case <-callDone:
			// Call completed - try to send result (non-blocking if context expired)
			select {
			case resultChan <- result{res: res, err: err}:
				// Successfully sent result
			case <-ctx.Done():
				// Context expired while trying to send - exit without blocking
				// This prevents goroutine leak if function already returned
				return
			}
		case <-ctx.Done():
			// Context expired before call completed - exit immediately
			// Note: The inner client.CallTool goroutine may still be running,
			// but at least this wrapper goroutine exits cleanly
			return
		}
	}()

	startTime := time.Now()

	// Set up periodic progress logging for long-running calls (every 30 seconds)
	// Use time.AfterFunc instead of ticker for better efficiency - only schedules timers when needed
	// The context deadline will handle timeout automatically via ctx.Done()
	done := make(chan struct{})
	var nextLogTimer *time.Timer
	var scheduleNextLog func()

	scheduleNextLog = func() {
		nextLogTimer = time.AfterFunc(30*time.Second, func() {
			select {
			case <-done:
				// Call already completed, don't log or reschedule
				return
			default:
				// Still waiting, log progress and reschedule
				elapsed := time.Since(startTime)
				remaining := time.Until(deadline)
				logger.Debug("🔧 [TOOL_CALL] Still waiting for tool response",
					loggerv2.String("tool_name", toolName),
					loggerv2.String("server_name", serverName),
					loggerv2.String("elapsed", elapsed.String()),
					loggerv2.String("remaining", remaining.String()))
				// Reschedule next log if still waiting
				scheduleNextLog()
			}
		})
	}

	// Schedule the first progress log
	scheduleNextLog()
	defer func() {
		close(done)
		if nextLogTimer != nil {
			nextLogTimer.Stop()
		}
	}()

	for {
		select {
		case res := <-resultChan:
			// Call completed
			return res.res, res.err

		case <-ctx.Done():
			// Context cancelled or timed out
			logger.Warn("🔧 [TOOL_TIMEOUT] Context cancelled/timed out while waiting for tool call",
				loggerv2.String("tool_name", toolName),
				loggerv2.String("server_name", serverName),
				loggerv2.String("elapsed", time.Since(startTime).String()),
				loggerv2.Error(ctx.Err()))
			return nil, ctx.Err()
		}
	}
}

// ensureSystemPrompt ensures that the system prompt is included in the messages
func ensureSystemPrompt(a *Agent, messages []llmtypes.MessageContent) []llmtypes.MessageContent {
	return ensureSystemPromptForContext(context.Background(), a, messages)
}

func ensureSystemPromptForContext(ctx context.Context, a *Agent, messages []llmtypes.MessageContent) []llmtypes.MessageContent {
	// Always use the agent's current system prompt — it reflects the latest mode
	// (for example code execution) which may differ from a stale system
	// message carried over in conversation history from a previous turn.
	systemPrompt := a.outgoingSystemPromptForContext(ctx)

	systemMessage := llmtypes.MessageContent{
		Role:  llmtypes.ChatMessageTypeSystem,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: systemPrompt}},
	}

	// If the first message is already a system message, replace it
	if len(messages) > 0 && messages[0].Role == llmtypes.ChatMessageTypeSystem {
		messages[0] = systemMessage
		return messages
	}

	// If a system message exists elsewhere in the conversation, replace it
	for i, msg := range messages {
		if msg.Role == llmtypes.ChatMessageTypeSystem {
			messages[i] = systemMessage
			return messages
		}
	}

	// No system message found — prepend one
	return append([]llmtypes.MessageContent{systemMessage}, messages...)
}

// AskWithHistory runs an interaction using the provided message history (multi-turn conversation).
func askWithHistory(a *Agent, ctx context.Context, messages []llmtypes.MessageContent) (string, []llmtypes.MessageContent, error) {
	a.openSteerQueue()
	defer func() {
		reportUnsentSteers(context.WithoutCancel(ctx), a, a.closeSteerQueue())

	}()

	// Use agent's logger if available, otherwise use default
	v2Logger := a.logger
	v2Logger.Debug("Entered AskWithHistory", loggerv2.Int("message_count", len(messages)))
	if len(a.tracers) == 0 {
		a.tracers = []observability.Tracer{observability.NoopTracer{}}
	}
	if a.maxTurns == 0 {
		// Get default from environment variable, fallback to 500
		if envVal := os.Getenv("MAX_TURNS"); envVal != "" {
			if maxTurns, err := strconv.Atoi(envVal); err == nil && maxTurns > 0 {
				a.maxTurns = maxTurns
			} else {
				// Fallback to 500 if env var is invalid
				a.maxTurns = 500
			}
		} else {
			// Fallback to 500 if env var not set
			a.maxTurns = 500
		}
	}

	// Use the passed context for cancellation checks (not the agent's internal context)
	// This ensures we use the context that the caller wants us to respect
	agentCtx := ctx

	// Track conversation start time for duration calculation
	conversationStartTime := time.Now()
	log.Printf("[LATENCY_DEBUG] AskWithHistory started | messages=%d provider=%s model=%s",
		len(messages), a.provider, a.modelID)

	// ✅ CONTEXT-AWARE HIERARCHY: Initialize based on calling context
	// This ensures hierarchy reflects the actual calling context
	a.initializeHierarchyForContext(ctx)

	// Ensure system prompt is included in messages
	messages = ensureSystemPromptForContext(ctx, a, messages)

	// Log prompts to disk when LOG_AGENT_PROMPTS is enabled:
	// - Start: system prompt + user message (written now)
	// - End: tool calls + responses (written when function returns via defer)
	var promptLog *promptLogInfo
	startMsgCount := len(messages)
	if os.Getenv("LOG_AGENT_PROMPTS") == "true" {
		// Clear tool call log from previous turns
		a.toolCallLogMu.Lock()
		a.toolCallLog = nil
		a.toolCallLogMu.Unlock()

		promptLog = logFinalPrompts(a, messages)
		defer func() {
			a.toolCallLogMu.Lock()
			toolLog := make([]string, len(a.toolCallLog))
			copy(toolLog, a.toolCallLog)
			a.toolCallLogMu.Unlock()
			logConversationEnd(promptLog, messages, startMsgCount, toolLog)
		}()
	}

	// NEW: Set current query for hierarchy tracking (will be set later when lastUserMessage is extracted)

	// Add cache validation AFTER the agent is fully initialized
	if len(a.tracers) > 0 && len(a.clients) > 0 {
		// Debug: Log what's in the clients map
		clientKeys := make([]string, 0, len(a.clients))
		for k := range a.clients {
			clientKeys = append(clientKeys, k)
		}

		// Get actual server information for better cache events
		serverNames := make([]string, 0, len(a.clients))
		for serverName := range a.clients {
			serverNames = append(serverNames, serverName)
		}

		// Emit comprehensive cache validation event for all servers
		serverStatus := make(map[string]mcpcache.ServerCacheStatus)
		for serverName := range a.clients {
			serverStatus[serverName] = mcpcache.ServerCacheStatus{
				ServerName: serverName,
				Status:     "validation",
				ToolsCount: len(a.tools),
			}
		}

		// Emit cache operation start event through agent event system (frontend visible)
		cacheStartEvent := events.NewCacheOperationStartEvent("all-servers", "conversation_cache_validation")
		a.emitTypedEvent(ctx, cacheStartEvent)

		// Also emit to tracers for observability (Langfuse, etc.)
		mcpcache.EmitComprehensiveCacheEvent(
			a.tracers,
			"validation",
			"conversation_cache_validation",
			serverNames,
			nil, // No result available here
			serverStatus,
			time.Duration(0), // No connection time available
			time.Duration(0), // No cache time available
			nil,              // No errors
		)

		// Debug: Log the comprehensive cache event structure
		v2Logger.Debug("Comprehensive cache event emitted")

	}

	// Emit user message event for the current conversation
	// Extract the last user message (latest turn) - this is the message from the current turn
	// In multi-turn conversations, we want the latest user message, not the first one
	var lastUserMessage string

	// CRITICAL: The last message in the array should be the current user query
	// Check if the last message is a user message first (most common case)
	if len(messages) > 0 {
		lastMsg := messages[len(messages)-1]
		if lastMsg.Role == llmtypes.ChatMessageTypeHuman {
			// Get the text content from the last message (current query)
			for _, part := range lastMsg.Parts {
				if textPart, ok := part.(llmtypes.TextContent); ok {
					lastUserMessage = textPart.Text
					break
				}
			}
		}
	}

	// Fallback: If last message is not a user message, iterate backwards to find the most recent user message
	// This handles edge cases where there might be system messages or other message types at the end
	if lastUserMessage == "" {
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == llmtypes.ChatMessageTypeHuman {
				// Get the text content from the message
				for _, part := range messages[i].Parts {
					if textPart, ok := part.(llmtypes.TextContent); ok {
						lastUserMessage = textPart.Text
						break
					}
				}
				break
			}
		}
	}

	// Use last user message (latest turn) if available, otherwise use default
	var userMessageForEvent string
	if lastUserMessage != "" {
		userMessageForEvent = lastUserMessage
	} else {
		userMessageForEvent = "conversation_with_history"
	}

	// Start the agent session event.
	a.startAgentSession(ctx)

	// Emit user message event - this will appear in basic events (user_message is not in ADVANCED_MODE_EVENTS)
	userMessageEvent := events.NewUserMessageEvent(0, userMessageForEvent, "user")
	a.emitTypedEvent(ctx, userMessageEvent)
	v2Logger.Debug("🔄 Emitted user_message event for first user message",
		loggerv2.String("content", userMessageForEvent),
		loggerv2.Int("content_length", len(userMessageForEvent)))

	serverList := strings.Join(a.servers, ",")
	log.Printf("[LATENCY_DEBUG] T+%dms | Pre-conversation setup done (cache validation, system prompt, user message) | servers=%s",
		time.Since(conversationStartTime).Milliseconds(), serverList)

	// Events are now emitted directly to tracers (no event dispatcher)

	// Generate trace ID for this conversation
	traceID := events.GenerateEventID()

	// Store trace ID for correlation
	agentStartEventID := traceID
	effectivePrompt := a.outgoingSystemPromptForContext(ctx)

	// Metadata for conversation tracking (used in events)
	conversationMetadata := map[string]interface{}{
		"system_prompt":   effectivePrompt,
		"tools_count":     len(a.tools),
		"agent_mode":      string(a.agentMode),
		"model_id":        a.modelID,
		"provider":        string(a.provider),
		"max_turns":       a.maxTurns,
		"temperature":     a.temperature,
		"tool_choice":     a.toolChoice,
		"servers":         serverList,
		"conversation_id": fmt.Sprintf("conv_%d", time.Now().Unix()),
		"start_time":      conversationStartTime.Format(time.RFC3339),
	}

	// Use conversationMetadata to avoid unused variable error
	_ = conversationMetadata

	// Emit conversation start event with correlation (child of agent start)
	conversationStartEvent := events.NewConversationStartEventWithCorrelation(lastUserMessage, effectivePrompt, len(a.tools), serverList, traceID, agentStartEventID)
	a.emitTypedEvent(ctx, conversationStartEvent)

	// Store conversation start event ID for correlation
	// conversationStartEventID := conversationStartEvent.EventID
	// Metadata for processing tracking

	// Reset filtered tools at the start of each conversation to apply the current allow list.
	a.filteredTools = a.applyToolAllowList(a.tools)
	v2Logger.Debug("🔧 Using registered tools after allow-list filtering",
		loggerv2.Int("tools_count", len(a.tools)),
		loggerv2.Int("filtered_count", len(a.filteredTools)))
	toolNames := make([]string, 0, len(a.filteredTools))
	for _, t := range a.filteredTools {
		if t.Function != nil {
			toolNames = append(toolNames, t.Function.Name)
		}
	}
	v2Logger.Debug("🔧 Available tools", loggerv2.Any("tools", toolNames))

	// The filtered set selected during pre-call setup is what the LLM will see.

	systemPromptEvent := events.NewSystemPromptEventWithTokens(effectivePrompt, 0, 0)
	a.emitTypedEvent(ctx, systemPromptEvent)

	// Loop detection: track recent tool calls and responses to detect infinite loops
	loopDetector := NewToolLoopDetector(DefaultLoopDetectionThreshold)

	var lastResponse string
	for turn := 0; ; turn++ {
		if a.maxTurns > 0 && turn >= a.maxTurns {
			break
		}

		// Extract the last message from the conversation (could be user, assistant, or tool)
		var lastMessage string

		if len(messages) > 0 {
			lastMsg := messages[len(messages)-1]

			for _, part := range lastMsg.Parts {
				if textPart, ok := part.(llmtypes.TextContent); ok {
					lastMessage = textPart.Text
					break
				} else if toolResp, ok := part.(llmtypes.ToolCallResponse); ok {
					lastMessage = toolResp.Content
					break
				} else if toolCall, ok := part.(llmtypes.ToolCall); ok {
					lastMessage = fmt.Sprintf("Tool call: %s", toolCall.FunctionCall.Name)
					break
				}
			}
		}

		// If no message found, use the last user message as fallback
		if lastMessage == "" {
			lastMessage = lastUserMessage
		}

		// Check for context cancellation at the start of each turn
		if agentCtx.Err() != nil {
			v2Logger.Debug("Context cancelled at start of turn",
				loggerv2.Int("turn", turn+1),
				loggerv2.Error(agentCtx.Err()),
				loggerv2.String("duration", time.Since(conversationStartTime).String()))
			return "", messages, fmt.Errorf("conversation cancelled: %w", agentCtx.Err())
		}

		// Use the current messages that include tool results from previous turns
		llmMessages := messages

		// Track start time for duration calculation
		llmStartTime := time.Now()
		log.Printf("[LATENCY_DEBUG] Turn %d | T+%dms | Preparing LLM call | messages=%d tools=%d",
			turn+1, time.Since(conversationStartTime).Milliseconds(), len(llmMessages), len(a.filteredTools))

		opts := []llmtypes.CallOption{}
		if !llm.IsO3O4Model(a.modelID) {
			opts = append(opts, llmtypes.WithTemperature(a.temperature))
		}
		// The bridge-only shell disable belongs to mcp_only. In Full CLI the Codex
		// integration options keep the shell on; disabling it here as well made the
		// adapter union both lists, so native mode never got its shell or subagents.
		if a.provider == "codex-cli" && !a.nativeCodingToolsEnabled() {
			opts = append(opts, codexcli.WithDisableShellTool())
		}
		opts = a.appendCodingAgentInteractiveOptions(opts)

		// Use proper LLM function calling via llmtypes.WithTools()
		// Use the pre-filtered tools that were determined at conversation start
		if len(a.filteredTools) > 0 {
			// Tools are already normalized during conversion in ToolsAsLLM() and cache loading
			// No need for extra normalization here since langchaingo bug is fixed
			opts = append(opts, llmtypes.WithTools(a.filteredTools))
			if toolChoiceOpt := ConvertToolChoice(a.toolChoice); toolChoiceOpt != nil {
				opts = append(opts, llmtypes.WithToolChoice(toolChoiceOpt))
			}
		}
		toolNames := make([]string, len(a.filteredTools))
		for i, tool := range a.filteredTools {
			toolNames[i] = tool.Function.Name
		}

		// Emit conversation turn event RIGHT BEFORE LLM call to show exactly what's being sent to the LLM
		// This happens right before the LLM call, so it reflects the actual messages sent

		// Debug: Verify compacted messages are in llmMessages
		compactedInLLMMessages := 0
		for _, msg := range llmMessages {
			if msg.Role == llmtypes.ChatMessageTypeTool {
				for _, part := range msg.Parts {
					if tr, ok := part.(llmtypes.ToolCallResponse); ok {
						if strings.Contains(tr.Content, "has been saved to:") || strings.Contains(tr.Content, "tool_output_folder") {
							compactedInLLMMessages++
							// Log first compacted message as sample
							if compactedInLLMMessages == 1 {
								contentPreview := tr.Content
								if len(contentPreview) > 200 {
									contentPreview = contentPreview[:200] + "..."
								}
								v2Logger.Info("🔍 [CONVERSATION_TURN] Sample compacted message in llmMessages",
									loggerv2.String("tool_name", tr.Name),
									loggerv2.String("content_preview", contentPreview))
							}
						}
					}
				}
			}
		}
		v2Logger.Info("🔍 [CONVERSATION_TURN] Messages being sent to LLM and event",
			loggerv2.Int("total_messages", len(llmMessages)),
			loggerv2.Int("compacted_messages_found", compactedInLLMMessages))

		tools := events.ConvertToolsToToolInfo(a.filteredTools, a.toolToServer)
		conversationTurnEvent := events.NewConversationTurnEvent(turn+1, lastMessage, len(llmMessages), false, 0, tools, llmMessages)
		a.emitTypedEvent(ctx, conversationTurnEvent)

		// NEW: Start LLM generation for hierarchy tracking
		a.startLLMGeneration(ctx)

		// Use GenerateContentWithRetry for bounded same-model retry handling
		log.Printf("[LATENCY_DEBUG] Turn %d | T+%dms | Sending to LLM API | provider=%s model=%s",
			turn+1, time.Since(conversationStartTime).Milliseconds(), a.provider, a.modelID)
		resp, usage, genErr := generateContentWithRetry(a, ctx, llmMessages, opts, turn)
		log.Printf("[LATENCY_DEBUG] Turn %d | T+%dms | LLM API responded | llm_duration=%dms err=%v",
			turn+1, time.Since(conversationStartTime).Milliseconds(), time.Since(llmStartTime).Milliseconds(), genErr)

		// Capture provider-specific session IDs for --resume on next turn
		extractCodingAgentSessionIDs(a, resp)

		// NEW: End LLM generation for hierarchy tracking
		if resp != nil && len(resp.Choices) > 0 {
			// 🔧 DEBUG: Log token usage for this LLM call
			// Use unified extraction from multi-llm-provider-go
			cacheTokens, thoughtsTokens, reasoningTokens := extractAllTokenTypes(resp)
			// Note: We no longer estimate tokens - we only use actual values from LLM responses
			// The actual token count is in usage.InputTokens (from LLM response)

			v2Logger.Info("🔧 [TOKEN_USAGE] LLM call token usage",
				loggerv2.Int("turn", turn+1),
				loggerv2.String("model", a.modelID),
				loggerv2.Int("input_tokens", usage.InputTokens),
				loggerv2.Int("output_tokens", usage.OutputTokens),
				loggerv2.Int("total_tokens", usage.TotalTokens),
				loggerv2.Int("cache_tokens", cacheTokens),
				loggerv2.Int("thoughts_tokens", thoughtsTokens),
				loggerv2.Int("reasoning_tokens", reasoningTokens),
				loggerv2.Int("tool_calls", len(resp.Choices[0].ToolCalls)),
				loggerv2.String("duration", time.Since(llmStartTime).String()))

			a.endLLMGeneration(ctx, resp.Choices[0].Content, turn+1, len(resp.Choices[0].ToolCalls), time.Since(llmStartTime), events.UsageMetrics{
				PromptTokens:     usage.InputTokens,
				CompletionTokens: usage.OutputTokens,
				TotalTokens:      usage.TotalTokens,
			}, resp)

			// No-op unless a caller opted in via withConversationSink. Builds
			// its own history-plus-this-reply copy rather than reading
			// `messages` directly: at this point in the control flow the
			// assistant's reply has NOT yet been appended to `messages` on
			// every branch below (tool-call loops append it much later, in
			// several different places) — recording the pre-append slice
			// silently dropped the very last (and often only new) exchange,
			// so a resumed conversation had the question but never the
			// answer. Real bug, caught by the resume half of
			// TestConversationRecordingWritesRealTurnData actually failing.
			if a.conversationSink != nil {
				recordMessages := messages
				if resp.Choices[0].Content != "" {
					recordMessages = append(append([]llmtypes.MessageContent{}, messages...), llmtypes.MessageContent{
						Role:  llmtypes.ChatMessageTypeAI,
						Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: resp.Choices[0].Content}},
					})
				}
				a.recordConversationTurn(turn+1, time.Since(llmStartTime), recordMessages, usage.InputTokens, usage.OutputTokens, usage.TotalTokens, cacheTokens, reasoningTokens)
			}
		}

		// Check for context cancellation after LLM generation
		if agentCtx.Err() != nil {
			v2Logger.Debug("Context cancelled after LLM generation",
				loggerv2.Int("turn", turn+1),
				loggerv2.Error(agentCtx.Err()),
				loggerv2.String("duration", time.Since(conversationStartTime).String()))
			cancellationEvent := events.NewContextCancelledEvent(
				turn+1,
				agentCtx.Err().Error(),
				time.Since(conversationStartTime),
			)
			a.emitTypedEvent(ctx, cancellationEvent)
			return "", messages, fmt.Errorf("conversation cancelled after LLM generation: %w", agentCtx.Err())
		}

		if genErr != nil {
			if genErr != nil {
				// Check for context cancellation FIRST - distinguish cancellations from errors
				if isContextCanceledError(genErr) || ctx.Err() != nil {
					v2Logger.Debug("Context cancelled during LLM generation",
						loggerv2.Int("turn", turn+1),
						loggerv2.Error(genErr),
						loggerv2.String("duration", time.Since(conversationStartTime).String()))
					// Emit context cancellation event instead of error events
					cancellationEvent := events.NewContextCancelledEvent(
						turn+1,
						genErr.Error(),
						time.Since(conversationStartTime),
					)
					a.emitTypedEvent(ctx, cancellationEvent)
					return "", messages, fmt.Errorf("conversation cancelled: %w", genErr)
				}

				// Tmux-loss continuation errors are infrastructure failures; the session
				// may still be alive (observable as state=completed). Suppress user-visible
				// error events and let the orchestrator decide based on final session state.
				if !isTmuxLossContinuationError(genErr) {
					llmErrorEvent := events.NewLLMGenerationErrorEvent(turn+1, a.modelID, genErr.Error(), time.Since(llmStartTime))
					a.emitTypedEvent(ctx, llmErrorEvent)

					// The person sees plain words for a rejected API key; the raw provider text stays in the
					// generation-error event above and in the returned error.
					shown := genErr.Error()
					if plain := plainAuthFailureMessage(string(a.provider), a.sessionID, genErr); plain != "" {
						shown = plain
					}
					conversationErrorEvent := events.NewConversationErrorEvent(lastUserMessage, shown, turn+1, "conversation_error", time.Since(conversationStartTime))
					conversationErrorEvent.Metadata = events.ProviderFailureMetadata(genErr)
					a.emitTypedEvent(ctx, conversationErrorEvent)
				}

				return "", messages, fmt.Errorf("llm error: %w", genErr)
			}
		}
		if resp == nil || resp.Choices == nil || len(resp.Choices) == 0 {

			// 🎯 FIX: End the trace for error cases - replaced with event emission
			conversationErrorEvent := events.NewConversationErrorEvent(lastUserMessage, "no response choices returned", turn+1, "no_choices", time.Since(conversationStartTime))
			a.emitTypedEvent(ctx, conversationErrorEvent)

			return "", messages, fmt.Errorf("no response choices returned")
		}

		choice := resp.Choices[0]
		lastResponse = choice.Content

		// Log empty response as warning
		if len(choice.Content) == 0 && len(choice.ToolCalls) == 0 {
			v2Logger.Warn("LLM Response is empty", loggerv2.Int("turn", turn+1))
		}

		// LLM generation end event is already emitted by EndLLMGeneration() method above

		// For ReAct agents, reasoning is finalized in ProcessChunk when completion patterns are detected
		// No need to call FinalizeReasoning as it's handled automatically

		// Token usage is already included in the LLMGenerationEndEvent above

		if len(choice.ToolCalls) > 0 {

			// 🔧 FIX: Separate text content and tool calls into different messages
			// Gemini API has issues when a model message contains both TextContent and ToolCall parts.
			// We create separate messages to avoid this issue.

			// 1. If there's text content, append it as a separate AI message
			if choice.Content != "" {
				messages = append(messages, llmtypes.MessageContent{
					Role:  llmtypes.ChatMessageTypeAI,
					Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: choice.Content}},
				})
			}

			// 2. Append tool calls as a separate AI message (without text)
			// Include read_image in tool call message so LLM knows it called the tool
			toolCallParts := make([]llmtypes.ContentPart, 0, len(choice.ToolCalls))
			for _, tc := range choice.ToolCalls {
				if tc.FunctionCall != nil {
					toolCallParts = append(toolCallParts, tc)
				}
			}
			// Add tool call message if there are any tool calls
			if len(toolCallParts) > 0 {
				messages = append(messages, llmtypes.MessageContent{
					Role:  llmtypes.ChatMessageTypeAI,
					Parts: toolCallParts,
				})
			}

			// 2. For each tool call, execute and append the tool result as a new message
			// Use parallel execution when enabled and there are multiple tool calls
			toolDispatchMode := "sequential"
			if a.enableParallelToolExecution && len(choice.ToolCalls) > 1 {
				toolDispatchMode = "parallel"
			}
			v2Logger.Info(fmt.Sprintf("⏱️  TOOL DISPATCH START - Time: %s, Count: %d, Mode: %s, Turn: %d",
				time.Now().Format(time.RFC3339), len(choice.ToolCalls), toolDispatchMode, turn+1))
			if a.enableParallelToolExecution && len(choice.ToolCalls) > 1 {
				var parallelErr error
				messages, parallelErr = executeToolCallsParallel(ctx, a, choice.ToolCalls, messages, turn, traceID, conversationStartTime, lastUserMessage, loopDetector, agentCtx)
				if parallelErr != nil {
					return "", messages, parallelErr
				}
				// Drain and inject any pending steer messages from the user
				if steerMsgs := a.drainSteerMessages(); len(steerMsgs) > 0 {
					messages = injectSteerMessages(ctx, a, messages, steerMsgs, turn, "Injected steer message after parallel tool execution")
				}
				// After parallel execution, continue to next turn
				continue
			}

			// Sequential execution (default path, or single tool call)
			for i, tc := range choice.ToolCalls {
				functionCall, err := requireFunctionCall(tc)
				if err != nil {
					v2Logger.Warn("Tool call has nil FunctionCall", loggerv2.Int("tool_call_index", i+1))
					v2Logger.Error("AskWithHistory Early return: invalid tool call: nil function call", nil)

					conversationErrorEvent := events.NewConversationErrorEvent(lastUserMessage, "invalid tool call: nil function call", turn+1, "invalid_tool_call", time.Since(conversationStartTime))
					a.emitTypedEvent(ctx, conversationErrorEvent)

					return "", messages, err
				}

				// Determine server name for tool call events
				serverName := a.toolToServer[functionCall.Name]
				if isVirtualTool(functionCall.Name) {
					// For get_api_spec, extract the actual server_name from arguments
					// so error events show the correct server, making debugging easier.
					if functionCall.Name == "get_api_spec" && functionCall.Arguments != "" {
						var args map[string]interface{}
						if err := json.Unmarshal([]byte(functionCall.Arguments), &args); err == nil {
							if srvName, ok := args["server_name"].(string); ok && srvName != "" {
								serverName = srvName
							} else {
								serverName = "virtual-tools"
							}
						} else {
							serverName = "virtual-tools"
						}
					} else {
						serverName = "virtual-tools"
					}
				}

				// Emit tool call start event using typed event data with correlation
				toolStartEvent := events.NewToolCallStartEventWithCorrelation(turn+1, functionCall.Name, events.ToolParams{
					Arguments: functionCall.Arguments,
				}, serverName, traceID, traceID) // Using traceID for both traceID and parentID correlation
				toolStartEvent.ToolCallID = tc.ID

				a.emitTypedEvent(ctx, toolStartEvent)

				// 🔧 ENHANCED: Check for empty tool name and provide feedback to LLM for self-correction
				if tc.FunctionCall.Name == "" {
					v2Logger.Error("AskWithHistory: Empty tool name detected in tool call", nil,
						loggerv2.Int("turn", turn+1),
						loggerv2.String("arguments", toolerr.TruncateArgsForLog(tc.FunctionCall.Arguments)))

					// Generate feedback message for empty tool name
					feedbackMessage := generateEmptyToolNameFeedback(tc.FunctionCall.Arguments)

					// Emit tool call error event for observability (after tool start event)
					v2Logger.Error(toolerr.Marker+" empty tool name", nil,
						loggerv2.String("layer", "agent_tool_loop"),
						loggerv2.String("session_id", a.sessionID))
					toolNameErrorEvent := events.NewToolCallErrorEvent(turn+1, "", "empty tool name", "", time.Since(conversationStartTime))
					toolNameErrorEvent.ToolCallID = tc.ID
					a.emitTypedEvent(ctx, toolNameErrorEvent)

					// Add feedback to conversation so LLM can correct itself
					toolName := ""
					if tc.FunctionCall != nil {
						toolName = tc.FunctionCall.Name
					}
					messages = append(messages, llmtypes.MessageContent{
						Role:  llmtypes.ChatMessageTypeTool,
						Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: tc.ID, Name: toolName, Content: feedbackMessage, IsError: true}},
					})

					continue
				}
				args, err := mcpclient.ParseToolArguments(tc.FunctionCall.Arguments)
				if err != nil {
					v2Logger.Error("AskWithHistory Tool args parsing error", err)

					// 🔧 ENHANCED: Instead of failing, provide feedback to LLM for self-correction
					feedbackMessage := generateToolArgsParsingFeedback(tc.FunctionCall.Name, tc.FunctionCall.Arguments, err)

					// Emit tool call error event for observability
					v2Logger.Error(toolerr.Marker+" tool args parse failed", err,
						loggerv2.String("layer", "agent_tool_loop"),
						loggerv2.String("tool", tc.FunctionCall.Name),
						loggerv2.String("session_id", a.sessionID),
						loggerv2.String("args", toolerr.TruncateArgsForLog(tc.FunctionCall.Arguments)))
					toolArgsParsingErrorEvent := events.NewToolCallErrorEvent(turn+1, tc.FunctionCall.Name, fmt.Sprintf("parse tool args: %v", err), "", time.Since(conversationStartTime))
					toolArgsParsingErrorEvent.ToolCallID = tc.ID
					a.emitTypedEvent(ctx, toolArgsParsingErrorEvent)

					// Add feedback to conversation so LLM can correct itself
					messages = append(messages, llmtypes.MessageContent{
						Role:  llmtypes.ChatMessageTypeTool,
						Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: tc.ID, Name: tc.FunctionCall.Name, Content: feedbackMessage, IsError: true}},
					})

					continue
				}

				// Direct tools do not need MCP clients. Resolve them from the same
				// canonical record later used for timeout and execution.
				directTool, isCustomTool := a.lookupDirectTool(tc.FunctionCall.Name)
				v2Logger.Debug(fmt.Sprintf("🔧 [TOOL_LOOKUP] Tool '%s' direct=%v (canonical registry has %d direct tools)", tc.FunctionCall.Name, isCustomTool, len(a.directToolSnapshot())))

				// Check if it's a virtual tool
				isVirtual := isVirtualTool(tc.FunctionCall.Name)
				if isVirtual {
					v2Logger.Debug(fmt.Sprintf("🔧 [TOOL_LOOKUP] Tool '%s' identified as virtual tool", tc.FunctionCall.Name))
				}

				var client mcpclient.ClientInterface
				mappedServerName := ""
				hasMappedServer := false
				if a.toolToServer != nil {
					if mapped, ok := a.toolToServer[tc.FunctionCall.Name]; ok {
						v2Logger.Debug(fmt.Sprintf("🔧 [TOOL_LOOKUP] Tool '%s' mapped to server '%s' in toolToServer", tc.FunctionCall.Name, mapped))
						if mapped == "custom" {
							// Compatibility projection only. Direct-tool identity comes
							// from the canonical registry above.
							v2Logger.Debug(fmt.Sprintf("🔧 [TOOL_LOOKUP] Tool '%s' has the custom routing marker; canonical direct=%v", tc.FunctionCall.Name, isCustomTool))
						} else {
							client, mappedServerName, hasMappedServer = a.mappedMCPClient(tc.FunctionCall.Name)
							if client != nil {
								v2Logger.Debug(fmt.Sprintf("🔧 [TOOL_LOOKUP] Found client for tool '%s' from server '%s'", tc.FunctionCall.Name, mapped))
							} else {
								v2Logger.Debug(fmt.Sprintf("🔧 [TOOL_LOOKUP] Server '%s' mapped for tool '%s' but no client found in Clients map", mapped, tc.FunctionCall.Name))
							}
						}
					} else {
						v2Logger.Debug(fmt.Sprintf("🔧 [TOOL_LOOKUP] Tool '%s' not found in toolToServer mapping (map has %d entries)", tc.FunctionCall.Name, len(a.toolToServer)))
					}
				} else {
					v2Logger.Debug(fmt.Sprintf("🔧 [TOOL_LOOKUP] toolToServer map is nil for tool '%s'", tc.FunctionCall.Name))
				}

				// Only check for client errors for non-custom tools and non-virtual tools
				if !isCustomTool && !isVirtual && client == nil {
					v2Logger.Debug(fmt.Sprintf("🔧 [TOOL_LOOKUP] Tool '%s' requires client but none found (isCustomTool=%v, isVirtual=%v, client=nil)", tc.FunctionCall.Name, isCustomTool, isVirtual))
					v2Logger.Debug(fmt.Sprintf("🔧 [TOOL_LOOKUP] Attempting on-demand connection for tool '%s'", tc.FunctionCall.Name))

					// Create a connection on demand for the mapped server even when other
					// servers already have active clients.
					if !hasMappedServer || mappedServerName == "" {
						// Calculate counts for logging
						customToolsCount := len(a.directToolSnapshot())
						toolToServerCount := 0
						if a.toolToServer != nil {
							toolToServerCount = len(a.toolToServer)
						}
						v2Logger.Warn(fmt.Sprintf("🔧 [TOOL_LOOKUP] Tool '%s' not mapped to any server. isCustomTool=%v, isVirtual=%v, canonical registry has %d direct tools, toolToServer has %d entries",
							tc.FunctionCall.Name, isCustomTool, isVirtual, customToolsCount, toolToServerCount))
						v2Logger.Warn(fmt.Sprintf("[AGENT DEBUG] AskWithHistory Turn %d: Tool '%s' not mapped to any server. Providing feedback to LLM.", turn+1, tc.FunctionCall.Name))

						// Generate helpful feedback instead of failing
						feedbackMessage := fmt.Sprintf("❌ Tool '%s' is not available in this system.\n\n🔧 Available tools include:\n- MCP server tools (check system prompt for full list)\n\n💡 Please use one of the available tools listed above.", tc.FunctionCall.Name)

						// Emit tool call error event for observability
						v2Logger.Error(toolerr.Marker+" tool not found", nil,
							loggerv2.String("layer", "agent_tool_loop"),
							loggerv2.String("tool", tc.FunctionCall.Name),
							loggerv2.String("session_id", a.sessionID),
							loggerv2.String("args", toolerr.TruncateArgsForLog(tc.FunctionCall.Arguments)))
						toolNotFoundEvent := events.NewToolCallErrorEvent(turn+1, tc.FunctionCall.Name, fmt.Sprintf("tool '%s' not found", tc.FunctionCall.Name), "", time.Since(conversationStartTime))
						toolNotFoundEvent.ToolCallID = tc.ID
						a.emitTypedEvent(ctx, toolNotFoundEvent)

						// Add feedback to conversation so LLM can correct itself
						messages = append(messages, llmtypes.MessageContent{
							Role:  llmtypes.ChatMessageTypeTool,
							Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: tc.ID, Name: tc.FunctionCall.Name, Content: feedbackMessage, IsError: true}},
						})

						continue
					}

					onDemandClient, err := a.resolveOnDemandMCPClient(ctx, mappedServerName, v2Logger)
					if err != nil {
						v2Logger.Error("AskWithHistory Early return: failed to create on-demand connection",
							err,
							loggerv2.String("server", mappedServerName))
						conversationErrorEvent := events.NewConversationErrorEvent(lastUserMessage, fmt.Sprintf("failed to create on-demand connection for server %s: %v", mappedServerName, err), turn+1, "on_demand_connection_failed", time.Since(conversationStartTime))
						a.emitTypedEvent(ctx, conversationErrorEvent)
						return "", messages, fmt.Errorf("failed to create on-demand connection for server %s: %w", mappedServerName, err)
					}

					// Store the on-demand client back into a.Clients so subsequent tool calls
					// reuse this connection instead of spawning a new MCP process each time.
					// Without this, every call to this server would create a duplicate connection
					// (e.g. many duplicate subprocesses for a single workflow).
					a.clientsMu.Lock()
					if a.clients == nil {
						a.clients = make(map[string]mcpclient.ClientInterface)
					}
					a.clients[mappedServerName] = onDemandClient
					a.clientsMu.Unlock()

					// Use the on-demand client
					client = onDemandClient
				}

				// Check for context cancellation before tool execution
				if agentCtx.Err() != nil {
					v2Logger.Debug("Context cancelled before tool execution",
						loggerv2.Int("turn", turn+1),
						loggerv2.String("tool_name", tc.FunctionCall.Name),
						loggerv2.Error(agentCtx.Err()),
						loggerv2.String("duration", time.Since(conversationStartTime).String()))
					// Emit context cancellation event instead of just returning error
					cancellationEvent := events.NewContextCancelledEvent(
						turn+1,
						fmt.Sprintf("cancelled before tool execution: %s", tc.FunctionCall.Name),
						time.Since(conversationStartTime),
					)
					a.emitTypedEvent(ctx, cancellationEvent)
					return "", messages, fmt.Errorf("conversation cancelled before tool execution: %w", agentCtx.Err())
				}

				// Create timeout context for tool execution
				// Check if this is a custom tool with a per-tool timeout
				toolTimeout := getToolExecutionTimeout(a)
				hasNoTimeout := toolTimeout <= 0
				if isCustomTool {
					if directTool.Timeout != -1 {
						if directTool.Timeout == 0 {
							// No timeout - tool runs indefinitely
							hasNoTimeout = true
							v2Logger.Debug("🔧 [TOOL_TIMEOUT] Custom tool has NO timeout (runs indefinitely)",
								loggerv2.String("tool_name", tc.FunctionCall.Name))
						} else if directTool.Timeout > 0 {
							// Custom per-tool timeout
							hasNoTimeout = false
							toolTimeout = directTool.Timeout
							v2Logger.Debug("🔧 [TOOL_TIMEOUT] Custom tool has per-tool timeout",
								loggerv2.String("tool_name", tc.FunctionCall.Name),
								loggerv2.String("timeout", directTool.Timeout.String()))
						}
					}
				}

				var toolCtx context.Context
				var cancel context.CancelFunc
				if hasNoTimeout {
					// No timeout - use the parent context directly (will run until agent context is cancelled)
					toolCtx = ctx
					cancel = func() {} // No-op cancel
				} else {
					toolCtx, cancel = context.WithTimeout(ctx, toolTimeout)
				}
				defer cancel()

				startTime := time.Now()
				v2Logger.Info(fmt.Sprintf("⏱️  TOOL EXECUTION START - Time: %s, Tool: %s, Turn: %d",
					startTime.Format(time.RFC3339), tc.FunctionCall.Name, turn+1))

				// 🔧 DEBUG: Log tool call with arguments
				toolType := "MCP"
				if isVirtualTool(tc.FunctionCall.Name) {
					toolType = "virtual"
				} else if isCustomTool {
					toolType = "custom"
				}
				argsJSON, _ := json.Marshal(args)
				timeoutStr := toolTimeout.String()
				if hasNoTimeout {
					timeoutStr = "none (indefinite)"
				}
				v2Logger.Debug("🔧 [TOOL_CALL] Tool called",
					loggerv2.String("tool_name", tc.FunctionCall.Name),
					loggerv2.String("tool_type", toolType),
					loggerv2.String("server_name", serverName),
					loggerv2.String("tool_call_id", tc.ID),
					loggerv2.Int("turn", turn+1),
					loggerv2.String("arguments", toolerr.TruncateArgsForLog(string(argsJSON))),
					loggerv2.String("timeout", timeoutStr))

				// Add cache hit event during tool execution to show cached connection usage
				if len(a.tracers) > 0 && serverName != "" && serverName != "virtual-tools" {
					// Emit connection cache hit event to show we're using cached MCP server connection
					// Note: We do NOT cache tool execution results - only server connections
					connectionCacheHitEvent := events.NewCacheHitEvent(serverName, fmt.Sprintf("unified_%s", serverName), "unified_cache", 1, time.Duration(0))

					// Debug: Log the connection cache hit event structure
					v2Logger.Debug("Connection cache hit")

					a.emitTypedEvent(ctx, connectionCacheHitEvent)

				}

				// Inject generic tool execution metadata into context
				// Any tool can extract these values if needed (e.g., workspace tools for event emission)
				// Using generic keys keeps conversation.go agnostic about specific tool implementations
				toolCtx = context.WithValue(toolCtx, ToolExecutionAgentKey, a)
				toolCtx = context.WithValue(toolCtx, ToolExecutionTurnKey, turn+1)
				toolCtx = context.WithValue(toolCtx, ToolExecutionServerKey, serverName)
				toolCtx = context.WithValue(toolCtx, ToolExecutionLLMConfigKey, a.getLLMModelConfig())

				// Apply per-tool argument transformer if registered.
				// This runs BEFORE any execution branch (virtual → custom → MCP) so all paths
				// see transformed args. Primary use case: resolve workspace-relative paths
				// (e.g. "Downloads/file.pdf") to absolute host paths for tools that require them.
				// Transformers are registered via Agent.SetToolArgTransformer().
				if a.toolArgTransformers != nil {
					if transformer, ok := a.toolArgTransformers[tc.FunctionCall.Name]; ok {
						v2Logger.Info("[TOOL_ARGS] Applying tool arg transformer",
							loggerv2.String("tool_name", tc.FunctionCall.Name))
						transformer(args)
					}
				}

				var result *mcp.CallToolResult
				var toolErr error

				// Resolve the LLM-facing disambiguated name to the name registered by MCP.
				actualToolName := a.realMCPToolName(tc.FunctionCall.Name, serverName)
				if actualToolName != tc.FunctionCall.Name {
					v2Logger.Debug(fmt.Sprintf("🔧 [TOOL_LOOKUP] Resolved disambiguated tool '%s' -> '%s' (server: %s)", tc.FunctionCall.Name, actualToolName, serverName))
				}

				// The application's tool guard sees every call before it runs and
				// may answer it with a stub instead (test mode, PLAT-562).
				if guarded := toolguard.Check(toolCtx, a.guardCall(tc.FunctionCall.Name, actualToolName, serverName, isCustomTool, client, args)); guarded.Stub {
					result = guarded.AsResult()
				} else if isVirtualTool(tc.FunctionCall.Name) {
					// Handle virtual tool execution
					v2Logger.Debug("🔧 [TOOL_CALL] Executing virtual tool",
						loggerv2.String("tool_name", tc.FunctionCall.Name))
					resultText, toolErr := a.handleVirtualTool(toolCtx, tc.FunctionCall.Name, args)
					if toolErr != nil {
						result = &mcp.CallToolResult{
							IsError: true,
							Content: []mcp.Content{&mcp.TextContent{Text: toolErr.Error()}},
						}
					} else {
						// Ensure resultText is never empty for virtual tools
						// This prevents empty content from being sent to LLM
						if resultText == "" {
							v2Logger.Warn("Virtual tool returned empty result - using default message",
								loggerv2.String("tool", tc.FunctionCall.Name))
							resultText = fmt.Sprintf("Tool '%s' executed successfully but returned no output.", tc.FunctionCall.Name)
						}
						result = &mcp.CallToolResult{
							IsError: false,
							Content: []mcp.Content{&mcp.TextContent{Text: resultText}},
						}
					}
				} else if isCustomTool {
					v2Logger.Debug(fmt.Sprintf("🔧 [TOOL_EXECUTION] Executing custom tool '%s' (category: %s)", tc.FunctionCall.Name, directTool.DisplayGroup))
					resultText, toolErr := directTool.Executor(toolCtx, args)

					if toolErr != nil {
						v2Logger.Error(fmt.Sprintf("🔧 [TOOL_EXECUTION] Custom tool '%s' execution failed: %v", tc.FunctionCall.Name, toolErr), toolErr)
						result = &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: toolErr.Error()}}}
					} else {
						v2Logger.Debug(fmt.Sprintf("🔧 [TOOL_EXECUTION] Custom tool '%s' executed successfully (result length: %d chars)", tc.FunctionCall.Name, len(resultText)))
						result = &mcp.CallToolResult{IsError: false, Content: []mcp.Content{&mcp.TextContent{Text: resultText}}}
					}
				} else {
					// Handle regular MCP tool execution
					v2Logger.Debug("🔧 [TOOL_CALL] About to execute MCP tool",
						loggerv2.String("tool_name", actualToolName),
						loggerv2.String("server_name", serverName),
						loggerv2.String("timeout", toolTimeout.String()))
					callStart := time.Now()
					result, toolErr = callToolWithTimeoutWrapper(toolCtx, client, actualToolName, args, v2Logger, serverName)
					callDuration := time.Since(callStart)
					v2Logger.Debug("🔧 [TOOL_CALL] MCP tool call completed",
						loggerv2.String("tool_name", tc.FunctionCall.Name),
						loggerv2.String("server_name", serverName),
						loggerv2.String("duration", callDuration.String()),
						loggerv2.Any("ctx_done", toolCtx.Err() != nil),
						loggerv2.Any("has_error", toolErr != nil))
				}

				duration := time.Since(startTime)
				v2Logger.Info(fmt.Sprintf("⏱️  TOOL EXECUTION END - Time: %s, Tool: %s, Duration: %v, Turn: %d",
					time.Now().Format(time.RFC3339), tc.FunctionCall.Name, duration, turn+1))
				log.Printf("[LATENCY_DEBUG] Turn %d | T+%dms | Tool executed: %s | tool_duration=%dms err=%v",
					turn+1, time.Since(conversationStartTime).Milliseconds(), tc.FunctionCall.Name, duration.Milliseconds(), toolErr)

				// Check for timeout
				if toolCtx.Err() == context.DeadlineExceeded {
					toolErr = fmt.Errorf("tool execution timed out after %s: %s", toolTimeout.String(), tc.FunctionCall.Name)
					v2Logger.Debug("Tool call timed out",
						loggerv2.Int("turn", turn+1),
						loggerv2.String("tool_name", tc.FunctionCall.Name),
						loggerv2.String("timeout", toolTimeout.String()))
				}

				if agentCtx.Err() != nil {
					v2Logger.Debug("Context cancelled after tool execution (will stop after appending result)",
						loggerv2.Int("turn", turn+1),
						loggerv2.String("tool_name", tc.FunctionCall.Name),
						loggerv2.Error(agentCtx.Err()))
					// Don't return early here — the tool result must be appended to messages
					// first (line ~1613) to keep the conversation state valid (every tool call
					// needs a response). The cancellation will be caught at the start of the
					// next turn (line 533) or before the next tool execution (line 1222).
				}

				// Handle tool execution errors gracefully - provide feedback to LLM and continue
				if toolErr != nil {
					// 🔧 DEBUG: Log tool error
					v2Logger.Debug("🔧 [TOOL_RESPONSE] Tool execution error",
						loggerv2.String("tool_name", tc.FunctionCall.Name),
						loggerv2.String("tool_type", toolType),
						loggerv2.String("server_name", serverName),
						loggerv2.String("tool_call_id", tc.ID),
						loggerv2.Int("turn", turn+1),
						loggerv2.String("error", toolErr.Error()),
						loggerv2.String("duration", duration.String()))

					// 🔧 ENHANCED ERROR RECOVERY HANDLING
					errorRecoveryHandler := newErrorRecoveryHandler(a)

					// Attempt error recovery for recoverable errors
					recoveredResult, recoveredDuration, wasRecovered, recoveredErr := errorRecoveryHandler.HandleError(
						ctx, &tc, serverName, toolErr, startTime, isCustomTool, isVirtualTool(tc.FunctionCall.Name))

					if wasRecovered && recoveredErr == nil {
						// Successfully recovered - use recovered result and continue normal flow
						v2Logger.Debug("Successfully recovered from error for tool",
							loggerv2.String("tool", tc.FunctionCall.Name))
						result = recoveredResult
						toolErr = nil
						duration = recoveredDuration
						// Continue to normal result processing below (outside this if block)
					} else {
						// Recovery failed or not attempted - proceed with error handling
						if wasRecovered {
							v2Logger.Error("Recovery failed for tool", recoveredErr,
								loggerv2.String("tool", tc.FunctionCall.Name))
							toolErr = recoveredErr
							duration = recoveredDuration
						}

						// The event is the live view; this is the durable one. Events are
						// pruned by retention and a child terminal's are not even
						// fetched until someone opens it, so without this line an
						// API-provider tool failure leaves no trace in the backend log.
						// Same marker as the bridge handlers so one grep covers every
						// provider.
						v2Logger.Error(toolerr.Marker+" tool failed", toolErr,
							loggerv2.String("layer", "agent_tool_loop"),
							loggerv2.String("tool", tc.FunctionCall.Name),
							loggerv2.String("server", serverName),
							loggerv2.String("session_id", a.sessionID),
							loggerv2.String("duration", duration.String()),
							loggerv2.String("args", toolerr.TruncateArgsForLog(tc.FunctionCall.Arguments)))

						// Emit tool call error event using typed event data
						toolErrorEvent := events.NewToolCallErrorEvent(turn+1, tc.FunctionCall.Name, toolErr.Error(), serverName, duration)
						toolErrorEvent.ToolCallID = tc.ID
						a.emitTypedEvent(ctx, toolErrorEvent)

						// Instead of failing the entire conversation, provide feedback to the LLM
						errorResultText := fmt.Sprintf("Tool execution failed - %v", toolErr)

						// Add the error result to the conversation so the LLM can continue
						messages = append(messages, llmtypes.MessageContent{
							Role:  llmtypes.ChatMessageTypeTool, // Use "tool" role for tool responses
							Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: tc.ID, Name: tc.FunctionCall.Name, Content: errorResultText, IsError: true}},
						})

						// Continue to next turn instead of returning error
						continue
					}
				}
				var resultText string
				if result != nil {

					// Get the tool result as string (without prefix)
					resultText = mcpclient.ToolResultAsString(result)
					if signal, failed := toolerr.CanonicalFailureForTool(tc.FunctionCall.Name, resultText); failed && !result.IsError {
						result.IsError = true
						v2Logger.Error(toolerr.Marker+" canonical payload failure", nil,
							loggerv2.String("layer", "agent_tool_loop"),
							loggerv2.String("tool", tc.FunctionCall.Name),
							loggerv2.String("server", serverName),
							loggerv2.String("session_id", a.sessionID),
							loggerv2.String("signal", signal),
							loggerv2.String("result", toolerr.TruncateForLog(resultText)))
					}

					// 🔧 DEBUG: Log tool response
					resultPreview := resultText
					if len(resultPreview) > 500 {
						resultPreview = resultPreview[:500] + "... (truncated)"
					}
					v2Logger.Debug("🔧 [TOOL_RESPONSE] Tool response received",
						loggerv2.String("tool_name", tc.FunctionCall.Name),
						loggerv2.String("tool_type", toolType),
						loggerv2.String("server_name", serverName),
						loggerv2.String("tool_call_id", tc.ID),
						loggerv2.Int("turn", turn+1),
						loggerv2.Int("result_length", len(resultText)),
						loggerv2.Any("is_error", result.IsError),
						loggerv2.String("duration", duration.String()),
						loggerv2.String("result_preview", resultPreview))

					// Ensure resultText is never empty when sending to LLM
					// This is a safety check for all tool types (virtual, custom, MCP)
					if resultText == "" && !result.IsError {
						v2Logger.Warn("Tool returned empty result - using default message",
							loggerv2.String("tool", tc.FunctionCall.Name))
						resultText = fmt.Sprintf("Tool '%s' executed successfully but returned no output.", tc.FunctionCall.Name)
					}

					// 🔧 BROKEN PIPE DETECTION IN RESULT CONTENT (regardless of IsError flag)
					// Check for broken pipe errors in content text, even when IsError is false
					// This handles cases where the MCP server returns broken pipe errors in content rather than as error flags
					if mcpclient.IsBrokenPipeInContent(resultText) {
						v2Logger.Info(fmt.Sprintf("🔧 [BROKEN PIPE DETECTED IN RESULT] Turn %d, Tool: %s, Server: %s, IsError: %v - Attempting immediate connection recreation", turn+1, tc.FunctionCall.Name, serverName, result.IsError))

						// Create error recovery handler
						errorRecoveryHandler := newErrorRecoveryHandler(a)

						// Create a fake error for the recovery handler
						fakeErr := fmt.Errorf("broken pipe detected in result: %s", resultText)

						// Attempt error recovery
						recoveredResult, recoveredDuration, wasRecovered, recoveredErr := errorRecoveryHandler.HandleError(
							ctx, &tc, serverName, fakeErr, startTime, isCustomTool, isVirtualTool(tc.FunctionCall.Name))

						if wasRecovered && recoveredErr == nil {
							v2Logger.Debug("Broken pipe recovery successful for tool",
								loggerv2.String("tool", tc.FunctionCall.Name))
							result = recoveredResult
							duration = recoveredDuration
							resultText = mcpclient.ToolResultAsString(result)
						} else if wasRecovered {
							v2Logger.Error("Broken pipe recovery failed for tool", recoveredErr,
								loggerv2.String("tool", tc.FunctionCall.Name))
						}
					}

				} else {
					resultText = "Tool execution completed but no result returned"
				}
				// 3. Append the tool result as a new message (after the AI tool_call message)
				// Add recover block to catch panics
				func() {
					defer func() {
						if r := recover(); r != nil {
							v2Logger.Error("Panic while appending tool result message", fmt.Errorf("%v", r))
						}
					}()
					// Use the exact tool call ID from the LLM response
					messages = append(messages, llmtypes.MessageContent{
						Role:  llmtypes.ChatMessageTypeTool, // Use "tool" role for tool responses
						Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: tc.ID, Name: tc.FunctionCall.Name, Content: resultText, IsError: result != nil && result.IsError}},
					})
				}()

				// End the tool execution span with output and error information
				toolOutput := map[string]interface{}{
					"tool_name":   tc.FunctionCall.Name,
					"server_name": a.toolToServer[tc.FunctionCall.Name],
					"result":      resultText,
					"duration":    duration,
					"turn":        turn + 1,
					"success":     toolErr == nil && (result == nil || !result.IsError),
					"timeout":     getToolExecutionTimeout(a).String(),
				}
				if toolErr != nil {
					toolOutput["error"] = toolErr.Error()
					if strings.Contains(toolErr.Error(), "timed out") {
						toolOutput["error_type"] = "tool_execution_timeout"
					} else {
						toolOutput["error_type"] = "tool_execution_error"
					}
				}

				// Reported success is not evidence of success. Bridge and shell
				// results routinely carry a failure in the payload while the
				// transport reports OK, which is how 34 failures in one day
				// rendered as green checks. Sniff the content and mark it.
				if result == nil || !result.IsError {
					if signal, suspicious := toolerr.SuspiciousForTool(tc.FunctionCall.Name, resultText); suspicious {
						v2Logger.Error(toolerr.SuspectMarker+" tool reported success but the result reads like a failure", nil,
							loggerv2.String("layer", "agent_tool_loop"),
							loggerv2.String("tool", tc.FunctionCall.Name),
							loggerv2.String("server", serverName),
							loggerv2.String("session_id", a.sessionID),
							loggerv2.String("signal", signal),
							loggerv2.String("duration", duration.String()),
							loggerv2.String("result", toolerr.TruncateForLog(resultText)),
							loggerv2.String("args", toolerr.TruncateArgsForLog(tc.FunctionCall.Arguments)))
					}
				}

				// Tool execution completed - emit tool call end event
				// Only emit ToolCallEndEvent if result is not an error (errors should emit ToolCallErrorEvent)
				if result == nil || !result.IsError {
					// Get current token usage information for the tool call end event
					_, _, _, _, _, _, _, _, _, _, _, _, contextUsagePercent := a.getTokenUsageWithPricing()
					a.tokenTrackingMutex.RLock()
					modelContextWindow := a.modelContextWindow
					contextWindowUsage := a.currentContextWindowUsage
					a.tokenTrackingMutex.RUnlock()

					// Emit tool call end event using typed event data (consolidated - contains all tool information)
					toolEndEvent := events.NewToolCallEndEventWithTokenUsageAndModel(turn+1, tc.FunctionCall.Name, resultText, serverName, duration, "", contextUsagePercent, modelContextWindow, contextWindowUsage, a.modelID)
					toolEndEvent.ToolCallID = tc.ID
					a.emitTypedEvent(ctx, toolEndEvent)
				} else if result.IsError {
					// Result contains an error - emit tool call error event
					// This handles the case where tool execution succeeded but the tool returned an error result
					v2Logger.Error(toolerr.Marker+" tool returned an error result", nil,
						loggerv2.String("layer", "agent_tool_loop"),
						loggerv2.String("tool", tc.FunctionCall.Name),
						loggerv2.String("server", serverName),
						loggerv2.String("session_id", a.sessionID),
						loggerv2.String("duration", duration.String()),
						loggerv2.String("result", toolerr.TruncateForLog(resultText)),
						loggerv2.String("args", toolerr.TruncateArgsForLog(tc.FunctionCall.Arguments)))

					toolErrorEvent := events.NewToolCallErrorEvent(turn+1, tc.FunctionCall.Name, resultText, serverName, duration)
					toolErrorEvent.ToolCallID = tc.ID
					a.emitTypedEvent(ctx, toolErrorEvent)
				}

				// Note: Removed redundant tool_output and tool_response events
				// tool_call_end now contains all necessary tool information

				// 🔄 LOOP DETECTION: Check if this tool call + args + response matches recent calls
				if tc.FunctionCall != nil {
					loopResult := loopDetector.CheckAndHandleLoop(tc.FunctionCall.Name, tc.FunctionCall.Arguments, resultText)
					if loopResult.Detected {
						HandleLoopDetection(a, ctx, loopResult, lastUserMessage, turn+1, conversationStartTime, &messages, v2Logger)
						// Continue to next turn so LLM can respond to the correction message
					}
				}
			}

			// Drain and inject any pending steer messages from the user
			if steerMsgs := a.drainSteerMessages(); len(steerMsgs) > 0 {
				messages = injectSteerMessages(ctx, a, messages, steerMsgs, turn, "Injected steer message after sequential tool execution")
			}

			// After processing all tool calls, continue to next turn
			// The messages slice now includes any user messages added by read_image
			continue
		} else {
			// No tool calls - add the assistant response to conversation history
			// This is CRITICAL to prevent conversation loops.
			//
			// Also handle a race where the user sends a steer while the model is already
			// streaming its final answer: keep the assistant message we just produced,
			// inject the steer as a follow-up user turn, and continue instead of
			// dropping the steer on conversation exit.

			// For CLI providers (Claude Code, Codex CLI): tool calls happen
			// inside the subprocess and are not returned as ToolCalls in the response.
			// GenerateContentWithRetry attaches completed StreamChunkTypeToolCallEnd chunks
			// (collected by processChunks via streamingManager.CLIToolCalls) to
			// GenerationInfo.Additional["cli_tool_call_chunks"]. Reconstruct synthetic
			// messages here so the saved conversation history reflects what actually ran.
			if choice.GenerationInfo != nil {
				if chunksJSON, ok := choice.GenerationInfo.Additional["cli_tool_call_chunks"].(string); ok && chunksJSON != "" {
					var chunks []llmtypes.StreamChunk
					if err := json.Unmarshal([]byte(chunksJSON), &chunks); err == nil {
						for _, c := range chunks {
							// Skip flushed-but-incomplete tool calls (subprocess exited mid-flight)
							if c.ToolResult == "" {
								continue
							}
							// AI message: one ToolCall part per completed tool invocation
							messages = append(messages, llmtypes.MessageContent{
								Role: llmtypes.ChatMessageTypeAI,
								Parts: []llmtypes.ContentPart{llmtypes.ToolCall{
									ID:   c.ToolCallID,
									Type: "function",
									FunctionCall: &llmtypes.FunctionCall{
										Name:      c.ToolName,
										Arguments: c.ToolArgs,
									},
								}},
							})
							// Tool result message
							messages = append(messages, llmtypes.MessageContent{
								Role: llmtypes.ChatMessageTypeTool,
								Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{
									ToolCallID: c.ToolCallID,
									Name:       c.ToolName,
									Content:    c.ToolResult,
									IsError:    cliToolResultIsError(c.ToolName, c.ToolResult),
								}},
							})
						}
					}
				}
			}

			// Splice the coding-agent's internal turn trail (text +
			// tool_use + tool_result) BEFORE the final assistant text,
			// so the persisted conversation_history captures what
			// happened inside the CLI's hidden loop in the same shape
			// as outer-loop messages. Tmux transports (claude-code,
			// codex and cursor CLIs) populate this from their
			// sidecar transcript; structured/API transports typically
			// don't populate it because the agent layer already sees
			// those turns directly. Empty/missing is a no-op.
			intermediate, hasIntermediate := llmtypes.ExtractCodingProviderIntermediateMessages(choice.GenerationInfo)
			finalResult := choice.Content
			if hasIntermediate && len(intermediate.Messages) > 0 {
				// Trust the sidecar splice as the complete trail
				// (text + tool_use + tool_result + final answer).
				// Skip choice.Content entirely — it can be corrupted
				// for tmux CLIs whose pane-capture grabs stale user
				// inputs alongside the final answer (observed in
				// cursor's multi-input pane). The splice is built
				// from the CLI's structured sidecar, not the pane.
				messages = append(messages, intermediate.Messages...)
				if sidecarFinal := finalAssistantTextFromCodingTrail(intermediate.Messages); sidecarFinal != "" {
					finalResult = sidecarFinal
				}
			} else if choice.Content != "" {
				assistantMessage := llmtypes.MessageContent{
					Role:  llmtypes.ChatMessageTypeAI,
					Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: choice.Content}},
				}
				messages = append(messages, assistantMessage)
			}
			if steerMsgs := a.drainFinalSteerMessages(); len(steerMsgs) > 0 {
				messages = injectSteerMessages(ctx, a, messages, steerMsgs, turn, "Injected steer message after final assistant response")
				continue
			}

			// Simple agent - return immediately when no tool calls
			v2Logger.Debug("No tool calls detected, returning final answer", loggerv2.Int("turn", turn+1))

			// Emit unified completion event for simple agent
			unifiedCompletionEvent := events.NewUnifiedCompletionEvent(
				"simple",                          // agentType
				string(a.agentMode),               // agentMode
				lastUserMessage,                   // question
				finalResult,                       // finalResult
				"completed",                       // status
				time.Since(conversationStartTime), // duration
				turn+1,                            // turns
			)
			a.annotateUnifiedCompletionEvent(unifiedCompletionEvent)
			a.emitTypedEvent(ctx, unifiedCompletionEvent)

			// NEW: End agent session for hierarchy tracking
			a.endAgentSession(ctx, time.Since(conversationStartTime))

			return finalResult, messages, nil
		}
	}

	// Max turns reached - give agent one final chance to provide a proper answer
	v2Logger.Debug("Max turns reached, giving agent final chance to provide answer",
		loggerv2.Int("max_turns", a.maxTurns))

	// Emit max turns reached event
	maxTurnsEvent := events.NewMaxTurnsReachedEvent(a.maxTurns, a.maxTurns, lastUserMessage, "You are out of turns, you need to generate final now. Please provide your final answer based on what you have accomplished so far. If your task is not complete, please provide a summary of what you have accomplished so far and what is missing.", string(a.agentMode), time.Since(conversationStartTime))
	a.emitTypedEvent(ctx, maxTurnsEvent)

	// Add a user message asking for final answer
	finalUserMessage := llmtypes.MessageContent{
		Role: llmtypes.ChatMessageTypeHuman,
		Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{
				Text: "You are out of turns, you need to generate a final answer now. Please provide your final answer based on what you have accomplished so far.",
			},
		},
	}

	// Add the final user message to the conversation
	messages = append(messages, finalUserMessage)

	// Emit user message event for the final request
	finalUserMessageEvent := events.NewUserMessageEvent(a.maxTurns+1, "You are out of turns, you need to generate final now. Please provide your final answer based on what you have accomplished so far.", "user")
	a.emitTypedEvent(ctx, finalUserMessageEvent)

	// Make one final LLM call to get the final answer
	var finalResp *llmtypes.ContentResponse
	var err error

	finalOpts := []llmtypes.CallOption{}
	if !llm.IsO3O4Model(a.modelID) {
		finalOpts = append(finalOpts, llmtypes.WithTemperature(a.temperature))
	}
	finalOpts = a.appendCodingAgentInteractiveOptions(finalOpts)

	finalResp, finalUsage, err := generateContentWithRetry(a, ctx, messages, finalOpts, a.maxTurns+1)

	// Log finalUsage for debugging
	v2Logger.Info(fmt.Sprintf("🔍 [FINAL LLM CALL DEBUG] finalUsage from GenerateContentWithRetry:"))
	v2Logger.Info(fmt.Sprintf("   InputTokens: %d, OutputTokens: %d, TotalTokens: %d, Unit: %s",
		finalUsage.InputTokens, finalUsage.OutputTokens, finalUsage.TotalTokens, finalUsage.Unit))
	if finalResp != nil && len(finalResp.Choices) > 0 {
		if finalResp.Choices[0].GenerationInfo != nil {
			genInfo := finalResp.Choices[0].GenerationInfo
			v2Logger.Info(fmt.Sprintf("   GenerationInfo available:"))
			if genInfo.InputTokens != nil {
				v2Logger.Info(fmt.Sprintf("      InputTokens: %d", *genInfo.InputTokens))
			}
			if genInfo.OutputTokens != nil {
				v2Logger.Info(fmt.Sprintf("      OutputTokens: %d", *genInfo.OutputTokens))
			}
			if genInfo.TotalTokens != nil {
				v2Logger.Info(fmt.Sprintf("      TotalTokens: %d", *genInfo.TotalTokens))
			}
			if genInfo.CachedContentTokens != nil {
				v2Logger.Info(fmt.Sprintf("      CachedContentTokens: %d", *genInfo.CachedContentTokens))
			}
			if genInfo.ReasoningTokens != nil {
				v2Logger.Info(fmt.Sprintf("      ReasoningTokens: %d", *genInfo.ReasoningTokens))
			}
			if genInfo.CacheDiscount != nil {
				v2Logger.Info(fmt.Sprintf("      CacheDiscount: %.2f%%", *genInfo.CacheDiscount*100))
			}
			if len(genInfo.Additional) > 0 {
				v2Logger.Info(fmt.Sprintf("      Additional fields:"))
				for key, value := range genInfo.Additional {
					if strings.Contains(strings.ToLower(key), "cache") || strings.Contains(strings.ToLower(key), "token") {
						v2Logger.Info(fmt.Sprintf("         %s: %v", key, value))
					}
				}
			}
		} else {
			v2Logger.Info(fmt.Sprintf("   GenerationInfo is nil"))
		}
	} else {
		v2Logger.Info(fmt.Sprintf("   finalResp is nil or has no choices"))
	}

	// Accumulate token usage from final LLM call
	if finalResp != nil && len(finalResp.Choices) > 0 && finalUsage.TotalTokens > 0 {
		a.accumulateTokenUsage(ctx, events.UsageMetrics{
			PromptTokens:     finalUsage.InputTokens,
			CompletionTokens: finalUsage.OutputTokens,
			TotalTokens:      finalUsage.TotalTokens,
		}, finalResp, a.maxTurns+1)
	} else {
		choicesCount := 0
		if finalResp != nil {
			choicesCount = len(finalResp.Choices)
		}
		v2Logger.Warn("Skipping token accumulation",
			loggerv2.Any("final_resp_exists", finalResp != nil),
			loggerv2.Int("choices", choicesCount),
			loggerv2.Int("total_tokens", finalUsage.TotalTokens))
	}

	if err != nil {
		// If the final call also fails, emit error event
		conversationErrorEvent := &events.ConversationErrorEvent{
			BaseEventData: events.BaseEventData{
				Timestamp: time.Now(),
			},
			Question: lastUserMessage,
			Error:    "max turns reached and final attempt failed",
			Turn:     a.maxTurns + 1,
			Context:  "conversation",
			Duration: time.Since(conversationStartTime),
		}
		a.emitTypedEvent(ctx, conversationErrorEvent)

		if lastResponse != "" {
			v2Logger.Debug("Forced FINAL_ANSWER due to max turns")

			// Agent end event removed - no longer needed

			// 🎯 FIX: End the trace for fallback completion - replaced with event emission
			// Note: This was a successful completion, so we emit a completion event instead of error
			unifiedCompletionEvent := events.NewUnifiedCompletionEvent(
				"react",                           // agentType
				string(a.agentMode),               // agentMode
				lastUserMessage,                   // question
				lastResponse,                      // finalResult
				"completed",                       // status
				time.Since(conversationStartTime), // duration
				a.maxTurns+1,                      // turns (+1 for the final turn)
			)
			a.annotateUnifiedCompletionEvent(unifiedCompletionEvent)
			a.emitTypedEvent(ctx, unifiedCompletionEvent)

			// NEW: End agent session for hierarchy tracking
			a.endAgentSession(ctx, time.Since(conversationStartTime))

			// Append the final response to messages array for consistency
			if lastResponse != "" {
				assistantMessage := llmtypes.MessageContent{
					Role:  llmtypes.ChatMessageTypeAI,
					Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: lastResponse}},
				}
				messages = append(messages, assistantMessage)
			}

			return lastResponse, messages, nil
		}
		v2Logger.Warn("Exiting with no final answer after max turns",
			loggerv2.Int("max_turns", a.maxTurns))

		// 🎯 FIX: End the trace for max turns error - replaced with event emission
		maxTurnsErrorEvent := events.NewConversationErrorEvent(lastUserMessage, fmt.Sprintf("max turns (%d) reached without final answer", a.maxTurns), a.maxTurns+1, "max_turns_exceeded", time.Since(conversationStartTime))
		a.emitTypedEvent(ctx, maxTurnsErrorEvent)

		return "", messages, fmt.Errorf("max turns (%d) reached without final answer", a.maxTurns)
	}

	if finalResp == nil || finalResp.Choices == nil || len(finalResp.Choices) == 0 {
		v2Logger.Warn("Final call returned no response choices")

		// 🎯 FIX: End the trace for final call error - replaced with event emission
		finalCallErrorEvent := events.NewConversationErrorEvent(lastUserMessage, "final call returned no response choices", a.maxTurns+1, "no_final_choices", time.Since(conversationStartTime))
		a.emitTypedEvent(ctx, finalCallErrorEvent)

		return "", messages, fmt.Errorf("final call returned no response choices")
	}

	finalChoice := finalResp.Choices[0]

	// Token usage is already included in the LLMGenerationEndEvent above

	// Note: LLM generation end event is already emitted in the main conversation flow
	// No need to emit it again here to avoid duplication

	// Simple agent - use final choice content directly
	v2Logger.Debug("Final answer provided after max turns")

	// Emit unified completion event
	unifiedCompletionEvent := events.NewUnifiedCompletionEvent(
		"simple",                          // agentType
		string(a.agentMode),               // agentMode
		lastUserMessage,                   // question
		finalChoice.Content,               // finalResult
		"completed",                       // status
		time.Since(conversationStartTime), // duration
		a.maxTurns+1,                      // turns (+1 for the final turn)
	)
	a.annotateUnifiedCompletionEvent(unifiedCompletionEvent)
	a.emitTypedEvent(ctx, unifiedCompletionEvent)

	// NEW: End agent session for hierarchy tracking
	a.endAgentSession(ctx, time.Since(conversationStartTime))

	// Append the final response to messages array for consistency
	if finalChoice.Content != "" {
		assistantMessage := llmtypes.MessageContent{
			Role:  llmtypes.ChatMessageTypeAI,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: finalChoice.Content}},
		}
		messages = append(messages, assistantMessage)
	}

	return finalChoice.Content, messages, nil
}

func cliToolResultIsError(toolName, result string) bool {
	_, failed := toolerr.CanonicalFailureForTool(toolName, result)
	return failed
}

// promptLogCounter is a global counter for ordering prompt log files within a session.
var promptLogCounter uint64
var promptLogCounterMu sync.Mutex
var promptLogPruneMu sync.Mutex

const defaultAgentPromptLogMaxSessions = 10

func nextPromptLogCounter() uint64 {
	promptLogCounterMu.Lock()
	defer promptLogCounterMu.Unlock()
	promptLogCounter++
	return promptLogCounter
}

func agentPromptLogRoot() string {
	return filepath.Join("logs", "agent_prompts")
}

func agentPromptLogSessionDirName(sessionID string) string {
	sessionDir := strings.TrimSpace(sessionID)
	if sessionDir == "" {
		sessionDir = "no-session"
	}
	sessionDir = strings.ReplaceAll(sessionDir, "/", "_")
	sessionDir = strings.ReplaceAll(sessionDir, string(os.PathSeparator), "_")
	return sessionDir
}

func agentPromptLogSessionDir(sessionID string) string {
	return filepath.Join(agentPromptLogRoot(), agentPromptLogSessionDirName(sessionID))
}

func agentPromptLogMaxSessions() int {
	raw := strings.TrimSpace(os.Getenv("LOG_AGENT_PROMPTS_MAX_SESSIONS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("AGENT_PROMPTS_MAX_SESSIONS"))
	}
	if raw == "" {
		return defaultAgentPromptLogMaxSessions
	}
	maxSessions, err := strconv.Atoi(raw)
	if err != nil {
		return defaultAgentPromptLogMaxSessions
	}
	return maxSessions
}

func pruneAgentPromptLogSessions(activeSessionDir string) {
	maxSessions := agentPromptLogMaxSessions()
	if maxSessions <= 0 {
		return
	}

	promptLogPruneMu.Lock()
	defer promptLogPruneMu.Unlock()

	entries, err := os.ReadDir(agentPromptLogRoot())
	if err != nil {
		return
	}

	type sessionDirInfo struct {
		name    string
		modTime time.Time
	}
	sessionDirs := make([]sessionDirInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		sessionDirs = append(sessionDirs, sessionDirInfo{name: entry.Name(), modTime: info.ModTime()})
	}
	if len(sessionDirs) <= maxSessions {
		return
	}

	sort.Slice(sessionDirs, func(i, j int) bool {
		if sessionDirs[i].modTime.Equal(sessionDirs[j].modTime) {
			return sessionDirs[i].name > sessionDirs[j].name
		}
		return sessionDirs[i].modTime.After(sessionDirs[j].modTime)
	})

	keep := make(map[string]bool, maxSessions)
	if activeSessionDir != "" {
		keep[activeSessionDir] = true
	}
	for _, dir := range sessionDirs {
		if len(keep) >= maxSessions {
			break
		}
		keep[dir.name] = true
	}

	for _, dir := range sessionDirs {
		if keep[dir.name] {
			continue
		}
		_ = os.RemoveAll(filepath.Join(agentPromptLogRoot(), dir.name))
	}
}

// logFinalPrompts writes the final system prompt and user message to logs/agent_prompts/
// as both JSON and Markdown files. This captures the true source-of-truth prompts right
// before the LLM call, after all instruction processing (SetInstructions, AddInstructions,
// tool structure resolution) is complete.
//
// Files are organized by session:
//
//	logs/agent_prompts/{session_id}/001_{timestamp}_{provider}_{model}.{json,md}
//
// Runs in a goroutine to avoid blocking.
// promptLogInfo holds the file path info computed at start, shared with the end logger.
type promptLogInfo struct {
	Dir      string
	BaseName string
	Seq      uint64
}

// logFinalPrompts writes the system prompt + user message to disk at LLM call start.
// Returns path info so logConversationEnd can append tool calls to the same directory.
func logFinalPrompts(a *Agent, messages []llmtypes.MessageContent) *promptLogInfo {
	// Extract system prompt and last user message from the finalized messages
	var systemPrompt, userMessage string
	for _, msg := range messages {
		if msg.Role == llmtypes.ChatMessageTypeSystem {
			for _, part := range msg.Parts {
				if tc, ok := part.(llmtypes.TextContent); ok {
					systemPrompt = tc.Text
				}
			}
		}
	}
	// Find last user message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llmtypes.ChatMessageTypeHuman {
			for _, part := range messages[i].Parts {
				if tc, ok := part.(llmtypes.TextContent); ok {
					userMessage = tc.Text
					break
				}
			}
			break
		}
	}

	seq := nextPromptLogCounter()

	// Compute dir + baseName synchronously so the caller can pass them to the end logger
	sessionDir := agentPromptLogSessionDirName(a.sessionID)
	dir := filepath.Join(agentPromptLogRoot(), sessionDir)

	ts := time.Now()
	sanitize := func(s string) string {
		s = strings.ReplaceAll(s, "/", "_")
		s = strings.ReplaceAll(s, ":", "_")
		s = strings.ReplaceAll(s, " ", "-")
		s = strings.ToLower(s)
		return s
	}
	agentMode := a.promptLogLabel
	if agentMode == "" {
		agentMode = "agent"
		if idx := strings.Index(systemPrompt, "\n"); idx > 0 && strings.HasPrefix(systemPrompt, "# ") {
			header := strings.TrimPrefix(systemPrompt[:idx], "# ")
			header = strings.ToLower(strings.TrimSpace(header))
			header = strings.ReplaceAll(header, " ", "-")
			if len(header) > 30 {
				header = header[:30]
			}
			agentMode = header
		}
	}
	if a.useCodeExecutionMode {
		agentMode += "_code-exec"
	}

	baseName := fmt.Sprintf("%03d_%s_%s_%s_%s",
		seq,
		ts.Format("15-04-05"),
		sanitize(agentMode),
		sanitize(string(a.provider)),
		sanitize(a.modelID),
	)

	info := &promptLogInfo{Dir: dir, BaseName: baseName, Seq: seq}

	go func() {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return
		}

		// Write Markdown (system prompt + user message only — logged at start)
		var md strings.Builder
		md.WriteString(fmt.Sprintf("# Prompt #%d\n\n", seq))
		md.WriteString(fmt.Sprintf("- **Timestamp**: %s\n", ts.Format(time.RFC3339)))
		md.WriteString(fmt.Sprintf("- **Session**: %s\n", a.sessionID))
		md.WriteString(fmt.Sprintf("- **Provider**: %s\n", a.provider))
		md.WriteString(fmt.Sprintf("- **Model**: %s\n", a.modelID))
		md.WriteString(fmt.Sprintf("- **Messages**: %d\n\n", len(messages)))
		md.WriteString("---\n\n## System Prompt\n\n")
		md.WriteString(systemPrompt)
		md.WriteString("\n\n---\n\n## User Message\n\n")
		md.WriteString(userMessage)
		md.WriteString("\n")
		_ = os.WriteFile(filepath.Join(dir, baseName+".md"), []byte(md.String()), 0600)
		pruneAgentPromptLogSessions(sessionDir)
	}()

	return info
}

// logConversationEnd writes tool calls and responses to a separate file after the LLM turn completes.
// Uses the same directory and naming as logFinalPrompts with a "_conversation" suffix.
// toolLog contains tool call entries collected via EmitTypedEvent across providers.
func logConversationEnd(info *promptLogInfo, messages []llmtypes.MessageContent, startMsgCount int, toolLog []string) {
	if info == nil {
		return
	}
	go func() {
		_ = os.MkdirAll(info.Dir, 0700)

		var md strings.Builder
		md.WriteString(fmt.Sprintf("# Conversation #%d — Tool Calls & Responses\n\n", info.Seq))
		md.WriteString(fmt.Sprintf("- **Messages at start**: %d\n", startMsgCount))
		md.WriteString(fmt.Sprintf("- **Messages at end**: %d\n", len(messages)))
		md.WriteString(fmt.Sprintf("- **New messages**: %d\n", len(messages)-startMsgCount))
		md.WriteString(fmt.Sprintf("- **Tool events**: %d\n\n", len(toolLog)))
		md.WriteString("---\n\n")

		// Write tool call events (collected via EmitTypedEvent — works for ALL providers)
		if len(toolLog) > 0 {
			for _, entry := range toolLog {
				md.WriteString(entry)
				md.WriteString("\n")
			}
		} else {
			md.WriteString("*(no tool calls recorded)*\n")
		}

		_ = os.WriteFile(filepath.Join(info.Dir, info.BaseName+"_conversation.md"), []byte(md.String()), 0600)
		pruneAgentPromptLogSessions(filepath.Base(info.Dir))
	}()
}
