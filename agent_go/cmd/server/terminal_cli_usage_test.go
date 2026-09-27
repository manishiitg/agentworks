package server

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
)

// A launch-only Claude turn never streams its statusline, so the terminal
// list fills plan usage and counters from the CLI's own report, keeping
// counters the stream already recorded.
func TestTerminalListFillsUsageFromTheCLI(t *testing.T) {
	previous := readTerminalCLIUsage
	t.Cleanup(func() { readTerminalCLIUsage = previous })
	windows := []llmtypes.RateLimitWindow{{Name: "five_hour", UsedPercent: 44}}
	readTerminalCLIUsage = func(tmux string) terminalCLIUsage {
		if tmux != "mlp-claude-code-usage-test" {
			return terminalCLIUsage{}
		}
		return terminalCLIUsage{found: true, meta: map[string]interface{}{llmtypes.RateLimitWindowsMetaKey: windows}, totalInput: 1200, costUSD: 0.4}
	}
	got := withTerminalCLIUsage(terminals.Snapshot{TmuxSession: "mlp-claude-code-usage-test", Status: terminals.Status{StatusMeta: map[string]interface{}{"effort": "high"}}})
	if got.Status.StatusMeta[llmtypes.RateLimitWindowsMetaKey] == nil || got.Status.StatusMeta["effort"] != "high" {
		t.Fatalf("status meta = %v", got.Status.StatusMeta)
	}
	if got.Status.TotalInputTokens != 1200 || got.Status.CostUSD != 0.4 {
		t.Fatalf("counters = %+v", got.Status)
	}
	kept := withTerminalCLIUsage(terminals.Snapshot{TmuxSession: "mlp-claude-code-usage-test", Status: terminals.Status{TotalInputTokens: 99, CostUSD: 1}})
	if kept.Status.TotalInputTokens != 99 || kept.Status.CostUSD != 1 {
		t.Fatalf("streamed counters must be kept: %+v", kept.Status)
	}
	if other := withTerminalCLIUsage(terminals.Snapshot{TmuxSession: "mlp-cursor-cli-int-x"}); other.Status.StatusMeta != nil {
		t.Fatalf("a CLI without a report is untouched: %+v", other.Status)
	}
}
