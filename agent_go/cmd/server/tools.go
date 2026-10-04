package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/mcpcache"
	"github.com/manishiitg/mcpagent/mcpclient"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// --- SERVER LOG TYPES ---

// ServerLogEntry represents a single log entry for an MCP server
type ServerLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"` // "info", "error", "warn", "debug"
	Message   string    `json:"message"`
}

// maxServerLogEntries is the maximum number of log entries kept per server
const maxServerLogEntries = 100

// serverLogs stores per-server log entries (managed on StreamingAPI)
// serverLogsMux protects concurrent access to serverLogs

// appendServerLog adds a log entry for a server, capping at maxServerLogEntries
func (api *StreamingAPI) appendServerLog(serverName, level, message string) {
	api.serverLogsMux.Lock()
	defer api.serverLogsMux.Unlock()

	entry := ServerLogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   message,
	}

	logs := api.serverLogs[serverName]
	logs = append(logs, entry)
	if len(logs) > maxServerLogEntries {
		logs = logs[len(logs)-maxServerLogEntries:]
	}
	api.serverLogs[serverName] = logs
}

// --- TOOL MANAGEMENT TYPES ---

// ToolStatus represents the status of a tool
type ToolStatus struct {
	Name          string                 `json:"name"`
	Server        string                 `json:"server"`
	Status        string                 `json:"status"` // "ok", "loading", "not_connected" (awaiting OAuth), or "error"
	Error         string                 `json:"error,omitempty"`
	Description   string                 `json:"description,omitempty"`
	ToolsEnabled  int                    `json:"toolsEnabled"`
	FunctionNames []string               `json:"function_names"`
	Tools         []mcpclient.ToolDetail `json:"tools,omitempty"` // Only populated for detailed requests
	// Caller's private connection state: "connected" or "available" (catalog only).
	// Distinct from Status, which reports whether the server is reachable.
	Connection string `json:"connection"`
	// OAuth requirement, read from config rather than probed. A server needs
	// OAuth if and only if its config carries an oauth block.
	RequiresOAuth bool `json:"requires_oauth,omitempty"`
}

// SetEnabledToolsRequest represents a request to set enabled tools
type SetEnabledToolsRequest struct {
	Enabled []string `json:"enabled_tools"`
	QueryID string   `json:"query_id,omitempty"`
}

// AddServerRequest represents a request to add a server
type AddServerRequest struct {
	Name   string                    `json:"name"`
	Server mcpclient.MCPServerConfig `json:"server"`
}

// EditServerRequest represents a request to edit a server
type EditServerRequest struct {
	Name   string                    `json:"name"`
	Server mcpclient.MCPServerConfig `json:"server"`
}

// RemoveServerRequest represents a request to remove a server
type RemoveServerRequest struct {
	Name string `json:"name"`
}

// classifyConnectionFailure decides whether a failed MCP connection attempt
// should be reported as "needs OAuth" (not_connected) or a genuine error.
// Only relabel as "needs OAuth" when there's genuinely no token yet.
// srvCfg.OAuth != nil alone doesn't mean this failure IS an auth failure:
// A connection failure can be transient even when OAuth is configured.
// Preserve the actual error if the account already has a token.
func classifyConnectionFailure(serverName string, srvCfg mcpclient.MCPServerConfig, connErr error) *ToolStatus {
	toolStatus := &ToolStatus{
		Name:         serverName,
		Server:       serverName,
		Status:       "error",
		Error:        connErr.Error(),
		Description:  srvCfg.Description,
		ToolsEnabled: 0,
	}

	if srvCfg.OAuth != nil && !hasOAuthTokenFile(srvCfg) {
		toolStatus.Status = "not_connected"
		toolStatus.RequiresOAuth = true
		toolStatus.Error = "OAuth authentication required"
	}

	return toolStatus
}

