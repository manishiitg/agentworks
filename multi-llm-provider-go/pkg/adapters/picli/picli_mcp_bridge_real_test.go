package picli

import (
	"context"
	"fmt"
	"os"

	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestPiCLIRealMCPBridgeOnlyToolsContract(t *testing.T) {
	requireRealPiCLIContractE2E(t)
	apiKey := firstNonEmptyPiTestEnv("GEMINI_API_KEY", "GOOGLE_API_KEY", "PI_API_KEY")

	workDir := piLiveWorkDir(t)
	serverPath := filepath.Join(workDir, "pi-mcp-canary-server.js")
	logPath := filepath.Join(workDir, "pi-mcp-canary-calls.jsonl")
	if err := os.WriteFile(serverPath, []byte(piMCPBridgeCanaryServerSource()), 0o700); err != nil {
		t.Fatalf("write MCP canary server: %v", err)
	}
	mcpConfig := fmt.Sprintf(`{
  "mcpServers": {
    "api-bridge": {
      "command": "node",
      "args": [%q],
      "env": {"PI_MCP_CANARY_LOG": %q},
      "exposure": "direct"
    }
  }
}`, serverPath, logPath)

	adapter := NewPiCLIAdapter(apiKey, piRealContractModel(), &mockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ownerSessionID := "pi-mcp-bridge-e2e-" + piRandomHex(6)
	t.Cleanup(func() { _ = CleanupPiCLIInteractiveSessions(context.Background()) })

	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Use the MCP gateway only. Call the api-bridge MCP tool bridge_canary, then reply exactly with the tool output text."),
	}, WithWorkingDir(workDir), WithInteractiveSessionID(ownerSessionID), WithPersistentInteractiveSession(true), WithMCPConfig(mcpConfig), WithBridgeOnlyTools(true))
	if err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}
	if resp == nil || len(resp.Choices) == 0 {
		t.Fatal("GenerateContent() returned no choices")
	}
	if err := waitForPiMCPBridgeCanaryLog(ctx, logPath); err != nil {
		t.Fatalf("MCP canary tool was not called: %v\nresponse=%q", err, resp.Choices[0].Content)
	}
	if !strings.Contains(resp.Choices[0].Content, "PI_MCP_BRIDGE_OK") {
		t.Fatalf("response = %q, want PI_MCP_BRIDGE_OK", resp.Choices[0].Content)
	}
	liveSession := piRPCSessionForOwner(ownerSessionID)
	if liveSession == nil {
		t.Fatal("persistent Pi session missing before cleanup")
	}
	exclusiveAgentDir, _ := piSessionRuntimeDirs(workDir, liveSession.nativeID)
	ClosePiCLIInteractiveSessionForOwner(ownerSessionID, "test cleanup")
	if _, err := os.Stat(piNativeMCPConfigPath(exclusiveAgentDir)); !os.IsNotExist(err) {
		t.Fatalf("exclusive session mcp.json should be removed after persistent cleanup, err=%v", err)
	}
}

