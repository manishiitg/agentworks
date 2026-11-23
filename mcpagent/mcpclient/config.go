package mcpclient

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/oauth"
)

// ProtocolType defines the connection protocol
type ProtocolType string

const (
	ProtocolStdio ProtocolType = "stdio"
	ProtocolSSE   ProtocolType = "sse"
	ProtocolHTTP  ProtocolType = "http"
)

// Special server name constants
const (
	// AllServers indicates that all configured MCP servers should be connected
	// This is the default behavior when no specific server is requested
	AllServers = "all"

	// NoServers indicates that no MCP servers should be connected
	// This is used when an agent should work with pure LLM reasoning without any tools
	NoServers = "NO_SERVERS"
)

// PoolConfig defines connection pooling settings
type PoolConfig struct {
	MaxConnections       int           `json:"max_connections"`
	MinConnections       int           `json:"min_connections"`
	MaxIdleTime          time.Duration `json:"max_idle_time"`
	HealthCheckInterval  time.Duration `json:"health_check_interval"`
	ConnectionTimeout    time.Duration `json:"connection_timeout"`
	ReconnectDelay       time.Duration `json:"reconnect_delay"`
	MaxReconnectAttempts int           `json:"max_reconnect_attempts"`
}

// DefaultPoolConfig returns sensible default pooling configuration
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxConnections:       20,
		MinConnections:       2,
		MaxIdleTime:          15 * time.Minute, // Increased from 5 to 15 minutes
		HealthCheckInterval:  2 * time.Minute,  // Increased from 30s to 2 minutes
		ConnectionTimeout:    15 * time.Minute, // Increased from 10 minutes to 15 minutes for very slow npx commands
		ReconnectDelay:       2 * time.Second,
		MaxReconnectAttempts: 3,
	}
}

// ServerConfig represents a single MCP server configuration
type ServerConfig struct {
	Name        string            `json:"name"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	WorkingDir  string            `json:"working_dir,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Description string            `json:"description,omitempty"`
	Protocol    ProtocolType      `json:"protocol"`
	PoolConfig  PoolConfig        `json:"pool_config"`
	// SSE/HTTP specific fields
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// NewServerConfig creates a new server configuration with defaults
func NewServerConfig(name string, protocol ProtocolType) ServerConfig {
	return ServerConfig{
		Name:       name,
		Protocol:   protocol,
		PoolConfig: DefaultPoolConfig(),
		Headers:    make(map[string]string),
		Env:        make(map[string]string),
	}
}

type MCPServerConfig struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env,omitempty"`
	WorkingDir  string            `json:"working_dir,omitempty"`
	Description string            `json:"description,omitempty"`
	Protocol    ProtocolType      `json:"protocol,omitempty"`
	PoolConfig  *PoolConfig       `json:"pool_config,omitempty"`
	// SSE/HTTP specific fields
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// OAuth configuration
	OAuth *oauth.OAuthConfig `json:"oauth,omitempty"`
	// PublicOnly marks a user-supplied server (a personal MCP server). It is
	// serialized so no copy of the config can drop it: the server must be
	// remote (http/sse, https URL), and every request -- discovery, the MCP
	// transport, OAuth -- goes through a public-only client (netguard), so the
	// server cannot make the platform reach its own network.
	PublicOnly bool `json:"public_only,omitempty"`
	// ToolAlias is the stable name agents see for this server, from ToolAlias().
	// A host sets it when the server name carries a credential scope or
	// connection ID: the name then stays internal, and the server's tools are
	// treated as "<namespace>__<tool>", shown as "<alias>__<tool>".
	ToolAlias string `json:"tool_alias,omitempty"`
}

