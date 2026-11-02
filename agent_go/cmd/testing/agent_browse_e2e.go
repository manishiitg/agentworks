package testing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var agentBrowseE2EFlags struct {
	serverURL        string
	provider         string
	model            string
	sessionID        string
	selectedFolder   string
	targetURL        string
	cdpPort          int
	timeout          time.Duration
	skipCDPPreflight bool
}

var agentBrowseE2ECmd = &cobra.Command{
	Use:   "agent-browse-e2e",
	Short: "Run a Builder e2e test for a coding agent calling agent_browser",
	Long: `Runs a live Builder end-to-end regression test for agent_browser through a coding CLI provider.

This intentionally exercises the full Builder path:
1. POST /api/query with the selected coding CLI provider
2. chat-mode browser tool registration
3. coding CLI MCP bridge tool call
4. Builder workspace_browser:agent_browser executor
5. /api/sessions/{session_id}/events completion and tool-call evidence

Example:
  mcp-agent test agent-browse-e2e \
    --server-url http://127.0.0.1:18743 \
	--provider codex-cli \
    --model gemini-3.1-flash-lite \
    --target-url https://timesofindia.indiatimes.com/ \
    --cdp-port 9222`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), agentBrowseE2EFlags.timeout)
		defer cancel()

		provider := strings.TrimSpace(agentBrowseE2EFlags.provider)
		if provider == "" {
			provider = "codex-cli"
		}
		model := strings.TrimSpace(agentBrowseE2EFlags.model)
		if model == "" {
			model = defaultCodingAgentE2EModel(provider)
		}
		sessionID := strings.TrimSpace(agentBrowseE2EFlags.sessionID)
		if sessionID == "" {
			sessionID = fmt.Sprintf("agent-browse-e2e-%s-%d", strings.ReplaceAll(provider, "-", ""), time.Now().UnixNano())
		}

		client := &codingAgentChatE2EClient{
			baseURL: strings.TrimRight(agentBrowseE2EFlags.serverURL, "/"),
			token:   os.Getenv("AGENTWORKS_AUTH_TOKEN"),
			http:    &http.Client{Timeout: 30 * time.Second},
		}
		if err := client.ensureUserAuth(ctx); err != nil {
			return err
		}
		defer func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer stopCancel()
			_ = client.stopSession(stopCtx, sessionID)
		}()

		if !agentBrowseE2EFlags.skipCDPPreflight {
			if err := preflightAgentBrowserCDP(ctx, agentBrowseE2EFlags.cdpPort); err != nil {
				return err
			}
			fmt.Printf("PASS CDP preflight: Chrome DevTools and agent-browser are reachable on port %d\n", agentBrowseE2EFlags.cdpPort)
		}

		sentinel := "AGENT_BROWSER_E2E_OK_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		browserSession := "agent_browse_e2e_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		tabToken := strings.ReplaceAll(uuid.NewString(), "-", "")
		tabLabel := "e2e" + tabToken[:12]
		query := fmt.Sprintf(`This is a Builder agent_browser CDP-mode regression test.

Use the Builder browser MCP tool directly. Depending on the provider it may appear as mcp_api-bridge_agent_browser or mcp__api-bridge__agent_browser; both are the same Builder agent_browser tool.

Task:
1. Use agent_browser in CDP mode to create a new tab with this exact label: %s
2. Create that tab by calling command "tab". Begin args with the exact configured ["--cdp", "<endpoint>"] prefix from the system prompt, followed by ["new", "--label", "%s", "%s"]. Do not reorder these args.
3. Use agent_browser snapshot or get on that labeled tab to read the page.
4. Find one visible current news headline or page title from the site.

Do not call execute_shell_command. Do not run curl. Do not use raw CDP directly.

When done, reply with exactly two lines:
%s
headline: <the headline or title you found>

Use agent_browser session: %q`, tabLabel, tabLabel, agentBrowseE2EFlags.targetURL, sentinel, browserSession)

		fmt.Printf("Running agent_browser CDP e2e provider=%s model=%s session=%s cdp_port=%d\n", provider, model, sessionID, agentBrowseE2EFlags.cdpPort)
		queryID, err := client.startAgentBrowseQuery(ctx, sessionID, provider, model, query)
		if err != nil {
			return fmt.Errorf("agent_browser query failed to start: %w", err)
		}
		fmt.Printf("Started query=%s\n", queryID)

		final, raw, events, err := client.waitForAgentBrowseCompletion(ctx, sessionID, agentBrowseE2EFlags.timeout)
		if err != nil {
			return fmt.Errorf("agent_browser turn did not complete: %w", err)
		}
		if !eventsProveProvider(events, provider) {
			return fmt.Errorf("event stream did not prove provider %q was used", provider)
		}
		evidence := collectAgentBrowserEvidence(events, agentBrowseE2EFlags.targetURL, tabLabel)
		if evidence.shellCalls > 0 {
			return fmt.Errorf("event stream used execute_shell_command instead of direct agent_browser tool calls")
		}
		if evidence.agentBrowserCalls == 0 {
			return fmt.Errorf("event stream did not include an agent_browser tool call; final=%q", final)
		}
		if evidence.navigationCalls == 0 {
			return fmt.Errorf("event stream did not include agent_browser navigation to %s; commands=%v", agentBrowseE2EFlags.targetURL, evidence.commands)
		}
		if evidence.readCalls == 0 {
			return fmt.Errorf("event stream did not include agent_browser page read call after navigation; commands=%v", evidence.commands)
		}
		if containsAgentBrowserTimeout(raw) {
			return fmt.Errorf("event stream contains agent_browser timeout/failure evidence")
		}
		if !strings.Contains(final, sentinel) && !strings.Contains(raw, sentinel) {
			return fmt.Errorf("completion did not contain sentinel %q; final=%q", sentinel, final)
		}

		fmt.Println("PASS agent_browser e2e")
		return nil
	},
}

