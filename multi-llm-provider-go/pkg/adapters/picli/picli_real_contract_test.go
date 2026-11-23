package picli

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

type piRealResult struct {
	content string
	err     error
}

func requireRealPiCLIContractE2E(t *testing.T) {
	t.Helper()
	if !*codingCLIP0Live {
		t.Skip("run through the live coding CLI P0 runner")
	}
	for _, bin := range []string{"node"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("real Pi CLI tests require %s in PATH: %v", bin, err)
		}
	}
	if _, _, err := piCommandPrefix(); err != nil {
		t.Fatalf("real Pi CLI tests require the Pi executable: %v", err)
	}
	if firstNonEmptyPiTestEnv("GEMINI_API_KEY", "GOOGLE_API_KEY", "PI_API_KEY") == "" {
		cmd := exec.CommandContext(context.Background(), "pi", "--list-models")
		output, err := cmd.CombinedOutput()
		if err != nil || strings.Contains(strings.ToLower(string(output)), "no models available") || strings.TrimSpace(string(output)) == "" {
			t.Fatalf("Pi CLI P0 requires live authentication; run `pi` and use /login, or configure a provider API key (pi --list-models output: %s)", strings.TrimSpace(string(output)))
		}
	}
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", t.TempDir())
}

func newRealPiCLIAdapter(t *testing.T) *PiCLIAdapter {
	t.Helper()
	return NewPiCLIAdapter(firstNonEmptyPiTestEnv("GEMINI_API_KEY", "GOOGLE_API_KEY", "PI_API_KEY"), piRealContractModel(), &mockLogger{})
}

func piRealContractModel() string {
	if model := strings.TrimSpace(os.Getenv("PI_CLI_REAL_CONTRACT_MODEL")); model != "" {
		return model
	}
	return DefaultModelID
}

func assertPiResponseHasTranscriptUsage(t *testing.T, resp *llmtypes.ContentResponse, expectedModelRoute string) {
	t.Helper()
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil || resp.Choices[0].GenerationInfo == nil {
		t.Fatalf("missing Pi GenerationInfo: %#v", resp)
	}
	gi := resp.Choices[0].GenerationInfo
	if gi.InputTokens == nil || *gi.InputTokens == 0 || gi.OutputTokens == nil || *gi.OutputTokens == 0 || gi.TotalTokens == nil || *gi.TotalTokens == 0 {
		t.Fatalf("expected transcript-backed token usage, got GenerationInfo=%#v", gi)
	}
	additional := gi.Additional
	if got := additional["pi_token_usage_source"]; got != "transcript-file" {
		t.Fatalf("pi_token_usage_source = %#v, want transcript-file; additional=%#v", got, additional)
	}
	transcriptPath, _ := additional["pi_transcript_file"].(string)
	if strings.TrimSpace(transcriptPath) == "" {
		t.Fatalf("missing pi_transcript_file in additional=%#v", additional)
	}
	transcript := readPiTranscriptSummaryFile(transcriptPath, time.Time{})
	if transcript == nil {
		t.Fatalf("could not read Pi transcript %q", transcriptPath)
	}
	expectedProvider, expectedModel := resolvePiProviderModel(expectedModelRoute, "")
	if transcript.Provider != expectedProvider || transcript.Model != expectedModel {
		t.Fatalf("transcript route = %s/%s, want %s/%s", transcript.Provider, transcript.Model, expectedProvider, expectedModel)
	}
	t.Logf("verified Pi transcript route: %s/%s", transcript.Provider, transcript.Model)
	if got, ok := additional["cost_usd"].(float64); !ok || got <= 0 {
		t.Fatalf("cost_usd = %#v, want positive float64; additional=%#v", additional["cost_usd"], additional)
	}
	intermediate, ok := llmtypes.ExtractCodingProviderIntermediateMessages(gi)
	if !ok || len(intermediate.Messages) < 2 {
		t.Fatalf("expected Pi transcript conversation messages, got %#v ok=%v", intermediate, ok)
	}
}

func waitForPiRealFile(t *testing.T, path, label string, timeout time.Duration, errCh <-chan piRealResult) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case got := <-errCh:
			t.Fatalf("GenerateContent returned before %s: err=%v content=%q", label, got.err, got.content)
		default:
		}
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s at %s", label, path)
}

