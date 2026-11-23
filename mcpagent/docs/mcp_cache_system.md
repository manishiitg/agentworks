# MCP Cache System - Deep Dive

The MCP Agent includes a sophisticated multi-layer caching system designed to dramatically reduce connection times and improve performance when working with MCP servers.

## 🎯 Purpose & Performance

Connecting to multiple MCP servers and discovering their tools can be slow (5-30 seconds depending on the number of servers and network latency). The cache system solves this by:

1.  **Storing Server Metadata**: Tools, prompts, resources, and system prompts are saved after the first connection.
2.  **Configuration-Aware Keys**: Cache keys include a hash of the server configuration, ensuring cache is invalidated when server settings change.
3.  **Hybrid Mode**: Some servers can load from cache while others connect fresh (best of both worlds).

### Performance Impact
- **Cold Start (No Cache)**: 5-30 seconds to connect to all servers and discover tools.
- **Warm Start (Cache Hit)**: 60-85% faster - only need to establish connections, tool schemas are pre-loaded.
- **Cache TTL**: 7 days (configurable via `MCP_CACHE_TTL_MINUTES` environment variable).

---

## 📁 Files and Directories

### Core Implementation Files

#### 1. `mcpagent/mcpcache/manager.go`
**Purpose**: Singleton cache manager that handles all cache storage and retrieval operations.

**Key Types**:
```go
type CacheManager struct {
    cacheDir   string           // Directory where cache files are stored
    ttlMinutes int              // Time-to-live for cache entries
    logger     loggerv2.Logger  // Logger instance
    cache      sync.Map         // Thread-safe cacheKey -> *CacheEntry
}

type CacheEntry struct {
    ServerName    string              // Name of the MCP server
    Tools         []llmtypes.Tool     // Pre-normalized LLM tool definitions
    Prompts       []mcp.Prompt        // Server prompts
    Resources     []mcp.Resource      // Server resources
    SystemPrompt  string              // Generated system prompt fragment
    
    CreatedAt     time.Time           // When cache entry was created
    TTLMinutes    int                 // TTL for this entry
    Protocol      string              // "stdio", "sse", or "http"
    
    IsValid       bool                // Whether this entry is valid
    ErrorMessage  string              // Error if invalid
    
    ToolOwnership map[string]string   // Maps tool name -> "primary" or "duplicate"
}
```

**Key Functions**:
- `GetCacheManager(logger)` - Returns singleton instance
- `GenerateUnifiedCacheKey(serverName, config)` - Creates `unified_{serverName}_{configHash}`
- `Get(cacheKey)` - Retrieves cache entry if valid
- `Put(entry, config)` - Stores cache entry to memory and file
- `Invalidate(cacheKey)` - Removes specific cache entry
- `InvalidateByServer(configPath, serverName)` - Removes all entries for a server
- `ReloadFromDisk(cacheKey)` - Reloads entry from disk to memory
- `Cleanup()` - Removes expired entries

#### 2. `mcpagent/mcpcache/integration.go` (1250 lines)
**Purpose**: Integration layer that connects the cache system with the agent's connection logic.

**Key Functions**:
- `GetCachedOrFreshConnection()` - Main entry point, orchestrates cache lookup and hybrid logic
- `processCachedData()` - Processes cached entries and creates live connections
- `performFreshConnection()` - Makes fresh connections when cache misses
- `cacheFreshConnectionData()` - Saves fresh connection results to cache (async)
- `EmitComprehensiveCacheEvent()` - Emits observability events

#### 3. `mcpagent/mcpcache/openapi/` (directory)
**Purpose**: Generates OpenAPI 3.0 YAML specs for tools when in Code Execution Mode.

**Files**:
- `generator.go` - Generates per-server OpenAPI specs from MCP tool definitions
- `schema.go` - JSON Schema to OpenAPI schema conversion and naming utilities

### Cache Storage Files

#### Cache Directory Structure
```
agent_go/cache/
├── unified_aws_a1b2c3d4e5f6...json          # AWS server cache (includes config hash)
├── unified_github_f6e5d4c3b2a1...json        # GitHub server cache
├── unified_kubernetes_9876543210...json      # Kubernetes server cache
└── ...                                       # One file per server configuration
```

#### Example Cache File Content
**File**: `agent_go/cache/unified_aws_a1b2c3d4e5f6...json`

