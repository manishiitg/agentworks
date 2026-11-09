package server

import (
	"context"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"time"
)

// crewRunTokenUsageFromSummary converts one ledger execution summary into
// the per-run usage shape stored on schedule runs. The per-model split is
// preserved so the calling workflow can attribute the same models in its
// own cost breakdown.
func crewRunTokenUsageFromSummary(summary *costledger.Summary) *workflowtypes.CrewRunTokenUsage {
	usage := &workflowtypes.CrewRunTokenUsage{}
	if summary == nil {
		return usage
	}
	for _, modelID := range summary.SortedModels() {
		aggregate := summary.ByModel[modelID]
		if aggregate == nil {
			continue
		}
		usage.AddModel(modelID, aggregate.Provider,
			aggregate.PromptTokens, aggregate.CompletionTokens, aggregate.ReasoningTokens,
			aggregate.CacheReadTokens, aggregate.CacheWriteTokens,
			aggregate.TotalCostUSD, aggregate.CallCount)
	}
	return usage
}

// accumulateAutomationTurnUsage folds one automation turn's ledger spend into
// the run total. A missing ledger, unknown query, or summarize failure is a
// silent no-op: cost recording must never fail the run it measures.
func accumulateAutomationTurnUsage(ledger *costledger.Ledger, total *workflowtypes.CrewRunTokenUsage, queryID string) {
	if ledger == nil || total == nil || queryID == "" {
		return
	}
	summary, err := ledger.SummarizeExecution(queryID)
	if err != nil || summary == nil {
		return
	}
	total.Merge(crewRunTokenUsageFromSummary(summary))
}

// Workflow timers share their source run ID with all Python descendants.
// Cost recording remains optional and never changes a business run outcome.
func automationRunUsage(ctx context.Context, ledger *costledger.Ledger, userID, runID string, since time.Time) *workflowtypes.CrewRunTokenUsage {
	if ledger == nil {
		return nil
	}
	usage := &workflowtypes.CrewRunTokenUsage{}
	err := ledger.VisitEntries(context.WithoutCancel(ctx), since.UTC().Format("2006-01-02"), time.Now().UTC().Format("2006-01-02"), func(e costledger.Entry) error {
		if e.UserID == userID && e.SourceRunID == runID {
			usage.AddModel(firstNonEmptyTrimmed(e.EffectiveModelID, e.ModelID), firstNonEmptyTrimmed(e.EffectiveProvider, e.Provider), e.PromptTokens, e.CompletionTokens, e.ReasoningTokens, e.CacheReadTokens, e.CacheWriteTokens, e.TotalCostUSD, e.LLMCallCount)
		}
		return nil
	})
	if err != nil {
		return nil
	}
	return usage
}
