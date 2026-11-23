package mcpclient

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// StdioManager provides stdio connection management with direct connection creation
// Each agent owns its connections directly - no global pool
type StdioManager struct {
	command    string
	args       []string
	env        []string
	workingDir string
	logger     loggerv2.Logger
	serverKey  string
}

// NewStdioManager creates a new stdio manager.
// workingDir is optional; when set, the MCP subprocess is started with this as its current
// working directory so relative paths resolve there.
func NewStdioManager(command string, args []string, env []string, workingDir string, logger loggerv2.Logger) *StdioManager {
	logger.Debug("Creating StdioManager",
		loggerv2.String("command", command),
		loggerv2.Any("args", args))

	// Create server key for this configuration (used for logging/identification)
	envHash := hashEnvVars(env)
	serverKey := fmt.Sprintf("%s_%v_%s", command, args, envHash)

	return &StdioManager{
		command:    command,
		args:       args,
		env:        env,
		workingDir: workingDir,
		logger:     logger,
		serverKey:  serverKey,
	}
}

// CreateClient creates a new stdio client with direct connection
// This is the standard approach - each agent creates and owns its own connections
func (s *StdioManager) CreateClient() (*client.Client, error) {
	s.logger.Debug("Creating stdio client directly (no pooling)")

	var mcpClient *client.Client
	var err error
	if s.workingDir != "" {
		opts := []transport.StdioOption{
			transport.WithCommandFunc(func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
				cmd := exec.CommandContext(ctx, command, args...) // #nosec G204 -- Runs the explicitly configured MCP executable with separate argv, not a shell or tool-supplied command.
				cmd.Env = env
				cmd.Dir = s.workingDir
				return cmd, nil
			}),
		}
		mcpClient, err = client.NewStdioMCPClientWithOptions(s.command, s.env, s.args, opts...)
	} else {
		mcpClient, err = client.NewStdioMCPClient(s.command, s.env, s.args...)
	}
	if err != nil {
		s.logger.Error("Failed to create stdio client", err)
		return nil, fmt.Errorf("failed to create stdio client: %w", err)
	}
	s.logger.Debug("Stdio client created successfully")

	return mcpClient, nil
}