// RuntimeConfigOverride allows runtime modification of MCP server configuration
// This is useful for passing workflow-specific settings like output directories
type RuntimeConfigOverride struct {
	// ArgsReplace replaces specific arg values by flag name
	// e.g., {"--output-dir": "/new/path"} will find "--output-dir" and replace the next arg
	ArgsReplace map[string]string `json:"args_replace,omitempty"`
	// ArgsAppend appends additional args to the command
	ArgsAppend []string `json:"args_append,omitempty"`
	// EnvOverride adds or overrides environment variables
	EnvOverride map[string]string `json:"env_override,omitempty"`
	// WorkingDir sets the subprocess working directory (stdio only). Used so tools that resolve
	// relative paths use the desired output directory.
	WorkingDir string `json:"working_dir,omitempty"`
	// Server is a complete configuration for a server that is not in the
	// catalog (a person's own server). When set, it is used as-is instead of
	// a catalog lookup, and the fields above are ignored.
	Server *MCPServerConfig `json:"server,omitempty"`
}

// ExtraServerConfig returns the complete configuration an override carries
// for a server outside the catalog.
func (o RuntimeOverrides) ExtraServerConfig(name string) (MCPServerConfig, bool) {
	if override, ok := o[name]; ok && override.Server != nil {
		return *override.Server, true
	}
	return MCPServerConfig{}, false
}

// RuntimeOverrides maps server names to their runtime configuration overrides
type RuntimeOverrides map[string]RuntimeConfigOverride

// ApplyOverride applies a RuntimeConfigOverride to the MCPServerConfig
// Returns a new MCPServerConfig with the overrides applied (does not modify original)
func (c MCPServerConfig) ApplyOverride(override RuntimeConfigOverride) MCPServerConfig {
	// Create a copy of the config
	newConfig := c
	newConfig.Args = make([]string, len(c.Args))
	copy(newConfig.Args, c.Args)

	if c.Env != nil {
		newConfig.Env = make(map[string]string, len(c.Env))
		for k, v := range c.Env {
			newConfig.Env[k] = v
		}
	}

	// Apply ArgsReplace - find flag and replace its value
	for flag, newValue := range override.ArgsReplace {
		for i := 0; i < len(newConfig.Args); i++ {
			if newConfig.Args[i] == flag && i+1 < len(newConfig.Args) {
				// Replace the value after the flag
				newConfig.Args[i+1] = newValue
				break
			}
			// Also handle --flag=value format
			if strings.HasPrefix(newConfig.Args[i], flag+"=") {
				newConfig.Args[i] = flag + "=" + newValue
				break
			}
		}
	}

	// Apply ArgsAppend
	if len(override.ArgsAppend) > 0 {
		newConfig.Args = append(newConfig.Args, override.ArgsAppend...)
	}

	// Apply EnvOverride
	if len(override.EnvOverride) > 0 {
		if newConfig.Env == nil {
			newConfig.Env = make(map[string]string)
		}
		for k, v := range override.EnvOverride {
			newConfig.Env[k] = v
		}
	}

	if override.WorkingDir != "" {
		newConfig.WorkingDir = override.WorkingDir
	}

	return newConfig
}

// GetPoolConfig returns the pool configuration, using defaults if not specified
func (c *MCPServerConfig) GetPoolConfig() PoolConfig {
	if c.PoolConfig != nil {
		return *c.PoolConfig
	}
	return DefaultPoolConfig()
}

// GetProtocol returns the protocol type with smart detection
func (c *MCPServerConfig) GetProtocol() ProtocolType {
	// If protocol is explicitly set, use it
	if c.Protocol != "" {
		return c.Protocol
	}

	// Smart detection based on URL
	if c.URL != "" {
		// If URL contains /sse, assume SSE protocol
		if contains(c.URL, "/sse") {
			return ProtocolSSE
		}
		// If URL starts with http:// or https://, assume HTTP
		if contains(c.URL, "http://") || contains(c.URL, "https://") {
			return ProtocolHTTP
		}
	}

	// Default to stdio
	return ProtocolStdio
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && strings.Contains(s, substr)
}

