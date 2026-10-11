package testing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/agent/codeexec"
	mcpExecutor "github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/spf13/cobra"
)

var agentBrowseAPIStressE2EFlags struct {
	workspaceAPIURL  string
	sessionPrefix    string
	cdpPort          int
	workers          int
	timeout          time.Duration
	requestTimeout   time.Duration
	skipCDPPreflight bool
}

var agentBrowseAPIStressE2ECmd = &cobra.Command{
	Use:   "agent-browse-api-stress-e2e",
	Short: "Stress test agent_browser via the mcpbridge-compatible HTTP API",
	Long: `Runs a direct shared-CDP stress test without a coding CLI provider.

The test starts an in-process Builder-style per-tool HTTP API, registers
session-scoped workspace_browser:agent_browser executors, then sends concurrent
requests to:

  /s/{session_id}/tools/mcp/workspace_browser/agent_browser

That is the same HTTP shape the api-bridge MCP server calls when MCP_API_URL is
session-scoped. This isolates the mcpbridge API and browser locking from Gemini
startup, prompt timing, and final-answer formatting.

Example:
  mcp-agent test agent-browse-api-stress-e2e \
    --workspace-api-url http://127.0.0.1:18744 \
    --cdp-port 9222 \
    --workers 8`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if agentBrowseAPIStressE2EFlags.workers <= 0 {
			return fmt.Errorf("--workers must be > 0")
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), agentBrowseAPIStressE2EFlags.timeout)
		defer cancel()

		workspaceURL := strings.TrimRight(strings.TrimSpace(agentBrowseAPIStressE2EFlags.workspaceAPIURL), "/")
		if workspaceURL == "" {
			workspaceURL = strings.TrimRight(strings.TrimSpace(os.Getenv("WORKSPACE_API_URL")), "/")
		}
		if workspaceURL == "" {
			workspaceURL = "http://127.0.0.1:8081"
		}
		restoreWorkspaceEnv := setEnvForAgentBrowseAPIStress("WORKSPACE_API_URL", workspaceURL)
		defer restoreWorkspaceEnv()

		if !agentBrowseAPIStressE2EFlags.skipCDPPreflight {
			if err := preflightAgentBrowserCDP(ctx, agentBrowseAPIStressE2EFlags.cdpPort); err != nil {
				return err
			}
			fmt.Printf("PASS CDP preflight: Chrome DevTools and agent-browser are reachable on port %d\n", agentBrowseAPIStressE2EFlags.cdpPort)
		}

		cases := makeAgentBrowseStressCases(agentBrowseAPIStressE2EFlags.workers)
		pageServer := newAgentBrowseStressServer(cases)
		defer pageServer.Close()
		for i := range cases {
			cases[i].targetURL = pageServer.URL + "/case/" + cases[i].marker
		}

		apiServer, closeAPIServer := newAgentBrowseAPITestServer()
		defer closeAPIServer()

		sessionPrefix := strings.TrimSpace(agentBrowseAPIStressE2EFlags.sessionPrefix)
		if sessionPrefix == "" {
			sessionPrefix = fmt.Sprintf("agent-browse-api-stress-%d", time.Now().UnixNano())
		}

		client := &agentBrowseAPIStressClient{
			baseURL: apiServer.baseURL,
			token:   apiServer.token,
			http:    &http.Client{Timeout: agentBrowseAPIStressE2EFlags.requestTimeout},
		}

		fmt.Printf("Running agent_browser API stress workers=%d cdp_port=%d workspace_api=%s endpoint=%s/s/{session}/tools/mcp/workspace_browser/agent_browser\n",
			len(cases), agentBrowseAPIStressE2EFlags.cdpPort, workspaceURL, apiServer.baseURL)

		results := make([]agentBrowseAPIStressResult, len(cases))
		var wg sync.WaitGroup
		for i := range cases {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				results[idx] = runAgentBrowseAPIStressWorker(ctx, client, sessionPrefix, cases[idx], cases)
			}(i)
		}
		wg.Wait()

		sort.Slice(results, func(i, j int) bool { return results[i].worker < results[j].worker })
		var failures []string
		for _, result := range results {
			if result.err != nil {
				failures = append(failures, fmt.Sprintf("worker %02d session=%s: %v", result.worker, result.sessionID, result.err))
				continue
			}
			fmt.Printf("PASS worker %02d session=%s scenario=%s marker=%s\n", result.worker, result.sessionID, result.scenario, result.marker)
		}
		if len(failures) > 0 {
			return fmt.Errorf("agent_browser API stress e2e failed (%d/%d):\n%s", len(failures), len(results), strings.Join(failures, "\n"))
		}

		fmt.Println("PASS agent_browser API stress e2e")
		return nil
	},
}

