package mcpclient

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/netguard"
	"github.com/manishiitg/mcpagent/oauth"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// RetryConfig defines the retry behavior for MCP connections
type RetryConfig struct {
	MaxRetries     int           // Maximum number of retry attempts
	InitialDelay   time.Duration // Initial delay between retries
	MaxDelay       time.Duration // Maximum delay between retries
	BackoffFactor  float64       // Exponential backoff multiplier
	ConnectTimeout time.Duration // Timeout for each individual connection attempt
}

// DefaultRetryConfig returns a sensible default retry configuration
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:     3,
		InitialDelay:   1 * time.Second,
		MaxDelay:       30 * time.Second,
		BackoffFactor:  2.0,
		ConnectTimeout: 15 * time.Minute, // Increased from 10 minutes to 15 minutes for very slow npx commands
	}
}

// Client wraps the underlying MCP client with convenience methods
type Client struct {
	config          MCPServerConfig
	mcpClient       *client.Client
	serverInfo      *mcp.Implementation
	retryConfig     RetryConfig
	logger          loggerv2.Logger
	contextCancel   context.CancelFunc // Store context cancel function for SSE connections
	context         context.Context    // Store context for SSE connections
	mu              sync.RWMutex       // Protect access to contextCancel, context, and leakGuard
	oauthManager    *oauth.Manager     // OAuth manager for authentication
	reconnectMu     sync.Mutex         // Serializes mid-session reconnects (resilience.go)
	connGen         atomic.Int64       // Connection generation; bumped on each successful connect
	connectAttempts atomic.Int32       // Total connectOnce calls (retry accounting in tests)
	leakGuard       *runtime.Cleanup   // GC guard that reaps unclosed connections (resilience.go)
}

// New creates a new MCP client for the given server configuration
func New(config MCPServerConfig, logger loggerv2.Logger) *Client {
	return &Client{
		config:      config,
		retryConfig: DefaultRetryConfig(),
		logger:      logger,
	}
}

// Connect establishes a connection to the MCP server with retry logic
func (c *Client) Connect(ctx context.Context) error {
	// A refused public-only server is refused once, never retried.
	if err := c.checkPublicOnly(); err != nil {
		return err
	}
	maxRetries := 3
	baseDelay := time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			delay := time.Duration(attempt-1) * baseDelay
			c.logger.Debug("Retrying MCP connection",
				loggerv2.Int("attempt", attempt),
				loggerv2.Int("max_retries", maxRetries),
				loggerv2.String("server", c.getServerName()),
				loggerv2.String("delay", delay.String()))
			time.Sleep(delay)
		}

		protocol := c.config.GetProtocol()
		if protocol == ProtocolStdio {
			c.logger.Debug("Connecting to MCP server",
				loggerv2.String("server", c.getServerName()),
				loggerv2.String("protocol", string(protocol)),
				loggerv2.String("command", c.config.Command),
				loggerv2.Any("args", c.config.Args))
		} else {
			c.logger.Debug("Connecting to MCP server",
				loggerv2.String("server", c.getServerName()),
				loggerv2.String("protocol", string(protocol)),
				loggerv2.String("url", c.config.URL))
		}

		err := c.connectOnce(ctx)
		if err == nil {
			if attempt > 1 {
				c.logger.Info("Successfully connected to MCP server after retry attempts",
					loggerv2.String("server", c.getServerName()),
					loggerv2.Int("retry_attempts", attempt-1))
			} else {
				c.logger.Info("Successfully connected to MCP server on first attempt",
					loggerv2.String("server", c.getServerName()))
			}
			return nil
		}

		c.logger.Error("Connection attempt failed", err,
			loggerv2.String("server", c.getServerName()),
			loggerv2.Int("attempt", attempt))

		if isNonRetryableConnectError(err) {
			return c.refusedConnectError(err)
		}
		if attempt == maxRetries {
			return fmt.Errorf("failed to connect to MCP server '%s' after %d attempts: %w", c.getServerName(), maxRetries, err)
		}
	}

	return fmt.Errorf("unexpected error in retry loop for server '%s'", c.getServerName())
}

