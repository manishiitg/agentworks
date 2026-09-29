package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/mcpcache"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// isMCPConfigLocked returns true when MCP configuration is locked by admin (read-only mode).
func isMCPConfigLocked() bool {
	return os.Getenv("MCP_CONFIG_LOCKED") == "true" || os.Getenv("MCP_CONFIG_LOCKED") == "1"
}

// MCPConfigRequest represents a request to save MCP config
type MCPConfigRequest struct {
	Config mcpclient.MCPConfig `json:"config"`
}

// MCPConfigResponse represents the response for config operations
type MCPConfigResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Servers int    `json:"servers,omitempty"`
}

// customServers returns the overlay entries that the user actually authored:
// everything in the user config whose name is NOT in the base catalog.
//
// The overlay is dual-purpose, which is the whole subtlety here. Connecting a
// catalog connector writes that catalog server into the same file (see
// persistOAuthConfig), because overlay membership is what "connected" means.
// So the overlay is a mix of connection records for base servers and genuine
// custom servers, and only the latter belong to the JSON editor.
func (api *StreamingAPI) customServers(overlay *mcpclient.MCPConfig) map[string]mcpclient.MCPServerConfig {
	custom := make(map[string]mcpclient.MCPServerConfig)
	for name, server := range overlay.MCPServers {
		if _, isBase := api.mcpConfig.MCPServers[name]; !isBase {
			custom[name] = server
		}
	}
	return custom
}

// loadOverlay reads the user config overlay, treating a missing file as empty.
func (api *StreamingAPI) loadOverlay() *mcpclient.MCPConfig {
	overlay, err := mcpclient.LoadConfig(api.getUserConfigPath(), api.logger)
	if err != nil {
		api.logger.Debug(fmt.Sprintf("No user config overlay readable: %v", err))
		return &mcpclient.MCPConfig{MCPServers: make(map[string]mcpclient.MCPServerConfig)}
	}
	if overlay.MCPServers == nil {
		overlay.MCPServers = make(map[string]mcpclient.MCPServerConfig)
	}
	return overlay
}

// writeJSONError replies with a JSON body, which is what the frontend parses.
// http.Error sends text/plain, so the client's error path could never read the
// reason and reported every failure as a JSON syntax error.
func writeJSONError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message, "message": message})
}

// handleGetMCPConfig returns the user's OWN MCP servers -- not the catalog.
//
// This endpoint backs the "Add via JSON" editor, which is a full-document
// replace: whatever comes back is what the user edits and posts. Returning the
// merged catalog therefore handed them an editor in which the 113 built-in
// connectors looked editable and deletable, when in fact the save path silently
// discarded every change to them. Scoping the document to the user's own
// servers makes what is shown and what is saved the same thing.
func (api *StreamingAPI) handleGetMCPConfig(w http.ResponseWriter, r *http.Request) {
	if err := api.mcpConfig.ReloadConfig(api.mcpConfigPath, api.logger); err != nil {
		api.logger.Error(fmt.Sprintf("Failed to reload base MCP config: %v", err), err)
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to reload base config: %v", err))
		return
	}

	custom := api.customServers(api.loadOverlay())

	names := make([]string, 0, len(custom))
	for name := range custom {
		names = append(names, name)
	}
	sort.Strings(names)

	api.logger.Debug(fmt.Sprintf("Returning %d user-defined MCP servers (%d base servers withheld)",
		len(custom), len(api.mcpConfig.MCPServers)))

	// Written by hand so the servers keep a stable alphabetical order; Go maps
	// marshal in a random one, which would reshuffle the editor on every load.
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, "{\n  \"mcp_config_locked\": %v,\n  \"mcpServers\": {", isMCPConfigLocked())
	for i, name := range names {
		serverJSON, _ := json.Marshal(redactOAuthClientSecret(custom[name]))
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, "\n    %s: %s", mustMarshalString(name), string(serverJSON))
	}
	if len(names) > 0 {
		fmt.Fprint(w, "\n  ")
	}
	fmt.Fprint(w, "}\n}")
}