func init() {
	agentBrowseAPIStressE2ECmd.Flags().StringVar(&agentBrowseAPIStressE2EFlags.workspaceAPIURL, "workspace-api-url", "", "workspace API URL used by agent_browser executor; defaults to WORKSPACE_API_URL or http://127.0.0.1:8081")
	agentBrowseAPIStressE2ECmd.Flags().StringVar(&agentBrowseAPIStressE2EFlags.sessionPrefix, "session-prefix", "", "session ID prefix; generated when omitted")
	agentBrowseAPIStressE2ECmd.Flags().IntVar(&agentBrowseAPIStressE2EFlags.cdpPort, "cdp-port", 9222, "Chrome DevTools Protocol port to test")
	agentBrowseAPIStressE2ECmd.Flags().IntVar(&agentBrowseAPIStressE2EFlags.workers, "workers", 4, "number of parallel direct API sessions")
	agentBrowseAPIStressE2ECmd.Flags().DurationVar(&agentBrowseAPIStressE2EFlags.timeout, "timeout", 4*time.Minute, "overall stress test timeout")
	agentBrowseAPIStressE2ECmd.Flags().DurationVar(&agentBrowseAPIStressE2EFlags.requestTimeout, "request-timeout", 45*time.Second, "timeout for each direct agent_browser API request")
	agentBrowseAPIStressE2ECmd.Flags().BoolVar(&agentBrowseAPIStressE2EFlags.skipCDPPreflight, "skip-cdp-preflight", false, "skip local Chrome CDP and agent-browser smoke checks")
}

type agentBrowseAPITestServer struct {
	baseURL string
	token   string
}

type agentBrowseAPIStressResult struct {
	worker    int
	sessionID string
	marker    string
	scenario  string
	err       error
}

type agentBrowseAPIStressClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func newAgentBrowseAPITestServer() (agentBrowseAPITestServer, func()) {
	logger := loggerv2.NewNoop()
	handlers := mcpExecutor.NewExecutorHandlers("", logger)
	token := mcpExecutor.GenerateAPIToken()
	router := mux.NewRouter()

	routeMCPRequest := func(w http.ResponseWriter, r *http.Request, server, tool string) {
		normalized := strings.ReplaceAll(server, "-", "_")
		if normalized == "workspace_browser" {
			handlers.HandlePerToolCustomRequest(w, r, tool)
			return
		}
		handlers.HandlePerToolMCPRequest(w, r, server, tool)
	}

	toolsRouter := router.PathPrefix("/tools").Subrouter()
	toolsRouter.Use(mcpExecutor.AuthMiddleware(token))
	toolsRouter.HandleFunc("/mcp/{server}/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		routeMCPRequest(w, r, vars["server"], vars["tool"])
	}).Methods(http.MethodPost, http.MethodOptions)
	toolsRouter.HandleFunc("/custom/{tool}", func(w http.ResponseWriter, r *http.Request) {
		handlers.HandlePerToolCustomRequest(w, r, mux.Vars(r)["tool"])
	}).Methods(http.MethodPost, http.MethodOptions)

	sessionToolsRouter := router.PathPrefix("/s/{session_id}/tools").Subrouter()
	sessionToolsRouter.Use(mcpExecutor.AuthMiddleware(token))
	sessionToolsRouter.HandleFunc("/mcp/{server}/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		sessionID := vars["session_id"]
		r.Header.Set("X-Session-ID", sessionID)
		ctx := context.WithValue(r.Context(), common.ChatSessionIDKey, sessionID)
		routeMCPRequest(w, r.WithContext(ctx), vars["server"], vars["tool"])
	}).Methods(http.MethodPost, http.MethodOptions)
	sessionToolsRouter.HandleFunc("/custom/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		sessionID := vars["session_id"]
		r.Header.Set("X-Session-ID", sessionID)
		ctx := context.WithValue(r.Context(), common.ChatSessionIDKey, sessionID)
		handlers.HandlePerToolCustomRequest(w, r.WithContext(ctx), vars["tool"])
	}).Methods(http.MethodPost, http.MethodOptions)

	server := httptest.NewServer(router)
	return agentBrowseAPITestServer{
		baseURL: server.URL,
		token:   token,
	}, func() {
		server.Close()
	}
}

