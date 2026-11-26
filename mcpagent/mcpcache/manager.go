package mcpcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// CacheEntry represents a cached MCP server connection and its metadata
type CacheEntry struct {
	// Server identification
	ServerName string `json:"server_name"`

	// Connection data
	Tools        []llmtypes.Tool `json:"tools"`
	SystemPrompt string          `json:"system_prompt"`

	// Metadata
	CreatedAt  time.Time              `json:"created_at"`
	TTLMinutes int                    `json:"ttl_minutes"`
	Protocol   string                 `json:"protocol"`
	ServerInfo map[string]interface{} `json:"server_info,omitempty"`

	// Cache management
	IsValid      bool   `json:"is_valid"`
	ErrorMessage string `json:"error_message,omitempty"`

	// Tool ownership tracking (for duplicate detection)
	// Maps tool name -> ownership status ("primary" or "duplicate")
	// When multiple servers provide the same tool, only the "primary" server's entry
	// should expose that tool. This prevents duplicate tool names in the agent's tool list.
	ToolOwnership map[string]string `json:"tool_ownership,omitempty"`
}

// IsExpired checks if the cache entry has expired
func (ce *CacheEntry) IsExpired() bool {
	if !ce.IsValid {
		return true
	}
	expirationTime := ce.CreatedAt.Add(time.Duration(ce.TTLMinutes) * time.Minute)
	return time.Now().After(expirationTime)
}

// CacheManager manages MCP server connection caching
type CacheManager struct {
	cacheDir   string
	ttlMinutes int
	logger     loggerv2.Logger
	cache      sync.Map // cache key (string) -> entry (*CacheEntry) - thread-safe map
}

// Singleton instance
var (
	instance *CacheManager
	once     sync.Once
)

// GetCacheManager returns the singleton cache manager instance
func GetCacheManager(logger loggerv2.Logger) *CacheManager {
	once.Do(func() {
		// Use environment variable if set, otherwise default to cache/
		cacheDir := os.Getenv("MCP_CACHE_DIR")
		if cacheDir == "" {
			// Default to cache/ directory (works for both local and Docker)
			cacheDir = "/app/cache" // Docker mount point
			if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
				// For local development, use relative path to cache/
				cacheDir = filepath.Join(".", "cache")
			}
		}
		// Get TTL from environment variable, default to 7 days (10080 minutes)
		ttlMinutes := 10080 // 7 days default
		if ttlEnv := os.Getenv("MCP_CACHE_TTL_MINUTES"); ttlEnv != "" {
			if parsedTTL, err := strconv.Atoi(ttlEnv); err == nil && parsedTTL > 0 {
				ttlMinutes = parsedTTL
			} else if logger != nil {
				logger.Warn("Invalid MCP_CACHE_TTL_MINUTES value, using default",
					loggerv2.String("value", ttlEnv),
					loggerv2.Int("default_minutes", ttlMinutes))
			}
		}

		instance = &CacheManager{
			cacheDir:   cacheDir,
			ttlMinutes: ttlMinutes, // Configurable TTL via environment variable
			logger:     logger,
			// cache is sync.Map, zero value is ready to use
		}

		// NOTE: Cache directory is created lazily when actually saving entries
		// This prevents unnecessary directory creation when cache is disabled
		// The directory will be created in saveToFile() when needed

		// Load existing cache entries (this will create directory if cache files exist)
		instance.loadExistingCache()
	})
	return instance
}

