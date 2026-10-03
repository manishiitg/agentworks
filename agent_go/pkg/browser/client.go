package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Client handles communication with the workspace API for browser automation
type Client struct {
	WorkspaceAPIURL string
	HTTPClient      *http.Client
}

// NewClient creates a new browser client
func NewClient(workspaceAPIURL string) *Client {
	return &Client{
		WorkspaceAPIURL: strings.TrimRight(workspaceAPIURL, "/"),
		HTTPClient:      &http.Client{},
	}
}

// CheckCDP asks the host workspace API whether Chrome DevTools is reachable on
// a configured local port. Agents must use this path instead of probing
// /json/version directly so browser safety and topology stay backend-owned.
func (c *Client) CheckCDP(ctx context.Context, port int) (bool, string, error) {
	if c == nil {
		return false, "", fmt.Errorf("browser client is nil")
	}
	if port < 1 || port > 65535 {
		return false, "", fmt.Errorf("invalid CDP port %d", port)
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	endpoint := c.WorkspaceAPIURL + "/api/cdp-check?port=" + url.QueryEscape(strconv.Itoa(port))
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, "", err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("CDP status check returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Connected bool   `json:"connected"`
		Error     string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return false, "", err
	}
	return payload.Connected, strings.TrimSpace(payload.Error), nil
}

// ExecuteOptions contains optional configuration for command execution
type ExecuteOptions struct {
	Timeout          time.Duration
	UserID           string // Trusted caller identity for workspace account-slot checks.
	FolderGuard      *FolderGuardConfig
	WorkingDirectory string // Working directory for command execution (relative to workspace root)
	ArtifactTransfer *ArtifactTransfer
	UploadTransfers  []UploadTransfer
}

// ExecuteCommand executes an agent-browser command via the workspace API
func (c *Client) ExecuteCommand(ctx context.Context, args []string, opts *ExecuteOptions) (string, error) {
	timeout := 60 * time.Second
	if opts != nil && opts.Timeout > 0 {
		timeout = opts.Timeout
	}

	// Build the shell command: agent-browser [args...] with proper quoting
	quotedArgs := make([]string, len(args))
	for i, arg := range args {
		quotedArgs[i] = quoteShellArg(arg)
	}
	fullCommand := "agent-browser " + strings.Join(quotedArgs, " ")

	// If the command references host.docker.internal, wrap with IP resolution.
	// Chrome rejects HTTP requests with non-IP/non-localhost Host headers, so we
	// resolve host.docker.internal to its IP at runtime inside the Docker container.
	if strings.Contains(fullCommand, "host.docker.internal") {
		fullCommand = `HOST_IP=$(getent hosts host.docker.internal 2>/dev/null | awk '{print $1; exit}'); if [ -z "$HOST_IP" ]; then HOST_IP=localhost; fi; ` +
			strings.ReplaceAll(fullCommand, "host.docker.internal", "${HOST_IP}")
	}
	return c.executeWorkspaceCommand(ctx, fullCommand, timeout, opts)
}

// executeWorkspaceCommand invokes the workspace shell endpoint. Browser
// commands normally arrive through ExecuteCommand; transfer-only requests use
// a plain no-op command so a completed recording can be finalized without
// accidentally invoking a fictitious `agent-browser true` subcommand.
func (c *Client) executeWorkspaceCommand(ctx context.Context, fullCommand string, timeout time.Duration, opts *ExecuteOptions) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Prepare request body
	workingDir := "."
	if opts != nil && opts.WorkingDirectory != "" {
		workingDir = opts.WorkingDirectory
	}
	reqBody := ShellExecuteRequest{
		Command:          fullCommand,
		WorkingDirectory: workingDir,
		Timeout:          int(timeout.Seconds()),
	}

	if opts != nil && opts.FolderGuard != nil {
		reqBody.FolderGuard = opts.FolderGuard
	}
	if opts != nil && opts.ArtifactTransfer != nil {
		reqBody.ArtifactTransfer = opts.ArtifactTransfer
	}
	if opts != nil && len(opts.UploadTransfers) > 0 {
		reqBody.UploadTransfers = append([]UploadTransfer(nil), opts.UploadTransfers...)
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make HTTP request to workspace-api
	apiURL := c.WorkspaceAPIURL + "/api/execute"
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if opts != nil && opts.UserID != "" {
		req.Header.Set("X-User-ID", opts.UserID)
	}
	if token := strings.TrimSpace(os.Getenv("WORKSPACE_API_TOKEN")); token != "" {
		req.Header.Set("X-Workspace-Token", token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("command timed out after %v", timeout)
		}
		return "", fmt.Errorf("failed to execute command via workspace-api: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// Parse response
	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w (body: %s)", err, string(body))
	}

	// Check for errors
	if !apiResp.Success {
		errMsg := apiResp.Message
		if apiResp.Error != "" {
			errMsg = apiResp.Error
		}
		stderr := strings.TrimSpace(cleanBrowserShellStderr(apiResp.Data.Stderr))
		stdout := strings.TrimSpace(apiResp.Data.Stdout)
		if stdout != "" {
			errMsg += "\nOutput: " + stdout
		}
		if stderr != "" {
			errMsg += "\nStderr: " + stderr
		}
		return "", fmt.Errorf("command failed: %s", errMsg)
	}

	// Check exit code
	if apiResp.Data.ExitCode != 0 {
		stdout := strings.TrimSpace(apiResp.Data.Stdout)
		stderr := strings.TrimSpace(cleanBrowserShellStderr(apiResp.Data.Stderr))
		output := stdout
		if output == "" {
			output = stderr
		} else if stderr != "" {
			output += "\nStderr: " + stderr
		}
		return "", fmt.Errorf("command exited with code %d: %s", apiResp.Data.ExitCode, strings.TrimSpace(output))
	}

	// Return stdout (which contains JSON output from agent-browser)
	return apiResp.Data.Stdout, nil
}

// FinalizeArtifact performs only the trusted workspace-side transfer. It is
// used when an artifact spans commands, such as record start -> record stop.
func (c *Client) FinalizeArtifact(ctx context.Context, transfer *ArtifactTransfer, opts *ExecuteOptions) error {
	if transfer == nil {
		return fmt.Errorf("artifact transfer is required")
	}
	cloned := &ExecuteOptions{}
	if opts != nil {
		*cloned = *opts
	}
	finalized := *transfer
	finalized.Finalize = true
	cloned.ArtifactTransfer = &finalized
	timeout := 60 * time.Second
	if cloned.Timeout > 0 {
		timeout = cloned.Timeout
	}
	_, err := c.executeWorkspaceCommand(ctx, "true", timeout, cloned)
	return err
}

func cleanBrowserShellStderr(stderr string) string {
	lines := strings.Split(stderr, "\n")
	kept := lines[:0]
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "getcwd: cannot access parent directories: Operation not permitted") {
			continue
		}
		if trimmed != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// quoteShellArg quotes a shell argument if it contains special characters
func quoteShellArg(arg string) string {
	// If arg contains spaces, parentheses, or other special shell chars, quote it
	if strings.ContainsAny(arg, " \t\n()[]{}|&;'\"<>*?!`$#\\~") {
		// Use single quotes and escape any single quotes within
		// Replace ' with '"'"' (which is ' then " then ' then " then ')
		escaped := strings.ReplaceAll(arg, "\x27", "\x27\x22\x27\x22\x27")
		return "\x27" + escaped + "\x27"
	}
	return arg
}
