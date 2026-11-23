package mcpcache

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/observability"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/sirupsen/logrus"
)

// DuplicateToolEntry represents a tool that exists on multiple MCP servers
type DuplicateToolEntry struct {
	Server string
	Tool   llmtypes.Tool
}

// CachedConnectionResult represents the result of a cached or fresh MCP connection
type CachedConnectionResult struct {
	// Original connection data
	Clients      map[string]mcpclient.ClientInterface
	ToolToServer map[string]string
	Tools        []llmtypes.Tool
	SystemPrompt string

	// DuplicateTools contains tools that were skipped during dedup because another
	// server already registered a tool with the same name. Maps tool name to a list
	// of (server, tool) pairs for all duplicate occurrences.
	DuplicateTools map[string][]DuplicateToolEntry

	// Cache metadata
	CacheUsed     bool
	CacheKey      string
	FreshFallback bool
	Error         error
}

// GenericCacheEvent represents a generic cache event to avoid circular imports
type GenericCacheEvent struct {
	Type           string        `json:"type"`
	ServerName     string        `json:"server_name,omitempty"`
	CacheKey       string        `json:"cache_key,omitempty"`
	ConfigPath     string        `json:"config_path,omitempty"`
	ToolsCount     int           `json:"tools_count,omitempty"`
	Age            time.Duration `json:"age,omitempty"`
	TTL            time.Duration `json:"ttl,omitempty"`
	DataSize       int64         `json:"data_size,omitempty"`
	Reason         string        `json:"reason,omitempty"`
	Operation      string        `json:"operation,omitempty"`
	Error          string        `json:"error,omitempty"`
	ErrorType      string        `json:"error_type,omitempty"`
	CleanupType    string        `json:"cleanup_type,omitempty"`
	EntriesRemoved int           `json:"entries_removed,omitempty"`
	EntriesTotal   int           `json:"entries_total,omitempty"`
	SpaceFreed     int64         `json:"space_freed,omitempty"`
	Timestamp      time.Time     `json:"timestamp"`
}

// GetType implements the observability.AgentEvent interface
func (e *GenericCacheEvent) GetType() string {
	return e.Type
}

// GetCorrelationID implements the observability.AgentEvent interface
func (e *GenericCacheEvent) GetCorrelationID() string {
	return ""
}

// GetTimestamp implements the observability.AgentEvent interface
func (e *GenericCacheEvent) GetTimestamp() time.Time {
	return e.Timestamp
}

// GetData implements the observability.AgentEvent interface
func (e *GenericCacheEvent) GetData() interface{} {
	return e
}

// GetTraceID implements the observability.AgentEvent interface
func (e *GenericCacheEvent) GetTraceID() string {
	return ""
}

// GetParentID implements the observability.AgentEvent interface
func (e *GenericCacheEvent) GetParentID() string {
	return ""
}

// Individual cache event types removed - only comprehensive cache events are used

// ComprehensiveCacheEvent represents a consolidated cache event with all details
type ComprehensiveCacheEvent struct {
	Type       string    `json:"type"`
	ServerName string    `json:"server_name"`
	ConfigPath string    `json:"config_path"`
	Timestamp  time.Time `json:"timestamp"`

	// Cache operation details
	Operation     string `json:"operation"`      // "start", "complete", "error"
	CacheUsed     bool   `json:"cache_used"`     // Whether cache was used
	FreshFallback bool   `json:"fresh_fallback"` // Whether fresh connections were used

	// Server details
	ServersCount int `json:"servers_count"`
	TotalTools   int `json:"total_tools"`

	// Individual server cache status
	ServerStatus map[string]ServerCacheStatus `json:"server_status"`

	// Cache statistics
	CacheHits   int `json:"cache_hits"`
	CacheMisses int `json:"cache_misses"`
	CacheWrites int `json:"cache_writes"`
	CacheErrors int `json:"cache_errors"`

	// Performance metrics
	ConnectionTime string `json:"connection_time"`
	CacheTime      string `json:"cache_time"`

	// Error information
	Errors []string `json:"errors,omitempty"`
}

// ServerCacheStatus represents the cache status for a specific server
type ServerCacheStatus struct {
	ServerName string `json:"server_name"`
	Status     string `json:"status"` // "hit", "miss", "write", "error"
	CacheKey   string `json:"cache_key,omitempty"`
	ToolsCount int    `json:"tools_count"`
	Age        string `json:"age,omitempty"`    // For cache hits
	Reason     string `json:"reason,omitempty"` // For cache misses
	Error      string `json:"error,omitempty"`  // For cache errors
}

// DuplicateToolFields represents typed fields for duplicate tool warning logs
type DuplicateToolFields struct {
	ToolName        string
	ExistingServer  string
	DuplicateServer string
}

// ToLogrusFields converts DuplicateToolFields to logrus.Fields for structured logging
func (f DuplicateToolFields) ToLogrusFields() logrus.Fields {
	return logrus.Fields{
		"tool_name":        f.ToolName,
		"existing_server":  f.ExistingServer,
		"duplicate_server": f.DuplicateServer,
	}
}

// Individual cache event interface implementations removed

// GetType implements the observability.AgentEvent interface
func (e *ComprehensiveCacheEvent) GetType() string {
	return e.Type
}

// GetCorrelationID implements the observability.AgentEvent interface
func (e *ComprehensiveCacheEvent) GetCorrelationID() string {
	return ""
}

// GetTimestamp implements the observability.AgentEvent interface
func (e *ComprehensiveCacheEvent) GetTimestamp() time.Time {
	return e.Timestamp
}

// GetData implements the observability.AgentEvent interface
func (e *ComprehensiveCacheEvent) GetData() interface{} {
	return e
}

// GetTraceID implements the observability.AgentEvent interface
func (e *ComprehensiveCacheEvent) GetTraceID() string {
	return ""
}

// GetParentID implements the observability.AgentEvent interface
func (e *ComprehensiveCacheEvent) GetParentID() string {
	return ""
}

