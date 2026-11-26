package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpcache"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/toolcalllog"
	"github.com/manishiitg/mcpagent/toolerr"
	"github.com/manishiitg/mcpagent/toolguard"
	"github.com/mark3labs/mcp-go/mcp"
)

func resolveCustomToolTimeout(tool string) time.Duration {
	if envVal := os.Getenv("TOOL_EXECUTION_TIMEOUT"); envVal != "" {
		if d, err := time.ParseDuration(envVal); err == nil {
			return d
		}
	}
	return 0
}

func toolExecutionError(layer, tool, sessionID string, timeout time.Duration, err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Sprintf("tool execution canceled: layer=%s tool=%s session=%s", layer, tool, sessionID)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("tool execution timed out: layer=%s tool=%s session=%s timeout=%s", layer, tool, sessionID, toolTimeoutString(timeout))
	default:
		return fmt.Sprintf("tool execution failed: layer=%s tool=%s session=%s: %v", layer, tool, sessionID, err)
	}
}

func toolTimeoutString(timeout time.Duration) string {
	if timeout <= 0 {
		return "none"
	}
	return timeout.String()
}

func contextWithOptionalTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, timeout)
}

// --- REQUEST/RESPONSE TYPES ---

// MCPExecuteRequest represents a request to execute an MCP tool
type MCPExecuteRequest struct {
	Server    string                 `json:"server"`               // MCP server name (e.g., "aws", "gdrive")
	Tool      string                 `json:"tool"`                 // Tool name (e.g., "list_buckets")
	Args      map[string]interface{} `json:"args"`                 // Tool arguments
	SessionID string                 `json:"session_id,omitempty"` // Optional: MCP session ID for connection reuse
}

// MCPExecuteResponse represents the response from an MCP tool execution
type MCPExecuteResponse struct {
	Success bool               `json:"success"`
	Result  string             `json:"result,omitempty"`
	Error   string             `json:"error,omitempty"`
	Images  []mcp.ImageContent `json:"images,omitempty"`
}

// CustomExecuteRequest represents a request to execute a custom tool
type CustomExecuteRequest struct {
	Tool      string                 `json:"tool"`                 // Tool name (e.g., "read_workspace_file")
	Args      map[string]interface{} `json:"args"`                 // Tool arguments
	SessionID string                 `json:"session_id,omitempty"` // Optional: Session ID for scoping custom tools (prevents cross-workflow contamination)
}

// CustomExecuteResponse represents the response from a custom tool execution
type CustomExecuteResponse struct {
	Success bool              `json:"success"`
	Result  string            `json:"result,omitempty"`
	Data    map[string]string `json:"data,omitempty"`
	Error   string            `json:"error,omitempty"`
}

var executionIDPattern = regexp.MustCompile(`(?m)execution_id"?\s*[:=]\s*"?([A-Za-z0-9._:/-]+)`)

func customToolResponseData(result string) map[string]string {
	match := executionIDPattern.FindStringSubmatch(result)
	if len(match) != 2 || match[1] == "" {
		return nil
	}
	return map[string]string{"execution_id": match[1]}
}

// VirtualExecuteRequest represents a request to execute a virtual tool
type VirtualExecuteRequest struct {
	Tool      string                 `json:"tool"`                 // Tool name (e.g., "discover_code_structure")
	Args      map[string]interface{} `json:"args"`                 // Tool arguments
	SessionID string                 `json:"session_id,omitempty"` // Optional: Session ID for scoping virtual tools (prevents cross-workflow contamination)
}

// VirtualExecuteResponse represents the response from a virtual tool execution
type VirtualExecuteResponse struct {
	Success bool   `json:"success"`
	Result  string `json:"result,omitempty"`
	Error   string `json:"error,omitempty"`
}

// --- EXECUTOR HANDLERS ---

// ResolvedMCPServer is an authoritative, current server selection supplied by
// the host. Returning a resolution bypasses process-wide fallback registries.
type ResolvedMCPServer struct {
	Name                string
	Config              mcpclient.MCPServerConfig
	ConnectionSessionID string
}

type MCPServerResolver func(context.Context, string, string, string) (*ResolvedMCPServer, error)

// ExecutorHandlers provides HTTP handlers for tool execution endpoints.
// Use NewExecutorHandlers to create and attach to your HTTP mux.
type ExecutorHandlers struct {
	mcpServerResolver MCPServerResolver
	configPath        string
	logger            loggerv2.Logger
	// toolArgTransformers maps tool names to functions that mutate their arguments in-place
	// before execution. This is the HTTP handler path (backup) — the primary interception
	// happens in agent/conversation.go for agent-internal tool calls.
	// Example: resolving workspace-relative file paths for an MCP tool.
	toolArgTransformers map[string]func(args map[string]interface{})
}

