package cursorcli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func requireRealCursorCLIE2E(t *testing.T) {
	t.Helper()
	if !*codingCLIP0Live {
		t.Skip("run through the live coding CLI P0 runner")
	}
	// Use the owner-selected Composer model, never the ambient UI selection.
	t.Setenv(EnvCursorCLITestModel, "composer-2.5")
	for _, bin := range []string{"cursor-agent"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("real Cursor CLI tests require %s in PATH: %v", bin, err)
		}
	}
}

type cursorDrainedStream struct {
	content        string
	terminalCount  int
	terminalSample string
}

func drainCursorStream(streamChan <-chan llmtypes.StreamChunk) cursorDrainedStream {
	var parts []string
	var drained cursorDrainedStream
	for {
		select {
		case chunk, ok := <-streamChan:
			if !ok {
				drained.content = strings.TrimSpace(strings.Join(parts, ""))
				return drained
			}
			switch chunk.Type {
			case llmtypes.StreamChunkTypeContent:
				parts = append(parts, chunk.Content)
			case llmtypes.StreamChunkTypeTerminal:
				drained.terminalCount++
				if drained.terminalSample == "" {
					drained.terminalSample = chunk.Content
				}
			}
		default:
			drained.content = strings.TrimSpace(strings.Join(parts, ""))
			return drained
		}
	}
}

// writeCursorBridgeWriteMCPServer writes a tmux-compatible MCP server with a
// single write tool that prepends "BRIDGE_WROTE_THIS:" to every write — used
// by TestCursorTmuxSystemPromptSteersWritesThroughBridge to detect which
// path cursor took.
func writeCursorBridgeWriteMCPServer(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bridge-write-mcp.js")
	script := `#!/usr/bin/env node
const fs = require("fs");
const readline = require("readline");
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
function send(msg) { process.stdout.write(JSON.stringify(msg) + "\n"); }
rl.on("line", (line) => {
  if (!line.trim()) return;
  let msg; try { msg = JSON.parse(line); } catch { return; }
  if (msg.method === "initialize") return send({jsonrpc:"2.0",id:msg.id,result:{protocolVersion:"2024-11-05",capabilities:{tools:{}},serverInfo:{name:"api-bridge",version:"1.0.0"}}});
  if (msg.method === "notifications/initialized") return;
  if (msg.method === "tools/list") return send({jsonrpc:"2.0",id:msg.id,result:{tools:[{
    name: "write_via_bridge",
    description: "Write content to a file at an absolute path.",
    inputSchema: { type: "object", properties: { path: { type: "string" }, content: { type: "string" } }, required: ["path","content"] }
  }]}});
  if (msg.method === "tools/call") {
    const args = (msg.params && msg.params.arguments) || {};
    try {
      fs.writeFileSync(String(args.path), "BRIDGE_WROTE_THIS:" + String(args.content));
      send({jsonrpc:"2.0",id:msg.id,result:{content:[{type:"text",text:"wrote " + String(args.path)}],isError:false}});
    } catch (e) {
      send({jsonrpc:"2.0",id:msg.id,result:{content:[{type:"text",text:String(e)}],isError:true}});
    }
    return;
  }
  if (msg.id !== undefined) send({jsonrpc:"2.0",id:msg.id,result:{}});
});
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write bridge MCP server: %v", err)
	}
	return path
}

// ---------------------------------------------------------------------------
// Helpers: MCP servers and wait utilities for Cursor tmux contract tests
// ---------------------------------------------------------------------------

type cursorRealResult struct {
	content string
	err     error
}

// preApproveCursorMCP prepares the test bridge configuration. ACP passes server definitions inline.
func preApproveCursorMCP(t *testing.T, workDir, mcpConfig, serverName string) {
	t.Helper()
	if err := primeCursorWorkspaceForMCP(workDir, mcpConfig, serverName); err != nil {
		t.Fatalf("prime cursor workspace for MCP: %v", err)
	}
}

func primeCursorWorkspaceForMCP(workDir, mcpConfig, serverName string) error {
	normalizedMCPConfig, err := normalizeCursorMCPConfigForCLI(mcpConfig)
	if err != nil {
		return err
	}
	cursorDir := filepath.Join(workDir, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o755); err != nil {
		return fmt.Errorf("create .cursor dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(cursorDir, "mcp.json"), []byte(normalizedMCPConfig), 0o600); err != nil {
		return fmt.Errorf("write mcp.json: %w", err)
	}
	if allowlistJSON, ok, err := cursorMCPAllowlistCLIConfig(normalizedMCPConfig); err != nil {
		return err
	} else if ok {
		if err := os.WriteFile(filepath.Join(cursorDir, "cli.json"), []byte(allowlistJSON), 0o600); err != nil {
			return fmt.Errorf("write cli.json: %w", err)
		}
		defer os.Remove(filepath.Join(cursorDir, "cli.json"))
	}
	defer os.Remove(filepath.Join(cursorDir, "mcp.json"))

	candidateDirs := []string{workDir}
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil && resolved != workDir {
		candidateDirs = append(candidateDirs, resolved)
	}

	// 1. Register approval from each candidate dir so whichever path
	//    cursor-agent computes at TUI launch already has the approval.
	for _, dir := range candidateDirs {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, "cursor-agent", "mcp", "enable", serverName)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			return fmt.Errorf("cursor-agent mcp enable %s in %s: %w\noutput: %s", serverName, dir, err, string(out))
		}
	}

	// 2. Pre-warm: a non-interactive --print run accepts the workspace-trust
	//    dialog (via --trust) and loads the MCP server end-to-end. After this
	//    completes, the TUI launch can dispatch MCP tool calls without a
	//    trust/tools-list race.
	for _, dir := range candidateDirs {
		// Cursor's first authenticated --print call may initialize/update the
		// CLI and hydrate MCP metadata before answering. That exceeded one minute
		// in a healthy live P0 run, so give this prerequisite the same realistic
		// startup allowance as the interactive contract instead of killing it
		// before the bridge itself can be tested.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		cmd := exec.CommandContext(ctx, "cursor-agent",
			"--workspace", dir,
			"--model", "auto",
			"--force", "--approve-mcps", "--trust",
			"-p", "respond with the single word OK",
		)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			return fmt.Errorf("cursor-agent --print pre-warm in %s: %w\noutput: %s", dir, err, string(out))
		}
	}
	return nil
}

func writeCursorTmuxContractMCPServer(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cursor-tmux-contract-mcp.js")
	script := `#!/usr/bin/env node
const readline = require("readline");
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });

function send(message) {
  process.stdout.write(JSON.stringify(message) + "\n");
}

rl.on("line", (line) => {
  if (!line.trim()) return;
  let msg;
  try {
    msg = JSON.parse(line);
  } catch (err) {
    return;
  }
  if (msg.method === "initialize") {
    send({
      jsonrpc: "2.0",
      id: msg.id,
      result: {
        protocolVersion: "2024-11-05",
        capabilities: { tools: {} },
        serverInfo: { name: "api-bridge", version: "1.0.0" }
      }
    });
    return;
  }
  if (msg.method === "notifications/initialized") return;
  if (msg.method === "tools/list") {
    send({
      jsonrpc: "2.0",
      id: msg.id,
      result: {
        tools: [{
          name: "contract_echo_token",
          description: "REQUIRED tool for contract test - returns the token wrapped in BRIDGE_TOOL_OK_. Always call this when asked to echo a token.",
          inputSchema: { type: "object", properties: { token: { type: "string" } }, required: ["token"] }
        }]
      }
    });
    return;
  }
  if (msg.method === "tools/call") {
    const args = (msg.params && msg.params.arguments) || {};
    send({
      jsonrpc: "2.0",
      id: msg.id,
      result: { content: [{ type: "text", text: "BRIDGE_TOOL_OK_" + String(args.token || "") }], isError: false }
    });
    return;
  }
  if (msg.id !== undefined) send({ jsonrpc: "2.0", id: msg.id, result: {} });
});
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write MCP server: %v", err)
	}
	return path
}