// GetCachedOrFreshConnection attempts to get MCP connection data from cache first,
// falling back to fresh connection if cache is unavailable or expired
// If disableCache is true, skips cache lookup entirely and always performs fresh connections
// runtimeOverrides allows workflow-specific modifications to server configs (e.g., output directories)
func GetCachedOrFreshConnection(
	ctx context.Context,
	llm llmtypes.Model,
	serverName, configPath string,
	tracers []observability.Tracer,
	logger loggerv2.Logger,
	disableCache bool,
	runtimeOverrides mcpclient.RuntimeOverrides,
) (*CachedConnectionResult, error) {

	// Track cache operation start time
	cacheStartTime := time.Now()

	// Initialize server status tracking
	serverStatus := make(map[string]ServerCacheStatus)

	result := &CachedConnectionResult{
		Clients:      make(map[string]mcpclient.ClientInterface),
		ToolToServer: make(map[string]string),
	}

	// If cache is disabled, skip cache lookup and go directly to fresh connection
	if disableCache {
		logger.Info("Cache disabled", loggerv2.String("server_name", serverName))
		logger.Info("Cache disabled - performing fresh connection",
			loggerv2.String("server_name", serverName),
			loggerv2.String("disable_cache", "true"))

		// Load merged MCP configuration to get server details (base + user)
		logger.Info("Loading merged config")
		config, err := mcpclient.LoadMergedConfig(configPath, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to load merged MCP config: %w", err)
		}

		// Determine which servers to connect to
		var servers []string
		if serverName == "all" || serverName == "" {
			servers = config.ListServers()
			logger.Info("Using all servers", loggerv2.Int("server_count", len(servers)))
		} else if serverName == mcpclient.NoServers {
			servers = []string{}
			logger.Info("NoServers specified, servers list is empty")
		} else {
			requestedServers := strings.Split(serverName, ",")
			for _, reqServer := range requestedServers {
				reqServer = strings.TrimSpace(reqServer)
				for _, configServer := range config.ListServers() {
					if configServer == reqServer {
						servers = append(servers, reqServer)
						break
					}
				}
			}
		}

		// Handle special case: no servers requested (pure LLM reasoning)
		// Skip connection when NoServers is specified
		if len(servers) == 0 {
			logger.Info("Servers list is empty, returning early (NoServers mode)")
			logger.Info("No servers requested (NoServers) - skipping connection, returning empty result",
				loggerv2.String("server_name", serverName),
				loggerv2.Int("server_count", 0),
				loggerv2.Int("tools_count", 0))

			// Return empty result for pure LLM reasoning
			result.CacheUsed = false
			result.FreshFallback = true
			result.CacheKey = mcpclient.NoServers

			// Set empty collections
			result.Clients = make(map[string]mcpclient.ClientInterface)
			result.ToolToServer = make(map[string]string)
			result.Tools = []llmtypes.Tool{}
			result.SystemPrompt = ""

			// Emit comprehensive cache event
			cacheTime := time.Since(cacheStartTime)
			EmitComprehensiveCacheEvent(
				tracers,
				"complete",
				configPath,
				servers,
				result,
				serverStatus,
				time.Duration(0),
				cacheTime,
				nil,
			)

			return result, nil
		}

		// Perform fresh connection directly (only if servers list is not empty)
		logger.Info("🔍 [DEBUG] GetCachedOrFreshConnection: About to call performFreshConnection", loggerv2.Int("server_count", len(servers)), loggerv2.Any("servers", servers))
		freshStartTime := time.Now()
		logger.Info("Calling performFreshConnection", loggerv2.Int("server_count", len(servers)))
		freshResult, err := performFreshConnection(ctx, llm, serverName, configPath, logger, runtimeOverrides)
		freshDuration := time.Since(freshStartTime)
		if err != nil {
			logger.Error("❌ [DEBUG] GetCachedOrFreshConnection: performFreshConnection failed", err, loggerv2.String("duration", freshDuration.String()))
			return nil, err
		}
		logger.Info("✅ [DEBUG] GetCachedOrFreshConnection: performFreshConnection completed successfully", loggerv2.String("duration", freshDuration.String()), loggerv2.Int("clients_count", len(freshResult.Clients)))
		logger.Info("performFreshConnection completed")

		// Copy fresh result data
		result.Clients = freshResult.Clients
		result.ToolToServer = freshResult.ToolToServer
		result.Tools = freshResult.Tools
		result.SystemPrompt = freshResult.SystemPrompt
		result.CacheUsed = false
		result.FreshFallback = true

		// Emit comprehensive cache event (indicating cache was disabled)
		cacheTime := time.Since(cacheStartTime)
		EmitComprehensiveCacheEvent(
			tracers,
			"complete",
			configPath,
			servers,
			result,
			serverStatus,
			time.Duration(0),
			cacheTime,
			nil,
		)

		return result, nil
	}

	// Get cache manager (only if cache is enabled)
	cacheManager := GetCacheManager(logger)

	// Load merged MCP configuration to get server details (base + user)
	config, err := mcpclient.LoadMergedConfig(configPath, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to load merged MCP config: %w", err)
	}

	// Determine which servers to connect to
	var servers []string
	if serverName == "all" || serverName == "" {
		servers = config.ListServers()
		logger.Info("Using all servers", loggerv2.Int("server_count", len(servers)))
	} else if serverName == mcpclient.NoServers {
		// Special case: no servers should be connected
		servers = []string{}
		logger.Info("NoServers specified (cache enabled), servers list is empty")
		logger.Info("No servers requested - pure LLM reasoning mode",
			loggerv2.Int("server_count", 0),
			loggerv2.Any("servers", []string{}))
	} else {
		// Handle comma-separated server names
		requestedServers := strings.Split(serverName, ",")
		availableServers := config.ListServers()
		logger.Info("Validating requested servers against config",
			loggerv2.String("server_name", serverName),
			loggerv2.Any("requested_servers", requestedServers),
			loggerv2.Any("available_servers", availableServers))

		for _, reqServer := range requestedServers {
			reqServer = strings.TrimSpace(reqServer)
			found := false
			// Check if this server exists in config
			for _, configServer := range availableServers {
				if configServer == reqServer {
					servers = append(servers, reqServer)
					found = true
					break
				}
			}
			if !found {
				err := fmt.Errorf("requested MCP server '%s' not found in config. Available servers: %v. Please add '%s' to your MCP servers config file or remove it from the workflow/step configuration", reqServer, availableServers, reqServer)
				logger.Error("Requested server not found in config", err,
					loggerv2.String("requested_server", reqServer),
					loggerv2.Any("available_servers", availableServers))
				return result, err
			}
		}
	}

	logger.Info("Processing servers",
		loggerv2.Int("server_count", len(servers)),
		loggerv2.Any("servers", servers))

	// Handle special case: no servers requested (pure LLM reasoning)
	if len(servers) == 0 {
		logger.Info("Servers list is empty (cache enabled), returning early (NoServers mode)")
		logger.Info("No servers requested - returning empty connection result",
			loggerv2.Int("server_count", 0),
			loggerv2.Int("tools_count", 0),
			loggerv2.Any("cache_used", true))

		// Return empty result for pure LLM reasoning
		result.CacheUsed = true
		result.CacheKey = mcpclient.NoServers
		result.FreshFallback = false

		// Set empty collections (but preserve workspace tools)
		result.Clients = make(map[string]mcpclient.ClientInterface)
		result.ToolToServer = make(map[string]string)
		// Note: result.Tools is intentionally left empty - workspace tools are added separately
		// Note: result.SystemPrompt is intentionally left empty - agent will get proper system prompt from agent creation

		return result, nil
	}

	// Try to get data from cache for each server
	allFromCache := true
	var cachedData map[string]*CacheEntry
	var cachedServers []string
	var missedServers []string

	for _, srvName := range servers {
		_, exists := config.MCPServers[srvName]
		if !exists {
			return nil, fmt.Errorf("server %s not found in config", srvName)
		}

		// Get server configuration for cache key generation
		serverConfig, err := config.GetServer(srvName)
		if err != nil {
			logger.Warn("Failed to get server config", loggerv2.Error(err), loggerv2.String("server", srvName))
			continue
		}

		// Use configuration-aware cache key for consistency across all cache systems
		cacheKey := GenerateUnifiedCacheKey(srvName, serverConfig)

		// Try to get from cache using configuration-aware key
		if entry, found := cacheManager.Get(cacheKey); found {
			// Calculate cache age
			age := time.Since(entry.CreatedAt)

			logger.Info("Cache hit",
				loggerv2.String("server", srvName),
				loggerv2.String("cache_key", cacheKey))

			// Track cache hit status (no individual event emission)
			serverStatus[srvName] = ServerCacheStatus{
				ServerName: srvName,
				Status:     "hit",
				CacheKey:   cacheKey,
				ToolsCount: len(entry.Tools),
				Age:        age.String(),
			}

			// Store cached data for later processing
			if cachedData == nil {
				cachedData = make(map[string]*CacheEntry)
			}
			cachedData[srvName] = entry
			cachedServers = append(cachedServers, srvName)
			result.CacheKey = cacheKey
			result.CacheUsed = true
		} else {
			logger.Info("Cache miss",
				loggerv2.String("server", srvName),
				loggerv2.String("cache_key", cacheKey))

			// Try to reload cache from disk before giving up
			logger.Info("Attempting to reload cache from disk",
				loggerv2.String("server", srvName),
				loggerv2.String("cache_key", cacheKey))

			// Try to reload the cache entry from disk
			if reloadedEntry := cacheManager.ReloadFromDisk(cacheKey); reloadedEntry != nil {
				logger.Info("Cache reloaded from disk",
					loggerv2.String("server", srvName),
					loggerv2.String("cache_key", cacheKey),
					loggerv2.Int("tools", len(reloadedEntry.Tools)))

				// NOTE: Tools are already normalized before caching, no need to normalize again
				// This prevents race conditions from unlocked mutations

				// Use the reloaded entry
				age := time.Since(reloadedEntry.CreatedAt)
				serverStatus[srvName] = ServerCacheStatus{
					ServerName: srvName,
					Status:     "hit",
					CacheKey:   cacheKey,
					ToolsCount: len(reloadedEntry.Tools),
					Age:        age.String(),
				}

				// Store cached data for later processing
				if cachedData == nil {
					cachedData = make(map[string]*CacheEntry)
				}
				cachedData[srvName] = reloadedEntry
				cachedServers = append(cachedServers, srvName)
				result.CacheKey = cacheKey
				result.CacheUsed = true
				continue // Skip to next server
			} else {
				logger.Warn("Cache reload from disk failed",
					loggerv2.String("server", srvName),
					loggerv2.String("cache_key", cacheKey))
			}

			// Track cache miss status (no individual event emission)
			serverStatus[srvName] = ServerCacheStatus{
				ServerName: srvName,
				Status:     "miss",
				CacheKey:   cacheKey,
				ToolsCount: 0,
				Reason:     "not_found",
			}

			missedServers = append(missedServers, srvName)
			allFromCache = false
			// DON'T BREAK - continue checking all servers for hybrid approach
		}
	}

	// HYBRID APPROACH: Handle different cache scenarios
	if allFromCache && len(cachedData) > 0 {
		// SCENARIO 1: All servers cached - use cached data
		logger.Info("All servers cached - loading metadata and ensuring selected server connections",
			loggerv2.Int("cached_servers", len(cachedData)))

		// Emit comprehensive cache event for cached data usage
		cacheTime := time.Since(cacheStartTime)
		EmitComprehensiveCacheEvent(
			tracers,
			"complete",
			configPath,
			servers,
			result,
			serverStatus,
			time.Duration(0), // Connection time not available here
			cacheTime,
			nil, // No errors at this point
		)

		return processCachedData(ctx, llm, cachedData, config, servers, configPath, logger, runtimeOverrides)
	}

	// SCENARIO 2 & 3: Partial cache or all missed
	if len(cachedServers) > 0 && len(missedServers) > 0 {
		// HYBRID: Some cached, some missed - use cached data + connect only to missed servers
		logger.Info("Hybrid cache scenario - using cached + connecting to missed servers",
			loggerv2.Any("cached_servers", cachedServers),
			loggerv2.Any("missed_servers", missedServers),
			loggerv2.Int("cached_count", len(cachedServers)),
			loggerv2.Int("missed_count", len(missedServers)))

		// Start with cached data
		result.CacheUsed = true
		result.FreshFallback = true // Indicates hybrid mode

		// Process cached data first
		cachedResult, err := processCachedData(ctx, llm, cachedData, config, cachedServers, configPath, logger, runtimeOverrides)
		if err != nil {
			logger.Warn("Failed to process cached data in hybrid mode", loggerv2.Error(err))
			// Continue to fresh connection for missed servers anyway
		} else {
			// Copy cached result data
			result.Clients = cachedResult.Clients
			result.ToolToServer = cachedResult.ToolToServer
			result.Tools = cachedResult.Tools
			result.SystemPrompt = cachedResult.SystemPrompt
		}

		// Connect only to missed servers
		missedServersStr := strings.Join(missedServers, ",")
		freshResult, err := performFreshConnection(ctx, llm, missedServersStr, configPath, logger, runtimeOverrides)
		if err != nil {
			logger.Error("Failed to connect to missed servers", err)
			result.Error = err
			// Return partial result with cached data
			return result, fmt.Errorf("hybrid cache: cached %d servers, failed to connect to %d servers: %w", len(cachedServers), len(missedServers), err)
		}

		// Merge fresh results with cached results
		for serverName, client := range freshResult.Clients {
			result.Clients[serverName] = client
		}
		for toolName, serverName := range freshResult.ToolToServer {
			result.ToolToServer[toolName] = serverName
		}
		result.Tools = append(result.Tools, freshResult.Tools...)

		// Cache the fresh connection data for missed servers (synchronous to ensure all servers are cached)
		// Use a timeout context to prevent hanging
		cacheCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cacheFreshConnectionData(cacheCtx, cacheManager, config, configPath, missedServers, freshResult, logger, disableCache)

		logger.Info("Hybrid cache complete",
			loggerv2.Int("total_servers", len(servers)),
			loggerv2.Int("cached_servers", len(cachedServers)),
			loggerv2.Int("fresh_servers", len(missedServers)),
			loggerv2.Int("total_tools", len(result.Tools)))

		// Emit comprehensive cache event
		cacheTime := time.Since(cacheStartTime)
		EmitComprehensiveCacheEvent(
			tracers,
			"complete",
			configPath,
			servers,
			result,
			serverStatus,
			time.Duration(0),
			cacheTime,
			nil,
		)

		return result, nil
	}

	// SCENARIO 4: All servers missed cache - full fresh connection
	logger.Info("All servers missed cache - performing fresh connections",
		loggerv2.Any("missed_servers", missedServers),
		loggerv2.Int("missed_count", len(missedServers)))

	result.CacheUsed = false
	result.FreshFallback = true

	// Perform fresh connection (existing logic)
	freshResult, err := performFreshConnection(ctx, llm, serverName, configPath, logger, runtimeOverrides)
	if err != nil {
		result.Error = err
		return result, err
	}

	// Copy fresh result data
	result.Clients = freshResult.Clients
	result.ToolToServer = freshResult.ToolToServer
	result.Tools = freshResult.Tools
	result.SystemPrompt = freshResult.SystemPrompt

	// Cache the fresh connection data (synchronous to ensure all servers are cached)
	// Use a timeout context to prevent hanging
	cacheCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cacheFreshConnectionData(cacheCtx, cacheManager, config, configPath, servers, freshResult, logger, disableCache)

	// Emit comprehensive cache event with all details
	cacheTime := time.Since(cacheStartTime)
	EmitComprehensiveCacheEvent(
		tracers,
		"complete",
		configPath,
		servers,
		result,
		serverStatus,
		time.Duration(0), // Connection time not available here
		cacheTime,
		nil, // No errors at this point
	)

	return result, nil
}