```json
{
  "server_name": "aws",
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "aws_list_instances",
        "description": "List EC2 instances",
        "parameters": {
          "type": "object",
          "properties": {
            "region": {
              "type": "string",
              "description": "AWS region"
            }
          },
          "required": ["region"]
        }
      }
    }
  ],
  "prompts": [...],
  "resources": [...],
  "system_prompt": "AWS MCP server with 45 tools for EC2, S3, IAM...",
  "created_at": "2025-01-15T10:30:00Z",
  "ttl_minutes": 10080,
  "protocol": "stdio",
  "is_valid": true,
  "tool_ownership": {
    "aws_list_instances": "primary",
    "aws_create_bucket": "primary"
  }
}
```

### OpenAPI Specs (Code Execution Mode)

In code execution mode, OpenAPI 3.0 YAML specs are generated on-demand when the LLM calls `get_api_spec(server_name)`. Specs are generated in-memory from cached MCP tool definitions and cached on the agent after first generation. No files are written to disk.

The OpenAPI spec includes:
- Per-tool REST endpoints (`POST /tools/mcp/{server}/{tool}`)
- Request body schemas derived from MCP tool parameter JSON Schemas
- Bearer token authentication scheme
- Standard response format

---

## 🔄 Detailed Workflow

### Step-by-Step: Agent Startup with Cache

Let's trace what happens when an agent starts with `serverName = "aws,github"` and `configPath = "configs/mcp_server_actual.json"`:

#### Phase 1: Configuration Loading
**File**: `mcpagent/mcpcache/integration.go` (Line 212)

1. Load merged MCP configuration from `configs/mcp_server_actual.json`
2. Parse requested servers: `["aws", "github"]`
3. Initialize `CacheManager` singleton if not already created

#### Phase 2: Cache Key Generation
**File**: `mcpagent/mcpcache/manager.go` (Lines 128-198)

For each server:
1. Get server configuration from config file
2. Extract: `command`, `args`, `env`, `url`, `headers`, `protocol`
3. Sort maps deterministically (for consistent hashing)
4. Marshal to JSON
5. Generate SHA256 hash
6. Create cache key: `unified_aws_a1b2c3d4e5f6...`

**Example**:
```go
// AWS Server Config
config := MCPServerConfig{
    Command: "npx",
    Args: ["-y", "@modelcontextprotocol/server-aws"],
    Env: {"AWS_REGION": "us-east-1"},
    Protocol: "stdio"
}

// Generate hash
configHash := GenerateServerConfigHash(config)
// Result: "a1b2c3d4e5f6789..."

cacheKey := "unified_aws_a1b2c3d4e5f6789..."
```

#### Phase 3: Cache Lookup
**File**: `mcpagent/mcpcache/manager.go` (Lines 200-230)

For each cache key:
1. Check in-memory cache: `cacheManager.cache[cacheKey]`
2. If not in memory, try `ReloadFromDisk(cacheKey)`:
   - Read `agent_go/cache/{cacheKey}.json`
   - Unmarshal JSON to `CacheEntry`
   - Check if expired: `entry.CreatedAt + TTL > now`
   - If valid, load into memory
3. Return cache entry or `nil`

**Result Classification**:
- `AWS`: Cache hit (entry found and valid)
- `GitHub`: Cache miss (no file exists)

#### Phase 4: Hybrid Mode Processing
**File**: `mcpagent/mcpcache/integration.go` (Lines 416-498)

Scenario 2 (Partial Cache Hit) is triggered:
- `cachedServers`: `["aws"]`
- `missedServers`: `["github"]`

**Action Plan**:
1. Process cached data for AWS (use tool definitions from cache)
2. Connect fresh to GitHub (discover tools)
3. Merge results

#### Phase 5: Processing Cached Data (AWS)
**File**: `mcpagent/mcpcache/integration.go` (Lines 548-691)

1. Read cached tools from `CacheEntry.Tools` (pre-normalized)
2. Check `ToolOwnership` map:
   - `"aws_list_instances": "primary"` → Include tool
   - `"some_duplicate_tool": "duplicate"` →Skip tool
3. Aggregate cached tools, prompts, resources
4. **Still create live connection** to AWS server (for tool execution)
5. Use cached tool schemas (skip tool discovery = 80% time save)

#### Phase 6: Fresh Connection (GitHub)
**File**: `mcpagent/mcpcache/integration.go` (Lines 694-950)

1. Call `mcpclient.DiscoverAllToolsParallel(ctx, config, logger)`
2. Connect to GitHub MCP server
3. Call `client.ListTools()` to discover tools
4. Normalize tools: `mcpclient.NormalizeLLMTools(tools)`
5. Mark tool ownership (first-come-first-serve)
6. Return fresh connection result

