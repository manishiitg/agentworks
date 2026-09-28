package server

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

func TestBuildCostOverviewFoldsAndFiltersByAccess(t *testing.T) {
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })

	ts := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	add := func(id, workflowID, scope, provider, model string, usd float64) {
		t.Helper()
		if err := ledger.Append(costledger.Entry{
			EventID: id, Timestamp: ts, WorkflowID: workflowID, Scope: scope,
			Provider: provider, ModelID: model, LLMCallCount: 1,
			PromptTokens: 100, TotalCostUSD: usd, BillingBasis: "provider_actual",
		}); err != nil {
			t.Fatal(err)
		}
	}
	add("e1", "Workflow/sales", "workflow_execution", "claude-code", "opus", 2)
	add("e2", "Workflow/sales/runs/iteration-3", "pulse", "codex-cli", "gpt", 1)
	add("e3", "Workflow/secret", "workflow_execution", "claude-code", "opus", 5)
	add("e4", "_users/alice/Chats/Work/projects/writer-ab12/code", "chat", "claude-code", "sonnet", 0.5)
	add("e5", "_users/alice/Chats", "chat", "claude-code", "sonnet", 3)
	add("e6", "", "unknown", "claude-code", "sonnet", 4)

	summary, err := ledger.Summarize("2026-09-01", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	visible := func(id, kind string) bool { return id != "Workflow/secret" }

	member := buildCostOverview(summary, visible, false)
	if got, want := len(member.Items), 2; got != want {
		t.Fatalf("member items = %d, want %d: %+v", got, want, member.Items)
	}
	sales := member.Items[0]
	if sales.ID != "Workflow/sales" || sales.Kind != costOverviewKindWorkflow || sales.TotalCostUSD != 3 {
		t.Fatalf("sales row = %+v, want Workflow/sales folded to $3", sales)
	}
	if sales.ByScope["pulse"] == nil || sales.ByScope["pulse"].TotalCostUSD != 1 {
		t.Fatalf("sales pulse scope = %+v", sales.ByScope)
	}
	crew := member.Items[1]
	if crew.ID != "_users/alice/Chats/Work/projects/writer-ab12" || crew.Kind != costOverviewKindCrew || crew.OwnerID != "alice" || crew.Name != "writer-ab12" {
		t.Fatalf("crew row = %+v", crew)
	}
	if member.Total.TotalCostUSD != 3.5 {
		t.Fatalf("member total = %v, want only visible spend 3.5", member.Total.TotalCostUSD)
	}
	if member.ByProvider["codex-cli"] == nil || member.ByProvider["codex-cli"].TotalCostUSD != 1 {
		t.Fatalf("member by_provider = %+v", member.ByProvider)
	}

	admin := buildCostOverview(summary, func(string, string) bool { return true }, true)
	if admin.Total.TotalCostUSD != 15.5 {
		t.Fatalf("admin total = %v, want 15.5", admin.Total.TotalCostUSD)
	}
	var other *costOverviewItem
	for _, item := range admin.Items {
		if item.Kind == costOverviewKindOther {
			other = item
		}
	}
	if other == nil || other.TotalCostUSD != 7 {
		t.Fatalf("admin other row = %+v, want chat+unattributed folded to $7", other)
	}
}