// processCachedData processes cached entries and creates connections to servers
// Even when using cached tool definitions, we still connect to servers for execution
func processCachedData(
	ctx context.Context,
	llm llmtypes.Model,
	cachedData map[string]*CacheEntry,
	config *mcpclient.MCPConfig,
	servers []string,
	configPath string,
	logger loggerv2.Logger,
	runtimeOverrides mcpclient.RuntimeOverrides,
) (*CachedConnectionResult, error) {

	result := &CachedConnectionResult{
		Clients:      make(map[string]mcpclient.ClientInterface), // Will be populated with actual connections
		ToolToServer: make(map[string]string),
		CacheUsed:    true,
	}

	// Track seen tools to prevent duplicates (Gemini/Vertex rejects duplicate function declarations)
	seenTools := make(map[string]bool)

	// Aggregate data from all cached entries WITHOUT creating connections
	for _, srvName := range servers {
		entry, exists := cachedData[srvName]
		if !exists {
			continue
		}

		logger.Info("Using cached data without connection",
			loggerv2.String("server", srvName),
			loggerv2.Int("tools_count", len(entry.Tools)),
			loggerv2.String("protocol", entry.Protocol))

		// NOTE: Tools are already normalized before caching (see cacheFreshConnectionData)
		// No need to normalize again, which prevents race conditions from unlocked mutations
		logger.Debug("Using pre-normalized tools for server",
			loggerv2.String("server", srvName),
			loggerv2.Int("tools", len(entry.Tools)))

		// Deduplicate tools using ToolOwnership metadata
		// Only add tools marked as "primary" for this server
		for _, t := range entry.Tools {
			if t.Function == nil {
				continue
			}

			toolName := t.Function.Name

			// Check ToolOwnership field to determine if this server should expose this tool
			if entry.ToolOwnership != nil {
				ownership, exists := entry.ToolOwnership[toolName]
				if exists && ownership == "duplicate" {
					// This tool is a duplicate - track it in DuplicateTools for tool search disambiguation
					logger.Debug("Tracking duplicate tool for disambiguation",
						loggerv2.String("tool", toolName),
						loggerv2.String("server", srvName))
					if result.DuplicateTools == nil {
						result.DuplicateTools = make(map[string][]DuplicateToolEntry)
					}
					result.DuplicateTools[toolName] = append(result.DuplicateTools[toolName], DuplicateToolEntry{
						Server: srvName,
						Tool:   t,
					})
					continue
				}
				// If ownership is "primary" or not set, include the tool
			}

			// Runtime duplicate check (defensive - should not happen with ToolOwnership)
			if seenTools[toolName] {
				// Duplicate tool found despite ToolOwnership metadata - track for disambiguation
				logger.Debug("Tracking unexpected duplicate tool for disambiguation",
					loggerv2.String("tool", toolName),
					loggerv2.String("existing_server", result.ToolToServer[toolName]),
					loggerv2.String("duplicate_server", srvName))
				if result.DuplicateTools == nil {
					result.DuplicateTools = make(map[string][]DuplicateToolEntry)
				}
				result.DuplicateTools[toolName] = append(result.DuplicateTools[toolName], DuplicateToolEntry{
					Server: srvName,
					Tool:   t,
				})
				continue
			}

			// This server owns this tool - add it
			seenTools[toolName] = true
			result.ToolToServer[toolName] = srvName
			result.Tools = append(result.Tools, t)
		}

		logger.Info("Cached data loaded",
			loggerv2.String("server", srvName),
			loggerv2.Int("tools_count", len(entry.Tools)))
	}

	// Use cached system prompt if available
	if len(cachedData) > 0 {
		for _, entry := range cachedData {
			if entry.SystemPrompt != "" {
				result.SystemPrompt = entry.SystemPrompt
				break
			}
		}
	}

	// Now create actual connections to servers even though we're using cached tool definitions
	logger.Info("Creating actual connections to servers (using cached tool definitions)",
		loggerv2.Any("servers", servers))

	// Create live connections while retaining the cached tool definitions.
	clients, _, _, _, _, err := connectMCPServersFresh(ctx, llm, strings.Join(servers, ","), configPath, logger, runtimeOverrides)
	if err != nil {
		logger.Warn("Failed to create connections, but continuing with cached data", loggerv2.Error(err))
		// Continue with cached data even if connections fail
	} else {
		// Use the actual connections
		result.Clients = clients

	}

	logger.Info("Cached data processing complete with connections",
		loggerv2.Int("cached_servers", len(cachedData)),
		loggerv2.Int("total_tools", len(result.Tools)),
		loggerv2.Int("connections", len(result.Clients)))

	return result, nil
}

