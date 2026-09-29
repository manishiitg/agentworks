package step_based_workflow

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

func TestPulseAgentMetricsCaptureExactExecutionAndJoinReview(t *testing.T) {
	workspacePath := concernsWorkspace(t)
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteLedger: %v", err)
	}
	defer ledger.Close()
	costledger.SetDefaultLedger(ledger)
	t.Cleanup(func() { costledger.SetDefaultLedger(nil) })

	now := time.Date(2026, 8, 3, 6, 0, 0, 0, time.UTC)
	for i, executionID := range []string{"pulse-review-strategy", "another-reviewer"} {
		if err := ledger.Append(costledger.Entry{
			EventID:          executionID,
			IdempotencyKey:   executionID,
			Timestamp:        now.Add(time.Duration(i) * time.Second),
			ExecutionID:      executionID,
			Scope:            "pulse",
			EffectiveModelID: "gpt-5.6-sol",
			LLMCallCount:     1,
			PromptTokens:     100 + i,
			CompletionTokens: 20 + i,
			CacheReadTokens:  1000 + i,
			TotalCostUSD:     0.25 + float64(i),
			BillingBasis:     "subscription_shadow",
		}); err != nil {
			t.Fatalf("Append cost event: %v", err)
		}
	}

	ctx := context.Background()
	if err := CompletePulseReview(ctx, workspacePath, []string{"strategy_auditor"}, "review-1", "pulse-1", "One strategic gap.", "completed"); err != nil {
		t.Fatalf("CompletePulseReview: %v", err)
	}
	if err := RecordPulseAgentMetric(ctx, workspacePath, PulseAgentMetricRecord{
		ExecutionID:     "pulse-review-strategy",
		AgentSessionID:  "session-strategy",
		PulseRunID:      "pulse-1",
		ReviewRunID:     "review-1",
		Module:          "strategy_auditor",
		Role:            "reviewer",
		Status:          "completed",
		QueuedAt:        now.Format(time.RFC3339Nano),
		StartedAt:       now.Add(time.Second).Format(time.RFC3339Nano),
		CompletedAt:     now.Add(11 * time.Second).Format(time.RFC3339Nano),
		QueueDurationMS: 1000,
		DurationMS:      10000,
	}); err != nil {
		t.Fatalf("RecordPulseAgentMetric: %v", err)
	}

	metrics, err := LoadPulseAgentMetrics(ctx, workspacePath, "pulse-1", "strategy_auditor", "reviewer", -1)
	if err != nil {
		t.Fatalf("LoadPulseAgentMetrics: %v", err)
	}
	if len(metrics) != 1 {
		t.Fatalf("metrics = %#v, want one", metrics)
	}
	metric := metrics[0]
	if metric.LLMCallCount != 1 || metric.PromptTokens != 100 || metric.CompletionTokens != 20 || metric.CacheReadTokens != 1000 || metric.TotalCostUSD != 0.25 {
		t.Fatalf("metric mixed another execution or lost usage: %#v", metric)
	}
	if metric.DurationMS != 10000 || metric.QueueDurationMS != 1000 || metric.UsageStatus != "captured" {
		t.Fatalf("metric timing/coverage = %#v", metric)
	}

	reviews, err := LoadPulseReviewReceipts(ctx, workspacePath, "strategy_auditor", -1)
	if err != nil {
		t.Fatalf("LoadPulseReviewReceipts: %v", err)
	}
	if len(reviews) != 1 || reviews[0].Metrics == nil || reviews[0].Metrics.ExecutionID != "pulse-review-strategy" {
		t.Fatalf("review did not join its metric: %#v", reviews)
	}
}

func TestPulseAgentMetricsMakeMissingUsageExplicit(t *testing.T) {
	workspacePath := concernsWorkspace(t)
	costledger.SetDefaultLedger(nil)
	if err := RecordPulseAgentMetric(context.Background(), workspacePath, PulseAgentMetricRecord{
		ExecutionID: "pulse-review-no-ledger",
		PulseRunID:  "pulse-2",
		ReviewRunID: "review-2",
		Module:      "workflow_review",
		Role:        "reviewer",
		Status:      "failed",
	}); err != nil {
		t.Fatalf("RecordPulseAgentMetric: %v", err)
	}
	metrics, err := LoadPulseAgentMetrics(context.Background(), workspacePath, "pulse-2", "workflow_review", "reviewer", -1)
	if err != nil {
		t.Fatalf("LoadPulseAgentMetrics: %v", err)
	}
	if len(metrics) != 1 || metrics[0].UsageStatus != "unavailable" || metrics[0].UsageError == "" {
		t.Fatalf("missing ledger must not look like zero usage: %#v", metrics)
	}
}

func TestPulseAgentMetricsPricesCapturedClaudeUsageWithCanonicalRateCard(t *testing.T) {
	workspacePath := concernsWorkspace(t)
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteLedger: %v", err)
	}
	defer ledger.Close()
	costledger.SetDefaultLedger(ledger)
	t.Cleanup(func() { costledger.SetDefaultLedger(nil) })
	if err := ledger.Append(costledger.Entry{
		EventID:           "pulse-claude-unpriced",
		IdempotencyKey:    "pulse-claude-unpriced",
		Timestamp:         time.Date(2026, 8, 4, 6, 0, 0, 0, time.UTC),
		ExecutionID:       "pulse-claude-unpriced",
		Scope:             "pulse",
		Provider:          "claude-code",
		EffectiveProvider: "claude-code",
		EffectiveModelID:  "claude-sonnet-5-5",
		LLMCallCount:      1,
		PromptTokens:      1_000_000,
		CompletionTokens:  1_000_000,
		BillingBasis:      "unpriced",
	}); err != nil {
		t.Fatal(err)
	}
	if err := RecordPulseAgentMetric(context.Background(), workspacePath, PulseAgentMetricRecord{
		ExecutionID: "pulse-claude-unpriced", PulseRunID: "pulse-priced", Module: "strategy_auditor", Role: "reviewer",
	}); err != nil {
		t.Fatal(err)
	}
	metrics, err := LoadPulseAgentMetrics(context.Background(), workspacePath, "pulse-priced", "strategy_auditor", "reviewer", -1)
	if err != nil || len(metrics) != 1 {
		t.Fatalf("metrics=%#v err=%v", metrics, err)
	}
	metric := metrics[0]
	if metric.UsageStatus != "captured" || metric.TotalCostUSD <= 0 || metric.Models["claude-sonnet-5-5"].PricingVersion == "" {
		t.Fatalf("Claude usage was not canonically priced: %#v", metric)
	}
}