func init() {
	agentBrowseE2ECmd.Flags().StringVar(&agentBrowseE2EFlags.serverURL, "server-url", "http://127.0.0.1:18743", "coding-agent-loop server URL")
	agentBrowseE2ECmd.Flags().StringVar(&agentBrowseE2EFlags.provider, "provider", "codex-cli", "coding CLI provider; defaults to codex-cli")
	agentBrowseE2ECmd.Flags().StringVar(&agentBrowseE2EFlags.model, "model", "", "model ID; defaults to the provider-specific Builder coding-agent E2E model")
	agentBrowseE2ECmd.Flags().StringVar(&agentBrowseE2EFlags.sessionID, "session-id", "", "session ID to reuse; generated when omitted")
	agentBrowseE2ECmd.Flags().StringVar(&agentBrowseE2EFlags.selectedFolder, "selected-folder", "_users/default/Chats", "workspace-relative folder for the chat session")
	agentBrowseE2ECmd.Flags().StringVar(&agentBrowseE2EFlags.targetURL, "target-url", "https://timesofindia.indiatimes.com/", "URL to open with agent_browser CDP during the e2e")
	agentBrowseE2ECmd.Flags().IntVar(&agentBrowseE2EFlags.cdpPort, "cdp-port", 9222, "Chrome DevTools Protocol port to test")
	agentBrowseE2ECmd.Flags().DurationVar(&agentBrowseE2EFlags.timeout, "timeout", 5*time.Minute, "overall test timeout")
	agentBrowseE2ECmd.Flags().BoolVar(&agentBrowseE2EFlags.skipCDPPreflight, "skip-cdp-preflight", false, "skip local Chrome CDP and agent-browser smoke checks")
}

type agentBrowseQueryOptions struct {
	provider       string
	model          string
	query          string
	selectedFolder string
	cdpPort        int
}

func (c *codingAgentChatE2EClient) startAgentBrowseQuery(ctx context.Context, sessionID, provider, model, query string) (string, error) {
	return c.startAgentBrowseQueryWithOptions(ctx, sessionID, agentBrowseQueryOptions{
		provider:       provider,
		model:          model,
		query:          query,
		selectedFolder: agentBrowseE2EFlags.selectedFolder,
		cdpPort:        agentBrowseE2EFlags.cdpPort,
	})
}