// mustMarshalString quotes a map key the way encoding/json would, so a server
// name containing a quote or backslash cannot break out of the document.
func mustMarshalString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// handleSaveMCPConfig replaces the user's own MCP servers with the posted set.
//
// Two invariants hold here:
//
//  1. Base catalog servers are rejected outright rather than silently dropped.
//     The previous code filtered them out, so editing a built-in connector
//     looked like it worked and then did nothing. This mirrors the rule the
//     add_mcp_server chat tool already enforces.
//
//  2. Overlay entries for base servers are carried over untouched. They are
//     connection records, not configuration, and a blind overwrite here
//     disconnected every connected catalog connector on each save.
func (api *StreamingAPI) handleSaveMCPConfig(w http.ResponseWriter, r *http.Request) {
	if isMCPConfigLocked() {
		writeJSONError(w, http.StatusForbidden, "MCP configuration is locked by administrator")
		return
	}

	var req MCPConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}
	if req.Config.MCPServers == nil {
		writeJSONError(w, http.StatusBadRequest, "mcpServers field is required")
		return
	}

	if err := api.mcpConfig.ReloadConfig(api.mcpConfigPath, api.logger); err != nil {
		api.logger.Error(fmt.Sprintf("Failed to reload base config: %v", err), err)
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to reload base config: %v", err))
		return
	}

	// Reject catalog names instead of dropping them, so the editor cannot
	// appear to rename, retarget or delete a built-in connector.
	reserved := make([]string, 0)
	for name := range req.Config.MCPServers {
		if _, isBase := api.mcpConfig.MCPServers[name]; isBase {
			reserved = append(reserved, name)
		}
	}
	if len(reserved) > 0 {
		sort.Strings(reserved)
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf(
			"%s %s built-in connector%s and cannot be edited here. Connect %s from the connector directory instead.",
			strings.Join(reserved, ", "),
			map[bool]string{true: "is a", false: "are"}[len(reserved) == 1],
			map[bool]string{true: "", false: "s"}[len(reserved) == 1],
			map[bool]string{true: "it", false: "them"}[len(reserved) == 1]))
		return
	}

	// An empty document is legitimate: it means "I removed my last custom
	// server". Only validate the shape of servers that are actually present.
	if len(req.Config.MCPServers) > 0 {
		if err := api.validateMCPConfig(&req.Config); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Config validation failed: %v", err))
			return
		}
	}

	// Read-modify-write: keep every base-server overlay entry (connections),
	// replace only the custom ones.
	overlay := api.loadOverlay()
	merged := &mcpclient.MCPConfig{MCPServers: make(map[string]mcpclient.MCPServerConfig)}
	keptConnections := 0
	for name, server := range overlay.MCPServers {
		if _, isBase := api.mcpConfig.MCPServers[name]; isBase {
			merged.MCPServers[name] = server
			keptConnections++
		}
	}
	for name, server := range req.Config.MCPServers {
		// The editor never sees a client secret (redacted on read), so an
		// unchanged client keeps the stored one.
		if previous, ok := overlay.MCPServers[name]; ok && server.OAuth != nil && previous.OAuth != nil &&
			server.OAuth.ClientSecret == "" && server.OAuth.ClientID == previous.OAuth.ClientID {
			copied := *server.OAuth
			copied.ClientSecret = previous.OAuth.ClientSecret
			server.OAuth = &copied
		}
		merged.MCPServers[name] = server
	}

	removed := make([]string, 0)
	for name := range api.customServers(overlay) {
		if _, stillThere := req.Config.MCPServers[name]; !stillThere {
			removed = append(removed, name)
		}
	}

	if err := mcpclient.SaveConfig(api.getUserConfigPath(), merged); err != nil {
		api.logger.Error(fmt.Sprintf("Failed to save user MCP config: %v", err), err)
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to save user config: %v", err))
		return
	}
	_ = os.Chmod(api.getUserConfigPath(), 0o600) // it can hold OAuth client secrets

	for name := range req.Config.MCPServers {
		api.appendServerLog(name, "info", "Configuration saved, triggering discovery...")
	}
	for _, name := range removed {
		api.appendServerLog(name, "info", "Server removed from user configuration")
	}

	go api.triggerMCPDiscovery()

	api.logger.Info(fmt.Sprintf("✅ Saved %d user-defined MCP server(s); %d connector connection(s) preserved, %d removed",
		len(req.Config.MCPServers), keptConnections, len(removed)))

	response := MCPConfigResponse{
		Status:  "saved",
		Message: "User config saved and discovery triggered",
		Servers: len(req.Config.MCPServers),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleDiscoverServers handles POST requests to trigger MCP server discovery
func (api *StreamingAPI) handleDiscoverServers(w http.ResponseWriter, r *http.Request) {
	// Trigger background discovery
	go api.triggerMCPDiscovery()

	api.logger.Info("🔄 MCP server discovery triggered manually")

	response := MCPConfigResponse{
		Status:  "discovery_started",
		Message: "Server discovery started in background",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// validateMCPConfig validates the MCP config before saving
func (api *StreamingAPI) validateMCPConfig(config *mcpclient.MCPConfig) error {
	if config.MCPServers == nil {
		return fmt.Errorf("mcpServers field is required")
	}

	if len(config.MCPServers) == 0 {
		return fmt.Errorf("at least one server must be configured")
	}

	// Check for duplicate server names
	serverNames := make(map[string]bool)
	for name, server := range config.MCPServers {
		if name == "" {
			return fmt.Errorf("server name cannot be empty")
		}

		if serverNames[name] {
			return fmt.Errorf("duplicate server name: %s", name)
		}
		serverNames[name] = true

		// Validate protocol-specific fields
		if server.URL != "" {
			// SSE/HTTP server
			if server.Command != "" || len(server.Args) > 0 {
				return fmt.Errorf("server %s: cannot have both URL and command/args", name)
			}
		} else {
			// stdio server
			if server.Command == "" {
				return fmt.Errorf("server %s: command is required for stdio servers", name)
			}
		}

		// Validate protocol field if present
		if server.Protocol != "" {
			validProtocols := []mcpclient.ProtocolType{
				mcpclient.ProtocolStdio,
				mcpclient.ProtocolSSE,
				mcpclient.ProtocolHTTP,
			}
			isValid := false
			for _, valid := range validProtocols {
				if server.Protocol == valid {
					isValid = true
					break
				}
			}
			if !isValid {
				return fmt.Errorf("server %s: invalid protocol '%s', must be one of: %v", name, server.Protocol, validProtocols)
			}
		}
	}

	return nil
}

// loadMergedConfig loads the merged configuration (base + user additions)
func (api *StreamingAPI) loadMergedConfig() (*mcpclient.MCPConfig, error) {
	// Reload base config to get latest version
	if err := api.mcpConfig.ReloadConfig(api.mcpConfigPath, api.logger); err != nil {
		return nil, fmt.Errorf("failed to reload base config: %w", err)
	}

	// Load user additions (if any)
	userConfigPath := strings.Replace(api.mcpConfigPath, ".json", "_user.json", 1)
	api.logger.Debug(fmt.Sprintf("🔍 Attempting to load user config from: %s", userConfigPath))

	userConfig, err := mcpclient.LoadConfig(userConfigPath, api.logger)
	if err != nil {
		// User config doesn't exist yet, use empty config
		userConfig = &mcpclient.MCPConfig{MCPServers: make(map[string]mcpclient.MCPServerConfig)}
		api.logger.Debug(fmt.Sprintf("❌ No user config found at %s, using empty user config. Error: %v", userConfigPath, err))
	} else {
		api.logger.Debug(fmt.Sprintf("✅ Successfully loaded user config from %s with %d servers", userConfigPath, len(userConfig.MCPServers)))
		for serverName := range userConfig.MCPServers {
			api.logger.Debug(fmt.Sprintf("  📋 User config server: %s", serverName))
		}
	}

	// Merge base config with user additions. Keep this local because older
	// mcpagent releases do not export a merge helper.
	mergedConfig := &mcpclient.MCPConfig{MCPServers: make(map[string]mcpclient.MCPServerConfig, len(api.mcpConfig.MCPServers)+len(userConfig.MCPServers))}
	for name, server := range api.mcpConfig.MCPServers {
		mergedConfig.MCPServers[name] = server
	}
	for name, server := range userConfig.MCPServers {
		mergedConfig.MCPServers[name] = server
	}

	api.logger.Debug(fmt.Sprintf("Merged config: %d base servers + %d user servers = %d total",
		len(api.mcpConfig.MCPServers), len(userConfig.MCPServers), len(mergedConfig.MCPServers)))

	// Debug: List all servers in merged config
	api.logger.Debug("🔍 Final merged config servers:")
	for serverName := range mergedConfig.MCPServers {
		api.logger.Debug(fmt.Sprintf("  📋 Merged server: %s", serverName))
	}

	return mergedConfig, nil
}

// createTempMergedConfig creates a temporary merged config file and returns its path

// triggerMCPDiscovery triggers MCP server discovery in the background
func (api *StreamingAPI) triggerMCPDiscovery() {
	// Configuration updates reload metadata only, never connect to the catalog.
	api.toolStatusMux.Lock()
	api.toolStatus = make(map[string]ToolStatus)
	api.toolStatusMux.Unlock()
	api.initializeToolCache()
}

// handleGetMCPConfigStatus handles GET requests to get config status
func (api *StreamingAPI) handleGetMCPConfigStatus(w http.ResponseWriter, r *http.Request) {
	// Reload base config to get latest version
	if err := api.mcpConfig.ReloadConfig(api.mcpConfigPath, api.logger); err != nil {
		api.logger.Error(fmt.Sprintf("Failed to reload base MCP config: %v", err), err)
		http.Error(w, fmt.Sprintf("Failed to reload base config: %v", err), http.StatusInternalServerError)
		return
	}

	// Load user additions
	userConfigPath := strings.Replace(api.mcpConfigPath, ".json", "_user.json", 1)
	userConfig, err := mcpclient.LoadConfig(userConfigPath, api.logger)
	if err != nil {
		// User config doesn't exist yet
		userConfig = &mcpclient.MCPConfig{MCPServers: make(map[string]mcpclient.MCPServerConfig)}
	}

	// Get cache manager
	cacheManager := mcpcache.GetCacheManager(api.logger)
	cacheStats := cacheManager.GetStats()

	// Count discovered servers
	api.toolStatusMux.RLock()
	discoveredCount := len(api.toolStatus)
	api.toolStatusMux.RUnlock()

	// Check discovery status
	api.discoveryMux.RLock()
	isDiscoveryRunning := api.discoveryRunning
	lastDiscovery := api.lastDiscovery
	api.discoveryMux.RUnlock()

	// Collect base server names
	baseServerNames := make([]string, 0, len(api.mcpConfig.MCPServers))
	for name := range api.mcpConfig.MCPServers {
		baseServerNames = append(baseServerNames, name)
	}
	sort.Strings(baseServerNames)

	status := map[string]interface{}{
		"config_path":        api.mcpConfigPath,
		"user_config_path":   userConfigPath,
		"base_servers":       len(api.mcpConfig.MCPServers),
		"base_server_names":  baseServerNames,
		"user_servers":       len(userConfig.MCPServers),
		"total_servers":      len(api.mcpConfig.MCPServers) + len(userConfig.MCPServers),
		"discovered_servers": discoveredCount,
		"discovery_running":  isDiscoveryRunning,
		"last_discovery":     lastDiscovery.Format(time.RFC3339),
		"cache_stats":        cacheStats,
		"mcp_config_locked":  isMCPConfigLocked(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleGetServerLogs handles GET requests to retrieve per-server install/connection logs
func (api *StreamingAPI) handleGetServerLogs(w http.ResponseWriter, r *http.Request) {
	serverName := r.URL.Query().Get("server_name")

	api.serverLogsMux.RLock()
	defer api.serverLogsMux.RUnlock()

	result := make(map[string][]ServerLogEntry)

	if serverName != "" {
		// Return logs for a specific server
		if logs, exists := api.serverLogs[serverName]; exists {
			result[serverName] = logs
		} else {
			result[serverName] = []ServerLogEntry{}
		}
	} else {
		// Return logs for all servers
		for name, logs := range api.serverLogs {
			result[name] = logs
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"logs": result,
	})
}

// redactOAuthClientSecret drops an OAuth client secret from a config the API
// returns; the stored copy keeps it.
func redactOAuthClientSecret(server mcpclient.MCPServerConfig) mcpclient.MCPServerConfig {
	if server.OAuth != nil && server.OAuth.ClientSecret != "" {
		copied := *server.OAuth
		copied.ClientSecret = ""
		server.OAuth = &copied
	}
	return server
}