// connectOnce performs a single connection attempt
func (c *Client) connectOnce(ctx context.Context) error {
	c.connectAttempts.Add(1)
	// Close existing client before reconnect to prevent subprocess leaks
	if c.mcpClient != nil {
		c.logger.Debug("Closing existing mcpClient before reconnect")
		_ = c.mcpClient.Close()
		c.mcpClient = nil
	}

	// Prepare environment variables
	// Start with the current process environment, then override with config env vars
	env := os.Environ()

	// Create a map of existing env vars for quick lookup
	envMap := make(map[string]string)
	for _, e := range env {
		if idx := strings.IndexByte(e, '='); idx > 0 {
			key := e[:idx]
			value := e[idx+1:]
			envMap[key] = value
		}
	}

	// Override with config env vars
	for key, value := range c.config.Env {
		envMap[key] = value
	}

	// Convert back to []string format
	env = make([]string, 0, len(envMap))
	for key, value := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}

	// A user-supplied server is remote-only with a public https URL, and
	// every request goes through the public-only client.
	var guarded *http.Client
	if c.config.PublicOnly {
		if err := c.checkPublicOnly(); err != nil {
			return err
		}
		guarded = netguard.Client(0)
		if c.config.OAuth != nil {
			c.config.OAuth.PublicOnly = true
		}
	}

	// Handle OAuth authentication if configured
	if c.config.OAuth != nil {
		if err := c.setupOAuthAuth(ctx); err != nil {
			return fmt.Errorf("OAuth authentication failed: %w", err)
		}
	}

	var mcpClient *client.Client
	var err error

	// Create MCP client based on protocol type (use smart detection)
	protocol := c.config.GetProtocol()
	switch protocol {
	case ProtocolSSE:
		// Use SSE transport
		sseManager := NewSSEManager(c.config.URL, c.config.Headers, c.logger)
		if guarded != nil {
			sseManager.WithHTTPClient(guarded)
		}
		mcpClient, err = sseManager.Connect(ctx)
		if err != nil {
			return fmt.Errorf("failed to create SSE MCP client: %w", err)
		}

	case ProtocolHTTP:
		// Use HTTP transport
		httpManager := NewHTTPManager(c.config.URL, c.config.Headers, c.logger)
		if guarded != nil {
			httpManager.WithHTTPClient(guarded)
		}
		mcpClient, err = httpManager.Connect(ctx)
		if err != nil {
			return fmt.Errorf("failed to create HTTP MCP client: %w", err)
		}

	case ProtocolStdio:
		fallthrough
	default:
		// Default to stdio for backward compatibility
		stdioManager := NewStdioManager(c.config.Command, c.config.Args, env, c.config.WorkingDir, c.logger)
		mcpClient, err = stdioManager.Connect(ctx)
		if err != nil {
			return fmt.Errorf("failed to create MCP client: %w", err)
		}
	}

	c.mcpClient = mcpClient

	// For stdio clients, initialization is handled by the transport manager
	// For other protocols, we need to initialize here
	if protocol != ProtocolStdio {
		// Initialize connection
		initResult, err := c.mcpClient.Initialize(ctx, mcp.InitializeRequest{
			Params: mcp.InitializeParams{
				ProtocolVersion: "2024-11-05",
				Capabilities:    mcp.ClientCapabilities{},
				ClientInfo: mcp.Implementation{
					Name:    "mcp-agent-go",
					Version: "1.0.0",
				},
			},
		})
		if err != nil {
			_ = c.mcpClient.Close() // Ignore errors during cleanup
			return fmt.Errorf("failed to initialize MCP connection: %w", err)
		}

		c.serverInfo = &initResult.ServerInfo
	} else {
		// For stdio, we need to get server info separately since initialization was already done
		// We'll get this from the first tool listing or other operation
		c.serverInfo = &mcp.Implementation{
			Name:    "stdio-server",
			Version: "1.0.0",
		}
	}

	c.connGen.Add(1)
	c.mu.Lock()
	c.armLeakGuardLocked()
	c.mu.Unlock()

	return nil
}

