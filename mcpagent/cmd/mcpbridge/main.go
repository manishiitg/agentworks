// mcpbridge is a stdio MCP server that bridges tool calls to HTTP API endpoints.
// It reads tool definitions from the file named by MCP_TOOLS_FILE and forwards each tool
// call to the appropriate per-tool HTTP endpoint with bearer token authentication.
//
// This binary is launched by Claude Code via --mcp-config as a subprocess.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/pkg/codingtimeout"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// bridgeLogf keeps externally sourced values on one physical log line.
func bridgeLogf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	line = strings.NewReplacer("\r", "\\r", "\n", "\\n", "\u2028", "\\u2028", "\u2029", "\\u2029").Replace(line)
	log.Print(line) // #nosec G706 -- Line separators are escaped above, preventing forged log records.
}

// makeReadyFileWriter, when MCP_READY_FILE is set, creates that file the first
// time the CLI completes its tools/list handshake — i.e. the instant the bridge
// tools become connected and callable. The parent adapter waits for this file
// before sending a cold session's first prompt, closing the race where the
// model's opening turn runs before its tools exist. Best-effort and idempotent:
// a failed write must never take down the bridge, and repeated tools/list calls
// only write once.
func makeReadyFileWriter(readyPath string) func() {
	var once sync.Once
	return func() {
		if strings.TrimSpace(readyPath) == "" {
			return
		}
		once.Do(func() {
			if dir := filepath.Dir(readyPath); dir != "" {
				//nolint:gosec // dir derives from a parent-controlled temp path.
				_ = os.MkdirAll(dir, 0o750)
			}
			if err := os.WriteFile(readyPath, []byte("ready\n"), 0o600); err != nil { // #nosec G703 -- Marker destination is selected by the parent process via its environment, never by MCP tool arguments.
				bridgeLogf("mcpbridge: failed to write MCP_READY_FILE %q: %v", readyPath, err)
				return
			}
			bridgeLogf("mcpbridge: wrote MCP readiness marker %q (tools connected)", readyPath)
		})
	}
}

// ToolDef is the serialized tool definition read from the file named by MCP_TOOLS_FILE.
type ToolDef struct {
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	InputSchema   json.RawMessage `json:"input_schema"`
	Server        string          `json:"server"` // MCP server name (empty for custom/virtual)
	Type          string          `json:"type"`   // "mcp", "custom", or "virtual"
	TimeoutMillis *int64          `json:"timeout_ms,omitempty"`
	// CallTimeoutsMillis (type "call" only) carries the parent's resolved
	// deadline for every tool call_tool may reach, keyed by tool name.
	CallTimeoutsMillis map[string]int64 `json:"call_timeouts_ms,omitempty"`
}

var callToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// callToolTarget resolves a call_tool request to the tool definition a direct
// call would use: the same per-tool route, session header and deadline. The
// server's session registry decides whether that tool exists and may run.
func callToolTarget(def ToolDef, args map[string]any) (ToolDef, map[string]any, error) {
	name, _ := args["tool_name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return ToolDef{}, nil, errors.New("tool_name is required: pass the exact name from search_tools or get_api_spec")
	}
	if name == def.Name {
		return ToolDef{}, nil, errors.New("call_tool cannot call itself")
	}
	server, _ := args["server_name"].(string)
	server = strings.TrimSpace(server)
	// Names become URL path segments; anything but a plain tool identifier is refused.
	if !callToolNamePattern.MatchString(name) || (server != "" && !callToolNamePattern.MatchString(server)) {
		return ToolDef{}, nil, fmt.Errorf("unknown tool %q: pass the exact name from search_tools or get_api_spec", name)
	}
	target := ToolDef{Name: name, Type: "custom"}
	if server != "" {
		target.Server = server
		target.Type = "mcp"
	} else if millis, ok := def.CallTimeoutsMillis[name]; ok {
		target.TimeoutMillis = &millis
	} else if app, _, namespaced := strings.Cut(name, "__"); namespaced && app != "" {
		// A connected-app tool's name starts with its app: notion__notion-fetch.
		target.Server = app
		target.Type = "mcp"
	}
	callArgs := map[string]any{}
	switch raw := args["arguments"].(type) {
	case nil:
	case map[string]any:
		callArgs = raw
	case string:
		if strings.TrimSpace(raw) != "" {
			if err := json.Unmarshal([]byte(raw), &callArgs); err != nil {
				return ToolDef{}, nil, fmt.Errorf("arguments must be a JSON object: %v", err)
			}
		}
	default:
		return ToolDef{}, nil, errors.New("arguments must be a JSON object")
	}
	return target, callArgs, nil
}