func writeCursorSlowContractMCPServer(t *testing.T, markerPath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cursor-slow-contract-mcp.js")
	script := fmt.Sprintf(`#!/usr/bin/env node
const fs = require("fs");
const readline = require("readline");
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
const markerPath = %q;

function send(message) {
  process.stdout.write(JSON.stringify(message) + "\n");
}

rl.on("line", (line) => {
  if (!line.trim()) return;
  let msg;
  try {
    msg = JSON.parse(line);
  } catch (err) {
    return;
  }
  if (msg.method === "initialize") {
    send({
      jsonrpc: "2.0",
      id: msg.id,
      result: {
        protocolVersion: "2024-11-05",
        capabilities: { tools: {} },
        serverInfo: { name: "api-bridge", version: "1.0.0" }
      }
    });
    return;
  }
  if (msg.method === "notifications/initialized") return;
  if (msg.method === "tools/list") {
    send({
      jsonrpc: "2.0",
      id: msg.id,
      result: {
        tools: [{
          name: "contract_echo_token",
          description: "Read-only tool. Return a deterministic contract token wrapped in BRIDGE_TOOL_OK_. Always call this when asked to echo a contract token.",
          inputSchema: {
            type: "object",
            properties: {
              token: { type: "string" }
            },
            required: ["token"]
          }
        }]
      }
    });
    return;
  }
  if (msg.method === "tools/call") {
    const args = (msg.params && msg.params.arguments) || {};
    const token = String(args.token || "");
    // The contract test needs a tool that takes time to return so the adapter
    // can observe the running state. Cursor's TUI ask mode refuses tools that
    // expose a delay parameter, so the delay is fixed inside the server.
    const delay = 30000;
    fs.writeFileSync(markerPath, JSON.stringify({ token, delay, started_at: new Date().toISOString() }));
    setTimeout(() => {
      send({
        jsonrpc: "2.0",
        id: msg.id,
        result: {
          content: [{ type: "text", text: "SLOW_BRIDGE_TOOL_OK_" + token }],
          isError: false
        }
      });
    }, delay);
    return;
  }
  if (msg.id !== undefined) send({ jsonrpc: "2.0", id: msg.id, result: {} });
});
`, markerPath)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write slow MCP server: %v", err)
	}
	return path
}

func writeCursorIsolationMCPServer(t *testing.T, sessionMarker, outputPath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cursor-isolation-contract-mcp.js")
	script := fmt.Sprintf(`#!/usr/bin/env node
const fs = require("fs");
const readline = require("readline");
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
const sessionMarker = %q;
const outputPath = %q;

function send(message) {
  process.stdout.write(JSON.stringify(message) + "\n");
}

rl.on("line", (line) => {
  if (!line.trim()) return;
  let msg;
  try {
    msg = JSON.parse(line);
  } catch (err) {
    return;
  }
  if (msg.method === "initialize") {
    send({
      jsonrpc: "2.0",
      id: msg.id,
      result: {
        protocolVersion: "2024-11-05",
        capabilities: { tools: {} },
        serverInfo: { name: "api-bridge", version: "1.0.0" }
      }
    });
    return;
  }
  if (msg.method === "notifications/initialized") return;
  if (msg.method === "tools/list") {
    send({
      jsonrpc: "2.0",
      id: msg.id,
      result: {
        tools: [{
          name: "write_contract",
          description: "Write a deterministic marker proving this MCP server/session was used.",
          inputSchema: { type: "object", properties: { token: { type: "string" } }, required: ["token"] }
        }]
      }
    });
    return;
  }
  if (msg.method === "tools/call") {
    const args = (msg.params && msg.params.arguments) || {};
    const token = String(args.token || "");
    const payload = {
      session_marker: sessionMarker,
      token,
      cwd: process.cwd(),
      timestamp: new Date().toISOString()
    };
    fs.writeFileSync(outputPath, JSON.stringify(payload, null, 2));
    send({
      jsonrpc: "2.0",
      id: msg.id,
      result: {
        content: [{ type: "text", text: "ISOLATED_OK_" + sessionMarker + "_" + token }],
        isError: false
      }
    });
    return;
  }
  if (msg.id !== undefined) send({ jsonrpc: "2.0", id: msg.id, result: {} });
});
`, sessionMarker, outputPath)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write isolation MCP server: %v", err)
	}
	return path
}