// discoverServerToolsDetailed connects to a specific MCP server and returns detailed tool information using mcpcache
func (api *StreamingAPI) discoverServerToolsDetailed(ctx context.Context, serverName string) (*ToolStatus, error) {
	key := fmt.Sprintf("%p:%s:%s", api, platformMCPTokenUserID, serverName)
	lock, _ := discoveryLocks.LoadOrStore(key, &sync.Mutex{})
	mutex := lock.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()
	api.appendServerLog(serverName, "info", "Loading configuration...")

	// Load merged config to get server details
	cfg, err := api.loadMergedConfig()
	if err != nil {
		api.appendServerLog(serverName, "error", fmt.Sprintf("Failed to load merged config: %v", err))
		api.logger.Error(fmt.Sprintf("Failed to load merged config: %v", err), err)
		// Fallback to base config only
		api.mcpConfig.ReloadConfig(api.mcpConfigPath, api.logger)
		cfg = api.mcpConfig
	}

	// Get server configuration
	srvCfg, err := cfg.GetServer(serverName)
	if err != nil {
		api.appendServerLog(serverName, "error", fmt.Sprintf("Server not found in configuration: %s", serverName))
		return nil, fmt.Errorf("server not found: %s", serverName)
	}

	// Determine protocol for logging
	protocol := "stdio"
	if srvCfg.URL != "" {
		protocol = "http"
		if srvCfg.Protocol == mcpclient.ProtocolSSE {
			protocol = "sse"
		}
	}
	api.appendServerLog(serverName, "info", fmt.Sprintf("Connecting to server (protocol: %s)...", protocol))

	// Resolve the shared platform OAuth credential before cache lookup.
	if srvCfg.OAuth != nil {
		if strings.TrimSpace(srvCfg.OAuth.TokenFile) == "" {
			oauthCfg := *srvCfg.OAuth
			oauthCfg.TokenFile = getUserTokenFilePath(platformMCPTokenUserID, serverName)
			srvCfg.OAuth = &oauthCfg
		}
		cfg.MCPServers[serverName] = srvCfg
	}
	// Discovery only needs metadata. The runtime connection helper deliberately
	// ensures live clients even on cache hits, so do not call it for a hit here.
	if entry, ok := mcpcache.GetCacheManager(api.logger).Get(mcpcache.GenerateUnifiedCacheKey(serverName, srvCfg)); ok {
		status := api.convertCacheEntryToToolStatus(entry)
		return &status, nil
	}
	tmp, err := os.CreateTemp("", "mcp-discovery-*.json")
	if err != nil {
		return nil, err
	}
	tmpConfigPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpConfigPath)
	if err := mcpclient.SaveConfig(tmpConfigPath, cfg); err != nil {
		return nil, err
	}

	// Use mcpcache.GetCachedOrFreshConnection to get cached or fresh connection
	// This is the proper way to get MCP connections with caching
	result, err := mcpcache.GetCachedOrFreshConnection(
		ctx,
		nil, // No LLM needed for tool discovery
		serverName,
		tmpConfigPath, // Use temp merged config path
		nil,           // No tracers for server operations
		api.logger,
		false, // disableCache - use cache by default for server operations
		nil,   // No runtime overrides for tool discovery
	)
	if err != nil {
		api.appendServerLog(serverName, "error", fmt.Sprintf("Connection failed: %v", err))

		toolStatus := classifyConnectionFailure(serverName, srvCfg, err)
		if toolStatus.RequiresOAuth {
			api.appendServerLog(serverName, "warn", "OAuth authentication required")
		}
		return toolStatus, nil
	}

	api.appendServerLog(serverName, "info", "Connection established")
	api.appendServerLog(serverName, "info", "Discovering tools...")

	// Close MCP connections after extracting tool metadata.
	// Background discovery only needs tool names/schemas (metadata), not live connections.
	// Leaving connections open spawns subprocess per server that stays resident,
	// doubling memory usage and causing OOM.
	defer func() {
		for srvName, client := range result.Clients {
			if client != nil {
				if err := client.Close(); err != nil {
					api.logger.Debug(fmt.Sprintf("Failed to close MCP client for %s: %v", srvName, err))
				}
			}
		}
	}()

	// Extract tools for this specific server
	serverTools := api.extractServerTools(result.Tools, result.ToolToServer, serverName)

	// Convert to detailed format
	toolDetails := make([]mcpclient.ToolDetail, 0, len(serverTools))
	functionNames := make([]string, 0, len(serverTools))

	for _, tool := range serverTools {
		// llmtypes.Tool has a Function field that contains the actual tool information
		if tool.Function != nil {
			functionNames = append(functionNames, tool.Function.Name)

			toolDetail := mcpclient.ToolDetail{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  make(map[string]interface{}),
			}

			// Parse Parameters to extract properties and required fields
			if tool.Function.Parameters != nil {
				schemaBytes, err := json.Marshal(tool.Function.Parameters)
				if err == nil {
					var schemaMap map[string]interface{}
					if err := json.Unmarshal(schemaBytes, &schemaMap); err == nil {
						// Extract properties
						if props, ok := schemaMap["properties"].(map[string]interface{}); ok {
							toolDetail.Parameters = props
						}

						// Extract required fields
						if req, ok := schemaMap["required"].([]interface{}); ok {
							for _, reqField := range req {
								if reqStr, ok := reqField.(string); ok {
									toolDetail.Required = append(toolDetail.Required, reqStr)
								}
							}
						}
					}
				}
			}

			toolDetails = append(toolDetails, toolDetail)
		}
	}

	api.appendServerLog(serverName, "info", fmt.Sprintf("Found %d tools", len(serverTools)))

	toolStatus := &ToolStatus{
		Name:          serverName,
		Server:        serverName,
		Status:        "ok",
		Description:   srvCfg.Description,
		ToolsEnabled:  len(serverTools),
		FunctionNames: functionNames,
		Tools:         toolDetails,
	}

	return toolStatus, nil
}