#### Phase 7: Async Caching (GitHub)
**File**: `mcpagent/mcpcache/integration.go` (Lines 952-1050)

Spawned as goroutine (non-blocking):
1. Normalize GitHub tools before caching
2. Build `ToolOwnership` map
3. Create `CacheEntry` struct
4. Marshal to JSON
5. Write to `agent_go/cache/unified_github_f6e5d4c3...json`
6. Generate Go code in `agent_go/generated/github/`
7. Regenerate `agent_go/generated/index.go`

#### Phase 8: Result Aggregation
**File**: `mcpagent/mcpcache/integration.go` (Lines 454-468)

Merge cached (AWS) and fresh (GitHub) data:
```go
result.Tools = append(cachedAWSTools, freshGitHubTools...)
result.Clients["aws"] = awsClient
result.Clients["github"] = githubClient
result.ToolToServer["aws_list_instances"] = "aws"
result.ToolToServer["github_list_repos"] = "github"
```

---

## 🔑 Configuration-Aware Cache Keys

### Why Configuration Hashing Matters

**Problem**: If you change an environment variable (e.g., `AWS_REGION`), the cache should be invalidated.

**Solution**: Include a hash of the entire server configuration in the cache key.

### Hash Generation Process
**File**: `mcpagent/mcpcache/manager.go` (Lines 128-185)

```go
func GenerateServerConfigHash(config mcpclient.MCPServerConfig) string {
    // 1. Extract configuration fields
    configData := struct {
        Command  string
        Args     []string
        Env      map[string]string
        URL      string
        Headers  map[string]string
        Protocol string
    }{
        Command:  config.Command,
        Args:     config.Args,
        Env:      config.Env,
        URL:      config.URL,
        Headers:  config.Headers,
        Protocol: string(config.Protocol),
    }
    
    // 2. Sort maps for deterministic output
    sortedEnv := sortMapKeys(configData.Env)
    sortedHeaders := sortMapKeys(configData.Headers)
    
    // 3. Marshal to JSON
    jsonData, _ := json.Marshal(configData)
    
    // 4. Generate SHA256 hash
    hash := sha256.Sum256(jsonData)
    return hex.EncodeToString(hash[:])
}
```

### Examples

**Example 1**: AWS with US-EAST-1
```
Config: {Command: "npx", Env: {"AWS_REGION": "us-east-1"}}
Hash: a1b2c3d4e5f6789...
Cache Key: unified_aws_a1b2c3d4e5f6789...
```

**Example 2**: AWS with EU-WEST-1 (different region)
```
Config: {Command: "npx", Env: {"AWS_REGION": "eu-west-1"}}
Hash: 9876543210fedcba...  (DIFFERENT HASH)
Cache Key: unified_aws_9876543210fedcba...  (DIFFERENT FILE)
```

Result: Two separate cache files for the same server with different configurations.

---

## 🛡️ Race Condition Prevention

### Problem: Concurrent Access

Multiple goroutines might try to read/write cache simultaneously:
- Agent initialization
- Async cache writes
- Cache cleanup routines

### Solutions Implemented

#### 1. Pre-Normalization
**File**: `mcpagent/mcpcache/integration.go` (Lines 972-975)

Tools are normalized **before** caching, not after retrieval:
```go
// BEFORE caching (in cacheFreshConnectionData)
mcpclient.NormalizeLLMTools(serverTools)  // Mutates tools
cacheManager.Put(entry, config)           // Cache normalized tools

// AFTER retrieval (in processCachedData)
tools := cachedEntry.Tools  // Already normalized, no mutation needed
```

#### 2. Deep Copies
**File**: `mcpagent/mcpcache/manager.go`

`GetAllEntries()` returns deep copies:
```go
func (cm *CacheManager) GetAllEntries() map[string]*CacheEntry {
    result := make(map[string]*CacheEntry)
    cm.cache.Range(func(key, value interface{}) bool {
        entry := value.(*CacheEntry)
        entryCopy := *entry  // Copy struct
        
        // Deep copy slices
        entryCopy.Tools = make([]llmtypes.Tool, len(entry.Tools))
        copy(entryCopy.Tools, entry.Tools)
        
        // Deep copy maps
        entryCopy.ToolOwnership = make(map[string]string)
        for k, v := range entry.ToolOwnership {
            entryCopy.ToolOwnership[k] = v
        }
        
        result[key] = &entryCopy
        result[key.(string)] = &entryCopy
        return true
    })
    return result
}
```

#### 3. Minimal Lock Duration
**File**: `mcpagent/mcpcache/manager.go` (Lines 601-632)

