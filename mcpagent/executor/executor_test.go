package executor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/openai"
)

// TestExecutorHTTPHandlers is a comprehensive integration test for the executor package
// It starts a real HTTP server and makes actual requests to all three endpoints
func TestExecutorHTTPHandlers(t *testing.T) {
	// Check if OPENAI_API_KEY is set
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("Skipping test: OPENAI_API_KEY not set")
	}

	ctx := context.Background()
	logger := loggerv2.NewDefault()

	t.Log("=== Executor Package Integration Test ===")

	// Step 1: Initialize agent to set up the codeexec registry
	t.Log("Step 1: Initialize Agent and Registry")
	if err := initializeAgentRegistry(ctx, logger, t); err != nil {
		t.Fatalf("Failed to initialize registry: %v", err)
	}

	// Step 2: Start HTTP server with executor handlers
	t.Log("Step 2: Start HTTP Server")
	serverURL, shutdown, err := startTestServer(logger, t)
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer shutdown()

	t.Logf("✅ Server started at %s", serverURL)

	// Give server a moment to fully start
	time.Sleep(500 * time.Millisecond)

	// Step 3: Test MCP execute endpoint
	t.Log("Step 3: Test MCP Execute Endpoint")
	testMCPExecuteEndpoint(serverURL, logger, t)

	// Step 4: Test custom execute endpoint
	t.Log("Step 4: Test Custom Execute Endpoint")
	testCustomExecuteEndpoint(serverURL, logger, t)

	// Step 5: Test virtual execute endpoint
	t.Log("Step 5: Test Virtual Execute Endpoint")
	testVirtualExecuteEndpoint(serverURL, logger, t)

	t.Log("✅ All executor tests passed")
}

// initializeAgentRegistry creates an agent to initialize the codeexec registry
func initializeAgentRegistry(ctx context.Context, logger loggerv2.Logger, t *testing.T) error {
	t.Log("Creating agent to initialize tool registry...")

	// Create LLM with OpenAI
	llmInstance, err := llm.InitializeLLM(llm.Config{
		Provider:    llm.ProviderOpenAI,
		ModelID:     openai.ModelGPT41Mini,
		Temperature: 0.0,
		Logger:      logger,
	})
	if err != nil {
		return fmt.Errorf("failed to create LLM: %w", err)
	}

	// Create minimal agent with empty config
	agent, err := mcpagent.NewAgentFromDefinition(ctx, mcpagent.AgentDefinition{
		Tools: mcpagent.ToolSet{MCP: []mcpagent.MCPToolSource{{Name: "test-session"}}},
	}, mcpagent.RuntimeConfig{
		Model: llmInstance, MCPConfigPath: "configs/mcp_servers_simple.json",
		Observability: mcpagent.ObservabilityRuntimeConfig{Logger: logger},
	})
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}
	defer agent.Close()

	// Verify registry is initialized
	registry := codeexec.GetRegistry()
	if registry == nil {
		return fmt.Errorf("code execution registry not initialized")
	}

	t.Log("✅ Agent created and registry initialized")
	return nil
}

// startTestServer starts an HTTP server with executor handlers
func startTestServer(logger loggerv2.Logger, t *testing.T) (string, func(), error) {
	// Use empty config path (minimal setup)
	configPath := ""
	t.Logf("Using config path: %s (empty = minimal setup)", configPath)

	// Create executor handlers
	handlers := executor.NewExecutorHandlers(configPath, logger)

	// Create HTTP mux
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mcp/execute", handlers.HandleMCPExecute)
	mux.HandleFunc("/api/custom/execute", handlers.HandleCustomExecute)
	mux.HandleFunc("/api/virtual/execute", handlers.HandleVirtualExecute)

	// Start server on fixed port for testing
	server := &http.Server{
		Addr:              "127.0.0.1:18765",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Start server in background
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()

	serverURL := "http://127.0.0.1:18765"

	// Shutdown function
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Logf("Server shutdown error: %v", err)
		}
		t.Log("Server stopped")
	}

	return serverURL, shutdown, nil
}

// testMCPExecuteEndpoint tests the /api/mcp/execute endpoint
func testMCPExecuteEndpoint(serverURL string, logger loggerv2.Logger, t *testing.T) {
	t.Log("Testing POST /api/mcp/execute...")

	// Prepare request
	reqBody := map[string]interface{}{
		"server": "test-server",
		"tool":   "test-tool",
		"args":   map[string]interface{}{},
	}

	respBody, err := makeRequest(serverURL+"/api/mcp/execute", reqBody, t)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	t.Logf("Response: %s", string(respBody))

	// Parse response
	var response struct {
		Success bool   `json:"success"`
		Result  string `json:"result,omitempty"`
		Error   string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// We expect this to fail (server not configured), but the endpoint should respond properly
	if !response.Success && response.Error != "" {
		t.Logf("✅ Expected failure (server not configured): %s", response.Error)
	} else if response.Success {
		t.Log("✅ MCP execute succeeded")
	}
}

// testCustomExecuteEndpoint tests the /api/custom/execute endpoint
func testCustomExecuteEndpoint(serverURL string, logger loggerv2.Logger, t *testing.T) {
	t.Log("Testing POST /api/custom/execute...")

	// Prepare request - try to call a custom tool
	reqBody := map[string]interface{}{
		"tool": "test-custom-tool",
		"args": map[string]interface{}{},
	}

	respBody, err := makeRequest(serverURL+"/api/custom/execute", reqBody, t)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	t.Logf("Response: %s", string(respBody))

	// Parse response
	var response struct {
		Success bool   `json:"success"`
		Result  string `json:"result,omitempty"`
		Error   string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// We expect this to fail (tool not registered), but the endpoint should respond properly
	if !response.Success && response.Error != "" {
		t.Logf("✅ Expected failure (tool not registered): %s", response.Error)
	} else if response.Success {
		t.Log("✅ Custom execute succeeded")
	}
}

// testVirtualExecuteEndpoint tests the /api/virtual/execute endpoint
func testVirtualExecuteEndpoint(serverURL string, logger loggerv2.Logger, t *testing.T) {
	t.Log("Testing POST /api/virtual/execute...")

	// Prepare request - try to call get_api_spec
	reqBody := map[string]interface{}{
		"tool": "get_api_spec",
		"args": map[string]interface{}{},
	}

	respBody, err := makeRequest(serverURL+"/api/virtual/execute", reqBody, t)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	t.Logf("Response: %s", string(respBody))

	// Parse response
	var response struct {
		Success bool   `json:"success"`
		Result  string `json:"result,omitempty"`
		Error   string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Virtual tools may or may not be registered depending on agent mode
	if !response.Success && response.Error != "" {
		t.Logf("✅ Expected failure (virtual tool not registered): %s", response.Error)
	} else if response.Success {
		t.Logf("✅ Virtual tool executed successfully: %s", response.Result)
	}
}

// makeRequest makes an HTTP POST request and returns the response body
func makeRequest(url string, body map[string]interface{}, t *testing.T) ([]byte, error) {
	// Marshal request body
	reqBodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	t.Logf("Making request to %s", url)

	// Create request
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(reqBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Make request
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	t.Logf("Response status: %d", resp.StatusCode)

	return respBody, nil
}