// GenerateServerConfigHash creates a hash of the server configuration
// This includes command, args, env vars, URL, headers, and protocol
func GenerateServerConfigHash(config mcpclient.MCPServerConfig) string {
	// Create a deterministic representation of the config
	configData := struct {
		Command        string            `json:"command"`
		Args           []string          `json:"args"`
		Env            map[string]string `json:"env"`
		URL            string            `json:"url"`
		Headers        map[string]string `json:"headers"`
		Protocol       string            `json:"protocol"`
		OAuthTokenFile string            `json:"oauth_token_file,omitempty"`
	}{
		Command:  config.Command,
		Args:     config.Args,
		Env:      config.Env,
		URL:      config.URL,
		Headers:  config.Headers,
		Protocol: string(config.Protocol),
	}
	// Tool visibility may differ by OAuth account. Never share metadata between
	// users merely because they connect to the same server URL.
	if config.OAuth != nil {
		configData.OAuthTokenFile = config.OAuth.TokenFile
	}

	// Sort maps for deterministic output
	if configData.Env != nil {
		sortedEnv := make(map[string]string)
		var envKeys []string
		for k := range configData.Env {
			envKeys = append(envKeys, k)
		}
		sort.Strings(envKeys)
		for _, k := range envKeys {
			sortedEnv[k] = configData.Env[k]
		}
		configData.Env = sortedEnv
	}

	if configData.Headers != nil {
		sortedHeaders := make(map[string]string)
		var headerKeys []string
		for k := range configData.Headers {
			headerKeys = append(headerKeys, k)
		}
		sort.Strings(headerKeys)
		for _, k := range headerKeys {
			sortedHeaders[k] = configData.Headers[k]
		}
		configData.Headers = sortedHeaders
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(configData)
	if err != nil {
		// Fallback to simple string representation
		return fmt.Sprintf("config_%s", config.Command)
	}

	// Generate SHA256 hash
	hash := sha256.Sum256(jsonData)
	return hex.EncodeToString(hash[:]) // Use full hash to prevent collisions
}

// GenerateUnifiedCacheKey creates a cache key using server name and configuration hash
// This ensures cache is invalidated when server configuration changes
func GenerateUnifiedCacheKey(serverName string, config mcpclient.MCPServerConfig) string {
	// Clean server name
	cleanServerName := strings.TrimSpace(serverName)

	// Generate configuration hash
	configHash := GenerateServerConfigHash(config)

	// Combine server name and config hash
	return fmt.Sprintf("unified_%s_%s", cleanServerName, configHash)
}

// Get retrieves a cache entry if it exists and is valid
func (cm *CacheManager) Get(cacheKey string) (*CacheEntry, bool) {
	value, exists := cm.cache.Load(cacheKey)
	if !exists {
		return nil, false
	}

	entry, ok := value.(*CacheEntry)
	if !ok {
		cm.logger.Warn("Invalid cache entry type", loggerv2.String("key", cacheKey))
		return nil, false
	}

	// Check if entry is expired
	if entry.IsExpired() {
		cm.logger.Debug("Cache entry expired", loggerv2.String("key", cacheKey))
		return nil, false
	}

	cm.logger.Debug("Cache hit", loggerv2.String("key", cacheKey))
	return entry, true
}

// Put stores a cache entry using configuration-aware cache key
func (cm *CacheManager) Put(entry *CacheEntry, config mcpclient.MCPServerConfig) error {
	cm.logger.Debug("Put: Storing cache entry", loggerv2.String("server", entry.ServerName))

	// Use configuration-aware cache key
	cacheKey := GenerateUnifiedCacheKey(entry.ServerName, config)

	// Store in memory cache (sync.Map is thread-safe, no lock needed)
	cm.cache.Store(cacheKey, entry)
	cm.logger.Debug("Put: Stored in memory cache", loggerv2.String("server", entry.ServerName))

	// Persist to file (sync.Map operations are lock-free)
	cm.logger.Debug("Put: Calling saveToFile", loggerv2.String("server", entry.ServerName))
	err := cm.saveToFile(entry, config)
	cm.logger.Debug("Put: saveToFile returned", loggerv2.String("server", entry.ServerName), loggerv2.Error(err))
	return err
}

// Invalidate removes a cache entry
// FIXED: Releases mutex before blocking I/O operations to prevent deadlocks
func (cm *CacheManager) Invalidate(cacheKey string) error {
	cm.logger.Debug("🔧 [INVALIDATE] Starting invalidation for cache key", loggerv2.String("key", cacheKey))

	// Step 1: Get server name and prepare paths (sync.Map is thread-safe, no lock needed)
	cm.logger.Debug("🔧 [INVALIDATE] Collecting entry data", loggerv2.String("key", cacheKey))

	var serverName string
	var exists bool
	if value, found := cm.cache.Load(cacheKey); found {
		if entry, ok := value.(*CacheEntry); ok {
			serverName = entry.ServerName
			exists = true
		}
	}

	// Remove from in-memory cache map (sync.Map is thread-safe)
	if exists {
		cm.cache.Delete(cacheKey)
	}

	// Pre-compute the cache path.
	cacheFile := cm.getCacheFilePath(cacheKey)

	cm.logger.Debug("🔧 [INVALIDATE] Entry data collected, proceeding with I/O operations",
		loggerv2.String("key", cacheKey),
		loggerv2.Any("entry_exists", exists))

	// Step 2: Perform all blocking I/O operations OUTSIDE the lock
	if !exists {
		cm.logger.Debug("🔧 [INVALIDATE] Cache entry not found, nothing to invalidate", loggerv2.String("key", cacheKey))
		return nil
	}

	// Remove from filesystem
	cm.logger.Debug("🔧 [INVALIDATE] Removing cache file (outside lock)",
		loggerv2.String("key", cacheKey),
		loggerv2.String("file", cacheFile))
	if err := os.Remove(cacheFile); err != nil && !os.IsNotExist(err) {
		cm.logger.Warn("🔧 [INVALIDATE] Failed to remove cache file",
			loggerv2.Error(err),
			loggerv2.String("file", cacheFile),
			loggerv2.String("key", cacheKey))
		return fmt.Errorf("failed to remove cache file %s: %w", cacheFile, err)
	} else {
		cm.logger.Debug("🔧 [INVALIDATE] Successfully removed cache file",
			loggerv2.String("file", cacheFile),
			loggerv2.String("key", cacheKey))
	}

	cm.logger.Info("✅ [INVALIDATE] Successfully invalidated cache entry",
		loggerv2.String("key", cacheKey),
		loggerv2.String("server", serverName))
	return nil
}

// InvalidateByServer invalidates all cache entries for a specific server
// FIXED: Releases mutex before blocking I/O operations to prevent deadlocks
// This is a backward-compatible wrapper that uses context.Background()
func (cm *CacheManager) InvalidateByServer(configPath, serverName string) error {
	return cm.InvalidateByServerWithContext(context.Background(), configPath, serverName)
}

// InvalidateByServerWithContext invalidates all cache entries for a specific server with context support
// FIXED: Releases mutex before blocking I/O operations to prevent deadlocks
// Checks context cancellation before and during I/O operations to allow timeout/cancellation
func (cm *CacheManager) InvalidateByServerWithContext(ctx context.Context, configPath, serverName string) error {
	cm.logger.Info("🔧 [INVALIDATE] Starting invalidation for server",
		loggerv2.String("server", serverName))

	// Check context before starting
	select {
	case <-ctx.Done():
		cm.logger.Warn("🔧 [INVALIDATE] Context cancelled before starting invalidation",
			loggerv2.String("server", serverName),
			loggerv2.Error(ctx.Err()))
		return ctx.Err()
	default:
	}

	// Step 1: Collect keys and cache file paths (sync.Map is thread-safe, no lock needed)
	cm.logger.Debug("🔧 [INVALIDATE] Collecting keys", loggerv2.String("server", serverName))

	var keysToRemove []string
	var cacheFilesToRemove []string

	// Find all keys for this server using Range
	cm.cache.Range(func(key, value interface{}) bool {
		keyStr, ok := key.(string)
		if !ok {
			return true // Continue iteration
		}
		entry, ok := value.(*CacheEntry)
		if !ok {
			return true // Continue iteration
		}
		if entry.ServerName == serverName {
			keysToRemove = append(keysToRemove, keyStr)
			// Pre-compute cache file paths
			cacheFile := cm.getCacheFilePath(keyStr)
			cacheFilesToRemove = append(cacheFilesToRemove, cacheFile)
		}
		return true // Continue iteration
	})

	// Remove entries from in-memory cache map (sync.Map is thread-safe)
	for _, key := range keysToRemove {
		cm.cache.Delete(key)
	}

	cm.logger.Debug("🔧 [INVALIDATE] Keys collected, proceeding with I/O operations",
		loggerv2.String("server", serverName),
		loggerv2.Int("keys_count", len(keysToRemove)))

	// Check context after releasing lock
	select {
	case <-ctx.Done():
		cm.logger.Warn("🔧 [INVALIDATE] Context cancelled after lock release",
			loggerv2.String("server", serverName),
			loggerv2.Error(ctx.Err()))
		return ctx.Err()
	default:
	}

	// Step 2: Perform all blocking I/O operations OUTSIDE the lock
	if len(keysToRemove) == 0 {
		cm.logger.Info("🔧 [INVALIDATE] No cache entries found for server",
			loggerv2.String("server", serverName))
		return nil
	}

	cm.logger.Info("🔧 [INVALIDATE] Removing cache files (outside lock)",
		loggerv2.String("server", serverName),
		loggerv2.Int("file_count", len(cacheFilesToRemove)))

	// Remove cache files from filesystem
	for i, key := range keysToRemove {
		// Check context before each file operation
		select {
		case <-ctx.Done():
			cm.logger.Warn("🔧 [INVALIDATE] Context cancelled during cache file removal",
				loggerv2.String("server", serverName),
				loggerv2.Error(ctx.Err()))
			return ctx.Err()
		default:
		}

		cacheFile := cacheFilesToRemove[i]
		cm.logger.Debug("🔧 [INVALIDATE] Removing cache file",
			loggerv2.String("server", serverName),
			loggerv2.String("key", key),
			loggerv2.String("file", cacheFile))
		if err := os.Remove(cacheFile); err != nil && !os.IsNotExist(err) {
			cm.logger.Warn("🔧 [INVALIDATE] Failed to remove cache file",
				loggerv2.Error(err),
				loggerv2.String("file", cacheFile),
				loggerv2.String("server", serverName))
		} else {
			cm.logger.Debug("🔧 [INVALIDATE] Successfully removed cache file",
				loggerv2.String("file", cacheFile),
				loggerv2.String("server", serverName))
		}
	}

	cm.logger.Info("Successfully invalidated cache entries for server",
		loggerv2.Int("count", len(keysToRemove)),
		loggerv2.String("server", serverName))
	return nil
}

// GetAllEntries returns all cached entries (for debugging and registry integration)
// Returns deep copies to prevent race conditions from external mutations
func (cm *CacheManager) GetAllEntries() map[string]*CacheEntry {
	// Return deep copies of cache entries to prevent external mutations
	result := make(map[string]*CacheEntry)
	cm.cache.Range(func(key, value interface{}) bool {
		keyStr, ok := key.(string)
		if !ok {
			return true // Continue iteration
		}
		entry, ok := value.(*CacheEntry)
		if !ok {
			return true // Continue iteration
		}

		// Create a deep copy of the entry
		entryCopy := *entry // Copy struct fields

		// Deep copy Tools slice
		if entry.Tools != nil {
			entryCopy.Tools = make([]llmtypes.Tool, len(entry.Tools))
			copy(entryCopy.Tools, entry.Tools)
		}

		// Deep copy ServerInfo map if it exists
		if entry.ServerInfo != nil {
			entryCopy.ServerInfo = make(map[string]interface{})
			for k, v := range entry.ServerInfo {
				entryCopy.ServerInfo[k] = v
			}
		}

		// Deep copy ToolOwnership map if it exists
		if entry.ToolOwnership != nil {
			entryCopy.ToolOwnership = make(map[string]string)
			for k, v := range entry.ToolOwnership {
				entryCopy.ToolOwnership[k] = v
			}
		}

		result[keyStr] = &entryCopy
		return true // Continue iteration
	})
	return result
}

// Clear removes all cache entries
func (cm *CacheManager) Clear() error {
	// Clear memory cache by deleting all entries
	cm.cache.Range(func(key, value interface{}) bool {
		cm.cache.Delete(key)
		return true // Continue iteration
	})

	// Remove all cache files
	return cm.clearCacheDirectory()
}

// GetStats returns cache statistics
func (cm *CacheManager) GetStats() map[string]interface{} {
	totalEntries := 0
	validEntries := 0
	expiredEntries := 0
	totalSize := int64(0)

	// Count entries using Range (sync.Map doesn't have len())
	cm.cache.Range(func(key, value interface{}) bool {
		entry, ok := value.(*CacheEntry)
		if !ok {
			return true // Continue iteration
		}

		totalEntries++
		if entry.IsValid && !entry.IsExpired() {
			validEntries++
		} else {
			expiredEntries++
		}

		// Estimate size (rough calculation)
		entrySize := len(entry.ServerName) + len(entry.SystemPrompt)
		for _, tool := range entry.Tools {
			entrySize += len(tool.Function.Name) + len(tool.Function.Description)
		}
		totalSize += int64(entrySize)

		return true // Continue iteration
	})

	return map[string]interface{}{
		"total_entries":   totalEntries,
		"valid_entries":   validEntries,
		"expired_entries": expiredEntries,
		"estimated_size":  totalSize,
		"cache_directory": cm.cacheDir,
		"ttl_minutes":     cm.ttlMinutes,
	}
}

// Cleanup removes expired entries from both memory and filesystem
func (cm *CacheManager) Cleanup() error {
	var expiredKeys []string

	// Find expired entries using Range
	cm.cache.Range(func(key, value interface{}) bool {
		keyStr, ok := key.(string)
		if !ok {
			return true // Continue iteration
		}
		entry, ok := value.(*CacheEntry)
		if !ok {
			return true // Continue iteration
		}
		if entry.IsExpired() {
			expiredKeys = append(expiredKeys, keyStr)
		}
		return true // Continue iteration
	})

	// Remove expired entries (sync.Map is thread-safe)
	for _, key := range expiredKeys {
		cm.cache.Delete(key)

		// Remove from filesystem
		cacheFile := cm.getCacheFilePath(key)
		if err := os.Remove(cacheFile); err != nil && !os.IsNotExist(err) {
			cm.logger.Warn("Failed to remove expired cache file",
				loggerv2.Error(err),
				loggerv2.String("file", cacheFile))
		}
	}

	if len(expiredKeys) > 0 {
		cm.logger.Info("Cleaned up expired cache entries", loggerv2.Int("count", len(expiredKeys)))
	}

	return nil
}

// loadExistingCache loads cache entries from the filesystem
func (cm *CacheManager) loadExistingCache() {
	// Log the cache directory being used for debugging
	if cm.logger != nil {
		cm.logger.Info("🔍 Loading existing cache from directory", loggerv2.String("cache_dir", cm.cacheDir))
	}

	// Try to read cache directory - if it doesn't exist, that's fine (lazy creation)
	// Only create directory if cache files actually exist
	files, err := os.ReadDir(cm.cacheDir)
	if err != nil {
		// Directory doesn't exist yet - that's fine, it will be created when saving entries
		if cm.logger != nil {
			cm.logger.Info("📁 Cache directory does not exist yet (will be created lazily)", loggerv2.String("cache_dir", cm.cacheDir))
		}
		return
	}

	// Log number of files found
	if cm.logger != nil {
		cm.logger.Info("📁 Found files in cache directory", loggerv2.Int("file_count", len(files)), loggerv2.String("cache_dir", cm.cacheDir))
	}

	loadedCount := 0

	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".json") {
			cacheFile := filepath.Join(cm.cacheDir, file.Name())
			if entry := cm.loadFromFile(cacheFile); entry != nil {
				// Use filename as cache key (config-aware format)
				fileName := strings.TrimSuffix(file.Name(), ".json")
				cm.cache.Store(fileName, entry)
				loadedCount++
				if cm.logger != nil {
					cm.logger.Info("📦 Loaded cache entry from disk",
						loggerv2.String("server", entry.ServerName),
						loggerv2.String("cache_key", fileName),
						loggerv2.Int("tools", len(entry.Tools)),
						loggerv2.Int("ttl_minutes", entry.TTLMinutes))
				}
			}
		}
	}

	if loadedCount > 0 && cm.logger != nil {
		cm.logger.Info("Loaded cache entries from filesystem", loggerv2.Int("count", loadedCount))
	}
}

