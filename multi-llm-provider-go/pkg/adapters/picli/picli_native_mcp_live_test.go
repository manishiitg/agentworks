package picli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Runs the real native MCP extension without a model request. A conflicting
// project server must neither replace the session bridge nor become available.
func TestPiNativeMCPRuntimeIsolation(t *testing.T) {
	if !*codingCLIP0Live && os.Getenv("RUN_PI_CLI_NATIVE_MCP_E2E") != "1" {
		t.Skip("set RUN_PI_CLI_NATIVE_MCP_E2E=1")
	}
	workDir := t.TempDir()
	ambientDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", ambientDir)
	t.Setenv("PI_OFFLINE", "1")
	foreign := []byte(`{"mcpServers":{"api-bridge":{"command":"must-not-load-project"},"foreign":{"command":"must-not-load-ambient"}}}`)
	if err := os.WriteFile(filepath.Join(ambientDir, "mcp.json"), foreign, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, ".pi"), 0700); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(workDir, ".pi", "mcp.json")
	if err := os.WriteFile(projectPath, foreign, 0600); err != nil {
		t.Fatal(err)
	}
	serverPath := filepath.Join(workDir, "canary.js")
	serverSource := strings.TrimPrefix(piMCPBridgeCanaryServerSource(), "#!/usr/bin/env node\n")
	if err := os.WriteFile(serverPath, []byte(serverSource), 0600); err != nil {
		t.Fatal(err)
	}
	opts := &llmtypes.CallOptions{}
	WithMCPConfig(fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":"node","args":[%q]}}}`, serverPath))(opts)
	WithBridgeOnlyTools(true)(opts)
	agentDir, _, cleanup, err := preparePiNativeMCPConfig(workDir, "mlp-pi-native-isolation", opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	reportPath := filepath.Join(workDir, "report.json")
	auditPath := filepath.Join(workDir, "audit.ts")
	audit := fmt.Sprintf(`import { writeFileSync } from "node:fs";
export default function(pi) {
 pi.on("session_start", () => {
  const started = Date.now();
  const timer = setInterval(() => {
   const active = pi.getActiveTools();
   if (active.includes("mcp__api_bridge__bridge_canary") || Date.now()-started > 12000) {
    clearInterval(timer);
    writeFileSync(%q, JSON.stringify({active, all: pi.getAllTools()}));
    process.exit(0);
   }
  }, 100);
 });
}`, reportPath)
	if err := os.WriteFile(auditPath, []byte(audit), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--mode", "rpc", "--no-extensions", "--no-skills", "--no-context-files", "--no-builtin-tools", "-e", "builtin:mcp", "-e", auditPath}
	env := piNativeMCPEnv(agentDir, filepath.Join(workDir, "sessions"))

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pi", args...)
	cmd.Dir = workDir
	cmd.Env = piOverrideEnv(os.Environ(), env)
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer writer.Close()
	cmd.Stdin = input
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native MCP runtime: %v\n%s", err, output)
	}
	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("missing audit report: %v\n%s", err, output)
	}
	var report struct {
		Active []string `json:"active"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Active) != 1 || report.Active[0] != "mcp__api_bridge__bridge_canary" {
		t.Fatalf("bridge-only native tool set violated: %s\n%s", raw, output)
	}
	if strings.Contains(string(raw), "foreign") {
		t.Fatalf("ambient MCP tools loaded: %s", raw)
	}
	if current, err := os.ReadFile(projectPath); err != nil || string(current) != string(foreign) {
		t.Fatal("foreign project config modified")
	}
	cleanup()
	if _, err := os.Stat(piNativeMCPConfigPath(agentDir)); !os.IsNotExist(err) {
		t.Fatalf("private config not removed: %v", err)
	}
}