// ConnectWithRetry establishes connection to the MCP server with retry logic
func (c *Client) ConnectWithRetry(ctx context.Context) error {
	var lastErr error

	for attempt := 0; attempt <= c.retryConfig.MaxRetries; attempt++ {
		if attempt > 0 {
			// Calculate delay with exponential backoff
			delay := time.Duration(float64(c.retryConfig.InitialDelay) * math.Pow(c.retryConfig.BackoffFactor, float64(attempt-1)))
			if delay > c.retryConfig.MaxDelay {
				delay = c.retryConfig.MaxDelay
			}

			c.logger.Debug("Retrying MCP connection",
				loggerv2.Int("attempt", attempt+1),
				loggerv2.Int("max_retries", c.retryConfig.MaxRetries+1),
				loggerv2.String("server", c.getServerName()),
				loggerv2.String("delay", delay.String()))

			select {
			case <-time.After(delay):
				// Continue with retry
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during retry delay: %w", ctx.Err())
			}
		}

		// Create context with timeout for this specific attempt
		connectCtx, cancel := context.WithTimeout(ctx, c.retryConfig.ConnectTimeout)

		// Log connection attempt
		if attempt == 0 {
			c.logger.Debug("Connecting to MCP server",
				loggerv2.String("server", c.getServerName()),
				loggerv2.String("command", c.config.Command),
				loggerv2.Any("args", c.config.Args))
		}

		// One attempt per iteration. This used to call Connect, which runs its
		// own 3-attempt loop, so a failing server was tried 12 times with both
		// loops' backoffs stacked (~19s for a server that fails instantly).
		err := c.connectOnce(connectCtx)
		cancel()

		if err == nil {
			if attempt > 0 {
				c.logger.Info("Successfully connected to MCP server after retry attempts",
					loggerv2.String("server", c.getServerName()),
					loggerv2.Int("retry_attempts", attempt))
			} else {
				c.logger.Info("Successfully connected to MCP server on first attempt",
					loggerv2.String("server", c.getServerName()))
			}
			return nil
		}

		lastErr = err
		c.logger.Error("Connection attempt failed", err,
			loggerv2.String("server", c.getServerName()),
			loggerv2.Int("attempt", attempt+1))

		if isNonRetryableConnectError(err) {
			return c.refusedConnectError(err)
		}
		// If this was the last attempt, don't sleep
		if attempt == c.retryConfig.MaxRetries {
			break
		}

		// Check if context was cancelled
		if ctx.Err() != nil {
			return fmt.Errorf("context cancelled during connection retry: %w", ctx.Err())
		}
	}

	return fmt.Errorf("failed to connect to MCP server '%s' after %d attempts: %w",
		c.getServerName(), c.retryConfig.MaxRetries+1, lastErr)
}

// getServerName returns a human-readable name for the server (used for logging)
func (c *Client) getServerName() string {
	if c.config.Description != "" {
		return c.config.Description
	}
	return fmt.Sprintf("%s %v", c.config.Command, c.config.Args)
}

// Close closes the connection to the MCP server
func (c *Client) Close() error {
	// For SSE connections, cancel the stored context first
	if c.contextCancel != nil {
		c.logger.Debug("Canceling SSE context before closing client")
		c.contextCancel()
	}

	// Clear the stored context and cancel function; explicit Close means no leak.
	c.mu.Lock()
	c.context = nil
	c.contextCancel = nil
	c.disarmLeakGuardLocked()
	c.mu.Unlock()

	if c.mcpClient != nil {
		return c.mcpClient.Close()
	}
	return nil
}

// Ping checks if the connection is still alive by sending a ping to the MCP server.
func (c *Client) Ping(ctx context.Context) error {
	if c.mcpClient == nil {
		return fmt.Errorf("client not connected")
	}
	return c.mcpClient.Ping(ctx)
}

// GetServerInfo returns information about the connected server
func (c *Client) GetServerInfo() *mcp.Implementation {
	return c.serverInfo
}