// The trusted parent owns direct-tool deadlines. Dynamic tools must not lose
// them at the CLI boundary; existing provider/backstop caps still apply.
func bridgeToolHTTPTimeout(def ToolDef, defaultTimeout, longRunningTimeout time.Duration) time.Duration {
	if def.Type != "custom" || def.TimeoutMillis == nil {
		return defaultTimeout
	}
	declared := time.Duration(*def.TimeoutMillis) * time.Millisecond
	if declared <= 0 || declared > longRunningTimeout {
		return longRunningTimeout
	}
	return declared
}

func truncateBridgeErrorText(s string) string {
	const maxBytes = 16 * 1024
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + fmt.Sprintf("\n... truncated %d bytes ...", len(s)-maxBytes)
}

func bridgeToolOutputDir() string {
	if configured := strings.TrimSpace(os.Getenv("MCP_TOOL_OUTPUT_DIR")); configured != "" {
		return configured
	}
	// Older parent processes do not set MCP_TOOL_OUTPUT_DIR. Prefer persistent
	// workflow paths already present in the coding-agent environment before
	// falling back to the bridge process's cwd.
	for _, key := range []string{"STEP_OUTPUT_DIR", "STEP_EXECUTION_DIR", "VAR_WORKSPACE_PATH"} {
		if dir := strings.TrimSpace(os.Getenv(key)); dir != "" {
			return filepath.Join(dir, "tool_output_folder")
		}
	}
	if cwd, err := os.Getwd(); err == nil && strings.TrimSpace(cwd) != "" {
		return filepath.Join(cwd, "tool_output_folder")
	}
	return ""
}

// bridgeMaxCall parses MCP_BRIDGE_MAX_CALL_SECONDS: a positive whole number of seconds, else 0 (no cap).
func bridgeMaxCall(raw string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func bridgeRequestError(toolType, toolName, sessionID string, timeout time.Duration, err error) string {
	layer := "mcpbridge_http"
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Sprintf("CANCELED: layer=%s type=%s tool=%s session=%s: %v", layer, toolType, toolName, sessionID, err)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("TIMEOUT: layer=%s type=%s tool=%s session=%s timeout=%s: %v. The call was stopped; run anything this slow in the background (nohup ... &) and poll its output instead of waiting on it.", layer, toolType, toolName, sessionID, timeout, err)
	default:
		return fmt.Sprintf("ERROR: layer=%s type=%s tool=%s session=%s: HTTP request failed: %v", layer, toolType, toolName, sessionID, err)
	}
}

