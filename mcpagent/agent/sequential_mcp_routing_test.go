package mcpagent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestMappedMCPClientDoesNotUseAnotherServersClient(t *testing.T) {
	eagerClient := new(mcpclient.Client)
	agent := &Agent{
		clients: map[string]mcpclient.ClientInterface{
			"server-a": eagerClient,
		},
		toolToServer: map[string]string{
			"tool-a": "server-a",
			"tool-b": "server-b",
		},
	}

	client, serverName, mapped := agent.mappedMCPClient("tool-b")
	if !mapped {
		t.Fatal("tool-b should be mapped")
	}
	if serverName != "server-b" {
		t.Fatalf("server name = %q, want %q", serverName, "server-b")
	}
	if client != nil {
		t.Fatalf("client = %p, want nil so server-b is connected on demand", client)
	}

	client, serverName, mapped = agent.mappedMCPClient("tool-a")
	if !mapped || serverName != "server-a" {
		t.Fatalf("tool-a mapping = (%q, %v), want (%q, true)", serverName, mapped, "server-a")
	}
	if client != eagerClient {
		t.Fatal("tool-a should use server-a's existing client")
	}
}

func TestPrepareParallelToolExecutionDoesNotUseAnotherServersClientForLazyServer(t *testing.T) {
	eagerClient := new(mcpclient.Client)
	agent := &Agent{
		clients: map[string]mcpclient.ClientInterface{
			"server-a": eagerClient,
		},
		toolToServer: map[string]string{
			"tool-b": "server-b",
		},
		logger:     loggerv2.NewDefault(),
		sessionID:  "parallel-routing-regression",
		configPath: filepath.Join(t.TempDir(), "missing-mcp-config.json"),
	}

	plan := prepareToolExecution(
		context.Background(),
		agent,
		llmtypes.ToolCall{
			ID: "call-b",
			FunctionCall: &llmtypes.FunctionCall{
				Name:      "tool-b",
				Arguments: `{}`,
			},
		},
		0,
		0,
		"trace-id",
		time.Now(),
		context.Background(),
	)

	if !plan.skipExecution {
		t.Fatal("parallel tool call should stop when server-b cannot be connected")
	}
	if plan.client != nil {
		t.Fatal("parallel tool call must not retain server-a's client")
	}
	if plan.preErrorMessage == nil || len(plan.preErrorMessage.Parts) != 1 {
		t.Fatal("parallel tool call should return a connection error to the model")
	}
	response, ok := plan.preErrorMessage.Parts[0].(llmtypes.ToolCallResponse)
	if !ok || !response.IsError || !strings.Contains(response.Content, "server-b") {
		t.Fatalf("unexpected parallel routing error response: %#v", plan.preErrorMessage.Parts[0])
	}
}