// ListTools returns all available tools from the server
func (c *Client) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	c.logger.Debug("Starting ListTools call")

	if c.mcpClient == nil {
		c.logger.Debug("Client not connected")
		return nil, fmt.Errorf("client not connected")
	}

	c.logger.Debug("About to call underlying mcpClient.ListTools")
	deadline, hasDeadline := ctx.Deadline()
	c.logger.Debug("Context info",
		loggerv2.Any("has_deadline", hasDeadline),
		loggerv2.Any("deadline", deadline),
		loggerv2.Any("done", ctx.Done()))

	listStartTime := time.Now()

	// Call ListTools directly without goroutine wrapper
	c.logger.Debug("About to make the actual ListTools call")

	c.logger.Debug("Making ListTools call without an added timeout")
	tools, err := listAllTools(ctx, c.mcpClient.ListTools)

	listDuration := time.Since(listStartTime)
	c.logger.Debug("ListTools call completed",
		loggerv2.String("duration", listDuration.String()))

	if err != nil {
		c.logger.Error("Failed to list tools", err)
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	c.logger.Debug("Successfully listed tools", loggerv2.Int("tool_count", len(tools)))
	return tools, nil
}

// maxToolListPages bounds tools/list pagination, so a server that keeps
// returning a cursor cannot hold discovery forever.
const maxToolListPages = 50

// listAllTools follows tools/list pagination (nextCursor) until the server
// stops returning a cursor, up to maxToolListPages. A server that paginates
// would otherwise show only its first page of tools.
func listAllTools(ctx context.Context, list func(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error)) ([]mcp.Tool, error) {
	var tools []mcp.Tool
	var cursor mcp.Cursor
	for page := 0; page < maxToolListPages; page++ {
		request := mcp.ListToolsRequest{}
		request.Params.Cursor = cursor
		result, err := list(ctx, request)
		if err != nil {
			return nil, err
		}
		tools = append(tools, result.Tools...)
		if result.NextCursor == "" || result.NextCursor == cursor {
			return tools, nil
		}
		cursor = result.NextCursor
	}
	return tools, fmt.Errorf("tools/list returned more than %d pages", maxToolListPages)
}

// CallTool invokes a tool with the given arguments. If the transport died
// mid-session (server crash, dropped stream), it reconnects once and retries
// the call — see resilience.go.
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*mcp.CallToolResult, error) {
	if c.mcpClient == nil {
		return nil, fmt.Errorf("client not connected")
	}

	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: arguments,
		},
	}

	observedGen := c.connGeneration()
	result, err := c.mcpClient.CallTool(ctx, request)
	if err == nil {
		return result, nil
	}

	if !shouldReconnectAfterError(ctx, err) {
		return nil, fmt.Errorf("failed to call tool %s: %w", name, err)
	}
	if reconnectErr := c.reconnectIfStale(ctx, observedGen); reconnectErr != nil {
		return nil, fmt.Errorf("failed to call tool %s: %w (reconnect also failed: %w)", name, err, reconnectErr)
	}

	c.logger.Warn("Retrying tool call after mid-session reconnect",
		loggerv2.String("server", c.getServerName()),
		loggerv2.String("tool", name))
	result, err = c.mcpClient.CallTool(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to call tool %s after reconnect: %w", name, err)
	}
	return result, nil
}

// SetContextCancel stores the context cancel function for later cleanup (used for SSE connections)
func (c *Client) SetContextCancel(cancel context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.contextCancel = cancel
}

// GetContextCancel retrieves the stored context cancel function
func (c *Client) GetContextCancel() context.CancelFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.contextCancel
}

// SetContext stores the context for SSE connections
func (c *Client) SetContext(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.context = ctx
}

// GetContext retrieves the stored context
func (c *Client) GetContext() context.Context {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.context
}

// ConnectWithTimeout is a convenience method that connects with a default timeout
func (c *Client) ConnectWithTimeout(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return c.ConnectWithRetry(ctx)
}

// ParallelToolDiscoveryResult represents the result of discovering tools from a single server
type ParallelToolDiscoveryResult struct {
	ServerName string
	Tools      []mcp.Tool
	Error      error
	Client     ClientInterface // Add client to the result so it can be reused
}

