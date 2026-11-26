// error_handler.go
//
// This file contains error handling strategies for the Agent, including broken pipe recovery,
// connection error handling, and other error recovery mechanisms.
//
// Exported:
//   - BrokenPipeHandler
//   - NewBrokenPipeHandler
//   - IsBrokenPipeError

package mcpagent

import (
	"context"
	"fmt"
	"time"

	"github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/mark3labs/mcp-go/mcp"
)

// BrokenPipeHandler handles broken pipe errors by recreating connections and retrying operations
type BrokenPipeHandler struct {
	agent  *Agent
	logger loggerv2.Logger
}

// NewBrokenPipeHandler creates a new broken pipe handler
func newBrokenPipeHandler(agent *Agent) *BrokenPipeHandler {
	return &BrokenPipeHandler{
		agent:  agent,
		logger: agent.logger,
	}
}

// IsBrokenPipeError checks if an error is a broken pipe error
// Delegates to mcpclient.IsBrokenPipeError for shared implementation
func IsBrokenPipeError(err error) bool {
	return mcpclient.IsBrokenPipeError(err)
}

// HandleBrokenPipeError handles broken pipe errors by recreating the connection and retrying.
//
// IMPORTANT: This method checks for intentional session stops before retrying.
// When a user stops a workflow, MCP connections are closed which causes "transport closed"
// errors that look like broken pipes. Without this check, the broken pipe handler would
// reconnect and the sub-agent would continue running as a zombie — ignoring the stop.
// See: zombie sub-agent bug where stopped workflows continued executing for 60+ seconds.
func (h *BrokenPipeHandler) HandleBrokenPipeError(
	ctx context.Context,
	toolCall *llmtypes.ToolCall,
	serverName string,
	originalErr error,
	startTime time.Time,
) (*mcp.CallToolResult, time.Duration, error) {

	// CHECK 1: If the context is already canceled, this is likely an intentional stop,
	// not a transient network error. Do NOT retry — return immediately so the agent
	// loop can detect the cancellation and exit cleanly.
	if ctx.Err() != nil {
		h.logger.Info("🔧 [BROKEN PIPE] Skipping retry — context already canceled (intentional stop)",
			loggerv2.String("tool", toolCall.FunctionCall.Name),
			loggerv2.String("server", serverName),
			loggerv2.String("ctx_err", ctx.Err().Error()))
		return nil, time.Since(startTime), fmt.Errorf("broken pipe during canceled context: %w", originalErr)
	}

	// CHECK 2: If the session was stopped via CloseHTTPSession, the registry marks
	// all associated MCP session IDs as "stopped". This catches the case where
	// the sub-agent's context is NOT derived from the workflow context (e.g., tool
	// calls dispatched via HTTP from claude-code CLI have independent contexts).
	registry := mcpclient.GetSessionRegistry()
	if registry.IsSessionStopped(h.agent.sessionID) {
		h.logger.Info("🔧 [BROKEN PIPE] Skipping retry — session was stopped (zombie prevention)",
			loggerv2.String("tool", toolCall.FunctionCall.Name),
			loggerv2.String("server", serverName),
			loggerv2.String("session_id", h.agent.sessionID))
		return nil, time.Since(startTime), fmt.Errorf("session stopped — broken pipe not retried: %w", originalErr)
	}

	h.logger.Info("Broken pipe detected, attempting connection recreation",
		loggerv2.String("tool", toolCall.FunctionCall.Name),
		loggerv2.String("server", serverName))

	// Emit broken pipe detection event
	h.emitBrokenPipeEvent(ctx, toolCall, serverName, originalErr)

	// Recreate through the registry so the replacement remains tracked and is
	// cleaned up with the rest of the session.
	freshClient, freshErr := h.recreateViaRegistry(ctx, serverName)

	if freshErr != nil {
		h.logger.Error(fmt.Sprintf("🔧 [BROKEN PIPE] Failed to create fresh connection: %v", freshErr), freshErr)
		return nil, time.Since(startTime), freshErr
	}

	// Update the agent's client map with the new connection
	// This ensures future tool calls use the new connection
	h.agent.clientsMu.Lock()
	h.agent.clients[serverName] = freshClient
	h.agent.clientsMu.Unlock()
	h.logger.Info(fmt.Sprintf("🔧 [BROKEN PIPE] Updated agent's client map with fresh connection for server: %s", serverName),
		loggerv2.String("server", serverName))

	// Retry the tool call once with the fresh connection
	return h.retryToolCall(ctx, toolCall, freshClient, serverName, startTime)
}

