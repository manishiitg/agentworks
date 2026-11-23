package claudecode

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

var codingCLIStress = flag.Bool("coding-cli-stress", false, "run the opt-in live hybrid-mode stress scenario")

func requireClaudeStress(t *testing.T) int {
	t.Helper()
	skipClaudeInteractivePersistentE2E(t)
	if !*codingCLIStress {
		t.Skip("opt-in: pass -coding-cli-stress")
	}
	if n, err := strconv.Atoi(os.Getenv("CODING_CLI_STRESS_ITERATIONS")); err == nil && n > 0 {
		return n
	}
	return 2
}

// TestClaudeCodeTmuxStressHybridParallelAgents: in hybrid mode, three parallel
// background subagents, a todo list, a slow MCP call and a mid-turn steer must
// produce one final answer that only exists after every child reported; a
// follow-up turn on the retained session must stay consistent.
func TestClaudeCodeTmuxStressHybridParallelAgents(t *testing.T) {
	iterations := requireClaudeStress(t)
	for i := 1; i <= iterations; i++ {
		t.Run(fmt.Sprintf("iteration-%d", i), func(t *testing.T) {
			t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })
			workDir := t.TempDir()
			tokens := map[string]string{}
			for _, part := range []string{"A", "B", "C"} {
				tokens[part] = "PART-" + part + "-" + randomHex(4)
				if err := os.WriteFile(filepath.Join(workDir, "part-"+strings.ToLower(part)+".txt"), []byte(tokens[part]+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			bridgeToken := "BRIDGE-" + randomHex(4)
			steerToken := "STEER-" + randomHex(4)
			owner := "claude-stress-" + randomHex(4)
			opts := claudeNativeToolsOptions(t, workDir, owner)
			adapter := NewClaudeCodeInteractiveAdapter(defaultClaudeInteractiveTestModel, &MockLogger{})
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
			defer cancel()

			prompt := "Stress test in a disposable directory. 1) Make a todo list for this task. " +
				"2) Using the Agent tool with run_in_background true, spawn THREE subagents in parallel: one reads part-a.txt, one part-b.txt, one part-c.txt, and each reports the exact token in its file. " +
				"Do NOT read those files yourself. 3) While they run, call the api-bridge slow_contract MCP tool with token " + bridgeToken + " and delay_ms 8000. " +
				"4) Only after all three subagents have reported, reply with exactly one line: A=<token> B=<token> C=<token> MCP=<slow_contract result>."
			started := time.Now()
			go func() {
				time.Sleep(15 * time.Second)
				sendCtx, sendCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer sendCancel()
				if err := SendClaudeCodeInput(sendCtx, owner, "Additional requirement: append STEER="+steerToken+" to the end of your final line."); err != nil {
					t.Logf("steer send error (turn may already be past the steer window): %v", err)
				}
			}()
			resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, opts...)
			if err != nil {
				t.Fatalf("turn 1: %v", err)
			}
			final := firstChoiceText(resp)
			names := claudeTranscriptToolNames(t, experimentalClaudeSessionID(resp), workDir)
			t.Logf("turn 1 in %s; tools %v; final %.400s", time.Since(started).Round(time.Second), names, final)
			for part, tok := range tokens {
				if !strings.Contains(final, tok) {
					t.Fatalf("final missing child token %s=%s (turn ended before every subagent reported?): %q", part, tok, final)
				}
			}
			if !strings.Contains(final, "SLOW_BRIDGE_TOOL_OK_"+bridgeToken) {
				t.Fatalf("final missing MCP result: %q", final)
			}
			if !strings.Contains(final, steerToken) {
				t.Fatalf("mid-turn steer %s not honoured: %q", steerToken, final)
			}
			if names["Agent"]+names["Task"] < 3 {
				t.Fatalf("expected three subagents, tools: %v", names)
			}
			claudeAssertNoNativeWrites(t, names, workDir)

			resp2, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with only the B token from your previous answer."}}}}, opts...)
			if err != nil {
				t.Fatalf("turn 2: %v", err)
			}
			if got := firstChoiceText(resp2); !strings.Contains(got, tokens["B"]) || strings.Contains(got, tokens["A"]) {
				t.Fatalf("turn 2 = %q, want only %s", got, tokens["B"])
			}
		})
	}
}
