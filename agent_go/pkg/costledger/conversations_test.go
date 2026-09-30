package costledger

import (
	"path/filepath"
	"testing"
	"time"
)

func TestConversationCostsKeepActorsDatesAndExecutionsSeparate(t *testing.T) {
	l, err := NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	base := time.Date(2026, 9, 29, 23, 59, 0, 0, time.UTC)
	entries := []Entry{
		{EventID: "a", Timestamp: base, WorkflowID: "Workflow/a", SessionID: "chat", UserID: "alice", ExecutionID: "turn-1", Provider: "muse-cli", EffectiveModelID: "muse", PromptTokens: 100, CacheReadTokens: 80, CompletionTokens: 2, TotalCostUSD: .01, LLMCallCount: 1},
		{EventID: "b", Timestamp: base.Add(time.Second), WorkflowID: "Workflow/a", SessionID: "chat", UserID: "alice", ExecutionID: "turn-1", Provider: "muse-cli", EffectiveModelID: "muse", PromptTokens: 120, CacheReadTokens: 100, CompletionTokens: 3, TotalCostUSD: .01, LLMCallCount: 1},
		{EventID: "c", Timestamp: base.Add(time.Hour), WorkflowID: "Workflow/a", SessionID: "chat", UserID: "alice", ExecutionID: "turn-2", Provider: "muse-cli", PromptTokens: 130, CacheReadTokens: 110, TotalCostUSD: .01, LLMCallCount: 1},
		{EventID: "d", Timestamp: base, WorkflowID: "Workflow/a", SessionID: "chat", UserID: "bob", ExecutionID: "turn-1", PromptTokens: 500, LLMCallCount: 1},
		{EventID: "e", Timestamp: base, WorkflowID: "Workflow/b", SessionID: "chat", UserID: "alice", ExecutionID: "turn-1", PromptTokens: 900, LLMCallCount: 1},
	}
	for _, e := range entries {
		if err := l.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	s, err := l.Summarize("2026-09-29", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ByConversation) != 3 {
		t.Fatalf("conversations=%d", len(s.ByConversation))
	}
	a := s.ByConversation["Workflow/a\x1falice\x1fchat"]
	if a == nil || a.InputTokens != 350 || a.CacheReadTokens != 290 || a.CompletionTokens != 5 || len(a.ByExecution) != 2 {
		t.Fatalf("conversation=%+v", a)
	}
	if a.ByExecution["turn-1"].InputTokens != 220 || a.ByExecution["turn-1"].ByModel["muse"].InputTokens != 220 {
		t.Fatalf("turn=%+v", a.ByExecution["turn-1"])
	}
	if !a.FirstSeen.Equal(base) || !a.LastSeen.Equal(base.Add(time.Hour)) {
		t.Fatalf("times=%+v", a)
	}
	if got := s.ByDate["2026-09-30"].ByConversation["Workflow/a\x1falice\x1fchat"]; got.InputTokens != 130 || len(got.ByExecution) != 1 {
		t.Fatalf("day=%+v", got)
	}
	window, _, err := l.SummarizeWorkflowOverview("Workflow/a", "2026-09-30", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	if len(window.ByConversation) != 1 || window.Total.InputTokens != 850 {
		t.Fatalf("window=%+v", window)
	}
}