// recreateViaRegistry closes the stale connection in the session registry and
// creates a fresh one that the registry tracks. This prevents connection leaks
// because CloseAllSessions will close the replacement connection at shutdown.
func (h *BrokenPipeHandler) recreateViaRegistry(ctx context.Context, serverName string) (mcpclient.ClientInterface, error) {
	registry := mcpclient.GetSessionRegistry()
	connSessionID := registry.ResolveConnectionSessionID(h.agent.sessionID, serverName)

	// Atomically close AND remove the stale entry from the registry.
	h.logger.Info(fmt.Sprintf("🔧 [BROKEN PIPE] Closing stale registry entry for server: %s (session=%s)", serverName, connSessionID),
		loggerv2.String("server", serverName))
	registry.CloseSessionServer(connSessionID, serverName)

	// Load server config so we can pass it to GetOrCreateConnection: a server
	// outside the catalog reconnects from its own configuration.
	serverConfig, extra := h.agent.runtimeOverrides.ExtraServerConfig(serverName)
	if !extra {
		config, err := mcpclient.LoadMergedConfig(h.agent.configPath, h.logger)
		if err != nil {
			return nil, fmt.Errorf("failed to load MCP config for broken pipe recovery: %w", err)
		}
		serverConfig, err = config.GetServer(serverName)
		if err != nil {
			return nil, fmt.Errorf("server %s not found in config: %w", serverName, err)
		}
	}

	// Apply runtime overrides (matching connection_session.go:149-158)
	if h.agent.runtimeOverrides != nil && !extra {
		if override, hasOverride := h.agent.runtimeOverrides[serverName]; hasOverride {
			serverConfig = serverConfig.ApplyOverride(override)
		}
	}

	// Apply per-user OAuth token path (matching connection_session.go:161-168)
	if h.agent.userID != "" && serverConfig.OAuth != nil {
		serverConfig.OAuth.TokenFile = fmt.Sprintf("~/.config/mcpagent/tokens/%s/%s.json", h.agent.userID, serverName)
	}

	// Create a fresh connection tracked by the registry.
	// The per-key mutex inside the registry prevents concurrent broken pipe
	// handlers from spawning duplicate connections for the same server.
	client, _, err := registry.GetOrCreateConnection(ctx, connSessionID, serverName, serverConfig, h.logger)
	if err != nil {
		return nil, fmt.Errorf("registry GetOrCreateConnection failed: %w", err)
	}
	return client, nil
}

// retryToolCall retries a tool call with a fresh connection
func (h *BrokenPipeHandler) retryToolCall(
	ctx context.Context,
	toolCall *llmtypes.ToolCall,
	client mcpclient.ClientInterface,
	serverName string,
	startTime time.Time,
) (*mcp.CallToolResult, time.Duration, error) {

	// Final guard: context may have been canceled while we were reconnecting.
	// Check again before actually retrying the tool call.
	if ctx.Err() != nil {
		h.logger.Info("🔧 [BROKEN PIPE] Aborting retry — context canceled during reconnection",
			loggerv2.String("tool", toolCall.FunctionCall.Name),
			loggerv2.String("ctx_err", ctx.Err().Error()))
		return nil, time.Since(startTime), fmt.Errorf("context canceled before retry: %w", ctx.Err())
	}

	h.logger.Info("Retrying tool call with fresh connection",
		loggerv2.String("tool", toolCall.FunctionCall.Name))

	// Parse the tool arguments from JSON string to map
	retryArgs, parseErr := mcpclient.ParseToolArguments(toolCall.FunctionCall.Arguments)
	if parseErr != nil {
		h.logger.Error(fmt.Sprintf("🔧 [BROKEN PIPE] Failed to parse tool arguments: %v", parseErr), parseErr)
		return nil, time.Since(startTime), parseErr
	}

	// Create a timeout context for the retry
	retryCtx, retryCancel := context.WithTimeout(ctx, 30*time.Second)
	defer retryCancel()

	// Execute the retry with the server's registered name, not the qualified
	// LLM-facing name used to disambiguate duplicate tools.
	actualToolName := h.agent.realMCPToolName(toolCall.FunctionCall.Name, serverName)
	retryResult, retryErr := client.CallTool(retryCtx, actualToolName, retryArgs)
	retryDuration := time.Since(startTime)

	if retryErr == nil {
		h.logger.Info("Retry successful",
			loggerv2.String("tool", toolCall.FunctionCall.Name),
			loggerv2.String("duration", retryDuration.String()))
		h.emitRetrySuccessEvent(ctx, toolCall, serverName, retryDuration)
		return retryResult, retryDuration, nil
	}

	h.logger.Error("Retry failed", retryErr,
		loggerv2.String("tool", toolCall.FunctionCall.Name))
	h.emitRetryFailureEvent(ctx, toolCall, serverName, retryErr, retryDuration)
	return nil, retryDuration, retryErr
}