// --- TOOL MANAGEMENT API HANDLERS ---

// handleGetTools handles GET requests to retrieve all tools
func (api *StreamingAPI) handleGetTools(w http.ResponseWriter, r *http.Request) {
	// Connection status is private to the authenticated caller.
	userID := mcpCaller(r.Context())
	if !activeMCPPerson(userID) {
		writeUsersError(w, 401, "sign in first")
		return
	}
	w.Header().Set("Cache-Control", "no-store")

	// Always show all configured servers, not just cached ones
	// This ensures users see all servers including those that are loading or failed

	// Load merged config (base + user additions)
	cfg, err := api.loadMergedConfig()
	if err != nil {
		api.logger.Error(fmt.Sprintf("Failed to load merged config: %v", err), err)
		// Fallback to base config only
		api.mcpConfig.ReloadConfig(api.mcpConfigPath, api.logger)
		cfg = api.mcpConfig
	}

	allResults := make([]ToolStatus, 0, len(cfg.MCPServers))
	for name, config := range cfg.MCPServers {
		status := api.mcpToolStatusForUser(name, userID, config)
		status.Description = config.Description
		status.RequiresOAuth = config.OAuth != nil
		allResults = append(allResults, status)
	}
	private, _ := listPlaceMCPServers(userID)
	for _, server := range private {
		if _, catalog := cfg.MCPServers[server.Name]; catalog {
			continue
		}
		if server.Catalog != "" {
			if _, catalog := cfg.MCPServers[server.Catalog]; catalog {
				continue
			}
		}
		allResults = append(allResults, api.mcpToolStatusForUser(server.Name, userID, mcpclient.MCPServerConfig{}))
	}

	// Sort results alphabetically by server name
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].Name < allResults[j].Name
	})

	// Return comprehensive results showing all servers
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(allResults)
}

