// connection_session.go
//
// This file contains session-aware MCP connection management.
// When SessionID is provided, connections are stored in SessionConnectionRegistry
// and reused across agents in the same session.
//
// Exported:
//   - NewAgentConnectionWithSession: Session-aware connection that uses registry for connection reuse.

package mcpagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpcache"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/observability"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/mark3labs/mcp-go/mcp"
)

// serverConnectionResult holds the per-server results from parallel connection + discovery.
type serverConnectionResult struct {
	serverName string
	client     mcpclient.ClientInterface
	tools      []llmtypes.Tool
	toolNames  []string // tool names in order, for toolToServer mapping
	toolAlias  string   // alias the host declared (MCPServerConfig.ToolAlias), if any
	wasCreated bool
	isLazy     bool // true = tools loaded from cache, connection deferred until first tool call
	mcpCount   int  // number of MCP tools discovered (for logging)
	err        error
}

// NewAgentConnectionWithSession creates MCP connections using the session registry.
// Connections are reused if they already exist for the session.
//
// When a connection already exists for (sessionID, serverName), it is reused.
// When a connection doesn't exist, it is created and stored in the registry.
//
// This function should be used when agents share connections within a workflow/conversation.
// Connections are NOT closed when the agent closes - call CloseSession(sessionID) at workflow end.
//
// Parameters:
//   - ctx: Context for connection timeout
//   - llm: LLM model for tool schema generation
//   - serverName: Server name(s) to connect to (comma-separated or "all")
//   - configPath: Path to MCP config file
//   - sessionID: Session ID for connection sharing
//   - traceID: Trace ID for observability
//   - tracers: Tracers for event emission
//   - logger: Logger for logging
//   - disableCache: If true, skip cache lookup for tool metadata (connections still reused via registry)
//   - userID: User ID for per-user OAuth token isolation (empty = use default from config)
//
// Returns:
//   - clients: Map of server name to client interface
//   - toolToServer: Map of model-facing tool name (<alias>__<tool>) to server name
//   - realToolNames: Map of model-facing tool name to the name the server registered
//   - serverAliases: Map of server name to the alias its host declared (MCPServerConfig.ToolAlias)
//   - tools: List of LLM tools
//   - servers: List of server names
//   - systemPrompt: Combined system prompt from servers
//   - error: Error if connection failed
func NewAgentConnectionWithSession(
	ctx context.Context,
	llm llmtypes.Model,
	serverName, configPath string,
	sessionID string,
	traceID string,
	tracers []observability.Tracer,
	logger loggerv2.Logger,
	disableCache bool,
	runtimeOverrides mcpclient.RuntimeOverrides,
	userID string,
) (map[string]mcpclient.ClientInterface, map[string]string, map[string]string, map[string]string, []llmtypes.Tool, []string, string, error) {

	config, err := mcpclient.LoadMergedConfig(configPath, logger)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, "", err
	}
	return newAgentConnectionWithConfig(ctx, llm, serverName, configPath, sessionID, traceID, tracers, logger, disableCache, runtimeOverrides, userID, config, nil)
}