// SetMCPServerResolver installs a host scope resolver before handlers are served.
// A nil result delegates to legacy connection resolution; errors deny execution.
func (h *ExecutorHandlers) SetMCPServerResolver(resolve MCPServerResolver) {
	h.mcpServerResolver = resolve
}

// SetToolArgTransformer registers a function that mutates tool arguments in-place
// before the tool is executed. This covers the HTTP /api/mcp/execute path.
// For agent-internal tool calls (the primary path), use Agent.SetToolArgTransformer instead.
func (h *ExecutorHandlers) SetToolArgTransformer(toolName string, fn func(args map[string]interface{})) {
	if h.toolArgTransformers == nil {
		h.toolArgTransformers = make(map[string]func(args map[string]interface{}))
	}
	h.toolArgTransformers[toolName] = fn
}

// NewExecutorHandlers creates a new ExecutorHandlers instance.
// configPath: path to the MCP servers configuration file
// logger: logger instance for logging
func NewExecutorHandlers(configPath string, logger loggerv2.Logger) *ExecutorHandlers {
	if logger == nil {
		logger = loggerv2.NewNoop()
	}
	return &ExecutorHandlers{
		configPath: configPath,
		logger:     logger,
	}
}

// HandleMCPExecute handles the /api/mcp/execute endpoint
// POST /api/mcp/execute
// Body: {"server": "aws", "tool": "list_buckets", "args": {...}}
// Response: {"success": true, "result": "..."}
func (h *ExecutorHandlers) HandleMCPExecute(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !applyExecutorCORS(w, r) {
		return
	}

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse request
	var req MCPExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Warn("Failed to decode MCP execute request", loggerv2.Error(err))
		_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	h.logger.Info("🔧 MCP Execute request",
		loggerv2.String("server", req.Server),
		loggerv2.String("tool", req.Tool),
		loggerv2.String("session_id", req.SessionID))

	// Validate request
	if req.Server == "" {
		_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   "server parameter is required",
		})
		return
	}

	if req.Tool == "" {
		_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   "tool parameter is required",
		})
		return
	}

	// The request context is authoritative. Durable asynchronous child executions
	// return an execution ID immediately; there is no reason to detach the bridge
	// request from parent cancellation while starting them.
	toolTimeout := resolveCustomToolTimeout(req.Tool)
	ctx, cancel := contextWithOptionalTimeout(r.Context(), toolTimeout)
	defer cancel()

	// Apply tool argument transformers before ANY execution path (session registry, codeexec, mcpcache).
	// This mutates req.Args in-place so all downstream paths see the transformed values.
	// Example: resolves "Downloads/file.pdf" to an absolute host path for a tool
	// that cannot interpret workspace-relative paths.
	if h.toolArgTransformers != nil {
		if transformer, ok := h.toolArgTransformers[req.Tool]; ok {
			h.logger.Info("[TOOL_ARGS] Applying tool arg transformer before execution",
				loggerv2.String("tool", req.Tool))
			transformer(req.Args)
		}
	}

	// 🛑 STOPPED SESSION GUARD: If this session was stopped (workflow ended/failed),
	// refuse in-flight code-exec calls before they can recreate MCP processes.
	if req.SessionID != "" {
		registry := mcpclient.GetSessionRegistry()
		if registry.IsSessionStopped(req.SessionID) {
			h.logger.Info("🛑 [STOPPED SESSION] Refusing MCP tool call for stopped session",
				loggerv2.String("session_id", req.SessionID),
				loggerv2.String("server", req.Server),
				loggerv2.String("tool", req.Tool))
			_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec
				Success: false,
				Error:   fmt.Sprintf("Session %s was stopped — refusing to create new %s connection", req.SessionID, req.Server),
			})
			return
		}
	}

	// 🔧 STRATEGY: Try multiple connection sources in priority order
	// 1. Session registry (if session_id provided) - enables connection reuse
	// 2. Codeexec global registry - has session-aware connections from agent initialization
	// 3. mcpcache - creates a new connection as fallback

	var client mcpclient.ClientInterface
	var err error

	var resolved *ResolvedMCPServer
	if h.mcpServerResolver != nil {
		resolved, err = h.mcpServerResolver(ctx, req.SessionID, req.Server, req.Tool)
		if err != nil {
			_ = json.NewEncoder(w).Encode(MCPExecuteResponse{Success: false, Error: err.Error()})
			return
		}
		if resolved != nil {
			req.Server = resolved.Name
			client, _, err = mcpclient.GetSessionRegistry().GetOrCreateConnection(ctx, resolved.ConnectionSessionID, resolved.Name, resolved.Config, h.logger)
			if err != nil {
				_ = json.NewEncoder(w).Encode(MCPExecuteResponse{Success: false, Error: fmt.Sprintf("Failed to connect to server %s: %v", req.Server, err)})
				return
			}
		}
	}

	// PRIORITY 1: If session_id is provided, try session registry first
	// This is the primary mechanism for connection reuse.
	if client == nil && req.SessionID != "" {
		registry := mcpclient.GetSessionRegistry()
		connSessionID := registry.ResolveConnectionSessionID(req.SessionID, req.Server)

		// Debug: List all available sessions
		allSessions := registry.ListSessions()
		h.logger.Info("🔍 [SESSION DEBUG] Available sessions in registry",
			loggerv2.String("requested_session_id", req.SessionID),
			loggerv2.String("resolved_connection_session_id", connSessionID),
			loggerv2.Any("all_sessions", allSessions),
			loggerv2.Int("session_count", len(allSessions)))

		sessionConns := registry.GetSessionConnections(connSessionID)

		// Debug: List all connections for this session
		var connServers []string
		for serverName := range sessionConns {
			connServers = append(connServers, serverName)
		}
		h.logger.Info("🔍 [SESSION DEBUG] Connections for session",
			loggerv2.String("session_id", connSessionID),
			loggerv2.Any("available_servers", connServers),
			loggerv2.String("requested_server", req.Server))

		if existingClient, exists := sessionConns[req.Server]; exists {
			h.logger.Info("✅ Using session registry connection (session-aware)",
				loggerv2.String("session_id", req.SessionID),
				loggerv2.String("server", req.Server))
			client = existingClient
		} else if serverConfig, hasConfig := registry.GetServerConfig(req.SessionID, req.Server); hasConfig {
			// Lazy connect: first actual tool call to this server — connect now
			h.logger.Info("⚡ [LAZY] First tool call to "+req.Server+" — connecting now",
				loggerv2.String("session_id", req.SessionID))
			lazyClient, _, lazyErr := registry.GetOrCreateConnection(ctx, connSessionID, req.Server, serverConfig, h.logger)
			if lazyErr != nil {
				h.logger.Error("Lazy connect failed for server "+req.Server, lazyErr)
			} else {
				client = lazyClient
			}
		} else if connSessionID != req.SessionID {
			// Fallback: config may be stored under the resolved shared connection ID.
			if serverConfig, hasConfig := registry.GetServerConfig(connSessionID, req.Server); hasConfig {
				h.logger.Info("⚡ [LAZY] First tool call to "+req.Server+" — connecting via shared session config",
					loggerv2.String("session_id", req.SessionID),
					loggerv2.String("connection_session_id", connSessionID))
				lazyClient, _, lazyErr := registry.GetOrCreateConnection(ctx, connSessionID, req.Server, serverConfig, h.logger)
				if lazyErr != nil {
					h.logger.Error("Lazy connect failed for server "+req.Server+" (shared session config)", lazyErr)
				} else {
					client = lazyClient
				}
			} else {
				h.logger.Info("🔄 Session registry miss, trying codeexec registry",
					loggerv2.String("session_id", req.SessionID),
					loggerv2.String("server", req.Server),
					loggerv2.Any("available_servers", connServers))
			}
		} else {
			h.logger.Info("🔄 Session registry miss, trying codeexec registry",
				loggerv2.String("session_id", req.SessionID),
				loggerv2.String("server", req.Server),
				loggerv2.Any("available_servers", connServers))
		}
	} else {
		h.logger.Info("⚠️ No session_id provided in request, skipping session registry")
	}

	// PRIORITY 2: Try codeexec global registry if no session connection found
	if client == nil {
		// The global clients are whichever agent registered last, with that
		// agent's user's credentials, so no call uses them: a session call
		// gets its own session's connection (above), and any other call the
		// shared-config cache below, never another user's connection
		// (AgentWorks PLAT-362 D3).

		// 🔒 SCOPE ENFORCEMENT: If the session registry is active (has MCP clients) but
		// the requested server is not in scope, deny the request instead of spawning a
		// new process. This prevents agents from reaching MCP servers that are not
		// configured for the current workflow.
		if client == nil && req.SessionID != "" && !codeexec.IsServerInScope(req.Server) {
			availableServers := codeexec.ScopedServerNames()
			h.logger.Warn("🔒 [SCOPE DENIED] Server not in session scope, refusing mcpcache fallback",
				loggerv2.String("server", req.Server),
				loggerv2.String("session_id", req.SessionID),
				loggerv2.Any("available_servers", availableServers))
			_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec
				Success: false,
				Error:   fmt.Sprintf("Server '%s' is not available in this session's scope. The workflow does not have access to this MCP server. Available servers: %v", req.Server, availableServers),
			})
			return
		}
	}

	// PRIORITY 3: Fall back to mcpcache (creates new connection).
	if client == nil {
		h.logger.Warn("⚠️ [SESSION MISS] Falling back to mcpcache - creating new connection",
			loggerv2.String("server", req.Server),
			loggerv2.String("session_id", req.SessionID))
		client, err = GetOrCreateMCPClient(ctx, req.Server, h.configPath, h.logger)
		if err != nil {
			h.logger.Error("Failed to get MCP client", err, loggerv2.String("server", req.Server))
			_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
				Success: false,
				Error:   fmt.Sprintf("Failed to connect to server %s: %v", req.Server, err),
			})
			return
		}
		h.logger.Warn("⚠️ [SESSION MISS] Created new connection via mcpcache",
			loggerv2.String("server", req.Server))
		// Store the new connection in the session registry so future requests reuse it
		if req.SessionID != "" {
			registry := mcpclient.GetSessionRegistry()
			connSessionID := registry.ResolveConnectionSessionID(req.SessionID, req.Server)
			registry.StoreConnection(connSessionID, req.Server, client)
			h.logger.Info("✅ [SESSION MISS] Stored mcpcache connection in session registry for reuse",
				loggerv2.String("server", req.Server),
				loggerv2.String("session_id", connSessionID))
		}
	}

	// The application's tool guard sees the call before it runs and may answer
	// it with a stub instead (test mode, PLAT-562).
	if guarded := toolguard.Check(ctx, toolguard.Call{
		SessionID:   req.SessionID,
		Kind:        toolguard.KindMCP,
		Server:      req.Server,
		Tool:        req.Tool,
		Args:        req.Args,
		Annotations: toolguard.AnnotationsFrom(client, req.Tool),
	}); guarded.Stub {
		resp := MCPExecuteResponse{Success: !guarded.IsError, Result: guarded.Result}
		if guarded.IsError {
			resp.Error = guarded.Result
		}
		_ = json.NewEncoder(w).Encode(resp) //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
		return
	}

	// Execute tool
	var argsJSON []byte
	var toolCallID string
	if req.SessionID != "" {
		argsJSON, _ = json.Marshal(req.Args)
		toolCallID = toolcalllog.RecordStart(req.SessionID, req.Tool, string(argsJSON))
	}
	h.logger.Info("🚀 Executing tool via direct connection",
		loggerv2.String("tool", req.Tool),
		loggerv2.String("server", req.Server))
	mcpToolStartTime := time.Now()
	h.logger.Info(fmt.Sprintf("⏱️  TOOL EXECUTION START - Time: %s, Tool: %s, Server: %s", mcpToolStartTime.Format(time.RFC3339), req.Tool, req.Server))
	result, err := client.CallTool(ctx, req.Tool, req.Args)
	if err != nil && strings.Contains(req.Tool, "_") {
		// The per-tool HTTP routes and the API spec carry the sanitized tool name (hyphens
		// became underscores, lowercased), but the server only knows its real name
		// (notion-fetch). Ask for the server's tools once and call the real name.
		if tools, listErr := client.ListTools(ctx); listErr == nil {
			if real, ok := realToolName(tools, req.Tool); ok {
				h.logger.Info("🔧 [TOOL NAME] retrying with the server's own tool name",
					loggerv2.String("asked", req.Tool), loggerv2.String("real", real), loggerv2.String("server", req.Server))
				if retryResult, retryErr := client.CallTool(ctx, real, req.Args); retryErr == nil {
					result, err, req.Tool = retryResult, nil, real
				} else {
					h.logger.Error("🔧 [TOOL NAME] retry with the server's own tool name failed", retryErr,
						loggerv2.String("asked", req.Tool), loggerv2.String("real", real), loggerv2.String("server", req.Server))
				}
			}
		}
	}

	// 🔧 BROKEN PIPE DETECTION AND RETRY
	// When a workflow is stopped, MCP connections are closed which causes "transport closed"
	// errors. We must NOT retry in that case — otherwise sub-agents continue as zombies.
	if err != nil && mcpclient.IsBrokenPipeError(err) {
		// Guard: skip retry if context is canceled (intentional stop, not transient error)
		if ctx.Err() != nil {
			h.logger.Info("🔧 [BROKEN PIPE] Skipping retry — context canceled (intentional stop)",
				loggerv2.String("tool", req.Tool),
				loggerv2.String("server", req.Server),
				loggerv2.String("ctx_err", ctx.Err().Error()))
		} else if req.SessionID != "" && mcpclient.GetSessionRegistry().IsSessionStopped(req.SessionID) {
			// Guard: skip retry if the session was stopped via CloseHTTPSession
			h.logger.Info("🔧 [BROKEN PIPE] Skipping retry — session stopped (zombie prevention)",
				loggerv2.String("tool", req.Tool),
				loggerv2.String("server", req.Server),
				loggerv2.String("session_id", req.SessionID))
		} else {
			h.logger.Info("🔧 [BROKEN PIPE] Detected, closing old connection and getting fresh one...",
				loggerv2.String("tool", req.Tool),
				loggerv2.String("server", req.Server))

			// Close the old broken connection first to kill the subprocess.
			if client != nil {
				h.logger.Info("🔧 [BROKEN PIPE] Closing old broken connection",
					loggerv2.String("server", req.Server))
				_ = client.Close()
			}

			// Also remove the stale connection from the session registry.
			if req.SessionID != "" {
				registry := mcpclient.GetSessionRegistry()
				connSessionID := registry.ResolveConnectionSessionID(req.SessionID, req.Server)
				if resolved != nil {
					connSessionID = resolved.ConnectionSessionID
				}
				registry.CloseSessionServer(connSessionID, req.Server)
			}

			var freshClient mcpclient.ClientInterface
			var freshErr error
			if resolved != nil {
				freshClient, _, freshErr = mcpclient.GetSessionRegistry().GetOrCreateConnection(ctx, resolved.ConnectionSessionID, resolved.Name, resolved.Config, h.logger)
			} else {
				freshClient, freshErr = mcpcache.GetFreshConnection(ctx, req.Server, h.configPath, h.logger)
			}
			if freshErr == nil {
				h.logger.Info("🔧 [BROKEN PIPE] Retrying with fresh connection...",
					loggerv2.String("tool", req.Tool))
				result, err = freshClient.CallTool(ctx, req.Tool, req.Args)
				if err == nil {
					h.logger.Info("🔧 [BROKEN PIPE] Retry successful",
						loggerv2.String("tool", req.Tool))
				} else {
					h.logger.Error("🔧 [BROKEN PIPE] Retry failed", err,
						loggerv2.String("tool", req.Tool))
				}
			} else {
				h.logger.Error("🔧 [BROKEN PIPE] Failed to get fresh connection", freshErr,
					loggerv2.String("server", req.Server))
			}
		}
	}

	h.logger.Info(fmt.Sprintf("⏱️  TOOL EXECUTION END - Time: %s, Tool: %s, Server: %s, Duration: %v", time.Now().Format(time.RFC3339), req.Tool, req.Server, time.Since(mcpToolStartTime)))
	if err != nil {
		// Same marker and envelope as the custom and virtual handlers. This path
		// serves every MCP server tool, and it previously logged a bare
		// "Tool execution failed" with no marker and built its own error string
		// without `layer=` — so MCP failures were absent from
		// `grep '\[TOOL_ERROR\]'` and did not match the UI's harness-error
		// detector either. Both surfaces were blind to this entire tool category.
		h.logger.Error(toolerr.Marker+" mcp tool failed", err,
			loggerv2.String("layer", "mcp_tool_handler"),
			loggerv2.String("tool", req.Tool),
			loggerv2.String("server", req.Server),
			loggerv2.String("session_id", req.SessionID),
			loggerv2.String("duration", time.Since(mcpToolStartTime).String()),
			loggerv2.String("args", toolerr.TruncateArgsForLog(string(argsJSON))))
		errorText := toolExecutionError("mcp_tool_handler", req.Tool, req.SessionID, toolTimeout, err)
		if req.SessionID != "" {
			toolcalllog.RecordEnd(req.SessionID, toolCallID, req.Tool, string(argsJSON), errorText, mcpToolStartTime)
		}
		_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   errorText,
		})
		return
	}

	// Convert result to string
	resultStr := ConvertMCPResultToString(result)

	// 🔧 BROKEN PIPE DETECTION IN RESULT CONTENT
	// MCP servers sometimes return broken pipe errors as text content (success=true, err=nil).
	// The err != nil check above won't catch these — check the result text too.
	if err == nil && mcpclient.IsBrokenPipeInContent(resultStr) {
		// Same guards as above: skip retry if context canceled or session stopped
		if ctx.Err() != nil {
			h.logger.Info("🔧 [BROKEN PIPE IN CONTENT] Skipping retry — context canceled",
				loggerv2.String("tool", req.Tool),
				loggerv2.String("server", req.Server))
		} else if req.SessionID != "" && mcpclient.GetSessionRegistry().IsSessionStopped(req.SessionID) {
			h.logger.Info("🔧 [BROKEN PIPE IN CONTENT] Skipping retry — session stopped",
				loggerv2.String("tool", req.Tool),
				loggerv2.String("server", req.Server))
		} else {
			h.logger.Info("🔧 [BROKEN PIPE IN CONTENT] Detected broken pipe in result text, closing and retrying...",
				loggerv2.String("tool", req.Tool),
				loggerv2.String("server", req.Server),
				loggerv2.String("result_snippet", resultStr[:min(len(resultStr), 200)]))

			if client != nil {
				_ = client.Close()
			}
			if req.SessionID != "" {
				registry := mcpclient.GetSessionRegistry()
				connSessionID := registry.ResolveConnectionSessionID(req.SessionID, req.Server)
				if resolved != nil {
					connSessionID = resolved.ConnectionSessionID
				}
				registry.CloseSessionServer(connSessionID, req.Server)
			}

			var freshClient mcpclient.ClientInterface
			var freshErr error
			if resolved != nil {
				freshClient, _, freshErr = mcpclient.GetSessionRegistry().GetOrCreateConnection(ctx, resolved.ConnectionSessionID, resolved.Name, resolved.Config, h.logger)
			} else {
				freshClient, freshErr = mcpcache.GetFreshConnection(ctx, req.Server, h.configPath, h.logger)
			}
			if freshErr == nil {
				if resolved == nil {
					defer freshClient.Close()
				} //nolint:errcheck
				h.logger.Info("🔧 [BROKEN PIPE IN CONTENT] Retrying with fresh connection...",
					loggerv2.String("tool", req.Tool))
				retryResult, retryErr := freshClient.CallTool(ctx, req.Tool, req.Args)
				if retryErr == nil {
					result = retryResult
					resultStr = ConvertMCPResultToString(retryResult)
					h.logger.Info("🔧 [BROKEN PIPE IN CONTENT] Retry successful",
						loggerv2.String("tool", req.Tool))
				} else {
					h.logger.Error("🔧 [BROKEN PIPE IN CONTENT] Retry failed", retryErr,
						loggerv2.String("tool", req.Tool))
				}
			} else {
				h.logger.Error("🔧 [BROKEN PIPE IN CONTENT] Failed to get fresh connection", freshErr,
					loggerv2.String("server", req.Server))
			}
		}
	}

	h.logger.Info("✅ Tool executed successfully",
		loggerv2.String("tool", req.Tool),
		loggerv2.Int("result_length", len(resultStr)))

	// The transport says OK; the payload may not. This is the exact class that
	// produced 34 green-checked failures in a day — a shell denial or harness
	// envelope carried as content under exit_code 0. Over-matching is intended.
	if signal, suspicious := toolerr.SuspiciousForTool(req.Tool, resultStr); suspicious {
		h.logger.Error(toolerr.SuspectMarker+" mcp tool reported success but the result reads like a failure", nil,
			loggerv2.String("layer", "mcp_tool_handler"),
			loggerv2.String("tool", req.Tool),
			loggerv2.String("server", req.Server),
			loggerv2.String("session_id", req.SessionID),
			loggerv2.String("signal", signal),
			loggerv2.String("args", toolerr.TruncateArgsForLog(string(argsJSON))),
			loggerv2.String("result", toolerr.TruncateForLog(resultStr)))
	}

	// Record completed call so LLMAgentWrapper can reconstruct history on cancellation.
	if req.SessionID != "" {
		toolcalllog.RecordEnd(req.SessionID, toolCallID, req.Tool, string(argsJSON), resultStr, mcpToolStartTime)
	}

	// Return success response
	_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
		Success: true,
		Result:  resultStr,
		Images:  mcpResultImages(result),
	})
}