// Connect creates and initializes a stdio client with full MCP handshake
// Each call creates a new connection - the caller owns and manages the connection lifecycle
func (s *StdioManager) Connect(ctx context.Context) (*client.Client, error) {
	s.logger.Debug("Starting stdio connection process (direct creation)")

	startTime := time.Now()
	s.logger.Info(fmt.Sprintf("🚀 [MCP INIT] Starting new stdio connection creation - server=%s, command=%s, args=%v", s.serverKey, s.command, s.args),
		loggerv2.String("server", s.serverKey),
		loggerv2.String("command", s.command),
		loggerv2.Any("args", s.args))

	// Debug: Log environment variables (but mask sensitive values)
	envCount := len(s.env)
	envPreview := make([]string, 0)
	for _, e := range s.env {
		if idx := strings.IndexByte(e, '='); idx > 0 {
			key := e[:idx]
			if strings.Contains(key, "SERVICE_ACCOUNT") || strings.Contains(key, "DRIVE_FOLDER") {
				envPreview = append(envPreview, e)
			}
			if len(envPreview) < 10 {
				if strings.Contains(strings.ToLower(key), "secret") || strings.Contains(strings.ToLower(key), "password") || strings.Contains(strings.ToLower(key), "key") {
					envPreview = append(envPreview, fmt.Sprintf("%s=***", key))
				} else {
					envPreview = append(envPreview, e)
				}
			}
		}
	}
	if envCount > 5 {
		envPreview = append(envPreview, fmt.Sprintf("... and %d more", envCount-5))
	}
	s.logger.Debug("Environment variables",
		loggerv2.Int("total", envCount),
		loggerv2.Any("preview", envPreview))

	// Create the MCP client
	s.logger.Info(fmt.Sprintf("🔍 [MCP INIT] Step 1/2: Creating stdio MCP client - server=%s, command=%s", s.serverKey, s.command),
		loggerv2.String("server", s.serverKey))
	clientStartTime := time.Now()
	var mcpClient *client.Client
	var err error
	if s.workingDir != "" {
		opts := []transport.StdioOption{
			transport.WithCommandFunc(func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
				cmd := exec.CommandContext(ctx, command, args...) // #nosec G204 -- Runs the explicitly configured MCP executable with separate argv, not a shell or tool-supplied command.
				cmd.Env = env
				cmd.Dir = s.workingDir
				return cmd, nil
			}),
		}
		mcpClient, err = client.NewStdioMCPClientWithOptions(s.command, s.env, s.args, opts...)
	} else {
		mcpClient, err = client.NewStdioMCPClient(s.command, s.env, s.args...)
	}

	if err != nil {
		clientDuration := time.Since(clientStartTime)
		s.logger.Error(fmt.Sprintf("❌ [MCP INIT] Failed to create stdio client - server=%s, duration=%v", s.serverKey, clientDuration), err,
			loggerv2.String("duration", clientDuration.String()))
		return nil, fmt.Errorf("failed to create stdio client: %w", err)
	}
	clientDuration := time.Since(clientStartTime)
	s.logger.Info(fmt.Sprintf("✅ [MCP INIT] Stdio MCP client created successfully - server=%s, duration=%v", s.serverKey, clientDuration.Round(time.Millisecond)),
		loggerv2.String("duration", clientDuration.Round(time.Millisecond).String()))

	// Initialize the connection with timeout
	initTimeout := 10 * time.Minute
	initCtx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()

	// Channel to signal fatal errors detected from stderr
	fatalErrorChan := make(chan error, 1)

	// Capture stderr from the subprocess for logging and error detection
	stderrReader, hasStderr := client.GetStderr(mcpClient)
	if hasStderr && stderrReader != nil {
		s.logger.Info(fmt.Sprintf("📋 [MCP INIT] Capturing stderr from subprocess - server=%s", s.serverKey),
			loggerv2.String("server", s.serverKey))
		go s.captureStderr(stderrReader, fatalErrorChan)
	} else {
		s.logger.Debug(fmt.Sprintf("⚠️ [MCP INIT] No stderr reader available - server=%s", s.serverKey),
			loggerv2.String("server", s.serverKey))
	}

	// Start a goroutine to log progress during initialization
	progressDone := make(chan bool, 1)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		initStartTime := time.Now()
		for {
			select {
			case <-ticker.C:
				elapsed := time.Since(initStartTime)
				remaining := initTimeout - elapsed
				if remaining > 0 {
					s.logger.Info(fmt.Sprintf("⏳ [MCP INIT] Still initializing connection - server=%s, elapsed=%v, remaining=%v", s.serverKey, elapsed.Round(time.Second), remaining.Round(time.Second)),
						loggerv2.String("server", s.serverKey),
						loggerv2.String("elapsed", elapsed.Round(time.Second).String()),
						loggerv2.String("remaining", remaining.Round(time.Second).String()))
				} else {
					s.logger.Warn(fmt.Sprintf("⚠️ [MCP INIT] Initialization has exceeded timeout - server=%s, timeout=%v", s.serverKey, initTimeout),
						loggerv2.String("server", s.serverKey),
						loggerv2.String("timeout", initTimeout.String()))
				}
			case <-initCtx.Done():
				return
			case <-progressDone:
				return
			}
		}
	}()

	// Initialize the connection with early failure detection
	s.logger.Info(fmt.Sprintf("🔍 [MCP INIT] Step 2/2: About to initialize MCP connection - server=%s, timeout=%v", s.serverKey, initTimeout),
		loggerv2.String("server", s.serverKey),
		loggerv2.String("timeout", initTimeout.String()))
	initStartTime := time.Now()
	s.logger.Info(fmt.Sprintf("🔍 [MCP INIT] Calling mcpClient.Initialize() - server=%s", s.serverKey),
		loggerv2.String("server", s.serverKey))

	// Create a channel for initialization result
	initResultChan := make(chan struct {
		result *mcp.InitializeResult
		err    error
	}, 1)

	// Run Initialize in a goroutine so we can monitor for fatal errors
	go func() {
		initResult, err := mcpClient.Initialize(initCtx, mcp.InitializeRequest{
			Params: mcp.InitializeParams{
				ProtocolVersion: "2024-11-05",
				Capabilities:    mcp.ClientCapabilities{},
				ClientInfo: mcp.Implementation{
					Name:    "mcp-agent-go",
					Version: "1.0.0",
				},
			},
		})
		select {
		case initResultChan <- struct {
			result *mcp.InitializeResult
			err    error
		}{result: initResult, err: err}:
		case <-initCtx.Done():
			// Context cancelled, don't send result
		}
	}()

	// Wait for either initialization result or fatal error from stderr
	var initResult *mcp.InitializeResult
	select {
	case result := <-initResultChan:
		initResult = result.result
		err = result.err
	case fatalErr := <-fatalErrorChan:
		// Fatal error detected from stderr - cancel initialization and fail fast
		cancel()
		initDuration := time.Since(initStartTime)
		progressDone <- true
		_ = mcpClient.Close()
		totalDuration := time.Since(startTime)
		s.logger.Error(fmt.Sprintf("❌ [MCP INIT] Fatal error detected from stderr - server=%s, init_duration=%v, total_duration=%v", s.serverKey, initDuration, totalDuration), fatalErr,
			loggerv2.String("server", s.serverKey),
			loggerv2.String("init_duration", initDuration.String()),
			loggerv2.String("total_duration", totalDuration.String()))
		return nil, fmt.Errorf("MCP server failed to start for %s: %w", s.serverKey, fatalErr)
	case <-initCtx.Done():
		// Context timeout or cancellation
		err = initCtx.Err()
		// Try to get the result if it's available (non-blocking)
		select {
		case result := <-initResultChan:
			initResult = result.result
			if err == nil {
				err = result.err
			}
		default:
			// No result available
		}
	}

	initDuration := time.Since(initStartTime)
	progressDone <- true

	s.logger.Info(fmt.Sprintf("🔍 [MCP INIT] mcpClient.Initialize() returned - server=%s, duration=%v, error=%v", s.serverKey, initDuration, err != nil),
		loggerv2.String("server", s.serverKey))

	if err != nil {
		_ = mcpClient.Close()
		totalDuration := time.Since(startTime)

		// Check if it was a timeout
		if initCtx.Err() == context.DeadlineExceeded {
			s.logger.Error(fmt.Sprintf("❌ [MCP INIT] Initialization timed out - server=%s, init_duration=%v, total_duration=%v", s.serverKey, initDuration, totalDuration), err,
				loggerv2.String("server", s.serverKey),
				loggerv2.String("init_duration", initDuration.String()),
				loggerv2.String("total_duration", totalDuration.String()))
			return nil, fmt.Errorf("failed to initialize MCP connection for %s: timed out after %v: %w",
				s.serverKey, initTimeout, err)
		}

		s.logger.Error(fmt.Sprintf("❌ [MCP INIT] Failed to initialize MCP connection - server=%s, init_duration=%v, total_duration=%v", s.serverKey, initDuration, totalDuration), err,
			loggerv2.String("server", s.serverKey),
			loggerv2.String("init_duration", initDuration.String()),
			loggerv2.String("total_duration", totalDuration.String()))
		return nil, fmt.Errorf("failed to initialize MCP connection: %w", err)
	}

	totalDuration := time.Since(startTime)
	s.logger.Info(fmt.Sprintf("✅ [MCP INIT] Connection initialized successfully - server=%s, init_time=%v, total_time=%v", s.serverKey, initDuration.Round(time.Millisecond), totalDuration.Round(time.Millisecond)),
		loggerv2.String("server", s.serverKey),
		loggerv2.String("init_time", initDuration.Round(time.Millisecond).String()),
		loggerv2.String("total_time", totalDuration.Round(time.Millisecond).String()))
	s.logger.Debug("Server info", loggerv2.Any("server_info", initResult.ServerInfo))

	s.logger.Debug("Stdio connection obtained successfully")
	return mcpClient, nil
}