// performFreshConnection establishes live connections and packages their discovered capabilities.
func performFreshConnection(
	ctx context.Context,
	llm llmtypes.Model,
	serverName, configPath string,
	logger loggerv2.Logger,
	runtimeOverrides mcpclient.RuntimeOverrides,
) (*CachedConnectionResult, error) {
	logger.Info("🔍 [DEBUG] performFreshConnection: Starting", loggerv2.String("server_name", serverName), loggerv2.String("config_path", configPath))
	performStartTime := time.Now()

	logger.Info("🔍 [DEBUG] performFreshConnection: About to call connectMCPServersFresh")
	clients, toolToServer, tools, _, systemPrompt, err := connectMCPServersFresh(ctx, llm, serverName, configPath, logger, runtimeOverrides)
	performDuration := time.Since(performStartTime)
	if err != nil {
		logger.Error("❌ [DEBUG] performFreshConnection: connectMCPServersFresh failed", err, loggerv2.String("duration", performDuration.String()))
		return nil, err
	}
	logger.Info("✅ [DEBUG] performFreshConnection: connectMCPServersFresh completed successfully", loggerv2.String("duration", performDuration.String()), loggerv2.Int("clients_count", len(clients)))

	result := &CachedConnectionResult{
		Clients:      clients,
		ToolToServer: toolToServer,
		Tools:        tools,
		SystemPrompt: systemPrompt,
	}

	return result, nil
}