func (c *codingAgentChatE2EClient) startAgentBrowseQueryWithOptions(ctx context.Context, sessionID string, opts agentBrowseQueryOptions) (string, error) {
	payload := map[string]interface{}{
		"query":    opts.query,
		"provider": opts.provider,
		"model_id": opts.model,
		"llm_config": map[string]interface{}{
			"primary": map[string]interface{}{
				"provider": opts.provider,
				"model_id": opts.model,
			},
		},
		"agent_mode":            "simple",
		"enabled_servers":       []string{"NO_SERVERS"},
		"selected_tools":        []string{"workspace_browser:agent_browser"},
		"selected_folder":       opts.selectedFolder,
		"max_turns":             -1,
		"enable_browser_access": true,
		"browser_mode":          "cdp",
	}
	if opts.cdpPort > 0 {
		payload["cdp_port"] = opts.cdpPort
	}

	var resp struct {
		QueryID string `json:"query_id"`
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/query", sessionID, payload, &resp); err != nil {
		return "", err
	}
	if resp.Status != "started" && resp.Status != "workflow_started" {
		return "", fmt.Errorf("unexpected query status %q message=%q", resp.Status, resp.Message)
	}
	if resp.QueryID == "" {
		return "", fmt.Errorf("server returned empty query_id")
	}
	return resp.QueryID, nil
}

func (c *codingAgentChatE2EClient) waitForAgentBrowseCompletion(ctx context.Context, sessionID string, timeout time.Duration) (string, string, []map[string]interface{}, error) {
	var rawBuilder strings.Builder
	var finalResult string
	var finalEvents []map[string]interface{}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, raw, err := c.getEvents(ctx, sessionID)
		rawBuilder.WriteString(raw)
		if err != nil {
			return finalResult, rawBuilder.String(), finalEvents, err
		}
		finalEvents = resp.Events
		if extracted := extractUnifiedCompletionFinal(resp.Events); extracted != "" {
			finalResult = extracted
		}
		switch resp.SessionStatus {
		case "completed":
			return finalResult, rawBuilder.String(), finalEvents, nil
		case "error", "stopped":
			return finalResult, rawBuilder.String(), finalEvents, fmt.Errorf("session ended with status %s; final=%q; browser_failure=%q", resp.SessionStatus, finalResult, summarizeAgentBrowserFailure(rawBuilder.String()))
		}
		if containsAgentBrowserTimeout(rawBuilder.String()) {
			return finalResult, rawBuilder.String(), finalEvents, fmt.Errorf("agent_browser failure observed before session completion: %q", summarizeAgentBrowserFailure(rawBuilder.String()))
		}
		if err := sleepContext(ctx, time.Second); err != nil {
			return finalResult, rawBuilder.String(), finalEvents, err
		}
	}
	return finalResult, rawBuilder.String(), finalEvents, fmt.Errorf("timed out waiting for session completion; browser_failure=%q", summarizeAgentBrowserFailure(rawBuilder.String()))
}