type MCPConfig struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// LoadConfig loads MCP server configuration from the specified file
// logger is optional - if provided, debug information will be logged at debug level
// If configPath is empty, returns an empty config (useful for pure LLM mode without MCP servers)
func LoadConfig(configPath string, logger loggerv2.Logger) (*MCPConfig, error) {
	// If config path is empty, return an empty config for pure LLM mode
	if configPath == "" {
		if logger != nil {
			logger.Debug("Config path is empty, returning empty config for pure LLM mode")
		}
		return &MCPConfig{MCPServers: make(map[string]MCPServerConfig)}, nil
	}

	if logger != nil {
		logger.Debug("About to read config file", loggerv2.String("config_path", configPath))
	}
	startTime := time.Now()

	//nolint:gosec // G304: configPath comes from command-line/config, not user input
	data, err := os.ReadFile(configPath)

	readDuration := time.Since(startTime)
	if err != nil {
		if logger != nil {
			logger.Debug("os.ReadFile failed",
				loggerv2.String("config_path", configPath),
				loggerv2.Any("duration", readDuration),
				loggerv2.Error(err))
		}
		return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}
	if logger != nil {
		logger.Debug("os.ReadFile completed",
			loggerv2.String("config_path", configPath),
			loggerv2.Any("duration", readDuration),
			loggerv2.Int("bytes_read", len(data)))
	}

	if logger != nil {
		logger.Debug("About to unmarshal JSON", loggerv2.String("config_path", configPath))
	}
	unmarshalStartTime := time.Now()

	var config MCPConfig
	if err := json.Unmarshal(data, &config); err != nil {
		unmarshalDuration := time.Since(unmarshalStartTime)
		if logger != nil {
			logger.Debug("json.Unmarshal failed",
				loggerv2.String("config_path", configPath),
				loggerv2.Any("duration", unmarshalDuration),
				loggerv2.Error(err))
		}
		return nil, fmt.Errorf("failed to parse config file %s: %w", configPath, err)
	}

	unmarshalDuration := time.Since(unmarshalStartTime)
	if logger != nil {
		logger.Debug("json.Unmarshal completed",
			loggerv2.String("config_path", configPath),
			loggerv2.Any("duration", unmarshalDuration))
	}

	return &config, nil
}

// LoadMergedConfig loads the merged configuration (base + user additions)
// This mirrors the logic from mcp_config_routes.go to ensure consistency
type configFileStamp struct {
	Size     int64
	Modified time.Time
	Exists   bool
}
type mergedConfigEntry struct {
	Base, User configFileStamp
	Config     *MCPConfig
}

var mergedConfigs = struct {
	sync.Mutex
	entries map[string]mergedConfigEntry
}{entries: make(map[string]mergedConfigEntry)}

func configStamp(path string) (configFileStamp, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return configFileStamp{}, nil
	}
	if err != nil {
		return configFileStamp{}, err
	}
	return configFileStamp{info.Size(), info.ModTime(), true}, nil
}

// A caller may add runtime servers or replace credentials. Never share mutable
// maps, slices or OAuth pointers with another caller or with the cached catalog.
func cloneMCPConfig(config *MCPConfig) *MCPConfig {
	out := &MCPConfig{MCPServers: make(map[string]MCPServerConfig, len(config.MCPServers))}
	for name, cfg := range config.MCPServers {
		cfg.Args = slices.Clone(cfg.Args)
		cfg.Env = maps.Clone(cfg.Env)
		cfg.Headers = maps.Clone(cfg.Headers)
		if cfg.PoolConfig != nil {
			pool := *cfg.PoolConfig
			cfg.PoolConfig = &pool
		}
		if cfg.OAuth != nil {
			auth := *cfg.OAuth
			auth.Scopes = slices.Clone(auth.Scopes)
			auth.ExtraAuthParams = maps.Clone(auth.ExtraAuthParams)
			cfg.OAuth = &auth
		}
		out.MCPServers[name] = cfg
	}
	return out
}

