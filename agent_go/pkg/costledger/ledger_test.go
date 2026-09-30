package costledger

import (
	"fmt"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newLedgerTestServer(t *testing.T) *httptest.Server {
	return NewTestServer(t)
}

func TestSQLiteLedgerConcurrentAppendsAreIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "costs.sqlite")
	first, err := NewSQLiteLedger(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteLedger(first) error = %v", err)
	}
	defer first.Close()
	second, err := NewSQLiteLedger(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteLedger(second) error = %v", err)
	}
	defer second.Close()

	const eventCount = 50
	var wg sync.WaitGroup
	errCh := make(chan error, eventCount*2)
	for i := 0; i < eventCount; i++ {
		entry := Entry{
			EventID:                 fmt.Sprintf("event-%d", i),
			IdempotencyKey:          fmt.Sprintf("call-%d", i),
			Timestamp:               time.Date(2026, 7, 13, 10, 0, i, 0, time.UTC),
			Provider:                "codex-cli",
			ModelID:                 "auto",
			EffectiveModelID:        "gpt-5.6-sol",
			LLMCallCount:            1,
			LLMGenerationDurationMS: int64(100 + i),
			PromptTokens:            100,
			TotalCostUSD:            0.01,
			BillingBasis:            "subscription_shadow",
		}
		for _, ledger := range []*Ledger{first, second} {
			wg.Add(1)
			go func(ledger *Ledger, entry Entry) {
				defer wg.Done()
				if err := ledger.Append(entry); err != nil {
					errCh <- err
				}
			}(ledger, entry)
		}
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent Append error = %v", err)
	}

	summary, err := first.Summarize("", "")
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if summary.Total.CallCount != eventCount {
		t.Fatalf("CallCount = %d, want %d", summary.Total.CallCount, eventCount)
	}
	if summary.Total.AccountingEventCount != eventCount {
		t.Fatalf("AccountingEventCount = %d, want %d", summary.Total.AccountingEventCount, eventCount)
	}
	if got := summary.ByModel["gpt-5.6-sol"]; got == nil || got.CallCount != eventCount {
		t.Fatalf("effective model aggregate = %#v, want %d calls", got, eventCount)
	}
	if math.Abs(summary.Total.SubscriptionShadowUSD-0.5) > 1e-9 {
		t.Fatalf("SubscriptionShadowUSD = %v, want 0.5", summary.Total.SubscriptionShadowUSD)
	}
}

func TestSummarizeExecutionDoesNotMixParallelAgents(t *testing.T) {
	ledger, err := NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteLedger() error = %v", err)
	}
	defer ledger.Close()

	for i, executionID := range []string{"pulse-review-workflow", "pulse-review-strategy", "pulse-review-workflow"} {
		if err := ledger.Append(Entry{
			EventID:                 fmt.Sprintf("pulse-event-%d", i),
			IdempotencyKey:          fmt.Sprintf("pulse-call-%d", i),
			Timestamp:               time.Date(2026, 8, 3, 6, i, 0, 0, time.UTC),
			ExecutionID:             executionID,
			Scope:                   "pulse",
			EffectiveModelID:        "claude-opus-5",
			LLMCallCount:            1,
			LLMGenerationDurationMS: int64(100 + i),
			PromptTokens:            100 + i,
			CompletionTokens:        10 + i,
			CacheReadTokens:         1000 + i,
			TotalCostUSD:            float64(i+1) / 10,
			BillingBasis:            "provider_actual",
		}); err != nil {
			t.Fatalf("Append(%d) error = %v", i, err)
		}
	}

	summary, err := ledger.SummarizeExecution("pulse-review-workflow")
	if err != nil {
		t.Fatalf("SummarizeExecution() error = %v", err)
	}
	if summary.Total.CallCount != 2 || summary.Total.PromptTokens != 202 || summary.Total.CompletionTokens != 22 || summary.Total.CacheReadTokens != 2002 {
		t.Fatalf("execution summary mixed agents or lost calls: %#v", summary.Total)
	}
	if math.Abs(summary.Total.TotalCostUSD-0.4) > 1e-9 {
		t.Fatalf("TotalCostUSD = %v, want 0.4", summary.Total.TotalCostUSD)
	}
	if summary.Total.LLMGenerationDurationMS != 202 {
		t.Fatalf("LLMGenerationDurationMS = %d, want 202", summary.Total.LLMGenerationDurationMS)
	}
}