// connectMCPServersFresh establishes live connections and discovers their MCP capabilities.
func connectMCPServersFresh(
	ctx context.Context,
	llm llmtypes.Model,
	serverName, configPath string,
	logger loggerv2.Logger,
	runtimeOverrides mcpclient.RuntimeOverrides,
) (map[string]mcpclient.ClientInterface, map[string]string, []llmtypes.Tool, []string, string, error) {

	// Load merged MCP server configuration (base + user)
	logger.Info("Loading merged MCP config", loggerv2.String("config_path", configPath))
	cfg, err := mcpclient.LoadMergedConfig(configPath, logger)
	if err != nil {
		logger.Error("Failed to load merged MCP config", err)
		return nil, nil, nil, nil, "", fmt.Errorf("load merged config: %w", err)
	}
	logger.Info("Merged MCP config loaded", loggerv2.Int("server_count", len(cfg.MCPServers)))

	// Determine which servers to connect to
	var servers []string
	if serverName == "all" || serverName == "" {
		servers = cfg.ListServers()
		logger.Info("Using all servers", loggerv2.Int("server_count", len(servers)))
	} else {
		for _, s := range strings.Split(serverName, ",") {
			servers = append(servers, strings.TrimSpace(s))
		}
		logger.Info("Using specific servers", loggerv2.Any("servers", servers))
	}

	clients := make(map[string]mcpclient.ClientInterface)
	toolToServer := make(map[string]string)
	var allLLMTools []llmtypes.Tool

	// Create a filtered config that only contains the specified servers
	filteredConfig := &mcpclient.MCPConfig{
		MCPServers: make(map[string]mcpclient.MCPServerConfig),
	}
	for _, serverName := range servers {
		if serverConfig, exists := cfg.MCPServers[serverName]; exists {
			// Apply runtime overrides if provided for this server
			if runtimeOverrides != nil {
				if override, hasOverride := runtimeOverrides[serverName]; hasOverride {
					serverConfig = serverConfig.ApplyOverride(override)
					logger.Info("Applied runtime overrides to server config",
						loggerv2.String("server", serverName),
						loggerv2.Any("args_replace", override.ArgsReplace),
						loggerv2.Any("args_append", override.ArgsAppend),
						loggerv2.Any("env_override", override.EnvOverride))
				}
			}
			filteredConfig.MCPServers[serverName] = serverConfig
		}
	}
	logger.Info("Filtered config created", loggerv2.Int("filtered_server_count", len(filteredConfig.MCPServers)))

	// Use new parallel tool discovery for only the specified servers
	discoveryStartTime := time.Now()
	logger.Info("Starting parallel tool discovery",
		loggerv2.Int("server_count", len(filteredConfig.MCPServers)),
		loggerv2.Any("servers", servers),
		loggerv2.String("start_time", discoveryStartTime.Format(time.RFC3339)))

	logger.Info("🔍 [DEBUG] connectMCPServersFresh: About to call DiscoverAllToolsParallel",
		loggerv2.Int("server_count", len(servers)),
		loggerv2.Any("servers", servers))

	parallelResults := mcpclient.DiscoverAllToolsParallel(ctx, filteredConfig, logger)

	discoveryDuration := time.Since(discoveryStartTime)
	logger.Info("✅ [DEBUG] connectMCPServersFresh: DiscoverAllToolsParallel completed",
		loggerv2.String("duration", discoveryDuration.String()),
		loggerv2.Int("result_count", len(parallelResults)))
	logger.Info("Parallel tool discovery completed",
		loggerv2.Int("result_count", len(parallelResults)),
		loggerv2.Any("discovery_time", discoveryDuration.String()),
		loggerv2.Any("discovery_ms", discoveryDuration.Milliseconds()))

	// Track seen tools to prevent duplicates (Gemini/Vertex rejects duplicate function declarations)
	seenTools := make(map[string]bool)

	// Collect all server errors for detailed error reporting
	var serverErrors []string

	for _, r := range parallelResults {
		srvName := r.ServerName

		_, err := cfg.GetServer(srvName)
		if err != nil {
			logger.Warn("Server not found in config, skipping",
				loggerv2.String("server", srvName),
				loggerv2.Error(err))
			serverErrors = append(serverErrors, fmt.Sprintf("%s: server not found in config: %v", srvName, err))
			if r.Client != nil {
				_ = r.Client.Close()
			}
			continue // Skip this server instead of failing everything
		}

		if r.Error != nil {
			logger.Warn("Failed to connect to server, skipping",
				loggerv2.String("server", srvName),
				loggerv2.Error(r.Error))
			// Collect the raw error for this server
			serverErrors = append(serverErrors, fmt.Sprintf("%s: %v", srvName, r.Error))
			if r.Client != nil {
				_ = r.Client.Close()
			}
			continue // Skip this server instead of failing everything
		}

		// Use the client from parallel tool discovery instead of creating a new one
		// This ensures we reuse the working connection (already connected and listed tools successfully)
		c := r.Client

		srvTools := r.Tools
		llmTools, err := mcpclient.ToolsAsLLM(srvTools)
		if err != nil {
			logger.Warn("Failed to convert tools for server, skipping",
				loggerv2.String("server", srvName),
				loggerv2.Error(err))
			serverErrors = append(serverErrors, fmt.Sprintf("%s: failed to convert tools: %v", srvName, err))
			if r.Client != nil {
				_ = r.Client.Close()
			}
			continue // Skip this server instead of failing everything
		}

		// Tools are already normalized by ToolsAsLLM() during conversion
		// No extra normalization needed since langchaingo bug is fixed

		// Deduplicate tools: only add tools we haven't seen before
		for _, t := range llmTools {
			if t.Function == nil {
				continue
			}

			toolName := t.Function.Name
			if seenTools[toolName] {
				// Duplicate tool found - log warning and skip
				existingServer := toolToServer[toolName]
				logger.Warn("Duplicate tool detected, skipping",
					loggerv2.String("tool", toolName),
					loggerv2.String("existing_server", existingServer),
					loggerv2.String("duplicate_server", srvName))
				continue
			}

			// First occurrence of this tool - add it
			seenTools[toolName] = true
			toolToServer[toolName] = srvName
			allLLMTools = append(allLLMTools, t)
		}

		clients[srvName] = c
	}

	// Check if we have at least one successful connection
	if len(clients) == 0 {
		// Build detailed error message with all individual server errors
		errorMsg := "no servers could be connected - all servers failed or were skipped"
		if len(serverErrors) > 0 {
			errorMsg += "; individual server errors: " + strings.Join(serverErrors, "; ")
		}
		return nil, nil, nil, nil, "", errors.New(errorMsg)
	}

	logger.Info("Aggregated tools",
		loggerv2.Int("total_tools", len(allLLMTools)),
		loggerv2.Int("server_count", len(clients)),
		loggerv2.Int("total_servers_attempted", len(parallelResults)),
		loggerv2.String("connection_type", "direct"))

	// Log detailed discovery completion (events handled by connection.go)

	// Build minimal system prompt (will be enhanced in agent creation)
	systemPrompt := fmt.Sprintf("Connected to %d MCP servers with %d tools available.",
		len(clients), len(allLLMTools))
	return clients, toolToServer, allLLMTools, servers, systemPrompt, nil
}