func newAgentConnectionWithConfig(
	ctx context.Context,
	llm llmtypes.Model,
	serverName, configPath string,
	sessionID string,
	traceID string,
	tracers []observability.Tracer,
	logger loggerv2.Logger,
	disableCache bool,
	runtimeOverrides mcpclient.RuntimeOverrides,
	userID string,
	config *mcpclient.MCPConfig,
	retain func(string),
) (map[string]mcpclient.ClientInterface, map[string]string, map[string]string, map[string]string, []llmtypes.Tool, []string, string, error) {

	connectionStartTime := time.Now()

	logger.Info("NewAgentConnectionWithSession starting",
		loggerv2.String("session_id", sessionID),
		loggerv2.String("server_name", serverName),
		loggerv2.String("config_path", configPath))

	// Emit connection start event
	if len(tracers) > 0 {
		eventData := events.NewMCPServerConnectionEvent(serverName, "started", 0, 0, "")
		eventData.ConfigPath = configPath
		eventData.Operation = "connection_start_with_session"
		eventData.Timestamp = connectionStartTime

		event := events.NewAgentEvent(eventData)
		event.Type = events.MCPServerConnectionStart
		event.Timestamp = connectionStartTime
		event.TraceID = traceID
		event.CorrelationID = fmt.Sprintf("conn-session-%s-%s", sessionID, traceID)

		for _, tracer := range tracers {
			if err := tracer.EmitEvent(event); err != nil {
				logger.Warn("Failed to emit connection start event to tracer", loggerv2.Error(err))
			}
		}
	}

	// Determine which servers to connect to
	var servers []string
	if serverName == "all" || serverName == "" {
		servers = config.ListServers()
		logger.Info("Using all servers", loggerv2.Int("server_count", len(servers)))
	} else if serverName == mcpclient.NoServers {
		servers = []string{}
		logger.Info("NoServers specified, returning empty connections")
	} else {
		for _, s := range strings.Split(serverName, ",") {
			trimmed := strings.TrimSpace(s)
			if trimmed != "" {
				servers = append(servers, trimmed)
			}
		}
		servers = canonicalizeRequestedServers(config, servers)
		logger.Info("Using specific servers", loggerv2.Any("servers", servers))
	}

	// Handle special case: no servers requested
	if len(servers) == 0 {
		logger.Info("No servers requested, returning empty result")
		return make(map[string]mcpclient.ClientInterface), make(map[string]string), make(map[string]string), nil, nil, servers, "", nil
	}

	registry := mcpclient.GetSessionRegistry()

	// Connect to all servers in parallel — each goroutine handles connection + tool discovery
	results := make([]serverConnectionResult, len(servers))
	var wg sync.WaitGroup

	for i, srvName := range servers {
		wg.Add(1)
		go func(idx int, srvName string) {
			defer wg.Done()
			result := &results[idx]
			result.serverName = srvName

			serverConfig, extra := runtimeOverrides.ExtraServerConfig(srvName)
			if !extra {
				var err error
				serverConfig, err = config.GetServer(srvName)
				if err != nil {
					logger.Warn(fmt.Sprintf("Server %s not found in config, skipping", srvName),
						loggerv2.Error(err))
					result.err = err
					return
				}
			}
			result.toolAlias = serverConfig.ToolAlias
			// Apply runtime overrides if provided for this server
			if runtimeOverrides != nil && !extra {
				if override, hasOverride := runtimeOverrideForServer(runtimeOverrides, srvName); hasOverride {
					serverConfig = serverConfig.ApplyOverride(override)
					logger.Info("Applied runtime overrides to server config",
						loggerv2.String("server", srvName),
						loggerv2.Any("args_replace", override.ArgsReplace),
						loggerv2.Any("args_append", override.ArgsAppend),
						loggerv2.Any("env_override", override.EnvOverride))
				}
			}

			// Override OAuth token file path for per-user isolation
			if userID != "" && serverConfig.OAuth != nil {
				userTokenFile := userOAuthTokenFile(userID, srvName)
				oauth := *serverConfig.OAuth
				oauth.TokenFile = userTokenFile
				serverConfig.OAuth = &oauth
				logger.Info("Using per-user OAuth token path",
					loggerv2.String("server", srvName),
					loggerv2.String("user_id", userID),
					loggerv2.String("token_file", userTokenFile))
			}

			if retain != nil {
				retain(srvName)
			}

			// Lazy connection: if tool definitions are cached, defer subprocess spawn
			// until the first actual tool call. This avoids starting idle browser/server
			// processes when the LLM might not use them at all.
			if !disableCache {
				cacheManager := mcpcache.GetCacheManager(logger)
				cacheKey := mcpcache.GenerateUnifiedCacheKey(srvName, serverConfig)
				var cachedEntry *mcpcache.CacheEntry
				var exists bool
				cachedEntry, exists = cacheManager.Get(cacheKey)
				if exists && len(cachedEntry.Tools) > 0 {
					logger.Info(fmt.Sprintf("💤 [LAZY] Cache hit for %s — deferring connection until first tool call (%d tools)", srvName, len(cachedEntry.Tools)))
					for _, llmTool := range cachedEntry.Tools {
						if llmTool.Function == nil {
							continue
						}
						toolName := llmTool.Function.Name
						if cachedEntry.ToolOwnership != nil {
							if ownership, ok := cachedEntry.ToolOwnership[toolName]; ok && ownership == "duplicate" {
								continue
							}
						}
						result.tools = append(result.tools, llmTool)
						result.toolNames = append(result.toolNames, toolName)
					}
					result.isLazy = true
					// Store config so on-demand connect knows how to spawn the server
					registry.StoreServerConfig(sessionID, srvName, serverConfig)
					return
				}
			}

			// MCP subprocesses use the shared global connection pool.
			connSessionID := registry.ResolveConnectionSessionID(sessionID, srvName)

			// Get or create connection via registry
			client, wasCreated, err := registry.GetOrCreateConnection(ctx, connSessionID, srvName, serverConfig, logger)
			if err != nil {
				logger.Error(fmt.Sprintf("Failed to get/create connection for %s", srvName), err,
					loggerv2.String("session_id", sessionID))
				mcpclient.ReportConnectResult(srvName, err)
				result.err = err
				return
			}

			mcpclient.ReportConnectResult(srvName, nil)
			result.client = client
			result.wasCreated = wasCreated

			// Discover tools using ListTools (correct interface method)
			mcpTools, err := client.ListTools(ctx)
			if err != nil {
				logger.Warn(fmt.Sprintf("Failed to discover tools for %s: %v", srvName, err))

				// 🔧 FALLBACK: Try to use cached tools when ListTools fails (e.g., connection closed)
				if !wasCreated {
					// Only try cache fallback for reused connections (new connections shouldn't have cache yet)
					logger.Info(fmt.Sprintf("Attempting to use cached tools for %s as fallback", srvName))

					cacheManager := mcpcache.GetCacheManager(logger)
					cacheKey := mcpcache.GenerateUnifiedCacheKey(srvName, serverConfig)

					if cachedEntry, exists := cacheManager.Get(cacheKey); exists && len(cachedEntry.Tools) > 0 {
						logger.Info(fmt.Sprintf("✅ Using %d cached tools for %s (fallback from failed ListTools)",
							len(cachedEntry.Tools), srvName))

						// Use cached tools directly (they're already in llmtypes.Tool format)
						for _, llmTool := range cachedEntry.Tools {
							if llmTool.Function == nil {
								continue
							}
							toolName := llmTool.Function.Name

							// Check ToolOwnership to skip duplicates (if available)
							if cachedEntry.ToolOwnership != nil {
								if ownership, exists := cachedEntry.ToolOwnership[toolName]; exists && ownership == "duplicate" {
									logger.Debug("Skipping duplicate tool from cache",
										loggerv2.String("tool", toolName),
										loggerv2.String("server", srvName))
									continue
								}
							}

							result.tools = append(result.tools, llmTool)
							result.toolNames = append(result.toolNames, toolName)
						}

						mcpTools = []mcp.Tool{} // indicate cache was used
					} else {
						logger.Warn(fmt.Sprintf("No cached tools available for %s, continuing with empty tool list", srvName))
					}
				}
			} else {
				// Convert MCP tools to LLM tools using batch conversion
				llmTools, convErr := mcpclient.ToolsAsLLM(mcpTools)
				if convErr != nil {
					logger.Warn(fmt.Sprintf("Failed to convert tools for %s: %v", srvName, convErr))
				} else {
					for _, llmTool := range llmTools {
						if llmTool.Function == nil {
							continue
						}
						result.tools = append(result.tools, llmTool)
						result.toolNames = append(result.toolNames, llmTool.Function.Name)
					}
				}
			}
			result.mcpCount = len(mcpTools)

		}(i, srvName)
	}

	wg.Wait()

	// Merge results from all goroutines (serial — preserves server order).
	// Every MCP tool is offered as <alias>__<tool> (PLAT-519), so tools of
	// different servers never collide, even two connections of one server.
	clients := make(map[string]mcpclient.ClientInterface)
	toolToServer := make(map[string]string)
	realToolNames := make(map[string]string)
	var allTools []llmtypes.Tool
	var connectedServers []string
	seenTools := make(map[string]bool)
	aliasServers := make([]string, 0, len(results))
	declaredAliases := make(map[string]string)
	for _, result := range results {
		if result.err == nil && (result.client != nil || result.isLazy) {
			aliasServers = append(aliasServers, result.serverName)
			if result.toolAlias != "" {
				declaredAliases[result.serverName] = result.toolAlias
			}
		}
	}
	aliases := mcpToolAliases(aliasServers, declaredAliases)

	for _, result := range results {
		if result.err != nil {
			continue
		}
		// Lazy results have nil client but valid tools — include their tools but skip storing client.
		// Eagerly-connected results with nil client (unexpected) are skipped entirely.
		if result.client == nil && !result.isLazy {
			continue
		}

		if !result.isLazy {
			clients[result.serverName] = result.client
		}
		connectedServers = append(connectedServers, result.serverName)

		// Merge tools with deduplication
		visibleNames := mcpclient.ToolNames(aliases[result.serverName], result.toolNames, result.toolAlias != "")
		for i, llmTool := range result.tools {
			realName := result.toolNames[i]
			toolName := visibleNames[realName]
			if seenTools[toolName] {
				logger.Warn(fmt.Sprintf("Duplicate tool %s from server %s, skipping", realName, result.serverName))
				continue
			}
			seenTools[toolName] = true
			// Copy the definition: cached tool lists are shared between agents.
			function := *llmTool.Function
			function.Name = toolName
			llmTool.Function = &function
			allTools = append(allTools, llmTool)
			toolToServer[toolName] = result.serverName
			realToolNames[toolName] = realName
		}

		if result.isLazy {
			logger.Info(fmt.Sprintf("💤 Lazy server %s (session=%s): %d tools registered, connection deferred",
				result.serverName, sessionID, len(result.tools)))
		} else if result.wasCreated {
			logger.Info(fmt.Sprintf("New connection to %s (session=%s): %d tools discovered",
				result.serverName, sessionID, result.mcpCount))
		} else {
			logger.Info(fmt.Sprintf("Reused connection to %s (session=%s): %d tools available",
				result.serverName, sessionID, result.mcpCount))
		}
	}

	// System prompt is empty - it comes from agent configuration, not MCP servers
	systemPrompt := ""

	connectionDuration := time.Since(connectionStartTime)

	// Emit connection complete event
	if len(tracers) > 0 {
		eventData := events.NewMCPServerConnectionEvent(serverName, "completed_with_session", len(allTools), connectionDuration, "")
		eventData.ConfigPath = configPath
		eventData.Operation = "connection_complete_with_session"
		eventData.ServerInfo = map[string]interface{}{
			"session_id":    sessionID,
			"servers_count": len(clients),
			"tools_count":   len(allTools),
		}
		eventData.Timestamp = time.Now()

		event := events.NewAgentEvent(eventData)
		event.Type = events.MCPServerConnectionEnd
		event.Timestamp = eventData.Timestamp
		event.TraceID = traceID
		event.CorrelationID = fmt.Sprintf("conn-session-complete-%s-%s", sessionID, traceID)

		for _, tracer := range tracers {
			if err := tracer.EmitEvent(event); err != nil {
				logger.Warn("Failed to emit connection complete event to tracer", loggerv2.Error(err))
			}
		}
	}

	logger.Info("NewAgentConnectionWithSession completed",
		loggerv2.String("session_id", sessionID),
		loggerv2.Int("clients_count", len(clients)),
		loggerv2.Int("tools_count", len(allTools)),
		loggerv2.String("duration", connectionDuration.String()))

	return clients, toolToServer, realToolNames, declaredAliases, allTools, connectedServers, systemPrompt, nil
}