func TestSummarizeWorkflowGroupsScopeAndChildExecution(t *testing.T) {
	ledger, err := NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteLedger() error = %v", err)
	}
	defer ledger.Close()

	entries := []Entry{
		{EventID: "builder", IdempotencyKey: "builder", WorkflowID: "Workflow/demo", Scope: "builder", ExecutionID: "builder-turn", LLMCallCount: 1, TotalCostUSD: 1},
		{EventID: "pulse-a", IdempotencyKey: "pulse-a", WorkflowID: "Workflow/demo", Scope: "pulse", ExecutionID: "engineering-review", LLMCallCount: 1, TotalCostUSD: 2},
		{EventID: "pulse-b", IdempotencyKey: "pulse-b", WorkflowID: "Workflow/demo", Scope: "pulse", ExecutionID: "engineering-review", LLMCallCount: 1, TotalCostUSD: 3},
		{EventID: "run-a", IdempotencyKey: "run-a", WorkflowID: "Workflow/demo", Scope: "workflow_execution", RunID: "run-a", ExecutionID: "step-a", LLMCallCount: 1, TotalCostUSD: 4},
		{EventID: "run-a-second-call", IdempotencyKey: "run-a-second-call", WorkflowID: "Workflow/demo", Scope: "workflow_execution", RunID: "run-a", ExecutionID: "step-b", LLMCallCount: 1, TotalCostUSD: 5},
		{EventID: "other", IdempotencyKey: "other", WorkflowID: "Workflow/other", Scope: "pulse", ExecutionID: "engineering-review", LLMCallCount: 1, TotalCostUSD: 100},
	}
	for i := range entries {
		entries[i].Timestamp = time.Date(2026, 8, 12, 1, i, 0, 0, time.UTC)
		if err := ledger.Append(entries[i]); err != nil {
			t.Fatalf("Append(%q) error = %v", entries[i].EventID, err)
		}
	}

	summary, err := ledger.SummarizeWorkflow("Workflow/demo")
	if err != nil {
		t.Fatalf("SummarizeWorkflow() error = %v", err)
	}
	if summary.Total.CallCount != 5 || summary.Total.TotalCostUSD != 15 {
		t.Fatalf("workflow filter failed: total = %#v", summary.Total)
	}
	if got := summary.ByScope["pulse"]; got == nil || got.CallCount != 2 || got.TotalCostUSD != 5 {
		t.Fatalf("pulse scope = %#v, want 2 calls and $5", got)
	}
	if got := summary.ByScope["pulse"].ByExecution["engineering-review"]; got == nil || got.CallCount != 2 || got.TotalCostUSD != 5 {
		t.Fatalf("pulse child execution = %#v, want 2 calls and $5", got)
	}
	day := summary.ByDate["2026-08-12"]
	if day == nil || day.ByScope["pulse"].TotalCostUSD != 5 || day.ByScope["workflow_execution"].TotalCostUSD != 9 || day.WorkflowRunCount != 1 {
		t.Fatalf("daily category attribution = %#v, want pulse=$5 workflow=$9 and one workflow run", day)
	}
}

