package server

import (
	"testing"
	"time"

	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/nativeusage"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
)

// PLAT-872-71: a chat switched from Codex to Claude showed Codex's context (274K / 475K) and Codex's 7-day
// window as Claude's. Usage and plan windows count only for the provider that reported them.
func TestChatContextUsageHidesPreviousProviderAfterSwitch(t *testing.T) {
	const session, tmux = "chat-switch-session", "mlp-codex-cli-switch-test"
	previous := readTerminalCLIUsage
	t.Cleanup(func() { readTerminalCLIUsage = previous })
	codexWindows := []llmtypes.RateLimitWindow{{Name: "seven_day", UsedPercent: 21, ResetsAt: time.Now().Add(6 * 24 * time.Hour)}}
	readTerminalCLIUsage = func(name string) terminalCLIUsage {
		if name != tmux {
			return terminalCLIUsage{}
		}
		return terminalCLIUsage{found: true, meta: map[string]interface{}{llmtypes.RateLimitWindowsMetaKey: codexWindows}}
	}
	api := &StreamingAPI{terminalStore: terminals.NewStore(), nativeCosts: map[string]*nativeCostAccountant{}}
	api.terminalStore.HandleEvent(session, storeevents.Event{Type: "status_line", SessionID: session, Timestamp: time.Now(), Data: &agentevents.AgentEvent{
		Type: agentevents.StreamingStatusLine,
		Data: &agentevents.StreamingStatusLineEvent{Provider: "codex-cli", TmuxSession: tmux, Metadata: map[string]interface{}{
			llmtypes.ContextUsedTokensMetaKey: 274000, llmtypes.ContextWindowTokensMetaKey: 475000, llmtypes.RateLimitWindowsMetaKey: codexWindows,
		}},
	}})
	api.nativeCosts[session] = &nativeCostAccountant{state: nativeCostState{Provider: "codex-cli"},
		usage: nativeusage.PrivateUsage{ContextUsed: 274000, ContextWindow: 475000, Limits: []nativeusage.Limit{{Name: "seven_day", UsedPercent: 21}}}}

	// The provider that reported them still sees them, so the empty answer below is the switch, not missing setup.
	if used, window, limits := api.chatContextUsage(session, "codex-cli", "gpt-6.1-sol", true); used != 274000 || window != 475000 || len(limits) != 1 {
		t.Fatalf("codex usage = %d / %d, limits %v", used, window, limits)
	}
	if used, window, limits := api.chatContextUsage(session, "claude-code", "claude-opus-5-5", true); used != 0 || window != 0 || len(limits) != 0 {
		t.Fatalf("after the switch to Claude, Codex usage must not show: %d / %d, limits %v", used, window, limits)
	}
}