Expensive I/O happens **outside** locks:
```go
func (cm *CacheManager) ReloadFromDisk(cacheKey string) *CacheEntry {
    cacheFile := cm.getCacheFilePath(cacheKey)
    
    // I/O OUTSIDE lock (expensive operation)
    entry := cm.loadFromFile(cacheFile)
    if entry == nil {
        return nil
    }
    
    // LOCK only for memory update (fast operation)
    cm.mu.Lock()
    cm.cache[cacheKey] = entry
    cm.mu.Unlock()
    
    return entry
}
```

---

## 🔧 Duplicate Tool Handling

### Problem

Multiple servers might provide the same tool:
- `workspace.create_file`
- `filesystem.create_file`

If both are registered, LLM providers (especially Gemini/Vertex) reject the duplicate function declarations.

### Solution: ToolOwnership Tracking

**File**: `mcpagent/mcpcache/integration.go` (Lines 977-998)

#### During Caching
```go
// First server to register a tool becomes "primary"
toolOwnership := make(map[string]string)
for _, tool := range serverTools {
    toolName := tool.Function.Name
    owningServer := result.ToolToServer[toolName]
    
    if owningServer == currentServerName {
        toolOwnership[toolName] = "primary"  // This server owns it
    } else {
        toolOwnership[toolName] = "duplicate"  // Another server owns it
    }
}

cacheEntry.ToolOwnership = toolOwnership
```

#### During Retrieval
**File**: `mcpagent/mcpcache/integration.go` (Lines 598-607)

```go
for _, tool := range cachedEntry.Tools {
    toolName := tool.Function.Name
    
    // Check ownership
    if cachedEntry.ToolOwnership[toolName] == "duplicate" {
        logger.Debugf("Skipping duplicate tool %s", toolName)
        continue  // Skip this tool
    }
    
    // Tool is "primary" or not marked - include it
    result.Tools = append(result.Tools, tool)
}
```

---

## 📈 Observability & Events

### Event Types
**File**: `mcpagent/mcpcache/integration.go` (Lines 36-86)

1. **CacheHitEvent**: Tool definitions loaded from cache
2. **CacheMissEvent**: Server needs fresh connection
3. **CacheWriteEvent**: Fresh data saved to cache
4. **ComprehensiveCacheEvent**: Consolidated event with all details

### Comprehensive Cache Event
```go
type ComprehensiveCacheEvent struct {
    Operation      string  // "start", "complete", "error"
    CacheUsed      bool
    FreshFallback  bool
    
    ServersCount   int
    TotalTools     int
    
    ServerStatus   map[string]ServerCacheStatus
    
    CacheHits      int
    CacheMisses    int
    CacheWrites    int
    
    ConnectionTime string
    CacheTime      string
}
```

---

## ⚙️ Configuration

### Environment Variables

```bash
# Cache directory (default: ./cache or /app/cache in Docker)
export MCP_CACHE_DIR="/custom/cache/path"

# Cache TTL in minutes (default: 10080 = 7 days)
export MCP_CACHE_TTL_MINUTES=1440  # 1 day

# Generated code directory (no longer used - OpenAPI specs are in-memory)
# export MCP_GENERATED_DIR="/custom/generated/path"
```

### Programmatic API

```go
// Get singleton instance
cacheManager := mcpcache.GetCacheManager(logger)

// Modify TTL
cacheManager.SetTTL(1440)  // 1 day

// Get cache statistics
stats := cacheManager.GetStats()
// Returns: {
//   "total_entries": 5,
//   "valid_entries": 4,
//   "expired_entries": 1,
//   "estimated_size": 1024576,
//   "cache_directory": "./cache",
//   "ttl_minutes": 10080
// }

// Clear entire cache
cacheManager.Clear()

// Invalidate specific server (all configurations)
cacheManager.InvalidateByServer(configPath, "aws")

// Manual cleanup of expired entries
cacheManager.Cleanup()
```

---

## 💡 Best Practices

1.  **Don't Manually Edit Cache Files**: The cache is self-managing. Manual edits may cause inconsistencies.
2.  **Monitor Cache Hits**: Use observability events to track cache hit rates. Frequent misses indicate configuration is changing often.
3.  **Clear Cache After Major Upgrades**: If you upgrade MCP servers significantly, clear the cache to ensure fresh tool definitions.
4.  **Use Configuration Hashing**: Rely on the automatic cache invalidation via config hashing rather than manual cache clearing.
5.  **Understand Hybrid Mode**: The system can use partial cache - don't assume it's all-or-nothing.