func preflightAgentBrowserCDP(ctx context.Context, port int) error {
	if port <= 0 {
		return fmt.Errorf("CDP preflight requires --cdp-port > 0")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	if _, err := getCDPJSON[map[string]interface{}](ctx, client, base+"/json/version"); err != nil {
		return fmt.Errorf("CDP preflight failed for /json/version on port %d: %w", port, err)
	}
	tabs, err := getCDPJSON[[]map[string]interface{}](ctx, client, base+"/json/list")
	if err != nil {
		return fmt.Errorf("CDP preflight failed for /json/list on port %d: %w", port, err)
	}
	if len(tabs) == 0 {
		return fmt.Errorf("CDP preflight found no browser tabs on port %d", port)
	}
	if err := preflightAgentBrowserCDPCLI(ctx, port); err != nil {
		return err
	}
	return nil
}

func preflightAgentBrowserCDPCLI(ctx context.Context, port int) error {
	version, versionErr := runAgentBrowserPreflightCommand(ctx, 5*time.Second, "--version")
	version = strings.TrimSpace(version)
	if versionErr != nil {
		return fmt.Errorf("agent-browser CDP preflight failed: could not run agent-browser --version: %w\noutput:\n%s", versionErr, version)
	}
	if version == "" {
		version = "unknown"
	}

	session := fmt.Sprintf("agent-browse-preflight-%d", time.Now().UnixNano())
	defer cleanupAgentBrowserPreflightSession(session)

	output, err := runAgentBrowserPreflightCommand(ctx, 15*time.Second,
		"--session", session,
		"--cdp", strconv.Itoa(port),
		"tab", "list",
		"--json",
	)
	output = strings.TrimSpace(output)
	if err != nil {
		return fmt.Errorf("agent-browser CDP preflight failed: Chrome /json/version and /json/list succeeded, but %s could not complete `tab list` against port %d within 15s: %w\noutput:\n%s", version, port, err, output)
	}
	if output == "" {
		return fmt.Errorf("agent-browser CDP preflight failed: %s returned empty output for `tab list` against port %d", version, port)
	}
	return nil
}

func runAgentBrowserPreflightCommand(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "agent-browser", args...)
	setPreflightProcessGroup(cmd)

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Start(); err != nil {
		return output.String(), err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		return output.String(), err
	case <-runCtx.Done():
		killAgentBrowserPreflightProcessGroup(cmd)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		return output.String(), fmt.Errorf("timed out after %s", timeout)
	}
}

func killAgentBrowserPreflightProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if killPreflightProcessGroup(cmd.Process.Pid) == nil {
		return
	}
	_ = cmd.Process.Kill()
}

func cleanupAgentBrowserPreflightSession(session string) {
	homeDir, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(session) == "" {
		return
	}
	stateDir := filepath.Join(homeDir, ".agent-browser")
	pidPath := filepath.Join(stateDir, session+".pid")
	if raw, readErr := os.ReadFile(pidPath); readErr == nil {
		if pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw))); convErr == nil && pid > 0 {
			if p, findErr := os.FindProcess(pid); findErr == nil {
				_ = p.Kill()
			}
		}
	}
	for _, suffix := range []string{".pid", ".sock", ".stream", ".version", ".engine", ".log", ".chrome-pid"} {
		_ = os.Remove(filepath.Join(stateDir, session+suffix))
	}
}

func getCDPJSON[T any](ctx context.Context, client *http.Client, endpoint string) (T, error) {
	var zero T
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return zero, err
	}
	req.Header.Set("Host", "localhost")
	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return zero, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, fmt.Errorf("GET %s returned %s: %s", endpoint, resp.Status, string(body))
	}
	var parsed T
	if err := json.Unmarshal(body, &parsed); err != nil {
		return zero, fmt.Errorf("decode %s: %w; raw=%s", endpoint, err, string(body))
	}
	return parsed, nil
}

func containsAgentBrowserTimeout(raw string) bool {
	lower := strings.ToLower(raw)
	return strings.Contains(lower, "agent_browser") && (strings.Contains(lower, "command timed out") ||
		strings.Contains(lower, "context deadline exceeded") ||
		strings.Contains(lower, "context canceled") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "invalid cdp url") ||
		strings.Contains(lower, "empty host") ||
		strings.Contains(lower, "auto-launch failed"))
}

type agentBrowserEvidence struct {
	agentBrowserCalls int
	navigationCalls   int
	readCalls         int
	shellCalls        int
	commandNames      []string
	commands          []string
}

func (e agentBrowserEvidence) hasCommand(command string) bool {
	command = strings.ToLower(strings.TrimSpace(command))
	for _, candidate := range e.commandNames {
		if candidate == command {
			return true
		}
	}
	return false
}

type agentBrowserToolArgs struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Session string   `json:"session"`
}

