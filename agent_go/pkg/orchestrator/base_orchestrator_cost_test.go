package orchestrator

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"testing"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	unifiedevents "github.com/manishiitg/mcpagent/events"

	_ "modernc.org/sqlite"
)

// recordingObserverSink stands in for the agent an orchestrator would build.
// setupStandardAgent's only interaction with the cost observer is registering
// it here, so capturing that is enough to drive the real attribution code.
type recordingObserverSink struct {
	observers []mcpagent.AgentEventListener
}

func (r *recordingObserverSink) AddObserver(observer mcpagent.AgentEventListener) error {
	r.observers = append(r.observers, observer)
	return nil
}

func (r *recordingObserverSink) costObserver(t *testing.T) mcpagent.AgentEventListener {
	t.Helper()
	if len(r.observers) != 1 {
		t.Fatalf("expected exactly one attached observer, got %d", len(r.observers))
	}
	return r.observers[0]
}

type ledgerRow struct {
	scope       string
	executionID string
	sessionID   string
	workflowID  string
	runID       string
	userID      string
	prompt      int
	completion  int
}

func newTestLedger(t *testing.T) (*costledger.Ledger, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "costs.sqlite")
	ledger, err := costledger.NewSQLiteLedger(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteLedger() error = %v", err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	costledger.SetDefaultLedger(ledger)
	t.Cleanup(func() { costledger.SetDefaultLedger(nil) })
	return ledger, dbPath
}