// DiscoverAllToolsParallel connects to all servers in the config in parallel, lists tools, and returns results per server.
func DiscoverAllToolsParallel(ctx context.Context, cfg *MCPConfig, logger loggerv2.Logger) []ParallelToolDiscoveryResult {
	servers := cfg.ListServers()
	if len(servers) == 0 {
		logger.Debug("No servers configured, returning empty result")
		return []ParallelToolDiscoveryResult{}
	}

	logger.Info("DiscoverAllToolsParallel started",
		loggerv2.Int("server_count", len(servers)),
		loggerv2.Any("servers", servers))
	logger.Debug("Parent context info",
		loggerv2.Any("done", ctx.Done()),
		loggerv2.Any("err", ctx.Err()))

	resultsCh := make(chan ParallelToolDiscoveryResult, len(servers))
	var wg sync.WaitGroup

	logger.Debug("Starting goroutines for servers", loggerv2.Int("count", len(servers)))
	for _, name := range servers {
		srvCfg, _ := cfg.GetServer(name) // ignore error, will be caught below
		wg.Add(1)
		logger.Debug("Starting goroutine for server",
			loggerv2.String("server", name),
			loggerv2.String("protocol", string(srvCfg.Protocol)))
		go func(name string, srvCfg MCPServerConfig) {
			// Ensure we always send a result and call wg.Done, even if we panic
			resultSent := false
			defer func() {
				if !resultSent {
					logger.Warn("Goroutine exiting without sending result, sending error result",
						loggerv2.String("server", name))
					// Send error result to prevent deadlock
					select {
					case resultsCh <- ParallelToolDiscoveryResult{
						ServerName: name,
						Tools:      nil,
						Error:      fmt.Errorf("goroutine exited unexpectedly without sending result"),
						Client:     nil,
					}:
						resultSent = true
					default:
						// Channel might be closed or full, but we tried
						logger.Error("Failed to send error result, channel may be closed", nil,
							loggerv2.String("server", name))
					}
				}
				wg.Done()
			}()

			logger.Debug("Goroutine started for server", loggerv2.String("server", name))

			// Check parent context cancellation before starting connection
			// This prevents creating isolated contexts when parent is already cancelled
			select {
			case <-ctx.Done():
				logger.Warn("Parent context cancelled before starting connection, skipping server",
					loggerv2.String("server", name),
					loggerv2.Error(ctx.Err()))
				resultsCh <- ParallelToolDiscoveryResult{
					ServerName: name,
					Tools:      nil,
					Error:      fmt.Errorf("parent context cancelled: %w", ctx.Err()),
					Client:     nil,
				}
				resultSent = true
				return
			default:
				// Parent context still valid, continue
			}

			client := New(srvCfg, logger)
			var cancel context.CancelFunc
			var connCtx context.Context

			if srvCfg.Protocol == ProtocolSSE {
				// For SSE, create a new background context with timeout to avoid parent cancellation
				// IMPORTANT: Do NOT defer cancel() here - we need the context to remain valid for the entire client lifecycle
				connCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 15*time.Minute)
				logger.Debug("Using SSE protocol with isolated context",
					loggerv2.String("server", name),
					loggerv2.String("timeout", "15m"))
			} else {
				// For stdio and other protocols, also use isolated context with longer timeout
				connCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 15*time.Minute)
				defer cancel() // Safe to cancel immediately for non-SSE protocols
				logger.Debug("Using protocol with isolated context",
					loggerv2.String("protocol", string(srvCfg.Protocol)),
					loggerv2.String("server", name),
					loggerv2.String("timeout", "15m"))
			}

			logger.Info("Connecting to MCP server", loggerv2.String("server", name))
			connectStartTime := time.Now()

			if err := client.ConnectWithRetry(connCtx); err != nil {
				connectDuration := time.Since(connectStartTime)
				logger.Error("Connection failed", err,
					loggerv2.String("server", name),
					loggerv2.String("duration", connectDuration.String()))
				if cancel != nil {
					cancel() // Clean up context on connection failure
				}
				resultsCh <- ParallelToolDiscoveryResult{ServerName: name, Tools: nil, Error: err, Client: nil}
				resultSent = true
				return
			}

			connectDuration := time.Since(connectStartTime)
			logger.Info("Connection successful",
				loggerv2.String("server", name),
				loggerv2.String("duration", connectDuration.String()))

			// For SSE connections, the SSE manager now uses background context for Start() automatically
			// For other protocols, no additional Start() call is needed
			logger.Debug("Client ready for use", loggerv2.String("server", name))

			// For SSE connections, use the same isolated context for tool listing
			// For other protocols, use the same isolated context
			listCtx := connCtx // Use the same isolated context for all protocols

			// Check parent context cancellation before tool listing
			// This allows early exit if parent context is cancelled
			select {
			case <-ctx.Done():
				logger.Warn("Parent context cancelled before tool listing, aborting",
					loggerv2.String("server", name),
					loggerv2.Error(ctx.Err()))
				// Close the client to prevent subprocess leak
				_ = client.Close()
				if cancel != nil {
					cancel() // Clean up context
				}
				resultsCh <- ParallelToolDiscoveryResult{
					ServerName: name,
					Tools:      nil,
					Error:      fmt.Errorf("parent context cancelled during tool listing: %w", ctx.Err()),
					Client:     nil,
				}
				resultSent = true
				return
			default:
				// Parent context still valid, continue
			}

			logger.Debug("Starting tool listing for server", loggerv2.String("server", name))
			logger.Debug("Context info before ListTools",
				loggerv2.String("server", name),
				loggerv2.Any("context_done", listCtx.Done()),
				loggerv2.Any("context_err", listCtx.Err()))
			listStartTime := time.Now()

			logger.Debug("Calling client.ListTools for server", loggerv2.String("server", name))
			tools, err := client.ListTools(listCtx)

			listDuration := time.Since(listStartTime)
			logger.Debug("ListTools completed",
				loggerv2.String("server", name),
				loggerv2.String("duration", listDuration.String()),
				loggerv2.Error(err))

			// Don't close the client here - we need to reuse it for agent creation
			// _ = client.Close()

			// For SSE connections, store the context and cancel function for later cleanup
			// Don't cancel the context here - it needs to remain valid for the client lifecycle
			if srvCfg.Protocol == ProtocolSSE {
				// Store the context and cancel function in the client for later cleanup
				// We'll cancel it when the client is actually closed
				client.SetContextCancel(cancel)
				client.SetContext(connCtx) // Store the context as well
				logger.Debug("Stored SSE context and cancel function for later cleanup",
					loggerv2.String("server", name))
			}

			if err != nil {
				logger.Error("Tool listing failed", err, loggerv2.String("server", name))
			} else {
				logger.Info("Tool listing successful",
					loggerv2.String("server", name),
					loggerv2.Int("tools_count", len(tools)))
			}

			logger.Debug("Sending result for server", loggerv2.String("server", name))
			resultsCh <- ParallelToolDiscoveryResult{ServerName: name, Tools: tools, Error: err, Client: client}
			resultSent = true
			logger.Debug("Result sent for server", loggerv2.String("server", name))
		}(name, srvCfg)
	}

	results := make([]ParallelToolDiscoveryResult, 0, len(servers))
	received := make(map[string]bool)
	total := len(servers)

	logger.Debug("Starting result collection loop", loggerv2.Int("total", total))
	timeout := false
	done := make(chan struct{})
	go func() {
		wg.Wait()
		logger.Debug("All goroutines finished, closing done channel")
		close(done)
	}()

	// Add a maximum timeout for result collection (30 minutes to allow for retries)
	// This prevents infinite waiting if goroutines get stuck
	resultCollectionTimeout := 30 * time.Minute
	resultCollectionCtx, resultCollectionCancel := context.WithTimeout(ctx, resultCollectionTimeout)
	defer resultCollectionCancel()

	resultCollectionStartTime := time.Now()
	allGoroutinesDone := false
	for receivedCount := 0; receivedCount < total && !timeout && !allGoroutinesDone; {
		logger.Debug("Waiting for results",
			loggerv2.Int("received", receivedCount),
			loggerv2.Int("total", total))
		select {
		case r := <-resultsCh:
			results = append(results, r)
			received[r.ServerName] = true
			receivedCount++
			logger.Debug("Received result for server",
				loggerv2.String("server", r.ServerName),
				loggerv2.Int("total_received", receivedCount),
				loggerv2.Int("total", total))
		case <-resultCollectionCtx.Done():
			logger.Warn("Result collection timeout or parent context cancelled, stopping result collection",
				loggerv2.String("reason", resultCollectionCtx.Err().Error()))
			timeout = true
		case <-done:
			logger.Debug("All goroutines finished, draining remaining results")
			allGoroutinesDone = true
			// Drain any remaining results before breaking
			drained := false
			for !drained {
				select {
				case r := <-resultsCh:
					results = append(results, r)
					received[r.ServerName] = true
					receivedCount++
					logger.Debug("Drained result for server",
						loggerv2.String("server", r.ServerName),
						loggerv2.Int("total_received", receivedCount))
				default:
					drained = true
				}
			}
		}
	}

	resultCollectionDuration := time.Since(resultCollectionStartTime)
	logger.Debug("Result collection completed",
		loggerv2.String("duration", resultCollectionDuration.String()),
		loggerv2.Any("timeout", timeout),
		loggerv2.Int("received", len(results)),
		loggerv2.Int("total", total))

	// If timeout, add missing servers as timeouts
	if timeout {
		logger.Warn("Timeout detected, adding missing servers as timeouts")
		for _, name := range servers {
			if !received[name] {
				logger.Warn("Adding timeout result for missing server", loggerv2.String("server", name))
				results = append(results, ParallelToolDiscoveryResult{
					ServerName: name,
					Tools:      nil,
					Error:      fmt.Errorf("tool discovery timed out for this server"),
				})
			}
		}
	}

	// Drain any remaining results (if any)
	for len(results) < total {
		select {
		case r := <-resultsCh:
			results = append(results, r)
		default:
			// No more results available
		}
	}

	// Emit comprehensive cache event for all discovered servers
	// This ensures the frontend can see comprehensive cache status during active operations
	serverNames := make([]string, 0, len(servers))
	serverStatus := make(map[string]interface{})

	for _, result := range results {
		serverNames = append(serverNames, result.ServerName)
		status := "ok"
		if result.Error != nil {
			status = "error"
		}
		serverStatus[result.ServerName] = map[string]interface{}{
			"status":      status,
			"tools_count": len(result.Tools),
			"error":       result.Error,
		}
	}

	// Log comprehensive cache event for debugging
	logger.Debug("Comprehensive cache event for active tool discovery",
		loggerv2.Int("servers_count", len(serverNames)),
		loggerv2.Any("servers", serverNames),
		loggerv2.Int("total_tools", len(results)))

	// Final summary logging
	successCount := 0
	errorCount := 0
	totalTools := 0
	for _, result := range results {
		if result.Error == nil {
			successCount++
			totalTools += len(result.Tools)
		} else {
			errorCount++
		}
	}

	logger.Info("FINAL SUMMARY",
		loggerv2.Int("total_servers", len(results)),
		loggerv2.Int("successful", successCount),
		loggerv2.Int("failed", errorCount),
		loggerv2.Int("total_tools", totalTools))

	// Note: To emit actual events, we would need to pass tracers to this function
	// For now, we log the information so it appears in the server logs

	return results
}

