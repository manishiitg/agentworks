package pythontools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The runtime reconstructs providers for each turn; a local HTTP model fixture
// verifies the real model-facing schema and tool-result round trip.
type initialToolModel struct{ llmtypes.Model }

func (initialToolModel) GetModelID() string { return "gpt-4o" }
func (initialToolModel) GetModelMetadata(string) (*llmtypes.ModelMetadata, error) {
	return nil, fmt.Errorf("test model has no pricing")
}

func TestPythonToolRunsInsideSharedAgentLoop(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	tool, directory := fixture(t, testDefinition, `import sqlite3
def run(input):
    with sqlite3.connect("customers.sqlite") as db:
        row = db.execute("SELECT name FROM customers WHERE id = ?", (input["id"],)).fetchone()
    return {"name": row[0] if row else None}
`)
	// Seed a real custom database; the tool performs the actual parameterized query.
	command := exec.Command("python3", "-c", `import sqlite3
with sqlite3.connect("customers.sqlite") as db:
    db.execute("CREATE TABLE customers (id TEXT, name TEXT)")
    db.execute("INSERT INTO customers VALUES (?, ?)", ("123", "Ada"))
`)
	command.Dir = directory
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("seed database: %s %v", out, err)
	}
	config := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(config, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	sawResult := false
	modelAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			Messages []struct {
				Role    string      `json:"role"`
				Content interface{} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad request", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			found := false
			for _, tool := range request.Tools {
				if tool.Function.Name == "lookup_customer" {
					found = true
				}
			}
			if !found {
				t.Error("model was not offered its named Python tool")
			}
			_, _ = w.Write([]byte(`{"id":"test-1","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"lookup-1","type":"function","function":{"name":"lookup_customer","arguments":"{\"id\":\"123\"}"}}]}}]}`))
			return
		}
		for _, message := range request.Messages {
			if message.Role == "tool" {
				if value, ok := message.Content.(string); ok && strings.Contains(value, `{"name": "Ada"}`) {
					sawResult = true
				}
			}
		}
		if !sawResult {
			t.Error("Python result was not delivered back to model")
		}
		_, _ = w.Write([]byte(`{"id":"test-2","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"{\"customer\":\"Ada\"}"}}]}`))
	}))
	defer modelAPI.Close()
	t.Setenv("OPENAI_BASE_URL", modelAPI.URL+"/")
	t.Setenv("OPENAI_API_KEY", "unit-test")
	agent, err := mcpagent.NewAgentFromDefinition(context.Background(), mcpagent.AgentDefinition{
		Instructions: "Use lookup_customer then return JSON.",
		Tools:        mcpagent.ToolSet{Direct: []mcpagent.ToolDefinition{{Name: tool.Name, Description: tool.Description, InputSchema: tool.Parameters, Execute: tool.Bind(testShell, filepath.Join(directory, "main.py"), directory), DisplayGroup: Category}}},
	}, mcpagent.RuntimeConfig{Model: initialToolModel{}, MCPConfigPath: config, Generation: mcpagent.GenerationRuntimeConfig{Provider: llm.ProviderOpenAI, MaxTurns: 3}, Observability: mcpagent.ObservabilityRuntimeConfig{Logger: loggerv2.NewNoop()}})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	result, err := agent.Run(context.Background(), mcpagent.Turn{Input: "Find customer 123"})
	if err != nil || result.Text != `{"customer":"Ada"}` || !sawResult {
		t.Fatalf("shared agent tool loop: result=%q calls=%d err=%v", result.Text, calls, err)
	}
}
