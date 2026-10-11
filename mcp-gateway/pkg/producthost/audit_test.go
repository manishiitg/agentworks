package producthost

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	api "github.com/manishiitg/agentworks/packages/server-product-sdk"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

func auditHost(t *testing.T, backend string) (*Store, *store.MemoryStore) {
	t.Helper()
	st := store.NewMemoryStore()
	if backend == "sqlite" {
		b, err := store.OpenAuditBackend(store.AuditOptions{Provider: "sqlite", StateDir: t.TempDir(), Retention: 72 * time.Hour, WriteMode: "durable"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { b.Close() })
		st.SetAuditBackend(b)
	}
	return New(st), st
}

func TestAuditQueryHonorsAllFiltersBeforeLimit(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			h, st := auditHost(t, backend)
			now := time.Now().UTC().Truncate(time.Second)
			a := api.AuditEvent{ID: "llm-a", WorkspaceID: "w", UserID: "alice", GroupIDs: []string{"g1"}, ClientID: "c1", ConnectorID: "p1", PublicName: "llm:one", Decision: "allow", Outcome: "ok", Timestamp: now.Add(-2 * time.Minute)}
			b := api.AuditEvent{ID: "llm-b", WorkspaceID: "w", UserID: "bob", GroupIDs: []string{"g2"}, ClientID: "c2", ConnectorID: "p2", PublicName: "llm:two", Decision: "deny", Outcome: "denied", Timestamp: now.Add(-3 * time.Minute)}
			c := api.AuditEvent{ID: "llm-c", WorkspaceID: "w", UserID: "alice", GroupIDs: []string{"g2"}, ClientID: "c1", ConnectorID: "p2", PublicName: "llm:one", Decision: "allow", Outcome: "upstream_error", Timestamp: now.Add(-time.Minute)}
			for _, e := range []api.AuditEvent{b, a, c} {
				if err := h.AppendAudit(e); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.AppendAudit(store.AuditEvent{ID: "mcp-d", WorkspaceID: "w", UserID: "alice", GroupIDs: []string{"g1"}, ClientID: "c3", ConnectorID: "p3", PublicName: "notion.fetch", Decision: "allow", Outcome: "ok", Timestamp: now}); err != nil {
				t.Fatal(err)
			}
			foreign := a
			foreign.ID = "llm-foreign"
			foreign.WorkspaceID = "other"
			if err := h.AppendAudit(foreign); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name   string
				filter api.AuditFilter
				ids    []string
			}{
				{"workspace", api.AuditFilter{}, []string{"llm-a", "llm-b", "llm-c", "mcp-d"}},
				{"user", api.AuditFilter{UserID: "alice"}, []string{"llm-a", "llm-c", "mcp-d"}},
				{"group", api.AuditFilter{GroupID: "g1"}, []string{"llm-a", "mcp-d"}},
				{"client", api.AuditFilter{ClientID: "c1"}, []string{"llm-a", "llm-c"}},
				{"connector", api.AuditFilter{ConnectorID: "p1"}, []string{"llm-a"}},
				{"model substring", api.AuditFilter{PublicName: "LLM:On"}, []string{"llm-a", "llm-c"}},
				{"decision", api.AuditFilter{Decision: "deny"}, []string{"llm-b"}},
				{"outcome", api.AuditFilter{Outcome: "upstream_error"}, []string{"llm-c"}},
				{"llm kind before limit", api.AuditFilter{Kind: "llm", Limit: 1}, []string{"llm-c"}},
				{"mcp kind", api.AuditFilter{Kind: "mcp"}, []string{"mcp-d"}},
				{"unknown kind", api.AuditFilter{Kind: "invalid"}, []string{}},
				{"after", api.AuditFilter{After: now.Add(-90 * time.Second)}, []string{"llm-c", "mcp-d"}},
				{"before", api.AuditFilter{Before: now.Add(-90 * time.Second)}, []string{"llm-a", "llm-b"}},
				{"combined", api.AuditFilter{UserID: "alice", GroupID: "g1", ClientID: "c1", ConnectorID: "p1", PublicName: "one", Decision: "allow", Outcome: "ok", Kind: "llm", Limit: 1}, []string{"llm-a"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					tc.filter.WorkspaceID = "w"
					rows, err := h.Query(context.Background(), tc.filter)
					if err != nil {
						t.Fatal(err)
					}
					ids := []string{}
					for _, e := range rows {
						ids = append(ids, e.ID)
					}
					slices.Sort(ids)
					if !reflect.DeepEqual(ids, tc.ids) {
						t.Fatalf("got %v want %v", ids, tc.ids)
					}
				})
			}
		})
	}
}

