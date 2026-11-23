package codexcli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

var codingCLIStress = flag.Bool("coding-cli-stress", false, "run the opt-in live native-tools stress scenario")

// TestCodexCLIStressNativeTools: Codex in "Native agent tools" mode (shell
// + subagents in the workspace-write sandbox): a todo plan, three parallel subagents
// that each read a file the parent may not read, a slow MCP call and a
// mid-turn steer must yield one final line only possible after every child
// reported; nothing may be written natively; a follow-up turn stays consistent.
func TestCodexCLIStressNativeTools(t *testing.T) {
	requireRealCodexCLIE2E(t)
	if !*codingCLIStress {
		t.Skip("opt-in: pass -coding-cli-stress")
	}
	iterations := 2
	if n, err := strconv.Atoi(os.Getenv("CODING_CLI_STRESS_ITERATIONS")); err == nil && n > 0 {
		iterations = n
	}
	for i := 1; i <= iterations; i++ {
		t.Run(fmt.Sprintf("iteration-%d", i), func(t *testing.T) {
			t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })
			workDir := t.TempDir()
			tokens := map[string]string{}
			for _, part := range []string{"A", "B", "C"} {
				tokens[part] = "PART-" + part + "-" + codexRandomHex(4)
				if err := os.WriteFile(filepath.Join(workDir, "part-"+strings.ToLower(part)+".txt"), []byte(tokens[part]+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			mcpServerPath := writeCodexSlowContractMCPServer(t, filepath.Join(t.TempDir(), "slow-tool-started"))
			mcpOverride, err := codexStringConfigOverride("mcp_servers.api-bridge.command", mcpServerPath)
			if err != nil {
				t.Fatal(err)
			}
			owner := "codex-stress-" + codexRandomHex(4)
			opts := []llmtypes.CallOption{
				WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true), WithProjectDirID(workDir),
				WithSandbox("workspace-write"), WithNativeTools(), WithApprovalPolicy("never"),
				WithReasoningEffort("low"), WithConfigOverrides([]string{mcpOverride}),
			}
			bridgeToken := "BRIDGE_" + codexRandomHex(4)
			steerToken := "STEER-" + codexRandomHex(4)
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
			defer cancel()
			prompt := "Stress test in a disposable directory. 1) Make a plan with your plan/todo tool. " +
				"2) Spawn THREE subagents in parallel: one reads part-a.txt, one part-b.txt, one part-c.txt, and each reports the exact token in its file. Do NOT read those files yourself. " +
				"3) While they run, call the api-bridge slow_contract MCP tool with token " + bridgeToken + " and delay_ms 8000. " +
				"4) Also create native-write.txt with your shell. " +
				"5) Only after all three subagents reported, reply with exactly one line: A=<token> B=<token> C=<token> MCP=<slow_contract result>."
			started := time.Now()
			go func() {
				time.Sleep(20 * time.Second)
				sendCtx, sendCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer sendCancel()
				if err := SendCodexInteractiveInput(sendCtx, owner, "Additional requirement: append STEER="+steerToken+" to the end of your final line."); err != nil {
					t.Logf("steer send error: %v", err)
				}
			}()
			resp, err := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{}).GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, opts...)
			if _, statErr := os.Stat(filepath.Join(workDir, "native-write.txt")); statErr != nil {
				t.Fatalf("workspace-write sandbox refused a native write: %v", statErr)
			}
			if err != nil {
				t.Fatalf("turn 1: %v", err)
			}
			final := resp.Choices[0].Content
			t.Logf("turn 1 in %s; final %.400s", time.Since(started).Round(time.Second), final)
			for part, tok := range tokens {
				if !strings.Contains(final, tok) {
					t.Fatalf("final missing child token %s=%s: %q", part, tok, final)
				}
			}
			if !strings.Contains(final, bridgeToken) {
				t.Fatalf("final missing MCP result: %q", final)
			}
			if !strings.Contains(final, steerToken) {
				t.Fatalf("mid-turn steer %s not honoured: %q", steerToken, final)
			}
			resp2, err := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{}).GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with only the B token from your previous answer."}}}}, opts...)
			if err != nil {
				t.Fatalf("turn 2: %v", err)
			}
			if got := resp2.Choices[0].Content; !strings.Contains(got, tokens["B"]) || strings.Contains(got, tokens["A"]) {
				t.Fatalf("turn 2 = %q, want only %s", got, tokens["B"])
			}
		})
	}
}