// canonicalizeRequestedServers resolves sanitized aliases to their configured
// server names and removes duplicates before any connection goroutines start.
// Unknown names are retained so the existing per-server lookup can report and
// skip them without changing connection error semantics.
func canonicalizeRequestedServers(config *mcpclient.MCPConfig, requested []string) []string {
	canonical := make([]string, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, requestedName := range requested {
		resolvedName := requestedName
		if name, _, err := config.ResolveServer(requestedName); err == nil {
			resolvedName = name
		}
		if _, exists := seen[resolvedName]; exists {
			continue
		}
		seen[resolvedName] = struct{}{}
		canonical = append(canonical, resolvedName)
	}
	return canonical
}

func runtimeOverrideForServer(overrides mcpclient.RuntimeOverrides, canonicalName string) (mcpclient.RuntimeConfigOverride, bool) {
	if override, exists := overrides[canonicalName]; exists {
		return override, true
	}
	if strings.Contains(canonicalName, "-") {
		override, exists := overrides[strings.ReplaceAll(canonicalName, "-", "_")]
		return override, exists
	}
	if strings.Contains(canonicalName, "_") {
		override, exists := overrides[strings.ReplaceAll(canonicalName, "_", "-")]
		return override, exists
	}
	return mcpclient.RuntimeConfigOverride{}, false
}

// resolveOnDemandMCPClient returns the MCP client for an on-demand server connection.
// It prefers the session registry (lazy-connect, reuses existing connections) over
// spawning a fresh process, preserving normal session-registry reuse.
func (a *Agent) resolveOnDemandMCPClient(ctx context.Context, serverName string, logger loggerv2.Logger) (mcpclient.ClientInterface, error) {
	if a.sessionID != "" {
		registry := mcpclient.GetSessionRegistry()
		if serverConfig, hasConfig := registry.GetServerConfig(a.sessionID, serverName); hasConfig {
			logger.Info(fmt.Sprintf("⚡ [ON-DEMAND] Using session registry lazy connect for server '%s' (session=%s)", serverName, a.sessionID))
			connSessionID := registry.ResolveConnectionSessionID(a.sessionID, serverName)
			client, _, err := registry.GetOrCreateConnection(ctx, connSessionID, serverName, serverConfig, logger)
			if client != nil || err != nil {
				return client, err
			}
			// client nil, err nil — fall through to fresh connection
		}
	}
	// A server outside the catalog connects from its own configuration; it is
	// never looked up (or created) from the catalog by name.
	if serverConfig, ok := a.runtimeOverrides.ExtraServerConfig(serverName); ok {
		registry := mcpclient.GetSessionRegistry()
		client, _, err := registry.GetOrCreateConnection(ctx, registry.ResolveConnectionSessionID(a.sessionID, serverName), serverName, serverConfig, logger)
		return client, err
	}
	return mcpcache.GetFreshConnection(ctx, serverName, a.configPath, logger)
}

// Match the host connector OAuth store, including headless services whose
// writable config directory is supplied through XDG_CONFIG_HOME.
func userOAuthTokenFile(userID, server string) string {
	root := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if root == "" {
		root = "~/.config"
	}
	return filepath.Join(root, "mcpagent", "tokens", userID, server+".json")
}
