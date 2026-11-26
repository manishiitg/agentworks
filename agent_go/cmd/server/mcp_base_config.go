package server

import (
	"maps"

	"github.com/manishiitg/mcpagent/mcpclient"
)

// The base MCP config (api.mcpConfig) is shared by every request: the server
// editor changes its map and saves it while discovery, listings and chat start
// read it. A map read during a write is a fatal error that stops the whole
// server, so every access goes through these helpers under api.mcpConfigMu.

// reloadBaseMCPConfig re-reads the base config from disk.
func (api *StreamingAPI) reloadBaseMCPConfig() error {
	api.mcpConfigMu.Lock()
	defer api.mcpConfigMu.Unlock()
	return api.mcpConfig.ReloadConfig(api.mcpConfigPath, api.logger)
}

// baseMCPConfig returns a private copy of the base config; callers may change it.
func (api *StreamingAPI) baseMCPConfig() *mcpclient.MCPConfig {
	api.mcpConfigMu.RLock()
	defer api.mcpConfigMu.RUnlock()
	out := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{}}
	if api.mcpConfig != nil {
		maps.Copy(out.MCPServers, api.mcpConfig.MCPServers)
	}
	return out
}

// reloadedBaseMCPConfig re-reads the base config and returns a private copy.
func (api *StreamingAPI) reloadedBaseMCPConfig() (*mcpclient.MCPConfig, error) {
	if err := api.reloadBaseMCPConfig(); err != nil {
		return nil, err
	}
	return api.baseMCPConfig(), nil
}

// changeBaseMCPConfig applies one edit (AddServer, EditServer or RemoveServer,
// which also save the file) under the lock.
func (api *StreamingAPI) changeBaseMCPConfig(change func(*mcpclient.MCPConfig) error) error {
	api.mcpConfigMu.Lock()
	defer api.mcpConfigMu.Unlock()
	return change(api.mcpConfig)
}
