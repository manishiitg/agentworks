package costledger

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"
)

// A chat's cost counts its helper agents separately, leaves unpriced calls out of
// the dollar total, and labels subscription-equivalent spend as an estimate (real
// failure mode: one unlabelled dollar figure for spend nobody was charged).
func TestSessionCostSeparatesHelpersUnpricedAndEstimates(t *testing.T) {
	ledger, err := NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteLedger() error = %v", err)
	}
	defer ledger.Close()
	entries := []Entry{
		{SessionID: "chat-1", EffectiveModelID: "model-a", BillingBasis: "provider_actual", TotalCostUSD: 1.0, PromptTokens: 100, CacheReadTokens: 300},
		{SessionID: "helper-1", Attribution: Attribution{ParentSessionID: "chat-1"}, EffectiveModelID: "model-b", BillingBasis: "provider_actual", TotalCostUSD: 0.25, PromptTokens: 100},
		{SessionID: "chat-1", EffectiveModelID: "model-a", BillingBasis: "unpriced", PromptTokens: 50},
		{SessionID: "other", EffectiveModelID: "model-a", BillingBasis: "provider_actual", TotalCostUSD: 9, PromptTokens: 100},
	}
	for i, e := range entries {
		e.EventID = fmt.Sprintf("event-%d", i)
		e.IdempotencyKey = fmt.Sprintf("call-%d", i)
		e.Timestamp = time.Date(2026, 11, 1, 0, i, 0, 0, time.UTC)
		e.LLMCallCount = 1
		if err := ledger.Append(e); err != nil {
			t.Fatalf("Append(%d) error = %v", i, err)
		}
	}
	got, err := ledger.SessionCost("chat-1")
	if err != nil {
		t.Fatalf("SessionCost() error = %v", err)
	}
	if math.Abs(got.TotalUSD-1.25) > 1e-9 || math.Abs(got.Current.HelperUSD-0.25) > 1e-9 {
		t.Fatalf("total/helper = %v/%v, want 1.25/0.25 (other chats must not count)", got.TotalUSD, got.Current.HelperUSD)
	}
	if got.UnpricedCalls != 1 || got.Basis != "actual" || got.Earlier != nil || len(got.Current.Models) != 2 || got.Current.Models[0].Model != "model-a" {
		t.Fatalf("unexpected summary: %#v", got)
	}
	if got.Current.CachePercent != 54 { // 300 cached of 550 input tokens (400 + 100 + 50)
		t.Fatalf("CachePercent = %d, want 54", got.Current.CachePercent)
	}
}

// A chat that moved from one coding app to another keeps one session; the cost
// it shows now must not include what it spent on the old app (real report: a
// chat on Claude listed GPT models it no longer used at the top).
func TestSessionCostSplitsAtTheLastAppSwitch(t *testing.T) {
	ledger, err := NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteLedger() error = %v", err)
	}
	defer ledger.Close()
	entries := []Entry{
		{SessionID: "chat-1", EffectiveProvider: "codex-cli", EffectiveModelID: "model-old", TotalCostUSD: 5, PromptTokens: 100},
		{SessionID: "chat-1", EffectiveProvider: "codex-cli", EffectiveModelID: "model-old", BillingBasis: "unpriced", PromptTokens: 10},
		{SessionID: "chat-1", EffectiveProvider: "claude-code", EffectiveModelID: "model-new", TotalCostUSD: 2, PromptTokens: 40, CacheReadTokens: 60, CompletionTokens: 7},
		{SessionID: "helper-1", Attribution: Attribution{ParentSessionID: "chat-1"}, EffectiveProvider: "codex-cli", EffectiveModelID: "model-old", TotalCostUSD: 1, PromptTokens: 10},
	}
	for i, e := range entries {
		e.EventID = fmt.Sprintf("event-%d", i)
		e.IdempotencyKey = fmt.Sprintf("call-%d", i)
		e.Timestamp = time.Date(2026, 11, 1+i, 0, 0, 0, 0, time.UTC)
		if e.BillingBasis == "" {
			e.BillingBasis = "subscription_shadow"
		}
		e.LLMCallCount = 1
		if err := ledger.Append(e); err != nil {
			t.Fatalf("Append(%d) error = %v", i, err)
		}
	}
	got, err := ledger.SessionCost("chat-1")
	if err != nil {
		t.Fatalf("SessionCost() error = %v", err)
	}
	// A helper on another app after the move does not move the chat back.
	if got.TotalUSD != 8 || got.Current.TotalUSD != 3 || got.Current.HelperUSD != 1 || got.Earlier == nil || got.Earlier.TotalUSD != 5 {
		t.Fatalf("periods = total %v, current %#v, earlier %#v", got.TotalUSD, got.Current, got.Earlier)
	}
	if got.Current.Providers[0] != "claude-code" || got.Earlier.Providers[0] != "codex-cli" || got.Basis != "estimate" {
		t.Fatalf("providers/basis = %v / %v / %s", got.Current.Providers, got.Earlier.Providers, got.Basis)
	}
	// Real report: calls without a price from before the move were listed under the spend since it.
	if got.Current.UnpricedCalls != 0 || got.Earlier.UnpricedCalls != 1 {
		t.Fatalf("unpriced calls = current %d, earlier %d; want 0 and 1", got.Current.UnpricedCalls, got.Earlier.UnpricedCalls)
	}
	if tokens := got.Current.Tokens; tokens.NewInput != 50 || tokens.CacheRead != 60 || tokens.Output != 7 {
		t.Fatalf("current tokens = %#v, want 50 new input, 60 from cache, 7 output", tokens)
	}
}

func TestSessionCostQueriesUseSessionIndexes(t *testing.T) {
	ledger, err := NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteLedger() error = %v", err)
	}
	defer ledger.Close()
	store := ledger.db.(*sqliteLedger)
	queries := []struct {
		name string
		sql  string
		args []any
	}{
		{
			name: "session_id lookup",
			sql:  `EXPLAIN QUERY PLAN SELECT occurred_at FROM cost_events WHERE session_id = ?`,
			args: []any{"chat-1"},
		},
		{
			name: "session_id or parent_session_id lookup",
			sql:  `EXPLAIN QUERY PLAN SELECT total_cost_usd FROM cost_events WHERE session_id = ? OR parent_session_id = ?`,
			args: []any{"chat-1", "chat-1"},
		},
	}
	for _, tc := range queries {
		rows, err := store.db.Query(tc.sql, tc.args...)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var plan string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				rows.Close()
				t.Fatalf("%s scan: %v", tc.name, err)
			}
			plan += detail + "\n"
		}
		rows.Close()
		if !containsPlanIndex(plan, "idx_cost_events_session_id") {
			t.Fatalf("%s did not use idx_cost_events_session_id:\n%s", tc.name, plan)
		}
		if tc.name == "session_id or parent_session_id lookup" && !containsPlanIndex(plan, "idx_cost_events_parent_session_id") {
			t.Fatalf("%s did not use idx_cost_events_parent_session_id:\n%s", tc.name, plan)
		}
	}
}

func containsPlanIndex(plan, idx string) bool {
	for i := 0; i+len(idx) <= len(plan); i++ {
		if plan[i:i+len(idx)] == idx {
			return true
		}
	}
	return false
}