// captureStderr reads from the stderr reader, logs each line, and detects fatal errors
func (s *StdioManager) captureStderr(stderrReader io.Reader, fatalErrorChan chan<- error) {
	scanner := bufio.NewScanner(stderrReader)
	buffer := make([]byte, 0, 64*1024) // 64KB buffer for long lines
	scanner.Buffer(buffer, 1024*1024)  // Allow up to 1MB lines

	fatalErrorSent := false

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) != "" {
			s.logger.Info(fmt.Sprintf("📋 [MCP STDERR] %s: %s", s.serverKey, line),
				loggerv2.String("server", s.serverKey),
				loggerv2.String("stderr_line", line))

			// Detect fatal errors and signal early failure
			if !fatalErrorSent {
				fatalErr := s.detectFatalError(line)
				if fatalErr != nil {
					fatalErrorSent = true
					// Try to send fatal error (non-blocking)
					select {
					case fatalErrorChan <- fatalErr:
						s.logger.Error(fmt.Sprintf("🚨 [MCP STDERR] Fatal error detected, failing fast - server=%s", s.serverKey), fatalErr,
							loggerv2.String("server", s.serverKey))
					default:
						// Channel already has an error or is closed, skip
					}
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, io.EOF) {
			s.logger.Warn(fmt.Sprintf("⚠️ [MCP STDERR] Error reading stderr - server=%s: %v", s.serverKey, err),
				loggerv2.String("server", s.serverKey),
				loggerv2.Error(err))
		}
	} else {
		s.logger.Debug(fmt.Sprintf("📋 [MCP STDERR] Stderr stream closed - server=%s", s.serverKey),
			loggerv2.String("server", s.serverKey))
	}
}

