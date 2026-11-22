package costledger

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The package's tests are written in UTC days; without this they would follow the test machine's own time zone.
func TestMain(m *testing.M) {
	SetDayLocation(time.UTC)
	os.Exit(m.Run())
}

// Real report: the Costs page grouped by UTC days while a chat's "today" started at local midnight, so the same
// day showed two different amounts. Both now use one day location; a call late in the UTC evening lands on the
// next local day, and a date window covers that local day.
func TestCostDaysFollowTheDayLocation(t *testing.T) {
	loc := time.FixedZone("UTC+5:30", 5*3600+1800)
	SetDayLocation(loc)
	defer SetDayLocation(time.UTC)
	ledger, err := NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteLedger() error = %v", err)
	}
	defer ledger.Close()
	if err := ledger.Append(Entry{EventID: "late", IdempotencyKey: "late", Timestamp: time.Date(2026, 11, 21, 20, 0, 0, 0, time.UTC), Scope: "builder", EffectiveModelID: "example-model", LLMCallCount: 1, PromptTokens: 10, TotalCostUSD: 1, BillingBasis: "provider_actual"}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	summary, err := ledger.Summarize("2026-11-22", "2026-11-22")
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if summary.ByDate["2026-11-22"] == nil || summary.ByDate["2026-11-21"] != nil {
		t.Fatalf("by_date keys = %v, want the local day 2026-11-22 only", keysOf(summary.ByDate))
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