// saveToFile persists a cache entry to the filesystem using configuration-aware naming.
func (cm *CacheManager) saveToFile(entry *CacheEntry, config mcpclient.MCPServerConfig) error {
	// Use configuration-aware cache key for file naming
	cacheFile := cm.getCacheFilePath(GenerateUnifiedCacheKey(entry.ServerName, config))

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(cacheFile), 0755); err != nil { //nolint:gosec // 0755 permissions are intentional for cache directories
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Marshal to JSON
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cache entry: %w", err)
	}

	// Write to file
	cm.logger.Debug("About to write cache file", loggerv2.String("file", cacheFile), loggerv2.String("server", entry.ServerName), loggerv2.Int("data_size", len(data)))
	if err := os.WriteFile(cacheFile, data, 0644); err != nil { //nolint:gosec // 0644 permissions are intentional for cache files
		cm.logger.Error("Failed to write cache file", err, loggerv2.String("file", cacheFile), loggerv2.String("server", entry.ServerName))
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	// Verify file was written
	if stat, err := os.Stat(cacheFile); err != nil {
		cm.logger.Error("Cache file not found after write", err, loggerv2.String("file", cacheFile), loggerv2.String("server", entry.ServerName))
		return fmt.Errorf("cache file not found after write: %w", err)
	} else {
		cm.logger.Info("Saved cache entry to file",
			loggerv2.String("file", cacheFile),
			loggerv2.String("server", entry.ServerName),
			loggerv2.Int("file_size", int(stat.Size())))
	}

	cm.logger.Debug("saveToFile completed", loggerv2.String("server", entry.ServerName))
	return nil
}