// detectFatalError checks if a stderr line indicates a fatal error that should cause early failure
func (s *StdioManager) detectFatalError(line string) error {
	lineLower := strings.ToLower(line)

	// Check for Node.js version mismatch errors
	if strings.Contains(lineLower, "npm") && strings.Contains(lineLower, "is known not to run on node.js") {
		return fmt.Errorf("Node.js version mismatch detected: %s", strings.TrimSpace(line))
	}

	// Check for syntax errors (usually fatal)
	if strings.Contains(lineLower, "syntaxerror") {
		return fmt.Errorf("syntax error detected: %s", strings.TrimSpace(line))
	}

	// Check for other critical errors
	if strings.Contains(lineLower, "error:") {
		if strings.Contains(lineLower, "cannot") ||
			strings.Contains(lineLower, "failed") ||
			strings.Contains(lineLower, "unable") ||
			strings.Contains(lineLower, "not found") ||
			strings.Contains(lineLower, "permission denied") {
			return fmt.Errorf("critical error detected: %s", strings.TrimSpace(line))
		}
	}

	// Check for process exit errors
	if strings.Contains(lineLower, "process exited") || strings.Contains(lineLower, "exited with code") {
		return fmt.Errorf("process exited unexpectedly: %s", strings.TrimSpace(line))
	}

	return nil
}

// GetServerKey returns the server key for this manager (useful for logging/debugging)
func (s *StdioManager) GetServerKey() string {
	return s.serverKey
}

// hashEnvVars creates a deterministic hash of environment variables
// This ensures that connections with different env vars get different server keys
func hashEnvVars(env []string) string {
	if len(env) == 0 {
		return "noenv"
	}

	// Sort env vars to ensure deterministic hash
	sorted := make([]string, len(env))
	copy(sorted, env)
	sort.Strings(sorted)

	// Create hash of sorted env vars
	hasher := sha256.New()
	for _, e := range sorted {
		hasher.Write([]byte(e))
		hasher.Write([]byte("\n"))
	}
	hash := hasher.Sum(nil)

	// Return first 16 characters of hex hash (enough for uniqueness)
	return fmt.Sprintf("%x", hash)[:16]
}
