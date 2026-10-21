package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	unifiedevents "github.com/manishiitg/mcpagent/events"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

// PLAT-702: a Code project's 30-minute heartbeat schedule was recorded as the
// owner's chatting. A product schedule turn is recorded under scope
// "schedule" with its schedule id, name and run; a person's turn stays chat.
func TestProductScheduleTurnRecordsScheduleCostScope(t *testing.T) {
	ledger, err := costledger.NewSQLiteLedger(t.TempDir() + "/costs.sqlite")
	if err != nil {
		t.Fatalf("NewSQLiteLedger() error = %v", err)
	}
	defer ledger.Close()

	job := productScheduleJob{
		Profile:   agentprofiles.Profile{ID: workproduct.ProfileID},
		ProjectID: "orbit",
		Schedule:  productschedule.Schedule{ID: "heartbeat", Name: "Orbit 30-min heartbeat", Isolated: true},
	}
	scheduled := map[string]interface{}{"query": "progress check", "agent_mode": "multi-agent"}
	applyAutomationCostSource(scheduled, job, "run-1")
	workflow := (&SchedulerService{}).buildWorkshopRequest(context.Background(), &ScheduleContext{
		WorkflowID: "project-a", WorkspacePath: "Workflow/project-a",
		Schedule: WorkflowSchedule{ID: "receipt-check", Name: "Receipt check"},
	}, "workflow-run-1")
	if id, _ := workflow["cost_source_id"].(string); strings.Contains(id, "Workflow/project-a") || !strings.HasPrefix(id, "workflow-schedule:") {
		t.Fatalf("source key exposes workspace: %q", id)
	}
	person := map[string]interface{}{"query": "hi", "agent_mode": "multi-agent"}

	record := func(reqMap map[string]interface{}, session string) string {
		// The runner's map reaches handleQuery (or the durable queue) as JSON.
		raw, _ := json.Marshal(reqMap)
		var req QueryRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		scope := chatTurnCostScope(req, req.AgentMode, "")
		calls := 1
		if session == "workflow" {
			calls = 25
		}
		obs := newCostObserver(ledger, session, "owner", req.AgentMode,
			withCostAttribution(scope, "", "", "query-"+session), costobserver.WithSourceFromContext(costSourceContext(context.Background(), req)))
		if err := obs.HandleEvent(context.Background(), &unifiedevents.AgentEvent{
			Type: unifiedevents.LLMGenerationEnd, Timestamp: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC),
			SpanID: "span-" + session, Component: "llm",
			Data: &unifiedevents.LLMGenerationEndEvent{
				UsageMetrics:  unifiedevents.UsageMetrics{PromptTokens: 100, CompletionTokens: 10},
				BaseEventData: unifiedevents.BaseEventData{Metadata: map[string]interface{}{"provider": "agy-cli", "llm_call_count": calls, "llm_call_count_source": "native_model_generations"}},
			},
		}); err != nil {
			t.Fatalf("HandleEvent: %v", err)
		}
		return scope
	}

	if scope := record(scheduled, "sched"); scope != "schedule" {
		t.Fatalf("schedule turn scope = %q, want schedule", scope)
	}
	if scope := record(person, "chat"); scope != "chat" {
		t.Fatalf("person turn scope = %q, want chat", scope)
	}
	if scope := record(requestWithWorkshopMode(workflow, "workshop"), "workflow"); scope != "schedule" {
		t.Fatalf("workflow schedule turn scope = %q, want schedule", scope)
	}

	summary, err := ledger.Summarize("", "")
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if summary.ByScope["schedule"] == nil || summary.ByScope["chat"] == nil {
		t.Fatalf("by_scope = %v, want schedule and chat", summary.ByScope)
	}
	source := summary.BySource[job.ID()]
	if len(summary.BySource) != 2 || source == nil {
		t.Fatalf("by_source = %v, want product and workflow schedules", summary.BySource)
	}
	if source.Label != "Orbit 30-min heartbeat" || source.RunCount != 1 || source.PromptTokens != 100 || source.Scope != "schedule" {
		t.Fatalf("source = %+v", source)
	}
	workflowSource := summary.BySource[workflowScheduleCostSourceID("Workflow/project-a", "receipt-check")]
	if workflowSource == nil || workflowSource.Label != "Receipt check" || workflowSource.RunCount != 1 || workflowSource.Scope != "schedule" || workflowSource.CallCount != 25 {
		t.Fatalf("workflow schedule source = %+v", workflowSource)
	}
	var pulseReq QueryRequest
	raw, _ := json.Marshal(workflow)
	if err := json.Unmarshal(raw, &pulseReq); err != nil {
		t.Fatal(err)
	}
	pulseReq.PulseLifecycleTurn = true
	if got := chatTurnCostScope(pulseReq, pulseReq.AgentMode, ""); got != "pulse" {
		t.Fatalf("scheduled Pulse scope = %q, want pulse", got)
	}
}