func TestAuditStatusFilterCannotResurrectAdmission(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			h, _ := auditHost(t, backend)
			e := api.AuditEvent{ID: "llm-changed", WorkspaceID: "w", Timestamp: time.Now(), Phase: "started", Decision: "allow", Outcome: "ok"}
			if err := h.AppendAudit(e); err != nil {
				t.Fatal(err)
			}
			e.Phase = ""
			e.Outcome = "upstream_error"
			if err := h.AppendAudit(e); err != nil {
				t.Fatal(err)
			}
			rows, err := h.Query(context.Background(), api.AuditFilter{WorkspaceID: "w", Outcome: "ok"})
			if err != nil || len(rows) != 0 {
				t.Fatal("completed request resurfaced as an admission", rows, err)
			}
		})
	}
}

func TestAuditSummaryIncludesAllMatchingRequestsAndCosts(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			h, st := auditHost(t, backend)
			now := time.Now().UTC()
			cost := func(status string, amount, input, output int64) *api.LLMCost {
				return &api.LLMCost{Status: status, AmountNanoUSD: amount, Usage: api.LLMTokenUsage{Input: input, Output: output}}
			}
			events := []api.AuditEvent{
				{ID: "llm-a", UserID: "alice", PublicName: "llm:one", Decision: "allow", Outcome: "ok", DurationMs: 100, LLMCost: cost("estimated", 100, 10, 2), Timestamp: now.Add(-24 * time.Hour)},
				{ID: "llm-b", UserID: "bob", PublicName: "llm:two", Decision: "deny", Outcome: "denied", LLMCost: cost("not_billable", 999, 99, 99)},
				{ID: "llm-c", UserID: "alice", PublicName: "llm:one", Decision: "allow", Outcome: "upstream_error", DurationMs: 200},
				{ID: "llm-d", UserID: "bob", PublicName: "llm:two", Decision: "allow", Outcome: "interrupted", DurationMs: 300, LLMCost: cost("interrupted_estimate", 300, 20, 3)},
				{ID: "llm-e", UserID: "alice", PublicName: "llm:one", Decision: "allow", Outcome: "ok", DurationMs: 500, LLMCost: cost("future-status", 999, 99, 99)},
				{ID: "llm-pending", UserID: "alice", PublicName: "llm:pending", Decision: "allow", Outcome: "ok", DurationMs: 9999, Phase: "started", LLMCost: cost("estimated", 999, 99, 99)},
			}
			events[0].LLMCost.Usage.CacheRead = 1
			events[0].LLMCost.Usage.CacheWrite5m = 2
			events[0].LLMCost.Usage.CacheWrite1h = 3
			for _, e := range events {
				e.WorkspaceID = "w"
				if e.Timestamp.IsZero() {
					e.Timestamp = now
				}
				if e.ID == "llm-a" {
					admission := e
					admission.Phase = "started"
					if err := h.AppendAudit(admission); err != nil {
						t.Fatal(err)
					}
				}
				if err := h.AppendAudit(e); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.AppendAudit(store.AuditEvent{ID: "mcp-f", WorkspaceID: "w", PublicName: "notion.fetch", Decision: "allow", Outcome: "ok", DurationMs: 400, Timestamp: now}); err != nil {
				t.Fatal(err)
			}
			s, err := h.Summary(context.Background(), api.AuditFilter{WorkspaceID: "w", Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if s.Total != 7 || s.Allowed != 6 || s.Denied != 1 || s.UpstreamErrors != 1 || s.AvgDurationMs != 250 {
				t.Fatalf("incomplete summary: %+v", s)
			}
			if len(s.ByDay) != 2 || s.ByDay[0].Count != 6 || s.ByDay[1].Count != 1 || len(s.ByTool) != 4 || s.ByTool[0] != (api.UsageBucket{Key: "llm:one", Count: 3, UpstreamErrors: 1}) {
				t.Fatalf("incorrect usage buckets: %+v", s)
			}
			want := api.LLMCostBucket{Requests: 5, PricedRequests: 2, UnknownRequests: 2, UnbilledRequests: 1, AmountNanoUSD: 400, InputTokens: 36, OutputTokens: 5, InterruptedRequests: 1, InterruptedAmountNanoUSD: 300}
			if s.Costs == nil || s.Costs.LLMCostBucket != want {
				t.Fatalf("costs: %+v want %+v", s.Costs, want)
			}
			if len(s.Costs.ByUser) != 2 || s.Costs.ByUser[0].Key != "alice" || s.Costs.ByUser[0].AmountNanoUSD != 100 || s.Costs.ByUser[1].Key != "bob" || s.Costs.ByUser[1].AmountNanoUSD != 300 || len(s.Costs.ByModel) != 2 {
				t.Fatalf("cost buckets: %+v", s.Costs)
			}
			filtered, err := h.Summary(context.Background(), api.AuditFilter{WorkspaceID: "w", UserID: "bob", Kind: "llm", Limit: 1})
			if err != nil || filtered.Total != 2 || filtered.Denied != 1 || filtered.Costs == nil || filtered.Costs.AmountNanoUSD != 300 {
				t.Fatalf("filtered summary: %+v %v", filtered, err)
			}
			empty, err := h.Summary(context.Background(), api.AuditFilter{WorkspaceID: "other", Limit: 1})
			if err != nil || empty.Total != 0 || empty.Costs != nil || len(empty.ByDay) != 0 || len(empty.ByTool) != 0 {
				t.Fatalf("empty tenant summary: %+v %v", empty, err)
			}
		})
	}
}