func runAgentBrowseAPIStressWorker(ctx context.Context, client *agentBrowseAPIStressClient, sessionPrefix string, tc agentBrowseStressCase, allCases []agentBrowseStressCase) agentBrowseAPIStressResult {
	result := agentBrowseAPIStressResult{
		worker:    tc.worker,
		sessionID: fmt.Sprintf("%s-%02d", sessionPrefix, tc.worker),
		marker:    tc.marker,
		scenario:  tc.scenario,
	}

	logger := loggerv2.NewNoop()
	codeexec.InitRegistryForSession(result.sessionID, virtualtools.CreateWorkspaceBrowserToolExecutorsWithSession(result.sessionID, agentBrowseAPIStressE2EFlags.cdpPort), logger)
	codeexec.SetSessionToolAllowList(result.sessionID, map[string]bool{"agent_browser": true})
	defer codeexec.CleanupSession(result.sessionID)

	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = client.callAgentBrowser(stopCtx, result.sessionID, agentBrowseAPIToolArgs(tc, "tab", []string{"--cdp", cdpURLForAgentBrowseAPIStress(), "close", tc.tabLabel}))
	}()

	createArgs := []string{"--cdp", cdpURLForAgentBrowseAPIStress(), "new", "--label", tc.tabLabel, tc.targetURL}
	if tc.worker == 0 {
		createArgs = []string{"--cdp", cdpURLForAgentBrowseAPIStress(), "tab", "new", "--label", tc.tabLabel, tc.targetURL}
	}
	if output, err := client.callAgentBrowser(ctx, result.sessionID, agentBrowseAPIToolArgs(tc, "tab", createArgs)); err != nil {
		result.err = fmt.Errorf("create labeled tab failed: %w; output=%s", err, summarizeAgentBrowseAPIOutput(output))
		return result
	}

	if tc.worker == 0 {
		waitArgs := []string{"--cdp", cdpURLForAgentBrowseAPIStress(), "tab", tc.tabLabel, "wait", "10ms"}
		if output, err := client.callAgentBrowser(ctx, result.sessionID, agentBrowseAPIToolArgs(tc, "wait", waitArgs)); err != nil {
			result.err = fmt.Errorf("normalized wait failed: %w; output=%s", err, summarizeAgentBrowseAPIOutput(output))
			return result
		}
	}

	if tc.scenario == "tab-list-snapshot" {
		if output, err := client.callAgentBrowser(ctx, result.sessionID, agentBrowseAPIToolArgs(tc, "tab", []string{"--cdp", cdpURLForAgentBrowseAPIStress(), "list"})); err != nil {
			result.err = fmt.Errorf("tab list failed: %w; output=%s", err, summarizeAgentBrowseAPIOutput(output))
			return result
		}
	}

	readCommand, readArgs := agentBrowseAPIReadCommand(tc)
	output, err := client.callAgentBrowser(ctx, result.sessionID, agentBrowseAPIToolArgs(tc, readCommand, readArgs))
	if err != nil {
		result.err = fmt.Errorf("%s read failed: %w; output=%s", readCommand, err, summarizeAgentBrowseAPIOutput(output))
		return result
	}
	if !strings.Contains(output, tc.marker) {
		result.err = fmt.Errorf("%s output did not contain this worker marker %q; output=%s", readCommand, tc.marker, summarizeAgentBrowseAPIOutput(output))
		return result
	}
	for _, other := range allCases {
		if other.marker != tc.marker && strings.Contains(output, other.marker) {
			result.err = fmt.Errorf("%s output leaked another worker marker %q; output=%s", readCommand, other.marker, summarizeAgentBrowseAPIOutput(output))
			return result
		}
	}

	return result
}

func (c *agentBrowseAPIStressClient) callAgentBrowser(ctx context.Context, sessionID string, args map[string]interface{}) (string, error) {
	body, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("%s/s/%s/tools/mcp/workspace_browser/agent_browser", c.baseURL, url.PathEscape(sessionID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return string(respBody), fmt.Errorf("HTTP %s", resp.Status)
	}
	var parsed mcpExecutor.CustomExecuteResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return string(respBody), fmt.Errorf("decode custom response: %w", err)
	}
	if !parsed.Success {
		return parsed.Result, fmt.Errorf("%s", parsed.Error)
	}
	if containsAgentBrowserTimeout(parsed.Result) {
		return parsed.Result, fmt.Errorf("agent_browser timeout/failure evidence in response")
	}
	return parsed.Result, nil
}

func agentBrowseAPIToolArgs(tc agentBrowseStressCase, command string, args []string) map[string]interface{} {
	return map[string]interface{}{
		"command": command,
		"args":    args,
		"session": tc.browserSession,
	}
}

func agentBrowseAPIReadCommand(tc agentBrowseStressCase) (string, []string) {
	cdpURL := cdpURLForAgentBrowseAPIStress()
	switch tc.scenario {
	case "get-text":
		return "get", []string{"--cdp", cdpURL, "tab", tc.tabLabel, "text", "body"}
	case "eval":
		return "eval", []string{"--cdp", cdpURL, "tab", tc.tabLabel, "document.querySelector('#marker').textContent + '\\n' + document.querySelector('#headline').textContent"}
	default:
		return "snapshot", []string{"--cdp", cdpURL, "tab", tc.tabLabel, "-i"}
	}
}

func cdpURLForAgentBrowseAPIStress() string {
	return fmt.Sprintf("http://127.0.0.1:%d", agentBrowseAPIStressE2EFlags.cdpPort)
}

func summarizeAgentBrowseAPIOutput(output string) string {
	output = strings.TrimSpace(output)
	if len(output) > 600 {
		return output[:600] + "...<truncated>"
	}
	return output
}

func setEnvForAgentBrowseAPIStress(key, value string) func() {
	previous, existed := os.LookupEnv(key)
	_ = os.Setenv(key, value)
	return func() {
		if existed {
			_ = os.Setenv(key, previous)
			return
		}
		_ = os.Unsetenv(key)
	}
}