// Native MCP must expose typed tools on the first and subsequent launches,
// without the retired plugin's metadata cache or double-encoded proxy args.
func TestPiCLIRealNativeToolsAvailableOnEveryLaunch(t *testing.T) {
	requireRealPiCLIContractE2E(t)
	t.Cleanup(func() { _ = CleanupPiCLIInteractiveSessions(context.Background()) })

	workDir := piLiveWorkDir(t)
	serverPath := writePiReportCWDMCPServer(t)
	mcpConfig := fmt.Sprintf(`{
  "mcpServers": {
    "api-bridge": {
      "command": "node",
      "args": [%q],
      "exposure": "direct"
    }
  }
}`, serverPath)

	prompt := "Call the mcp__api_bridge__report_cwd tool, then reply exactly with the tool output text."

	runOnce := func(label string) []llmtypes.StreamChunk {
		adapter := newRealPiCLIAdapter(t)
		ownerSessionID := "pi-directtools-" + label + "-" + piRandomHex(6)
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		stream := make(chan llmtypes.StreamChunk, 4096)
		_, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
			llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, prompt),
		}, WithWorkingDir(workDir), WithInteractiveSessionID(ownerSessionID), WithPersistentInteractiveSession(true), WithMCPConfig(mcpConfig), WithBridgeOnlyTools(true), llmtypes.WithStreamingChan(stream))
		if err != nil {
			t.Fatalf("GenerateContent() [%s] error = %v", label, err)
		}
		ClosePiCLIInteractiveSessionForOwner(ownerSessionID, "test cleanup")
		var chunks []llmtypes.StreamChunk
		for chunk := range stream {
			chunks = append(chunks, chunk)
		}
		return chunks
	}

	for _, label := range []string{"first", "repeat"} {
		chunks := runOnce(label)

		sawReportCWDCall := false
		for _, chunk := range chunks {
			if chunk.Type != llmtypes.StreamChunkTypeToolCallStart {
				continue
			}
			if chunk.ToolName != "mcp__api_bridge__report_cwd" {
				continue
			}
			sawReportCWDCall = true
			args := strings.TrimSpace(chunk.ToolArgs)
			if strings.Contains(args, `"tool"`) && strings.Contains(args, `"args"`) {
				t.Fatalf("report_cwd's raw tool_execution_start args still carry the generic proxy wrapper shape "+
					"({\"tool\":...,\"args\":...}) on launch %s: %s -- "+
					"native typed tools were not registered", label, args)
			}
		}
		if !sawReportCWDCall {
			t.Fatal("expected a report_cwd tool_execution_start chunk on each native MCP launch")
		}
	}
}

// The retired generic mcp proxy must be unavailable. Exercise the actual
// mcpbridge stdio-to-HTTP transport before requesting the absent proxy.
func TestPiCLIRealNativeMCPHasNoProxyTool(t *testing.T) {
	requireRealPiCLIContractE2E(t)
	t.Cleanup(func() { _ = CleanupPiCLIInteractiveSessions(context.Background()) })

	bridgeBin := buildRealMCPBridgeBinary(t)
	const apiToken = "real-bridge-p0-test-token"
	fakeAPI := newFakeBridgeAPIServer(t, apiToken, map[string]string{
		"execute_shell_command": "PI_REAL_BRIDGE_OK",
	})
	toolsJSON := realMCPBridgeToolDefsJSON("execute_shell_command")
	toolsPath := filepath.Join(t.TempDir(), "tools.json")
	if err := os.WriteFile(toolsPath, []byte(toolsJSON), 0600); err != nil {
		t.Fatal(err)
	}

	workDir := piLiveWorkDir(t)
	mcpConfig := fmt.Sprintf(`{
  "mcpServers": {
    "api-bridge": {
      "command": %q,
      "env": {"MCP_API_URL": %q, "MCP_API_TOKEN": %q, "MCP_TOOLS_FILE": %q},
      "exposure": "direct"
    }
  }
}`, bridgeBin, fakeAPI.URL, apiToken, toolsPath)

	runOnce := func(label, prompt string) []llmtypes.StreamChunk {
		adapter := newRealPiCLIAdapter(t)
		ownerSessionID := "pi-proxy-disabled-" + label + "-" + piRandomHex(6)
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		stream := make(chan llmtypes.StreamChunk, 4096)
		_, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
			llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, prompt),
		}, WithWorkingDir(workDir), WithInteractiveSessionID(ownerSessionID), WithPersistentInteractiveSession(true), WithMCPConfig(mcpConfig), WithBridgeOnlyTools(true), llmtypes.WithStreamingChan(stream))
		if err != nil {
			t.Fatalf("GenerateContent() [%s] error = %v", label, err)
		}
		ClosePiCLIInteractiveSessionForOwner(ownerSessionID, "test cleanup")
		var chunks []llmtypes.StreamChunk
		for chunk := range stream {
			chunks = append(chunks, chunk)
		}
		return chunks
	}

	runOnce("warm", "Call the api-bridge MCP tool execute_shell_command with command \"echo hi\", then reply exactly with the tool output text.")
	if !fakeAPI.sawCall("execute_shell_command") {
		t.Fatal("expected the real mcpbridge binary to forward execute_shell_command to the fake bridge API server on the first launch")
	}

	chunks := runOnce("verify", `Call exactly this tool: name "mcp", arguments {"search": "execute_shell_command"}. Report back verbatim whatever text the tool result contains, with no commentary of your own.`)

	var toolResultText, finalText string
	sawMcpToolCall := false
	for _, chunk := range chunks {
		if chunk.Type == llmtypes.StreamChunkTypeToolCallStart && chunk.ToolName == "mcp" {
			sawMcpToolCall = true
		}
		if chunk.Type == llmtypes.StreamChunkTypeToolCallEnd && chunk.ToolName == "mcp" {
			toolResultText += chunk.ToolResult
		}
		if chunk.Type == llmtypes.StreamChunkTypeContent {
			finalText += chunk.Content
		}
	}
	if finalText == "" && !sawMcpToolCall {
		t.Fatal("expected either an mcp tool call attempt or a text response on each native MCP launch -- got neither, the launch may have failed silently")
	}
	if sawMcpToolCall {

		if !strings.Contains(toolResultText, "not found") {
			t.Fatalf("expected pi's own proxy-tool-not-found rejection under native MCP, "+
				"got tool result: %q -- the mcp proxy tool answered instead of being disabled", toolResultText)
		}
	} else {

		t.Logf("model never attempted an mcp tool call at all (no such tool in its list); final text: %q", finalText)
	}
}

func firstNonEmptyPiTestEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func waitForPiMCPBridgeCanaryLog(ctx context.Context, path string) error {
	return waitForPiMCPBridgeToolLog(ctx, path, "bridge_canary")
}

func waitForPiMCPBridgeToolLog(ctx context.Context, path, toolName string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	want := fmt.Sprintf("%q", toolName)
	for {
		body, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(body), want) {
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func piMCPBridgeCanaryServerSource() string {
	return `#!/usr/bin/env node
const fs = require("fs");
const readline = require("readline");

const logPath = process.env.PI_MCP_CANARY_LOG;
const rl = readline.createInterface({ input: process.stdin });

function write(message) {
  process.stdout.write(JSON.stringify(message) + "\n");
}

function result(id, payload) {
  write({ jsonrpc: "2.0", id, result: payload });
}

function error(id, code, message) {
  write({ jsonrpc: "2.0", id, error: { code, message } });
}

rl.on("line", (line) => {
  if (!line.trim()) return;
  let msg;
  try {
    msg = JSON.parse(line);
  } catch {
    return;
  }
  if (msg.id === undefined || msg.id === null) return;
  switch (msg.method) {
    case "initialize":
      result(msg.id, {
        protocolVersion: msg.params?.protocolVersion || "2025-06-18",
        capabilities: { tools: {} },
        serverInfo: { name: "pi-mcp-canary", version: "0.1.0" }
      });
      break;
    case "ping":
      result(msg.id, {});
      break;
    case "tools/list":
      result(msg.id, {
        tools: [{
          name: "bridge_canary",
          description: "Return a fixed canary proving the Pi MCP bridge is mounted.",
          inputSchema: { type: "object", properties: {}, additionalProperties: false }
        }]
      });
      break;
    case "tools/call":
      if (msg.params?.name !== "bridge_canary") {
        error(msg.id, -32602, "unknown tool");
        break;
      }
      if (logPath) {
        fs.appendFileSync(logPath, JSON.stringify({ tool: msg.params.name, ts: Date.now() }) + "\n");
      }
      result(msg.id, {
        content: [{ type: "text", text: "PI_MCP_BRIDGE_OK" }],
        isError: false
      });
      break;
    default:
      error(msg.id, -32601, "method not found");
  }
});
`
}
