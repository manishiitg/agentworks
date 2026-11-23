package codexcli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestCodexCLIRealRolloutCompletionP0 certifies done_detection and
// slow_tool_false_idle from the rollout (PLAT-354) on one retained tmux
// session: a slow MCP tool turn must not end before the tool and must be ended
// by task_complete, and a follow-up turn on the same session must be too.
func TestCodexCLIRealRolloutCompletionP0(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })
	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	mcpServerPath := writeCodexSlowContractMCPServer(t, filepath.Join(t.TempDir(), "slow-tool-started"))
	mcpCommandOverride, err := codexStringConfigOverride("mcp_servers.api-bridge.command", mcpServerPath)
	if err != nil {
		t.Fatalf("build MCP command override: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	options := []llmtypes.CallOption{
		WithInteractiveSessionID("codex-rollout-done-" + codexRandomHex(4)),
		WithPersistentInteractiveSession(true),
		WithDisableShellTool(),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
		WithConfigOverrides([]string{mcpCommandOverride}),
	}
	turn := func(prompt string) (string, map[string]interface{}, time.Duration) {
		t.Helper()
		started := time.Now()
		resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}, options...)
		if err != nil {
			t.Fatalf("turn %q: %v", prompt, err)
		}
		var additional map[string]interface{}
		if gi := resp.Choices[0].GenerationInfo; gi != nil {
			additional = gi.Additional
		}
		return strings.TrimSpace(resp.Choices[0].Content), additional, time.Since(started)
	}

	token1 := "SLOWROLLOUT_" + codexRandomHex(4)
	final1, meta1, took1 := turn(fmt.Sprintf("Call the api-bridge slow_contract MCP tool with token %s and delay_ms 12000. Do not answer until the tool returns. Then reply with exactly %s and nothing else.", token1, token1))
	if !strings.Contains(final1, token1) {
		t.Fatalf("slow-tool turn final = %q, want %s", final1, token1)
	}
	if took1 < 12*time.Second {
		t.Fatalf("slow-tool turn ended after %s, before its 12s tool could finish (false idle)", took1)
	}
	if got := meta1["codex_completion_source"]; got != codexCompletionSourceRollout {
		t.Fatalf("slow-tool turn codex_completion_source = %v, want %s", got, codexCompletionSourceRollout)
	}

	token2 := "RETAINED_" + codexRandomHex(4)
	final2, meta2, _ := turn(fmt.Sprintf("Reply with exactly %s and nothing else. Do not call tools.", token2))
	if !strings.Contains(final2, token2) || strings.Contains(final2, token1) {
		t.Fatalf("retained turn final = %q, want only %s", final2, token2)
	}
	if got := meta2["codex_completion_source"]; got != codexCompletionSourceRollout {
		t.Fatalf("retained turn codex_completion_source = %v, want %s", got, codexCompletionSourceRollout)
	}
	t.Logf("PASS: slow tool turn %s and retained follow-up both completed from rollout task_complete", took1.Round(time.Second))
}