// HandleCustomExecute handles the /api/custom/execute endpoint
// POST /api/custom/execute
// Body: {"tool": "read_workspace_file", "args": {...}}
// Response: {"success": true, "result": "..."}
func (h *ExecutorHandlers) HandleCustomExecute(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !applyExecutorCORS(w, r) {
		return
	}

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse request
	var req CustomExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Warn("Failed to decode custom execute request", loggerv2.Error(err))
		_ = json.NewEncoder(w).Encode(CustomExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	h.logger.Info("🔧 Custom Execute request",
		loggerv2.String("tool", req.Tool),
		loggerv2.String("session_id", req.SessionID))

	// Validate request
	if req.Tool == "" {
		_ = json.NewEncoder(w).Encode(CustomExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   "tool parameter is required",
		})
		return
	}

	// Preserve request cancellation. Async delegation tools return quickly and
	// persist their child lifecycle separately; detaching here would let a stopped
	// parent continue launching work.
	toolTimeout := resolveCustomToolTimeout(req.Tool)
	ctx, cancel := contextWithOptionalTimeout(r.Context(), toolTimeout)
	defer cancel()
	ctx = WithSessionID(ctx, req.SessionID)

	// Execute custom tool using codeexec registry (session-scoped to prevent cross-workflow contamination)
	var argsJSON []byte
	var toolCallID string
	var toolStartedAt time.Time
	if req.SessionID != "" {
		argsJSON, _ = json.Marshal(req.Args)
		toolStartedAt = time.Now()
		toolCallID = toolcalllog.RecordStart(req.SessionID, req.Tool, string(argsJSON))
	}
	h.logger.Info("🚀 Executing custom tool",
		loggerv2.String("tool", req.Tool),
		loggerv2.String("session_id", req.SessionID))
	toolStartTime := time.Now()
	h.logger.Info(fmt.Sprintf("⏱️  TOOL EXECUTION START - Time: %s, Tool: %s", toolStartTime.Format(time.RFC3339), req.Tool))
	result, err := codeexec.CallCustomToolWithSession(ctx, req.SessionID, req.Tool, req.Args)
	toolDuration := time.Since(toolStartTime)
	h.logger.Info(fmt.Sprintf("⏱️  TOOL EXECUTION END - Time: %s, Tool: %s, Duration: %v", time.Now().Format(time.RFC3339), req.Tool, toolDuration))
	if err != nil {
		// One greppable marker with everything needed to locate the call. These
		// failures reach the model as stdout with exit_code 0, so they are
		// invisible unless the log says so plainly: a day of rollouts on
		// 2026-08-01 held 34 of them and every one rendered as a success.
		// `grep '\[TOOL_ERROR\]'` should be the whole debugging story.
		h.logger.Error(toolerr.Marker+" custom tool failed", err,
			loggerv2.String("layer", "custom_tool_handler"),
			loggerv2.String("tool", req.Tool),
			loggerv2.String("session_id", req.SessionID),
			loggerv2.String("duration", toolDuration.String()),
			loggerv2.String("args", toolerr.TruncateArgsForLog(string(argsJSON))))
		errorText := toolExecutionError("custom_tool_handler", req.Tool, req.SessionID, toolTimeout, err)
		if req.SessionID != "" {
			toolcalllog.RecordEnd(req.SessionID, toolCallID, req.Tool, string(argsJSON), errorText, toolStartedAt)
		}
		_ = json.NewEncoder(w).Encode(CustomExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   errorText,
		})
		return
	}

	h.logger.Info("✅ Custom tool executed successfully",
		loggerv2.String("tool", req.Tool),
		loggerv2.Int("result_length", len(result)))

	// This handler is the one path that never applied the canonical check —
	// agent/conversation.go, agent/parallel_tool_execution.go, and
	// agent/llm_generation.go all call CanonicalFailureForTool where they call
	// SuspiciousForTool; this file only ever called the latter. Every custom/
	// bridge tool a workflow step runs (execute_shell_command chief among them)
	// goes through here, so Success was unconditionally true regardless of
	// payload content unless the Go call itself errored — a shell command
	// returning a real nonzero exit_code was reported exactly the same as a
	// clean one. Measured live: 868 SuspiciousForTool hits from this layer alone
	// in 8 hours, none of them able to change what the caller saw.
	//
	// CanonicalFailureForTool already carries the same per-tool suppression
	// (problemReportingTools) that keeps content-bearing tools like read_skill
	// and query_workflow_db from being misclassified, so nothing further is
	// needed here to stay safe for those.
	success := true
	errorText := ""
	if signal, failed := toolerr.CanonicalFailureForTool(req.Tool, result); failed {
		success = false
		errorText = fmt.Sprintf("tool execution failed: %s", signal)
		h.logger.Error(toolerr.Marker+" custom tool payload failure", nil,
			loggerv2.String("layer", "custom_tool_handler"),
			loggerv2.String("tool", req.Tool),
			loggerv2.String("session_id", req.SessionID),
			loggerv2.String("signal", signal),
			loggerv2.String("args", toolerr.TruncateArgsForLog(string(argsJSON))),
			loggerv2.String("result", toolerr.TruncateForLog(result)))
	} else if signal, suspicious := toolerr.SuspiciousForTool(req.Tool, result); suspicious {
		h.logger.Error(toolerr.SuspectMarker+" custom tool reported success but the result reads like a failure", nil,
			loggerv2.String("layer", "custom_tool_handler"),
			loggerv2.String("tool", req.Tool),
			loggerv2.String("session_id", req.SessionID),
			loggerv2.String("signal", signal),
			loggerv2.String("args", toolerr.TruncateArgsForLog(string(argsJSON))),
			loggerv2.String("result", toolerr.TruncateForLog(result)))
	}

	// Record completed call so LLMAgentWrapper can reconstruct history on cancellation.
	if req.SessionID != "" {
		toolcalllog.RecordEnd(req.SessionID, toolCallID, req.Tool, string(argsJSON), result, toolStartedAt)
	}

	_ = json.NewEncoder(w).Encode(CustomExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
		Success: success,
		Result:  result,
		Data:    customToolResponseData(result),
		Error:   errorText,
	})
}