// checkPublicOnly refuses a public-only server that is not remote with a
// public https URL; nil for every other server.
func (c *Client) checkPublicOnly() error {
	if !c.config.PublicOnly {
		return nil
	}
	if p := c.config.GetProtocol(); p != ProtocolHTTP && p != ProtocolSSE {
		return fmt.Errorf("public-only MCP server must be remote (http or sse), not %s", p)
	}
	if err := netguard.CheckURL(c.config.URL, true); err != nil {
		return fmt.Errorf("MCP server URL refused: %w", err)
	}
	return nil
}

// discoveryClient is the client for OAuth discovery requests to the server:
// public-only for a user-supplied server.
func (c *Client) discoveryClient() *http.Client {
	if c.config.PublicOnly {
		return netguard.Client(30 * time.Second)
	}
	return http.DefaultClient
}

// setupOAuthAuth handles OAuth authentication for the client
func (c *Client) setupOAuthAuth(ctx context.Context) error {
	// Initialize OAuth manager if not already done
	if c.oauthManager == nil {
		c.oauthManager = oauth.NewManager(c.config.OAuth, c.logger)
	}

	// Auto-discover endpoints if enabled
	if c.config.OAuth.AutoDiscover && (c.config.OAuth.AuthURL == "" || c.config.OAuth.TokenURL == "") {
		c.logger.Info("Auto-discovering OAuth endpoints...")
		if err := c.discoverOAuthEndpoints(ctx); err != nil {
			c.logger.Warn("OAuth auto-discovery failed", loggerv2.Error(err))
			// Continue anyway - maybe endpoints are manually configured
		}
	}

	// Try to get access token
	token, err := c.oauthManager.GetAccessToken(ctx)
	if err != nil {
		// Token not available - need interactive auth
		c.logger.Info("OAuth token not available, authentication required")
		return fmt.Errorf("%w\nRun authentication flow to obtain token", err)
	}

	// Inject Bearer token into headers
	if c.config.Headers == nil {
		c.config.Headers = make(map[string]string)
	}
	c.config.Headers["Authorization"] = fmt.Sprintf("Bearer %s", token)

	c.logger.Debug("OAuth token injected into request headers")
	return nil
}