func collectAgentBrowserEvidence(events []map[string]interface{}, targetURL, tabLabel string) agentBrowserEvidence {
	var evidence agentBrowserEvidence
	for _, event := range events {
		if fmt.Sprint(event["type"]) != "tool_call_start" {
			continue
		}
		data := nestedEventData(event)
		toolName := fmt.Sprint(data["tool_name"])
		if toolName == "execute_shell_command" {
			evidence.shellCalls++
			continue
		}
		if toolName != "agent_browser" {
			continue
		}
		evidence.agentBrowserCalls++

		toolArgs, ok := parseAgentBrowserToolArgs(data)
		if !ok {
			evidence.commands = append(evidence.commands, "agent_browser:<unparsed>")
			continue
		}
		command := strings.ToLower(strings.TrimSpace(toolArgs.Command))
		evidence.commandNames = append(evidence.commandNames, command)
		evidence.commands = append(evidence.commands, command+" "+strings.Join(toolArgs.Args, " "))
		if isAgentBrowserNavigationCall(command, toolArgs.Args, targetURL, tabLabel) {
			evidence.navigationCalls++
		}
		if isAgentBrowserReadCall(command) {
			evidence.readCalls++
		}
	}
	return evidence
}

func nestedEventData(event map[string]interface{}) map[string]interface{} {
	outer, _ := event["data"].(map[string]interface{})
	inner, _ := outer["data"].(map[string]interface{})
	return inner
}

func parseAgentBrowserToolArgs(data map[string]interface{}) (agentBrowserToolArgs, bool) {
	params, _ := data["tool_params"].(map[string]interface{})
	rawArgs, _ := params["arguments"].(string)
	if rawArgs == "" {
		return agentBrowserToolArgs{}, false
	}
	var parsed agentBrowserToolArgs
	if err := json.Unmarshal([]byte(rawArgs), &parsed); err != nil {
		return agentBrowserToolArgs{}, false
	}
	return parsed, true
}

func isAgentBrowserNavigationCall(command string, args []string, targetURL, tabLabel string) bool {
	switch command {
	case "open", "goto", "navigate":
		return argsContainURL(args, targetURL)
	case "tab":
		return argsContain(args, "new") && argsContainURL(args, targetURL)
	default:
		return false
	}
}

func isAgentBrowserReadCall(command string) bool {
	switch command {
	case "snapshot", "get", "eval":
		return true
	default:
		return false
	}
}

func argsContain(args []string, needle string) bool {
	for _, arg := range args {
		if arg == needle {
			return true
		}
	}
	return false
}

func argsContainURL(args []string, targetURL string) bool {
	normalizedTarget := strings.TrimRight(targetURL, "/")
	for _, arg := range args {
		normalizedArg := strings.TrimRight(arg, "/")
		if normalizedArg == normalizedTarget {
			return true
		}
	}
	return false
}

func summarizeAgentBrowserFailure(raw string) string {
	if raw == "" {
		return ""
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	var summary string
	for {
		var resp struct {
			Events []map[string]interface{} `json:"events"`
		}
		if err := decoder.Decode(&resp); err != nil {
			break
		}
		for _, event := range resp.Events {
			if fmt.Sprint(event["type"]) != "tool_call_end" {
				continue
			}
			data := nestedEventData(event)
			if fmt.Sprint(data["tool_name"]) != "agent_browser" {
				continue
			}
			result := fmt.Sprint(data["result"])
			lowerResult := strings.ToLower(result)
			if strings.Contains(lowerResult, "command timed out") ||
				strings.Contains(lowerResult, "custom tool execution failed") ||
				strings.Contains(lowerResult, "context deadline exceeded") ||
				strings.Contains(lowerResult, "context canceled") ||
				strings.Contains(lowerResult, "connection refused") ||
				strings.Contains(lowerResult, "invalid cdp url") ||
				strings.Contains(lowerResult, "empty host") ||
				strings.Contains(lowerResult, "auto-launch failed") {
				summary = result
			}
		}
	}
	if summary != "" {
		return summary
	}
	lower := strings.ToLower(raw)
	for _, needle := range []string{
		"command timed out",
		"custom tool execution failed",
		"context canceled",
		"connection refused",
		"agent_browser",
	} {
		idx := strings.Index(lower, needle)
		if idx < 0 {
			continue
		}
		start := idx - 240
		if start < 0 {
			start = 0
		}
		end := idx + 500
		if end > len(raw) {
			end = len(raw)
		}
		return raw[start:end]
	}
	if len(raw) > 700 {
		return raw[len(raw)-700:]
	}
	return raw
}
