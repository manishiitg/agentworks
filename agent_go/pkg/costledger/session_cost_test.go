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
	if math.Abs(got.TotalUSD-1.25) > 1e-9 || math.Abs(got.HelperUSD-0.25) > 1e-9 {
		t.Fatalf("total/helper = %v/%v, want 1.25/0.25 (other chats must not count)", got.TotalUSD, got.HelperUSD)
	}
	if got.UnpricedCalls != 1 || got.Basis != "actual" || len(got.Models) != 2 || got.Models[0].Model != "model-a" {
		t.Fatalf("unexpected summary: %#v", got)
	}
	if got.CachePercent != 54 { // 300 cached of 550 input tokens (400 + 100 + 50)
		t.Fatalf("CachePercent = %d, want 54", got.CachePercent)
	}
}