func writePiReportCWDMCPServer(t *testing.T) string {
	t.Helper()
	return writePiMCPServer(t, "PI_CWD", "")
}

func writePiEchoMCPServer(t *testing.T, prefix string) string {
	t.Helper()
	return writePiMCPServer(t, prefix, "")
}

func writePiSlowMCPServer(t *testing.T, secret, markerPath string) string {
	t.Helper()
	return writePiMCPServer(t, secret, markerPath)
}

func writePiMCPServer(t *testing.T, prefixOrSecret, markerPath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pi-contract-mcp.js")
	script := fmt.Sprintf(`#!/usr/bin/env node
const fs = require("fs");
const readline = require("readline");
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
const prefixOrSecret = %q;
const markerPath = %q;
function send(msg) { process.stdout.write(JSON.stringify(msg) + "\n"); }
function sleep(ms) { return new Promise((resolve) => setTimeout(resolve, ms)); }
rl.on("line", async (line) => {
  if (!line.trim()) return;
  let msg;
  try { msg = JSON.parse(line); } catch { return; }
  if (msg.method === "initialize") {
    return send({ jsonrpc: "2.0", id: msg.id, result: { protocolVersion: "2025-06-18", capabilities: { tools: {} }, serverInfo: { name: "api-bridge", version: "1.0.0" } } });
  }
  if (msg.method === "notifications/initialized") return;
  if (msg.method === "ping") {
    return send({ jsonrpc: "2.0", id: msg.id, result: {} });
  }
  if (msg.method === "tools/list") {
    return send({ jsonrpc: "2.0", id: msg.id, result: { tools: [
      {
        name: "report_cwd",
        description: "Return the current working directory of this MCP server process.",
        inputSchema: { type: "object", properties: {}, additionalProperties: false }
      },
      {
        name: "echo_contract",
        description: "Return a deterministic Pi bridge contract token.",
        inputSchema: { type: "object", properties: { token: { type: "string" } }, required: ["token"] }
      },
      {
        name: "slow_contract",
        description: "Sleep for the requested delay and return a deterministic Pi bridge contract token.",
        inputSchema: { type: "object", properties: { token: { type: "string" }, delay_ms: { type: "number" } }, required: ["token", "delay_ms"] }
      }
    ] } });
  }
  if (msg.method === "tools/call" && msg.params) {
    const args = msg.params.arguments || {};
    if (msg.params.name === "report_cwd") {
      return send({ jsonrpc: "2.0", id: msg.id, result: { content: [{ type: "text", text: "PI_MCP_CWD:" + process.cwd() }], isError: false } });
    }
    if (msg.params.name === "echo_contract") {
      const token = String(args.token || "");
      return send({ jsonrpc: "2.0", id: msg.id, result: { content: [{ type: "text", text: prefixOrSecret + "_" + token }], isError: false } });
    }
    if (msg.params.name === "slow_contract") {
      const token = String(args.token || "");
      const delayMS = Math.max(0, Math.min(120000, Number(args.delay_ms || 0)));
      if (markerPath) fs.writeFileSync(markerPath, "started");
      await sleep(delayMS);
      return send({ jsonrpc: "2.0", id: msg.id, result: { content: [{ type: "text", text: "SLOW_PI_TOOL_OK_" + token + "_" + prefixOrSecret }], isError: false } });
    }
  }
  if (msg.id !== undefined) {
    send({ jsonrpc: "2.0", id: msg.id, error: { code: -32601, message: "method not found" } });
  }
});
`, prefixOrSecret, markerPath)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write Pi MCP server: %v", err)
	}
	return path
}

// piLiveWorkDir is t.TempDir() for a live Pi workspace. Cleanups run LIFO, so
// a session-kill registered before t.TempDir() runs after the directory's
// RemoveAll, which then races the still-running Pi writing .pi/agentworks/
// ("unlinkat ...: directory not empty"). Registering the kill here, after the
// directory, stops Pi first.
func piLiveWorkDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() {
		_ = CleanupPiCLIInteractiveSessions(context.Background())

		deadline := time.Now().Add(5 * time.Second)
		for {
			if err := os.RemoveAll(dir); err == nil {
				time.Sleep(200 * time.Millisecond)
				if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
					return
				}
			}
			if time.Now().After(deadline) {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
	})
	return dir
}