func TestBuildCostOverviewRecognizesOwnerScopedProductProjects(t *testing.T) {
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	ts := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	for _, entry := range []costledger.Entry{
		{EventID: "alice-1", Timestamp: ts, WorkflowID: "_users/alice/Chats/Video Studio/projects/launch", UserID: "alice", Scope: "workflow_execution", LLMCallCount: 1, BillingBasis: "subscription_shadow", TotalCostUSD: 2},
		{EventID: "alice-2", Timestamp: ts, WorkflowID: "_users/alice/Chats/Video Studio/projects/launch/runs/one", UserID: "alice", Scope: "chat", LLMCallCount: 1, BillingBasis: "subscription_shadow", TotalCostUSD: 1},
		{EventID: "bob-1", Timestamp: ts, WorkflowID: "_users/bob/Chats/Video Studio/projects/launch", UserID: "bob", Scope: "chat", LLMCallCount: 1, BillingBasis: "subscription_shadow", TotalCostUSD: 4},
	} {
		if err := ledger.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := ledger.Summarize("2026-09-01", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	alice := buildCostOverview(summary, func(id, kind string) bool {
		return kind == costOverviewKindProduct && costOverviewProductVisible(id, "alice", false)
	}, false)
	if len(alice.Items) != 1 || alice.Items[0].Kind != costOverviewKindProduct ||
		alice.Items[0].ID != "_users/alice/Chats/Video Studio/projects/launch" ||
		alice.Items[0].OwnerID != "alice" || alice.Items[0].TotalCostUSD != 3 || alice.Total.TotalCostUSD != 3 {
		t.Fatalf("Alice's visible product project = %+v, total %+v", alice.Items, alice.Total)
	}
	if costOverviewProductVisible("_users/bob/Chats/Video Studio/projects/launch", "alice", false) {
		t.Fatal("another user's product project was visible")
	}
	if !costOverviewProductVisible("_users/bob/Chats/Video Studio/projects/launch", "alice", true) {
		t.Fatal("admin could not see another user's product project")
	}
	if costOverviewProductVisible("Workflow/launch", "alice", true) {
		t.Fatal("product-project check admitted a workflow path")
	}
}

func TestBuildCostOverviewGroupsUserSpendAfterAccessFiltering(t *testing.T) {
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })

	ts := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	add := func(id, userID, workflowID, scope string, usd float64) {
		t.Helper()
		if err := ledger.Append(costledger.Entry{
			EventID: id, Timestamp: ts, UserID: userID, WorkflowID: workflowID,
			Scope: scope, Provider: "codex-cli", ModelID: "gpt", LLMCallCount: 1,
			PromptTokens: 100, TotalCostUSD: usd, BillingBasis: "subscription_shadow",
		}); err != nil {
			t.Fatal(err)
		}
	}
	add("a1", "alice", "Workflow/shared", "workflow_execution", 2)
	add("b1", "bob", "Workflow/shared/runs/one", "pulse", 1)
	add("b2", "bob", "_users/bob/Chats/Work/projects/crew/code", "chat", 0.5)
	add("u1", "", "Workflow/shared", "builder", 0.25)
	add("a2", "alice", "Workflow/secret", "workflow_execution", 10)
	add("a3", "alice", "", "chat", 5)

	summary, err := ledger.Summarize("2026-09-01", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	// The general summary endpoint must not serialize per-user details without
	// the overview endpoint's workflow access checks.
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.ByWorkflowUser) != 5 {
		t.Fatalf("expected five raw workflow buckets, got %d", len(summary.ByWorkflowUser))
	}
	var publicSummary map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &publicSummary); err != nil {
		t.Fatal(err)
	}
	if _, exposed := publicSummary["ByWorkflowUser"]; exposed {
		t.Fatal("general summary exposed per-user attribution")
	}
	if _, exposed := publicSummary["by_workflow_user"]; exposed {
		t.Fatal("general summary exposed per-user attribution")
	}

	member := buildCostOverview(summary, func(id, _ string) bool { return id != "Workflow/secret" }, false)
	if member.Total.TotalCostUSD != 3.75 || len(member.ByUser) != 3 {
		t.Fatalf("member total/users = %v/%d, want 3.75/3", member.Total.TotalCostUSD, len(member.ByUser))
	}
	byID := make(map[string]*costOverviewUser)
	for _, user := range member.ByUser {
		byID[user.ID] = user
	}
	if byID["alice"].TotalCostUSD != 2 || byID["bob"].TotalCostUSD != 1.5 || byID[""].TotalCostUSD != 0.25 {
		t.Fatalf("incorrect visible per-user costs: %+v", byID)
	}
	if byID["bob"].ByScope["pulse"].TotalCostUSD != 1 || byID["bob"].ByScope["chat"].TotalCostUSD != 0.5 {
		t.Fatalf("incorrect per-user scope costs: %+v", byID["bob"].ByScope)
	}
	if byID["bob"].ByModel["gpt"].TotalCostUSD != 1.5 || len(byID["bob"].ByWork) != 2 {
		t.Fatalf("missing user model/work details: %+v", byID["bob"])
	}
	var shared *costOverviewItem
	for _, item := range member.Items {
		if item.ID == "Workflow/shared" {
			shared = item
		}
	}
	if shared == nil || len(shared.ByUser) != 3 || shared.ByUser[0].ByModel["gpt"] == nil {
		t.Fatalf("missing workflow user/model details: %+v", shared)
	}
	wire, err := json.Marshal(member)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "Workflow/secret") {
		t.Fatalf("filtered cost overview leaked private work: %s", wire)
	}
	var decoded struct {
		ByUser []struct {
			ID           string                           `json:"id"`
			TotalCostUSD float64                          `json:"total_cost_usd"`
			ByScope      map[string]*costledger.Aggregate `json:"by_scope"`
		} `json:"by_user"`
	}
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.ByUser) != 3 || decoded.ByUser[1].ByScope["pulse"] == nil {
		t.Fatalf("per-user API shape missing scope data: %s", wire)
	}
	admin := buildCostOverview(summary, func(string, string) bool { return true }, true)
	if admin.Total.TotalCostUSD != 18.75 || admin.ByUser[0].ID != "alice" || admin.ByUser[0].TotalCostUSD != 17 {
		t.Fatalf("incorrect admin per-user costs: %+v", admin.ByUser)
	}
}

