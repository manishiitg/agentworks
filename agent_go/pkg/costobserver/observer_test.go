package costobserver

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	_ "modernc.org/sqlite"
)

func TestObserverRecordsOnlyRealInProcessMCPActivity(t *testing.T) {
	ledger, err := costledger.NewSQLiteLedger(t.TempDir() + "/costs.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	obs := New(ledger, "session-1", "bot-slack-route", "chat",
		WithModel("openai", "gpt"),
		WithAttribution(ScopeChat, "Workflow/demo", "", "execution-1"),
		WithSourcePlatform("slack"),
	)
	now := time.Now().UTC()
	for _, event := range []*unifiedevents.AgentEvent{
		{Type: unifiedevents.ToolCallEnd, Timestamp: now, Data: &unifiedevents.ToolCallEndEvent{ToolName: "query", ServerName: "github", ToolCallID: "one"}},
		{Type: unifiedevents.ToolCallError, Timestamp: now, Data: &unifiedevents.ToolCallErrorEvent{ToolName: "query", ServerName: "github", ToolCallID: "two"}},
		{Type: unifiedevents.ToolCallEnd, Timestamp: now, Data: &unifiedevents.ToolCallEndEvent{ToolName: "get_api_spec", ServerName: "github", ToolCallID: "virtual"}},
		{Type: unifiedevents.ToolCallEnd, Timestamp: now, Data: &unifiedevents.ToolCallEndEvent{ToolName: "query", ServerName: "custom", ToolCallID: "custom"}},
		{Type: unifiedevents.ToolCallEnd, Timestamp: now, Data: &unifiedevents.ToolCallEndEvent{ToolName: "query", ServerName: "github", ToolCallID: "settled", SyntheticSettle: true}},
	} {
		if err := obs.HandleEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	cli := New(ledger, "session-2", "bot-slack-route", "chat",
		WithModel("codex-cli", "gpt"), WithAttribution(ScopeChat, "Workflow/demo", "", "execution-2"))
	if err := cli.HandleEvent(context.Background(), &unifiedevents.AgentEvent{
		Type: unifiedevents.ToolCallEnd, Timestamp: now,
		Data: &unifiedevents.ToolCallEndEvent{ToolName: "query", ServerName: "github", ToolCallID: "bridge-owned"},
	}); err != nil {
		t.Fatal(err)
	}
	summary, err := ledger.Summarize("", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := summary.ByWorkflowMCP["Workflow/demo"]["github"].Calls; got != 2 {
		t.Fatalf("MCP calls = %d, want 2", got)
	}
	if summary.Total.CallCount != 0 || summary.Total.TotalCostUSD != 0 {
		t.Fatalf("MCP activity changed LLM calls/cost: %+v", summary.Total)
	}
	if got := summary.ByWorkflowBot["Workflow/demo"]["slack\x00bot-slack-route"].AccountingEventCount; got != 2 {
		t.Fatalf("bot accounting events = %d, want 2", got)
	}
	ctx := ContextWithSourcePlatform(context.Background(), "WhatsApp")
	if got := SourcePlatformFromContext(ctx); got != "whatsapp" {
		t.Fatalf("source platform = %q, want whatsapp", got)
	}
}

func TestSubscriptionDollarTelemetryRemainsShadowUsage(t *testing.T) {
	dbPath := t.TempDir() + "/costs.sqlite"
	ledger, err := costledger.NewSQLiteLedger(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	for _, legacy := range []bool{false, true} {
		obs := New(ledger, fmt.Sprintf("subscription-%v", legacy), "owner", "chat", WithModel("claude-code", "claude-opus-5"), WithAttribution(ScopeChat, "", "", "turn"))
		metadata := map[string]interface{}{"provider": "claude-code", "cost_usd": 0.123, "cost_usd_estimated": 0.00075}
		event := &unifiedevents.AgentEvent{Type: unifiedevents.LLMGenerationEnd, Timestamp: time.Now(), SpanID: "native-cost", Data: &unifiedevents.LLMGenerationEndEvent{UsageMetrics: unifiedevents.UsageMetrics{PromptTokens: 100, CompletionTokens: 10}, BaseEventData: unifiedevents.BaseEventData{Metadata: metadata}}}
		if legacy {
			event.Type = unifiedevents.TokenUsage
			event.Data = &unifiedevents.TokenUsageEvent{Provider: "claude-code", ModelID: "claude-opus-5", GenerationInfo: metadata}
		}
		if err := obs.HandleEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := ledger.Summarize("", "")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Total.TotalCostUSD != 0.246 {
		t.Fatalf("usage totals changed: %+v", rows.Total)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var shadows int
	if err := db.QueryRow(`SELECT count(*) FROM cost_events WHERE billing_basis='subscription_shadow' AND pricing_source='provider'`).Scan(&shadows); err != nil {
		t.Fatal(err)
	}
	if shadows != 2 {
		t.Fatalf("shadow rows = %d, want 2", shadows)
	}
}

func TestNewWarnsWhenLaunchPathCannotNameItsScope(t *testing.T) {
	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	defer log.SetOutput(previous)

	observer := New(nil, "sess-1", "user-1", "simple",
		WithAttribution("", "Workflow/demo", "", "exec-1"),
		WithLaunchPath("somePackage.someLaunchPath"),
	)

	if observer.Scope() != ScopeUnknown {
		t.Fatalf("Scope() = %q, want %q", observer.Scope(), ScopeUnknown)
	}
	output := logged.String()
	if !strings.Contains(output, "somePackage.someLaunchPath") {
		t.Fatalf("an unattributed observer must name its launch path in the log, got: %q", output)
	}
	if !strings.Contains(output, "did not name a cost scope") {
		t.Fatalf("missing unattributed-scope warning, got: %q", output)
	}
}

func TestNewDoesNotWarnForAnAttributedObserver(t *testing.T) {
	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	defer log.SetOutput(previous)

	observer := New(nil, "sess-1", "user-1", "simple",
		WithAttribution(ScopePulse, "Workflow/demo", "", "pulse-review-1"),
		WithLaunchPath("somePackage.someLaunchPath"),
	)
	if observer.Scope() != ScopePulse {
		t.Fatalf("Scope() = %q, want %q", observer.Scope(), ScopePulse)
	}
	if observer.ExecutionID() != "pulse-review-1" {
		t.Fatalf("ExecutionID() = %q, want %q", observer.ExecutionID(), "pulse-review-1")
	}
	if logged.Len() != 0 {
		t.Fatalf("attributed observer logged %q", logged.String())
	}
}

func TestInferScope(t *testing.T) {
	tests := []struct {
		agentMode string
		phaseID   string
		want      string
	}{
		{"chat", "", ScopeChat},
		{"simple", "", ScopeChat},
		{"multi-agent", "", ScopeChat},
		{"workflow", "", ScopeBuilder},
	}
	for _, tc := range tests {
		if got := InferScope(tc.agentMode, tc.phaseID); got != tc.want {
			t.Errorf("InferScope(%q, %q) = %q, want %q", tc.agentMode, tc.phaseID, got, tc.want)
		}
	}
}

func TestInferWorkflowScope(t *testing.T) {
	tests := []struct {
		name         string
		agentMode    string
		hasRunFolder bool
		identifiers  []string
		want         string
	}{
		{
			name:         "live workflow step",
			agentMode:    "simple",
			hasRunFolder: true,
			identifiers:  []string{"execution", "execution-agent-step-1", "iteration-0/default"},
			want:         ScopeWorkflowExecution,
		},
		{
			name:        "builder-side background stage",
			agentMode:   "simple",
			identifiers: []string{"generic-agent-refresh-docs-1785", "Background: Generic agent - refresh-docs", ""},
			want:        ScopeBuilder,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := InferWorkflowScope(tc.agentMode, tc.hasRunFolder, tc.identifiers...)
			if got != tc.want {
				t.Fatalf("InferWorkflowScope() = %q, want %q", got, tc.want)
			}
			if got == ScopeUnknown {
				t.Fatalf("InferWorkflowScope() must always name a scope")
			}
		})
	}
}

func TestObserverPersistsInputCacheConvention(t *testing.T) {
	for _, includes := range []bool{true, false} {
		t.Run(map[bool]string{true: "inclusive", false: "exclusive"}[includes], func(t *testing.T) {
			ledger, err := costledger.NewSQLiteLedger(t.TempDir() + "/costs.sqlite")
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			obs := New(ledger, "session-input", "alice", "chat", WithModel("codex-cli", "gpt"))
			generation := &unifiedevents.LLMGenerationEndEvent{
				UsageMetrics: unifiedevents.UsageMetrics{PromptTokens: 100, CompletionTokens: 10, CacheTokens: 80},
			}
			generation.Metadata = map[string]interface{}{"prompt_tokens_include_cache": includes, "cost_usd": 0.25}
			if err := obs.HandleEvent(context.Background(), &unifiedevents.AgentEvent{Type: unifiedevents.LLMGenerationEnd, Timestamp: time.Now().UTC(), SpanID: "call", Data: generation}); err != nil {
				t.Fatal(err)
			}
			summary, err := ledger.Summarize("", "")
			if err != nil {
				t.Fatal(err)
			}
			want := 180
			if includes {
				want = 100
			}
			if summary.Total.InputTokens != want || summary.Total.PromptTokens != 100 || summary.Total.TotalCostUSD != 0.25 {
				t.Fatalf("reporting convention lost: %+v, want input %d", summary.Total, want)
			}
		})
	}
}

// A chat turn and two follow-ups typed into the running terminal (each reported as its own generation-end event, with
// no turn number, text or duration) are three ledger rows for the same person, so the Costs page and the token limits
// that read the ledger count them all.
func TestObserverRecordsEachReportedRunOfLiveInputTurnsForTheSamePerson(t *testing.T) {
	dbPath := t.TempDir() + "/costs.sqlite"
	ledger, err := costledger.NewSQLiteLedger(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	obs := New(ledger, "code:project:demo", "alice", "chat",
		WithModel("muse-cli", "muse-spark-1.3-contributor"),
		WithAttribution(ScopeChat, "Chats/Code/projects/demo", "", "execution-1"),
	)
	now := time.Now().UTC()
	end := func(offset time.Duration, prompt, completion int, duration time.Duration) *unifiedevents.AgentEvent {
		data := &unifiedevents.LLMGenerationEndEvent{
			BaseEventData: unifiedevents.BaseEventData{Timestamp: now.Add(offset), EventID: unifiedevents.GenerateEventID()},
			Duration:      duration,
			UsageMetrics:  unifiedevents.UsageMetrics{PromptTokens: prompt, CompletionTokens: completion, TotalTokens: prompt + completion},
		}
		if offset > 0 { // the follow-ups come from the CLI's own transcript and name the run they came from
			data.Metadata = map[string]interface{}{"usage_source": "retained", "usage_source_id": fmt.Sprintf("run-%d#1-2", prompt)}
		}
		return &unifiedevents.AgentEvent{Type: unifiedevents.LLMGenerationEnd, Timestamp: now.Add(offset), SessionID: "code:project:demo", Data: data}
	}
	for i, event := range []*unifiedevents.AgentEvent{
		end(0, 27299, 475, 27*time.Second),
		end(16*time.Minute, 27913, 924, 0),
		end(19*time.Minute, 33938, 847, 0),
	} {
		if i == 2 { // the last follow-up was sent by another person's agent
			SetRetainedInputSource("code:project:demo", "agent_message", "From another agent")
			defer SetRetainedInputSource("code:project:demo", "", "")
		}
		if err := obs.HandleEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := ledger.Summarize("", "")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total.CallCount != 3 || summary.Total.PromptTokens != 27299+27913+33938 || summary.Total.CompletionTokens != 475+924+847 {
		t.Fatalf("a turn and two live-input follow-ups must be three rows, got %+v", summary.Total)
	}
	// The ledger keeps the source of a usage row in operation_metadata_json (read-backs expose only cache semantics).
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var traced int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cost_events WHERE json_extract(operation_metadata_json, '$.usage_source') = 'retained' AND json_extract(operation_metadata_json, '$.usage_source_id') LIKE 'run-%'`).Scan(&traced); err != nil {
		t.Fatal(err)
	}
	if traced != 2 {
		t.Fatalf("the two follow-up rows must name the CLI run they came from, %d did", traced)
	}
	var sent int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cost_events WHERE json_extract(operation_metadata_json, '$.triggered_by') = 'agent_message' AND source_label = 'From another agent' AND user_id = 'alice'`).Scan(&sent); err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("the follow-up another agent sent must name its sender and stay on the chat owner, %d rows did", sent)
	}
}
