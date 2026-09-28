package costobserver

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	unifiedevents "github.com/manishiitg/mcpagent/events"
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
		{"chat", "post_run_monitor", ScopePulse},
		{"chat", "pulse-fixer", ScopePulse},
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
			name:        "pulse reviewer stage",
			agentMode:   "simple",
			identifiers: []string{"pulse-reviewer-stores-health-1785", "Background: Pulse reviewer - stores-health", ""},
			want:        ScopePulse,
		},
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