func TestBuildCostOverviewGroupsBotsAndMCPsAfterAccessFiltering(t *testing.T) {
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	ts := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	add := func(id, workflow, user, platform, component, basis string, usd float64) {
		t.Helper()
		if err := ledger.Append(costledger.Entry{
			EventID: id, Timestamp: ts, WorkflowID: workflow, UserID: user,
			SourcePlatform: platform, Scope: "chat", Component: component,
			BillingBasis: basis, TotalCostUSD: usd,
		}); err != nil {
			t.Fatal(err)
		}
	}
	add("bot1", "Workflow/shared", "bot-slack-route1", "slack", "", "provider_actual", 2)
	add("bot2", "Workflow/shared/runs/one", "bot-slack-route1", "slack", "", "provider_actual", 1)
	add("bot3", "Workflow/shared", "alice", "whatsapp", "", "provider_actual", 0.5)
	add("mcp1", "Workflow/shared", "bot-slack-route1", "slack", "mcp:github", "unpriced", 0)
	add("mcp2", "Workflow/shared", "bot-slack-route1", "slack", "mcp:github", "provider_actual", 0.2)
	add("hidden", "Workflow/secret", "bot-slack-secret", "slack", "mcp:secret", "provider_actual", 7)

	summary, err := ledger.Summarize("2026-09-01", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	var generalSummary map[string]json.RawMessage
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &generalSummary); err != nil {
		t.Fatal(err)
	}
	if generalSummary["ByWorkflowBot"] != nil || generalSummary["ByWorkflowMCP"] != nil {
		t.Fatal("general summary exposed bot/MCP details before access filtering")
	}
	member := buildCostOverview(summary, func(id, _ string) bool { return id != "Workflow/secret" }, false)
	if len(member.ByBot) != 2 || len(member.ByMCP) != 1 {
		t.Fatalf("member bots/MCPs = %d/%d, want 2/1", len(member.ByBot), len(member.ByMCP))
	}
	if member.Total.TotalCostUSD != 3.7 || member.ByBot[0].TotalCostUSD != 3.2 || member.ByMCP[0].Server != "github" || member.ByMCP[0].Calls != 2 || member.ByMCP[0].UnpricedCalls != 1 || member.ByMCP[0].RecordedCostUSD != 0.2 {
		t.Fatalf("incorrect visible bot/MCP totals: bots=%+v mcp=%+v total=%v", member.ByBot, member.ByMCP, member.Total.TotalCostUSD)
	}
	if len(member.Items) != 1 || len(member.Items[0].ByBot) != 2 || len(member.Items[0].ByMCP) != 1 || member.Items[0].ByMCP[0].Calls != 2 {
		t.Fatalf("missing per-work bot/MCP detail: %+v", member.Items)
	}
	admin := buildCostOverview(summary, func(string, string) bool { return true }, true)
	if len(admin.ByMCP) != 2 || len(admin.ByBot) != 3 {
		t.Fatalf("admin bots/MCPs = %d/%d, want 3/2", len(admin.ByBot), len(admin.ByMCP))
	}
}