// HandleVirtualExecute handles the /api/virtual/execute endpoint
// POST /api/virtual/execute
// Body: {"tool": "discover_code_structure", "args": {...}}
// Response: {"success": true, "result": "..."}
func (h *ExecutorHandlers) HandleVirtualExecute(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !applyExecutorCORS(w, r) {
		return
	}

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse request
	var req VirtualExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Warn("Failed to decode virtual execute request", loggerv2.Error(err))
		_ = json.NewEncoder(w).Encode(VirtualExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	h.logger.Info("🔧 Virtual Execute request",
		loggerv2.String("tool", req.Tool),
		loggerv2.String("session_id", req.SessionID))

	// Validate request
	if req.Tool == "" {
		_ = json.NewEncoder(w).Encode(VirtualExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   "tool parameter is required",
		})
		return
	}

	// Preserve request cancellation for virtual tools as well. This keeps Stop
	// behavior identical regardless of whether a tool is exposed as custom or
	// virtual in the coding-agent bridge.
	toolTimeout := resolveCustomToolTimeout(req.Tool)
	ctx, cancel := contextWithOptionalTimeout(r.Context(), toolTimeout)
	defer cancel()

	// Execute virtual tool using codeexec registry (session-scoped to prevent cross-workflow contamination)
	h.logger.Info("🚀 Executing virtual tool",
		loggerv2.String("tool", req.Tool),
		loggerv2.String("session_id", req.SessionID))
	result, err := codeexec.CallVirtualToolWithSession(ctx, req.SessionID, req.Tool, req.Args)
	if err != nil {
		// Same marker as the custom path. get_api_spec lives here, and it was the
		// single largest source of failed tool calls in the system while being
		// completely invisible in logs and UI alike.
		argsForLog, _ := json.Marshal(req.Args)
		h.logger.Error(toolerr.Marker+" virtual tool failed", err,
			loggerv2.String("layer", "virtual_tool_handler"),
			loggerv2.String("tool", req.Tool),
			loggerv2.String("session_id", req.SessionID),
			loggerv2.String("args", toolerr.TruncateArgsForLog(string(argsForLog))))
		errorText := toolExecutionError("virtual_tool_handler", req.Tool, req.SessionID, toolTimeout, err)
		_ = json.NewEncoder(w).Encode(VirtualExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
			Success: false,
			Error:   errorText,
		})
		return
	}

	h.logger.Info("✅ Virtual tool executed successfully",
		loggerv2.String("tool", req.Tool),
		loggerv2.Int("result_length", len(result)))

	// Return success response
	_ = json.NewEncoder(w).Encode(VirtualExecuteResponse{ //nolint:gosec // JSON encoding errors are non-critical in HTTP handlers
		Success: true,
		Result:  result,
	})
}

// truncateForLog bounds a value going into a log line. Tool arguments are the
// most useful field for reproducing a failure and also the most likely to be
// enormous — a prompt, a file body, a SQL result. Logging them whole turns one
// failure into an unreadable log; logging none of them means the line says a
// tool failed without saying what was asked of it.

// realToolName maps a name an agent was given (sanitized by a route, or the model-facing
// <alias>__<tool>) back to the name the server lists. It answers only when the asked name is not
// itself a listed tool and exactly one listed tool matches.
func realToolName(tools []mcp.Tool, asked string) (string, bool) {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.Name == asked {
			return "", false
		}
		names = append(names, tool.Name)
	}
	if real, _ := mcpclient.MatchToolName(asked, "", names, false); real != "" {
		return real, true
	}
	if _, word, ok := strings.Cut(asked, mcpclient.ToolNameSeparator); ok {
		real, _ := mcpclient.MatchToolName(word, "", names, false)
		return real, real != ""
	}
	return "", false
}