// LoadMergedConfig decodes unchanged base/user files once. Each caller receives
// its own mutable catalog so per-person OAuth and runtime overrides stay private.
func LoadMergedConfig(configPath string, logger loggerv2.Logger) (*MCPConfig, error) {
	if configPath == "" {
		return loadMergedConfigUncached(configPath, logger)
	}
	absolute, err := filepath.Abs(configPath)
	if err != nil {
		return nil, err
	}
	mergedConfigs.Lock()
	defer mergedConfigs.Unlock()
	base, err := configStamp(absolute)
	if err != nil {
		return nil, err
	}
	user, err := configStamp(strings.Replace(absolute, ".json", "_user.json", 1))
	if err != nil {
		return nil, err
	}
	if cached, ok := mergedConfigs.entries[absolute]; ok && cached.Base == base && cached.User == user {
		return cloneMCPConfig(cached.Config), nil
	}
	config, err := loadMergedConfigUncached(absolute, logger)
	if err != nil {
		delete(mergedConfigs.entries, absolute)
		return nil, err
	}
	mergedConfigs.entries[absolute] = mergedConfigEntry{base, user, config}
	return cloneMCPConfig(config), nil
}

func loadMergedConfigUncached(configPath string, logger loggerv2.Logger) (*MCPConfig, error) {
	userConfigPath := strings.Replace(configPath, ".json", "_user.json", 1)
	if logger != nil {
		logger.Debug("Starting LoadMergedConfig",
			loggerv2.String("base_config_path", configPath),
			loggerv2.String("user_config_path", userConfigPath))
	}
	startTime := time.Now()

	// Load base config
	if logger != nil {
		logger.Debug("About to load base config", loggerv2.String("config_path", configPath))
	}
	baseConfigStartTime := time.Now()
	baseConfig, err := LoadConfig(configPath, logger)
	baseConfigDuration := time.Since(baseConfigStartTime)
	if err != nil {
		if logger != nil {
			logger.Debug("Failed to load base config",
				loggerv2.String("config_path", configPath),
				loggerv2.Any("duration", baseConfigDuration),
				loggerv2.Error(err))
		}
		return nil, fmt.Errorf("failed to load base config: %w", err)
	}
	if logger != nil {
		logger.Debug("Base config loaded successfully",
			loggerv2.String("config_path", configPath),
			loggerv2.Any("duration", baseConfigDuration),
			loggerv2.Int("server_count", len(baseConfig.MCPServers)))
	}

	// Load user additions (if any)
	if logger != nil {
		logger.Debug("About to load user config", loggerv2.String("config_path", userConfigPath))
	}
	userConfigStartTime := time.Now()
	userConfig, err := LoadConfig(userConfigPath, logger)
	userConfigDuration := time.Since(userConfigStartTime)
	if err != nil {
		if logger != nil {
			logger.Debug("User config load failed (this is OK if file doesn't exist)",
				loggerv2.String("config_path", userConfigPath),
				loggerv2.Any("duration", userConfigDuration),
				loggerv2.Error(err))
		}
		// User config doesn't exist yet, use empty config
		userConfig = &MCPConfig{MCPServers: make(map[string]MCPServerConfig)}
	} else {
		if logger != nil {
			logger.Debug("User config loaded successfully",
				loggerv2.String("config_path", userConfigPath),
				loggerv2.Any("duration", userConfigDuration),
				loggerv2.Int("server_count", len(userConfig.MCPServers)))
		}
	}

	// Merge base config with user additions
	if logger != nil {
		logger.Debug("Starting merge operation")
	}
	mergeStartTime := time.Now()
	mergedConfig := MergeMCPConfigs(baseConfig, userConfig)
	mergeDuration := time.Since(mergeStartTime)
	if logger != nil {
		logger.Debug("Merge operation completed",
			loggerv2.Any("duration", mergeDuration))
	}

	if logger != nil {
		logger.Info("Merged config",
			loggerv2.Int("base_servers", len(baseConfig.MCPServers)),
			loggerv2.Int("user_servers", len(userConfig.MCPServers)),
			loggerv2.Int("total_servers", len(mergedConfig.MCPServers)))
	}

	totalDuration := time.Since(startTime)
	if logger != nil {
		logger.Debug("LoadMergedConfig completed successfully",
			loggerv2.Any("duration", totalDuration))
	}
	return mergedConfig, nil
}