func TestAuditKindDoesNotDependOnCallerID(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			h, st := auditHost(t, backend)
			now := time.Now().UTC().Truncate(time.Second)
			e := api.AuditEvent{ID: "142b73c8-69fe-4e7d-b16d-f4d64cb68c8d", WorkspaceID: "w", UserID: "alice", Timestamp: now, PublicName: "llm:tiny", Decision: "allow", Outcome: "ok", Phase: "started"}
			if err := h.AppendAudit(e); err != nil {
				t.Fatal(err)
			}
			e.Phase = ""
			e.LLMCost = &api.LLMCost{Status: "estimated", AmountNanoUSD: 125}
			if err := h.AppendAudit(e); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{e.ID, "llm-native-tool"} {
				if err := st.AppendAudit(store.AuditEvent{ID: id, WorkspaceID: "w", Timestamp: now, Output: []byte(`{"tool":"result"}`)}); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := h.Query(context.Background(), api.AuditFilter{WorkspaceID: "w", Kind: "llm"})
			if err != nil || len(rows) != 1 || rows[0].Kind != "llm" || rows[0].ID != e.ID || rows[0].Phase != "" || rows[0].LLMCost == nil || rows[0].LLMCost.AmountNanoUSD != 125 {
				t.Fatal("UUID cost lost or admission duplicated", rows, err)
			}
			mcp, err := h.Query(context.Background(), api.AuditFilter{WorkspaceID: "w", Kind: "mcp"})
			if err != nil || len(mcp) != 2 {
				t.Fatal("native MCP kind inferred from ID", mcp, err)
			}
			summary, err := h.Summary(context.Background(), api.AuditFilter{WorkspaceID: "w"})
			if err != nil || summary.Total != 3 || summary.Costs == nil || summary.Costs.AmountNanoUSD != 125 {
				t.Fatal("UUID cost omitted from totals", summary, err)
			}
		})
	}
}

func TestAuditReadsLegacyProductEnvelope(t *testing.T) {
	h, st := auditHost(t, "sqlite")
	e := api.AuditEvent{ID: "llm-legacy", WorkspaceID: "w", UserID: "alice", Timestamp: time.Now().UTC(), PublicName: "llm:tiny", LLMCost: &api.LLMCost{Status: "estimated", AmountNanoUSD: 10}}
	payload, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendAudit(store.AuditEvent{ID: e.ID, WorkspaceID: e.WorkspaceID, UserID: e.UserID, Timestamp: e.Timestamp, PublicName: e.PublicName, Output: payload}); err != nil {
		t.Fatal(err)
	}
	rows, err := h.Query(context.Background(), api.AuditFilter{WorkspaceID: "w", Kind: "llm"})
	if err != nil || len(rows) != 1 || rows[0].LLMCost == nil || rows[0].LLMCost.AmountNanoUSD != 10 {
		t.Fatal("legacy cost lost", rows, err)
	}
}