// emitBrokenPipeEvent emits a broken pipe detection event
func (h *BrokenPipeHandler) emitBrokenPipeEvent(ctx context.Context, toolCall *llmtypes.ToolCall, serverName string, originalErr error) {
	brokenPipeEvent := &events.BrokenPipeEvent{
		BaseEventData: events.BaseEventData{
			Timestamp: time.Now(),
		},
		Operation:  events.BrokenPipeOpDetected,
		ToolName:   toolCall.FunctionCall.Name,
		ServerName: serverName,
		ToolCallID: toolCall.ID,
		Error:      originalErr.Error(),
	}
	h.agent.emitTypedEvent(ctx, brokenPipeEvent)
}

// emitRetrySuccessEvent emits a successful retry event
func (h *BrokenPipeHandler) emitRetrySuccessEvent(ctx context.Context, toolCall *llmtypes.ToolCall, serverName string, duration time.Duration) {
	retrySuccessEvent := &events.BrokenPipeEvent{
		BaseEventData: events.BaseEventData{
			Timestamp: time.Now(),
		},
		Operation:  events.BrokenPipeOpRetrySuccess,
		ToolName:   toolCall.FunctionCall.Name,
		ServerName: serverName,
		ToolCallID: toolCall.ID,
		Duration:   duration.String(),
	}
	h.agent.emitTypedEvent(ctx, retrySuccessEvent)
}

// emitRetryFailureEvent emits a failed retry event
func (h *BrokenPipeHandler) emitRetryFailureEvent(ctx context.Context, toolCall *llmtypes.ToolCall, serverName string, retryErr error, duration time.Duration) {
	retryFailureEvent := &events.BrokenPipeEvent{
		BaseEventData: events.BaseEventData{
			Timestamp: time.Now(),
		},
		Operation:  events.BrokenPipeOpRetryFailure,
		ToolName:   toolCall.FunctionCall.Name,
		ServerName: serverName,
		ToolCallID: toolCall.ID,
		Duration:   duration.String(),
		Error:      retryErr.Error(),
	}
	h.agent.emitTypedEvent(ctx, retryFailureEvent)
}

// ErrorRecoveryHandler provides a unified interface for different error recovery strategies
type ErrorRecoveryHandler struct {
	brokenPipeHandler *BrokenPipeHandler
	logger            loggerv2.Logger
}

// NewErrorRecoveryHandler creates a new error recovery handler
func newErrorRecoveryHandler(agent *Agent) *ErrorRecoveryHandler {
	return &ErrorRecoveryHandler{
		brokenPipeHandler: newBrokenPipeHandler(agent),
		logger:            agent.logger,
	}
}

// HandleError attempts to recover from various types of errors
func (h *ErrorRecoveryHandler) HandleError(
	ctx context.Context,
	toolCall *llmtypes.ToolCall,
	serverName string,
	originalErr error,
	startTime time.Time,
	isCustomTool bool,
	isVirtualTool bool,
) (*mcp.CallToolResult, time.Duration, bool, error) {

	// Only handle errors for regular MCP tools (not custom or virtual tools)
	if isCustomTool || isVirtualTool {
		return nil, time.Since(startTime), false, originalErr
	}

	// Handle broken pipe errors
	if IsBrokenPipeError(originalErr) {
		result, duration, err := h.brokenPipeHandler.HandleBrokenPipeError(ctx, toolCall, serverName, originalErr, startTime)
		return result, duration, true, err
	}

	// No recovery strategy available for this error type
	return nil, time.Since(startTime), false, originalErr
}
