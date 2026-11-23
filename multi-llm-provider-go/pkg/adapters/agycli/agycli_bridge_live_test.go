package agycli

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

// Live bridge P0 proofs for agy: mcp_bridge (canary server mounts through
// WithMCPConfig and the model routes a call through it) and
// slow_tool_false_idle (a 25s tool does not trip early completion).
//
// Mount lifecycle is adapter-owned: each run writes a private 0600 MCP
// config under its own HOME. The native-tool-mode test proves the workspace
// PreToolUse gate keeps the bridge available while denying native writes.

func agyMCPBridgeCanaryServerSource() string {
	return `#!/usr/bin/env node
const fs = require("fs");
const readline = require("readline");

const logPath = process.env.AGY_MCP_CANARY_LOG;
const sleepMs = parseInt(process.env.AGY_MCP_SLEEP_MS || "0", 10);
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
        serverInfo: { name: "agy-mcp-canary", version: "0.1.0" }
      });
      break;
    case "ping":
      result(msg.id, {});
      break;
    case "tools/list":
      result(msg.id, {
        tools: [{
          name: "bridge_canary",
          description: "Return a fixed canary proving the agy MCP bridge is mounted. Call it when asked to use the MCP gateway.",
          inputSchema: { type: "object", properties: {}, additionalProperties: false }
        }]
      });
      break;
    case "tools/call": {
      if (msg.params?.name !== "bridge_canary") {
        error(msg.id, -32602, "unknown tool");
        break;
      }
      const respond = () => {
        if (logPath) {
          fs.appendFileSync(logPath, JSON.stringify({ tool: msg.params.name, ts: Date.now() }) + "\n");
        }
        result(msg.id, {
          content: [{ type: "text", text: "AGY_MCP_BRIDGE_OK" }],
          isError: false
        });
      };
      if (sleepMs > 0) {
        setTimeout(respond, sleepMs);
      } else {
        respond();
      }
      break;
    }
    default:
      error(msg.id, -32601, "method not found");
  }
});
`
}

func agyWriteCanaryServer(t *testing.T, workDir, file string) (serverPath, logPath string) {
	t.Helper()
	serverPath = filepath.Join(workDir, file)
	logPath = serverPath + ".calls.jsonl"
	if err := os.WriteFile(serverPath, []byte(agyMCPBridgeCanaryServerSource()), 0o700); err != nil {
		t.Fatalf("write canary server: %v", err)
	}
	return serverPath, logPath
}

func agyCanaryMCPConfig(serverPath, logPath, sleepMs string) string {
	return fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":"node","args":[%q],"env":{"AGY_MCP_CANARY_LOG":%q,"AGY_MCP_SLEEP_MS":%q}}}}`,
		serverPath, logPath, sleepMs)
}

func agyAssertNoMountLeak(t *testing.T) {
	t.Helper()
	out, err := exec.CommandContext(context.Background(), "agy", "mcp", "list").CombinedOutput()
	if err != nil {
		t.Fatalf("agy mcp list: %v\n%s", err, out)
	}
	// A different live backend may still own a legacy global mount. This
	// process must never add one: its mounts live only in private HOME dirs.
	if strings.Contains(string(out), fmt.Sprintf("-%d-", os.Getpid())) {
		t.Fatalf("mount leak: mounted server survived the turn:\n%s", out)
	}
}

func TestAgyCLIRealBestEffortToolRestrictions(t *testing.T) {
	requireRealAgyCLIE2E(t)
	// Best-effort posture, denial half: an unmounted turn that reaches for
	// a native tool is auto-denied with the action named (nothing runs).
	// The companion bridge proof shows tools flowing once the caller
	// explicitly mounts. No selective containment is claimed.
	workDir := t.TempDir()
	target := filepath.Join(workDir, "should_not_exist.txt")

	adapter := NewAgyCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Create the file "+target+" with exactly the word HI in it, using your file tools."),
	}, WithWorkingDir(workDir))
	if err == nil {
		t.Fatal("unmounted native-tool turn succeeded, want auto-denied failure")
	}
	if !strings.Contains(err.Error(), "auto-denied") {
		t.Fatalf("error = %v, want auto-denied posture named", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("denied turn left file behind: stat err = %v", statErr)
	}
	t.Logf("denial posture held: %v", err)
}

func TestAgyCLIRealMCPBridgeContract(t *testing.T) {
	requireRealAgyCLIE2E(t)
	workDir := t.TempDir()
	serverPath, logPath := agyWriteCanaryServer(t, workDir, "agy-mcp-canary-server.js")

	adapter := NewAgyCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Use the MCP gateway only. Call the MCP tool bridge_canary, then reply exactly with the tool output text."),
	},
		WithWorkingDir(workDir),
		WithMCPConfig(agyCanaryMCPConfig(serverPath, logPath, "0")),
	)
	if err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}
	if content := strings.TrimSpace(resp.Choices[0].Content); !strings.Contains(content, "AGY_MCP_BRIDGE_OK") {
		t.Fatalf("content = %q, want canary tool output", content)
	}
	calls, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(calls), "bridge_canary") {
		t.Fatalf("canary log = %q, err = %v, want recorded tool call", string(calls), err)
	}
	agyAssertNoMountLeak(t)
}