func TestSummarizeWorkflowOverviewKeepsExactTotalsAndBoundsDailyDetails(t *testing.T) {
	ledger, err := NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteLedger() error = %v", err)
	}
	defer ledger.Close()

	entries := []Entry{
		{EventID: "old-builder", IdempotencyKey: "old-builder", Timestamp: time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC), WorkflowID: "Workflow/demo", Scope: "builder", ExecutionID: "old-turn", LLMCallCount: 1, LLMGenerationDurationMS: 1000, PromptTokens: 10, TotalCostUSD: 1, BillingBasis: "provider_actual"},
		{EventID: "recent-pulse", IdempotencyKey: "recent-pulse", Timestamp: time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC), WorkflowID: "Workflow/demo", Scope: "pulse", ExecutionID: "review", LLMCallCount: 2, LLMGenerationDurationMS: 2000, PromptTokens: 20, TotalCostUSD: 2, BillingBasis: "token_estimate"},
		{EventID: "recent-run", IdempotencyKey: "recent-run", Timestamp: time.Date(2026, 8, 11, 1, 0, 0, 0, time.UTC), WorkflowID: "Workflow/demo", Scope: "workflow_execution", RunID: "run-1", ExecutionID: "step-1", LLMCallCount: 3, LLMGenerationDurationMS: 3000, PromptTokens: 30, TotalCostUSD: 3, BillingBasis: "subscription_shadow"},
		{EventID: "other-workflow", IdempotencyKey: "other-workflow", Timestamp: time.Date(2026, 8, 11, 1, 0, 0, 0, time.UTC), WorkflowID: "Workflow/other", Scope: "pulse", LLMCallCount: 99, TotalCostUSD: 99},
	}
	for _, entry := range entries {
		if err := ledger.Append(entry); err != nil {
			t.Fatalf("Append(%q) error = %v", entry.EventID, err)
		}
	}

	summary, hasMore, err := ledger.SummarizeWorkflowOverview("Workflow/demo", "2026-08-01", "2026-08-16")
	if err != nil {
		t.Fatalf("SummarizeWorkflowOverview() error = %v", err)
	}
	if !hasMore {
		t.Fatal("hasMore = false, want true for the June event")
	}
	if summary.Total.CallCount != 6 || summary.Total.PromptTokens != 60 || summary.Total.TotalCostUSD != 6 || summary.Total.LLMGenerationDurationMS != 6000 {
		t.Fatalf("all-time total = %#v, want exact totals for all three demo events", summary.Total)
	}
	if len(summary.ByDate) != 2 || summary.ByDate["2026-06-01"] != nil {
		t.Fatalf("ByDate = %#v, want only the bounded August window", summary.ByDate)
	}
	if got := summary.ByDate["2026-08-10"].ByScope["pulse"].ByExecution["review"]; got == nil || got.TotalCostUSD != 2 {
		t.Fatalf("recent execution detail = %#v, want the review child", got)
	}
	if got := summary.ByScope["builder"]; got == nil || got.TotalCostUSD != 1 || len(got.ByExecution) != 0 {
		t.Fatalf("compact all-time builder scope = %#v, want $1 without unbounded execution detail", got)
	}
	if summary.Total.ProviderActualCostUSD != 1 || summary.Total.TokenEstimateCostUSD != 2 || summary.Total.SubscriptionShadowUSD != 3 {
		t.Fatalf("billing totals = %#v, want provider=$1 estimate=$2 shadow=$3", summary.Total)
	}
}

func TestSummarizeWorkflowScopeWindowIsolatesOnePulsePass(t *testing.T) {
	ledger, err := NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteLedger() error = %v", err)
	}
	defer ledger.Close()

	base := time.Date(2026, 8, 15, 1, 0, 0, 0, time.UTC)
	entries := []Entry{
		{EventID: "prior-pulse", IdempotencyKey: "prior-pulse", Timestamp: base.Add(-time.Minute), WorkflowID: "Workflow/demo", Scope: "pulse", LLMCallCount: 1, TotalCostUSD: 50},
		{EventID: "current-gate", IdempotencyKey: "current-gate", Timestamp: base.Add(time.Minute), WorkflowID: "Workflow/demo", Scope: "pulse", LLMCallCount: 1, TotalCostUSD: 2},
		{EventID: "current-review", IdempotencyKey: "current-review", Timestamp: base.Add(2 * time.Minute), WorkflowID: "Workflow/demo", Scope: "pulse", LLMCallCount: 2, TotalCostUSD: 3},
		{EventID: "workflow", IdempotencyKey: "workflow", Timestamp: base.Add(2 * time.Minute), WorkflowID: "Workflow/demo", Scope: "workflow_execution", LLMCallCount: 1, TotalCostUSD: 100},
		{EventID: "other-workflow", IdempotencyKey: "other-workflow", Timestamp: base.Add(2 * time.Minute), WorkflowID: "Workflow/other", Scope: "pulse", LLMCallCount: 1, TotalCostUSD: 200},
		{EventID: "after-window", IdempotencyKey: "after-window", Timestamp: base.Add(4 * time.Minute), WorkflowID: "Workflow/demo", Scope: "pulse", LLMCallCount: 1, TotalCostUSD: 75},
	}
	for _, entry := range entries {
		if err := ledger.Append(entry); err != nil {
			t.Fatalf("Append(%q) error = %v", entry.EventID, err)
		}
	}

	summary, err := ledger.SummarizeWorkflowScopeWindow("Workflow/demo", "pulse", base, base.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("SummarizeWorkflowScopeWindow() error = %v", err)
	}
	if summary.Total.CallCount != 3 || summary.Total.TotalCostUSD != 5 {
		t.Fatalf("window summary = %#v, want 3 calls and $5", summary.Total)
	}
}

