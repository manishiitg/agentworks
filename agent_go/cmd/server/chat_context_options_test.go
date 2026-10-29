package server

import (
	"testing"
	"time"

	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/nativeusage"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
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

	// The chat's terminal status reads the same figures (chatUsage): Codex's context while Codex is current,
	// nothing after the switch, and never a plan window (those are shown only by the usage chip).
	main := terminals.Snapshot{SessionID: session, TmuxSession: tmux, ExecutionKind: "main_agent", Status: terminals.Status{StatusMeta: map[string]interface{}{
		llmtypes.ContextUsedTokensMetaKey: 274000, llmtypes.ContextWindowTokensMetaKey: 475000, llmtypes.RateLimitWindowsMetaKey: codexWindows,
		llmtypes.StatusExtrasMetaKey: []interface{}{"ctx 58%", "7d 21%"},
	}}}
	status := func(subject chatUsageSubject) map[string]interface{} {
		chatUsageSubjects.Lock()
		chatUsageSubjects.bySession[session] = cachedChatUsageSubject{subject: subject, expires: time.Now().Add(time.Minute)}
		chatUsageSubjects.Unlock()
		return api.nativeUsageSnapshot(main).Status.StatusMeta
	}
	t.Cleanup(func() {
		chatUsageSubjects.Lock()
		delete(chatUsageSubjects.bySession, session)
		chatUsageSubjects.Unlock()
	})
	if meta := status(chatUsageSubject{provider: "codex-cli", model: "gpt-6.1-sol"}); chatMetaInt(meta[llmtypes.ContextUsedTokensMetaKey]) != 274000 || meta[llmtypes.RateLimitWindowsMetaKey] != nil {
		t.Fatalf("codex terminal status = %v", meta)
	}
	if meta := status(chatUsageSubject{provider: "claude-code", model: "claude-opus-5-5", switched: true}); meta[llmtypes.ContextUsedTokensMetaKey] != nil || meta[llmtypes.ContextWindowTokensMetaKey] != nil || len(meta[llmtypes.StatusExtrasMetaKey].([]interface{})) != 0 {
		t.Fatalf("after the switch to Claude, the terminal status still shows Codex usage: %v", meta)
	}

	// The owner's laptop: a Crew main chat with no model of its own follows the Crew's model, now Claude Opus
	// 5.5, while its last run was Codex. Its next message runs on Claude, so nothing is shown until Claude replies.
	profile := engineModelTestProfile()
	crewModel := func(provider, model string) productConversationBinding {
		return productConversationBinding{ProjectLLMConfig: &workflowtypes.PresetLLMConfig{Mode: "explicit", BuilderLLM: &workflowtypes.AgentLLMConfig{Provider: provider, ModelID: model}}}
	}
	chat := ProductConversationRecord{ConversationKey: "crew", SessionID: session, WorkspacePath: "Chats/Crew", Provider: "codex-cli", ModelID: "gpt-6-astra"}
	next, err := effectiveProfileChatModel(profile, crewModel("claude-code", "claude-opus-5-5"), chat, ProductAgentIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if provider, model, switched := chatUsageTarget(chat.Provider, chat.ModelID, chat.ConnectionID, next, true); !switched || provider != "claude-code" || model != "claude-opus-5-5" {
		t.Fatalf("Crew model switched to Claude: provider=%q model=%q switched=%v", provider, model, switched)
	}
	// While the Crew's model is still Codex, the chat's Codex figures are its own.
	same, err := effectiveProfileChatModel(profile, crewModel("codex-cli", "gpt-6-astra"), chat, ProductAgentIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if provider, _, switched := chatUsageTarget(chat.Provider, chat.ModelID, chat.ConnectionID, same, true); switched || provider != "codex-cli" {
		t.Fatalf("unchanged Crew model: provider=%q switched=%v", provider, switched)
	}
}