// loadFromFile loads a cache entry from the filesystem
func (cm *CacheManager) loadFromFile(cacheFile string) *CacheEntry {
	//nolint:gosec // G304: cacheFile path is generated internally from validated inputs
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		if cm.logger != nil {
			cm.logger.Debug("Failed to read cache file",
				loggerv2.Error(err),
				loggerv2.String("file", cacheFile))
		}
		return nil
	}

	var entry CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		if cm.logger != nil {
			cm.logger.Warn("Failed to unmarshal cache file",
				loggerv2.Error(err),
				loggerv2.String("file", cacheFile))
		}
		return nil
	}

	// Check if entry is still valid
	if entry.IsExpired() {
		cm.logger.Debug("Loaded expired cache entry", loggerv2.String("file", cacheFile))
		// Don't return expired entries - attempt to clean up expired file
		if err := os.Remove(cacheFile); err != nil && !os.IsNotExist(err) {
			// Log warning but don't fail - expired entry won't be used anyway
			cm.logger.Warn("Failed to remove expired cache file",
				loggerv2.Error(err),
				loggerv2.String("file", cacheFile))
		}
		return nil
	}

	return &entry
}

// ReloadFromDisk reloads a specific cache entry from disk and updates the in-memory cache
func (cm *CacheManager) ReloadFromDisk(cacheKey string) *CacheEntry {
	cacheFile := cm.getCacheFilePath(cacheKey)

	// Check if file exists (outside lock)
	if _, err := os.Stat(cacheFile); os.IsNotExist(err) {
		if cm.logger != nil {
			cm.logger.Debug("Cache file does not exist", loggerv2.String("file", cacheFile))
		}
		return nil
	}

	// Load the entry from disk (outside lock - this is the expensive I/O operation)
	entry := cm.loadFromFile(cacheFile)
	if entry == nil {
		if cm.logger != nil {
			cm.logger.Debug("Failed to load cache entry from disk", loggerv2.String("file", cacheFile))
		}
		return nil
	}

	// Store in cache (sync.Map is thread-safe, no lock needed)
	cm.cache.Store(cacheKey, entry)

	if cm.logger != nil {
		cm.logger.Debug("Reloaded cache entry from disk",
			loggerv2.String("key", cacheKey),
			loggerv2.Int("tools_count", len(entry.Tools)))
	}

	return entry
}

// getCacheFilePath returns the filesystem path for a cache key
func (cm *CacheManager) getCacheFilePath(cacheKey string) string {
	return filepath.Join(cm.cacheDir, fmt.Sprintf("%s.json", cacheKey))
}

// clearCacheDirectory removes all files from the cache directory
func (cm *CacheManager) clearCacheDirectory() error {
	files, err := os.ReadDir(cm.cacheDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		filePath := filepath.Join(cm.cacheDir, file.Name())
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			cm.logger.Warn("Failed to remove cache file",
				loggerv2.Error(err),
				loggerv2.String("file", filePath))
		}
	}

	return nil
}

// GetCacheDirectory returns the cache directory path
func (cm *CacheManager) GetCacheDirectory() string {
	return cm.cacheDir
}

// SetTTL sets the TTL for cache entries (in minutes)
func (cm *CacheManager) SetTTL(minutes int) {
	cm.ttlMinutes = minutes
}

// GetTTL returns the current TTL setting
func (cm *CacheManager) GetTTL() int {
	return cm.ttlMinutes
}