func TestSQLiteLedgerMigrationQuarantinesMalformedRowsAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, "costs.jsonl")
	valid := `{"ts":"2026-07-13T23:59:59Z","provider":"anthropic","model_id":"claude-sonnet","prompt_tokens":10,"total_cost_usd":0.02,"cost_usd_source":"estimated"}`
	if err := os.WriteFile(legacyPath, []byte(valid+"\n{not-json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	ledger, err := NewSQLiteLedger(filepath.Join(root, "costs.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteLedger() error = %v", err)
	}
	defer ledger.Close()

	report, err := ledger.MigrateLegacyJSONL(legacyPath)
	if err != nil {
		t.Fatalf("MigrateLegacyJSONL(first) error = %v", err)
	}
	if report.Imported != 1 || report.Quarantined != 1 {
		t.Fatalf("first report = %#v, want 1 imported and 1 quarantined", report)
	}
	report, err = ledger.MigrateLegacyJSONL(legacyPath)
	if err != nil {
		t.Fatalf("MigrateLegacyJSONL(second) error = %v", err)
	}
	if report.Duplicates != 1 || report.Imported != 0 || report.Quarantined != 0 {
		t.Fatalf("second report = %#v, want one duplicate only", report)
	}

	summary, err := ledger.Summarize("2026-07-13", "2026-07-13")
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if summary.Total.CallCount != 1 || summary.Coverage.QuarantinedEventCount != 1 {
		t.Fatalf("summary = %#v, want one call and one quarantined row", summary)
	}
	if summary.Total.TokenEstimateCostUSD != 0.02 {
		t.Fatalf("TokenEstimateCostUSD = %v, want 0.02", summary.Total.TokenEstimateCostUSD)
	}
}

func TestSQLiteLedgerSeparatesCallsFromToolEventsAndUTCDateBounds(t *testing.T) {
	ledger, err := NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteLedger() error = %v", err)
	}
	defer ledger.Close()
	entries := []Entry{
		{
			EventID: "llm", IdempotencyKey: "llm", Timestamp: time.Date(2026, 7, 13, 23, 59, 59, 0, time.UTC),
			Provider: "openai", ModelID: "gpt-5.6-sol", LLMCallCount: 2, BillingBasis: "unpriced",
		},
		{
			EventID: "tool", IdempotencyKey: "tool", Timestamp: time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC),
			Component: "tool:image_gen", ToolName: "image_gen", TotalCostUSD: 0.04, BillingBasis: "provider_actual",
		},
	}
	for _, entry := range entries {
		if err := ledger.Append(entry); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	dayOne, err := ledger.Summarize("2026-07-13", "2026-07-13")
	if err != nil {
		t.Fatalf("Summarize(day one) error = %v", err)
	}
	if dayOne.Total.CallCount != 2 || dayOne.Total.AccountingEventCount != 1 || dayOne.Total.UnpricedCallCount != 2 {
		t.Fatalf("day one total = %#v", dayOne.Total)
	}
	dayTwo, err := ledger.Summarize("2026-07-14", "2026-07-14")
	if err != nil {
		t.Fatalf("Summarize(day two) error = %v", err)
	}
	if dayTwo.Total.CallCount != 0 || dayTwo.Total.AccountingEventCount != 1 || dayTwo.Total.ProviderActualCostUSD != 0.04 {
		t.Fatalf("day two total = %#v", dayTwo.Total)
	}
}

func TestSQLiteLedgerRejectsInvalidDateBounds(t *testing.T) {
	ledger, err := NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	if _, err := ledger.Summarize("2026-07-14", "2026-07-13"); err == nil {
		t.Fatal("reversed date bounds should be rejected")
	}
	if _, err := ledger.Summarize("not-a-date", ""); err == nil {
		t.Fatal("invalid date should be rejected")
	}
}

