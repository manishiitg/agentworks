package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
)

func installWorkflowCostTestLedger(t *testing.T) *costledger.Ledger {
	t.Helper()
	previous := costledger.DefaultLedger()
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteLedger() error = %v", err)
	}
	costledger.SetDefaultLedger(ledger)
	t.Cleanup(func() {
		costledger.SetDefaultLedger(previous)
		_ = ledger.Close()
	})
	return ledger
}

func TestLoadWorkflowCostSummaryReturnsBoundedHistoryAndExactHeadline(t *testing.T) {
	ledger := installWorkflowCostTestLedger(t)
	entries := []costledger.Entry{
		{EventID: "old", IdempotencyKey: "old", Timestamp: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), WorkflowID: "Workflow/demo", Scope: "builder", LLMCallCount: 1, TotalCostUSD: 4},
		{EventID: "recent", IdempotencyKey: "recent", Timestamp: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC), WorkflowID: "Workflow/demo", Scope: "pulse", ExecutionID: "review", LLMCallCount: 2, TotalCostUSD: 6},
	}
	for _, entry := range entries {
		if err := ledger.Append(entry); err != nil {
			t.Fatalf("Append(%q) error = %v", entry.EventID, err)
		}
	}

	response, err := loadWorkflowCostSummary("Workflow/demo", 30, "", time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("loadWorkflowCostSummary() error = %v", err)
	}
	if response.History == nil || response.History.WindowFrom != "2026-07-18" || response.History.WindowTo != "2026-08-16" || !response.History.HasMore || response.History.NextBefore != "2026-07-18" {
		t.Fatalf("history = %#v, want bounded first page with an older cursor", response.History)
	}
	if response.ScopedCosts == nil || response.ScopedCosts.Total.TotalCostUSD != 10 || len(response.ScopedCosts.ByDate) != 1 {
		t.Fatalf("scoped costs = %#v, want exact $10 headline and one recent day", response.ScopedCosts)
	}
	if len(response.Runs) != 0 || len(response.RunDailyCosts) != 0 || len(response.PhaseDailyCosts) != 0 {
		t.Fatalf("summary endpoint returned legacy artifact payloads: %#v", response)
	}

	older, err := loadWorkflowCostSummary("Workflow/demo", 90, response.History.NextBefore, time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("loadWorkflowCostSummary(older) error = %v", err)
	}
	if older.History == nil || older.History.WindowTo != "2026-07-17" || len(older.ScopedCosts.ByDate) != 1 || older.ScopedCosts.ByDate["2026-05-01"] == nil {
		t.Fatalf("older page = %#v, want the cursor page containing May", older)
	}
}

func TestHandleGetCostsSummaryUsesBoundedEndpoint(t *testing.T) {
	ledger := installWorkflowCostTestLedger(t)
	if err := ledger.Append(costledger.Entry{
		EventID: "today", IdempotencyKey: "today", Timestamp: time.Now().UTC(),
		WorkflowID: "Workflow/demo", Scope: "workflow_execution", LLMCallCount: 1, TotalCostUSD: 2,
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/workflow/costs?workspace_path=Workflow%2Fdemo&view=summary&days=7", nil)
	recorder := httptest.NewRecorder()
	(&StreamingAPI{}).handleGetCosts(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response workflowCostsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.History == nil || response.History.Days != 7 || response.ScopedCosts == nil || response.ScopedCosts.Total.TotalCostUSD != 2 {
		t.Fatalf("response = %#v, want seven-day summary", response)
	}
}

func TestCostWorkspacePathForUserScopesPublicChatProjects(t *testing.T) {
	if got := costWorkspacePathForUser("Chats/Work/projects/demo", "alice"); got != "_users/alice/Chats/Work/projects/demo" {
		t.Fatalf("costWorkspacePathForUser() = %q", got)
	}
	if got := costWorkspacePathForUser("Workflow/demo", "alice"); got != "Workflow/demo" {
		t.Fatalf("workflow cost path changed to %q", got)
	}
}

func TestHandleGetCostsSummaryResolvesWorkProjectToOwnerLedgerKey(t *testing.T) {
	ledger := installWorkflowCostTestLedger(t)
	const ownerLedgerKey = "_users/alice/Chats/Work/projects/demo"
	if err := ledger.Append(costledger.Entry{
		EventID: "work-turn", IdempotencyKey: "work-turn", Timestamp: time.Now().UTC(),
		WorkflowID: ownerLedgerKey, Scope: "chat", Provider: "muse-cli",
		EffectiveProvider: "muse-cli", ModelID: "muse-spark-1.3-contributor",
		EffectiveModelID: "muse-spark-1.3-contributor", LLMCallCount: 1,
		PromptTokens: 120, CompletionTokens: 30, TotalCostUSD: 0.04,
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/workflow/costs?workspace_path=Chats%2FWork%2Fprojects%2Fdemo&view=summary&days=7", nil)
	request = request.WithContext(context.WithValue(request.Context(), UserContextKey, &UserClaims{UserID: "alice"}))
	recorder := httptest.NewRecorder()
	(&StreamingAPI{}).handleGetCosts(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response workflowCostsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	day := response.ScopedCosts.ByDate[time.Now().UTC().Format("2006-01-02")]
	model := day.ByModel["muse-spark-1.3-contributor"]
	if model == nil || model.Provider != "muse-cli" || model.TotalCostUSD != 0.04 {
		t.Fatalf("daily model split = %#v, want Muse provider and model cost", day.ByModel)
	}
}

// The runs list shows one number per run: its model spend plus its tool spend (the browser used to add both up).
func TestWorkflowRunTotalAddsModelAndToolSpend(t *testing.T) {
	usage := &orchestrator.TokenUsageFile{
		ByModel: map[string]*orchestrator.ModelTokenUsage{"a": {TotalCost: 0.25}, "b": {TotalCost: 0.5}, "gone": nil},
		ByTool:  map[string]*orchestrator.ToolCostUsage{"search": {TotalCost: 0.125}},
	}
	if got := workflowRunTotalFromUsage(usage); got != 0.875 {
		t.Fatalf("run total = %v, want 0.875", got)
	}
	if got := workflowRunTotalFromUsage(&orchestrator.TokenUsageFile{}); got != 0 {
		t.Fatalf("empty run total = %v, want 0", got)
	}
}