func TestAgyCLIRealNativeToolModeBridgeAndDenial(t *testing.T) {
	requireRealAgyCLIE2E(t)
	workDir := t.TempDir()
	serverPath, logPath := agyWriteCanaryServer(t, workDir, "agy-mcp-native-mode-server.js")
	config := agyCanaryMCPConfig(serverPath, logPath, "0")
	adapter := NewAgyCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Use the MCP gateway only. Call bridge_canary, then reply with its output."),
	}, WithWorkingDir(workDir), WithMCPConfig(config), WithNativeToolsMode("mcp_only"))
	if err != nil {
		t.Fatalf("MCP-only bridge turn: %v", err)
	}
	if !strings.Contains(resp.Choices[0].Content, "AGY_MCP_BRIDGE_OK") {
		t.Fatalf("MCP bridge response = %q", resp.Choices[0].Content)
	}
	target := filepath.Join(workDir, "native-must-not-write.txt")
	deniedResp, deniedErr := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Use only your native file writing tool to create "+target+" with exactly HI. Do not call MCP or use a command. If blocked, say so."),
	}, WithWorkingDir(workDir), WithMCPConfig(config), WithNativeToolsMode("mcp_only"))
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("MCP-only mode allowed native write: stat err = %v", err)
	}
	if deniedErr == nil {
		handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(deniedResp)
		if !ok || handle.NativeSessionID == "" {
			t.Fatal("denied native turn returned no AGY conversation id")
		}
		var attempted bool
		for _, call := range agyTurnToolCallsSince(handle.NativeSessionID, 0) {
			if call.Name == "write_to_file" {
				attempted = true
			}
		}
		if !attempted {
			t.Fatal("denial proof did not record a native write attempt")
		}
	} else if !strings.Contains(strings.ToLower(deniedErr.Error()), "denied") {
		t.Fatalf("native turn failed for an unrelated reason: %v", deniedErr)
	}
	agyAssertNoMountLeak(t)
}

func TestAgyCLIRealConcurrentMountIsolationContract(t *testing.T) {
	requireRealAgyCLIE2E(t)
	// Two concurrent mounted turns must both route through their own bridge
	// with no cross-talk or global mount leak.
	adapter := NewAgyCLIAdapter("", "", nil)
	type mountedResult struct {
		content string
		calls   string
		err     error
	}
	type mountedLane struct {
		workDir    string
		serverPath string
		logPath    string
	}
	setup := func(tag string) mountedLane {
		workDir := t.TempDir()
		serverPath, logPath := agyWriteCanaryServer(t, workDir, "canary-"+tag+"-server.js")
		return mountedLane{workDir: workDir, serverPath: serverPath, logPath: logPath}
	}
	laneA, laneB := setup("a"), setup("b")
	run := func(lane mountedLane) mountedResult {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
			llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Use the MCP gateway only. Call the MCP tool bridge_canary, then reply exactly with the tool output text."),
		},
			WithWorkingDir(lane.workDir),
			WithMCPConfig(agyCanaryMCPConfig(lane.serverPath, lane.logPath, "0")),
		)
		if err != nil {
			return mountedResult{err: err}
		}
		raw, _ := os.ReadFile(lane.logPath)
		return mountedResult{content: resp.Choices[0].Content, calls: string(raw)}
	}
	results := make(chan mountedResult, 2)
	go func() { results <- run(laneA) }()
	go func() { results <- run(laneB) }()
	first, second := <-results, <-results
	if first.err != nil {
		t.Fatalf("concurrent mounted turn A error = %v", first.err)
	}
	if second.err != nil {
		t.Fatalf("concurrent mounted turn B error = %v", second.err)
	}
	for i, r := range []mountedResult{first, second} {
		if !strings.Contains(r.content, "AGY_MCP_BRIDGE_OK") {
			t.Fatalf("turn %d content = %q, want canary output", i, r.content)
		}
		if !strings.Contains(r.calls, "bridge_canary") {
			t.Fatalf("turn %d log = %q, want own recorded call", i, r.calls)
		}
	}
	agyAssertNoMountLeak(t)
}

func TestAgyCLIRealSlowToolFalseIdleContract(t *testing.T) {
	requireRealAgyCLIE2E(t)
	workDir := t.TempDir()
	serverPath, logPath := agyWriteCanaryServer(t, workDir, "agy-mcp-slow-server.js")

	adapter := NewAgyCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	start := time.Now()
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Call the MCP tool bridge_canary (it is slow; wait for it), then reply exactly with the tool output text."),
	},
		WithWorkingDir(workDir),
		WithMCPConfig(agyCanaryMCPConfig(serverPath, logPath, "25000")),
	)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}
	if content := strings.TrimSpace(resp.Choices[0].Content); !strings.Contains(content, "AGY_MCP_BRIDGE_OK") {
		t.Fatalf("content = %q, want slow canary tool output", content)
	}
	if elapsed < 20*time.Second {
		t.Fatalf("turn finished in %s with a 25s tool, want no false idle", elapsed)
	}
	t.Logf("slow-tool turn completed in %s", elapsed)
	agyAssertNoMountLeak(t)
}
