package server

import (
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func rewindTestHistory() []llmtypes.MessageContent {
	text := func(role llmtypes.ChatMessageType, value string) llmtypes.MessageContent {
		return llmtypes.MessageContent{Role: role, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: value}}}
	}
	return []llmtypes.MessageContent{
		text(llmtypes.ChatMessageTypeSystem, "system"),
		text(llmtypes.ChatMessageTypeHuman, "add a retry"),
		text(llmtypes.ChatMessageTypeAI, "done"),
		text(llmtypes.ChatMessageTypeHuman, "[Background work finished]"),
		text(llmtypes.ChatMessageTypeAI, "noted"),
		text(llmtypes.ChatMessageTypeHuman, "[handoff]\nadd a retry"),
		text(llmtypes.ChatMessageTypeAI, "again"),
		text(llmtypes.ChatMessageTypeHuman, "mention it in the README"),
	}
}

func TestRewindHistoryCutCountsFromNewest(t *testing.T) {
	history := rewindTestHistory()
	if cut, later := rewindHistoryCut(history, "add a retry", 1); cut != 5 || later != 1 {
		t.Fatalf("newest match: cut=%d later=%d", cut, later)
	}
	if cut, later := rewindHistoryCut(history, "add a retry", 2); cut != 1 || later != 3 {
		t.Fatalf("older match: cut=%d later=%d", cut, later)
	}
	if cut, _ := rewindHistoryCut(history, "add a retry", 3); cut != -1 {
		t.Fatalf("missing match should refuse, got %d", cut)
	}
	if cut, _ := rewindHistoryCut(history, "not sent", 1); cut != -1 {
		t.Fatalf("unknown text should refuse, got %d", cut)
	}
}

func TestChatRewindCanCutClaudeCodexAndAgy(t *testing.T) {
	if chatRewindCanCut(nil) || chatRewindCanCut(&ChatHistoryAgentRuntime{}) {
		t.Fatal("no native session cannot be cut")
	}
	runtime := func(provider string) *ChatHistoryAgentRuntime {
		r := &ChatHistoryAgentRuntime{}
		r.AgentSessionHandle = &mcpagent.AgentSessionHandle{Provider: llmtypes.CodingProviderSessionHandle{Provider: provider, NativeSessionID: "11111111-1111-4111-8111-111111111111"}}
		return r
	}
	for provider, want := range map[string]bool{"claude-code": true, "codex-cli": true, "cursor-cli": false, "agy-cli": true} {
		if got := chatRewindCanCut(runtime(provider)); got != want {
			t.Fatalf("%s: got %t want %t", provider, got, want)
		}
	}
}

func TestUIEventsBeforeKeepsEarlierRows(t *testing.T) {
	cutoff := time.Date(2026, 11, 24, 10, 42, 0, 0, time.UTC)
	events := []storeevents.Event{
		{ID: "old", Type: "agent_reply", Timestamp: cutoff.Add(-time.Minute)},
		{ID: "edited", Type: "user_message", Timestamp: cutoff},
		{ID: "later", Type: "agent_reply", Timestamp: cutoff.Add(time.Minute)},
	}
	kept, ok := uiEventsBefore(events, 0, cutoff).([]storeevents.Event)
	if !ok || len(kept) != 1 || kept[0].ID != "old" {
		t.Fatalf("kept=%v", kept)
	}
	// A sequence wins over the clock: a late-stamped earlier row stays, an early-stamped later row goes.
	sequenced := []storeevents.Event{
		{ID: "earlier", Sequence: 4, Timestamp: cutoff.Add(time.Hour)},
		{ID: "edited", Sequence: 5, Timestamp: cutoff},
		{ID: "after", Sequence: 6, Timestamp: cutoff.Add(-time.Hour)},
		{ID: "unsequenced", Timestamp: cutoff.Add(-time.Minute)},
	}
	kept, _ = uiEventsBefore(sequenced, 5, cutoff).([]storeevents.Event)
	if len(kept) != 2 || kept[0].ID != "earlier" || kept[1].ID != "unsequenced" {
		t.Fatalf("by sequence kept=%v", kept)
	}
}