func readLedgerRows(t *testing.T, dbPath string) []ledgerRow {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open cost database: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT scope, execution_id, session_id, workflow_id, run_id, user_id,
	       prompt_tokens, completion_tokens FROM cost_events ORDER BY occurred_at`)
	if err != nil {
		t.Fatalf("query cost_events: %v", err)
	}
	defer rows.Close()
	var out []ledgerRow
	for rows.Next() {
		var row ledgerRow
		if err := rows.Scan(&row.scope, &row.executionID, &row.sessionID, &row.workflowID,
			&row.runID, &row.userID, &row.prompt, &row.completion); err != nil {
			t.Fatalf("scan cost_events: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate cost_events: %v", err)
	}
	return out
}

func stageAgentConfig(sessionID, provider, modelID string) *agents.OrchestratorAgentConfig {
	config := agents.NewOrchestratorAgentConfig("stage-agent")
	config.MCPSessionID = sessionID
	config.LLMConfig.Primary = agents.LLMModel{Provider: provider, ModelID: modelID}
	return config
}

func generationEvent(spanID string, prompt, completion int) *unifiedevents.AgentEvent {
	return &unifiedevents.AgentEvent{
		Type:      unifiedevents.LLMGenerationEnd,
		Timestamp: time.Date(2026, 8, 2, 22, 45, 0, 0, time.UTC),
		SpanID:    spanID,
		Component: "llm",
		Data: &unifiedevents.LLMGenerationEndEvent{
			UsageMetrics: unifiedevents.UsageMetrics{
				PromptTokens:     prompt,
				CompletionTokens: completion,
			},
			BaseEventData: unifiedevents.BaseEventData{Metadata: map[string]interface{}{
				"provider":           "codex-cli",
				"cost_usd_estimated": 0.42,
			}},
		},
	}
}

func TestWorkflowChildUsageJoinsNamedScheduleWithoutDuplicatingCharges(t *testing.T) {
	ledger, dbPath := newTestLedger(t)
	ctx := context.WithValue(context.Background(), common.UserIDKey, "owner")
	ctx = costobserver.ContextWithAttribution(ctx, costledger.Attribution{AttributionVersion: 1, ProductKind: "workflow", ProductID: "workflow-a", ProductName: "Checks", Activity: "scheduled_run", ActorID: "owner"})
	ctx = costobserver.ContextWithSource(ctx, costobserver.Source{ID: "workflow-schedule:opaque", Label: "Daily checks", RunID: "occurrence-a"})
	bo := &BaseOrchestrator{agentMode: "workflow", workspacePath: "Workflow/demo", iterationFolder: "iteration-1-sched"}
	sink := &recordingObserverSink{}
	if err := bo.attachCostObserver(ctx, sink, stageAgentConfig("child-a", "pi-cli", "google/gemini-3.7-flash"), "agent", "execution", "step-a"); err != nil {
		t.Fatal(err)
	}
	event := generationEvent("span-a", 2522, 1730)
	data := event.Data.(*unifiedevents.LLMGenerationEndEvent)
	data.Metadata = map[string]interface{}{"provider": "pi-cli", "cost_usd_estimated": 0.0199488, "cache_read_input_tokens": 154264, "prompt_tokens_include_cache": false}
	observer := sink.costObserver(t)
	for i := 0; i < 2; i++ {
		if err := observer.HandleEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	actual := generationEvent("span-b", 10, 5)
	actual.Data.(*unifiedevents.LLMGenerationEndEvent).Metadata = map[string]interface{}{"provider": "pi-cli", "cost_usd": 0.123, "cost_usd_estimated": 0.42}
	if err := observer.HandleEvent(ctx, actual); err != nil {
		t.Fatal(err)
	}
	summary, err := ledger.Summarize("", "")
	if err != nil {
		t.Fatal(err)
	}
	source := summary.BySource["workflow-schedule:opaque"]
	if source == nil || source.Label != "Daily checks" || source.RunCount != 1 || source.CallCount != 2 {
		t.Fatalf("schedule grouping = %+v", source)
	}
	if math.Abs(summary.Total.TotalCostUSD-0.1429488) > 1e-12 || summary.Total.CallCount != 2 {
		t.Fatalf("repeated event changed totals: %+v", summary.Total)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT billing_basis, pricing_source, product_id, activity FROM cost_events ORDER BY event_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for i, expected := range []string{"token_estimate", "provider_actual"} {
		if !rows.Next() {
			t.Fatalf("missing row %d: %v", i, rows.Err())
		}
		var basis, pricing, product, activity string
		if err := rows.Scan(&basis, &pricing, &product, &activity); err != nil {
			t.Fatal(err)
		}
		wantPricing := "model_registry"
		if expected == "provider_actual" {
			wantPricing = "provider"
		}
		if basis != expected || pricing != wantPricing || product != "workflow-a" || activity != "scheduled_run" {
			t.Fatalf("unexpected ledger identity: %s %s %s %s", basis, pricing, product, activity)
		}
	}
}

// A Pulse reviewer is launched by the workshop's call_generic_agent tool: it
// mints an execution id, carries it on the child context as the background
// agent id, and creates its agent through the same orchestrator factory every
// other agent uses. Four of those ran for 56 minutes and wrote nothing to the
// ledger, because nothing on that path ever attached a cost observer.
func TestAttachCostObserverRecordsBackgroundAgentSpend(t *testing.T) {
	_, dbPath := newTestLedger(t)

	const reviewExecID = "pulse-review-pulse-stores-health-review-2026-0-1785694058048462000"
	const agentSessionID = "workshop-pulse-reviewer-pulse-stores-health-review-2026-0-1785694058048462000-4"

	bo := &BaseOrchestrator{agentMode: "simple", workspacePath: "Workflow/linkedin"}
	ctx := virtualtools.WithBackgroundAgentID(context.Background(), reviewExecID)
	ctx = context.WithValue(ctx, orchEvents.ParentExecutionIDKey, reviewExecID)
	ctx = context.WithValue(ctx, common.UserIDKey, "default")

	sink := &recordingObserverSink{}
	if err := bo.attachCostObserver(
		ctx,
		sink,
		stageAgentConfig(agentSessionID, "codex-cli", "auto"),
		"Background: Autopilot reviewer - stores-health-review",
		"pulse-reviewer-pulse-stores-health-review-1785694058048462000-4",
		"pulse-review:stores-health-review",
	); err != nil {
		t.Fatalf("attachCostObserver() error = %v", err)
	}

	if err := sink.costObserver(t).HandleEvent(ctx, generationEvent("span-1", 1200, 340)); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}

	rows := readLedgerRows(t, dbPath)
	if len(rows) != 1 {
		t.Fatalf("cost_events rows = %d, want 1 — a background agent launch must be recorded", len(rows))
	}
	row := rows[0]
	if row.executionID != reviewExecID {
		t.Errorf("execution_id = %q, want the launch site's execution id %q", row.executionID, reviewExecID)
	}
	if row.scope == costobserver.ScopeUnknown || row.scope == "" {
		t.Errorf("scope = %q — a named launch path must never fall back to unknown", row.scope)
	}
	if row.sessionID != agentSessionID {
		t.Errorf("session_id = %q, want %q", row.sessionID, agentSessionID)
	}
	if row.workflowID != "Workflow/linkedin" {
		t.Errorf("workflow_id = %q, want %q", row.workflowID, "Workflow/linkedin")
	}
	if row.userID != "default" {
		t.Errorf("user_id = %q, want %q", row.userID, "default")
	}
	if row.prompt != 1200 || row.completion != 340 {
		t.Errorf("tokens = (%d, %d), want (1200, 340)", row.prompt, row.completion)
	}
}

// A step agent inside a live workflow run has no background agent id; it
// inherits the run's execution id instead, and the run folder is what marks it
// as execution spend rather than builder spend.
func TestAttachCostObserverRecordsWorkflowStepSpend(t *testing.T) {
	_, dbPath := newTestLedger(t)

	const runExecID = "exec-step-3-1785694058048462000"
	bo := &BaseOrchestrator{
		agentMode:       "simple",
		workspacePath:   "Workflow/linkedin",
		iterationFolder: "iteration-0/default-group",
	}
	ctx := context.WithValue(context.Background(), orchEvents.ParentExecutionIDKey, runExecID)

	sink := &recordingObserverSink{}
	if err := bo.attachCostObserver(
		ctx,
		sink,
		stageAgentConfig("session-group-default-group-1785694058048462000", "anthropic", "claude-sonnet-4-6"),
		"execution-agent-step-3",
		"execution",
		"evaluate-leads",
	); err != nil {
		t.Fatalf("attachCostObserver() error = %v", err)
	}
	if err := sink.costObserver(t).HandleEvent(ctx, generationEvent("span-step", 90, 12)); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}

	rows := readLedgerRows(t, dbPath)
	if len(rows) != 1 {
		t.Fatalf("cost_events rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.scope != costobserver.ScopeWorkflowExecution {
		t.Errorf("scope = %q, want %q — a user-authored step id containing \"eval\" must not decide the scope",
			row.scope, costobserver.ScopeWorkflowExecution)
	}
	if row.executionID != runExecID {
		t.Errorf("execution_id = %q, want %q", row.executionID, runExecID)
	}
	if row.runID != "iteration-0/default-group" {
		t.Errorf("run_id = %q, want %q", row.runID, "iteration-0/default-group")
	}
}

// Builder-side background work (Goal Advisor stages, workshop generic agents)
// runs without a run folder and must not be filed as workflow execution.
func TestAttachCostObserverScopesBuilderBackgroundAgent(t *testing.T) {
	_, dbPath := newTestLedger(t)

	const execID = "generic-agent-refresh-docs-1785694058048462000"
	bo := &BaseOrchestrator{agentMode: "simple", workspacePath: "Workflow/linkedin"}
	ctx := virtualtools.WithBackgroundAgentID(context.Background(), execID)

	sink := &recordingObserverSink{}
	if err := bo.attachCostObserver(
		ctx,
		sink,
		stageAgentConfig("workshop-generic-agent-refresh-docs-1785694058048462000-2", "anthropic", "claude-sonnet-4-6"),
		"Background: Generic agent - refresh-docs",
		"generic-agent-refresh-docs-1785694058048462000-2",
		"generic-agent:refresh-docs",
	); err != nil {
		t.Fatalf("attachCostObserver() error = %v", err)
	}
	if err := sink.costObserver(t).HandleEvent(ctx, generationEvent("span-generic", 42, 7)); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}

	rows := readLedgerRows(t, dbPath)
	if len(rows) != 1 {
		t.Fatalf("cost_events rows = %d, want 1", len(rows))
	}
	if rows[0].scope != costobserver.ScopeBuilder {
		t.Errorf("scope = %q, want %q", rows[0].scope, costobserver.ScopeBuilder)
	}
	if rows[0].executionID != execID {
		t.Errorf("execution_id = %q, want %q", rows[0].executionID, execID)
	}
}

// Processes that never publish a ledger (tests, standalone tools) must still
// be able to create agents.
func TestAttachCostObserverWithoutDefaultLedgerIsNoOp(t *testing.T) {
	costledger.SetDefaultLedger(nil)
	bo := &BaseOrchestrator{agentMode: "simple"}
	sink := &recordingObserverSink{}
	if err := bo.attachCostObserver(context.Background(), sink, stageAgentConfig("s", "anthropic", "m"), "a", "p", "s"); err != nil {
		t.Fatalf("attachCostObserver() error = %v", err)
	}
	if len(sink.observers) != 0 {
		t.Fatalf("observers = %d, want 0 when no ledger is published", len(sink.observers))
	}
}

func TestAgentCostExecutionIDPrefersLaunchSiteIdentity(t *testing.T) {
	config := stageAgentConfig("session-abc", "anthropic", "m")
	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{
			name: "background agent id wins",
			ctx: context.WithValue(
				virtualtools.WithBackgroundAgentID(context.Background(), "bg-1"),
				orchEvents.ParentExecutionIDKey, "parent-1",
			),
			want: "bg-1",
		},
		{
			name: "parent execution id is the fallback",
			ctx:  context.WithValue(context.Background(), orchEvents.ParentExecutionIDKey, "parent-1"),
			want: "parent-1",
		},
		{
			name: "session and step identify an unregistered step agent",
			ctx:  context.Background(),
			want: "session-abc:step-2",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentCostExecutionID(tc.ctx, config, "step-2"); got != tc.want {
				t.Fatalf("agentCostExecutionID() = %q, want %q", got, tc.want)
			}
		})
	}
}