// ResolveServer returns both the canonical configured name and configuration
// for a server. After an exact match miss the lookup retries with
// hyphen↔underscore swaps so callers that received a sanitized name (e.g.
// "google_sheets" via the bridge URL) still find the canonical config entry
// (e.g. "google-sheets"). Returning the configured name lets connection
// callers deduplicate aliases before starting subprocesses.
func (c *MCPConfig) ResolveServer(name string) (string, MCPServerConfig, error) {
	if server, exists := c.MCPServers[name]; exists {
		return name, server, nil
	}
	if strings.Contains(name, "_") {
		canonicalName := strings.ReplaceAll(name, "_", "-")
		if server, ok := c.MCPServers[canonicalName]; ok {
			return canonicalName, server, nil
		}
	}
	if strings.Contains(name, "-") {
		canonicalName := strings.ReplaceAll(name, "-", "_")
		if server, ok := c.MCPServers[canonicalName]; ok {
			return canonicalName, server, nil
		}
	}
	return "", MCPServerConfig{}, fmt.Errorf("server '%s' not found in configuration", name)
}

// GetServer returns the configuration for a specific server while preserving
// the legacy configuration-only API.
func (c *MCPConfig) GetServer(name string) (MCPServerConfig, error) {
	_, server, err := c.ResolveServer(name)
	return server, err
}

// ListServers returns all configured server names
func (c *MCPConfig) ListServers() []string {
	var names []string
	for name := range c.MCPServers {
		names = append(names, name)
	}
	return names
}

// SaveConfig writes the MCPConfig to the specified file atomically
func SaveConfig(configPath string, config *MCPConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := configPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil { //nolint:gosec // 0644 permissions are intentional for user-accessible config files
		return err
	}
	return os.Rename(tmpPath, configPath)
}

// AddServer adds a new server to the config and saves it
func (c *MCPConfig) AddServer(name string, server MCPServerConfig, configPath string) error {
	c.MCPServers[name] = server
	return SaveConfig(configPath, c)
}

// EditServer edits an existing server in the config and saves it
func (c *MCPConfig) EditServer(name string, server MCPServerConfig, configPath string) error {
	c.MCPServers[name] = server
	return SaveConfig(configPath, c)
}

// RemoveServer removes a server from the config and saves it
func (c *MCPConfig) RemoveServer(name string, configPath string) error {
	delete(c.MCPServers, name)
	return SaveConfig(configPath, c)
}

// ReloadConfig reloads the config from disk
// logger is optional - if provided, debug information will be logged at debug level
func (c *MCPConfig) ReloadConfig(configPath string, logger loggerv2.Logger) error {
	newConfig, err := LoadConfig(configPath, logger)
	if err != nil {
		return err
	}
	c.MCPServers = newConfig.MCPServers
	return nil
}

// MergeMCPConfigs preserves separately authenticated connections instead of
// silently hiding a base account behind an overlay account. Existing names
// retain overlay precedence so persisted selections keep their meaning.
func MergeMCPConfigs(base, overlay *MCPConfig) *MCPConfig {
	result := &MCPConfig{MCPServers: make(map[string]MCPServerConfig)}
	if base != nil {
		for name, cfg := range base.MCPServers {
			result.MCPServers[name] = cfg
		}
	}
	if overlay == nil {
		return result
	}
	for name, cfg := range overlay.MCPServers {
		original, exists := result.MCPServers[name]
		if exists && original.OAuth != nil && cfg.OAuth != nil &&
			strings.TrimSpace(original.OAuth.TokenFile) != "" && strings.TrimSpace(cfg.OAuth.TokenFile) != "" &&
			original.OAuth.TokenFile != cfg.OAuth.TokenFile {
			alias := name + "-base"
			for index := 2; ; index++ {
				_, inBase := result.MCPServers[alias]
				_, inOverlay := overlay.MCPServers[alias]
				if !inBase && !inOverlay {
					break
				}
				alias = fmt.Sprintf("%s-base-%d", name, index)
			}
			original.Description += " Separate base connection for " + name + "; account/workspace identity must be verified using this connection's read-only tools."
			result.MCPServers[alias] = original
		}
		result.MCPServers[name] = cfg
	}
	return result
}