// handleGetToolDetail handles GET requests to retrieve detailed tool information for a specific server
func (api *StreamingAPI) handleGetToolDetail(w http.ResponseWriter, r *http.Request) {
	serverName := r.URL.Query().Get("server_name")
	if serverName == "" {
		http.Error(w, "server_name parameter is required", http.StatusBadRequest)
		return
	}

	// Discovery resolves only the caller's private or permitted Vault connection.
	userID := mcpCaller(r.Context())

	// If no cached detailed results, fetch them and cache
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	result, err := api.discoverGovernedServerTools(ctx, userID, serverName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// The cache is already keyed by the isolated runtime configuration. Do not
	// copy private metadata into the platform cache or consult platform tokens.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleSetEnabledTools handles POST requests to set enabled tools
func (api *StreamingAPI) handleSetEnabledTools(w http.ResponseWriter, r *http.Request) {
	var req SetEnabledToolsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.QueryID == "" {
		http.Error(w, "Missing query_id", http.StatusBadRequest)
		return
	}
	api.toolStatusMux.Lock()
	api.enabledTools[req.QueryID] = req.Enabled
	api.toolStatusMux.Unlock()
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok"})
}

// handleAddServer handles POST requests to add a server
func (api *StreamingAPI) handleAddServer(w http.ResponseWriter, r *http.Request) {
	// Check if MCP config is locked
	if isMCPConfigLocked() {
		http.Error(w, "MCP configuration is locked by administrator", http.StatusForbidden)
		return
	}

	var req AddServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := api.mcpConfig.AddServer(req.Name, req.Server, api.mcpConfigPath); err != nil {
		api.appendServerLog(req.Name, "error", fmt.Sprintf("Failed to add server: %v", err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	api.appendServerLog(req.Name, "info", "Server added to configuration")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok"})
}

// handleEditServer handles POST requests to edit a server
func (api *StreamingAPI) handleEditServer(w http.ResponseWriter, r *http.Request) {
	// Check if MCP config is locked
	if isMCPConfigLocked() {
		http.Error(w, "MCP configuration is locked by administrator", http.StatusForbidden)
		return
	}

	var req EditServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := api.mcpConfig.EditServer(req.Name, req.Server, api.mcpConfigPath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok"})
}

// handleRemoveServer handles POST requests to remove a server
func (api *StreamingAPI) handleRemoveServer(w http.ResponseWriter, r *http.Request) {
	// Check if MCP config is locked
	if isMCPConfigLocked() {
		http.Error(w, "MCP configuration is locked by administrator", http.StatusForbidden)
		return
	}

	var req RemoveServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := api.mcpConfig.RemoveServer(req.Name, api.mcpConfigPath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok"})
}

// --- BACKGROUND TOOL DISCOVERY ---

// discoveryFailedFile is the on-disk format for persisted discovery failures.
// It includes a config hash so that manual edits to the JSON config file
// automatically invalidate the persisted failures.
type discoveryFailedFile struct {
	ConfigHash string            `json:"config_hash"`
	Servers    map[string]string `json:"servers"`
}

// configFileHash returns a SHA-256 hash of the user MCP config file contents.
// If the file changes (manual edit, API update, etc.) the hash changes.
func (api *StreamingAPI) configFileHash() string {
	userConfigPath := strings.TrimSuffix(api.mcpConfigPath, ".json") + "_user.json"
	data, err := os.ReadFile(userConfigPath)
	if err != nil {
		// Fall back to base config
		data, err = os.ReadFile(api.mcpConfigPath)
		if err != nil {
			return ""
		}
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// persistDiscoveryFailedServers saves the failed servers map to disk so it
// survives server restarts. This prevents wasting ~19s per OAuth-failing server
// on every startup. A config hash is stored alongside so that manual config
// edits automatically invalidate the persisted failures.
func (api *StreamingAPI) persistDiscoveryFailedServers() {
	cacheManager := mcpcache.GetCacheManager(api.logger)
	filePath := filepath.Join(cacheManager.GetCacheDirectory(), "discovery_failed_servers.json")

	payload := discoveryFailedFile{
		ConfigHash: api.configFileHash(),
		Servers:    api.discoveryFailedServers,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		api.logger.Warn(fmt.Sprintf("Failed to marshal discoveryFailedServers: %v", err))
		return
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		api.logger.Warn(fmt.Sprintf("Failed to create cache directory: %v", err))
		return
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		api.logger.Warn(fmt.Sprintf("Failed to persist discoveryFailedServers: %v", err))
		return
	}

	api.logger.Info(fmt.Sprintf("Persisted %d failed servers to disk", len(api.discoveryFailedServers)))
}

// loadDiscoveryFailedServers loads previously failed servers from disk.
// If the MCP config file has changed since the failures were persisted,
// the file is discarded so all servers are retried.
func (api *StreamingAPI) loadDiscoveryFailedServers() {
	cacheManager := mcpcache.GetCacheManager(api.logger)
	filePath := filepath.Join(cacheManager.GetCacheDirectory(), "discovery_failed_servers.json")

	data, err := os.ReadFile(filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			api.logger.Warn(fmt.Sprintf("Failed to read discoveryFailedServers from disk: %v", err))
		}
		return
	}

	var payload discoveryFailedFile
	if err := json.Unmarshal(data, &payload); err != nil {
		api.logger.Warn(fmt.Sprintf("Failed to unmarshal discoveryFailedServers: %v", err))
		return
	}

	// If config changed, discard stale failures so servers are retried
	currentHash := api.configFileHash()
	if payload.ConfigHash != currentHash {
		api.logger.Info("MCP config changed since last run — clearing persisted failed servers")
		_ = os.Remove(filePath)
		return
	}

	api.discoveryFailedServers = payload.Servers
	api.logger.Info(fmt.Sprintf("Loaded %d previously failed servers from disk", len(payload.Servers)))
	for name, reason := range payload.Servers {
		api.logger.Debug(fmt.Sprintf("  Previously failed: %s — %s", name, reason))
	}
}

// deleteDiscoveryFailedServersFile removes the persisted file (used on config reload).
func (api *StreamingAPI) deleteDiscoveryFailedServersFile() {
	cacheManager := mcpcache.GetCacheManager(api.logger)
	filePath := filepath.Join(cacheManager.GetCacheDirectory(), "discovery_failed_servers.json")
	_ = os.Remove(filePath)
}

func (api *StreamingAPI) clearDiscoveryFailure(serverName string) {
	if _, failed := api.discoveryFailedServers[serverName]; !failed {
		return
	}

	delete(api.discoveryFailedServers, serverName)
	if len(api.discoveryFailedServers) == 0 {
		api.deleteDiscoveryFailedServersFile()
	} else {
		api.persistDiscoveryFailedServers()
	}
	api.logger.Info(fmt.Sprintf("🔄 Cleared failed discovery state for %s", serverName))
}

// hasOAuthTokenFile checks whether a server's OAuth token file exists on disk.
// Returns true if the server doesn't use OAuth or if the token file is present.
func hasOAuthTokenFile(cfg mcpclient.MCPServerConfig) bool {
	if cfg.OAuth == nil {
		return true // no OAuth needed
	}
	tokenFile := cfg.OAuth.TokenFile
	if tokenFile == "" {
		tokenFile = filepath.Join(mcpagentTokensRoot(), "default.json")
	}
	if strings.HasPrefix(tokenFile, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			tokenFile = filepath.Join(home, tokenFile[2:])
		}
	}
	_, err := os.Stat(tokenFile)
	return err == nil
}

// Connection states reported to the UI. "connected" means the user added this
// server; "available" means it is offered by the catalog but not connected.
const (
	connectionConnected = "connected"
	connectionAvailable = "available"
)

// loadOverlayServerNames returns the set of server names present in the user
// config overlay. Overlay membership is the definition of "connected": the
// base catalog is what we offer, the overlay is what the user took. OAuth
// callbacks already write there on success, so this was already true for OAuth
// servers before it was made explicit. Callers that iterate many servers must
// read this once and pass the result into connectionState rather than
// re-reading the file per server.
//nolint:unused // its caller was dropped in the Vault checkpoint; kept for its owner (PLAT-466 only unblocks the cmd/server lint gate)
func (api *StreamingAPI) loadOverlayServerNames() map[string]bool {
	names := make(map[string]bool)
	overlay, err := mcpclient.LoadConfig(api.getUserConfigPath(), api.logger)
	if err != nil {
		// A missing overlay is the normal first-run state, not an error worth
		// failing the request over — it simply means nothing is connected yet.
		api.logger.Debug(fmt.Sprintf("No user config overlay readable: %v", err))
		return names
	}
	for name := range overlay.MCPServers {
		names[name] = true
	}
	return names
}

// connectionState answers "is this server connected to the platform?" —
// distinct from Status, which answers "is it working?".
func connectionState(name string, cfg mcpclient.MCPServerConfig, overlay map[string]bool, userID string) string {
	_ = userID
	if !overlay[name] && !(cfg.OAuth != nil && strings.TrimSpace(cfg.OAuth.TokenFile) != "" && hasOAuthTokenFile(cfg)) {
		return connectionAvailable
	}
	if cfg.OAuth == nil {
		return connectionConnected // no credential needed
	}
	tokenFile := cfg.OAuth.TokenFile
	if strings.TrimSpace(tokenFile) == "" {
		tokenFile = getUserTokenFilePath(platformMCPTokenUserID, name)
	}
	if _, err := os.Stat(expandPath(tokenFile)); err != nil {
		return connectionAvailable
	}
	return connectionConnected
}

// initializeToolCache initializes the tool cache on server startup using existing mcpcache service
func (api *StreamingAPI) initializeToolCache() {
	api.logger.Info("🚀 Initializing tool cache on server startup using existing mcpcache service...")

	// Get the existing cache manager
	cacheManager := mcpcache.GetCacheManager(api.logger)

	// Load previously failed servers from disk to avoid re-attempting OAuth failures
	api.loadDiscoveryFailedServers()

	// Log cache statistics
	stats := cacheManager.GetStats()
	api.logger.Info(fmt.Sprintf("📊 Cache stats: total_entries=%v, valid_entries=%v, cache_dir=%v",
		stats["total_entries"], stats["valid_entries"], stats["cache_directory"]))

	// Load merged config (base + user additions)
	cfg, err := api.loadMergedConfig()
	if err != nil {
		api.logger.Error(fmt.Sprintf("Failed to load merged config: %v", err), err)
		// Fallback to base config only
		api.mcpConfig.ReloadConfig(api.mcpConfigPath, api.logger)
		cfg = api.mcpConfig
	}

	api.logger.Info(fmt.Sprintf("📋 Loaded %d servers from config", len(cfg.MCPServers)))

	cachedServers := 0
	missedServers := 0
	for serverName := range cfg.MCPServers {
		// Get server configuration for cache key generation
		serverConfig, exists := cfg.MCPServers[serverName]
		if !exists {
			continue
		}
		// OAuth metadata belongs to the requesting account, not the catalog.
		if serverConfig.OAuth != nil {
			continue
		}

		// Try to get cached entry using configuration-aware key
		cacheKey := mcpcache.GenerateUnifiedCacheKey(serverName, serverConfig)
		if entry, exists := cacheManager.Get(cacheKey); exists {
			cachedServers++
			api.logger.Debug(fmt.Sprintf("✅ Cache HIT for server %s (tools=%d)", serverName, len(entry.Tools)))
			// Convert cached entry to ToolStatus
			toolStatus := api.convertCacheEntryToToolStatus(entry)
			api.toolStatusMux.Lock()
			api.toolStatus[serverName] = toolStatus
			api.toolStatusMux.Unlock()
		} else {
			missedServers++
			// Truncate cache key for logging
			truncatedKey := cacheKey
			if len(cacheKey) > 50 {
				truncatedKey = cacheKey[:50] + "..."
			}
			api.logger.Debug(fmt.Sprintf("❌ Cache MISS for server %s (key=%s)", serverName, truncatedKey))
		}
	}

	api.logger.Info(fmt.Sprintf("📊 Cache lookup results: %d hits, %d misses out of %d total servers",
		cachedServers, missedServers, len(cfg.MCPServers)))

	if cachedServers > 0 {
		api.logger.Info(fmt.Sprintf("✅ Loaded %d servers from existing mcpcache", cachedServers))
	}

	// Cache misses remain idle until a selected server is needed.

}

// convertCacheEntryToToolStatus converts a mcpcache.CacheEntry to ToolStatus
func (api *StreamingAPI) convertCacheEntryToToolStatus(entry *mcpcache.CacheEntry) ToolStatus {
	functionNames := make([]string, 0, len(entry.Tools))
	toolDetails := make([]mcpclient.ToolDetail, 0, len(entry.Tools))

	for _, tool := range entry.Tools {
		// llmtypes.Tool has a Function field that contains the actual tool information
		if tool.Function != nil {
			functionNames = append(functionNames, tool.Function.Name)

			toolDetail := mcpclient.ToolDetail{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  make(map[string]interface{}),
			}

			// Parse Parameters to extract properties and required fields
			if tool.Function.Parameters != nil {
				schemaBytes, err := json.Marshal(tool.Function.Parameters)
				if err == nil {
					var schemaMap map[string]interface{}
					if err := json.Unmarshal(schemaBytes, &schemaMap); err == nil {
						// Extract properties
						if props, ok := schemaMap["properties"].(map[string]interface{}); ok {
							toolDetail.Parameters = props
						}

						// Extract required fields
						if req, ok := schemaMap["required"].([]interface{}); ok {
							for _, reqField := range req {
								if reqStr, ok := reqField.(string); ok {
									toolDetail.Required = append(toolDetail.Required, reqStr)
								}
							}
						}
					}
				}
			}

			toolDetails = append(toolDetails, toolDetail)
		}
	}

	status := "ok"
	if !entry.IsValid {
		status = "error"
	}

	return ToolStatus{
		Name:          entry.ServerName,
		Server:        entry.ServerName,
		Status:        status,
		Error:         entry.ErrorMessage,
		ToolsEnabled:  len(entry.Tools),
		FunctionNames: functionNames,
		Tools:         toolDetails,
	}
}

// extractServerTools extracts tools specific to a server from the aggregated tool list
func (api *StreamingAPI) extractServerTools(allTools []llmtypes.Tool, toolToServer map[string]string, serverName string) []llmtypes.Tool {
	var serverTools []llmtypes.Tool
	for _, tool := range allTools {
		if tool.Function != nil {
			if srv, exists := toolToServer[tool.Function.Name]; exists && srv == serverName {
				serverTools = append(serverTools, tool)
			}
		}
	}
	return serverTools
}

// Discovery is scoped to an explicit server and requesting user. There is no
// catalog-wide scan or periodic network refresh.
func (api *StreamingAPI) startServerDiscovery(userID, serverName string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		ctx = context.WithValue(ctx, discoveryUserKey{}, userID)
		result, err := api.discoverServerToolsDetailed(ctx, serverName)
		if err != nil {
			api.appendServerLog(serverName, "error", fmt.Sprintf("Discovery failed: %v", err))
			return
		}
		// OAuth tool lists may be account-specific; never publish them globally.
		cfg, err := api.loadMergedConfig()
		if err == nil && cfg.MCPServers[serverName].OAuth == nil {
			api.toolStatusMux.Lock()
			api.toolStatus[serverName] = *result
			api.toolStatusMux.Unlock()
		}
	}()
}

type discoveryUserKey struct{}

var discoveryLocks sync.Map

// stopPeriodicRefresh stops the periodic refresh
func (api *StreamingAPI) stopPeriodicRefresh() {
	api.discoveryMux.Lock()
	defer api.discoveryMux.Unlock()

	if api.discoveryTicker != nil {
		api.discoveryTicker.Stop()
		api.discoveryTicker = nil
		api.logger.Info("⏹️ Stopped periodic tool discovery refresh")
	}
}

// --- MCP/CUSTOM/VIRTUAL EXECUTION APIs MOVED TO mcpagent/executor ---
// These handlers are now provided by the mcpagent/executor library.
// See: mcpagent/executor/handlers.go
// Routes are wired in server.go using executor.NewExecutorHandlers()