func TestLedgerAppendAndSummarizeViaWorkspaceAPI(t *testing.T) {
	server := newLedgerTestServer(t)
	defer server.Close()

	ledger := NewLedger(server.URL)
	if err := ledger.Append(Entry{
		Timestamp:        time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC),
		ModelID:          "gpt-5.2",
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalCostUSD:     0.12,
	}); err != nil {
		t.Fatalf("Append(first) error = %v", err)
	}
	if err := ledger.Append(Entry{
		Timestamp:        time.Date(2026, 4, 14, 10, 0, 0, 0, time.UTC),
		ModelID:          "gpt-5.2",
		PromptTokens:     4,
		CompletionTokens: 6,
		TotalCostUSD:     0.08,
	}); err != nil {
		t.Fatalf("Append(second) error = %v", err)
	}

	summary, err := ledger.Summarize("2026-04-13", "2026-04-14")
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if summary.Total.CallCount != 2 {
		t.Fatalf("summary.Total.CallCount = %d, want 2", summary.Total.CallCount)
	}
	if got := summary.ByModel["gpt-5.2"].TotalCostUSD; got != 0.20 {
		t.Fatalf("summary.ByModel total cost = %v, want 0.20", got)
	}
	if len(summary.SortedDates()) != 2 {
		t.Fatalf("SortedDates() len = %d, want 2", len(summary.SortedDates()))
	}
}

func TestInputTokensCountCacheOnceAcrossMixedProviders(t *testing.T) {
	ledger, err := NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	// Historical Muse, inclusive structured CLI, exclusive native CLI, and
	// old exclusive Anthropic input must normalize before merging providers.
	entries := []Entry{
		{Provider: "muse-cli", PromptTokens: 100, CacheReadTokens: 80},
		{Provider: "claude-code", PromptTokens: 200, CacheReadTokens: 120, CacheWriteTokens: 30, OperationMetadata: map[string]interface{}{"prompt_tokens_include_cache": true}},
		{Provider: "codex-cli", PromptTokens: 40, CacheReadTokens: 60, OperationMetadata: map[string]interface{}{"prompt_tokens_include_cache": false}},
		{Provider: "anthropic", PromptTokens: 10, CacheReadTokens: 20, CacheWriteTokens: 5},
	}
	for i, entry := range entries {
		entry.EventID = fmt.Sprintf("input-%d", i)
		entry.Timestamp = time.Now().UTC()
		entry.WorkflowID = "Workflow/test"
		entry.UserID = "alice"
		entry.ModelID = entry.Provider
		entry.LLMCallCount = 1
		entry.CompletionTokens = 7
		entry.BillingBasis = "token_estimate"
		entry.TotalCostUSD = 0.1
		if err := ledger.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := ledger.Summarize("", "")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total.InputTokens != 435 || summary.Total.PromptTokens != 350 || summary.Total.CompletionTokens != 28 {
		t.Fatalf("unexpected normalized usage: %+v", summary.Total)
	}
	if math.Abs(summary.Total.TotalCostUSD-0.4) > 1e-9 {
		t.Fatal("normalization changed costs")
	}
	var merged Aggregate
	for _, model := range summary.ByModel {
		merged.Merge(*model)
	}
	if merged.InputTokens != summary.Total.InputTokens || summary.ByWorkflow["Workflow/test"].InputTokens != 435 || summary.ByWorkflowUser["Workflow/test"]["alice"].InputTokens != 435 {
		t.Fatalf("input tokens lost in merged or attributed summaries: %+v", merged)
	}
}

func TestMissingUsageIsASubsetOfUnpricedCalls(t *testing.T) {
	var aggregate Aggregate
	aggregate.add(Entry{LLMCallCount: 1, BillingBasis: "unpriced"})
	aggregate.add(Entry{LLMCallCount: 1, BillingBasis: "unpriced", PromptTokens: 10})
	aggregate.add(Entry{LLMCallCount: 1, BillingBasis: "provider_actual", TotalCostUSD: 0.1})
	aggregate.add(Entry{Component: "mcp:github", BillingBasis: "unpriced"})
	var merged Aggregate
	merged.Merge(aggregate)
	if merged.UnpricedCallCount != 2 || merged.MissingUsageCallCount != 1 {
		t.Fatalf("incorrect pricing coverage: %+v", merged)
	}
}