// cacheFreshConnectionData caches the results of a fresh connection
// Processes all servers in parallel for better performance
// If disableCache is true, skips caching entirely
func cacheFreshConnectionData(
	ctx context.Context,
	cacheManager *CacheManager,
	config *mcpclient.MCPConfig,
	configPath string,
	servers []string,
	result *CachedConnectionResult,
	logger loggerv2.Logger,
	disableCache bool,
) {
	// If cache is disabled, skip caching entirely
	if disableCache {
		logger.Info("Cache disabled - skipping cache save", loggerv2.Any("servers", servers))
		return
	}
	logger.Info("Starting parallel cache save for servers", loggerv2.Any("servers", servers), loggerv2.Int("count", len(servers)))

	var wg sync.WaitGroup
	var mu sync.Mutex
	var errors []string

	for _, srvName := range servers {
		// Check if context is cancelled before starting new goroutine
		select {
		case <-ctx.Done():
			logger.Warn("Cache save cancelled by context", loggerv2.Error(ctx.Err()), loggerv2.String("last_server", srvName))
			return
		default:
		}

		wg.Add(1)
		go func(srvName string) {
			defer wg.Done()

			// Check if context is cancelled
			select {
			case <-ctx.Done():
				logger.Warn("Cache save cancelled for server", loggerv2.Error(ctx.Err()), loggerv2.String("server", srvName))
				mu.Lock()
				errors = append(errors, fmt.Sprintf("%s: cancelled", srvName))
				mu.Unlock()
				return
			default:
			}

			serverConfig, exists := config.MCPServers[srvName]
			if !exists {
				logger.Warn("Server config not found, skipping cache", loggerv2.String("server", srvName))
				return
			}

			logger.Info("Processing cache save for server", loggerv2.String("server", srvName))

			// Extract server-specific tools
			serverTools := extractServerTools(result.Tools, result.ToolToServer, srvName)

			// IMPORTANT: Normalize tools BEFORE caching to ensure all array parameters have 'items' field
			// This prevents race conditions from normalizing after cache retrieval
			// Normalization is required for LLM providers (especially Gemini/Vertex)
			mcpclient.NormalizeLLMTools(serverTools, logger)

			// Build ToolOwnership map to mark which tools this server owns
			// This prevents duplicate tool detection issues when loading from cache
			toolOwnership := make(map[string]string)
			for _, tool := range serverTools {
				toolName := tool.Function.Name
				owningServer, exists := result.ToolToServer[toolName]
				if !exists {
					// Tool not in ToolToServer map (shouldn't happen, but be defensive)
					logger.Warn("Tool not found in ToolToServer map",
						loggerv2.String("tool", toolName),
						loggerv2.String("server", srvName))
					toolOwnership[toolName] = "primary" // Assume primary by default
					continue
				}

				if owningServer == srvName {
					// This server is the primary owner of this tool
					toolOwnership[toolName] = "primary"
				} else {
					// Another server owns this tool (this server has a duplicate)
					toolOwnership[toolName] = "duplicate"
					logger.Debug("Tool marked as duplicate",
						loggerv2.String("tool", toolName),
						loggerv2.String("server", srvName),
						loggerv2.String("primary", owningServer))
				}
			}

			// Create cache entry with pre-normalized tools and ownership info
			entry := &CacheEntry{
				ServerName:    srvName,
				Tools:         serverTools, // Already normalized
				SystemPrompt:  result.SystemPrompt,
				CreatedAt:     time.Now(),
				TTLMinutes:    cacheManager.GetTTL(), // Use configured TTL instead of hardcoded 30 minutes
				Protocol:      string(serverConfig.Protocol),
				IsValid:       true,
				ToolOwnership: toolOwnership, // Add ownership tracking
			}

			// Store in cache using configuration-aware cache key
			logger.Debug("Calling cacheManager.Put", loggerv2.String("server", srvName), loggerv2.Int("tools_count", len(serverTools)))

			// Call Put directly (no anonymous function wrapper that might cause issues)
			if err := cacheManager.Put(entry, serverConfig); err != nil {
				logger.Warn("Failed to cache connection data", loggerv2.Error(err), loggerv2.String("server", srvName))
				mu.Lock()
				errors = append(errors, fmt.Sprintf("%s: %v", srvName, err))
				mu.Unlock()
			} else {
				logger.Info("Successfully cached connection data",
					loggerv2.String("server", srvName),
					loggerv2.Int("tools_count", len(serverTools)))
			}

			logger.Debug("Completed cache save for server", loggerv2.String("server", srvName))
		}(srvName)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	if len(errors) > 0 {
		logger.Warn("Some cache saves failed", loggerv2.Int("error_count", len(errors)), loggerv2.Any("errors", errors))
	}

	logger.Info("Completed parallel cache save for all servers", loggerv2.Int("servers_processed", len(servers)), loggerv2.Int("servers_requested", len(servers)), loggerv2.Int("errors", len(errors)))
}

// extractServerTools extracts tools specific to a server from the aggregated tool list
func extractServerTools(allTools []llmtypes.Tool, toolToServer map[string]string, serverName string) []llmtypes.Tool {
	var serverTools []llmtypes.Tool
	for _, tool := range allTools {
		if srv, exists := toolToServer[tool.Function.Name]; exists && srv == serverName {
			serverTools = append(serverTools, tool)
		}
	}
	return serverTools
}

// InvalidateServerCache invalidates cache entries for a specific server
// This is a backward-compatible wrapper that uses context.Background()
func InvalidateServerCache(configPath, serverName string, logger loggerv2.Logger) error {
	return InvalidateServerCacheWithContext(context.Background(), configPath, serverName, logger)
}

// InvalidateServerCacheWithContext invalidates cache entries for a specific server with context support
func InvalidateServerCacheWithContext(ctx context.Context, configPath, serverName string, logger loggerv2.Logger) error {
	cacheManager := GetCacheManager(logger)
	return cacheManager.InvalidateByServerWithContext(ctx, configPath, serverName)
}

// GetFreshConnection creates a fresh MCP connection for a server, bypassing any cache
// This is used for broken pipe recovery when existing connections are dead
// It invalidates the cache first, then creates a new connection
// Uses a timeout wrapper to prevent invalidation from blocking connection recovery
func GetFreshConnection(ctx context.Context, serverName, configPath string, logger loggerv2.Logger) (mcpclient.ClientInterface, error) {
	logger.Info("🔧 [FRESH CONNECTION] Creating fresh MCP client for server", loggerv2.String("server", serverName))

	// Invalidate cache first to force fresh connection
	// Use a timeout to prevent invalidation from hanging and blocking connection recovery
	// 30 seconds should be enough for file I/O operations, but won't block indefinitely
	invalidateCtx, invalidateCancel := context.WithTimeout(ctx, 30*time.Second)
	defer invalidateCancel()

	invalidateDone := make(chan error, 1)
	go func() {
		invalidateDone <- InvalidateServerCacheWithContext(invalidateCtx, configPath, serverName, logger)
	}()

	invalidationTimedOut := false
	select {
	case <-invalidateCtx.Done():
		// Timeout or cancellation - log warning but continue with connection creation
		logger.Warn("🔧 [FRESH CONNECTION] Invalidation timed out or was cancelled (continuing with connection creation)",
			loggerv2.String("server", serverName),
			loggerv2.Error(invalidateCtx.Err()))
		invalidationTimedOut = true
		// Drain the channel in a non-blocking way to avoid goroutine leak
		select {
		case <-invalidateDone:
			logger.Debug("🔧 [FRESH CONNECTION] Drained invalidation result after timeout")
		default:
			// Channel not ready yet, goroutine will complete in background
		}
	case invalidateErr := <-invalidateDone:
		if invalidateErr != nil {
			logger.Warn("🔧 [FRESH CONNECTION] Failed to invalidate cache for server (continuing anyway)",
				loggerv2.String("server", serverName),
				loggerv2.Error(invalidateErr))
		} else {
			logger.Info("🔧 [FRESH CONNECTION] Invalidated cache for server", loggerv2.String("server", serverName))
		}
	}

	// Determine if we should disable cache
	// If invalidation timed out, cache state is uncertain, so bypass it entirely
	disableCache := invalidationTimedOut

	// Add timeout wrapper around GetCachedOrFreshConnection to prevent indefinite blocking
	// Use a reasonable timeout (5 minutes) for connection creation
	connectionCtx, connectionCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer connectionCancel()

	// Get fresh connection using existing infrastructure
	// If invalidation timed out, disable cache to avoid inconsistent state
	result, err := GetCachedOrFreshConnection(
		connectionCtx,
		nil, // No LLM needed for tool execution
		serverName,
		configPath,
		nil, // No tracers needed
		logger,
		disableCache, // disableCache=true if invalidation timed out, false otherwise
		nil,          // No runtime overrides needed for tool execution
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get fresh connection for server %s: %w", serverName, err)
	}

	// Get the client from the Clients map
	client, exists := result.Clients[serverName]
	if !exists {
		return nil, fmt.Errorf("server %s not found in fresh connection result", serverName)
	}

	logger.Info("✅ [FRESH CONNECTION] Successfully created fresh MCP client for server", loggerv2.String("server", serverName))
	return client, nil
}

// ClearAllCache clears all cache entries
func ClearAllCache(logger loggerv2.Logger) error {
	cacheManager := GetCacheManager(logger)
	return cacheManager.Clear()
}

// GetCacheStats returns cache statistics
func GetCacheStats(logger loggerv2.Logger) map[string]interface{} {
	cacheManager := GetCacheManager(logger)
	return cacheManager.GetStats()
}

// CleanupExpiredEntries removes expired cache entries
func CleanupExpiredEntries(logger loggerv2.Logger) error {
	cacheManager := GetCacheManager(logger)
	return cacheManager.Cleanup()
}

// ValidateCacheHealth validates the health of cached connections and emits events
func ValidateCacheHealth(tracers []observability.Tracer, logger loggerv2.Logger) {
	cacheManager := GetCacheManager(logger)
	stats := cacheManager.GetStats()

	logger.Info("Cache health check started",
		loggerv2.Any("total_entries", stats["total_entries"]),
		loggerv2.Any("valid_entries", stats["valid_entries"]),
		loggerv2.Any("expired_entries", stats["expired_entries"]))

	// Cleanup expired entries
	if err := cacheManager.Cleanup(); err != nil {
		logger.Warn("Cache cleanup failed", loggerv2.Error(err))
	} else {
		cleanupStats := cacheManager.GetStats()
		logger.Info("Cache cleanup completed",
			loggerv2.Any("expired_entries_removed", cleanupStats["expired_entries"]))
	}
}

// ValidateServerCache validates cache for a specific server and emits events
func ValidateServerCache(serverName, configPath string, tracers []observability.Tracer, logger loggerv2.Logger) bool {
	cacheManager := GetCacheManager(logger)

	// Get merged server config to generate cache key
	config, err := mcpclient.LoadMergedConfig(configPath, logger)
	if err != nil {
		logger.Warn("Failed to load merged config for cache validation", loggerv2.Error(err))
		return false
	}

	serverConfig, exists := config.MCPServers[serverName]
	if !exists {
		logger.Warn("Server not found in config for cache validation", loggerv2.String("server", serverName))
		return false
	}

	cacheKey := GenerateUnifiedCacheKey(serverName, serverConfig)

	// Check if entry exists and is valid
	if entry, found := cacheManager.Get(cacheKey); found {
		age := time.Since(entry.CreatedAt)
		ttl := time.Duration(entry.TTLMinutes) * time.Minute

		if age < ttl {
			// Cache is valid
			logger.Debug("Cache validation: server is valid",
				loggerv2.String("server", serverName),
				loggerv2.Any("age", age.String()),
				loggerv2.Any("ttl", ttl.String()))
			return true
		} else {
			// Cache expired - invalidate
			if err := cacheManager.InvalidateByServer(configPath, serverName); err != nil {
				logger.Warn("Failed to invalidate cache for server", loggerv2.String("server", serverName), loggerv2.Error(err))
			}
			logger.Debug("Cache validation: server expired and invalidated", loggerv2.String("server", serverName))
			return false
		}
	} else {
		// Cache miss
		logger.Debug("Cache validation: server not found in cache", loggerv2.String("server", serverName))
		return false
	}
}

// GetCacheStatus returns detailed cache status for monitoring
func GetCacheStatus(configPath string, tracers []observability.Tracer, logger loggerv2.Logger) map[string]interface{} {
	cacheManager := GetCacheManager(logger)
	stats := cacheManager.GetStats()

	// Load merged config to get server list
	config, err := mcpclient.LoadMergedConfig(configPath, logger)
	if err != nil {
		logger.Warn("Failed to load merged config for cache status", loggerv2.Error(err))
		return stats
	}

	// Add server-specific cache status
	serverStatus := make(map[string]interface{})
	for serverName := range config.MCPServers {
		serverConfig, exists := config.MCPServers[serverName]
		if !exists {
			continue
		}
		cacheKey := GenerateUnifiedCacheKey(serverName, serverConfig)

		if entry, found := cacheManager.Get(cacheKey); found {
			age := time.Since(entry.CreatedAt)
			ttl := time.Duration(entry.TTLMinutes) * time.Minute
			isValid := age < ttl

			serverStatus[serverName] = map[string]interface{}{
				"cached":      true,
				"cache_key":   cacheKey,
				"age":         age.String(),
				"ttl":         ttl.String(),
				"is_valid":    isValid,
				"tools_count": len(entry.Tools),
			}
		} else {
			serverStatus[serverName] = map[string]interface{}{
				"cached": false,
			}
		}
	}

	result := map[string]interface{}{
		"cache_stats":   stats,
		"server_status": serverStatus,
		"config_path":   configPath,
		"timestamp":     time.Now(),
	}

	return result
}

// EmitComprehensiveCacheEvent emits a single comprehensive cache event with all details
func EmitComprehensiveCacheEvent(
	tracers []observability.Tracer,
	operation string,
	configPath string,
	servers []string,
	result *CachedConnectionResult,
	serverStatus map[string]ServerCacheStatus,
	connectionTime time.Duration,
	cacheTime time.Duration,
	errors []string,
) {
	// Count cache statistics
	cacheHits := 0
	cacheMisses := 0
	cacheWrites := 0
	cacheErrors := 0

	for _, status := range serverStatus {
		switch status.Status {
		case "hit":
			cacheHits++
		case "miss":
			cacheMisses++
		case "write":
			cacheWrites++
		case "error":
			cacheErrors++
		}
	}

	// Calculate totals
	totalTools := 0

	if result != nil {
		totalTools = len(result.Tools)

	}

	event := &ComprehensiveCacheEvent{
		Type:           "comprehensive_cache_event",
		ServerName:     "all-servers",
		ConfigPath:     configPath,
		Timestamp:      time.Now(),
		Operation:      operation,
		CacheUsed:      result != nil && result.CacheUsed,
		FreshFallback:  result != nil && result.FreshFallback,
		ServersCount:   len(servers),
		TotalTools:     totalTools,
		ServerStatus:   serverStatus,
		CacheHits:      cacheHits,
		CacheMisses:    cacheMisses,
		CacheWrites:    cacheWrites,
		CacheErrors:    cacheErrors,
		ConnectionTime: connectionTime.String(),
		CacheTime:      cacheTime.String(),
		Errors:         errors,
	}

	for _, tracer := range tracers {
		_ = tracer.EmitEvent(event) // Silently ignore emission errors to avoid disrupting cache operations
	}
}

// Individual cache event functions removed - only comprehensive cache events are used