// discoverOAuthEndpoints auto-discovers OAuth endpoints from 401 response
func (c *Client) discoverOAuthEndpoints(ctx context.Context) error {
	// Make a test request to trigger 401 response
	resp, err := c.discoveryClient().Get(c.config.URL)
	if err != nil {
		return fmt.Errorf("failed to reach server for discovery: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		endpoints, err := oauth.DiscoverFromResponse(resp)
		if err != nil {
			return fmt.Errorf("failed to discover endpoints: %w", err)
		}

		// Update OAuth manager with discovered endpoints
		c.oauthManager.UpdateEndpoints(endpoints.AuthURL, endpoints.TokenURL)

		c.logger.Info("Auto-discovered OAuth endpoints",
			loggerv2.String("auth_url", endpoints.AuthURL),
			loggerv2.String("token_url", endpoints.TokenURL))

		return nil
	}

	return fmt.Errorf("server did not return 401 for discovery (got %d)", resp.StatusCode)
}

// DiscoverOAuthEndpoints discovers OAuth endpoints from the server and returns them
// This is the public wrapper for external use (e.g., UI)
func (c *Client) DiscoverOAuthEndpoints(ctx context.Context) (*oauth.OAuthEndpoints, error) {
	// Make a test request to trigger 401 response
	resp, err := c.discoveryClient().Get(c.config.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to reach server for discovery: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		endpoints, err := oauth.DiscoverFromResponse(resp)
		if err != nil {
			return nil, fmt.Errorf("failed to discover endpoints: %w", err)
		}

		c.logger.Info("Auto-discovered OAuth endpoints",
			loggerv2.String("auth_url", endpoints.AuthURL),
			loggerv2.String("token_url", endpoints.TokenURL))

		return endpoints, nil
	}

	return nil, fmt.Errorf("server did not return 401 for discovery (got %d)", resp.StatusCode)
}

// isNonRetryableConnectError reports errors that no amount of retrying can
// turn into a connection. A hosted OAuth connector whose user has never signed
// in (oauth.ErrNoValidToken) is the important one: the nested retry loops
// used to spend ~19s per agent start re-checking a token file that only an
// interactive login can create, and every workflow turn on a deployment with
// an unauthenticated connector paid that before the model was even launched.
// isNonRetryableConnectError reports a connect failure the server gave as an answer, not a network hiccup: no
// token, or a 4xx/401 on initialize. Retrying those only cost each chat start about 8 s per broken connection.
func isNonRetryableConnectError(err error) bool {
	return errors.Is(err, oauth.ErrNoValidToken) || errors.Is(err, transport.ErrLegacySSEServer) || errors.Is(err, transport.ErrUnauthorized)
}

// refusedConnectError names the server and, when it answered with an error, what it said (for example Slack: "App is
// not enabled for Slack MCP server access"), so the person sees the real reason instead of a transport error.
func (c *Client) refusedConnectError(err error) error {
	if reason := serverRefusal(c.config.URL); reason != "" && !errors.Is(err, oauth.ErrNoValidToken) {
		return fmt.Errorf("failed to connect to MCP server '%s': the server said: %s: %w", c.getServerName(), reason, err)
	}
	return fmt.Errorf("failed to connect to MCP server '%s': %w", c.getServerName(), err)
}