func main() {
	// `mcpbridge secret NAME` prints one secret's current value for this chat (no MCP server).
	if len(os.Args) >= 2 && os.Args[1] == "secret" {
		os.Exit(runSecretCommand(os.Args[2:], os.Stdout, os.Stderr))
	}
	// If MCP_BRIDGE_LOG is set, tee all log output to that file in addition to stderr.
	// This lets the Go server capture mcpbridge startup/crash messages for debugging.
	if logPath := os.Getenv("MCP_BRIDGE_LOG"); logPath != "" {
		//nolint:gosec // MCP_BRIDGE_LOG is set by the parent process to a trusted log file path.
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			log.SetOutput(io.MultiWriter(os.Stderr, f))
		}
	}

	apiURL := os.Getenv("MCP_API_URL")
	apiToken := os.Getenv("MCP_API_TOKEN")
	// A launcher that must not write the token into a config file others can
	// read (Cursor's .cursor/mcp.json lives in the agent's working folder)
	// passes a private file holding it instead.
	if apiToken == "" {
		if path := strings.TrimSpace(os.Getenv("MCP_API_TOKEN_FILE")); path != "" {
			if raw, err := os.ReadFile(filepath.Clean(path)); err == nil { //nolint:gosec // path comes from the launcher's own env, not from a model or user
				apiToken = strings.TrimSpace(string(raw))
			}
		}
	}
	toolsFile := strings.TrimSpace(os.Getenv("MCP_TOOLS_FILE"))
	var toolsJSON string
	if toolsFile != "" {
		raw, err := os.ReadFile(filepath.Clean(toolsFile)) //nolint:gosec // path comes from the launcher's own env, not from a model or user
		if err != nil {
			log.Fatalf("bridge_start_failed: cannot read MCP_TOOLS_FILE %q: %v", toolsFile, err)
		}
		toolsJSON = string(raw)
	}
	virtualScopeID := os.Getenv("MCP_VIRTUAL_SCOPE_ID") // Per-agent scope for virtual tools (prevents parent/child overwrite)
	sessionID := os.Getenv("MCP_SESSION_ID")
	toolOutputDir := bridgeToolOutputDir()

	if apiURL == "" || apiToken == "" || toolsJSON == "" {
		log.Fatal("MCP_API_URL, MCP_API_TOKEN, and MCP_TOOLS_FILE are required")
	}

	var toolDefs []ToolDef
	if err := json.Unmarshal([]byte(toolsJSON), &toolDefs); err != nil {
		log.Fatalf("Failed to parse MCP_TOOLS_FILE %q: %v", toolsFile, err)
	}

	// Signal readiness the moment the CLI finishes its tools/list handshake, so
	// the parent adapter can hold a cold session's first prompt until the tools
	// are actually connected. tools/list is the precise "tools are now callable"
	// signal; initialize is used as an earlier belt-and-suspenders fallback.
	markReady := makeReadyFileWriter(os.Getenv("MCP_READY_FILE"))
	hooks := &server.Hooks{}
	hooks.AddAfterListTools(func(_ context.Context, _ any, _ *mcp.ListToolsRequest, _ *mcp.ListToolsResult) {
		markReady()
	})

	s := server.NewMCPServer("mcpbridge", "1.0.0",
		server.WithToolCapabilities(false),
		server.WithHooks(hooks),
	)

	defaultTimeout := codingtimeout.DefaultBridgeHTTPTimeout
	longRunningTimeout := codingtimeout.LongRunningMCPToolTimeout()
	// A CLI with its own tool-call limit (Muse: 300 s) must get an answer from the bridge first. When its limit
	// fires the CLI drops the whole MCP connection for good and every later call fails with "MCP stdio connection
	// is closed", so this cap makes a slow call end as an ordinary tool error instead.
	if limit := bridgeMaxCall(os.Getenv("MCP_BRIDGE_MAX_CALL_SECONDS")); limit > 0 {
		if limit < defaultTimeout {
			defaultTimeout = limit
		}
		if limit < longRunningTimeout {
			longRunningTimeout = limit
		}
	}

	for _, td := range toolDefs {
		def := td // capture loop variable

		// Use empty JSON object schema if none provided
		inputSchema := def.InputSchema
		if len(inputSchema) == 0 {
			inputSchema = json.RawMessage(`{"type":"object","properties":{}}`)
		}

		mcpTool := mcp.NewToolWithRawSchema(def.Name, def.Description, inputSchema)

		s.AddTool(mcpTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			def := def
			args := req.GetArguments()
			if def.Type == "call" {
				target, callArgs, err := callToolTarget(def, args)
				if err != nil {
					return mcp.NewToolResultText("ERROR: " + err.Error()), nil
				}
				bridgeLogf("mcpbridge: call_tool target type=%s server=%s tool=%s session=%s", target.Type, target.Server, target.Name, sessionID)
				def, args = target, callArgs
			}
			// Build endpoint URL based on tool type
			var url string
			switch def.Type {
			case "mcp":
				url = fmt.Sprintf("%s/tools/mcp/%s/%s", apiURL, neturl.PathEscape(def.Server), neturl.PathEscape(def.Name))
			case "virtual":
				url = fmt.Sprintf("%s/tools/virtual/%s", apiURL, neturl.PathEscape(def.Name))
			default:
				url = fmt.Sprintf("%s/tools/custom/%s", apiURL, neturl.PathEscape(def.Name))
			}

			// Marshal arguments
			argsJSON, err := json.Marshal(args)
			if err != nil {
				return mcp.NewToolResultText(fmt.Sprintf("ERROR: failed to marshal arguments: %v", err)), nil
			}
			diffBytes := -1
			filepathArg := ""
			if def.Name == "diff_patch_workspace_file" {
				if v, ok := args["diff"].(string); ok {
					diffBytes = len(v)
				}
				if v, ok := args["filepath"].(string); ok {
					filepathArg = v
				}
			}
			// Make HTTP POST request
			httpReq, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(argsJSON))) // #nosec G704 -- Destination comes from parent-configured MCP_API_URL and tool definitions, not tool-call arguments; local bridge endpoints are intentional.
			if err != nil {
				return mcp.NewToolResultText(fmt.Sprintf("ERROR: failed to create request: %v", err)), nil
			}
			httpReq.Header.Set("Authorization", "Bearer "+apiToken)
			httpReq.Header.Set("Content-Type", "application/json")
			if def.Type == "virtual" && virtualScopeID != "" {
				httpReq.Header.Set("X-Virtual-Scope-ID", virtualScopeID)
			}
			if sessionID != "" {
				httpReq.Header.Set("X-Session-ID", sessionID)
			}

			httpClient := &http.Client{Timeout: bridgeToolHTTPTimeout(def, defaultTimeout, longRunningTimeout)}

			started := time.Now()
			bridgeLogf("mcpbridge: tool call start type=%s tool=%s url=%s args_bytes=%d diff_bytes=%d filepath=%q session=%s timeout=%s", def.Type, def.Name, url, len(argsJSON), diffBytes, filepathArg, sessionID, httpClient.Timeout)
			resp, err := httpClient.Do(httpReq) // #nosec G704 -- Sends to the parent-configured bridge service; callers control the JSON body, not the destination.
			if err != nil {
				log.Printf("mcpbridge: tool call http error type=%s tool=%s duration=%s error=%v", def.Type, def.Name, time.Since(started), err)
				return mcp.NewToolResultText(bridgeRequestError(def.Type, def.Name, sessionID, httpClient.Timeout, err)), nil
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				bridgeLogf("mcpbridge: tool call read error type=%s tool=%s status=%d duration=%s error=%v", def.Type, def.Name, resp.StatusCode, time.Since(started), err)
				return mcp.NewToolResultText(fmt.Sprintf("ERROR: failed to read response: %v", err)), nil
			}
			bridgeLogf("mcpbridge: tool call response type=%s tool=%s status=%d duration=%s body_bytes=%d", def.Type, def.Name, resp.StatusCode, time.Since(started), len(body))

			if resp.StatusCode >= 400 {
				return mcp.NewToolResultText(fmt.Sprintf("ERROR: HTTP %d: %s", resp.StatusCode, truncateBridgeErrorText(string(body)))), nil
			}

			var result struct {
				Success bool               `json:"success"`
				Result  string             `json:"result"`
				Error   string             `json:"error"`
				Images  []mcp.ImageContent `json:"images"`
			}
			if err := json.Unmarshal(body, &result); err != nil {
				// If response isn't our expected format, return raw body
				return mcp.NewToolResultText(string(body)), nil
			}

			if !result.Success {
				// Return error as regular text (not IsError) so the LLM always sees the
				// actual error details. Some CLI providers (for example Claude Code) show
				// only a generic "MCP tool reported an error" when IsError=true, hiding
				// the actual error content from the LLM.
				errorMsg := result.Error
				if errorMsg == "" {
					errorMsg = "unknown error (no details in response)"
				}
				// result.Result is not an alternate success payload -- it is the
				// same tool output the caller would have received on success (for
				// execute_shell_command, the captured stdout/stderr/exit_code of
				// every command in a chain, even the ones that ran and produced
				// real output before a later command failed). Discarding it here
				// left only a bare "tool execution failed: exit_code=N" with no way
				// to see what actually happened short of a blind retry
				// (PUL-AAC278EF). Append it whenever present.
				if strings.TrimSpace(result.Result) != "" {
					return mcp.NewToolResultText(fmt.Sprintf("ERROR: %s\n\n%s", truncateBridgeErrorText(errorMsg), result.Result)), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("ERROR: %s", truncateBridgeErrorText(errorMsg))), nil
			}
			return bridgeResultWithImages(result.Result, result.Images, toolOutputDir), nil
		})
	}

	bridgeLogf("mcpbridge: starting with %d tools, API URL: %s, default_http_timeout=%s, long_running_http_timeout=%s", len(toolDefs), apiURL, defaultTimeout, longRunningTimeout)

	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("mcpbridge: stdio server error: %v", err)
	}
}
