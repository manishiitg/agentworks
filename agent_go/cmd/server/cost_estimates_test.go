package server

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// Earlier Cursor Auto calls, recorded without any cost, are priced at the
// estimated average as plan-equivalent estimates; nothing else is touched,
// and a second pass changes nothing.
func TestCursorAutoCallsRecordedUnpricedGetTheEstimatedAverage(t *testing.T) {
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	now := time.Now().UTC()
	unpriced := func(id, provider, model string) costledger.Entry {
		return costledger.Entry{EventID: id, IdempotencyKey: id, Timestamp: now, UserID: "u", Scope: "chat", Provider: provider, ModelID: model,
			EffectiveProvider: provider, EffectiveModelID: model, LLMCallCount: 1, PromptTokens: 1_000_000, CompletionTokens: 100_000, BillingBasis: "unpriced"}
	}
	for _, e := range []costledger.Entry{
		unpriced("auto", "cursor-cli", "auto"),
		unpriced("composer", "cursor-cli", "composer-2.5"),
		unpriced("other", "muse-cli", "auto"),
	} {
		if err := ledger.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	n, err := ledger.RepriceUnpriced(estimateCursorAutoCost)
	if err != nil || n != 1 {
		t.Fatalf("priced %d, %v", n, err)
	}
	summary, err := ledger.Summarize(now.Add(-time.Hour).Format("2006-01-02"), now.Add(24*time.Hour).Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	total := summary.Total
	if total.UnpricedCallCount != 2 || total.SubscriptionShadowUSD < 1.84 || total.SubscriptionShadowUSD > 1.86 {
		t.Fatalf("total = %+v (want 2 still unpriced and $1.85 estimated: 1M in x $1.25 + 100K out x $6)", total)
	}
	if again, _ := ledger.RepriceUnpriced(estimateCursorAutoCost); again != 0 {
		t.Fatalf("a second pass priced %d more", again)
	}
}
