package mcpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const fakeMCPServerEnv = "MCPAGENT_FAKE_MCP_SERVER"

// TestMain lets this test binary double as a stdio MCP server, so the PLAT-519
// test below drives real MCP connections (spawn, list, call) without any
// external server.
func TestMain(m *testing.M) {
	if label := os.Getenv(fakeMCPServerEnv); label != "" {
		s := server.NewMCPServer("fake-"+label, "1.0.0")
		s.AddTool(mcp.NewTool("delete_function", mcp.WithString("function_id")),
			func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText(label + ":delete_function:" + req.GetString("function_id", "")), nil
			})
		if err := server.ServeStdio(s); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeChatCompletions is an OpenAI-compatible endpoint that asks for the
// given tool calls on its first request, then answers with every tool result
// it was sent as "<tool name>=<result>" lines. It records the offered tools.
type fakeChatCompletions struct {
	calls   []map[string]interface{}
	mu      sync.Mutex
	offered []string
}

func (f *fakeChatCompletions) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Stream   bool `json:"stream"`
		Messages []struct {
			Role       string `json:"role"`
			Content    any    `json:"content"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	names := map[string]string{}
	for _, call := range f.calls {
		names[call["id"].(string)] = call["function"].(map[string]interface{})["name"].(string)
	}
	var results []string
	for _, message := range req.Messages {
		if message.Role == "tool" {
			text, _ := json.Marshal(message.Content)
			var plain string
			if json.Unmarshal(text, &plain) != nil {
				plain = string(text)
			}
			results = append(results, names[message.ToolCallID]+"="+plain)
		}
	}
	f.mu.Lock()
	if len(results) == 0 {
		f.offered = f.offered[:0]
		for _, tool := range req.Tools {
			f.offered = append(f.offered, tool.Function.Name)
		}
	}
	f.mu.Unlock()

	message := map[string]interface{}{"role": "assistant", "content": strings.Join(results, "\n")}
	finish := "stop"
	if len(results) == 0 {
		calls := make([]map[string]interface{}, len(f.calls))
		for i, call := range f.calls {
			calls[i] = map[string]interface{}{"index": i, "id": call["id"], "type": "function", "function": call["function"]}
		}
		message = map[string]interface{}{"role": "assistant", "content": "", "tool_calls": calls}
		finish = "tool_calls"
	}
	usage := map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	if !req.Stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "chatcmpl-1", "object": "chat.completion", "created": 1, "model": "gpt-4.1",
			"choices": []interface{}{map[string]interface{}{"index": 0, "message": message, "finish_reason": finish}},
			"usage":   usage,
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	chunk := func(choice map[string]interface{}, withUsage bool) {
		body := map[string]interface{}{"id": "chatcmpl-1", "object": "chat.completion.chunk", "created": 1, "model": "gpt-4.1", "choices": []interface{}{choice}}
		if withUsage {
			body["usage"] = usage
		}
		data, _ := json.Marshal(body)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	}
	chunk(map[string]interface{}{"index": 0, "delta": message, "finish_reason": nil}, false)
	chunk(map[string]interface{}{"index": 0, "delta": map[string]interface{}{}, "finish_reason": finish}, true)
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func toolCall(id, name, arguments string) map[string]interface{} {
	return map[string]interface{}{"id": id, "function": map[string]interface{}{"name": name, "arguments": arguments}}
}

// PLAT-519: one person has two Neon connections, and both they and the
// platform have a tool named delete_function. Every MCP tool is offered as
// <alias>__<tool>, so all three are callable, every name is valid for the
// providers, and each call reaches the right server with the real tool name,
// through the sequential and the parallel tool runner alike. get_api_spec
// (code execution) keeps the bridge route on the real names.
func TestMCPToolsArePrefixedAndRouteToTheirServer(t *testing.T) {
	const (
		serverA = "ue03d5aec7071d9d6e888aca57a337a56__neon_46fb2cad6010"
		serverB = "ue03d5aec7071d9d6e888aca57a337a56__neon_9c1d00aa7713"
	)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fakeServer := func(label string) map[string]interface{} {
		return map[string]interface{}{"command": binary, "protocol": "stdio", "env": map[string]string{fakeMCPServerEnv: label}}
	}
	configJSON, _ := json.Marshal(map[string]interface{}{"mcpServers": map[string]interface{}{serverA: fakeServer("A"), serverB: fakeServer("B")}})
	configPath := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(configPath, configJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, parallel := range []bool{false, true} {
		name := map[bool]string{false: "sequential", true: "parallel"}[parallel]
		t.Run(name, func(t *testing.T) {
			fake := &fakeChatCompletions{calls: []map[string]interface{}{
				toolCall("a", "neon_46fb__delete_function", `{"function_id":"f1"}`),
				toolCall("b", "neon_9c1d__delete_function", `{"function_id":"f2"}`),
				toolCall("c", "delete_function", `{}`),
			}}
			endpoint := httptest.NewServer(fake)
			defer endpoint.Close()
			t.Setenv("OPENAI_BASE_URL", endpoint.URL)
			apiKey := "test-key"
			model, err := llm.InitializeLLM(llm.Config{Provider: llm.ProviderOpenAI, ModelID: "gpt-4.1", APIKeys: &llm.ProviderAPIKeys{OpenAI: &apiKey}})
			if err != nil {
				t.Fatal(err)
			}
			agent, err := NewAgentFromDefinition(context.Background(), AgentDefinition{
				Instructions: "test",
				Tools: ToolSet{
					MCP: []MCPToolSource{{Name: serverA}, {Name: serverB}},
					Direct: []ToolDefinition{{
						Name: "delete_function", Description: "platform function delete", DisplayGroup: "workflow",
						InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
						Execute: func(context.Context, map[string]interface{}) (string, error) {
							return "platform:delete_function", nil
						},
					}},
				},
			}, RuntimeConfig{
				Model:         model,
				MCPConfigPath: configPath,
				Generation: GenerationRuntimeConfig{
					Provider: llm.ProviderOpenAI, MaxTurns: 4, APIKeys: &AgentAPIKeys{OpenAI: &apiKey},
					LLM: AgentLLMConfiguration{Primary: LLMModel{Provider: string(llm.ProviderOpenAI), ModelID: "gpt-4.1"}},
				},
				Tools: ToolRuntimeConfig{DisableCache: true, ParallelExecution: parallel},
				MCP:   MCPRuntimeConfig{SessionID: "plat519-" + name},
			})
			if err != nil {
				t.Fatalf("agent build failed: %v", err)
			}
			defer func() { _ = agent.Close() }()

			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			result, err := agent.Run(ctx, Turn{Input: "delete them"})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			for _, offered := range fake.offered {
				if !isValidToolName(offered) {
					t.Errorf("offered tool name %q is not valid for the providers", offered)
				}
			}
			lines := map[string]string{}
			for _, line := range strings.Split(result.Text, "\n") {
				if name, value, ok := strings.Cut(line, "="); ok {
					lines[name] = value
				}
			}
			for name, want := range map[string]string{
				"neon_46fb__delete_function": "A:delete_function:f1",
				"neon_9c1d__delete_function": "B:delete_function:f2",
				"delete_function":            "platform:delete_function",
			} {
				if !strings.Contains(lines[name], want) {
					t.Errorf("call %s returned %q, want %q (offered: %v)", name, lines[name], want, fake.offered)
				}
			}

			// get_api_spec: the prefixed name, or the real name with server_name,
			// documents the unchanged bridge route; the bare name is the platform tool.
			specFor := func(args map[string]interface{}) string {
				spec, err := agent.handleGetAPISpec(ctx, args)
				if err != nil {
					t.Fatalf("get_api_spec(%v): %v", args, err)
				}
				return spec
			}
			if spec := specFor(map[string]interface{}{"tool_name": "neon_46fb__delete_function"}); !strings.Contains(spec, "/tools/mcp/"+serverA+"/delete_function") {
				t.Errorf("prefixed name: spec has no real bridge route:\n%s", spec)
			}
			if spec := specFor(map[string]interface{}{"tool_name": "delete_function", "server_name": serverB}); !strings.Contains(spec, "/tools/mcp/"+serverB+"/delete_function") {
				t.Errorf("real name + server_name: spec has no bridge route for server B:\n%s", spec)
			}
			if spec := specFor(map[string]interface{}{"tool_name": "delete_function"}); !strings.Contains(spec, "/tools/custom/delete_function") || strings.Contains(spec, "/tools/mcp/") {
				t.Errorf("bare name should be the platform tool:\n%s", spec)
			}
		})
	}
}
