package costledger

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// Upgrade an actual older SQLite schema, append/retry the new attributed
// event, and compare streaming detail with the existing accounting summary.
func TestAttributedLedgerUpgradeAndAccounting(t *testing.T) {
	file := filepath.Join(t.TempDir(), "costs.sqlite")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(sqliteSchema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO cost_events(event_id,idempotency_key,occurred_at,user_id,scope,prompt_tokens,cache_read_tokens,operation_metadata_json)
		VALUES('legacy','legacy','2026-10-13T00:00:00Z','historical-person','chat',10,5,'{}')`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	ledger, err := NewSQLiteLedger(file)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	e := Entry{Attribution: Attribution{AttributionVersion: 1, ActorID: "caller", ProductKind: "crew", ProductID: "specialist", ProductOwnerID: "owner", ProductName: "Specialist", Activity: "function_call", OriginDashboardID: "dashboard-1", FunctionCallID: "fn-1"},
		EventID: "new", Timestamp: time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC), UserID: "owner", Scope: "schedule", SessionID: "call-session", ExecutionID: "query-1", Provider: "openai", ModelID: "test", AccountID: "global:openai", LLMCallCount: 1, PromptTokens: 10, CompletionTokens: 2, CacheReadTokens: 5, TotalCostUSD: 0.5, BillingBasis: "provider_actual", OperationMetadata: map[string]interface{}{"prompt_tokens_include_cache": true}}
	if err := ledger.Append(e); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Append(e); err != nil {
		t.Fatal(err)
	}
	count := 0
	var aggregate Aggregate
	if err := ledger.VisitEntries(t.Context(), "2026-10-13", "2026-10-13", func(got Entry) error {
		count++
		aggregate.AddEntry(got)
		if got.EventID == "legacy" && (got.AttributionVersion != 0 || got.ActorID != "" || got.ProductID != "") {
			t.Fatal("migration fabricated historical attribution", got)
		}
		if got.EventID == "new" && (got.Attribution != e.Attribution || got.UserID != "owner" || got.ActorID != "caller") {
			t.Fatal("origin attribution or runtime identity changed", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	summary, err := ledger.Summarize("2026-10-13", "2026-10-13")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || aggregate.InputTokens != 25 || aggregate.CompletionTokens != 2 || aggregate.TotalCostUSD != 0.5 || aggregate.ProviderActualCostUSD != 0.5 || aggregate != summary.Total {
		t.Fatalf("streaming detail/accounting conservation failed: count=%d detail=%+v summary=%+v", count, aggregate, summary.Total)
	}
}
