package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	"github.com/manishiitg/multi-llm-provider-go/pkg/nativeusage"
)

// PLAT-872 B1/B2: a partial native message grows once across restart; an
// unreadable source must preserve the existing foreground charge.
func TestNativeAccountingConservesGrowingUsageAndForegroundFallback(t *testing.T) {
	dir := t.TempDir()
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(dir, "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)
	source := filepath.Join(dir, "native.jsonl")
	row := func(id string, input, output int, at time.Time) string {
		return fmt.Sprintf(`{"type":"assistant","sessionId":"native","timestamp":%q,"message":{"id":%q,"model":"unknown-example-model","usage":{"input_tokens":%d,"output_tokens":%d}}}`+"\n", at.Format(time.RFC3339Nano), id, input, output)
	}
	if err := os.WriteFile(source, []byte(row("message1", 10, 2, now)), 0600); err != nil {
		t.Fatal(err)
	}
	base := costledger.Entry{EventID: "foreground1", IdempotencyKey: "foreground1", Timestamp: now.Add(-time.Second), SessionID: "cost-872-example", UserID: "alice", Provider: "claude-code", ModelID: "unknown-example-model", WorkflowID: "", Scope: "builder", ExecutionID: "query1", SourceID: "schedule1", SourceLabel: "Daily review", PromptTokens: 10, CompletionTokens: 2, LLMCallCount: 1, BillingBasis: "unpriced"}
	a := &nativeCostAccountant{api: &StreamingAPI{costLedger: ledger}, state: nativeCostState{Current: base, Provider: "claude-code", NativeID: "native", Path: source, Floor: base.Timestamp, Contexts: map[string]costledger.Entry{}}, file: filepath.Join(dir, "state.json"), done: make(chan struct{})}
	first := base
	first.Timestamp = now.Add(100 * time.Millisecond)
	if !a.Record(first) {
		t.Fatal("complete source did not claim accounting")
	}
	f, err := os.OpenFile(source, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(row("message1", 10, 8, now.Add(500*time.Millisecond)))
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	a.poll()
	// Restoring the context with the reader's old checkpoint cannot replay a
	// second charge: the SQLite source receipt and message key are canonical.
	b := &nativeCostAccountant{api: a.api, state: a.state, file: a.file, done: make(chan struct{})}
	b.poll()
	b.poll()
	day := now.Format("2006-01-02")
	summary, err := ledger.Summarize(day, day)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total.PromptTokens != 10 || summary.Total.CompletionTokens != 8 || summary.Total.CallCount != 1 {
		t.Fatalf("replayed usage %+v", summary.Total)
	}
	if err := os.Rename(source, source+".away"); err != nil {
		t.Fatal(err)
	}
	next := base
	next.EventID = "foreground2"
	next.IdempotencyKey = "foreground2"
	next.ExecutionID = "query2"
	next.Timestamp = now.Add(time.Second)
	next.PromptTokens = 7
	next.CompletionTokens = 3
	b.Begin(next)
	second := next
	second.Timestamp = now.Add(2 * time.Second)
	// The CLI wrote usage while its path was unavailable to the reader.
	appendRow := func(path, value string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString(value)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	appendRow(source+".away", row("message2", 7, 3, now.Add(1500*time.Millisecond)))
	if !b.Record(second) {
		t.Fatal("foreground source gap was not persisted")
	}
	if err := os.Rename(source+".away", source); err != nil {
		t.Fatal(err)
	}
	b.poll()
	b.poll()
	summary, err = ledger.Summarize(day, day)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total.PromptTokens != 17 || summary.Total.CompletionTokens != 11 || summary.Total.CallCount != 2 {
		t.Fatalf("lost/duplicated foreground charge %+v", summary.Total)
	}
	// A delayed write arrives after a foreground receipt that had only the
	// earlier messages. Timestamp belongs to the completed generation.
	third := next
	third.EventID, third.IdempotencyKey, third.ExecutionID = "foreground3", "foreground3", "query3"
	third.Timestamp = now.Add(3 * time.Second)
	third.PromptTokens, third.CompletionTokens = 5, 4
	b.Begin(third)
	third.Timestamp = now.Add(4 * time.Second)
	if !b.Record(third) {
		t.Fatal("late-flush foreground not persisted")
	}
	appendRow(source, row("message3", 5, 4, now.Add(3500*time.Millisecond)))
	b.poll()
	b.poll()
	summary, err = ledger.Summarize(day, day)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total.PromptTokens != 22 || summary.Total.CompletionTokens != 15 || summary.Total.CallCount != 3 {
		t.Fatalf("late tail counted twice %+v", summary.Total)
	}
	// Fresh autonomous work after the covered generation still counts.
	appendRow(source, row("message4", 4, 1, now.Add(5*time.Second)))
	b.poll()
	b.poll()
	summary, err = ledger.Summarize(day, day)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total.PromptTokens != 26 || summary.Total.CompletionTokens != 16 || summary.Total.CallCount != 4 {
		t.Fatalf("late native work lost or replayed %+v", summary.Total)
	}
	// Source has an autonomous call newer than generation-end before Record
	// reads it. A generation receipt must not claim that later call's offset.
	fourth := next
	fourth.EventID, fourth.IdempotencyKey, fourth.ExecutionID = "foreground4", "foreground4", "query4"
	fourth.Timestamp = now.Add(6 * time.Second)
	fourth.PromptTokens, fourth.CompletionTokens = 6, 2
	b.Begin(fourth)
	appendRow(source, row("message5", 6, 2, now.Add(6500*time.Millisecond)))
	appendRow(source, row("message6", 3, 1, now.Add(7500*time.Millisecond)))
	fourth.Timestamp = now.Add(7 * time.Second)
	if !b.Record(fourth) {
		t.Fatal("native replacement failed")
	}
	b.poll()
	b.poll()
	summary, err = ledger.Summarize(day, day)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total.PromptTokens != 35 || summary.Total.CompletionTokens != 19 || summary.Total.CallCount != 6 {
		t.Fatalf("generation receipt swallowed later source %+v", summary.Total)
	}
	if err := ledger.VisitEntries(context.Background(), day, day, func(e costledger.Entry) error {
		if e.UserID != "alice" || e.SourceID != "schedule1" {
			return fmt.Errorf("lost admitted attribution")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// The visible conversation survives provider/account changes. Its native
// accounting context and private usage must belong to the newly admitted CLI.
func TestNativeAccountingResetsWhenRuntimeChanges(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", dir)
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(dir, "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	api := &StreamingAPI{costLedger: ledger}
	defer api.stopNativeCostAccounting()
	now := time.Now().UTC()
	base := costledger.Entry{Timestamp: now, SessionID: "native-switch-example", UserID: "alice", AccountID: "account-a", Scope: "builder", ModelID: "unknown-example-model"}
	foreground := base
	foreground.EventID, foreground.IdempotencyKey = "switch-foreground", "switch-foreground"
	foreground.PromptTokens, foreground.CompletionTokens, foreground.LLMCallCount = 7, 1, 1
	foreground.BillingBasis = "unpriced"
	if err := ledger.Append(foreground); err != nil {
		t.Fatal(err)
	}
	seed := func(a *nativeCostAccountant) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.state.NativeID, a.state.Path = "old-native", "/old-source"
		a.state.Contexts["old-turn"] = base
		a.usage = nativeusage.PrivateUsage{ContextUsed: 100, Limits: []nativeusage.Limit{{Name: "private-old-limit"}}}
		a.stamp = "old-stamp"
	}
	clean := func(a *nativeCostAccountant) {
		t.Helper()
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.usage.ContextUsed != 0 || len(a.usage.Limits) != 0 || len(a.state.Contexts) != 0 || a.state.Path != "" || a.stamp != "" {
			t.Fatal("new runtime inherited private usage or source ownership")
		}
	}
	first := api.nativeCostAccounting(base, "claude-code").(*nativeCostAccountant)
	seed(first)
	base.Timestamp = now.Add(time.Second)
	second := api.nativeCostAccounting(base, "codex-cli").(*nativeCostAccountant)
	if first == second {
		t.Fatal("provider change kept accountant")
	}
	select {
	case <-first.done:
	default:
		t.Fatal("old provider reader still active")
	}
	clean(second)
	seed(second)
	oldAccount := base
	base.AccountID = "account-b"
	base.Timestamp = now.Add(2 * time.Second)
	third := api.nativeCostAccounting(base, "codex-cli").(*nativeCostAccountant)
	if second == third {
		t.Fatal("account change kept accountant")
	}
	select {
	case <-second.done:
	default:
		t.Fatal("old account reader still active")
	}
	clean(third)
	seed(third)
	third.mu.Lock()
	third.bindNativeID("new-native")
	third.mu.Unlock()
	clean(third)
	day := now.Format("2006-01-02")
	before, err := ledger.Summarize(day, day)
	if err != nil {
		t.Fatal(err)
	}
	if before.Total.PromptTokens != 7 || before.Total.CompletionTokens != 1 || before.Total.CallCount != 1 {
		t.Fatalf("runtime reset changed foreground charge %+v", before.Total)
	}
	// The same source id under different accounts names distinct admitted
	// usage. Replay after the switch never relabels or adds either charge.
	for _, owner := range []costledger.Entry{oldAccount, base, base} {
		e := costobserver.NativeEntry(owner, "codex-cli", nativeusage.Record{ID: "response", NativeSessionID: "native", Timestamp: now, Model: "unknown-example-model", Input: 2, Output: 1})
		if err := ledger.CommitNativeUsage(owner.AccountID, []costledger.Entry{e}, costledger.NativeCoverage{}); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := ledger.Summarize(now.Format("2006-01-02"), now.Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total.CallCount != 3 || summary.Total.PromptTokens != 11 || summary.Total.CompletionTokens != 3 {
		t.Fatalf("switch lost/duplicated charges %+v", summary.Total)
	}
	// Foreground-only providers must still retire the previous native cache.
	// Returning nil leaves their existing cost observer as the sole writer.
	for _, provider := range []string{"cursor-cli", "agy-cli", "openai"} {
		old := api.nativeCostAccounting(base, "codex-cli").(*nativeCostAccountant)
		clean(old)
		seed(old)
		if api.nativeCostAccounting(base, provider) != nil {
			t.Fatal("unsupported native parser claimed accounting")
		}
		if api.nativeCosts[base.SessionID] != nil {
			t.Fatal("foreground provider retained old private cache")
		}
		select {
		case <-old.done:
		default:
			t.Fatal("old native reader not retired")
		}
		if _, err := os.Stat(old.file); !os.IsNotExist(err) {
			t.Fatal("retired native checkpoint can restore old provider")
		}
	}
	returned := api.nativeCostAccounting(base, "codex-cli").(*nativeCostAccountant)
	clean(returned)
	source := filepath.Join(dir, "returned.jsonl")
	row := fmt.Sprintf(`{"type":"token_usage_record","timestamp":%q,"payload":{"response_id":"foreground-covered","usage":{"input_tokens":99,"output_tokens":9}}}`+"\n", now.Add(3*time.Second).Format(time.RFC3339Nano))
	if err := os.WriteFile(source, []byte(row), 0600); err != nil {
		t.Fatal(err)
	}
	returned.mu.Lock()
	returned.bindNativeID("returned-native")
	returned.state.Path = source
	returned.mu.Unlock()
	returned.poll()
	coverage, err := ledger.NativeCoverage(returned.runtimeKey())
	if err != nil || !coverage.Ready || coverage.Positions[source] == 0 {
		t.Fatal("returning CLI did not establish a fresh handoff")
	}
	after, err := ledger.Summarize(day, day)
	if err != nil {
		t.Fatal(err)
	}
	if after.Total != summary.Total {
		t.Fatal("foreground-only switch changed cost totals")
	}
}
